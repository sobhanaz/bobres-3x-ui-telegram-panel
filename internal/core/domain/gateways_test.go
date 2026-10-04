package domain

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

func setSetting(t *testing.T, s *store.Store, key, value string) {
	t.Helper()
	raw, _ := json.Marshal(value)
	if err := s.SetSetting(context.Background(), s.Conn(), key, raw); err != nil {
		t.Fatal(err)
	}
}

func TestGatewayCharge(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	setSetting(t, s, "payments.stars_rate", "1,500") // Toman per Star
	cases := []struct {
		provider, currency string
		amount             int64
		want               int64
		unit               string
	}{
		{Zarinpal, "IRT", 150_000, 1_500_000, "IRR"},
		{Stars, "IRT", 150_000, 100, "XTR"},
		{Stars, "IRT", 150_001, 101, "XTR"}, // rounded up: never undercharge
	}
	for _, c := range cases {
		got, unit, err := svc.gatewayCharge(ctx, c.provider, c.amount, c.currency)
		if err != nil || got != c.want || unit != c.unit {
			t.Errorf("%s %d %s: got %d %s %v, want %d %s", c.provider, c.amount, c.currency, got, unit, err, c.want, c.unit)
		}
	}
	for _, bad := range []struct{ provider, currency string }{
		{Zarinpal, "USDT"}, {Stars, "USDT"}, {"paypal", "IRT"},
	} {
		if _, _, err := svc.gatewayCharge(ctx, bad.provider, 1000, bad.currency); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s/%s accepted: %v", bad.provider, bad.currency, err)
		}
	}
	setSetting(t, s, "payments.stars_rate", "")
	if _, _, err := svc.gatewayCharge(ctx, Stars, 1000, "IRT"); !errors.Is(err, ErrInvalid) {
		t.Errorf("Stars without a rate: %v", err)
	}
}

func TestPaymentMethodsAndGatewayIntent(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	pay := &fakePayments{gateways: []string{Stars, Zarinpal}}

	// No rates yet: only Zarinpal can price a Toman plan.
	if ms, err := svc.PaymentMethods(ctx, pay, "IRT", 0); err != nil || len(ms) != 1 || ms[0] != Zarinpal {
		t.Fatalf("methods without rates: %v %v", ms, err)
	}
	setSetting(t, s, "payments.stars_rate", "1500")
	if ms, _ := svc.PaymentMethods(ctx, pay, "IRT", 150_000); len(ms) != 2 {
		t.Fatalf("methods with a Stars rate: %v", ms)
	}
	// Zarinpal's limits hide it for prices it cannot take.
	if ms, _ := svc.PaymentMethods(ctx, pay, "IRT", 500); len(ms) != 1 || ms[0] != Stars {
		t.Fatalf("methods for 500 Toman: %v", ms)
	}
	if ms, _ := svc.PaymentMethods(ctx, pay, "USDT", 0); len(ms) != 0 {
		t.Fatalf("USDT plans have no automated method yet: %v", ms)
	}

	u := seedUser(t, s, 2101)
	pl := seedPlan(t, s, false, 150_000)
	o, err := svc.CreateOrder(ctx, CreateOrderParams{UserID: u.ID, PlanID: pl.ID, IdempotencyKey: "g-1"})
	if err != nil {
		t.Fatal(err)
	}
	res, err := svc.CreatePaymentIntent(ctx, pay, CreatePaymentIntentParams{
		UserID: u.ID, OrderID: o.ID, Provider: Stars, IdempotencyKey: "g-int-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(pay.started) != 1 || pay.started[0].GatewayAmount != 100 || pay.started[0].GatewayCurrency != "XTR" ||
		pay.started[0].Amount != 150_000 || pay.started[0].Currency != "IRT" || res.GatewayAmount != 100 || res.Description == "" {
		t.Fatalf("stars intent: %+v / %+v", pay.started, res)
	}
	// Someone else's order cannot be paid through a gateway either.
	other := seedUser(t, s, 2102)
	if _, err := svc.CreatePaymentIntent(ctx, pay, CreatePaymentIntentParams{
		UserID: other.ID, OrderID: o.ID, Provider: Zarinpal, IdempotencyKey: "g-int-2",
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("other user's order: %v", err)
	}
}
