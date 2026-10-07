package tgfake

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
)

func TestFakeServesTheBotClient(t *testing.T) {
	f := New()
	srv := httptest.NewServer(f.Handler())
	defer srv.Close()
	c := tg.New("123:abc", tg.WithAPIRoot(srv.URL))
	ctx := context.Background()

	me, err := c.GetMe(ctx)
	if err != nil || me.Username != "fake_bot" {
		t.Fatalf("getMe: %v %+v", err, me)
	}
	if err := c.SetWebhook(ctx, "https://x.example/tg", "s"); err != nil || f.Webhook() != "https://x.example/tg" {
		t.Fatalf("setWebhook: %v %q", err, f.Webhook())
	}
	if err := c.DeleteWebhook(ctx); err != nil || f.Webhook() != "" {
		t.Fatalf("deleteWebhook: %v %q", err, f.Webhook())
	}

	// Nothing queued: getUpdates returns promptly after the (capped) timeout.
	start := time.Now()
	if ups, err := c.GetUpdates(ctx, 0, 1); err != nil || len(ups) != 0 || time.Since(start) > 3*time.Second {
		t.Fatalf("empty poll: %v %v after %v", ups, err, time.Since(start))
	}
	id := f.Inject(tg.Update{Message: &tg.Message{Chat: tg.Chat{ID: 7, Type: "private"}, From: &tg.User{ID: 7}, Text: "/start"}})
	ups, err := c.GetUpdates(ctx, 0, 1)
	if err != nil || len(ups) != 1 || ups[0].UpdateID != id || ups[0].Message.Text != "/start" {
		t.Fatalf("poll: %v %+v", err, ups)
	}
	if ups, _ := c.GetUpdates(ctx, id+1, 1); len(ups) != 0 {
		t.Fatalf("offset not honoured: %+v", ups)
	}

	kb := (&tg.Keyboard{}).Row(tg.CB("Buy", "buy"))
	if _, err := c.SendMessage(ctx, 7, "hello", kb); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SendPhoto(ctx, 7, tg.Photo{Data: []byte("png")}, "qr", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SendDocument(ctx, 7, "doc-1", "receipt", nil); err != nil {
		t.Fatal(err)
	}
	sent := f.Sent()
	if len(sent) != 3 || sent[0].ChatID != 7 || sent[0].Text != "hello" || !strings.Contains(string(sent[0].Markup), "buy") || sent[1].Method != "sendPhoto" || sent[1].Text != "qr" ||
		sent[2].Method != "sendDocument" || sent[2].Text != "receipt" {
		t.Fatalf("sent: %+v", sent)
	}
}

func TestHelperEndpointsAndAuth(t *testing.T) {
	f := New()
	srv := httptest.NewServer(f.Handler())
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/_inject", "application/json", strings.NewReader(`{"message":{"message_id":1,"chat":{"id":9,"type":"private"},"from":{"id":9},"text":"hi"}}`))
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("inject: %v %v", err, resp)
	}
	_ = resp.Body.Close()
	c := tg.New("1:x", tg.WithAPIRoot(srv.URL))
	if ups, _ := c.GetUpdates(context.Background(), 0, 1); len(ups) != 1 || ups[0].Message.Text != "hi" {
		t.Fatalf("injected update not served: %+v", ups)
	}
	_, _ = c.SendMessage(context.Background(), 9, "yo", nil)
	resp, err = http.Get(srv.URL + "/_sent")
	if err != nil {
		t.Fatal(err)
	}
	var sent []Sent
	_ = json.NewDecoder(resp.Body).Decode(&sent)
	_ = resp.Body.Close()
	if len(sent) != 1 || sent[0].Text != "yo" {
		t.Fatalf("/_sent: %+v", sent)
	}
	resp, err = http.Post(srv.URL+"/getMe", "application/json", strings.NewReader("{}"))
	if err != nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("missing bot token path: %v %v", err, resp.StatusCode)
	}
	_ = resp.Body.Close()
}

// Edits keep the edited message's id, so replaying the log gives each
// message's current state; toasts are recorded without a chat.
func TestEditsAndToastsAreRecorded(t *testing.T) {
	f := New()
	srv := httptest.NewServer(f.Handler())
	defer srv.Close()
	c := tg.New("1:x", tg.WithAPIRoot(srv.URL))
	ctx := context.Background()
	m, err := c.SendMessage(ctx, 5, "first", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.EditMessageText(ctx, 5, m.MessageID, "edited", nil); err != nil {
		t.Fatal(err)
	}
	m2, _ := c.SendMessage(ctx, 5, "second", nil)
	if err := c.AnswerCallback(ctx, "cb1", "done", false); err != nil {
		t.Fatal(err)
	}
	sent := f.Sent()
	if len(sent) != 4 || sent[1].MessageID != m.MessageID || sent[1].Text != "edited" ||
		m2.MessageID == m.MessageID || sent[3].Method != "answerCallbackQuery" || sent[3].Text != "done" || sent[3].ChatID != 0 {
		t.Fatalf("log: %+v (ids %d %d)", sent, m.MessageID, m2.MessageID)
	}
}

func TestStarsInvoiceAndPreCheckoutAreRecorded(t *testing.T) {
	f := New()
	srv := httptest.NewServer(f.Handler())
	defer srv.Close()
	c := tg.New("1:x", tg.WithAPIRoot(srv.URL))
	ctx := context.Background()
	if _, err := c.SendInvoice(ctx, 7, tg.Invoice{Title: "Monthly", Description: "d", Payload: "intent-1", Stars: 100, StartParameter: "buy"}); err != nil {
		t.Fatal(err)
	}
	if err := c.AnswerPreCheckoutQuery(ctx, "q1", false, "expired"); err != nil {
		t.Fatal(err)
	}
	sent := f.Sent()
	if len(sent) != 2 || sent[0].Method != "sendInvoice" || sent[0].Payload != "intent-1" || sent[0].Stars != 100 ||
		sent[1].QueryID != "q1" || sent[1].OK == nil || *sent[1].OK || sent[1].Text != "expired" {
		t.Fatalf("log: %+v", sent)
	}
}
