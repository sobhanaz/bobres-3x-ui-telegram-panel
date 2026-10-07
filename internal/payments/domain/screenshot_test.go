package domain

import (
	"context"
	"errors"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/provider"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/store"
)

// Every manual method takes a receipt screenshot for an admin to approve.
func TestScreenshotReceiptsForEveryManualMethod(t *testing.T) {
	svc, st := testService(t)
	ctx := context.Background()

	// Crypto: a screenshot of the transfer is enough, no TXID or reference.
	cr, err := svc.CreateIntent(ctx, &store.Intent{UserID: uid, Provider: provider.ManualCrypto, Amount: 2_500_000, Currency: "USDT", IdempotencyKey: "sc-crypto"})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := svc.SubmitReceipt(ctx, uid, cr.ID, "photo-crypto", ""); err != nil || got.Status != "confirming" {
		t.Fatalf("crypto screenshot: %+v %v", got, err)
	}

	// Zarinpal payment link: screenshot plus the tracking code.
	zl, err := svc.CreateIntent(ctx, &store.Intent{UserID: uid, Provider: provider.ManualZarinpal, Amount: 150_000, Currency: "IRT", IdempotencyKey: "sc-zl"})
	if err != nil {
		t.Fatalf("manual Zarinpal intent: %v", err)
	}
	if got, err := svc.SubmitReceipt(ctx, uid, zl.ID, "photo-zl", "TRK123"); err != nil || got.Status != "confirming" {
		t.Fatalf("Zarinpal link screenshot: %+v %v", got, err)
	}

	// A card transfer still needs the bank reference number.
	cd, _ := svc.CreateIntent(ctx, &store.Intent{UserID: uid, Provider: provider.ManualCard, Amount: 150_000, Currency: "IRT", IdempotencyKey: "sc-card"})
	if _, err := svc.SubmitReceipt(ctx, uid, cd.ID, "photo-card", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("card receipt without a reference: %v", err)
	}
	if _, err := svc.SubmitReceipt(ctx, uid, cd.ID, "", "REF1"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("receipt without a screenshot: %v", err)
	}

	// Approving a Zarinpal-link receipt pays like a card transfer.
	if got, err := svc.Review(ctx, admin, zl.ID, "approved", ""); err != nil || got.Status != "succeeded" {
		t.Fatalf("approve: %+v %v", got, err)
	}
	ev := outbox(t, st)[events.PaymentsPaymentSucceeded]
	if len(ev) != 1 || ev[0].Provider != provider.ManualZarinpal || ev[0].Amount != 150_000 {
		t.Fatalf("succeeded events: %+v", ev)
	}
}
