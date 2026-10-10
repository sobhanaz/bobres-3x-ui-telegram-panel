package runner

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
)

const coreTok = "core-token-0123456789abcdef0123"

func call(t *testing.T, h http.Handler, method, target, token string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	body, _ := io.ReadAll(rec.Body)
	return rec.Code, string(body)
}

func TestRefreshOnlyForCore(t *testing.T) {
	var calls atomic.Int32
	var fail atomic.Bool
	h := Refresh(func(context.Context) error {
		calls.Add(1)
		if fail.Load() {
			return errors.New("core unreachable")
		}
		return nil
	}, coreTok, slog.New(slog.DiscardHandler))
	for _, tok := range []string{"", "wrong-token-0123456789abcdef012"} {
		if code, _ := call(t, h, http.MethodPost, "/internal/settings/refresh", tok); code != http.StatusUnauthorized {
			t.Fatalf("token %q: %d", tok, code)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("refreshed for a caller without core's token")
	}
	if code, _ := call(t, h, http.MethodPost, "/internal/settings/refresh", coreTok); code != http.StatusNoContent || calls.Load() != 1 {
		t.Fatalf("refresh: %d after %d calls", code, calls.Load())
	}
	fail.Store(true)
	if code, _ := call(t, h, http.MethodPost, "/internal/settings/refresh", coreTok); code != http.StatusServiceUnavailable {
		t.Fatalf("failed refresh: %d", code)
	}
	// No token configured: nobody may call it.
	if code, _ := call(t, Refresh(func(context.Context) error { return nil }, "", nil), http.MethodPost, "/", ""); code != http.StatusUnauthorized {
		t.Fatalf("no token configured: %d", code)
	}
}

// fakeChats is a Telegram with a few chats and the bot's place in them.
type fakeChats struct {
	chats  map[string]tg.Chat
	status map[string]string // the bot's status per chat
	err    error             // every call fails
	asked  []string
}

func (f *fakeChats) GetChat(_ context.Context, chat string) (*tg.Chat, error) {
	f.asked = append(f.asked, "getChat "+chat)
	if f.err != nil {
		return nil, f.err
	}
	c, ok := f.chats[chat]
	if !ok {
		return nil, &tg.APIError{Code: 400, Description: "Bad Request: chat not found"}
	}
	return &c, nil
}

func (f *fakeChats) GetChatMember(_ context.Context, chat string, userID int64) (*tg.ChatMember, error) {
	f.asked = append(f.asked, "getChatMember "+chat)
	if userID != 42 {
		return nil, errors.New("asked about someone other than the bot")
	}
	st, ok := f.status[chat]
	if !ok {
		return nil, &tg.APIError{Code: 400, Description: "Bad Request: member list is inaccessible"}
	}
	return &tg.ChatMember{Status: st}, nil
}

func TestChannelCheck(t *testing.T) {
	bot := &fakeChats{
		chats: map[string]tg.Chat{
			"@mynews":        {ID: -1001, Type: "channel", Title: "My News"},
			"-1009876543210": {ID: -1009876543210, Type: "channel", Title: "Private"},
			"@hidden":        {ID: -1002, Type: "channel", Title: "Hidden"},
			"@friends":       {ID: -1003, Type: "supergroup", Title: "Friends"},
			"@readers":       {ID: -1004, Type: "channel", Title: "Readers"},
		},
		status: map[string]string{"@mynews": "administrator", "-1009876543210": "creator", "@readers": "member"},
	}
	h := ChannelCheck(bot, 42, coreTok)
	check := func(chat string) map[string]any {
		t.Helper()
		code, body := call(t, h, http.MethodGet, "/internal/channel-check?chat="+url.QueryEscape(chat), coreTok)
		if code != http.StatusOK {
			t.Fatalf("%s: %d %s", chat, code, body)
		}
		var out map[string]any
		if err := json.Unmarshal([]byte(body), &out); err != nil {
			t.Fatalf("%s: %v %s", chat, err, body)
		}
		return out
	}

	if got := check("@mynews"); got["ok"] != true || got["title"] != "My News" || got["bot_admin"] != true || len(got) != 3 {
		t.Fatalf("admin of a public channel: %v", got)
	}
	if got := check("-1009876543210"); got["ok"] != true || got["title"] != "Private" {
		t.Fatalf("private channel by id: %v", got)
	}
	for chat, problem := range map[string]string{
		"@nosuchchan": ProblemNotFound,
		"@hidden":     ProblemNotAdmin, // Telegram hides members from non-admins
		"@readers":    ProblemNotAdmin, // a plain member
		"@friends":    ProblemNotChannel,
	} {
		got := check(chat)
		if got["ok"] != false || got["problem"] != problem || got["detail"] == "" || len(got) != 3 {
			t.Errorf("%s: %v (want %s)", chat, got, problem)
		}
	}
	bot.err = errors.New("telegram: getChat: context deadline exceeded")
	if got := check("@mynews"); got["problem"] != ProblemError || !strings.Contains(got["detail"].(string), "deadline") {
		t.Fatalf("telegram unreachable: %v", got)
	}

	// Core's token only, and only channel ids the setting accepts.
	bot.asked = nil
	if code, _ := call(t, h, http.MethodGet, "/internal/channel-check?chat=@mynews", ""); code != http.StatusUnauthorized {
		t.Fatalf("no token: %d", code)
	}
	for _, bad := range []string{"", "mynews", "@abc", "@1abcdef", "-12345", "-100", "@my-news", "https://t.me/mynews"} {
		if code, _ := call(t, h, http.MethodGet, "/internal/channel-check?chat="+url.QueryEscape(bad), coreTok); code != http.StatusBadRequest {
			t.Errorf("chat %q: %d", bad, code)
		}
	}
	if len(bot.asked) != 0 {
		t.Fatalf("telegram was asked for refused requests: %v", bot.asked)
	}
}
