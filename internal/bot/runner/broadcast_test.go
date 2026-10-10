package runner

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
)

// fakeSender answers by chat: 1 blocked, 2 a refused message, 3 no answer,
// others delivered.
type fakeSender struct {
	mu   sync.Mutex
	sent []tg.Keyboard
}

func (f *fakeSender) SendMessage(_ context.Context, chat int64, _ string, kb *tg.Keyboard) (*tg.Message, error) {
	switch chat {
	case 1:
		return nil, &tg.APIError{Code: 403, Description: "Forbidden: bot was blocked by the user"}
	case 2:
		return nil, &tg.APIError{Code: 400, Description: "Bad Request: BUTTON_URL_INVALID"}
	case 3:
		return nil, errors.New("telegram: sendMessage: context deadline exceeded")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if kb == nil {
		kb = &tg.Keyboard{}
	}
	f.sent = append(f.sent, *kb)
	return &tg.Message{MessageID: 77}, nil
}

func TestBroadcastTestEndpoint(t *testing.T) {
	bot := &fakeSender{}
	h := BroadcastTest(bot, coreTok, nil)
	post := func(token, body string) (int, map[string]any, string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/internal/broadcasts/test", strings.NewReader(body))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		raw, _ := io.ReadAll(rec.Body)
		var out map[string]any
		_ = json.Unmarshal(raw, &out)
		return rec.Code, out, string(raw)
	}

	for _, tok := range []string{"", "wrong-token-0123456789abcdef012"} {
		if code, _, _ := post(tok, `{"chat_id":5,"text":"hi"}`); code != http.StatusUnauthorized {
			t.Fatalf("token %q: %d", tok, code)
		}
	}
	code, out, raw := post(coreTok, `{"chat_id":5,"text":"<b>Hi</b> all","button":{"text":"Open","url":"https://example.com/x"}}`)
	if code != http.StatusOK || out["ok"] != true || out["message_id"] != float64(77) || len(out) != 2 {
		t.Fatalf("sent: %d %s", code, raw)
	}
	if len(bot.sent) != 1 || bot.sent[0].InlineKeyboard[0][0].URL != "https://example.com/x" || bot.sent[0].InlineKeyboard[0][0].Text != "Open" {
		t.Fatalf("button: %+v", bot.sent)
	}
	if code, out, raw := post(coreTok, `{"chat_id":5,"text":"plain","button":null}`); code != http.StatusOK || out["ok"] != true ||
		len(bot.sent[1].InlineKeyboard) != 0 {
		t.Fatalf("no button: %d %s", code, raw)
	}

	for body, problem := range map[string]string{
		`{"chat_id":1,"text":"hi"}`:    ProblemBlocked,
		`{"chat_id":2,"text":"hi"}`:    ProblemBadRequest,
		`{"chat_id":3,"text":"hi"}`:    ProblemError,
		`{"chat_id":5,"text":"<b>hi"}`: ProblemBadRequest, // checked again: never sent
		`{"chat_id":5,"text":""}`:      ProblemBadRequest,
		`{"chat_id":5,"text":"hi","button":{"text":"Go","url":"http://example.com"}}`: ProblemBadRequest,
		`{"chat_id":5,"text":"hi","button":{"text":"","url":"https://example.com"}}`:  ProblemBadRequest,
	} {
		code, out, raw := post(coreTok, body)
		if code != http.StatusOK || out["ok"] != false || out["problem"] != problem || out["detail"] == "" || len(out) != 3 {
			t.Errorf("%s: %d %s (want %s)", body, code, raw, problem)
		}
	}
	if len(bot.sent) != 2 {
		t.Fatalf("refused messages were sent: %d", len(bot.sent))
	}
	for _, body := range []string{`not json`, `{"chat_id":0,"text":"hi"}`, `{"chat_id":-100123,"text":"hi"}`} {
		if code, _, _ := post(coreTok, body); code != http.StatusBadRequest {
			t.Errorf("%s: %d", body, code)
		}
	}
	if code, _, _ := post(coreTok, `{"chat_id":5,"text":"`+strings.Repeat("a", 17<<10)+`"}`); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body: %d", code)
	}
}
