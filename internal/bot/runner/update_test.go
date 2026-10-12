package runner

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/release"
)

func TestUpdateCheckOnlyForCore(t *testing.T) {
	st := release.Status{Current: "1.0.0", Latest: "1.1.0", Channel: "stable", Newer: true, Verified: true, CheckedAt: 1760000000}
	h := UpdateCheck(func() release.Status { return st }, coreTok)
	for _, tok := range []string{"", "wrong-token-0123456789abcdef012"} {
		if code, _ := call(t, h, http.MethodGet, "/internal/update-check", tok); code != http.StatusUnauthorized {
			t.Fatalf("token %q: %d", tok, code)
		}
	}
	code, body := call(t, h, http.MethodGet, "/internal/update-check", coreTok)
	var got map[string]any
	if err := json.Unmarshal([]byte(body), &got); err != nil || code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	for k, want := range map[string]any{"current": "1.0.0", "latest": "1.1.0", "channel": "stable", "newer": true,
		"verified": true, "checked_at": float64(1760000000), "error": "", "detail": ""} {
		if got[k] != want {
			t.Errorf("%s: %v, want %v", k, got[k], want)
		}
	}
}
