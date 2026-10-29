package store

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

// Purchases are counted by when they were made, in three windows that end now; a refund, a top-up and a purchase in the
// future are not purchases.
func TestPaidOrderCounts(t *testing.T) {
	x := newSeed(t)
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	u := x.user(1)
	n := 0
	buy := func(kind string, ago time.Duration) {
		n++
		x.ledger(u, "IRT", -1000, kind, "", "", fmt.Sprintf("paid-orders-%s-%d", kind, n), now.Add(-ago))
	}
	buy("purchase", 10*time.Minute)         // all three windows
	buy("license_purchase", 50*time.Minute) // all three
	buy("purchase", 3*time.Hour)            // the day and the week
	buy("supplier_purchase", 23*time.Hour)  // the day and the week
	buy("purchase", 25*time.Hour)           // the week only
	buy("purchase", 6*24*time.Hour)         // the week only
	buy("purchase", 8*24*time.Hour)         // none
	buy("refund", time.Minute)              // not a purchase
	buy("topup", time.Minute)               // not a purchase
	buy("purchase", -time.Hour)             // after now: not yet
	got, err := x.s.PaidOrderCounts(x.ctx, x.s.Conn(), now)
	if err != nil {
		t.Fatal(err)
	}
	if got.Hour != 2 || got.Day != 4 || got.Week != 6 {
		t.Fatalf("counts: %+v, want 2 4 6", got)
	}
	// An empty ledger counts zero, not nothing.
	empty := newSeed(t)
	if got, err := empty.s.PaidOrderCounts(empty.ctx, empty.s.Conn(), now); err != nil || got.Hour != 0 || got.Day != 0 || got.Week != 0 {
		t.Fatalf("empty: %v %+v", err, got)
	}
}

// One setting is read by its key, and a missing one is ErrNotFound (not a value that is empty).
func TestGetSetting(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	if _, err := s.GetSetting(ctx, s.Conn(), "monitor.test_key"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unset: %v", err)
	}
	if err := s.SetSetting(ctx, s.Conn(), "monitor.test_key", []byte(`"abc"`)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.DeleteSetting(ctx, s.Conn(), "monitor.test_key") })
	got, err := s.GetSetting(ctx, s.Conn(), "monitor.test_key")
	if err != nil || string(got) != `"abc"` {
		t.Fatalf("set: %q %v", got, err)
	}
}
