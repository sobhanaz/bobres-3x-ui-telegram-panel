package runner

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/settings"
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

func TestUpdateCheckNowOnlyForCore(t *testing.T) {
	var order []string
	st := release.Status{Current: "1.0.0", Latest: "1.1.0", Channel: "beta", Newer: true, Verified: true, CheckedAt: 1760000500}
	h := UpdateCheckNow(
		func(context.Context) error { order = append(order, "settings"); return nil },
		func(context.Context) release.Status { order = append(order, "check"); return st },
		coreTok, nil)
	for _, tok := range []string{"", "wrong-token-0123456789abcdef012"} {
		if code, _ := call(t, h, http.MethodPost, "/internal/update-check/refresh", tok); code != http.StatusUnauthorized {
			t.Fatalf("token %q: %d", tok, code)
		}
	}
	if len(order) != 0 {
		t.Fatalf("an unauthorized call reloaded settings or asked GitHub: %v", order)
	}
	code, body := call(t, h, http.MethodPost, "/internal/update-check/refresh", coreTok)
	var got map[string]any
	if err := json.Unmarshal([]byte(body), &got); err != nil || code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	// the settings come first: the channel just saved is the one that is looked at
	if strings.Join(order, ",") != "settings,check" {
		t.Fatalf("order: %v", order)
	}
	for k, want := range map[string]any{"current": "1.0.0", "latest": "1.1.0", "channel": "beta", "newer": true, "verified": true,
		"checked_at": float64(1760000500), "error": "", "detail": ""} {
		if got[k] != want {
			t.Errorf("%s: %v, want %v", k, got[k], want)
		}
	}
}

// A failed look is an answer: its error and detail say why. Core being out of
// reach for the settings does not stop the look.
func TestUpdateCheckNowAnswersFailures(t *testing.T) {
	failed := release.Status{Current: "1.0.0", Channel: "stable", CheckedAt: 1760000600, Error: release.ErrUnreachable, Detail: "checksums.txt: timeout"}
	var asked atomic.Int32
	h := UpdateCheckNow(
		func(context.Context) error { return errors.New("core unreachable") },
		func(context.Context) release.Status { asked.Add(1); return failed },
		coreTok, slog.New(slog.DiscardHandler))
	code, body := call(t, h, http.MethodPost, "/internal/update-check/refresh", coreTok)
	var got release.Status
	if err := json.Unmarshal([]byte(body), &got); err != nil || code != http.StatusOK || got != failed || asked.Load() != 1 {
		t.Fatalf("%d %s (looked %d times)", code, body, asked.Load())
	}
	// no settings reload wired: the check still runs
	h = UpdateCheckNow(nil, func(context.Context) release.Status { return failed }, coreTok, nil)
	if code, _ := call(t, h, http.MethodPost, "/internal/update-check/refresh", coreTok); code != http.StatusOK {
		t.Fatalf("without a reload: %d", code)
	}
}

// The mux the bot mounts it on lets only POST reach it: a GET of the cached
// outcome is GET /internal/update-check.
func TestUpdateCheckNowIsPostOnly(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle("POST /internal/update-check/refresh", UpdateCheckNow(nil, func(context.Context) release.Status { return release.Status{} }, coreTok, nil))
	req := httptest.NewRequest(http.MethodGet, "/internal/update-check/refresh", nil)
	req.Header.Set("Authorization", "Bearer "+coreTok)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET: %d", rec.Code)
	}
}

// The bot's check follows the store's updates.channel setting, and
// BOBRES_UPDATE_CHANNEL (the fallback) when the setting is unset or nonsense.
func TestFollowUpdateChannel(t *testing.T) {
	for _, tc := range []struct{ setting, env, want string }{
		{"", "stable", "stable"},
		{"", "beta", "beta"},
		{"beta", "stable", "beta"},
		{"stable", "beta", "stable"},
		{"nightly", "beta", "beta"},
		{" Beta ", "stable", "beta"},
	} {
		set := settings.New()
		set.Replace(map[string]string{release.SettingChannel: tc.setting})
		c := release.NewChecker("1.0.0", tc.env, nil)
		FollowUpdateChannel(c, set.Get, tc.env)()
		if c.Channel() != tc.want {
			t.Errorf("setting %q, env %q: follows %q, want %q", tc.setting, tc.env, c.Channel(), tc.want)
		}
	}

	// a change of the setting, seen at the next reload, moves the check; the
	// same value again does not (it would reset what the check found)
	set := settings.New()
	c := release.NewChecker("1.0.0", "stable", nil)
	follow := FollowUpdateChannel(c, set.Get, "stable")
	follow()
	set.Replace(map[string]string{release.SettingChannel: "beta"})
	follow()
	if c.Channel() != "beta" || c.Status().Channel != "beta" || c.Status().Error != release.ErrNotChecked {
		t.Fatalf("after the owner chose beta: %q %+v", c.Channel(), c.Status())
	}
	set.Replace(map[string]string{})
	follow() // unset again: back to the server's default (.env)
	if c.Channel() != "stable" {
		t.Fatalf("after the setting was removed: %q", c.Channel())
	}
}
