package i18n

import "testing"

// BenchmarkMoney is the bot writing a price, as on every plan list, plan page
// and receipt: Persian digits and separators for Toman, the decimals of USDT
// (docs/21-benchmark.md).
func BenchmarkMoney(b *testing.B) {
	for _, c := range []struct {
		name, lang, currency string
		minor                int64
	}{
		{"fa-IRT", "fa", "IRT", 1_250_000},
		{"en-IRT", "en", "IRT", 1_250_000},
		{"fa-USDT", "fa", "USDT", 25_500_000},
	} {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if Money(c.lang, c.minor, c.currency) == "" {
					b.Fatal("empty")
				}
			}
		})
	}
}
