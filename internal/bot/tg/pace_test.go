package tg

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func flood429(retryAfter int) map[string]any {
	return map[string]any{"ok": false, "error_code": 429, "description": "Too Many Requests: retry after 2",
		"parameters": map[string]any{"retry_after": retryAfter}}
}

// A 429 seen by any client sets the shared flood marker; a client without
// retries hands the 429 back at once.
func TestFloodIsSharedAndNoRetry(t *testing.T) {
	var calls atomic.Int32
	_, srv := fakeAPI(t, func(string, []byte, *http.Request) any {
		calls.Add(1)
		return flood429(2)
	})
	flood := &Flood{}
	bulk := New(token, WithBaseURL(srv.URL+"/bot"+token), WithFlood(flood), WithNoRetry())
	start := time.Now()
	_, err := bulk.SendMessage(context.Background(), 1, "x", nil)
	wait, ok := IsFlood(err)
	if !ok || wait != 2*time.Second || calls.Load() != 1 || time.Since(start) > time.Second {
		t.Fatalf("no-retry client: %v (wait %v) after %d calls in %v", err, wait, calls.Load(), time.Since(start))
	}
	if until := time.Until(flood.Until()); until < time.Second || until > 2*time.Second {
		t.Fatalf("flood marker: %v from now", until)
	}

	// Another client (the interactive one) sharing the marker extends it; a
	// shorter wait never shortens it.
	other := New(token, WithBaseURL(srv.URL+"/bot"+token), WithFlood(flood), WithNoRetry())
	before := flood.Until()
	flood.Set(time.Millisecond)
	if !flood.Until().Equal(before) {
		t.Fatal("a shorter wait shortened the marker")
	}
	_ = other.AnswerCallback(context.Background(), "cb", "", false)
	if !flood.Until().After(before) {
		t.Fatal("a 429 seen by another client did not extend the marker")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := flood.Wait(ctx); err == nil {
		t.Fatal("Wait returned during a flood wait")
	}
	var none Flood
	if err := none.Wait(context.Background()); err != nil || !none.Until().IsZero() {
		t.Fatalf("no flood: %v %v", err, none.Until())
	}
	short := &Flood{}
	short.Set(100 * time.Millisecond)
	start = time.Now()
	if err := short.Wait(context.Background()); err != nil || time.Since(start) < 80*time.Millisecond {
		t.Fatalf("short wait: %v after %v", err, time.Since(start))
	}
}

func TestIsFlood(t *testing.T) {
	if _, ok := IsFlood(&APIError{Code: 400, Description: "Bad Request"}); ok {
		t.Fatal("a 400 is not a flood")
	}
	if d, ok := IsFlood(&APIError{Code: 429}); !ok || d != time.Second {
		t.Fatalf("429 without retry_after: %v %v", d, ok)
	}
}

// The meter counts message sends of the last second, not polling or taps.
func TestMeterCountsSends(t *testing.T) {
	_, srv := fakeAPI(t, func(method string, _ []byte, _ *http.Request) any {
		if method == "getUpdates" {
			return ok([]any{})
		}
		return ok(map[string]any{"message_id": 1, "chat": map[string]any{"id": 1}})
	})
	m := &Meter{}
	mc := New(token, WithBaseURL(srv.URL+"/bot"+token), WithMeter(m))
	ctx := context.Background()
	for range 3 {
		if _, err := mc.SendMessage(ctx, 1, "x", nil); err != nil {
			t.Fatal(err)
		}
	}
	_ = mc.EditMessageText(ctx, 1, 1, "y", nil)
	_ = mc.AnswerCallback(ctx, "cb", "", false)
	_, _ = mc.GetUpdates(ctx, 0, 0)
	if n := m.LastSecond(); n != 4 {
		t.Fatalf("meter: %d, want 4 (3 sends and an edit)", n)
	}
	time.Sleep(1100 * time.Millisecond)
	if n := m.LastSecond(); n != 0 {
		t.Fatalf("meter after a second: %d", n)
	}
	var nilMeter *Meter
	nilMeter.Add()
	if nilMeter.LastSecond() != 0 {
		t.Fatal("a nil meter counts nothing")
	}
}

func TestMyChatMemberAndReplies(t *testing.T) {
	raw := `{"update_id":5,"my_chat_member":{"chat":{"id":42,"type":"private"},"from":{"id":42},"date":1,
		"old_chat_member":{"status":"member","user":{"id":1000}},"new_chat_member":{"status":"kicked","user":{"id":1000}}}}`
	var u Update
	if err := json.Unmarshal([]byte(raw), &u); err != nil {
		t.Fatal(err)
	}
	if u.MyChatMember == nil || u.MyChatMember.NewChatMember.Status != "kicked" || u.Sender().ID != 42 {
		t.Fatalf("my_chat_member: %+v", u.MyChatMember)
	}
	raw = `{"update_id":6,"message":{"message_id":9,"chat":{"id":42,"type":"private"},"from":{"id":42},
		"media_group_id":"g1","voice":{"file_id":"v"},"reply_to_message":{"message_id":3,"chat":{"id":42,"type":"private"},"from":{"id":1000,"is_bot":true}}}}`
	u = Update{}
	if err := json.Unmarshal([]byte(raw), &u); err != nil {
		t.Fatal(err)
	}
	m := u.Message
	if m.MediaGroupID != "g1" || m.ReplyToMessage == nil || m.ReplyToMessage.MessageID != 3 || !m.HasOtherMedia() {
		t.Fatalf("message: %+v", m)
	}
	if (&Message{Photo: []PhotoSize{{FileID: "a", Width: 1, Height: 1}}}).HasOtherMedia() {
		t.Fatal("a photo is not other media")
	}
	if p := (&Message{Photo: []PhotoSize{{FileID: "s", Width: 9, Height: 9}, {FileID: "l", Width: 90, Height: 90, FileSize: 7}}}).LargestPhotoSize(); p == nil || p.FileID != "l" || p.FileSize != 7 {
		t.Fatalf("largest size: %+v", p)
	}
	if (&Message{}).LargestPhotoSize() != nil {
		t.Fatal("no photo")
	}
}

func TestBulkHTTPClientHasItsOwnTransport(t *testing.T) {
	hc := NewBulkHTTPClient(8)
	tr, ok := hc.Transport.(*http.Transport)
	if !ok || tr == http.DefaultTransport || tr.MaxIdleConnsPerHost != 8 {
		t.Fatalf("transport: %+v", hc.Transport)
	}
}
