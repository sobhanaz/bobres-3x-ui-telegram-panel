package domain

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/store"
)

// Telegram Stars: Telegram charges the customer and tells the bot, so there is
// no gateway call to make here. The intent fixes the number of Stars; the
// pre-checkout must match it, and the payment settles it once.
const (
	Stars        = "stars"
	starsInvoice = time.Hour // how long a Stars invoice can be paid
	// ReasonPayerMismatch: someone other than the intent's owner paid.
	ReasonPayerMismatch = "payer_mismatch"
)

// ErrAlreadyPaid: a second, different payment arrived for a settled intent
// (the admin should refund it).
var ErrAlreadyPaid = errors.New("payments: this payment was already settled")

// StartStars creates a Stars intent (idempotent per key).
func (s *Service) StartStars(ctx context.Context, in *store.Intent) (*store.Intent, error) {
	in.Provider = Stars
	if in.GatewayAmount == nil || *in.GatewayAmount <= 0 || in.GatewayCurrency == nil || *in.GatewayCurrency != "XTR" {
		return nil, invalid("a Stars payment needs a positive number of Stars (XTR)")
	}
	if in.ExpiresAt == nil {
		exp := time.Now().Add(starsInvoice)
		in.ExpiresAt = &exp
	}
	return s.createIntent(ctx, in)
}

// PrecheckStars answers Telegram's pre-checkout: the intent must be an open
// Stars intent of this user, not expired, for exactly this many Stars.
func (s *Service) PrecheckStars(ctx context.Context, userID, intentID string, total int64, currency string) (*store.Intent, error) {
	in, err := s.st.GetIntent(ctx, nil, intentID)
	if err != nil {
		return nil, err
	}
	switch {
	case in.Provider != Stars:
		return nil, invalid("not a Stars payment")
	case in.UserID != userID:
		return nil, ErrForbidden
	case currency != "XTR" || in.GatewayAmount == nil || total != *in.GatewayAmount:
		return nil, invalid("the amount does not match the invoice")
	case in.Status != "pending":
		return nil, store.ErrInvalidTransition
	case in.ExpiresAt != nil && time.Now().After(*in.ExpiresAt):
		return nil, invalid("this invoice has expired")
	}
	return in, nil
}

// ConfirmStars settles a Stars intent from Telegram's successful_payment. The
// money is already taken, so an intent that expired a moment ago still settles;
// a mismatch is recorded as a failed payment for the admin to refund.
func (s *Service) ConfirmStars(ctx context.Context, userID, intentID string, total int64, currency, chargeID string) (*store.Intent, error) {
	if chargeID == "" {
		return nil, invalid("telegram payment charge id required")
	}
	in, err := s.st.GetIntent(ctx, nil, intentID)
	if err != nil {
		return nil, err
	}
	if in.Provider != Stars {
		return nil, invalid("not a Stars payment")
	}
	if in.Status == "succeeded" {
		if in.ExternalID != nil && *in.ExternalID == chargeID {
			return in, nil // the same payment reported again
		}
		s.log.Warn("second Stars payment for a settled intent", "intent", in.ID)
		_ = s.st.RecordGatewayEvent(ctx, nil, in.ID, Stars, "confirm", "rejected",
			map[string]any{"charge_id": chargeID, "stars": total, "reason": "already_paid"})
		return nil, ErrAlreadyPaid
	}
	detail := map[string]any{"charge_id": chargeID, "stars": total}
	switch {
	case in.UserID != userID:
		return s.failStars(ctx, in, ReasonPayerMismatch, detail)
	case currency != "XTR" || in.GatewayAmount == nil || total != *in.GatewayAmount:
		return s.failStars(ctx, in, ReasonAmountMismatch, detail)
	}
	var out *store.Intent
	err = s.st.WithTx(ctx, func(tx pgx.Tx) error {
		got, err := s.st.SettleStarsPaid(ctx, tx, in.ID, chargeID)
		if errors.Is(err, store.ErrInvalidTransition) {
			out, err = s.st.GetIntent(ctx, tx, in.ID)
			return err
		}
		if err != nil {
			return err
		}
		if err := s.st.AppendLedgerCredit(ctx, tx, got.UserID, got.Currency, got.Amount,
			"intent", got.ID, "gateway:"+got.ID); err != nil {
			return err
		}
		if err := s.st.RecordGatewayEvent(ctx, tx, got.ID, Stars, "confirm", "paid", detail); err != nil {
			return err
		}
		out = got
		return s.publishOutcome(ctx, tx, got, events.PaymentsPaymentSucceeded, "")
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// failStars records a Stars payment that cannot settle its intent. The intent
// fails (also from expired) so the customer and the admin are told.
func (s *Service) failStars(ctx context.Context, in *store.Intent, reason string, detail map[string]any) (*store.Intent, error) {
	s.log.Warn("Stars payment rejected", "intent", in.ID, "reason", reason)
	var out *store.Intent
	err := s.st.WithTx(ctx, func(tx pgx.Tx) error {
		got, err := s.st.FailStars(ctx, tx, in.ID, reason)
		if errors.Is(err, store.ErrInvalidTransition) {
			out, err = s.st.GetIntent(ctx, tx, in.ID)
			return err
		}
		if err != nil {
			return err
		}
		if err := s.st.RecordGatewayEvent(ctx, tx, got.ID, Stars, "confirm", "rejected", detail); err != nil {
			return err
		}
		out = got
		return s.publishOutcome(ctx, tx, got, events.PaymentsPaymentRejected, reason)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
