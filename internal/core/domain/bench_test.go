package domain

import (
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// Benchmarks of the price arithmetic (docs/21-benchmark.md): pure functions,
// no database.

// BenchmarkTierPrice is a reseller's price for one plan: a fixed tier price,
// or the store price less the tier's discount.
func BenchmarkTierPrice(b *testing.B) {
	plans := []store.Plan{{ID: "a", Price: 150_000}, {ID: "b", Price: 249_000}, {ID: "c", Price: 1_000_000}}
	fixed := map[string]int64{"b": 199_000}
	b.ReportAllocs()
	var sum int64
	for b.Loop() {
		for i := range plans {
			sum += tierPrice(&plans[i], 15, fixed)
		}
	}
	if sum == 0 {
		b.Fatal("no prices")
	}
}

// BenchmarkCashbackAmount is the cashback a delivered order earns: basis
// points in 128-bit arithmetic, with the cap.
func BenchmarkCashbackAmount(b *testing.B) {
	b.ReportAllocs()
	var sum int64
	for b.Loop() {
		a, _ := cashbackAmount(1_250_000, 500, 50_000)
		sum += a
	}
	if sum == 0 {
		b.Fatal("no cashback")
	}
}
