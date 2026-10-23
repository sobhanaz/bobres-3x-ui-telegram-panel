package server

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
)

// The bot is told the store's own Terms and Privacy pages (its public HTTPS address plus /terms and /privacy) while the owner has set no address of
// their own, so its buttons and /terms work from the first day; the owner's own address wins; a store with no public address gets none; and the
// pages' texts never go to the bot.
func TestSettingsCarryTheStoresOwnLegalAddresses(t *testing.T) {
	var origin atomic.Pointer[string]
	shop := "https://shop.example.com"
	origin.Store(&shop)
	f := newFixtureWith(t, func(s *Server) { s.SetPublicURLFunc(func(context.Context) string { return *origin.Load() }) })
	ctx := context.Background()
	for _, k := range []string{"branding.terms_url", "branding.privacy_url", "legal.terms.en"} {
		if _, err := f.st.DB().Exec(ctx, `DELETE FROM core.settings WHERE key = $1`, k); err != nil {
			t.Fatal(err)
		}
	}
	c := f.client(t, botToken)
	values := func() map[string]string {
		t.Helper()
		s, err := c.GetSettings(ctx, &corev1.GetSettingsRequest{})
		if err != nil {
			t.Fatal(err)
		}
		return s.GetValues()
	}
	if v := values(); v["branding.terms_url"] != `"https://shop.example.com/terms"` || v["branding.privacy_url"] != `"https://shop.example.com/privacy"` {
		t.Fatalf("defaults: %v %v", v["branding.terms_url"], v["branding.privacy_url"])
	}
	if _, err := f.st.DB().Exec(ctx, `INSERT INTO core.settings (key, value) VALUES ('branding.terms_url', '"https://acme.example/terms"'), ('legal.terms.en', '"# My terms"')`); err != nil {
		t.Fatal(err)
	}
	v := values()
	if v["branding.terms_url"] != `"https://acme.example/terms"` || v["branding.privacy_url"] != `"https://shop.example.com/privacy"` {
		t.Fatalf("the owner's address: %v %v", v["branding.terms_url"], v["branding.privacy_url"])
	}
	if _, ok := v["legal.terms.en"]; ok {
		t.Error("a page's text went to the bot")
	}
	// No public HTTPS address: no default.
	plain := "http://10.0.0.5:8080"
	origin.Store(&plain)
	if v := values(); v["branding.privacy_url"] != "" {
		t.Errorf("a default for a plain-http address: %q", v["branding.privacy_url"])
	}
}

// The bot is told the rate the store charges: the live price (plus the margin) while it is fresh, else the owner's own, else none.
func TestSettingsCarryTheEffectiveUsdtRate(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	for _, k := range []string{"payments.usdt_rate", "payments.usdt_rate_auto", "payments.usdt_rate_margin", "state.usdt_rate.live"} {
		if _, err := f.st.DB().Exec(ctx, `DELETE FROM core.settings WHERE key = $1`, k); err != nil {
			t.Fatal(err)
		}
	}
	c := f.client(t, botToken)
	rate := func() string {
		t.Helper()
		s, err := c.GetSettings(ctx, &corev1.GetSettingsRequest{})
		if err != nil {
			t.Fatal(err)
		}
		return s.GetValues()["payments.usdt_rate"]
	}
	if got := rate(); got != "" {
		t.Fatalf("no rate at all: %q", got)
	}
	live := fmt.Sprintf(`{"rate":271600,"at":%d,"sources":["tetherland"]}`, time.Now().Unix())
	if _, err := f.st.DB().Exec(ctx, `INSERT INTO core.settings (key, value) VALUES ('state.usdt_rate.live', $1::jsonb), ('payments.usdt_rate', '"60000"'), ('payments.usdt_rate_margin', '"1"')`, live); err != nil {
		t.Fatal(err)
	}
	if got := rate(); got != `"274316"` { // 271600 + 1%, rounded up
		t.Fatalf("live plus the margin: %q", got)
	}
	if _, err := f.st.DB().Exec(ctx, `INSERT INTO core.settings (key, value) VALUES ('payments.usdt_rate_auto', '"false"')`); err != nil {
		t.Fatal(err)
	}
	if got := rate(); got != `"60000"` {
		t.Fatalf("automatic off: %q", got)
	}
}
