package sui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/xui"
)

func TestClientAnswers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/app/apiv2/clients":
			if r.Header.Get("Token") != "tok" {
				_, _ = w.Write([]byte(`{"success":false,"msg":": invalid token","obj":null}`))
				return
			}
			_, _ = w.Write([]byte(`{"success":false,"msg":"clients: database is locked","obj":null}`))
		case "/app/apiv2/inbounds":
			_, _ = w.Write([]byte(`<html>login</html>`))
		default:
			http.Redirect(w, r, "/app/login", http.StatusTemporaryRedirect)
		}
	}))
	defer srv.Close()
	opts := []xui.Option{xui.AllowInsecureHTTP(), xui.AllowPrivateAddresses()}
	ctx := context.Background()

	bad, _ := New(srv.URL+"/app", "nope", opts...)
	if _, err := bad.Clients(ctx); !IsUnauthorized(err) || !Answered(err) {
		t.Fatalf("refused token: %v", err)
	}
	c, err := New(srv.URL+"/app/", "tok", opts...)
	if err != nil || c.Base() != srv.URL+"/app" {
		t.Fatalf("New: %v %q", err, c.Base())
	}
	_, err = c.Clients(ctx)
	if err == nil || IsUnauthorized(err) || !strings.HasSuffix(err.Error(), "database is locked") || strings.Contains(err.Error(), "clients:") {
		t.Fatalf("refusal: %v", err)
	}
	if _, err := c.Inbounds(ctx); err == nil || !strings.Contains(err.Error(), "not an S-UI panel") {
		t.Fatalf("a page that is not the API: %v", err)
	}
	if _, err := c.Settings(ctx); err == nil || !strings.Contains(err.Error(), "web path") {
		t.Fatalf("a redirect: %v", err)
	}
	if IsNotFound(err) || !IsNotFound(&NotFoundError{Name: "x"}) || !Answered(&NotFoundError{Name: "x"}) {
		t.Fatal("NotFoundError")
	}
	for _, tok := range []string{"", "a b", strings.Repeat("x", MaxTokenLen+1)} {
		if _, err := New(srv.URL, tok, opts...); err == nil {
			t.Errorf("token %q taken", tok)
		}
	}
}

func TestRandomConfigs(t *testing.T) {
	a, err := RandomConfigs("u1")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := RandomConfigs("u1")
	if a["vless"]["uuid"] == b["vless"]["uuid"] || a["vless"]["uuid"] != a["vmess"]["uuid"] || a["mixed"]["username"] != "u1" || a["trojan"]["name"] != "u1" {
		t.Fatalf("configs %v / %v", a["vless"], b["vless"])
	}
	if id, _ := a["vless"]["uuid"].(string); len(id) != 36 || id[14] != '4' {
		t.Fatalf("uuid %q", id)
	}
	if pw, _ := a["mixed"]["password"].(string); len(pw) != 10 {
		t.Fatalf("password %q", pw)
	}
}
