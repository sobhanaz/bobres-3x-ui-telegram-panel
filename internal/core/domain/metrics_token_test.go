package domain

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

func storedMetricsHash(t *testing.T, svc *Service) (string, bool) {
	t.Helper()
	v, err := svc.Settings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	s, ok := v[SettingMetricsToken]
	return s, ok
}

// Off until a token is set; a token core makes is returned once and only its SHA-256 is kept; the check says
// whether the endpoint exists and whether the token is the one.
func TestMetricsTokenLifecycle(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	own := owner(t, svc)

	if on, err := svc.MetricsEnabled(ctx); err != nil || on {
		t.Fatalf("a new install: %v %v", on, err)
	}
	if enabled, ok, err := svc.CheckMetricsToken(ctx, "anything"); err != nil || enabled || ok {
		t.Fatalf("no token set, a token tried: enabled=%v ok=%v %v", enabled, ok, err)
	}

	token, err := svc.GenerateMetricsToken(ctx, own)
	if err != nil || !strings.HasPrefix(token, "bm_") || len(token) != 46 {
		t.Fatalf("generate: %q %v", token, err)
	}
	sum := sha256.Sum256([]byte(token))
	if got, ok := storedMetricsHash(t, svc); !ok || got != hex.EncodeToString(sum[:]) {
		t.Fatalf("stored %q, want the SHA-256 of the token", got)
	}
	if enabled, ok, err := svc.CheckMetricsToken(ctx, token); err != nil || !enabled || !ok {
		t.Fatalf("the token: enabled=%v ok=%v %v", enabled, ok, err)
	}
	for _, wrong := range []string{"", "bm_", token + "x", token[:len(token)-1], strings.ToUpper(token), " " + token, "Bearer " + token} {
		if enabled, ok, err := svc.CheckMetricsToken(ctx, wrong); err != nil || !enabled || ok {
			t.Errorf("%q: enabled=%v ok=%v %v", wrong, enabled, ok, err)
		}
	}

	// A second token replaces the first at once.
	next, err := svc.GenerateMetricsToken(ctx, own)
	if err != nil || next == token {
		t.Fatalf("regenerate: %q %v", next, err)
	}
	if _, ok, _ := svc.CheckMetricsToken(ctx, token); ok {
		t.Error("the replaced token still works")
	}
	if _, ok, _ := svc.CheckMetricsToken(ctx, next); !ok {
		t.Error("the new token does not work")
	}

	// A token of the owner's own: its length and characters are checked, and the check is the same.
	for _, bad := range []string{"", "short", strings.Repeat("a", MetricsTokenMin-1), strings.Repeat("a", MetricsTokenMax+1),
		strings.Repeat("a", 30) + " b", strings.Repeat("a", 15) + "\n" + strings.Repeat("a", 15), strings.Repeat("a", 30) + "ا", strings.Repeat("a", 30) + `"`} {
		var fe *FieldError
		if err := svc.SetMetricsToken(ctx, own, bad); !errors.As(err, &fe) || fe.Field != "token" {
			t.Errorf("%q: %v", bad, err)
		}
	}
	mine := "my-own.token_0123456789~+/=ABC"
	if err := svc.SetMetricsToken(ctx, own, "  "+mine+"  "); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := svc.CheckMetricsToken(ctx, mine); !ok {
		t.Error("the owner's token does not work")
	}
	if _, ok, _ := svc.CheckMetricsToken(ctx, next); ok {
		t.Error("the token it replaced still works")
	}

	// Turning it off: 404 again, and removing none is quiet.
	if err := svc.ClearMetricsToken(ctx, own); err != nil {
		t.Fatal(err)
	}
	if enabled, ok, err := svc.CheckMetricsToken(ctx, mine); err != nil || enabled || ok {
		t.Fatalf("after clearing: enabled=%v ok=%v %v", enabled, ok, err)
	}
	if err := svc.ClearMetricsToken(ctx, own); err != nil {
		t.Fatal(err)
	}
	if n := auditCount(t, s, "metrics.token.generate"); n != 2 {
		t.Errorf("generate audited %d times", n)
	}
	if auditCount(t, s, "metrics.token.set") != 1 || auditCount(t, s, "metrics.token.clear") != 1 {
		t.Errorf("set %d, clear %d", auditCount(t, s, "metrics.token.set"), auditCount(t, s, "metrics.token.clear"))
	}
}

// Nothing of the token is in the audit log, not even its hash, and the token is not a setting the Settings page or the
// bot can name; a damaged value opens nothing.
func TestMetricsTokenLeavesNoTrace(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	own := owner(t, svc)
	token, err := svc.GenerateMetricsToken(ctx, own)
	if err != nil {
		t.Fatal(err)
	}
	mine := "my-own-token-0123456789ABCDEF"
	if err := svc.SetMetricsToken(ctx, own, mine); err != nil {
		t.Fatal(err)
	}
	hash, _ := storedMetricsHash(t, svc)
	var all string
	if err := s.DB().QueryRow(ctx, `SELECT coalesce(string_agg(coalesce(before::text,'') || coalesce(after::text,'') || coalesce(reason,'') || coalesce(entity_id,''), ' '), '') FROM core.audit_log`).Scan(&all); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{token, mine, hash, strings.TrimPrefix(token, "bm_")} {
		if strings.Contains(all, secret) {
			t.Fatalf("the audit log holds %q", secret)
		}
	}
	if _, ok := SpecOf(SettingMetricsToken); ok {
		t.Error("the metrics token is a setting of the Settings page")
	}
	if got := SettingPerm(SettingMetricsToken); got != "" {
		t.Errorf("a permission for the bot's /set: %q", got)
	}
	if _, err := NormalizeSetting(SettingMetricsToken, mine); err == nil {
		t.Error("the setting can be written as a plain setting")
	}

	// A stored value that is not a SHA-256 in hex is no token at all: /metrics stays closed.
	for _, bad := range []string{`""`, `"not hex"`, `"abcd"`, `42`, `null`, `"` + strings.Repeat("a", 63) + `"`} {
		if _, err := s.DB().Exec(ctx, `INSERT INTO core.settings (key, value) VALUES ($1, $2::text::jsonb)
			ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, SettingMetricsToken, bad); err != nil {
			t.Fatal(err)
		}
		if on, err := svc.MetricsEnabled(ctx); err != nil || on {
			t.Errorf("%s: enabled=%v %v", bad, on, err)
		}
		if enabled, ok, _ := svc.CheckMetricsToken(ctx, mine); enabled || ok {
			t.Errorf("%s: a damaged value opens the endpoint", bad)
		}
	}
}
