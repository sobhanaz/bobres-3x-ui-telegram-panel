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
