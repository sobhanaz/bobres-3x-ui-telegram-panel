package domain

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/store"
)

func starsIntent(key string, stars int64) *store.Intent {
	ga, gc := stars, "XTR"
	o := orderUUID
	return &store.Intent{UserID: uid, OrderID: &o, Amount: 150_000, Currency: "IRT",
		IdempotencyKey: key, GatewayAmount: &ga, GatewayCurrency: &gc}
}

func TestStarsPrecheckAndConfirm(t *testing.T) {
	svc, st := testService(t)
	ctx := context.Background()
	in, err := svc.StartStars(ctx, starsIntent("s1", 250))
	if err != nil || in.Provider != Stars || in.ExpiresAt == nil {
		t.Fatalf("start: %+v %v", in, err)
	}
	if _, err := svc.StartStars(ctx, &store.Intent{UserID: uid, Amount: 1, Currency: "IRT", IdempotencyKey: "s0"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("no Stars amount: %v", err)
	}

	// Pre-checkout: only the owner, only the exact amount, only XTR.
	if _, err := svc.PrecheckStars(ctx, uid, in.ID, 250, "XTR"); err != nil {
		t.Fatalf("precheck: %v", err)
	}
	for name, c := range map[string]struct {
		user  string
		total int64
		cur   string
		want  error
	}{
		"other user": {"00000000-0000-7000-8000-00000000dfff", 250, "XTR", ErrForbidden},
		"less Stars": {uid, 249, "XTR", ErrInvalid},
		"wrong unit": {uid, 250, "USD", ErrInvalid},
	} {
		if _, err := svc.PrecheckStars(ctx, c.user, in.ID, c.total, c.cur); !errors.Is(err, c.want) {
			t.Errorf("%s: %v", name, err)
		}
	}

	// Payment: settles once; the same charge reported again is fine; a second
	// charge is refused for the admin to refund.
	got, err := svc.ConfirmStars(ctx, uid, in.ID, 250, "XTR", "chg-1")
	if err != nil || got.Status != "succeeded" || *got.ExternalID != "chg-1" {
		t.Fatalf("confirm: %+v %v", got, err)
	}
	if again, err := svc.ConfirmStars(ctx, uid, in.ID, 250, "XTR", "chg-1"); err != nil || again.Status != "succeeded" {
		t.Fatalf("same charge again: %+v %v", again, err)
	}
	if _, err := svc.ConfirmStars(ctx, uid, in.ID, 250, "XTR", "chg-2"); !errors.Is(err, ErrAlreadyPaid) {
		t.Fatalf("second charge: %v", err)
	}
	if _, err := svc.PrecheckStars(ctx, uid, in.ID, 250, "XTR"); !errors.Is(err, store.ErrInvalidTransition) {
		t.Fatalf("precheck after payment: %v", err)
	}
	if ev := outbox(t, st); len(ev[events.PaymentsPaymentSucceeded]) != 1 || ledgerCount(t, st) != 1 {
		t.Fatalf("settled more than once: %+v", ev)
	}
}

func TestStarsPaidAfterExpiryOrWrongAmount(t *testing.T) {
	svc, st := testService(t)
	ctx := context.Background()

	// Telegram took the Stars a moment after the intent expired: still settles.
	late := starsIntent("late", 100)
	past := time.Now().Add(-time.Minute)
	late.ExpiresAt = &past
	late, _ = svc.StartStars(ctx, late)
	if _, err := svc.PrecheckStars(ctx, uid, late.ID, 100, "XTR"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expired precheck accepted: %v", err)
	}
	if _, err := st.DB().Exec(ctx, `UPDATE payments.payment_intents SET status='expired' WHERE id=$1`, late.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := svc.ConfirmStars(ctx, uid, late.ID, 100, "XTR", "chg-late"); err != nil || got.Status != "succeeded" {
		t.Fatalf("paid after expiry: %+v %v", got, err)
	}

	// A wrong amount fails the intent (admin refunds), never settles it.
	wrong, _ := svc.StartStars(ctx, starsIntent("wrong", 100))
	got, err := svc.ConfirmStars(ctx, uid, wrong.ID, 99, "XTR", "chg-wrong")
	if err != nil || got.Status != "failed" || *got.FailureReason != ReasonAmountMismatch {
		t.Fatalf("wrong amount: %+v %v", got, err)
	}
	// One charge cannot settle two intents.
	other, _ := svc.StartStars(ctx, starsIntent("other", 100))
	if _, err := svc.ConfirmStars(ctx, uid, other.ID, 100, "XTR", "chg-late"); err == nil {
		t.Fatal("a charge id settled a second intent")
	}
}
