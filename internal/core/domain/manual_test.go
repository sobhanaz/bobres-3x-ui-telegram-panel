package domain

import (
	"context"
	"math"
	"slices"
	"testing"
)

func TestIsManual(t *testing.T) {
	for _, p := range []string{ManualCard, ManualCrypto, ManualZarinpal} {
		if !IsManual(p) {
			t.Errorf("%s is manual", p)
		}
	}
	for _, p := range []string{Zarinpal, IDPay, NextPay, Aqayepardakht, Stars, USDTAuto, "wallet", "", "manual_", "MANUAL_CARD"} {
		if IsManual(p) {
			t.Errorf("%q is not manual", p)
		}
	}
}

// The bot draws a manual button when its settings are set and it is not
// switched off (cardEnabled, cryptoEnabled, zarinpalLinkEnabled in the bot's
// buy.go); the Mini App offers exactly the same.
func TestManualMethodsFollowTheBotsRule(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	methods := func(currency string) []string {
		t.Helper()
		ms, err := svc.ManualMethods(ctx, currency)
		if err != nil {
			t.Fatal(err)
		}
		return ms
	}
	want := func(what, currency string, w ...string) {
		t.Helper()
		if got := methods(currency); !slices.Equal(got, w) {
			t.Errorf("%s (%s): %v, want %v", what, currency, got, w)
		}
	}

	want("nothing set", "IRT")
	want("nothing set", "USDT")

	setSetting(t, s, "payments.card_number", "6037-9911-2222-3333")
	want("a card", "IRT", ManualCard)
	want("a card does not take USDT prices", "USDT")
	setSetting(t, s, "payments.card_number", "")
	want("the card removed", "IRT")

	setSetting(t, s, "payments.zarinpal_link", "https://zarinp.al/shop")
	want("a payment link", "IRT", ManualZarinpal)
	want("a payment link does not take USDT prices", "USDT")

	// Crypto needs an address; a Toman price needs the rate as well.
	setSetting(t, s, "payments.usdt_trc20", "TXyz0000000000000000000000000000001")
	want("an address without a rate", "IRT", ManualZarinpal)
	want("an address and a USDT price", "USDT", ManualCrypto)
	setSetting(t, s, "payments.usdt_rate", "60,000")
	want("an address and a rate", "IRT", ManualCrypto, ManualZarinpal)
	setSetting(t, s, "payments.usdt_trc20", "")
	want("no address", "IRT", ManualZarinpal)
	setSetting(t, s, "payments.usdt_erc20", "0x0000000000000000000000000000000000000001")
	want("an ERC20 address alone is enough", "IRT", ManualCrypto, ManualZarinpal)

	// All three, in the bot's order.
	setSetting(t, s, "payments.card_number", "6037-9911-2222-3333")
	want("all three", "IRT", ManualCard, ManualCrypto, ManualZarinpal)

	// The owner's switches (unset counts as on).
	for _, c := range []struct{ off, left string }{
		{ManualCard, ManualCrypto + "," + ManualZarinpal},
		{ManualCrypto, ManualCard + "," + ManualZarinpal},
		{ManualZarinpal, ManualCard + "," + ManualCrypto},
	} {
		key := MethodToggle(c.off)
		setSetting(t, s, key, "false")
		got := methods("IRT")
		if len(got) != 2 || slices.Contains(got, c.off) {
			t.Errorf("%s switched off: %v", c.off, got)
		}
		setSetting(t, s, key, "true")
		if got := methods("IRT"); len(got) != 3 {
			t.Errorf("%s switched on again: %v", c.off, got)
		}
	}

	// Another currency has none.
	want("a currency the store does not sell in", "EUR")
}

// The free tier includes the manual methods, and a trial that is over
// changes nothing for them.
func TestManualMethodsStayOnTheFreeTier(t *testing.T) {
	w := newGateWorld(t)
	ctx := context.Background()
	setSetting(t, w.s, "payments.card_number", "6037-9911-2222-3333")
	setSetting(t, w.s, "payments.zarinpal_link", "https://zarinp.al/shop")
	w.endTrial()
	ms, err := w.svc.ManualMethods(ctx, "IRT")
	if err != nil || !slices.Equal(ms, []string{ManualCard, ManualZarinpal}) {
		t.Fatalf("on the free tier: %v %v", ms, err)
	}
}

// OfferedMethods is the automated methods, Stars among them, and then the
// manual ones.
func TestOfferedMethods(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	pay := &fakePayments{gateways: []string{Stars, Zarinpal}}
	setSetting(t, s, "payments.stars_rate", "1500")
	setSetting(t, s, "payments.card_number", "6037-9911-2222-3333")
	setSetting(t, s, "payments.zarinpal_link", "https://zarinp.al/shop")

	got, err := svc.OfferedMethods(ctx, pay, "IRT", 150_000)
	if err != nil || !slices.Equal(got, []string{Stars, Zarinpal, ManualCard, ManualZarinpal}) {
		t.Fatalf("offered: %v %v", got, err)
	}
	got, _ = svc.OfferedMethods(ctx, pay, "USDT", 1_000_000)
	if len(got) != 0 {
		t.Fatalf("nothing is offered for a USDT price here: %v", got)
	}
	// A price Zarinpal cannot take leaves it out; the manual ones take any.
	got, _ = svc.OfferedMethods(ctx, pay, "IRT", 500)
	if !slices.Equal(got, []string{Stars, ManualCard, ManualZarinpal}) {
		t.Fatalf("offered for 500 Toman: %v", got)
	}
}

func TestManualCryptoAmount(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	cases := []struct {
		amount   int64
		currency string
		rate     string
		want     int64
		ok       bool
	}{
		{150_000, "IRT", "60000", 2_500_000, true},                      // exactly 2.50 USDT
		{150_001, "IRT", "60000", 2_510_000, true},                      // rounded up to the next cent
		{1, "IRT", "60000", 10_000, true},                               // never less than a cent
		{150_000, "IRT", "60,000", 2_500_000, true},                     // the rate as it is typed
		{150_000, "IRT", "۶۰۰۰۰", 2_500_000, true},                      // Persian digits
		{12_345_678, "USDT", "", 12_345_678, true},                      // a USDT price as it is
		{150_000, "IRT", "", 0, false},                                  // no rate
		{150_000, "EUR", "60000", 0, false},                             // not a currency of the store
		{0, "IRT", "60000", 0, false},                                   // nothing to pay
		{-5, "USDT", "", 0, false},                                      // nonsense
		{math.MaxInt64, "IRT", "60000", 0, false},                       // too large to compute
		{math.MaxInt64/100 + 1, "IRT", "1", 0, false},                   // amount*100 would overflow
		{math.MaxInt64 / 100, "IRT", "1", 0, false},                     // cents would overflow once in micro-USDT
		{1_000_000_000_000, "IRT", "1000", 1_000_000_000_000_000, true}, // a trillion Toman at 1,000 a USDT: a billion USDT
	}
	for _, c := range cases {
		setSetting(t, s, "payments.usdt_rate", c.rate)
		got, ok := svc.ManualCryptoAmount(ctx, c.amount, c.currency)
		if got != c.want || ok != c.ok {
			t.Errorf("%d %s at %q: %d %v, want %d %v", c.amount, c.currency, c.rate, got, ok, c.want, c.ok)
		}
	}
}
