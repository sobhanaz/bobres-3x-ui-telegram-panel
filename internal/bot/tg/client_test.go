package tg

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const token = "123456:SECRET-bot-token" //gitleaks:allow test fixture

func fakeAPI(t *testing.T, h func(method string, body []byte, r *http.Request) any) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/bot"+token+"/") {
			http.Error(w, "bad path", http.StatusNotFound)
			return
		}
		method := strings.TrimPrefix(r.URL.Path, "/bot"+token+"/")
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(h(method, body, r))
	}))
	t.Cleanup(srv.Close)
	return New(token, WithBaseURL(srv.URL+"/bot"+token)), srv
}

func ok(result any) map[string]any { return map[string]any{"ok": true, "result": result} }

func TestSendMessageAndUpdates(t *testing.T) {
	var got map[string]any
	c, _ := fakeAPI(t, func(method string, body []byte, _ *http.Request) any {
		switch method {
		case "sendMessage":
			_ = json.Unmarshal(body, &got)
			return ok(map[string]any{"message_id": 7, "chat": map[string]any{"id": 42}})
		case "getUpdates":
			return ok([]map[string]any{{"update_id": 9, "message": map[string]any{"message_id": 1, "chat": map[string]any{"id": 42}, "text": "/start", "from": map[string]any{"id": 42}}}})
		}
		return map[string]any{"ok": false, "error_code": 404, "description": "Not Found"}
	})
	ctx := context.Background()
	kb := (&Keyboard{}).Row(CB("Buy", "buy"))
	m, err := c.SendMessage(ctx, 42, "<b>hi</b>", kb)
	if err != nil || m.MessageID != 7 {
		t.Fatalf("send: %v %+v", err, m)
	}
	if got["parse_mode"] != "HTML" || got["reply_markup"] == nil {
		t.Errorf("request: %v", got)
	}
	ups, err := c.GetUpdates(ctx, 0, 1)
	if err != nil || len(ups) != 1 || ups[0].Message.Text != "/start" || ups[0].Sender().ID != 42 {
		t.Fatalf("updates: %v %+v", err, ups)
	}
}

func TestErrorsNeverContainTheToken(t *testing.T) {
	c, srv := fakeAPI(t, func(string, []byte, *http.Request) any {
		return map[string]any{"ok": false, "error_code": 403, "description": "Forbidden: bot was blocked by the user"}
	})
	_, err := c.SendMessage(context.Background(), 1, "x", nil)
	if !IsBlocked(err) || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("blocked error: %v", err)
	}
	srv.Close() // transport error: url.Error would include the URL
	_, err = c.SendMessage(context.Background(), 1, "x", nil)
	if err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("transport error leaks the token: %v", err)
	}
}

func TestRetriesOnceAfter429(t *testing.T) {
	var calls atomic.Int32
	c, _ := fakeAPI(t, func(string, []byte, *http.Request) any {
		if calls.Add(1) == 1 {
			return map[string]any{"ok": false, "error_code": 429, "description": "Too Many Requests", "parameters": map[string]any{"retry_after": 1}}
		}
		return ok(true)
	})
	start := time.Now()
	if err := c.AnswerCallback(context.Background(), "cb", "", false); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 || time.Since(start) < 900*time.Millisecond {
		t.Fatalf("calls=%d after %v", calls.Load(), time.Since(start))
	}
}

func TestEditNotModifiedIsFine(t *testing.T) {
	c, _ := fakeAPI(t, func(string, []byte, *http.Request) any {
		return map[string]any{"ok": false, "error_code": 400, "description": "Bad Request: message is not modified"}
	})
	if err := c.EditMessageText(context.Background(), 1, 2, "same", nil); err != nil {
		t.Fatalf("not-modified edit: %v", err)
	}
}

func TestSendPhotoUploadsAndByFileID(t *testing.T) {
	var uploaded, byID bool
	c, _ := fakeAPI(t, func(method string, body []byte, r *http.Request) any {
		if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
			uploaded = strings.Contains(string(body), "PNGDATA") && strings.Contains(string(body), "inline_keyboard")
		} else {
			var m map[string]any
			_ = json.Unmarshal(body, &m)
			byID = m["photo"] == "file-123"
		}
		return ok(map[string]any{"message_id": 3, "chat": map[string]any{"id": 1}})
	})
	ctx := context.Background()
	kb := (&Keyboard{}).Row(CB("Back", "home"))
	if _, err := c.SendPhoto(ctx, 1, Photo{Data: []byte("PNGDATA")}, "qr", kb); err != nil || !uploaded {
		t.Fatalf("upload: %v uploaded=%v", err, uploaded)
	}
	if _, err := c.SendPhoto(ctx, 1, Photo{FileID: "file-123"}, "receipt", nil); err != nil || !byID {
		t.Fatalf("by file id: %v %v", err, byID)
	}
}

func TestSendDocumentByFileID(t *testing.T) {
	var got map[string]any
	var called string
	c, _ := fakeAPI(t, func(method string, body []byte, _ *http.Request) any {
		called = method
		_ = json.Unmarshal(body, &got)
		return ok(map[string]any{"message_id": 4, "chat": map[string]any{"id": 1}})
	})
	kb := (&Keyboard{}).Row(CB("Approve", "adm:ok:1"))
	if _, err := c.SendDocument(context.Background(), 1, "doc-9", "<b>receipt</b>", kb); err != nil {
		t.Fatal(err)
	}
	if called != "sendDocument" || got["document"] != "doc-9" || got["caption"] != "<b>receipt</b>" ||
		got["parse_mode"] != "HTML" || got["reply_markup"] == nil {
		t.Fatalf("sendDocument %s %v", called, got)
	}
}

func TestLargestPhoto(t *testing.T) {
	m := &Message{Photo: []PhotoSize{{FileID: "s", Width: 90, Height: 90}, {FileID: "l", Width: 1280, Height: 960}, {FileID: "m", Width: 320, Height: 240}}}
	if m.LargestPhoto() != "l" {
		t.Fatalf("got %q", m.LargestPhoto())
	}
}

// A text Telegram refuses as HTML (a broken override) still arrives: once
// more, without parse_mode, tags stripped and entities unescaped.
func TestRefusedMarkupIsSentAsPlainText(t *testing.T) {
	type call struct {
		method, text string
		html         bool
	}
	var calls []call
	c, _ := fakeAPI(t, func(method string, body []byte, r *http.Request) any {
		var got call
		if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
			_, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
			form, err := multipart.NewReader(bytes.NewReader(body), params["boundary"]).ReadForm(1 << 20)
			if err != nil {
				t.Fatal(err)
			}
			got = call{method: method, text: form.Value["caption"][0], html: len(form.Value["parse_mode"]) > 0}
		} else {
			var m map[string]any
			_ = json.Unmarshal(body, &m)
			text, _ := m["text"].(string)
			if text == "" {
				text, _ = m["caption"].(string)
			}
			got = call{method: method, text: text, html: m["parse_mode"] == "HTML"}
		}
		calls = append(calls, got)
		if got.html && strings.Contains(got.text, "<oops>") {
			return map[string]any{"ok": false, "error_code": 400,
				"description": "Bad Request: can't parse entities: Unsupported start tag \"oops\" at byte offset 0"}
		}
		return ok(map[string]any{"message_id": 5, "chat": map[string]any{"id": 1}})
	})
	ctx := context.Background()
	const broken, plain = "<oops><b>Price</b> &lt; 5 &amp; &quot;fast&quot;", `Price < 5 & "fast"`
	if _, err := c.SendMessage(ctx, 1, broken, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.EditMessageText(ctx, 1, 5, broken, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SendPhoto(ctx, 1, Photo{FileID: "file-1"}, broken, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SendPhoto(ctx, 1, Photo{Data: []byte("PNGDATA")}, broken, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SendDocument(ctx, 1, "doc-1", broken, nil); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 10 {
		t.Fatalf("want each call once as HTML and once plain: %+v", calls)
	}
	for i := 0; i < len(calls); i += 2 {
		first, retry := calls[i], calls[i+1]
		if !first.html || first.text != broken || retry.method != first.method || retry.html || retry.text != plain {
			t.Errorf("%s: %+v then %+v", first.method, first, retry)
		}
	}

	// Valid markup goes out once, as HTML; other errors are not retried.
	calls = nil
	if _, err := c.SendMessage(ctx, 1, "<b>ok</b>", nil); err != nil || len(calls) != 1 || !calls[0].html {
		t.Fatalf("valid markup: %v %+v", err, calls)
	}
	if IsParseError(&APIError{Code: 400, Description: "Bad Request: chat not found"}) ||
		!IsParseError(&APIError{Code: 400, Description: "Bad Request: can't parse entities: x"}) {
		t.Fatal("IsParseError")
	}
}

func TestGetChatAndMember(t *testing.T) {
	var bodies []map[string]any
	c, _ := fakeAPI(t, func(method string, body []byte, _ *http.Request) any {
		var m map[string]any
		_ = json.Unmarshal(body, &m)
		bodies = append(bodies, m)
		switch method {
		case "getChat":
			return ok(map[string]any{"id": -1001234567890, "type": "channel", "title": "News", "username": "mynews"})
		case "getChatMember":
			if m["user_id"] == float64(7) {
				return ok(map[string]any{"status": "restricted", "is_member": true, "user": map[string]any{"id": 7}})
			}
			return ok(map[string]any{"status": "left", "user": map[string]any{"id": 8}})
		}
		return map[string]any{"ok": false, "error_code": 404, "description": "Not Found"}
	})
	ctx := context.Background()
	ch, err := c.GetChat(ctx, "@mynews")
	if err != nil || ch.Type != "channel" || ch.Title != "News" || ch.Username != "mynews" || bodies[0]["chat_id"] != "@mynews" {
		t.Fatalf("getChat: %v %+v %v", err, ch, bodies)
	}
	m, err := c.GetChatMember(ctx, "-1001234567890", 7)
	if err != nil || !m.Joined() || m.IsAdmin() || bodies[1]["chat_id"] != float64(-1001234567890) {
		t.Fatalf("restricted member: %v %+v %v", err, m, bodies[1])
	}
	if m, _ := c.GetChatMember(ctx, "@mynews", 8); m.Joined() {
		t.Fatalf("a user who left is not a member: %+v", m)
	}
	for status, want := range map[string][2]bool{
		"creator": {true, true}, "administrator": {true, true}, "member": {true, false},
		"restricted": {false, false}, "left": {false, false}, "kicked": {false, false},
	} {
		m := ChatMember{Status: status}
		if m.Joined() != want[0] || m.IsAdmin() != want[1] {
			t.Errorf("%s: joined %v admin %v", status, m.Joined(), m.IsAdmin())
		}
	}
}
