package domain

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// fakeGateways is payments' gateway settings: it keeps the secrets (as the
// real one does) and answers only hints.
type fakeGateways struct {
	mu       sync.Mutex
	cfg      GatewayConfig
	merchant string
	proxy    string
	actor    string
	tested   []*GatewayCandidate
	report   *GatewayReport
	sets     int
}

func (f *fakeGateways) GatewayConfigs(context.Context) ([]GatewayConfig, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return []GatewayConfig{f.cfg}, nil
}

func (f *fakeGateways) SetGatewayConfig(_ context.Context, u GatewayUpdate, actorID string) (*GatewayConfig, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sets++
	f.actor = actorID
	if u.MerchantID != "" {
		f.merchant = u.MerchantID
	}
	switch {
	case u.ClearProxy:
		f.proxy = ""
	case u.Proxy != "":
		f.proxy = u.Proxy
	}
	f.cfg.Enabled, f.cfg.Sandbox, f.cfg.Configured = u.Enabled, u.Sandbox, f.merchant != ""
	f.cfg.ProxySet, f.cfg.Offered = f.proxy != "", u.Enabled && f.merchant != ""
	if f.merchant != "" {
		f.cfg.MerchantHint = f.merchant[len(f.merchant)-4:]
	}
	out := f.cfg
	return &out, nil
}

func (f *fakeGateways) TestGateway(_ context.Context, _ string, c *GatewayCandidate) (*GatewayTest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tested = append(f.tested, c)
	return &GatewayTest{OK: true, LatencyMS: 12}, nil
}

func (f *fakeGateways) GatewayReport(context.Context, string, int64, int64) (*GatewayReport, error) {
	return f.report, nil
}

const (
	testMerchant = "0b7e2f3a-1c9d-4e5f-8a6b-7c8d9e0f1a2b"
	testProxy    = "http://user:pr0xy-passw0rd@203.0.113.10:3128" //nolint:gosec // test fixture
)

// The owner's changes reach payments and the audit says what changed, never
// the merchant id or the proxy (which can carry a password).
func TestGatewaySettingsAreAuditedWithoutSecrets(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	own := owner(t, svc)
	fg := &fakeGateways{cfg: GatewayConfig{Provider: Zarinpal}}

	// Persian digits and capitals are what an owner may paste.
	typed := "۰B7E2F3A-1C9D-4E5F-8A6B-7C8D9E0F1A2B"
	c, err := svc.SetGatewayConfig(ctx, own, fg, GatewayUpdate{Provider: "Zarinpal", Enabled: true, MerchantID: typed, Proxy: testProxy})
	if err != nil || !c.Offered || c.MerchantHint != "1a2b" {
		t.Fatalf("set: %+v %v", c, err)
	}
	if fg.merchant != testMerchant || fg.proxy != testProxy || fg.actor != own.ID {
		t.Fatalf("payments got %q %q from %q", fg.merchant, fg.proxy, fg.actor)
	}
	// Switching off only, then clearing the proxy, then nothing at all.
	if _, err := svc.SetGatewayConfig(ctx, own, fg, GatewayUpdate{Provider: Zarinpal}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetGatewayConfig(ctx, own, fg, GatewayUpdate{Provider: Zarinpal, ClearProxy: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetGatewayConfig(ctx, own, fg, GatewayUpdate{Provider: Zarinpal, ClearProxy: true}); err != nil {
		t.Fatal(err)
	}
	entries := auditJSON(t, s, "gateway.update")
	if len(entries) != 3 {
		t.Fatalf("entries (a save that changes nothing is not one): %v", entries)
	}
	if e := entries[0]; !strings.Contains(e, `"merchant_id": "changed"`) || !strings.Contains(e, `"proxy": "changed"`) ||
		!strings.Contains(e, `"enabled": true`) {
		t.Fatalf("first entry: %s", e)
	}
	if e := entries[1]; strings.Contains(e, "merchant_id") || strings.Contains(e, "proxy") {
		t.Fatalf("switching off names a secret: %s", e)
	}
	if e := entries[2]; !strings.Contains(e, `"proxy": "cleared"`) {
		t.Fatalf("clearing: %s", e)
	}
	var all string
	if err := s.DB().QueryRow(ctx, `SELECT string_agg(coalesce(before::text,'') || coalesce(after::text,''), ' ') FROM core.audit_log`).Scan(&all); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{testMerchant, "0b7e2f3a", "pr0xy-passw0rd", "203.0.113.10"} {
		if strings.Contains(all, secret) {
			t.Fatalf("%q reached the audit log: %s", secret, all)
		}
	}

	// Refused before payments sees it, and never repeated back.
	sets := fg.sets
	for field, u := range map[string]GatewayUpdate{
		"merchant_id": {Provider: Zarinpal, MerchantID: "not-a-merchant-" + "s3cr3t"},
		"proxy":       {Provider: Zarinpal, Proxy: "socks5://user:s3cr3t@203.0.113.10:1080"},
		"proxy ":      {Provider: Zarinpal, Proxy: testProxy, ClearProxy: true},
	} {
		_, err := svc.SetGatewayConfig(ctx, own, fg, u)
		var fe *FieldError
		if !errors.As(err, &fe) || fe.Field != strings.TrimSpace(field) || strings.Contains(err.Error(), "s3cr3t") ||
			strings.Contains(err.Error(), "pr0xy") {
			t.Errorf("%s: %v", field, err)
		}
	}
	if _, err := svc.SetGatewayConfig(ctx, own, fg, GatewayUpdate{Provider: "paypal"}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown gateway: %v", err)
	}
	if fg.sets != sets {
		t.Fatal("a refused change reached payments")
	}

	// Tests: nil means the stored settings; a candidate is checked the same way.
	if _, err := svc.TestGateway(ctx, fg, Zarinpal, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.TestGateway(ctx, fg, Zarinpal, &GatewayCandidate{Sandbox: true, MerchantID: typed}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.TestGateway(ctx, fg, Zarinpal, &GatewayCandidate{Proxy: "ftp://x"}); err == nil {
		t.Fatal("a bad proxy was tested")
	}
	if len(fg.tested) != 2 || fg.tested[0] != nil || fg.tested[1].MerchantID != testMerchant || !fg.tested[1].Sandbox {
		t.Fatalf("tested: %+v", fg.tested)
	}
}

func TestGatewayHealthRule(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	ev := func(ago time.Duration) *GatewayEvent { return &GatewayEvent{At: now.Add(-ago).Unix()} }
	for name, c := range map[string]struct {
		r    GatewayReport
		want bool
	}{
		"nothing yet":            {GatewayReport{}, true},
		"fresh error":            {GatewayReport{LastError: ev(time.Minute)}, false},
		"paid since the error":   {GatewayReport{LastError: ev(time.Hour), LastPaid: ev(time.Minute)}, true},
		"error since the paying": {GatewayReport{LastError: ev(time.Minute), LastPaid: ev(time.Hour)}, false},
		"an old error":           {GatewayReport{LastError: ev(2 * time.Hour)}, true},
	} {
		if got := c.r.Healthy(now); got != c.want {
			t.Errorf("%s: %v", name, got)
		}
	}
}

// The manual methods and Stars have on/off switches (on by default), and a
// switched-off method is neither listed nor started, whatever the bot shows.
func TestPaymentMethodSwitches(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	pay := &fakePayments{gateways: []string{Stars, Zarinpal}, id: "00000000-0000-7000-8000-0000000000c1", status: "pending"}
	setSetting(t, s, "payments.stars_rate", "1500")
	setSetting(t, s, "payments.card_number", "6037991122223333")
	u := seedUser(t, s, 9501)
	start := func(provider, key string) error {
		_, err := svc.CreatePaymentIntent(ctx, pay, CreatePaymentIntentParams{UserID: u.ID, Provider: provider,
			Amount: 150_000, Currency: "IRT", IdempotencyKey: key})
		return err
	}
	if ms, _ := svc.PaymentMethods(ctx, pay, "IRT", 150_000); !slices.Equal(ms, []string{Stars, Zarinpal}) {
		t.Fatalf("default: %v", ms)
	}
	for _, p := range []string{"manual_card", "manual_crypto", "manual_zarinpal", Stars, Zarinpal} {
		if err := start(p, "on-"+p); err != nil {
			t.Fatalf("%s while on: %v", p, err)
		}
	}
	for _, sw := range []string{"payments.stars.enabled", "payments.manual_card.enabled", "payments.manual_crypto.enabled", "payments.manual_zarinpal.enabled"} {
		norm, err := NormalizeSetting(sw, "off")
		if err != nil || norm != "false" {
			t.Fatalf("%s: %q %v", sw, norm, err)
		}
		setSetting(t, s, sw, norm)
	}
	if ms, _ := svc.PaymentMethods(ctx, pay, "IRT", 150_000); !slices.Equal(ms, []string{Zarinpal}) {
		t.Fatalf("Stars switched off but listed: %v", ms)
	}
	for _, p := range []string{"manual_card", "manual_crypto", "manual_zarinpal", Stars} {
		if err := start(p, "off-"+p); !errors.Is(err, ErrState) {
			t.Errorf("%s while off: %v", p, err)
		}
	}
	// Zarinpal is switched in payments, not here.
	if err := start(Zarinpal, "off-zarinpal"); err != nil {
		t.Fatalf("zarinpal: %v", err)
	}
	setSetting(t, s, "payments.manual_card.enabled", "true")
	if err := start("manual_card", "back-card"); err != nil {
		t.Fatalf("switched back on: %v", err)
	}
	// Owner-only settings, with the payments group.
	for _, key := range []string{"payments.stars.enabled", "payments.manual_card.enabled", "payments.manual_crypto.enabled", "payments.manual_zarinpal.enabled"} {
		sp, ok := SpecOf(key)
		if !ok || sp.Kind != KindBool || sp.Perm != permGateways || sp.Default != "true" || sp.Group != "payments" || MethodToggle(strings.Split(key, ".")[1]) != key {
			t.Errorf("%s: %+v", key, sp)
		}
	}
	adm := staffMember(t, s, 9502, "admin")
	if err := svc.AdminSetSetting(ctx, adm.TelegramID, "payments.stars.enabled", "true"); err == nil {
		t.Fatal("an admin switched a payment method")
	}
}
