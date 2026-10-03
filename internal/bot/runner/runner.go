// Package runner feeds Telegram updates to the handler: long polling or a
// webhook, with per-user ordering and parallelism across users.
package runner

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
)

// HandleFunc serves one update.
type HandleFunc func(ctx context.Context, u tg.Update)

// Dispatcher runs updates on N workers, always sending one user's updates to
// the same worker so they are handled in order.
type Dispatcher struct {
	queues []chan tg.Update
	wg     sync.WaitGroup
	once   sync.Once
}

// NewDispatcher starts workers that call handle with ctx.
func NewDispatcher(ctx context.Context, workers int, handle HandleFunc) *Dispatcher {
	if workers <= 0 {
		workers = 8
	}
	d := &Dispatcher{queues: make([]chan tg.Update, workers)}
	for i := range d.queues {
		q := make(chan tg.Update, 64)
		d.queues[i] = q
		d.wg.Add(1)
		go func() {
			defer d.wg.Done()
			for u := range q {
				handle(ctx, u)
			}
		}()
	}
	return d
}

// Dispatch queues an update (blocking when that worker is full: back-pressure).
func (d *Dispatcher) Dispatch(u tg.Update) {
	var id int64
	if s := u.Sender(); s != nil {
		id = s.ID
	}
	if id < 0 {
		id = -id
	}
	d.queues[id%int64(len(d.queues))] <- u
}

// Stop drains the queues and waits for in-flight updates, until ctx expires.
func (d *Dispatcher) Stop(ctx context.Context) {
	d.once.Do(func() {
		for _, q := range d.queues {
			close(q)
		}
	})
	done := make(chan struct{})
	go func() { d.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

// Updates is what polling needs from the Bot API client.
type Updates interface {
	GetUpdates(ctx context.Context, offset int64, timeoutSec int) ([]tg.Update, error)
}

// Poll long-polls until ctx is done, backing off on errors.
func Poll(ctx context.Context, api Updates, d *Dispatcher, log *slog.Logger) error {
	var offset int64
	backoff := time.Second
	for {
		ups, err := api.GetUpdates(ctx, offset, 50)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			log.Warn("getUpdates failed; retrying", "err", err, "in", backoff)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(backoff):
			}
			backoff = min(2*backoff, 30*time.Second)
			continue
		}
		backoff = time.Second
		for _, u := range ups {
			d.Dispatch(u)
			if u.UpdateID >= offset {
				offset = u.UpdateID + 1
			}
		}
	}
}

// Webhook serves Telegram's webhook deliveries. Every request must carry the
// secret configured with setWebhook (X-Telegram-Bot-Api-Secret-Token).
func Webhook(secret string, d *Dispatcher, log *slog.Logger) http.Handler {
	want := []byte(secret)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		got := []byte(r.Header.Get("X-Telegram-Bot-Api-Secret-Token"))
		if len(want) == 0 || subtle.ConstantTimeCompare(got, want) != 1 {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		var u tg.Update
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&u); err != nil {
			log.Warn("bad webhook payload", "err", err)
			w.WriteHeader(http.StatusOK) // do not make Telegram retry garbage
			return
		}
		d.Dispatch(u)
		w.WriteHeader(http.StatusOK)
	})
}
