package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func cfgStore(t *testing.T) *Store {
	t.Helper()
	s := testStore(t)
	if _, err := s.DB().Exec(context.Background(), `TRUNCATE payments.gateway_configs, payments.gateway_events`); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestGatewayConfigSeedAndSave(t *testing.T) {
	s := cfgStore(t)
	ctx := context.Background()
	if _, err := s.GetGatewayConfig(ctx, nil, "zarinpal"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty: %v", err)
	}
	ok, err := s.SeedGatewayConfig(ctx, &GatewayConfig{Provider: "zarinpal", Enabled: true, MerchantEnc: []byte("m1"), ProxyEnc: []byte("p1")})
	if err != nil || !ok {
		t.Fatalf("seed: %v %v", ok, err)
	}
	// The seed never overwrites stored settings.
	if ok, err := s.SeedGatewayConfig(ctx, &GatewayConfig{Provider: "zarinpal", MerchantEnc: []byte("m2")}); err != nil || ok {
		t.Fatalf("second seed: %v %v", ok, err)
	}
	c, err := s.GetGatewayConfig(ctx, nil, "zarinpal")
	if err != nil || string(c.MerchantEnc) != "m1" || string(c.ProxyEnc) != "p1" || !c.Enabled || c.UpdatedBy != nil {
		t.Fatalf("seeded: %+v %v", c, err)
	}
	actor := "00000000-0000-7000-8000-00000000b0a1"
	saved, err := s.SaveGatewayConfig(ctx, nil, &GatewayConfig{Provider: "zarinpal", Sandbox: true, MerchantEnc: []byte("m3"), UpdatedBy: &actor})
	if err != nil || saved.Enabled || !saved.Sandbox || string(saved.MerchantEnc) != "m3" || saved.ProxyEnc != nil ||
		saved.UpdatedBy == nil || *saved.UpdatedBy != actor || !saved.UpdatedAt.After(c.UpdatedAt) {
		t.Fatalf("save: %+v %v", saved, err)
	}
	// The schema refuses what the domain never writes.
	for name, bad := range map[string]*GatewayConfig{
		"switched on without a merchant id": {Provider: "zarinpal", Enabled: true},
		"unknown provider":                  {Provider: "paypal", MerchantEnc: []byte("m")},
	} {
		if _, err := s.SaveGatewayConfig(ctx, nil, bad); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

// intentAt inserts an intent created at a given time, as the report sees it.
func intentAt(t *testing.T, s *Store, provider, status, currency string, amount int64, gateway *Money, created time.Time, ext, reason string, expires *time.Time) string {
	t.Helper()
	ctx := context.Background()
	in := &Intent{UserID: uid, Provider: provider, Amount: amount, Currency: currency, Status: "pending",
		IdempotencyKey: fmt.Sprintf("r-%d", time.Now().UnixNano()), ExpiresAt: expires}
	if gateway != nil {
		in.GatewayAmount, in.GatewayCurrency = &gateway.Amount, &gateway.Currency
	}
	got, err := s.CreateIntent(ctx, nil, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(ctx, `UPDATE payments.payment_intents SET status = $2, created_at = $3,
		external_id = NULLIF($4, ''), failure_reason = NULLIF($5, '') WHERE id = $1`, got.ID, status, created, ext, reason); err != nil {
		t.Fatal(err)
	}
	return got.ID
}

func TestGatewayReport(t *testing.T) {
	s := cfgStore(t)
	ctx := context.Background()
	now := time.Now()
	from, to := now.Add(-7*24*time.Hour), now.Add(time.Minute)
	rial := func(n int64) *Money { return &Money{Amount: n, Currency: "IRR"} }
	day := func(d int) time.Time { return now.Add(-time.Duration(d) * 24 * time.Hour) }

	paid1 := intentAt(t, s, "zarinpal", "succeeded", "IRT", 150_000, rial(1_500_000), day(1), "A1", "", nil)
	intentAt(t, s, "zarinpal", "succeeded", "IRT", 50_000, rial(500_000), day(2), "A2", "", nil)
	intentAt(t, s, "zarinpal", "succeeded", "USDT", 3, rial(2_000_000), day(2), "A3", "", nil)
	intentAt(t, s, "zarinpal", "expired", "IRT", 10_000, rial(100_000), day(3), "A4", "expired", nil)
	intentAt(t, s, "zarinpal", "expired", "IRT", 10_000, rial(100_000), day(3), "A5", "expired", nil)
	intentAt(t, s, "zarinpal", "failed", "IRT", 10_000, rial(100_000), day(3), "A6", "amount_mismatch", nil)
	oldOpen := intentAt(t, s, "zarinpal", "pending", "IRT", 10_000, rial(100_000), day(30), "A7", "", nil) // open, outside the period
	intentAt(t, s, "zarinpal", "pending", "IRT", 10_000, rial(100_000), day(1), "A8", "", nil)
	intentAt(t, s, "zarinpal", "pending", "IRT", 10_000, rial(100_000), day(40), "", "", nil)       // creation failed: never open
	intentAt(t, s, "zarinpal", "succeeded", "IRT", 999_000, rial(9_990_000), day(9), "A9", "", nil) // outside the period
	intentAt(t, s, "stars", "succeeded", "IRT", 70_000, &Money{Amount: 120, Currency: "XTR"}, day(1), "", "", nil)

	if err := s.RecordGatewayEvent(ctx, nil, "", "zarinpal", "create", "error",
		map[string]any{"error": "zarinpal: request failed: proxyconnect tcp: dial tcp 203.0.113.10:3128: connect: connection refused"}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordGatewayEvent(ctx, nil, paid1, "zarinpal", "check", "paid", map[string]any{"reference": "R1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordGatewayEvent(ctx, nil, "", "zarinpal", "create", "error", map[string]any{"error": "zarinpal: error -11 (HTTP 401): terminal [::1]:8443 inactive"}); err != nil {
		t.Fatal(err)
	}

	r, err := s.GatewayReport(ctx, "zarinpal", from, to)
	if err != nil {
		t.Fatal(err)
	}
	totals := map[string]StatusTotal{}
	for _, tt := range r.Totals {
		totals[tt.Status] = tt
	}
	ok := totals["succeeded"]
	if len(r.Totals) != 4 || ok.Count != 3 || len(ok.Amounts) != 2 || ok.Amounts[0] != (Money{200_000, "IRT"}) ||
		ok.Amounts[1] != (Money{3, "USDT"}) || len(ok.GatewayAmounts) != 1 || ok.GatewayAmounts[0] != (Money{4_000_000, "IRR"}) {
		t.Fatalf("succeeded: %+v (all %+v)", ok, r.Totals)
	}
	if totals["expired"].Count != 2 || totals["failed"].Count != 1 || totals["pending"].Count != 1 {
		t.Fatalf("totals: %+v", r.Totals)
	}
	if len(r.Failures) != 2 || r.Failures[0] != (ReasonCount{"expired", 2}) || r.Failures[1] != (ReasonCount{"amount_mismatch", 1}) {
		t.Fatalf("failures: %+v", r.Failures)
	}
	// Open now, whatever the period: the old pending one counts, the one
	// whose creation failed does not.
	var oldCreated time.Time
	if err := s.DB().QueryRow(ctx, `SELECT created_at FROM payments.payment_intents WHERE id = $1`, oldOpen).Scan(&oldCreated); err != nil {
		t.Fatal(err)
	}
	if r.OpenCount != 2 || r.OldestOpen == nil || !r.OldestOpen.Equal(oldCreated) {
		t.Fatalf("open: %d %v", r.OpenCount, r.OldestOpen)
	}
	if r.LastPaid == nil || r.LastPaid.IntentID != paid1 || r.LastPaid.Kind != "check" {
		t.Fatalf("last paid: %+v", r.LastPaid)
	}
	if r.LastError == nil || r.LastError.Detail != "zarinpal: error -11 (HTTP 401): terminal [address] inactive" {
		t.Fatalf("last error: %+v", r.LastError)
	}
	// Older events could name the proxy's address: it is scrubbed when read.
	if _, err := s.DB().Exec(ctx, `DELETE FROM payments.gateway_events WHERE detail->>'error' LIKE '%-11%'`); err != nil {
		t.Fatal(err)
	}
	if r, err = s.GatewayReport(ctx, "zarinpal", from, to); err != nil || r.LastError.Detail != "zarinpal: request failed: proxyconnect tcp: dial tcp [address]: connect: connection refused" {
		t.Fatalf("scrubbed: %+v %v", r.LastError, err)
	}

	// Nothing for a provider without payments.
	r, err = s.GatewayReport(ctx, "manual_card", from, to)
	if err != nil || len(r.Totals) != 0 || r.OpenCount != 0 || r.OldestOpen != nil || r.LastError != nil || r.LastPaid != nil {
		t.Fatalf("empty report: %+v %v", r, err)
	}
}

// Open means "may still settle": a Stars invoice past its expiry can no
// longer be paid; a manual payment awaiting review is open.
func TestOpenIntents(t *testing.T) {
	s := cfgStore(t)
	ctx := context.Background()
	now := time.Now()
	past, future := now.Add(-time.Hour), now.Add(time.Hour)
	stars := &Money{Amount: 100, Currency: "XTR"}
	intentAt(t, s, "stars", "pending", "IRT", 1000, stars, now, "", "", &future)
	intentAt(t, s, "stars", "pending", "IRT", 1000, stars, now, "", "", &past)
	intentAt(t, s, "manual_card", "confirming", "IRT", 1000, nil, now, "", "", nil)
	intentAt(t, s, "manual_card", "pending", "IRT", 1000, nil, now, "", "", nil)
	intentAt(t, s, "manual_card", "succeeded", "IRT", 1000, nil, now, "", "", nil)
	// Zarinpal: an expired-by-time payment stays open until the reconciler has asked.
	intentAt(t, s, "zarinpal", "pending", "IRT", 1000, &Money{Amount: 10_000, Currency: "IRR"}, now, "Z1", "", &past)
	for provider, want := range map[string]int64{"stars": 1, "manual_card": 2, "zarinpal": 1, "manual_crypto": 0} {
		n, _, err := s.OpenIntents(ctx, nil, provider)
		if err != nil || n != want {
			t.Errorf("%s: %d open, want %d (%v)", provider, n, want, err)
		}
	}
}
