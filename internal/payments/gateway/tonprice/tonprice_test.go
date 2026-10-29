package tonprice_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/gateway/tonprice"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tonfake"
)

func feed(t *testing.T) (*tonprice.Feed, *tonfake.Server) {
	t.Helper()
	fake := tonfake.New()
	t.Cleanup(fake.Close)
	f, err := tonprice.Default(tonprice.Config{BinanceURL: fake.BinanceURL(), OKXURL: fake.OKXURL()})
	if err != nil {
		t.Fatal(err)
	}
	return f, fake
}

func TestParseMicro(t *testing.T) {
	for in, want := range map[string]int64{"3": 3_000_000, "3.2145": 3_214_500, "3.21450000": 3_214_500, "0.5": 500_000,
		"3.1234569": 3_123_456, " 12.000001 ": 12_000_001} {
		if got, err := tonprice.ParseMicro(in); err != nil || got != want {
			t.Errorf("%q: %d %v, want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"", ".", "-3", "+3", "1e3", "abc", "3.2.1", "99999999999", "3,5", "3 5"} {
		if got, err := tonprice.ParseMicro(in); err == nil {
			t.Errorf("%q accepted as %d", in, got)
		}
	}
}

func TestQuoteUsesThePrimaryAndChecksTheSecond(t *testing.T) {
	f, fake := feed(t)
	q, err := f.Quote(context.Background())
	if err != nil || q.PriceMicro != 3_214_500 || q.Source != "binance" || time.Since(q.At) > time.Minute {
		t.Fatalf("%+v %v", q, err)
	}
	if fake.Calls("binance") != 1 || fake.Calls("okx") != 1 {
		t.Fatal("both sources are read")
	}
}

func TestOneSourceIsEnoughAndNoneIsNoPrice(t *testing.T) {
	f, fake := feed(t)
	fake.FailPrice("binance", true)
	if q, err := f.Quote(context.Background()); err != nil || q.Source != "okx" || q.PriceMicro != 3_215_000 {
		t.Fatalf("the second source: %+v %v", q, err)
	}
	fake.FailPrice("binance", false)
	fake.FailPrice("okx", true)
	if q, err := f.Quote(context.Background()); err != nil || q.Source != "binance" {
		t.Fatalf("the primary alone: %+v %v", q, err)
	}
	fake.FailPrice("binance", true)
	if q, err := f.Quote(context.Background()); err == nil {
		t.Fatalf("no source, a price: %+v", q)
	}
}

func TestSourcesThatDisagreeGiveNoPrice(t *testing.T) {
	f, fake := feed(t)
	fake.SetPrice("okx", "3.55") // 10% above
	if q, err := f.Quote(context.Background()); !errors.Is(err, tonprice.ErrDisagree) {
		t.Fatalf("%+v %v", q, err)
	}
	fake.SetPrice("okx", "3.30") // within 5%
	if q, err := f.Quote(context.Background()); err != nil || q.PriceMicro != 3_214_500 {
		t.Fatalf("%+v %v", q, err)
	}
}

func TestOddPricesAreNoSource(t *testing.T) {
	f, fake := feed(t)
	for _, bad := range []string{"0", "0.01", "5000", "abc", ""} {
		fake.SetPrice("binance", bad)
		fake.SetPrice("okx", bad)
		if q, err := f.Quote(context.Background()); err == nil {
			t.Errorf("%q: a price %+v", bad, q)
		}
	}
	// A stale OKX ticker is no source; Binance alone answers.
	fake.SetPrice("binance", "3.2")
	fake.SetPrice("okx", "9.9")
	fake.StaleOKX(true)
	if q, err := f.Quote(context.Background()); err != nil || q.Source != "binance" || q.PriceMicro != 3_200_000 {
		t.Fatalf("%+v %v", q, err)
	}
}
