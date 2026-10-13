package web

import (
	"fmt"
	"net/http"
	"testing"
)

// While every password-hashing slot is taken a login is refused at once
// (each check holds 32 MiB; queueing a burst runs core out of memory).
func TestPasswordLoginRefusedWhileHashingIsFull(t *testing.T) {
	f := newFixture(t)
	b := f.browser()
	for range cap(f.api.hashing) {
		f.api.hashing <- struct{}{}
	}
	code, out := b.do(http.MethodPost, "/api/v1/auth/login", map[string]string{"username": "nobody", "password": "x", "code": "1"})
	if code != http.StatusTooManyRequests || out["error"] != "rate_limited" {
		t.Fatalf("login while hashing is full: %d %v", code, out)
	}
	for range cap(f.api.hashing) {
		<-f.api.hashing
	}
	if code, out := b.do(http.MethodPost, "/api/v1/auth/login", map[string]string{"username": "nobody", "password": "x", "code": "1"}); code != http.StatusUnauthorized {
		t.Fatalf("login after the slots freed: %d %v", code, out)
	}
	if n := len(f.api.hashing); n != 0 {
		t.Fatalf("%d hashing slots still held after the logins", n)
	}
}

// Login attempts count per IPv6 /64, not per address: one host holds a
// whole /64 and could take a fresh address for every try.
func TestLoginRateLimitPerIPv6Prefix(t *testing.T) {
	f := newFixture(t)
	b := f.browser()
	last := 0
	for i := range 25 {
		last, _ = b.do(http.MethodPost, "/api/v1/auth/link", map[string]string{"token": "x"},
			"X-Forwarded-For", fmt.Sprintf("2001:db8:1:2::%x", i+1))
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("25 tries from one /64 not limited: %d", last)
	}
	// Another /64 and an IPv4 address have their own windows.
	if code, _ := b.do(http.MethodPost, "/api/v1/auth/link", map[string]string{"token": "x"}, "X-Forwarded-For", "2001:db8:1:3::1"); code == http.StatusTooManyRequests {
		t.Fatal("another /64 shares the limit")
	}
	if code, _ := b.do(http.MethodPost, "/api/v1/auth/link", map[string]string{"token": "x"}, "X-Forwarded-For", "203.0.113.7"); code == http.StatusTooManyRequests {
		t.Fatal("an IPv4 address shares the limit")
	}
}

func TestClientKey(t *testing.T) {
	for in, want := range map[string]string{
		"203.0.113.7":          "203.0.113.7",
		"::ffff:203.0.113.7":   "203.0.113.7",
		"2001:db8:1:2:aaaa::1": "2001:db8:1:2::/64",
		"not an address":       "not an address",
	} {
		r, _ := http.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("X-Forwarded-For", in)
		if got := clientKey(r); got != want {
			t.Errorf("clientKey(%q) = %q, want %q", in, got, want)
		}
	}
}
