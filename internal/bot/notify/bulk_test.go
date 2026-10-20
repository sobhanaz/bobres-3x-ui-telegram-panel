package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	commonv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/common/v1"
	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/settings"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/eventbus"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	enBuyer = 501 // a customer who reads English
	faBuyer = 502 // and one who reads Persian
)

// bulkCore answers what the delivery of a bulk purchase asks core.
type bulkCore struct {
	fakeCore
	mu      sync.Mutex
	bulk    *corev1.BulkOrder
	err     error
	asked   []string
	blocked []int64
}

func (c *bulkCore) GetBulkOrder(_ context.Context, in *corev1.GetBulkOrderRequest, _ ...grpc.CallOption) (*corev1.BulkOrder, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.asked = append(c.asked, in.GetBulkId()+"/"+in.GetUserId())
	return c.bulk, c.err
}

func (c *bulkCore) SetUserBotBlocked(_ context.Context, in *corev1.SetUserBotBlockedRequest, _ ...grpc.CallOption) (*corev1.SetUserBotBlockedResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.blocked = append(c.blocked, in.GetTelegramId())
	return &corev1.SetUserBotBlockedResponse{}, nil
}

func (c *bulkCore) ListPlans(context.Context, *corev1.ListPlansRequest, ...grpc.CallOption) (*corev1.ListPlansResponse, error) {
	return &corev1.ListPlansResponse{Plans: []*corev1.Plan{{Id: "plan-1", NameI18N: map[string]string{"en": "Monthly 50GB", "fa": "ماهانه"}}}}, nil
}

// uploadTG is a Telegram that can also take a file, and keeps the files.
type uploadTG struct {
	*fakeTG
	mu    sync.Mutex
	files []upload
	fail  error // the upload fails with this
}

type upload struct {
	chat    int64
	name    string
	body    string
	caption string
}

func (u *uploadTG) SendDocumentUpload(_ context.Context, chatID int64, name string, r io.Reader, size int64, caption string) (*tg.Message, error) {
	b, _ := io.ReadAll(r)
	if int64(len(b)) != size {
		return nil, fmt.Errorf("size %d, told %d", len(b), size)
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.fail != nil {
		return nil, u.fail
	}
	u.files = append(u.files, upload{chatID, name, string(b), caption})
	return &tg.Message{MessageID: 99}, nil
}

// bulkOf makes a bulk of n services named "<prefix> <i>" ("" : unnamed, with a
// service id) with links; ready says which are delivered.
func bulkOf(n int, prefix string, ready func(i int) bool) (*corev1.BulkOrder, []string) {
	b := &corev1.BulkOrder{Id: "bbbbbbbb-0000-7000-8000-00000000b001", UserId: "user-1", PlanId: "plan-1", Quantity: int32(n), //nolint:gosec // a small test count
		Total: &commonv1.Money{Amount: int64(n) * 1000, Currency: "IRT"}}
	var readyOrders []string
	for i := 1; i <= n; i++ {
		sv := &corev1.BulkService{Index: int32(i), OrderId: fmt.Sprintf("order-%03d", i), OrderStatus: "paid", //nolint:gosec // a small test count
			SubscriptionId: fmt.Sprintf("00000000-0000-7000-8000-%012d", i)}
		if prefix != "" {
			sv.Label = fmt.Sprintf("%s %d", prefix, i)
		}
		if ready(i) {
			sv.OrderStatus, sv.Status = "active", "active"
			sv.SubscriptionLink = fmt.Sprintf("https://sub.example.com/sub/token%03d", i)
			readyOrders = append(readyOrders, sv.OrderId)
		} else {
			sv.OrderStatus = "provision_failed"
		}
		b.Services = append(b.Services, sv)
	}
	return b, readyOrders
}

func allReady(int) bool { return true }

func readyEvent(b *corev1.BulkOrder, tgID int64, readyOrders []string) events.BulkReadyEvent {
	return events.BulkReadyEvent{BulkID: b.GetId(), UserID: b.GetUserId(), TelegramID: tgID, PlanID: b.GetPlanId(), Quantity: int(b.GetQuantity()),
		ReadyOrders: readyOrders, Delayed: int(b.GetQuantity()) - len(readyOrders), Amount: b.GetTotal().GetAmount(), Currency: "IRT"}
}

func bulkNotifier(t *testing.T, b *corev1.BulkOrder, upload bool) (*Notifier, *bulkCore, *fakeTG, *uploadTG) {
	t.Helper()
	cat, err := i18n.Load()
	if err != nil {
		t.Fatal(err)
	}
	core := &bulkCore{fakeCore: fakeCore{langs: map[int64]string{enBuyer: "en", faBuyer: "fa", ownerTG: "en"}}, bulk: b}
	base := &fakeTG{}
	var tgIface Telegram = base
	var up *uploadTG
	if upload {
		up = &uploadTG{fakeTG: base}
		tgIface = up
	}
	return New(core, tgIface, cat, settings.New(), []int64{ownerTG}, nil), core, base, up
}

func handleEvent(t *testing.T, n *Notifier, topic string, e any) error {
	t.Helper()
	payload, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	return n.Handle(context.Background(), eventbus.Message{Topic: topic, Payload: payload})
}

// Every service delivered: ONE message with the header, each service named and
// its link in a code span (tap to copy), and one button, My services; no file.
func TestBulkDeliveryIsOneMessageWithEveryLink(t *testing.T) {
	b, ready := bulkOf(3, "Family", allReady)
	n, core, tgf, up := bulkNotifier(t, b, true)
	if err := handleEvent(t, n, events.BulkReady, readyEvent(b, enBuyer, ready)); err != nil {
		t.Fatal(err)
	}
	sent := tgf.take()
	if len(sent) != 1 || sent[0].chat != enBuyer || len(up.files) != 0 {
		t.Fatalf("%d messages, %d files", len(sent), len(up.files))
	}
	m := sent[0]
	for _, want := range []string{"<b>Your 3 services are ready!</b>", "Tap a link to copy it",
		"1. <b>Family 1</b>\n<code>https://sub.example.com/sub/token001</code>",
		"2. <b>Family 2</b>\n<code>https://sub.example.com/sub/token002</code>",
		"3. <b>Family 3</b>\n<code>https://sub.example.com/sub/token003</code>"} {
		if !strings.Contains(m.text, want) {
			t.Errorf("the message lacks %q:\n%s", want, m.text)
		}
	}
	if strings.Contains(m.text, "Being prepared") || strings.Contains(m.text, "longer than usual") {
		t.Errorf("a delivery with nothing delayed says so:\n%s", m.text)
	}
	if m.kb == nil || len(m.kb.InlineKeyboard) != 1 || len(m.kb.InlineKeyboard[0]) != 1 ||
		m.kb.InlineKeyboard[0][0].Text != "📦 My services" || m.kb.InlineKeyboard[0][0].CallbackData != "subs" || m.kb.InlineKeyboard[0][0].Style != "primary" {
		t.Errorf("the button: %+v", m.kb)
	}
	if len(core.asked) != 1 || core.asked[0] != b.GetId()+"/user-1" {
		t.Errorf("core was asked %v (the buyer's own bulk)", core.asked)
	}
	if i18n.TextLength(m.text) > 4096 {
		t.Errorf("the message is %d long", i18n.TextLength(m.text))
	}

	// In Persian, with Persian digits and the Persian texts.
	faN, _, faTG, _ := bulkNotifier(t, b, true)
	if err := handleEvent(t, faN, events.BulkReady, readyEvent(b, faBuyer, ready)); err != nil {
		t.Fatal(err)
	}
	fa := faTG.take()
	if len(fa) != 1 || !strings.Contains(fa[0].text, "<b>۳ سرویس شما آماده است!</b>") || !strings.Contains(fa[0].text, "۱. <b>Family 1</b>") ||
		fa[0].kb.InlineKeyboard[0][0].Text != "📦 سرویس\u200cهای من" {
		t.Errorf("the Persian message: %+v", fa)
	}
}

// A service that is not delivered when the bulk is announced is named as being
// prepared, in its place, and the message says how many are; a service that was
// delivered only after the announcement is not in the message (it is announced on
// its own, so it is never in both); a service refunded before delivery is left out.
func TestBulkDeliveryNamesWhatIsStillBeingPrepared(t *testing.T) {
	b, ready := bulkOf(4, "Shop", func(i int) bool { return i != 3 })
	// Service 4 was delivered after the announcement: its link is in core now, not in the event.
	ready = ready[:2]
	b.Services[3].Status = "active"
	b.Services[3].OrderStatus = "active"
	n, _, tgf, _ := bulkNotifier(t, b, true)
	e := readyEvent(b, enBuyer, ready)
	e.Delayed = 2
	if err := handleEvent(t, n, events.BulkReady, e); err != nil {
		t.Fatal(err)
	}
	sent := tgf.take()
	if len(sent) != 1 {
		t.Fatalf("%d messages", len(sent))
	}
	text := sent[0].text
	for _, want := range []string{"<b>2 of your 4 services are ready!</b>", "1. <b>Shop 1</b>\n<code>", "2. <b>Shop 2</b>\n<code>",
		"3. <b>Shop 3</b>\n⏳ Being prepared", "4. <b>Shop 4</b>\n⏳ Being prepared", "⏳ 2 of your services are taking longer than usual"} {
		if !strings.Contains(text, want) {
			t.Errorf("the message lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "token004") || strings.Contains(text, "token003") {
		t.Errorf("a link of a service that was not delivered at the announcement:\n%s", text)
	}
	if !strings.HasSuffix(strings.TrimSpace(text), "each one will be sent as soon as it is ready.") {
		t.Errorf("the note comes last:\n%s", text)
	}

	// None delivered: no celebration, every service listed as being prepared.
	none, noReady := bulkOf(3, "", func(int) bool { return false })
	nn, _, noTG, _ := bulkNotifier(t, none, true)
	if err := handleEvent(t, nn, events.BulkReady, readyEvent(none, enBuyer, noReady)); err != nil {
		t.Fatal(err)
	}
	got := noTG.take()
	if len(got) != 1 || !strings.Contains(got[0].text, "<b>Your 3 services are not ready yet</b>") || strings.Count(got[0].text, "⏳ Being prepared") != 3 ||
		strings.Contains(got[0].text, "<code>") || strings.Contains(got[0].text, "🎉") {
		t.Fatalf("nothing delivered: %+v", got)
	}
	// An unnamed service has its number as My services shows it.
	if !strings.Contains(got[0].text, "1. <b>#000001</b>") {
		t.Errorf("an unnamed service: %s", got[0].text)
	}

	// One refunded before it was delivered is not shown, and the others keep their numbers.
	refunded, rReady := bulkOf(3, "Gift", allReady)
	refunded.Services[1].OrderStatus = "cancelled"
	rReady = []string{rReady[0], rReady[2]}
	rn, _, rTG, _ := bulkNotifier(t, refunded, true)
	e = readyEvent(refunded, enBuyer, rReady)
	e.Delayed = 0
	if err := handleEvent(t, rn, events.BulkReady, e); err != nil {
		t.Fatal(err)
	}
	r := rTG.take()
	if len(r) != 1 || !strings.Contains(r[0].text, "<b>Your 2 services are ready!</b>") || strings.Contains(r[0].text, "Gift 2") ||
		!strings.Contains(r[0].text, "3. <b>Gift 3</b>") {
		t.Fatalf("with a refunded service: %+v", r)
	}
}

// Too long for one message: a few messages, each within Telegram's limit, with
// every link once and in order, the button only under the last, and then ONE text
// file of all the links, one a line (name, a tab, the link).
func TestBulkDeliverySplitsALongListAndSendsAFile(t *testing.T) {
	const services = 100
	b, ready := bulkOf(services, "Customer", allReady)
	n, _, tgf, up := bulkNotifier(t, b, true)
	if err := handleEvent(t, n, events.BulkReady, readyEvent(b, enBuyer, ready)); err != nil {
		t.Fatal(err)
	}
	sent := tgf.take()
	if len(sent) < 2 {
		t.Fatalf("%d messages for %d services", len(sent), services)
	}
	for i, m := range sent {
		if l := i18n.TextLength(m.text); l > bulkMessageLimit {
			t.Errorf("message %d is %d long", i+1, l)
		}
		if (i == len(sent)-1) != (m.kb != nil) {
			t.Errorf("message %d of %d has the button: %v", i+1, len(sent), m.kb != nil)
		}
		if i > 0 && !strings.HasPrefix(m.text, "…continued") {
			t.Errorf("message %d does not say it continues: %q", i+1, m.text[:30])
		}
	}
	if !strings.HasPrefix(sent[0].text, "🎉 <b>Your 100 services are ready!</b>") {
		t.Errorf("the first message: %q", sent[0].text[:60])
	}
	all := ""
	for _, m := range sent {
		all += m.text + "\n"
	}
	last := -1
	for i := 1; i <= services; i++ {
		link := fmt.Sprintf("<code>https://sub.example.com/sub/token%03d</code>", i)
		at := strings.Index(all, link)
		if at < 0 || strings.Count(all, link) != 1 || at < last {
			t.Fatalf("link %d is missing, repeated or out of order", i)
		}
		last = at
	}
	// The file.
	if len(up.files) != 1 {
		t.Fatalf("%d files", len(up.files))
	}
	f := up.files[0]
	if f.chat != enBuyer || f.name != "bobres-services-00b001.txt" || f.caption != "All 100 links in one file." {
		t.Errorf("the file: %q %q %q", f.name, f.caption, f.body[:0])
	}
	lines := strings.Split(strings.TrimSuffix(f.body, "\n"), "\n")
	if len(lines) != services || lines[0] != "Customer 1\thttps://sub.example.com/sub/token001" || lines[99] != "Customer 100\thttps://sub.example.com/sub/token100" {
		t.Errorf("the file has %d lines: %q ... %q", len(lines), lines[0], lines[len(lines)-1])
	}
	if strings.ContainsAny(f.body, "<>") {
		t.Errorf("markup in the file: %q", f.body[:80])
	}

	// A client that cannot upload a file still sends every message.
	n2, _, tgf2, _ := bulkNotifier(t, b, false)
	if err := handleEvent(t, n2, events.BulkReady, readyEvent(b, enBuyer, ready)); err != nil {
		t.Fatal(err)
	}
	if got := tgf2.take(); len(got) != len(sent) {
		t.Errorf("%d messages without upload support, want %d", len(got), len(sent))
	}
	// And the Persian caption and header.
	n3, _, tgf3, up3 := bulkNotifier(t, b, true)
	if err := handleEvent(t, n3, events.BulkReady, readyEvent(b, faBuyer, ready)); err != nil {
		t.Fatal(err)
	}
	if got := tgf3.take(); len(got) < 2 || !strings.Contains(got[0].text, "۱۰۰ سرویس شما آماده است!") || !strings.HasPrefix(got[1].text, "…ادامه") {
		t.Errorf("the Persian split: %d messages", len(got))
	}
	if len(up3.files) != 1 || up3.files[0].caption != "همهٔ ۱۰۰ لینک در یک فایل." {
		t.Errorf("the Persian file: %+v", up3.files)
	}

	// Half of them delivered: the file holds the delivered ones only.
	half, hReady := bulkOf(100, "Half", func(i int) bool { return i%2 == 0 })
	n4, _, _, up4 := bulkNotifier(t, half, true)
	if err := handleEvent(t, n4, events.BulkReady, readyEvent(half, enBuyer, hReady)); err != nil {
		t.Fatal(err)
	}
	if len(up4.files) != 1 || strings.Count(up4.files[0].body, "\n") != 50 || strings.Contains(up4.files[0].body, "Half 1\t") || up4.files[0].caption != "All 50 links in one file." {
		t.Errorf("the file of half the services: %+v", up4.files)
	}
}

// bulkMessages never puts more than the limit in a message, loses no block, keeps
// their order, and keeps the footer with the last ones when it fits.
func TestBulkMessagesSplitProperly(t *testing.T) {
	const limit = 300
	for _, c := range []struct {
		items  int
		width  int
		footer string
	}{
		{0, 0, ""}, {1, 50, ""}, {3, 50, "FOOTER"}, {7, 100, ""}, {7, 100, "FOOTER"}, {20, 120, "FOOTER " + strings.Repeat("f", 250)}, {40, 10, "FOOTER"},
		{15, 140, strings.Repeat("ز", 150)},
	} {
		items := make([]string, c.items)
		for i := range items {
			items[i] = fmt.Sprintf("<b>%d</b> %s", i, strings.Repeat("x", c.width))
		}
		msgs := bulkMessages("HEADER", items, c.footer, "CONT", limit)
		var all []string
		for i, m := range msgs {
			if l := i18n.TextLength(m); l > limit {
				t.Errorf("%d items of %d: message %d is %d long", c.items, c.width, i+1, l)
			}
			if i == 0 && !strings.HasPrefix(m, "HEADER") || i > 0 && !strings.HasPrefix(m, "CONT") && !strings.HasPrefix(m, c.footer) {
				t.Errorf("%d items: message %d starts %q", c.items, i+1, m[:min(8, len(m))])
			}
			all = append(all, strings.Split(m, "\n\n")...)
		}
		joined := strings.Join(all, "\n\n")
		last := -1
		for i, it := range items {
			at := strings.Index(joined, it)
			if at < 0 || at < last || strings.Count(joined, it) != 1 {
				t.Fatalf("%d items of %d: item %d lost, repeated or out of order", c.items, c.width, i)
			}
			last = at
		}
		if c.footer != "" && !strings.HasSuffix(msgs[len(msgs)-1], c.footer) {
			t.Errorf("%d items: the footer is not last", c.items)
		}
	}
}

// What a customer typed (a name) and a link are escaped in the messages and left
// as they are in the file.
func TestBulkDeliveryEscapesWhatCustomersType(t *testing.T) {
	b, ready := bulkOf(2, "x", allReady)
	b.Services[0].Label = `<b>Ali</b> & "Sara"`
	b.Services[0].SubscriptionLink = "https://sub.example.com/sub?a=1&b=2"
	n, _, tgf, up := bulkNotifier(t, b, true)
	// Make it long enough to produce a file.
	long, lReady := bulkOf(100, "Name that is rather long to fill the message", allReady)
	long.Services[0].Label = b.Services[0].Label
	long.Services[0].SubscriptionLink = b.Services[0].SubscriptionLink
	if err := handleEvent(t, n, events.BulkReady, readyEvent(b, enBuyer, ready)); err != nil {
		t.Fatal(err)
	}
	text := tgf.take()[0].text
	if !strings.Contains(text, "1. <b>&lt;b&gt;Ali&lt;/b&gt; &amp; &#34;Sara&#34;</b>") || !strings.Contains(text, "<code>https://sub.example.com/sub?a=1&amp;b=2</code>") {
		t.Fatalf("not escaped:\n%s", text)
	}
	nl, _, _, upl := bulkNotifier(t, long, true)
	if err := handleEvent(t, nl, events.BulkReady, readyEvent(long, enBuyer, lReady)); err != nil {
		t.Fatal(err)
	}
	if len(upl.files) != 1 || !strings.HasPrefix(upl.files[0].body, `<b>Ali</b> & "Sara"`+"\t"+"https://sub.example.com/sub?a=1&b=2\n") {
		t.Fatalf("the file: %q", upl.files[0].body[:80])
	}
	_ = up
}

// A customer who blocked the bot gets nothing and is marked once; a failure to send
// the first message is retried (nothing went out), a later one is not (the first
// would go out twice), nor is a file that could not be sent; core not finding the
// bulk is not retried.
func TestBulkDeliveryFailures(t *testing.T) {
	b, ready := bulkOf(100, "Cust", allReady)
	ev := readyEvent(b, enBuyer, ready)

	// Blocked.
	n, core, tgf, up := bulkNotifier(t, b, true)
	tgf.fail = map[int64]error{enBuyer: &tg.APIError{Code: 403, Description: "Forbidden: bot was blocked by the user"}}
	if err := handleEvent(t, n, events.BulkReady, ev); err != nil {
		t.Fatalf("a blocked customer: %v", err)
	}
	if len(core.blocked) != 1 || core.blocked[0] != enBuyer || len(up.files) != 0 || len(tgf.take()) != 0 {
		t.Fatalf("blocked: %v, %d files", core.blocked, len(up.files))
	}

	// The first message fails: the event is retried.
	n2, _, tgf2, up2 := bulkNotifier(t, b, true)
	tgf2.fail = map[int64]error{enBuyer: errors.New("network")}
	if err := handleEvent(t, n2, events.BulkReady, ev); err == nil || err.Error() != "network" {
		t.Fatalf("a failed first message: %v", err)
	}
	if len(up2.files) != 0 {
		t.Fatal("a file went out after nothing else did")
	}

	// A later message fails: not retried, and the file is still sent.
	n3, _, tgf3, up3 := bulkNotifier(t, b, true)
	flaky := &flakyTG{fakeTG: tgf3, failAt: 2}
	n3.tg = &flakyUpload{flakyTG: flaky, up: up3}
	if err := handleEvent(t, n3, events.BulkReady, ev); err != nil {
		t.Fatalf("a failed later message was retried: %v", err)
	}
	if len(up3.files) != 1 {
		t.Fatalf("%d files after a failed later message", len(up3.files))
	}

	// The file cannot be sent: logged, the messages stand.
	n4, _, tgf4, up4 := bulkNotifier(t, b, true)
	up4.fail = errors.New("too big")
	if err := handleEvent(t, n4, events.BulkReady, ev); err != nil || len(tgf4.take()) < 2 {
		t.Fatalf("a failed file: %v", err)
	}

	// Core does not know the bulk: dropped. Any other error: retried.
	n5, core5, tgf5, _ := bulkNotifier(t, b, true)
	core5.err = status.Error(codes.NotFound, "bulk not found")
	if err := handleEvent(t, n5, events.BulkReady, ev); err != nil || len(tgf5.take()) != 0 {
		t.Fatalf("an unknown bulk: %v", err)
	}
	core5.err = status.Error(codes.Unavailable, "core is down")
	if err := handleEvent(t, n5, events.BulkReady, ev); status.Code(err) != codes.Unavailable {
		t.Fatalf("core down: %v", err)
	}
	// A bulk with nothing to deliver or wait for is not announced at all.
	empty, _ := bulkOf(2, "Z", allReady)
	for _, sv := range empty.Services {
		sv.OrderStatus = "cancelled"
	}
	n6, _, tgf6, _ := bulkNotifier(t, empty, true)
	if err := handleEvent(t, n6, events.BulkReady, readyEvent(empty, enBuyer, nil)); err != nil || len(tgf6.take()) != 0 {
		t.Fatalf("an all refunded bulk: %v", err)
	}
	// An event that cannot be read is dead-lettered at once.
	if err := n.Handle(context.Background(), eventbus.Message{Topic: events.BulkReady, Payload: []byte("{")}); !eventbus.IsPermanent(err) {
		t.Fatalf("a broken event: %v", err)
	}
}

// flakyTG fails the failAt-th message (from 1) and sends the rest.
type flakyTG struct {
	*fakeTG
	n      int
	failAt int
}

func (f *flakyTG) SendMessage(ctx context.Context, chatID int64, text string, kb *tg.Keyboard) (*tg.Message, error) {
	f.n++
	if f.n == f.failAt {
		return nil, errors.New("flaky")
	}
	return f.fakeTG.SendMessage(ctx, chatID, text, kb)
}

type flakyUpload struct {
	*flakyTG
	up *uploadTG
}

func (f *flakyUpload) SendDocumentUpload(ctx context.Context, chatID int64, name string, r io.Reader, size int64, caption string) (*tg.Message, error) {
	return f.up.SendDocumentUpload(ctx, chatID, name, r, size, caption)
}

// The events of a service in a bulk purchase: its own delivery message is not sent
// (its link comes in the bulk's), the staff are still told about a failed delivery
// but the customer gets no note per service, and one that is not in a bulk is
// delivered as ever. A bulk sale is announced to the staff once, with its first
// order, for the whole quantity.
func TestBulkServicesAreNotAnnouncedOnTheirOwn(t *testing.T) {
	b, _ := bulkOf(3, "Q", allReady)
	n, core, tgf, _ := bulkNotifier(t, b, true)
	core.langs[enBuyer] = "en"

	// A delivered service of a bulk: nothing sent (not even for the links lookup).
	prov := events.SubscriptionProvisionedEvent{SubscriptionID: "sub-1", OrderID: "order-001", UserID: "user-1", TelegramID: enBuyer,
		SubscriptionLink: "https://sub.example.com/sub/x", BulkID: b.GetId()}
	if err := handleEvent(t, n, events.SubscriptionProvisioned, prov); err != nil || len(tgf.take()) != 0 {
		t.Fatalf("a service of a bulk was announced on its own: %v", err)
	}

	// A failed delivery: the staff alert goes out, the customer's note does not.
	failed := events.ProvisionFailedEvent{OrderID: "order-001", UserID: "user-1", TelegramID: enBuyer, Attempts: 3, Error: "panel down", BulkID: b.GetId()}
	if err := handleEvent(t, n, events.ProvisionFailed, failed); err != nil {
		t.Fatal(err)
	}
	sent := tgf.take()
	if len(sent) != 1 || sent[0].chat != ownerTG || !strings.Contains(sent[0].text, "Provisioning failed 3 times") {
		t.Fatalf("a failed delivery of a bulk's service: %+v", sent)
	}
	failed.BulkID = ""
	if err := handleEvent(t, n, events.ProvisionFailed, failed); err != nil {
		t.Fatal(err)
	}
	if sent := tgf.take(); len(sent) != 2 || sent[1].chat != enBuyer || !strings.Contains(sent[1].text, "taking longer than usual") {
		t.Fatalf("a failed delivery of an ordinary order: %+v", sent)
	}

	// The sale: the first order tells the staff, for the whole quantity; the others stay quiet.
	n.set.Replace(map[string]string{"notify.sales": "true"})
	paid := func(index int, bulkID string) events.OrderPaidEvent {
		e := events.OrderPaidEvent{OrderID: fmt.Sprintf("order-%03d", index), UserID: "user-1", TelegramID: enBuyer, PlanID: "plan-1",
			Amount: 150_000, Currency: "IRT", Source: events.PaidByWallet}
		if bulkID != "" {
			e.BulkID, e.BulkIndex, e.BulkQuantity = bulkID, index, 3
		}
		return e
	}
	for i := 1; i <= 3; i++ {
		if err := handleEvent(t, n, events.OrderPaid, paid(i, b.GetId())); err != nil {
			t.Fatal(err)
		}
	}
	sale := tgf.take()
	if len(sale) != 1 || sale[0].chat != ownerTG || !strings.Contains(sale[0].text, "New bulk sale") || !strings.Contains(sale[0].text, "3 × Monthly 50GB") ||
		!strings.Contains(sale[0].text, "450,000 Toman") {
		t.Fatalf("the bulk sale alert: %+v", sale)
	}
	// An ordinary sale is as it was.
	if err := handleEvent(t, n, events.OrderPaid, paid(1, "")); err != nil {
		t.Fatal(err)
	}
	if one := tgf.take(); len(one) != 1 || !strings.Contains(one[0].text, "New sale") || !strings.Contains(one[0].text, "150,000 Toman") {
		t.Fatalf("an ordinary sale alert: %+v", one)
	}
	// An older core's bulk event without a quantity is still one sale of that order.
	old := paid(1, b.GetId())
	old.BulkQuantity = 0
	if err := handleEvent(t, n, events.OrderPaid, old); err != nil {
		t.Fatal(err)
	}
	if one := tgf.take(); len(one) != 1 || !strings.Contains(one[0].text, "New sale") {
		t.Fatalf("a bulk event without a quantity: %+v", one)
	}
}
