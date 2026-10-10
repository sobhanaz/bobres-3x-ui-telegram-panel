package broadcast

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/settings"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	"google.golang.org/grpc"
)

// fakeCore hands out batches and records reports.
type fakeCore struct {
	mu      sync.Mutex
	batches []*corev1.BroadcastBatch
	reports []*corev1.ReportBroadcastResultsRequest
	ctxErrs []error            // each report's context error
	status  func(n int) string // the status after the n-th report (nil: running)
}

func (f *fakeCore) ClaimBroadcastBatch(_ context.Context, in *corev1.ClaimBroadcastBatchRequest, _ ...grpc.CallOption) (*corev1.BroadcastBatch, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if in.GetLimit() != batchSize {
		return nil, fmt.Errorf("claimed %d", in.GetLimit())
	}
	if len(f.batches) == 0 {
		return &corev1.BroadcastBatch{}, nil
	}
	b := f.batches[0]
	f.batches = f.batches[1:]
	return b, nil
}

func (f *fakeCore) ReportBroadcastResults(ctx context.Context, in *corev1.ReportBroadcastResultsRequest, _ ...grpc.CallOption) (*corev1.BroadcastState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reports = append(f.reports, in)
	f.ctxErrs = append(f.ctxErrs, ctx.Err())
	st := "running"
	if f.status != nil {
		st = f.status(len(f.reports))
	}
	return &corev1.BroadcastState{Status: st}, nil
}

// results are the reported results by user; each user must be reported
// exactly once.
func (f *fakeCore) results(t *testing.T) map[string]*corev1.BroadcastResult {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]*corev1.BroadcastResult{}
	for _, r := range f.reports {
		for _, res := range r.GetResults() {
			if _, dup := out[res.GetUserId()]; dup {
				t.Errorf("%s reported twice", res.GetUserId())
			}
			out[res.GetUserId()] = res
		}
	}
	return out
}

func (f *fakeCore) halt() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.reports {
		if r.GetHaltReason() != "" {
			return r.GetHaltReason()
		}
	}
	return ""
}

func count(results map[string]*corev1.BroadcastResult, o corev1.BroadcastOutcome) int {
	n := 0
	for _, r := range results {
		if r.GetOutcome() == o {
			n++
		}
	}
	return n
}

type sentMsg struct {
	chat int64
	text string
	kb   *tg.Keyboard
	at   time.Time
}

// fakeSender answers each chat as planned (errors in order, then success).
type fakeSender struct {
	mu          sync.Mutex
	plan        map[int64][]error
	always      error // every call fails with it (unless planned)
	delay       time.Duration
	sent        []sentMsg
	calls       map[int64]int
	inFlight    int
	maxInFlight int
	next        int
}

func newSender() *fakeSender { return &fakeSender{plan: map[int64][]error{}, calls: map[int64]int{}} }

func (s *fakeSender) SendMessage(ctx context.Context, chat int64, text string, kb *tg.Keyboard) (*tg.Message, error) {
	s.mu.Lock()
	s.inFlight++
	s.maxInFlight = max(s.maxInFlight, s.inFlight)
	s.calls[chat]++
	err := s.always
	if q := s.plan[chat]; len(q) > 0 {
		err, s.plan[chat] = q[0], q[1:]
	}
	delay := s.delay
	s.mu.Unlock()
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			err = ctx.Err()
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inFlight--
	if err != nil {
		return nil, err
	}
	s.next++
	s.sent = append(s.sent, sentMsg{chat: chat, text: text, kb: kb, at: time.Now()})
	return &tg.Message{MessageID: 1000 + s.next}, nil
}

func (s *fakeSender) messages() []sentMsg {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]sentMsg(nil), s.sent...)
}

func (s *fakeSender) callsTo(chat int64) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[chat]
}

// batch has n recipients: Telegram ids 1..n, users u1..un, in lang.
func batch(n int, lang string) *corev1.BroadcastBatch {
	b := &corev1.BroadcastBatch{BroadcastId: "bc-1", Texts: map[string]string{"fa": "<b>سلام</b>", "en": "<b>Hello</b> &amp; welcome"}}
	for i := 1; i <= n; i++ {
		b.Recipients = append(b.Recipients, &corev1.BroadcastRecipient{UserId: fmt.Sprintf("u%d", i), TelegramId: int64(i), Language: lang})
	}
	return b
}

func newWorker(core Core, s Sender, perSec int) (*Worker, *tg.Flood) {
	set := settings.New()
	set.Replace(map[string]string{"limits.broadcast_per_sec": fmt.Sprint(perSec)})
	flood := &tg.Flood{}
	return New(core, s, set, flood, &tg.Meter{}, nil), flood
}

func apiErr(code int, desc string) error { return &tg.APIError{Code: code, Description: desc} }

func TestOutcomes(t *testing.T) {
	core := &fakeCore{batches: []*corev1.BroadcastBatch{batch(6, "en")}}
	s := newSender()
	s.plan[2] = []error{apiErr(403, "Forbidden: bot was blocked by the user")}
	s.plan[3] = []error{apiErr(400, "Bad Request: chat not found")}
	s.plan[4] = []error{apiErr(400, "Bad Request: message is too long")}
	s.plan[5] = []error{errors.New("telegram: sendMessage: connection reset by peer")}
	s.plan[6] = []error{apiErr(502, "Bad Gateway")}
	w, _ := newWorker(core, s, 25)
	n, err := w.Step(context.Background())
	if err != nil || n != 6 {
		t.Fatalf("step: %d %v", n, err)
	}
	res := core.results(t)
	want := map[string]corev1.BroadcastOutcome{"u1": outSent, "u2": outBlocked, "u3": outBlocked, "u4": outFailed,
		"u5": outUnknown, "u6": outUnknown}
	for u, o := range want {
		if res[u].GetOutcome() != o {
			t.Errorf("%s: %v, want %v", u, res[u].GetOutcome(), o)
		}
	}
	if res["u1"].GetMessageId() != 1001 || res["u4"].GetError() != "Bad Request: message is too long" ||
		!strings.Contains(res["u5"].GetError(), "connection reset") {
		t.Fatalf("details: %v %v %v", res["u1"], res["u4"], res["u5"])
	}
	if core.halt() != "" {
		t.Fatalf("halted: %s", core.halt())
	}
	if ms := s.messages(); len(ms) != 1 || ms[0].text != "<b>Hello</b> &amp; welcome" || ms[0].kb != nil {
		t.Fatalf("messages: %+v", ms)
	}
	if n, err := w.Step(context.Background()); n != 0 || err != nil {
		t.Fatalf("nothing left: %d %v", n, err)
	}
}

// A 429 waits for the flood to pass and sends the same recipient once.
func TestFloodRetriesTheSameRecipient(t *testing.T) {
	core := &fakeCore{batches: []*corev1.BroadcastBatch{batch(3, "en")}}
	s := newSender()
	s.plan[1] = []error{&tg.APIError{Code: 429, Description: "Too Many Requests: retry after 1", RetryAfter: 1}}
	w, flood := newWorker(core, s, 25)
	start := time.Now()
	if _, err := w.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) < 900*time.Millisecond || flood.Until().IsZero() {
		t.Fatalf("did not wait for the flood: %v", time.Since(start))
	}
	got := 0
	for _, m := range s.messages() {
		if m.chat == 1 {
			got++
		}
	}
	res := core.results(t)
	if got != 1 || s.callsTo(1) != 2 || res["u1"].GetOutcome() != outSent || count(res, outSent) != 3 {
		t.Fatalf("chat 1 got %d messages in %d calls; results %v", got, s.callsTo(1), res)
	}
}

// Staff pause the broadcast while a batch is sending: the bot stops at the
// next report and hands back who did not get it yet.
func TestPauseMidBatchReleasesTheRest(t *testing.T) {
	core := &fakeCore{batches: []*corev1.BroadcastBatch{batch(25, "en")}, status: func(int) string { return "paused" }}
	s := newSender()
	w, _ := newWorker(core, s, 5)
	w.FlushEvery = 300 * time.Millisecond
	if _, err := w.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	res := core.results(t)
	sent, released := count(res, outSent), count(res, outReleased)
	if len(res) != 25 || sent+released != 25 || released < 15 || sent != len(s.messages()) {
		t.Fatalf("%d results: %d sent, %d released, %d messages", len(res), sent, released, len(s.messages()))
	}
}

func TestSameRefusalsHalt(t *testing.T) {
	core := &fakeCore{batches: []*corev1.BroadcastBatch{batch(12, "en")}}
	s := newSender()
	s.always = apiErr(400, "Bad Request: BUTTON_URL_INVALID")
	w, _ := newWorker(core, s, 25)
	if _, err := w.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	res := core.results(t)
	failed := count(res, outFailed)
	if !strings.Contains(core.halt(), "refused 5 messages in a row") || failed < 5 || failed > 5+maxInFlight-1 ||
		failed+count(res, outReleased) != 12 {
		t.Fatalf("halt %q: %d failed of %v", core.halt(), failed, res)
	}

	// Different refusals are not a pattern.
	core = &fakeCore{batches: []*corev1.BroadcastBatch{batch(8, "en")}}
	s = newSender()
	for i := int64(1); i <= 8; i++ {
		s.plan[i] = []error{apiErr(400, fmt.Sprintf("Bad Request: problem %d", i%2))}
	}
	w, _ = newWorker(core, s, 25)
	if _, err := w.Step(context.Background()); err != nil || core.halt() != "" || count(core.results(t), outFailed) != 8 {
		t.Fatalf("mixed refusals: %v %q", err, core.halt())
	}
}

func TestUnansweredSendsHalt(t *testing.T) {
	core := &fakeCore{batches: []*corev1.BroadcastBatch{batch(10, "en")}}
	s := newSender()
	s.always = errors.New("telegram: sendMessage: context deadline exceeded")
	w, _ := newWorker(core, s, 25)
	if _, err := w.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	res := core.results(t)
	unknown := count(res, outUnknown)
	if !strings.Contains(core.halt(), "did not answer 3 sends in a row") || unknown < 3 || unknown > 3+maxInFlight-1 ||
		unknown+count(res, outReleased) != 10 {
		t.Fatalf("halt %q: %d unknown of %v", core.halt(), unknown, res)
	}
}

func TestRevokedTokenHalts(t *testing.T) {
	core := &fakeCore{batches: []*corev1.BroadcastBatch{batch(6, "en")}}
	s := newSender()
	s.always = apiErr(401, "Unauthorized")
	w, _ := newWorker(core, s, 25)
	if _, err := w.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	res := core.results(t)
	if !strings.Contains(core.halt(), "token") || count(res, outReleased) != 6 || s.callsTo(5) != 0 {
		t.Fatalf("halt %q: %v", core.halt(), res)
	}
}

// At shutdown the sends in flight finish; the others go back in line, and
// the report is made on a context that is not cancelled.
func TestShutdownReleasesTheRest(t *testing.T) {
	core := &fakeCore{batches: []*corev1.BroadcastBatch{batch(10, "en")}}
	s := newSender()
	s.delay = 300 * time.Millisecond
	w, _ := newWorker(core, s, 25)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	if _, err := w.Step(ctx); err != nil {
		t.Fatal(err)
	}
	res := core.results(t)
	sent := count(res, outSent)
	if sent == 0 || sent > maxInFlight || sent != len(s.messages()) || sent+count(res, outReleased) != 10 {
		t.Fatalf("%d sent, %d messages: %v", sent, len(s.messages()), res)
	}
	for i, err := range core.ctxErrs {
		if err != nil {
			t.Fatalf("report %d on a cancelled context", i)
		}
	}
	if n, err := w.Step(ctx); n != 0 || err != nil {
		t.Fatalf("after shutdown: %d %v", n, err)
	}
}

// The pace never goes over the rate; interactive messages lower it.
func TestPacing(t *testing.T) {
	core := &fakeCore{batches: []*corev1.BroadcastBatch{batch(25, "en")}}
	s := newSender()
	w, _ := newWorker(core, s, 20)
	start := time.Now()
	if _, err := w.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) < 24*45*time.Millisecond {
		t.Fatalf("25 messages at 20/s took %v", time.Since(start))
	}
	ms := s.messages()
	for i := range ms {
		n := 0
		for _, m := range ms[i:] {
			if m.at.Sub(ms[i].at) < time.Second {
				n++
			}
		}
		if n > 21 {
			t.Fatalf("%d messages within a second of message %d", n, i)
		}
	}
	if s.maxInFlight > maxInFlight {
		t.Fatalf("%d sends in flight", s.maxInFlight)
	}

	if budget(20, 0) != 20 || budget(20, 10) != 10 || budget(25, 3) != 22 || budget(40, 0) != maxTotalPerSec || budget(5, 5) != 0 {
		t.Fatal("budget")
	}
	// 15 interactive messages in the last second leave 5 a second of 20.
	m := &tg.Meter{}
	for range 15 {
		m.Add()
	}
	p := &pacer{rate: 20, flood: &tg.Flood{}, meter: m}
	start = time.Now()
	for range 2 {
		if err := p.wait(context.Background(), nil); err != nil {
			t.Fatal(err)
		}
	}
	if d := time.Since(start); d < 180*time.Millisecond {
		t.Fatalf("two sends %v apart at 5 a second", d)
	}
	m = &tg.Meter{}
	for range 20 {
		m.Add()
	}
	p = &pacer{rate: 20, flood: &tg.Flood{}, meter: m}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if err := p.wait(ctx, nil); err == nil {
		t.Fatal("sent while interactive replies used the whole second")
	}
}

// A recipient gets their language, or the other one; the button's label too.
func TestLanguageFallback(t *testing.T) {
	b := batch(2, "fa")
	b.Recipients[1].Language = "en"
	b.Texts = map[string]string{"en": "<b>Hello</b>"}
	b.ButtonUrl, b.ButtonLabels = "https://example.com/offer", map[string]string{"en": "Open"}
	b2 := batch(2, "fa")
	b2.BroadcastId = "bc-2"
	b2.Recipients[0].TelegramId, b2.Recipients[1].TelegramId = 3, 4
	b2.Recipients[1].Language = "en"
	b2.ButtonUrl, b2.ButtonLabels = "https://example.com/x", map[string]string{"fa": "باز کن", "en": "Open"}
	core := &fakeCore{batches: []*corev1.BroadcastBatch{b, b2}}
	s := newSender()
	w, _ := newWorker(core, s, 25)
	for range 2 {
		if _, err := w.Step(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	got := map[int64]sentMsg{}
	for _, m := range s.messages() {
		got[m.chat] = m
	}
	label := func(m sentMsg) string { return m.kb.InlineKeyboard[0][0].Text + " " + m.kb.InlineKeyboard[0][0].URL }
	if got[1].text != "<b>Hello</b>" || label(got[1]) != "Open https://example.com/offer" {
		t.Fatalf("fa recipient of an English-only broadcast: %+v", got[1])
	}
	if got[3].text != "<b>سلام</b>" || label(got[3]) != "باز کن https://example.com/x" || got[4].text != "<b>Hello</b> &amp; welcome" ||
		label(got[4]) != "Open https://example.com/x" {
		t.Fatalf("both languages: %+v %+v", got[3], got[4])
	}
}

// What core should have refused halts the broadcast before anything is sent.
func TestBrokenBroadcastHalts(t *testing.T) {
	for name, edit := range map[string]func(*corev1.BroadcastBatch){
		"unclosed tag": func(b *corev1.BroadcastBatch) { b.Texts["en"] = "<b>Hello" },
		"too long":     func(b *corev1.BroadcastBatch) { b.Texts["fa"] = strings.Repeat("ب", 4097) },
		"no text":      func(b *corev1.BroadcastBatch) { b.Texts = nil },
		"http link": func(b *corev1.BroadcastBatch) {
			b.ButtonUrl, b.ButtonLabels = "http://example.com", map[string]string{"en": "Go"}
		},
		"no label": func(b *corev1.BroadcastBatch) { b.ButtonUrl = "https://example.com" },
		"bad label": func(b *corev1.BroadcastBatch) {
			b.ButtonUrl, b.ButtonLabels = "https://example.com", map[string]string{"en": "<b>Go</b>"}
		},
	} {
		b := batch(3, "en")
		edit(b)
		core := &fakeCore{batches: []*corev1.BroadcastBatch{b}}
		s := newSender()
		w, _ := newWorker(core, s, 25)
		if _, err := w.Step(context.Background()); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if core.halt() == "" || count(core.results(t), outReleased) != 3 || len(s.messages()) != 0 {
			t.Errorf("%s: halt %q, %d sent", name, core.halt(), len(s.messages()))
		}
	}
}

func TestProblem(t *testing.T) {
	for _, c := range []struct {
		text, url, label string
		bad              bool
	}{
		{"<b>Hi</b> {name}", "", "", false},
		{"Hi", "https://example.com", "Open", false},
		{"<i>Hi", "", "", true},
		{"", "", "", true},
		{"Hi", "https://example.com", "", true},
		{"Hi", "javascript:alert(1)", "Open", true},
		{"Hi", "", "Open", true},
	} {
		if p := Problem(c.text, c.url, c.label); (p != "") != c.bad {
			t.Errorf("%q %q %q: %q", c.text, c.url, c.label, p)
		}
	}
	if HTTPSURL("https://user:pw@example.com") || HTTPSURL("https://example.com/"+strings.Repeat("a", 1024)) || !HTTPSURL("https://t.me/x") {
		t.Fatal("HTTPSURL")
	}
}

// Run sends batch after batch and stops with its context.
func TestRunUntilCancelled(t *testing.T) {
	b2 := batch(2, "en")
	b2.Recipients[0].TelegramId, b2.Recipients[1].TelegramId = 3, 4
	core := &fakeCore{batches: []*corev1.BroadcastBatch{batch(2, "en"), b2}}
	s := newSender()
	w, _ := newWorker(core, s, 25)
	w.Idle = 20 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	deadline := time.Now().Add(5 * time.Second)
	for len(s.messages()) < 4 {
		if time.Now().After(deadline) {
			t.Fatalf("sent %d of 4", len(s.messages()))
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
