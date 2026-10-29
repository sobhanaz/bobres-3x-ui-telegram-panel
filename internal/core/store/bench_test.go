package store

import (
	"context"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/migrate"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testdb"
)

// Benchmarks of the queries every bot update makes (docs/21-benchmark.md),
// against the test Postgres: one round trip each, on a warm cache, with
// 2,000 customers and 8 plans.

func benchStore(b *testing.B) *Store {
	b.Helper()
	dsn := testdb.DSN(b)
	ctx := context.Background()
	if err := migrate.Up(ctx, dsn, "core"); err != nil {
		b.Fatalf("migrate: %v", err)
	}
	s, err := New(ctx, testdb.WithPoolCap(b, dsn))
	if err != nil {
		b.Fatalf("connect: %v", err)
	}
	b.Cleanup(s.Close)
	if err := testdb.Truncate(ctx, s.DB(), `core.ledger_entries, core.wallets, core.subscriptions, core.orders,
		core.plans, core.users`); err != nil {
		b.Fatalf("truncate: %v", err)
	}
	for i := range 2000 {
		if _, err := s.UpsertUser(ctx, s.Conn(), benchTG+int64(i), "", "fa", ""); err != nil {
			b.Fatal(err)
		}
	}
	for range 8 {
		p := &Plan{NameI18n: map[string]string{"en": "Bench", "fa": "بنچ"}, Kind: "both", Price: 150_000, Currency: "IRT", Enabled: true}
		if err := s.UpsertPlan(ctx, s.Conn(), p); err != nil {
			b.Fatal(err)
		}
	}
	return s
}

const benchTG = 5_000_000_000

// BenchmarkGetUserByTelegramID is the lookup that starts every bot update.
func BenchmarkGetUserByTelegramID(b *testing.B) {
	s := benchStore(b)
	ctx := context.Background()
	b.ReportAllocs()
	i := int64(0)
	for b.Loop() {
		if _, err := s.GetUserByTelegramID(ctx, s.Conn(), benchTG+i%2000); err != nil {
			b.Fatal(err)
		}
		i++
	}
}

// BenchmarkListPlans is the plan list of the Buy menu.
func BenchmarkListPlans(b *testing.B) {
	s := benchStore(b)
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		plans, err := s.ListPlans(ctx, s.Conn(), false)
		if err != nil || len(plans) != 8 {
			b.Fatalf("%d plans, %v", len(plans), err)
		}
	}
}
