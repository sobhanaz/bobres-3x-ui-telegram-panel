// Package broadcast sends the staff's broadcasts. Core keeps the recipients;
// the worker claims them in small batches, sends each their message at a
// pace Telegram allows, and reports what happened. Delivery is at most
// once: a send whose outcome is unknown is reported so and never repeated.
package broadcast

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/settings"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	"google.golang.org/grpc"
)

// Core is what the worker needs from core.
type Core interface {
	ClaimBroadcastBatch(ctx context.Context, in *corev1.ClaimBroadcastBatchRequest, opts ...grpc.CallOption) (*corev1.BroadcastBatch, error)
	ReportBroadcastResults(ctx context.Context, in *corev1.ReportBroadcastResultsRequest, opts ...grpc.CallOption) (*corev1.BroadcastState, error)
}

// Sender sends one message: the broadcast's own Telegram client, which
// hands back a 429 instead of sleeping on it (tg.WithNoRetry).
type Sender interface {
	SendMessage(ctx context.Context, chatID int64, text string, kb *tg.Keyboard) (*tg.Message, error)
}

const (
	batchSize   = 25 // recipients claimed at once
	maxInFlight = 4  // sends waiting for Telegram at once
	// maxTotalPerSec is the bot's overall pace (Telegram allows about 30
	// messages a second): broadcasts get what interactive replies leave.
	maxTotalPerSec = 28
	sendTimeout    = 15 * time.Second
	reportTimeout  = 5 * time.Second
	// A broadcast halts (pauses, with the reason for the staff) after this
	// many identical refusals in a row (a text or button Telegram will refuse
	// for everyone), or sends with no answer in a row (Telegram unreachable).
	maxSameFailures = 5
	maxUnknown      = 3
)

// Outcomes reported to core.
const (
	outSent     = corev1.BroadcastOutcome_BROADCAST_OUTCOME_SENT
	outBlocked  = corev1.BroadcastOutcome_BROADCAST_OUTCOME_BLOCKED
	outFailed   = corev1.BroadcastOutcome_BROADCAST_OUTCOME_FAILED
	outUnknown  = corev1.BroadcastOutcome_BROADCAST_OUTCOME_UNKNOWN
	outReleased = corev1.BroadcastOutcome_BROADCAST_OUTCOME_RELEASED
)

// Worker sends broadcasts until its context ends. Run it once per process:
// the pace is kept per process.
type Worker struct {
	core  Core
	tg    Sender
	set   *settings.Store
	flood *tg.Flood
	meter *tg.Meter
	log   *slog.Logger
	// Idle is how long to wait when there is nothing to send; FlushEvery how
	// often results are reported while sending (also after every 25).
	Idle       time.Duration
	FlushEvery time.Duration
}

// New builds a Worker. set holds limits.broadcast_per_sec; flood is shared
// with the bot's other Telegram client and meter counts that client's
// messages (both may be nil).
func New(core Core, sender Sender, set *settings.Store, flood *tg.Flood, meter *tg.Meter, log *slog.Logger) *Worker {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	if set == nil {
		set = settings.New()
	}
	if flood == nil {
		flood = &tg.Flood{}
	}
	return &Worker{core: core, tg: sender, set: set, flood: flood, meter: meter, log: log,
		Idle: 3 * time.Second, FlushEvery: time.Second}
}

// Run claims and sends until ctx ends. It never fails: errors are logged and
// retried after Idle.
func (w *Worker) Run(ctx context.Context) error {
	for {
		n, err := w.Step(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			w.log.Warn("broadcast step failed", "err", err)
		}
		if n == 0 || err != nil {
			t := time.NewTimer(w.Idle)
			select {
			case <-ctx.Done():
				t.Stop()
				return nil
			case <-t.C:
			}
		}
	}
}

// Step claims one batch, sends it and reports every result. It returns how
// many recipients it handled (0: nothing to send).
func (w *Worker) Step(ctx context.Context) (int, error) {
	if ctx.Err() != nil {
		return 0, nil
	}
	b, err := w.core.ClaimBroadcastBatch(ctx, &corev1.ClaimBroadcastBatchRequest{Limit: batchSize})
	if err != nil {
		return 0, fmt.Errorf("claim: %w", err)
	}
	if b.GetBroadcastId() == "" || len(b.GetRecipients()) == 0 {
		return 0, nil
	}
	return len(b.GetRecipients()), w.send(ctx, b)
}

// send sends a claimed batch: paced, a few at a time, each on its own
// context so a shutdown lets sends in flight finish.
func (w *Worker) send(ctx context.Context, b *corev1.BroadcastBatch) error {
	id, rcpts := b.GetBroadcastId(), b.GetRecipients()
	if reason := checkBatch(b); reason != "" {
		w.log.Warn("broadcast halted: it cannot be sent", "broadcast", id, "reason", reason)
		_, err := w.report(ctx, id, released(rcpts, nil), reason)
		return err
	}
	rate := int(w.set.Int("limits.broadcast_per_sec", 20, 1, 25))
	p := &pacer{rate: rate, flood: w.flood, meter: w.meter}
	c := &collector{w: w, id: id, stop: make(chan struct{})}
	results := make(chan outcome, len(rcpts)) // senders never wait on the collector
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.run(ctx, results)
	}()

	started := make([]bool, len(rcpts))
	sem := make(chan struct{}, maxInFlight)
	var wg sync.WaitGroup
loop:
	for i, r := range rcpts {
		if p.wait(ctx, c.stop) != nil {
			break
		}
		select {
		case sem <- struct{}{}:
		case <-c.stop:
			break loop
		case <-ctx.Done():
			break loop
		}
		if c.stopped() || ctx.Err() != nil { // while waiting for a free slot
			<-sem
			break
		}
		started[i] = true
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			results <- w.sendOne(ctx, c.stop, p, b, r)
		}()
	}
	wg.Wait()
	close(results)
	<-done

	// What is left: unreported results, and the recipients never tried,
	// which go back in line (or are dropped, when the broadcast was
	// cancelled).
	final := append(c.buf, released(rcpts, started)...)
	if len(final) == 0 && c.halt == "" {
		return nil
	}
	_, err := w.report(ctx, id, final, c.halt)
	if err != nil {
		// Core gives up on these after the claim's lease: they count as
		// unknown and are never sent again.
		w.log.Error("broadcast results not reported", "broadcast", id, "results", len(final), "err", err)
		return err
	}
	if c.halt != "" {
		w.log.Warn("broadcast halted", "broadcast", id, "reason", c.halt)
	}
	return nil
}

// released reports recipients never tried (all of them when started is nil).
func released(rcpts []*corev1.BroadcastRecipient, started []bool) []*corev1.BroadcastResult {
	var out []*corev1.BroadcastResult
	for i, r := range rcpts {
		if started == nil || !started[i] {
			out = append(out, &corev1.BroadcastResult{UserId: r.GetUserId(), Outcome: outReleased})
		}
	}
	return out
}

// report sends results to core on a context of its own (a shutdown must not
// lose them) and answers the broadcast's status.
func (w *Worker) report(ctx context.Context, id string, results []*corev1.BroadcastResult, halt string) (string, error) {
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), reportTimeout)
	defer cancel()
	st, err := w.core.ReportBroadcastResults(rctx, &corev1.ReportBroadcastResultsRequest{BroadcastId: id, Results: results, HaltReason: halt})
	if err != nil {
		return "", fmt.Errorf("report: %w", err)
	}
	return st.GetStatus(), nil
}

// outcome is one recipient's result; halt stops the broadcast at once.
type outcome struct {
	res  *corev1.BroadcastResult
	halt string
}

// sendOne sends one recipient their message. A 429 waits for the flood to
// pass and tries the same recipient again; a stop or shutdown meanwhile puts
// them back in line (they got nothing).
func (w *Worker) sendOne(ctx context.Context, stop <-chan struct{}, p *pacer, b *corev1.BroadcastBatch, r *corev1.BroadcastRecipient) outcome {
	res := &corev1.BroadcastResult{UserId: r.GetUserId()}
	text, kb := message(b, r.GetLanguage())
	for {
		sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sendTimeout)
		m, err := w.tg.SendMessage(sctx, r.GetTelegramId(), text, kb)
		cancel()
		if wait, ok := tg.IsFlood(err); ok {
			w.flood.Set(wait)
			if p.wait(ctx, stop) != nil {
				res.Outcome = outReleased
				return outcome{res: res}
			}
			continue
		}
		return classify(res, m, err)
	}
}

// classify sorts a send's result.
func classify(res *corev1.BroadcastResult, m *tg.Message, err error) outcome {
	var ae *tg.APIError
	switch {
	case err == nil:
		res.Outcome, res.MessageId = outSent, int64(m.MessageID)
	case tg.IsBlocked(err):
		res.Outcome, res.Error = outBlocked, describe(err)
	case errors.As(err, &ae) && (ae.Code == http.StatusUnauthorized || ae.Code == http.StatusNotFound):
		// The bot token was revoked: nothing can be sent.
		res.Outcome = outReleased
		return outcome{res: res, halt: fmt.Sprintf("Telegram refused the bot's token (%d %s)", ae.Code, ae.Description)}
	case errors.As(err, &ae) && ae.Code >= 400 && ae.Code < 500 && ae.Description != "unexpected non-JSON reply":
		res.Outcome, res.Error = outFailed, describe(err)
	default: // no answer, a timeout, a server error: perhaps delivered
		res.Outcome, res.Error = outUnknown, describe(err)
	}
	return outcome{res: res}
}

// describe is an error as stored for the staff (never the bot token: the tg
// client keeps it out of its errors).
func describe(err error) string {
	var ae *tg.APIError
	s := err.Error()
	if errors.As(err, &ae) {
		s = ae.Description
	}
	if r := []rune(s); len(r) > 200 {
		s = string(r[:200])
	}
	return s
}

// collector gathers results, reports them every 25 or every FlushEvery, and
// stops the batch when the broadcast halts or is no longer running.
type collector struct {
	w    *Worker
	id   string
	stop chan struct{}
	once sync.Once

	buf      []*corev1.BroadcastResult // not reported yet
	halt     string
	lastFail string
	sameFail int
	unknown  int
}

func (c *collector) stopNow() { c.once.Do(func() { close(c.stop) }) }

func (c *collector) stopped() bool {
	select {
	case <-c.stop:
		return true
	default:
		return false
	}
}

func (c *collector) run(ctx context.Context, results <-chan outcome) {
	t := time.NewTicker(c.w.FlushEvery)
	defer t.Stop()
	for {
		select {
		case o, ok := <-results:
			if !ok {
				return
			}
			c.add(o)
			if len(c.buf) >= batchSize {
				c.flush(ctx)
			}
		case <-t.C:
			c.flush(ctx)
		}
	}
}

func (c *collector) add(o outcome) {
	c.buf = append(c.buf, o.res)
	switch o.res.GetOutcome() {
	case outFailed:
		if o.res.GetError() == c.lastFail {
			c.sameFail++
		} else {
			c.lastFail, c.sameFail = o.res.GetError(), 1
		}
		c.unknown = 0
		if c.sameFail >= maxSameFailures {
			c.halted(fmt.Sprintf("Telegram refused %d messages in a row: %s", c.sameFail, c.lastFail))
		}
	case outUnknown:
		c.unknown++
		c.lastFail, c.sameFail = "", 0
		if c.unknown >= maxUnknown {
			c.halted(fmt.Sprintf("Telegram did not answer %d sends in a row: %s", c.unknown, o.res.GetError()))
		}
	case outSent, outBlocked:
		c.lastFail, c.sameFail, c.unknown = "", 0, 0
	}
	if o.halt != "" {
		c.halted(o.halt)
	}
}

func (c *collector) halted(reason string) {
	if c.halt == "" {
		c.halt = reason
	}
	c.stopNow()
}

// flush reports what is buffered; when the broadcast was paused, cancelled
// or finished meanwhile, the batch stops. A failed report is tried again
// with the next one.
func (c *collector) flush(ctx context.Context) {
	if len(c.buf) == 0 {
		return
	}
	st, err := c.w.report(ctx, c.id, c.buf, "")
	if err != nil {
		c.w.log.Warn("broadcast results not reported yet", "broadcast", c.id, "err", err)
		return
	}
	c.buf = nil
	if st != "running" {
		c.w.log.Info("broadcast no longer running; stopping", "broadcast", c.id, "status", st)
		c.stopNow()
	}
}

// pacer spaces sends: never during a flood wait, and at most the broadcast
// rate a second, less the interactive messages of the last second.
type pacer struct {
	mu    sync.Mutex
	rate  int
	next  time.Time
	flood *tg.Flood
	meter *tg.Meter
}

// errStopped: the batch stopped while waiting.
var errStopped = errors.New("broadcast: stopped")

// budget is how many broadcast messages a second may go out now: the rate,
// less the interactive messages of the last second (customers come first),
// never more than Telegram's overall limit in all.
func budget(rate, interactive int) int { return min(rate, maxTotalPerSec) - interactive }

// wait returns when the next send may go, or an error when ctx ends or the
// batch stops first.
func (p *pacer) wait(ctx context.Context, stop <-chan struct{}) error {
	for {
		var d time.Duration
		if until := time.Until(p.flood.Until()); until > 0 {
			d = until
		} else {
			p.mu.Lock()
			now := time.Now()
			switch per := budget(p.rate, p.meter.LastSecond()); {
			case per < 1:
				d = 100 * time.Millisecond // interactive replies use it all
			case now.Before(p.next):
				d = p.next.Sub(now)
			default:
				p.next = now.Add(time.Second / time.Duration(per))
			}
			p.mu.Unlock()
		}
		if d == 0 {
			select {
			case <-stop:
				return errStopped
			default:
				return ctx.Err()
			}
		}
		t := time.NewTimer(d)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-stop:
			t.Stop()
			return errStopped
		case <-t.C:
		}
	}
}

// pick is a text in lang, else in another language.
func pick(m map[string]string, lang string) string {
	if v := m[lang]; v != "" {
		return v
	}
	for _, l := range i18n.Languages {
		if v := m[l]; v != "" {
			return v
		}
	}
	return ""
}

// message is what a recipient of lang gets: the text in their language (or
// the other one) and the link button labelled in it.
func message(b *corev1.BroadcastBatch, lang string) (string, *tg.Keyboard) {
	var kb *tg.Keyboard
	if u := b.GetButtonUrl(); u != "" {
		kb = (&tg.Keyboard{}).Row(tg.Link(pick(b.GetButtonLabels(), lang), u))
	}
	return pick(b.GetTexts(), lang), kb
}

// checkBatch checks again what core checked, so a broken broadcast halts
// instead of failing for every recipient: "" when it can be sent.
func checkBatch(b *corev1.BroadcastBatch) string {
	if pick(b.GetTexts(), i18n.Languages[0]) == "" {
		return "the broadcast has no text"
	}
	langs := make([]string, 0, len(b.GetTexts()))
	for l := range b.GetTexts() {
		langs = append(langs, l)
	}
	slices.Sort(langs)
	for _, l := range langs {
		if v := b.GetTexts()[l]; v != "" {
			if p := textProblem(v); p != "" {
				return "the " + l + " text " + p
			}
		}
	}
	if u := b.GetButtonUrl(); u != "" {
		if !HTTPSURL(u) {
			return "the button link must be an https:// link"
		}
		if pick(b.GetButtonLabels(), i18n.Languages[0]) == "" {
			return "the button has no label"
		}
		for l, v := range b.GetButtonLabels() {
			if _, probs := i18n.ValidateLabel(v); len(probs) > 0 {
				return "the " + l + " button label cannot be used (" + problemCodes(probs) + ")"
			}
		}
	}
	return ""
}

// Problem says why a broadcast message cannot be sent ("" when it can): the
// text must be Telegram's HTML within 4096 characters; a button (buttonURL
// not empty) needs an https link and a short plain label.
func Problem(text, buttonURL, buttonLabel string) string {
	if p := textProblem(text); p != "" {
		return "the text " + p
	}
	if buttonURL == "" && buttonLabel == "" {
		return ""
	}
	if !HTTPSURL(buttonURL) {
		return "the button link must be an https:// link"
	}
	if _, probs := i18n.ValidateLabel(buttonLabel); len(probs) > 0 {
		return "the button label cannot be used (" + problemCodes(probs) + ")"
	}
	return ""
}

func textProblem(text string) string {
	if _, probs := i18n.ValidateMessage(text, i18n.MaxMessage); len(probs) > 0 {
		return "cannot be sent (" + problemCodes(probs) + ")"
	}
	return ""
}

func problemCodes(probs []i18n.Problem) string {
	out := make([]string, 0, len(probs))
	for _, p := range probs {
		s := p.Code
		if p.Arg != "" {
			s += " " + p.Arg
		}
		out = append(out, s)
	}
	return strings.Join(out, ", ")
}

// HTTPSURL accepts a link for a broadcast button the way core does: https,
// a host, no credentials, at most 1024 bytes.
func HTTPSURL(s string) bool {
	u, err := url.Parse(s)
	return s != "" && len(s) <= 1024 && err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil
}
