package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tgauth"
)

func TestMiniAppVerify(t *testing.T) {
	const botToken, core = "123456:BOT", "core-token"
	now := time.Unix(1_800_000_000, 0)
	h := MiniAppVerify(botToken, "acme_bot", core, func() time.Time { return now })
	call := func(auth, initData string) (*httptest.ResponseRecorder, map[string]any) {
		body, _ := json.Marshal(map[string]string{"init_data": initData})
		req := httptest.NewRequest(http.MethodPost, "/internal/miniapp/verify", strings.NewReader(string(body)))
		if auth != "" {
			req.Header.Set("Authorization", "Bearer "+auth)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec, out
	}
	good := tgauth.Sign(botToken, tgauth.User{ID: 9, Username: "u", LanguageCode: "en"}, now.Add(-time.Minute), nil)

	if rec, _ := call("", good); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: %d", rec.Code)
	}
	if rec, _ := call("other", good); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token: %d", rec.Code)
	}
	_, out := call(core, good)
	user, _ := out["user"].(map[string]any)
	if out["ok"] != true || user["id"] != float64(9) || user["language_code"] != "en" || out["bot_username"] != "acme_bot" {
		t.Fatalf("good: %v", out)
	}
	for name, c := range map[string]struct{ data, problem string }{
		"signed for another bot": {tgauth.Sign("1:other", tgauth.User{ID: 9}, now, nil), "signature"},
		"expired":                {tgauth.Sign(botToken, tgauth.User{ID: 9}, now.Add(-2*time.Hour), nil), "expired"},
		"garbage":                {"x", "malformed"},
	} {
		if _, out := call(core, c.data); out["ok"] != false || out["problem"] != c.problem {
			t.Errorf("%s: %v", name, out)
		}
	}
	if strings.Contains(good, botToken) {
		t.Fatal("the init data must not carry the token")
	}
}

// fakeMembers is a channel whose members the bot can see.
type fakeMembers struct {
	member   map[int64]tg.ChatMember // by Telegram id; anyone else is not in the channel
	err      error                   // every call fails
	asked    []string                // "<chat> <user id>" per call
	deadline time.Duration           // the time the last call had to answer in (0: no deadline)
}

func (f *fakeMembers) GetChatMember(ctx context.Context, chat string, userID int64) (*tg.ChatMember, error) {
	f.asked = append(f.asked, chat+" "+strconv.FormatInt(userID, 10))
	f.deadline = 0
	if d, ok := ctx.Deadline(); ok {
		f.deadline = time.Until(d)
	}
	if err := ctx.Err(); err != nil { // a real client gives up with its context
		return nil, err
	}
	if f.err != nil {
		return nil, f.err
	}
	if m, ok := f.member[userID]; ok {
		return &m, nil
	}
	return &tg.ChatMember{Status: "left"}, nil
}

func askMember(t *testing.T, h http.Handler, token, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/internal/miniapp/channel-member", strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec, out
}

func TestMiniAppChannelMember(t *testing.T) {
	bot := &fakeMembers{member: map[int64]tg.ChatMember{
		1: {Status: "creator"}, 2: {Status: "administrator"}, 3: {Status: "member"},
		4: {Status: "restricted", IsMember: true}, 5: {Status: "restricted"}, 6: {Status: "left"}, 7: {Status: "kicked"},
	}}
	h := MiniAppChannelMember(bot, coreTok, nil)
	ask := func(chat string, user int64) (*httptest.ResponseRecorder, map[string]any) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"chat": chat, "user_id": user})
		return askMember(t, h, coreTok, string(body))
	}

	// Only core may ask, and nothing is asked of Telegram for anyone else.
	for _, tok := range []string{"", "wrong-token-0123456789abcdef012"} {
		if rec, _ := askMember(t, h, tok, `{"chat":"@mynews","user_id":3}`); rec.Code != http.StatusUnauthorized {
			t.Fatalf("token %q: %d", tok, rec.Code)
		}
	}
	if len(bot.asked) != 0 {
		t.Fatalf("Telegram was asked for a caller without core's token: %v", bot.asked)
	}

	// A member is whoever the bot's own gate lets in; every other status is not one.
	want := map[int64]bool{1: true, 2: true, 3: true, 4: true, 5: false, 6: false, 7: false, 99: false}
	for user, member := range want {
		for _, chat := range []string{"@mynews", "-1001234567890"} {
			rec, out := ask(chat, user)
			if rec.Code != http.StatusOK || out["ok"] != true || out["member"] != member || len(out) != 2 {
				t.Errorf("user %d in %s: %d %v (want member %v)", user, chat, rec.Code, out, member)
			}
		}
	}
	if rec, _ := ask("@mynews", 3); rec.Header().Get("Content-Type") != "application/json" || rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("headers: %v", rec.Header())
	}
	// Telegram is asked about the channel and the customer core named, and about nobody else.
	if got := bot.asked[len(bot.asked)-1]; got != "@mynews 3" {
		t.Errorf("Telegram was asked %q", got)
	}
	// Telegram is given a limit, as the bot's own gate gives it, so that a channel that
	// does not answer cannot hold core's sign-ins for long.
	if bot.deadline <= 0 || bot.deadline > memberTimeout {
		t.Errorf("Telegram was given %v to answer", bot.deadline)
	}

	// Requests out of range are refused before Telegram is asked.
	before := len(bot.asked)
	for name, body := range map[string]string{
		"a chat that is no channel":  `{"chat":"mynews","user_id":3}`,
		"a user's chat":              `{"chat":"12345","user_id":3}`,
		"an invite link":             `{"chat":"https://t.me/+abcdefgh","user_id":3}`,
		"a short name":               `{"chat":"@ab","user_id":3}`,
		"no user":                    `{"chat":"@mynews"}`,
		"a negative user":            `{"chat":"@mynews","user_id":-5}`,
		"an unknown field":           `{"chat":"@mynews","user_id":3,"extra":1}`,
		"not json":                   `chat=@mynews`,
		"a body that is far too big": `{"chat":"@mynews","user_id":3,"pad":"` + strings.Repeat("x", 2<<10) + `"}`,
	} {
		rec, out := askMember(t, h, coreTok, body)
		if rec.Code != http.StatusBadRequest || out["ok"] != false || out["problem"] != ProblemBadRequest || out["member"] != nil {
			t.Errorf("%s: %d %v", name, rec.Code, out)
		}
	}
	if len(bot.asked) != before {
		t.Errorf("Telegram was asked for a request out of range: %v", bot.asked[before:])
	}
}

// When Telegram cannot say, the bot does not guess: a status that is not 200 and no "member" at
// all, so that the caller cannot mistake it for an answer.
func TestMiniAppChannelMemberWhenTelegramCannotSay(t *testing.T) {
	for name, c := range map[string]struct {
		err    error
		code   int
		retry  string
		detail string
	}{
		"the bot is not the channel's admin": {&tg.APIError{Code: 400, Description: "Bad Request: member list is inaccessible"}, http.StatusBadGateway, "", "member list is inaccessible"},
		"no such channel":                    {&tg.APIError{Code: 400, Description: "Bad Request: chat not found"}, http.StatusBadGateway, "", "chat not found"},
		"the bot was removed":                {&tg.APIError{Code: 403, Description: "Forbidden: bot is not a member of the channel chat"}, http.StatusBadGateway, "", "not a member of the channel chat"},
		"a flood wait":                       {&tg.APIError{Code: 429, Description: "Too Many Requests: retry after 7", RetryAfter: 7}, http.StatusTooManyRequests, "7", "retry after 7"},
		"a timeout":                          {context.DeadlineExceeded, http.StatusBadGateway, "", "deadline exceeded"},
		"a network failure":                  {errors.New("telegram: getChatMember: connection refused"), http.StatusBadGateway, "", "connection refused"},
	} {
		h := MiniAppChannelMember(&fakeMembers{err: c.err}, coreTok, nil)
		rec, out := askMember(t, h, coreTok, `{"chat":"@mynews","user_id":3}`)
		detail, _ := out["detail"].(string)
		if rec.Code != c.code || rec.Header().Get("Retry-After") != c.retry || out["ok"] != false || out["member"] != nil ||
			out["problem"] == "" || !strings.Contains(detail, c.detail) {
			t.Errorf("%s: %d %v retry-after %q", name, rec.Code, out, rec.Header().Get("Retry-After"))
		}
	}
}

// A channel Telegram cannot read fails for every customer, so the bot says so once a minute;
// the customer's id is never in its log.
func TestMiniAppChannelMemberLogsOnceAMinute(t *testing.T) {
	var logs bytes.Buffer
	h := MiniAppChannelMember(&fakeMembers{err: &tg.APIError{Code: 400, Description: "Bad Request: chat not found"}}, coreTok,
		slog.New(slog.NewTextHandler(&logs, nil)))
	for range 5 {
		askMember(t, h, coreTok, `{"chat":"@mynews","user_id":424242}`)
	}
	if n := strings.Count(logs.String(), "cannot check members"); n != 1 {
		t.Fatalf("%d warnings for 5 failures:\n%s", n, logs.String())
	}
	if !strings.Contains(logs.String(), "@mynews") || !strings.Contains(logs.String(), "chat not found") {
		t.Errorf("the warning does not say which channel or why:\n%s", logs.String())
	}
	if strings.Contains(logs.String(), "424242") || strings.Contains(logs.String(), coreTok) {
		t.Errorf("the log has the customer's id or core's token:\n%s", logs.String())
	}
}

// Core hanging up (it gives a check five seconds) is not the channel failing: it is not logged as that.
func TestMiniAppChannelMemberNotLoggedWhenCoreHangsUp(t *testing.T) {
	var logs bytes.Buffer
	h := MiniAppChannelMember(&fakeMembers{}, coreTok, slog.New(slog.NewTextHandler(&logs, nil)))
	gone, hangUp := context.WithCancel(context.Background())
	hangUp()
	req := httptest.NewRequest(http.MethodPost, "/internal/miniapp/channel-member", strings.NewReader(`{"chat":"@mynews","user_id":3}`)).WithContext(gone)
	req.Header.Set("Authorization", "Bearer "+coreTok)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if rec.Code != http.StatusBadGateway || out["ok"] != false || out["member"] != nil {
		t.Fatalf("a hung-up check: %d %v", rec.Code, out)
	}
	if logs.Len() != 0 {
		t.Fatalf("logged as the channel failing:\n%s", logs.String())
	}
}
