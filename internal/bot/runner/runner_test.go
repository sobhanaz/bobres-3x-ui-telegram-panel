package runner

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
)

func update(id int64, user int64) tg.Update {
	return tg.Update{UpdateID: id, Message: &tg.Message{From: &tg.User{ID: user}, Chat: tg.Chat{ID: user, Type: "private"}}}
}

func TestDispatcherKeepsPerUserOrder(t *testing.T) {
	var mu sync.Mutex
	seen := map[int64][]int64{}
	d := NewDispatcher(context.Background(), 4, func(_ context.Context, u tg.Update) {
		time.Sleep(time.Millisecond)
		mu.Lock()
		seen[u.Message.From.ID] = append(seen[u.Message.From.ID], u.UpdateID)
		mu.Unlock()
	})
	for i := int64(0); i < 40; i++ {
		d.Dispatch(update(i, i%5))
	}
	d.Stop(context.Background())
	for user, ids := range seen {
		for i := 1; i < len(ids); i++ {
			if ids[i] < ids[i-1] {
				t.Fatalf("user %d handled out of order: %v", user, ids)
			}
		}
	}
	if len(seen) != 5 {
		t.Fatalf("users served: %d", len(seen))
	}
}

type fakeUpdates struct {
	mu      sync.Mutex
	calls   int
	offsets []int64
}

func (f *fakeUpdates) GetUpdates(ctx context.Context, offset int64, _ int) ([]tg.Update, error) {
	f.mu.Lock()
	f.calls++
	n := f.calls
	f.offsets = append(f.offsets, offset)
	f.mu.Unlock()
	switch n {
	case 1:
		return []tg.Update{update(10, 1), update(11, 2)}, nil
	case 2:
		return nil, errors.New("network down")
	case 3:
		return []tg.Update{update(12, 1)}, nil
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestPollAdvancesOffsetAndSurvivesErrors(t *testing.T) {
	var got []int64
	var mu sync.Mutex
	ctx, cancel := context.WithCancel(context.Background())
	d := NewDispatcher(ctx, 1, func(_ context.Context, u tg.Update) { mu.Lock(); got = append(got, u.UpdateID); mu.Unlock() })
	api := &fakeUpdates{}
	done := make(chan error, 1)
	go func() { done <- Poll(ctx, api, d, slog.New(slog.DiscardHandler)) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n == 3 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	d.Stop(context.Background())
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(got) != 3 || api.offsets[0] != 0 || api.offsets[1] != 12 || api.offsets[2] != 12 {
		t.Fatalf("got %v offsets %v", got, api.offsets)
	}
}

func TestWebhookRequiresTheSecret(t *testing.T) {
	var got []int64
	var mu sync.Mutex
	d := NewDispatcher(context.Background(), 1, func(_ context.Context, u tg.Update) { mu.Lock(); got = append(got, u.UpdateID); mu.Unlock() })
	h := Webhook("s3cret-value", d, slog.New(slog.DiscardHandler))
	post := func(secret, body string) int {
		req := httptest.NewRequest(http.MethodPost, "/tg/webhook", strings.NewReader(body))
		if secret != "" {
			req.Header.Set("X-Telegram-Bot-Api-Secret-Token", secret)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	body := `{"update_id":5,"message":{"message_id":1,"chat":{"id":9,"type":"private"},"from":{"id":9}}}`
	if code := post("", body); code != http.StatusForbidden {
		t.Fatalf("no secret: %d", code)
	}
	if code := post("wrong", body); code != http.StatusForbidden {
		t.Fatalf("wrong secret: %d", code)
	}
	if code := post("s3cret-value", body); code != http.StatusOK {
		t.Fatalf("valid: %d", code)
	}
	d.Stop(context.Background())
	if len(got) != 1 || got[0] != 5 {
		t.Fatalf("dispatched %v", got)
	}
}

// A pre-checkout query is answered even while the user's worker is stuck on a
// slow update (Telegram cancels the payment after 10 s).
func TestPreCheckoutSkipsTheQueue(t *testing.T) {
	block := make(chan struct{})
	answered := make(chan struct{})
	d := NewDispatcher(context.Background(), 1, func(_ context.Context, u tg.Update) {
		if u.PreCheckoutQuery != nil {
			close(answered)
			return
		}
		<-block // a slow update holding the only worker
	})
	d.Dispatch(tg.Update{UpdateID: 1, Message: &tg.Message{From: &tg.User{ID: 7}, Chat: tg.Chat{ID: 7}}})
	d.Dispatch(tg.Update{UpdateID: 2, PreCheckoutQuery: &tg.PreCheckoutQuery{ID: "q", From: tg.User{ID: 7}}})
	select {
	case <-answered:
	case <-time.After(2 * time.Second):
		t.Fatal("the pre-checkout waited behind a slow update")
	}
	close(block)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	d.Stop(ctx)
}

// After Stop, Dispatch drops updates instead of panicking on a closed queue.
func TestDispatchAfterStop(t *testing.T) {
	d := NewDispatcher(context.Background(), 2, func(context.Context, tg.Update) {})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	d.Stop(ctx)
	d.Dispatch(tg.Update{UpdateID: 1, Message: &tg.Message{From: &tg.User{ID: 1}, Chat: tg.Chat{ID: 1}}})
	d.Dispatch(tg.Update{UpdateID: 2, PreCheckoutQuery: &tg.PreCheckoutQuery{ID: "q", From: tg.User{ID: 1}}})
}
