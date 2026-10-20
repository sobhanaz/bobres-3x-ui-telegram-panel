package botfiles

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestVerifyMiniApp(t *testing.T) {
	answer, code := "", http.StatusOK
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/internal/miniapp/verify" || r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(code)
		_, _ = w.Write([]byte(answer))
	}))
	defer srv.Close()
	c := New(srv.URL, "tok")
	ctx := context.Background()

	answer = `{"ok":true,"bot_username":"acme_bot","user":{"id":42,"username":"ali","first_name":"Ali","language_code":"fa"},"auth_date":1760000000}`
	u, err := c.VerifyMiniApp(ctx, "query_id=1&hash=ab")
	if err != nil || *u != (MiniAppUser{ID: 42, Username: "ali", FirstName: "Ali", LanguageCode: "fa", BotUsername: "acme_bot"}) {
		t.Fatalf("got %+v, %v", u, err)
	}
	if !strings.Contains(gotBody, `"init_data":"query_id=1\u0026hash=ab"`) && !strings.Contains(gotBody, `"init_data":"query_id=1&hash=ab"`) {
		t.Fatalf("body %q", gotBody)
	}

	// The bot says the data is not valid: an auth refusal, not a backend fault.
	answer = `{"ok":false,"problem":"signature"}`
	if _, err := c.VerifyMiniApp(ctx, "x"); !errors.Is(err, ErrMiniAppAuth) {
		t.Fatalf("a refusal: %v", err)
	}
	// An answer that says ok with no user is not trusted.
	answer = `{"ok":true,"user":{"id":0}}`
	if _, err := c.VerifyMiniApp(ctx, "x"); err == nil || errors.Is(err, ErrMiniAppAuth) {
		t.Fatalf("no user: %v", err)
	}
	// Bot trouble is a plain error, never a login.
	code, answer = http.StatusInternalServerError, `{"ok":true,"user":{"id":1}}`
	if _, err := c.VerifyMiniApp(ctx, "x"); err == nil || errors.Is(err, ErrMiniAppAuth) {
		t.Fatalf("500: %v", err)
	}
	code, answer = http.StatusOK, `not json`
	if _, err := c.VerifyMiniApp(ctx, "x"); err == nil {
		t.Fatal("garbage was taken as an answer")
	}
	if _, err := New(srv.URL, "wrong").VerifyMiniApp(ctx, "x"); err == nil || errors.Is(err, ErrMiniAppAuth) {
		t.Fatalf("a wrong token: %v", err)
	}
}

func TestChannelMember(t *testing.T) {
	answer, code := "", http.StatusOK
	var gotBody, gotType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/internal/miniapp/channel-member" || r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		b, _ := io.ReadAll(r.Body)
		gotBody, gotType = string(b), r.Header.Get("Content-Type")
		w.WriteHeader(code)
		_, _ = w.Write([]byte(answer))
	}))
	defer srv.Close()
	c := New(srv.URL, "tok")
	ctx := context.Background()

	// The bot says yes or no.
	answer = `{"ok":true,"member":true}`
	if member, err := c.ChannelMember(ctx, "@mynews", 42); err != nil || !member {
		t.Fatalf("a member: %v %v", member, err)
	}
	if gotBody != `{"chat":"@mynews","user_id":42}` || gotType != "application/json" {
		t.Fatalf("request: %q as %q", gotBody, gotType)
	}
	answer = `{"ok":true,"member":false}`
	if member, err := c.ChannelMember(ctx, "-1001234567890", 7); err != nil || member {
		t.Fatalf("not a member: %v %v", member, err)
	}
	if gotBody != `{"chat":"-1001234567890","user_id":7}` {
		t.Fatalf("a private channel: %q", gotBody)
	}

	// Anything but a clear yes or no is an error, never a "no" (and never a "yes"): the caller
	// decides what not knowing lets through.
	for name, c2 := range map[string]struct {
		code   int
		answer string
		want   string // a part of the error
	}{
		"telegram could not tell":   {http.StatusBadGateway, `{"ok":false,"problem":"error","detail":"Bad Request: chat not found"}`, "answered 502 error: Bad Request: chat not found"},
		"a flood wait":              {http.StatusTooManyRequests, `{"ok":false,"problem":"flood","detail":"retry after 7"}`, "answered 429 flood"},
		"an older bot":              {http.StatusNotFound, "404 page not found\n", "answered 404"},
		"a refused token":           {http.StatusUnauthorized, "unauthorized\n", "answered 401"},
		"ok with no answer":         {http.StatusOK, `{"ok":true}`, "no answer"},
		"an answer of not ok":       {http.StatusOK, `{"ok":false,"problem":"error"}`, "no answer"},
		"an answer of no ok":        {http.StatusOK, `{"member":true}`, "no answer"},
		"not json":                  {http.StatusOK, "member", "no answer"},
		"an empty answer":           {http.StatusOK, "", "no answer"},
		"a member as a string":      {http.StatusOK, `{"ok":true,"member":"yes"}`, "no answer"},
		"an error with a long text": {http.StatusBadGateway, `{"ok":false,"problem":"error","detail":"` + strings.Repeat("x", 5000) + `"}`, "answered 502"},
	} {
		code, answer = c2.code, c2.answer
		member, err := c.ChannelMember(ctx, "@mynews", 42)
		if err == nil || member || !strings.Contains(err.Error(), c2.want) {
			t.Errorf("%s: %v %v (want an error with %q)", name, member, err, c2.want)
		} else if len(err.Error()) > 400 || strings.Contains(err.Error(), "tok") {
			t.Errorf("%s: the error is long or has the token: %q", name, err)
		}
	}

	// The wrong token, a stopped bot, a call cancelled.
	code, answer = http.StatusOK, `{"ok":true,"member":true}`
	if member, err := New(srv.URL, "wrong").ChannelMember(ctx, "@mynews", 42); err == nil || member {
		t.Errorf("a wrong token: %v %v", member, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if member, err := c.ChannelMember(cancelled, "@mynews", 42); err == nil || member {
		t.Errorf("a cancelled call: %v %v", member, err)
	}
	srv.Close()
	if member, err := c.ChannelMember(ctx, "@mynews", 42); err == nil || member {
		t.Errorf("a bot that is down: %v %v", member, err)
	}
}
