package tgfake

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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

// The bottom menu (a reply keyboard) is recorded as sent; a reader of inline
// keyboards, like the end-to-end run, finds no buttons in it.
func TestReplyKeyboardIsRecorded(t *testing.T) {
	f := New()
	srv := httptest.NewServer(f.Handler())
	defer srv.Close()
	c := tg.New("1:x", tg.WithAPIRoot(srv.URL))
	m := (&tg.Menu{IsPersistent: true, Resize: true}).Row(tg.MenuButton{Text: "🛒 Buy", Style: tg.StyleSuccess})
	if _, err := c.SendMenu(context.Background(), 7, "welcome", m); err != nil {
		t.Fatal(err)
	}
	sent := f.Sent()
	if len(sent) != 1 || sent[0].Method != "sendMessage" || sent[0].Text != "welcome" ||
		!strings.Contains(string(sent[0].Markup), `"keyboard":[[{`) || !strings.Contains(string(sent[0].Markup), "🛒 Buy") {
		t.Fatalf("sent: %+v %s", sent, sent[0].Markup)
	}
	var inline struct {
		InlineKeyboard [][]tg.Button `json:"inline_keyboard"`
	}
	if err := json.Unmarshal(sent[0].Markup, &inline); err != nil || inline.InlineKeyboard != nil {
		t.Fatalf("read as an inline keyboard: %v %+v", err, inline)
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

// Failures injected per chat: a blocked user, a vanished chat, a flood wait
// for a few calls, another refusal; other chats are not affected.
func TestFailuresPerChat(t *testing.T) {
	f := New()
	srv := httptest.NewServer(f.Handler())
	defer srv.Close()
	flood := &tg.Flood{}
	c := tg.New("1:x", tg.WithAPIRoot(srv.URL), tg.WithNoRetry(), tg.WithFlood(flood))
	ctx := context.Background()

	f.FailChat(1, Blocked())
	f.FailChat(2, ChatNotFound())
	f.FailChat(3, Flood(2, 1))
	f.FailChat(4, BadRequest("message is too long"))
	for _, chat := range []int64{1, 2} {
		if _, err := c.SendMessage(ctx, chat, "x", nil); !tg.IsBlocked(err) {
			t.Fatalf("chat %d: %v", chat, err)
		}
	}
	_, err := c.SendMessage(ctx, 3, "x", nil)
	if wait, ok := tg.IsFlood(err); !ok || wait != 2*time.Second || flood.Until().IsZero() {
		t.Fatalf("flood: %v (%v)", err, wait)
	}
	if _, err := c.SendMessage(ctx, 3, "x", nil); err != nil {
		t.Fatalf("the flood was for one call only: %v", err)
	}
	var ae *tg.APIError
	if _, err := c.SendMessage(ctx, 4, "x", nil); !errors.As(err, &ae) || ae.Code != 400 || tg.IsBlocked(err) ||
		!strings.Contains(ae.Description, "too long") {
		t.Fatalf("bad request: %v", err)
	}
	if _, err := c.SendMessage(ctx, 5, "x", nil); err != nil {
		t.Fatalf("another chat: %v", err)
	}
	f.ClearChat(1)
	if _, err := c.SendMessage(ctx, 1, "x", nil); err != nil {
		t.Fatalf("cleared: %v", err)
	}
	// Over HTTP, for the end-to-end run; code 0 clears.
	resp, err := http.Post(srv.URL+"/_fail", "application/json", strings.NewReader(`{"chat_id":6,"code":403,"description":"Forbidden: user is deactivated"}`))
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("/_fail: %v %v", err, resp)
	}
	_ = resp.Body.Close()
	if _, err := c.SendMessage(ctx, 6, "x", nil); !tg.IsBlocked(err) {
		t.Fatalf("/_fail not applied: %v", err)
	}
	resp, err = http.Post(srv.URL+"/_fail", "application/json", strings.NewReader(`{"chat_id":6,"code":0}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if _, err := c.SendMessage(ctx, 6, "x", nil); err != nil {
		t.Fatalf("/_fail clear: %v", err)
	}
	if sent := f.Sent(); len(sent) != 4 {
		t.Fatalf("only delivered messages are recorded: %+v", sent)
	}

	// A sent photo comes back with a file id to re-send it by.
	m, err := c.SendPhoto(ctx, 5, tg.Photo{Data: []byte("png")}, "qr", nil)
	if err != nil || m.LargestPhoto() == "" {
		t.Fatalf("photo reply: %v %+v", err, m)
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

func TestDocumentUploadIsRecorded(t *testing.T) {
	f := New()
	srv := httptest.NewServer(f.Handler())
	defer srv.Close()
	c := tg.New("123:abc", tg.WithAPIRoot(srv.URL))
	data := bytes.Repeat([]byte{0xbb}, 3<<20)
	m, err := c.SendDocumentUpload(context.Background(), 77, "bobres-20261011T033000Z.bkp", bytes.NewReader(data), int64(len(data)), "the caption")
	if err != nil || m.Document == nil || m.Document.FileName != "bobres-20261011T033000Z.bkp" {
		t.Fatalf("upload: %v %+v", err, m)
	}
	sum := sha256.Sum256(data)
	sent := f.Sent()
	if len(sent) != 1 {
		t.Fatalf("sent: %+v", sent)
	}
	got := sent[0]
	if got.Method != "sendDocument" || got.ChatID != 77 || got.FileName != "bobres-20261011T033000Z.bkp" ||
		got.FileSize != int64(len(data)) || got.FileSHA256 != hex.EncodeToString(sum[:]) || got.Text != "the caption" {
		t.Errorf("recorded: %+v", got)
	}
}

// A form cut short (no closing boundary) is refused and not recorded, as
// Telegram would: it is not a smaller file.
func TestCutShortUploadIsRefused(t *testing.T) {
	f := New()
	srv := httptest.NewServer(f.Handler())
	defer srv.Close()
	form := "--b\r\nContent-Disposition: form-data; name=\"chat_id\"\r\n\r\n77\r\n" +
		"--b\r\nContent-Disposition: form-data; name=\"document\"; filename=\"x.bkp\"\r\n\r\npart of the file"
	resp, err := http.Post(srv.URL+"/bot123:abc/sendDocument", "multipart/form-data; boundary=b", strings.NewReader(form))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest || len(f.Sent()) != 0 {
		t.Fatalf("%d, sent %+v", resp.StatusCode, f.Sent())
	}
}

// The calls that set the bot's own profile are answered and recorded apart
// from the messages; a profile photo must come as Telegram wants it.
func TestProfileCallsAreAnsweredAndRecorded(t *testing.T) {
	f := New()
	srv := httptest.NewServer(f.Handler())
	defer srv.Close()
	c := tg.New("123:abc", tg.WithAPIRoot(srv.URL))
	ctx := context.Background()
	jpeg := bytes.Repeat([]byte{0xd8}, 2048)
	for _, call := range []func() error{
		func() error {
			return c.SetMyCommands(ctx, []tg.BotCommand{{Command: "start", Description: "Menu"}}, 0, "en")
		},
		func() error { return c.DeleteMyCommands(ctx, 42, "") },
		func() error { return c.SetMyDescription(ctx, "Buy a VPN", "en") },
		func() error { return c.SetMyShortDescription(ctx, "VPN store", "") },
		func() error { return c.SetMyProfilePhoto(ctx, jpeg) },
		func() error { return c.RemoveMyProfilePhoto(ctx) },
	} {
		if err := call(); err != nil {
			t.Fatal(err)
		}
	}
	calls := f.Profile()
	var methods []string
	for _, pc := range calls {
		methods = append(methods, pc.Method)
	}
	if strings.Join(methods, " ") != "setMyCommands deleteMyCommands setMyDescription setMyShortDescription setMyProfilePhoto removeMyProfilePhoto" {
		t.Fatalf("recorded: %v", methods)
	}
	if calls[2].Params["description"] != "Buy a VPN" || calls[2].Params["language_code"] != "en" {
		t.Errorf("description: %+v", calls[2])
	}
	sum := sha256.Sum256(jpeg)
	if p := calls[4]; p.FileName != "avatar.jpg" || p.FileSize != int64(len(jpeg)) || p.FileSHA256 != hex.EncodeToString(sum[:]) {
		t.Errorf("photo: %+v", p)
	}
	if len(f.Sent()) != 0 {
		t.Fatalf("profile calls recorded as messages: %+v", f.Sent())
	}

	// A photo that is not attached as a file is refused, like Telegram does.
	form := "--b\r\nContent-Disposition: form-data; name=\"photo\"\r\n\r\n{\"type\":\"static\",\"photo\":\"attach://other\"}\r\n" +
		"--b\r\nContent-Disposition: form-data; name=\"avatar\"; filename=\"avatar.jpg\"\r\n\r\njpeg\r\n--b--\r\n"
	resp, err := http.Post(srv.URL+"/bot123:abc/setMyProfilePhoto", "multipart/form-data; boundary=b", strings.NewReader(form))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest || len(f.Profile()) != len(calls) {
		t.Fatalf("a photo naming another part: %d, %d calls", resp.StatusCode, len(f.Profile()))
	}
}

// getUserProfilePhotos gives the bot the photo it set last, in Telegram's
// sizes; one set in @BotFather replaces it, and removeMyProfilePhoto takes
// off whichever is current. No other user has a photo.
func TestProfilePhotoIsReadBack(t *testing.T) {
	f := New()
	srv := httptest.NewServer(f.Handler())
	defer srv.Close()
	c := tg.New("123:abc", tg.WithAPIRoot(srv.URL))
	ctx := context.Background()
	current := func(userID int64) string {
		t.Helper()
		p, err := c.GetUserProfilePhotos(ctx, userID, 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(p.Photos) == 0 {
			if p.TotalCount != 0 {
				t.Fatalf("no photos, total %d", p.TotalCount)
			}
			return ""
		}
		sizes := p.Photos[0]
		if p.TotalCount != 1 || len(sizes) != 3 || sizes[2].Width != 640 || sizes[0].FileUniqueID == sizes[2].FileUniqueID {
			t.Fatalf("photos: %+v", p)
		}
		return sizes[2].FileUniqueID
	}

	if id := current(BotID); id != "" || f.ProfilePhoto() != "" {
		t.Fatalf("before any photo: %q", id)
	}
	if err := c.SetMyProfilePhoto(ctx, []byte("jpeg")); err != nil {
		t.Fatal(err)
	}
	own := current(BotID)
	if own == "" || own != f.ProfilePhoto() || current(42) != "" {
		t.Fatalf("set: %q, fake %q", own, f.ProfilePhoto())
	}
	if owners := f.SetProfilePhoto(); owners == own || current(BotID) != owners {
		t.Fatalf("set in @BotFather: %q, was %q", owners, own)
	}
	if err := c.RemoveMyProfilePhoto(ctx); err != nil {
		t.Fatal(err)
	}
	if id := current(BotID); id != "" || f.ProfilePhoto() != "" {
		t.Fatalf("removed: %q", id)
	}
	var methods []string
	for _, pc := range f.Profile() {
		methods = append(methods, pc.Method)
	}
	if strings.Join(methods, " ") != "setMyProfilePhoto removeMyProfilePhoto" {
		t.Fatalf("recorded: %v", methods) // reads are not profile changes
	}
}
