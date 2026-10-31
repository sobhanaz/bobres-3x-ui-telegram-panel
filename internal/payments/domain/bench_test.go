package domain

import "testing"

// BenchmarkTONNano is an automatic TON payment's amount: USDT to nanoton at
// the live price, rounded up, in big integers (docs/21-benchmark.md).
func BenchmarkTONNano(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, ok := tonNano(25_500_000, 3_412_345); !ok {
			b.Fatal("no amount")
		}
	}
}
