package store

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
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

// A failed check is kept at most once per intent per window, even when
// replicas and customers record at the same moment.
func TestRecordCheckErrorIsRateLimited(t *testing.T) {
	s := cfgStore(t)
	ctx := context.Background()
	rial := &Money{Amount: 100_000, Currency: "IRR"}
	a := intentAt(t, s, "zarinpal", "pending", "IRT", 10_000, rial, time.Now(), "C1", "", nil)
	b := intentAt(t, s, "zarinpal", "pending", "IRT", 10_000, rial, time.Now(), "C2", "", nil)
	c := intentAt(t, s, "zarinpal", "pending", "IRT", 10_000, rial, time.Now(), "C3", "", nil)
	count := func(id string) int {
		t.Helper()
		var n int
		if err := s.DB().QueryRow(ctx, `SELECT count(*) FROM payments.gateway_events
			WHERE intent_id = $1 AND kind = 'check' AND outcome = 'error'`, id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if wrote, err := s.RecordCheckError(ctx, a, "zarinpal", "zarinpal: request failed: timed out", time.Hour); err != nil || !wrote {
		t.Fatalf("first: %v %v", wrote, err)
	}
	if wrote, err := s.RecordCheckError(ctx, a, "zarinpal", "zarinpal: request failed: connection reset", time.Hour); err != nil || wrote {
		t.Fatalf("within the hour: %v %v", wrote, err)
	}
	// Other events of the intent do not count, and each intent has its own window.
	if err := s.RecordGatewayEvent(ctx, nil, b, "zarinpal", "create", "error", map[string]any{"error": "x"}); err != nil {
		t.Fatal(err)
	}
	if wrote, err := s.RecordCheckError(ctx, b, "zarinpal", "zarinpal: request failed: timed out", time.Hour); err != nil || !wrote {
		t.Fatalf("another intent: %v %v", wrote, err)
	}
	var (
		wg    sync.WaitGroup
		wrote atomic.Int32
	)
	gate := make(chan struct{})
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-gate
			ok, err := s.RecordCheckError(ctx, c, "zarinpal", "zarinpal: request failed: timed out", time.Hour)
			if err != nil {
				t.Errorf("concurrent: %v", err)
			}
			if ok {
				wrote.Add(1)
			}
		}()
	}
	close(gate)
	wg.Wait()
	if wrote.Load() != 1 || count(c) != 1 {
		t.Fatalf("concurrent recorders wrote %d (rows %d), want 1", wrote.Load(), count(c))
	}
	// A recorder whose row is not committed yet holds the next one back,
	// which then sees that row: without the lock it would not, and write too.
	d := intentAt(t, s, "zarinpal", "pending", "IRT", 10_000, rial, time.Now(), "C4", "", nil)
	type result struct {
		wrote bool
		err   error
	}
	done := make(chan result, 1)
	err := s.WithTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('payments:check-error:' || $1, 0))`, d); err != nil {
			return err
		}
		if err := s.RecordGatewayEvent(ctx, tx, d, "zarinpal", "check", "error", map[string]any{"error": "first"}); err != nil {
			return err
		}
		go func() {
			ok, err := s.RecordCheckError(ctx, d, "zarinpal", "second", time.Hour)
			done <- result{ok, err}
		}()
		select {
		case r := <-done:
			done <- r
			t.Error("a recorder did not wait for the one in progress")
		case <-time.After(300 * time.Millisecond):
		}
		return nil
	})
	if r := <-done; err != nil || r.err != nil || r.wrote || count(d) != 1 {
		t.Fatalf("held back: wrote %v (rows %d), %v %v", r.wrote, count(d), r.err, err)
	}
	// Once the window has passed, the next failure is kept again.
	if _, err := s.DB().Exec(ctx, `UPDATE payments.gateway_events SET created_at = now() - interval '61 minutes' WHERE intent_id = $1`, a); err != nil {
		t.Fatal(err)
	}
	if wrote, err := s.RecordCheckError(ctx, a, "zarinpal", "zarinpal: request failed: timed out", time.Hour); err != nil || !wrote || count(a) != 2 {
		t.Fatalf("after the window: %v %v rows %d", wrote, err, count(a))
	}
}

// The report lists the failed checks kept in its period, newest first, at
// most MaxCheckErrors, with how many there are, addresses scrubbed.
func TestGatewayReportCheckErrors(t *testing.T) {
	s := cfgStore(t)
	ctx := context.Background()
	now := time.Now()
	from, to := now.Add(-24*time.Hour), now.Add(time.Minute)
	rial := &Money{Amount: 100_000, Currency: "IRR"}
	in := intentAt(t, s, "zarinpal", "pending", "IRT", 10_000, rial, now, "E1", "", nil)

	r, err := s.GatewayReport(ctx, "zarinpal", from, to)
	if err != nil || r.CheckErrors != nil || r.CheckErrorCount != 0 {
		t.Fatalf("none yet: %+v %d %v", r.CheckErrors, r.CheckErrorCount, err)
	}
	for i := range MaxCheckErrors + 5 {
		if err := s.RecordGatewayEvent(ctx, nil, in, "zarinpal", "check", "error",
			map[string]any{"error": fmt.Sprintf("zarinpal: error -12 (HTTP 429): busy %d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	// Outside the period, another kind, another outcome, another provider: not listed.
	if _, err := s.DB().Exec(ctx, `INSERT INTO payments.gateway_events (intent_id, provider, kind, outcome, detail, created_at)
		VALUES ($1, 'zarinpal', 'check', 'error', '{"error":"old"}', now() - interval '2 days')`, in); err != nil {
		t.Fatal(err)
	}
	for _, e := range []struct{ provider, kind, outcome string }{
		{"zarinpal", "create", "error"}, {"zarinpal", "check", "paid"}, {"stars", "check", "error"},
	} {
		if err := s.RecordGatewayEvent(ctx, nil, in, e.provider, e.kind, e.outcome, map[string]any{"error": "other"}); err != nil {
			t.Fatal(err)
		}
	}
	// The newest names an address (older events could): scrubbed when read.
	if err := s.RecordGatewayEvent(ctx, nil, in, "zarinpal", "check", "error",
		map[string]any{"error": "zarinpal: request failed: dial tcp 203.0.113.10:3128: connect: connection refused"}); err != nil {
		t.Fatal(err)
	}

	r, err = s.GatewayReport(ctx, "zarinpal", from, to)
	if err != nil {
		t.Fatal(err)
	}
	if r.CheckErrorCount != MaxCheckErrors+6 || len(r.CheckErrors) != MaxCheckErrors {
		t.Fatalf("listed %d of %d, want %d of %d", len(r.CheckErrors), r.CheckErrorCount, MaxCheckErrors, MaxCheckErrors+6)
	}
	first := r.CheckErrors[0]
	if first.Kind != "check" || first.IntentID != in || first.Detail != "zarinpal: request failed: dial tcp [address]: connect: connection refused" {
		t.Fatalf("newest: %+v", first)
	}
	if got := r.CheckErrors[1].Detail; got != fmt.Sprintf("zarinpal: error -12 (HTTP 429): busy %d", MaxCheckErrors+4) {
		t.Fatalf("second newest: %q", got)
	}
	// A failed check is not the gateway's last error (core's health and
	// alerts read that): the older failed create is. A paid check still is
	// its last payment.
	if r.LastError == nil || r.LastError.Kind != "create" || r.LastError.Detail != "other" {
		t.Fatalf("last error: %+v", r.LastError)
	}
	if r.LastPaid == nil || r.LastPaid.Kind != "check" {
		t.Fatalf("last paid: %+v", r.LastPaid)
	}
	// The other providers' reports have none.
	if r, err := s.GatewayReport(ctx, "manual_card", from, to); err != nil || len(r.CheckErrors) != 0 || r.CheckErrorCount != 0 {
		t.Fatalf("manual_card: %+v %v", r, err)
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
