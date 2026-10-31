package hiddify

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/xui"
)

const key = "0b5e6a3c-2f4d-4e8a-9c1b-7d6e5f4a3b2c" //gitleaks:allow test fixture

func TestNewTakesTheAdminPath(t *testing.T) {
	opts := []xui.Option{xui.AllowInsecureHTTP(), xui.AllowPrivateAddresses()}
	for in, want := range map[string]string{
		"https://p.example.com/Adm1n":                         "/Adm1n",
		"https://p.example.com/Adm1n/":                        "/Adm1n",
		"https://p.example.com/Adm1n/" + key + "/admin/":      "/Adm1n",
		"https://p.example.com:8443/Adm1n/admin/v2/?x=1#frag": "/Adm1n",
	} {
		c, err := New(in, key, opts...)
		if err != nil || c.adminPath != want {
			t.Errorf("%s: path %q, %v", in, c.adminPath, err)
		}
	}
	if _, err := New("https://p.example.com/", key, opts...); err == nil {
		t.Error("no admin path taken")
	}
	if _, err := New("https://p.example.com/x", "admin:pw", opts...); err == nil {
		t.Error("a key that is not a UUID taken")
	}
}

func TestClientAnswers(t *testing.T) {
	empty := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Hiddify-API-Key") != key {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"detail":{},"message":"Unauthorized"}`))
			return
		}
		switch {
		case r.Method == http.MethodPost:
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"detail":{"json":{"name":["Field required"]}},"message":"Validation error"}`))
		case r.URL.Path == "/A/api/v2/admin/user/" && empty:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"detail":{},"message":"You have no user"}`))
		case r.URL.Path == "/A/api/v2/admin/user/":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`<html>not found</html>`))
		case strings.HasPrefix(r.URL.Path, "/A/api/v2/admin/user/"):
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"detail":{},"message":"User not found"}`))
		case r.URL.Path == "/A/api/v2/admin/users/":
			_, _ = w.Write([]byte(`{"users":[],"link_domains":[{"kind":"current","base":"https://p.example.com/Cl1ent/"}]}`))
		default:
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"detail":{"json":{"name":["Field required"]}},"message":"Validation error"}`))
		}
	}))
	defer srv.Close()
	opts := []xui.Option{xui.AllowInsecureHTTP(), xui.AllowPrivateAddresses()}
	ctx := context.Background()
	c, err := New(srv.URL+"/A", key, opts...)
	if err != nil {
		t.Fatal(err)
	}
	if users, err := c.Users(ctx); err != nil || len(users) != 0 {
		t.Fatalf("no user: %v %v", users, err)
	}
	empty = false
	if _, err := c.Users(ctx); err == nil || IsNotFound(err) {
		t.Fatalf("a 404 that is not the panel's: %v", err)
	}
	if _, err := c.User(ctx, key); !IsNotFound(err) || !Answered(err) {
		t.Fatalf("missing user: %v", err)
	}
	if _, err := c.User(ctx, "x"); err == nil || Answered(err) {
		t.Fatalf("a bad UUID reached the panel: %v", err)
	}
	if p, err := c.ClientPath(ctx); err != nil || p != "/Cl1ent" {
		t.Fatalf("client path %q %v", p, err)
	}
	_, err = c.CreateUser(ctx, map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "Validation error") || !strings.Contains(err.Error(), "Field required") {
		t.Fatalf("validation: %v", err)
	}
	bad, _ := New(srv.URL+"/A", "11111111-2222-4333-8444-555555555555", opts...)
	if err := bad.Me(ctx); !IsUnauthorized(err) {
		t.Fatalf("refused key: %v", err)
	}
	if !IsExists(&APIError{Status: 400, Msg: "The user exists"}) || IsExists(&APIError{Status: 400, Msg: "other"}) {
		t.Fatal("IsExists")
	}
}
