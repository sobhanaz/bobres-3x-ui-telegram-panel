package domain

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
)

// order.paid.v1 says which provider took the money for a payment, so the
// reports can split gateway and manual sales by provider. Wallet, trial and
// free orders have no provider.
func TestOrderPaidCarriesTheProviderThatTookThePayment(t *testing.T) {
	for _, c := range []struct{ provider, source string }{
		{Zarinpal, events.PaidByGateway},
		{Stars, events.PaidByGateway},
		{USDTAuto, events.PaidByGateway},
		{"manual_card", events.PaidManually},
		{"manual_crypto", events.PaidManually},
	} {
		t.Run(c.provider, func(t *testing.T) {
			svc, s := testService(t)
			ctx := context.Background()
			u := seedUser(t, s, 4001)
			p := seedPlan(t, s, false, 700)
			o, err := svc.CreateOrder(ctx, CreateOrderParams{UserID: u.ID, PlanID: p.ID, IdempotencyKey: "prov-" + c.provider})
			if err != nil {
				t.Fatal(err)
			}
			m := payEvent(t, "ev-"+c.provider, events.PaymentsPaymentSucceeded, events.PaymentEvent{
				IntentID: "00000000-0000-7000-8000-0000000000a1", OrderID: o.ID, UserID: u.ID,
				Provider: c.provider, Amount: 700, Currency: "IRT",
			})
			if err := svc.HandlePaymentEvent(ctx, m); err != nil {
				t.Fatal(err)
			}
			e := onlyOrderPaid(t, s)
			if e.Provider != c.provider || e.Source != c.source || e.Amount != 700 {
				t.Fatalf("event = %+v, want source %s provider %s", e, c.source, c.provider)
			}
		})
	}
}

func TestOrderPaidHasNoProviderForWalletTrialAndFreeOrders(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	u := seedUser(t, s, 4002)

	// Wallet.
	p := seedPlan(t, s, false, 500)
	fund(t, s, u.ID, 500)
	o, err := svc.CreateOrder(ctx, CreateOrderParams{UserID: u.ID, PlanID: p.ID, IdempotencyKey: "np-wallet"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PayOrderWithWallet(ctx, o.ID, u.ID); err != nil {
		t.Fatal(err)
	}
	// Trial.
	tp := seedPlan(t, s, true, 0)
	if _, err := svc.StartTrial(ctx, u.ID, tp.ID, "np-trial"); err != nil {
		t.Fatal(err)
	}
	// Free (a plan priced 0).
	fp := seedPlan(t, s, false, 0)
	if _, err := svc.CreateOrder(ctx, CreateOrderParams{UserID: u.ID, PlanID: fp.ID, IdempotencyKey: "np-free"}); err != nil {
		t.Fatal(err)
	}

	raw := outboxEvents(t, s, events.OrderPaid)
	if len(raw) != 3 {
		t.Fatalf("order.paid events = %d, want 3", len(raw))
	}
	sources := map[string]bool{}
	for _, r := range raw {
		var e events.OrderPaidEvent
		if err := json.Unmarshal(r, &e); err != nil {
			t.Fatal(err)
		}
		if e.Provider != "" {
			t.Errorf("%s order carries provider %q", e.Source, e.Provider)
		}
		// The key is omitted, not written empty.
		var m map[string]any
		_ = json.Unmarshal(r, &m)
		if _, has := m["provider"]; has {
			t.Errorf("%s event has a provider key: %s", e.Source, r)
		}
		sources[e.Source] = true
	}
	for _, want := range []string{events.PaidByWallet, events.PaidTrial, events.PaidFree} {
		if !sources[want] {
			t.Errorf("no %s event among %v", want, sources)
		}
	}
}

func onlyOrderPaid(t *testing.T, s *store.Store) events.OrderPaidEvent {
	t.Helper()
	raw := outboxEvents(t, s, events.OrderPaid)
	if len(raw) != 1 {
		t.Fatalf("order.paid events = %d, want 1", len(raw))
	}
	var e events.OrderPaidEvent
	if err := json.Unmarshal(raw[0], &e); err != nil {
		t.Fatal(err)
	}
	return e
}
