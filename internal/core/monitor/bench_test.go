package monitor

import (
	"fmt"
	"testing"
)

// BenchmarkFoldRepeats is the Monitoring page's event list folding repeated
// log lines: 1,000 lines, newest first, where a few warnings come back again
// and again with different ids, as a busy store's logs do
// (docs/21-benchmark.md).
func BenchmarkFoldRepeats(b *testing.B) {
	events := make([]Event, 0, 1000)
	for i := range 1000 {
		e := Event{At: int64(2_000_000 - i), Level: "warn", Source: "core", Kind: KindLog}
		switch i % 4 {
		case 0:
			e.Title, e.Detail = "delivery retried", fmt.Sprintf("order 0198f3c2-%04x-7b2e-9c1d-5a6b7c8d9e0f for user %d", i, 7_000_000_000+i)
		case 1:
			e.Title, e.Detail = "panel slow", fmt.Sprintf("server 1 answered in %d ms", 1000+i%7)
		case 2:
			e.Level, e.Title, e.Detail = "error", "payment check failed", fmt.Sprintf("authority A%011d: timeout", i)
		default:
			e.Kind, e.Source, e.Title = KindAlert, "alert", "disk almost full"
		}
		events = append(events, e)
	}
	b.ReportAllocs()
	for b.Loop() {
		if out := foldRepeats(events); len(out) == 0 {
			b.Fatal("nothing left")
		}
	}
}
