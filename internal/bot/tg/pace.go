package tg

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// Flood is when Telegram allows sending again after a 429 (Too Many
// Requests). One Flood is shared by all of the bot's clients: a flood wait
// any of them hits pauses bulk sends (broadcasts) too. The zero value is
// ready to use; all methods are safe for concurrent use.
type Flood struct {
	until atomic.Int64 // unix nanoseconds; 0 = never flooded
}

// Set records a flood wait of d from now. It only ever extends the wait.
func (f *Flood) Set(d time.Duration) {
	if f == nil || d <= 0 {
		return
	}
	u := time.Now().Add(d).UnixNano()
	for {
		cur := f.until.Load()
		if u <= cur || f.until.CompareAndSwap(cur, u) {
			return
		}
	}
}

// Until is when sending is allowed again (zero: no flood wait seen).
func (f *Flood) Until() time.Time {
	if f == nil {
		return time.Time{}
	}
	n := f.until.Load()
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(0, n)
}

// Wait returns once no flood wait is running (at once when there is none),
// or with ctx's error when ctx ends first.
func (f *Flood) Wait(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		d := time.Until(f.Until())
		if d <= 0 {
			return nil
		}
		t := time.NewTimer(d)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C: // the wait may have been extended meanwhile: check again
		}
	}
}

// meterSlots × meterSlot is the window a Meter counts over.
const (
	meterSlots = 10
	meterSlot  = 100 * time.Millisecond
)

// Meter counts the messages a client sent in the last second, so bulk sends
// leave room for interactive replies under Telegram's overall limit. The
// zero value is ready to use; all methods are safe for concurrent use.
type Meter struct {
	mu    sync.Mutex
	slots [meterSlots]struct {
		tick int64 // which 100 ms the count belongs to
		n    int
	}
}

func meterTick(t time.Time) int64 { return t.UnixNano() / int64(meterSlot) }

// Add counts one message sent now.
func (m *Meter) Add() {
	if m == nil {
		return
	}
	tick := meterTick(time.Now())
	m.mu.Lock()
	defer m.mu.Unlock()
	s := &m.slots[tick%meterSlots]
	if s.tick != tick {
		s.tick, s.n = tick, 0
	}
	s.n++
}

// LastSecond is how many messages were counted in the last second.
func (m *Meter) LastSecond() int {
	if m == nil {
		return 0
	}
	tick := meterTick(time.Now())
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, s := range m.slots {
		if tick-s.tick < meterSlots {
			n += s.n
		}
	}
	return n
}

// countsAsSend: methods that put a message in a chat count against
// Telegram's per-second limits (answers to taps, lookups and polling do not).
func countsAsSend(method string) bool {
	switch method {
	case "sendMessage", "sendPhoto", "sendDocument", "sendInvoice", "editMessageText", "editMessageReplyMarkup",
		"copyMessage":
		return true
	}
	return false
}

// NewBulkHTTPClient is an HTTP client of its own for bulk sends (broadcasts):
// separate connections, up to conns kept open, so its sends never wait
// behind long polling or interactive replies. Each send's time limit comes
// from its context; the client's own timeout is only a backstop.
func NewBulkHTTPClient(conns int) *http.Client {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.MaxIdleConns = max(conns, t.MaxIdleConns)
	t.MaxIdleConnsPerHost = conns
	return &http.Client{Transport: t, Timeout: time.Minute}
}
