package domain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/eventbus"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/money"
)

// HandlePaymentEvent applies one event from the payments feed. The inbox
// claim and every effect commit in one transaction, so a re-delivered event
// is a no-op. Topics core does not use are acknowledged and ignored, so a
// newer payments service can add events without stalling core.
func (s *Service) HandlePaymentEvent(ctx context.Context, m eventbus.Message) error {
	switch m.Topic {
	case events.PaymentsPaymentSucceeded, events.PaymentsPaymentRejected, events.PaymentsLegacyWalletCredited:
	default:
		return nil
	}
	var p events.PaymentEvent
	if err := json.Unmarshal(m.Payload, &p); err != nil {
		return eventbus.Permanent(fmt.Errorf("decode %s: %w", m.Topic, err))
	}
	if p.IntentID == "" || p.UserID == "" {
		return eventbus.Permanent(fmt.Errorf("%s without intent_id or user_id", m.Topic))
	}
	err := s.st.WithTx(ctx, func(tx pgx.Tx) error {
		claimed, err := s.st.ClaimInbox(ctx, tx, "payments:"+m.EventID)
		if err != nil || !claimed {
			return err
		}
		if m.Topic == events.PaymentsPaymentRejected {
			return s.paymentRejected(ctx, tx, p)
		}
		return s.paymentReceived(ctx, tx, p)
	})
	if isDataError(err) {
		return eventbus.Permanent(err)
	}
	if err == nil {
		s.kickProvisioning() // an order may have become paid
	}
	return err
}

// isDataError reports errors that retrying cannot fix.
func isDataError(err error) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" { // foreign key: unknown user
		return true
	}
	return errors.Is(err, store.ErrNotFound) || errors.Is(err, ErrInvalid) ||
		errors.Is(err, store.ErrIdempotencyConflict)
}

// paymentReceived credits the money to the user's wallet (exactly once per
// intent), then uses it to pay the intent's order when that order is still
// payable. Otherwise the money stays in the wallet: it is real money, and the
// user can spend it or ask for it back.
func (s *Service) paymentReceived(ctx context.Context, tx pgx.Tx, p events.PaymentEvent) error {
	if p.Amount <= 0 || money.Scale(p.Currency) < 0 {
		return invalid("payment of %d %q", p.Amount, p.Currency)
	}
	u, err := s.st.GetUser(ctx, tx, p.UserID)
	if err != nil {
		return err
	}
	w, err := s.st.Credit(ctx, tx, p.UserID, p.Currency, p.Amount, &store.LedgerEntry{
		Kind: "topup", RefType: strptr("intent"), RefID: strptr(p.IntentID),
		IdempotencyKey: "pay-in:" + p.IntentID,
	})
	if errors.Is(err, store.ErrDuplicateIdempotency) {
		return nil // this intent was applied already (e.g. via the legacy topic)
	}
	if err != nil {
		return err
	}
	credited := events.WalletCreditedEvent{
		UserID: p.UserID, TelegramID: u.TelegramID, IntentID: p.IntentID, OrderID: p.OrderID,
		Amount: p.Amount, Currency: p.Currency, Balance: w.Balance, Reason: events.CreditTopup,
	}
	if p.OrderID == "" {
		return s.publish(ctx, tx, events.WalletCredited, credited)
	}

	o, err := s.st.LockOrder(ctx, tx, p.OrderID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	if o == nil || o.UserID != p.UserID || o.Currency != p.Currency || !isPayable(o.Status) || p.Amount < o.Amount {
		credited.Reason = events.CreditOrderNotPayable
		return s.publish(ctx, tx, events.WalletCredited, credited)
	}
	if _, err := s.st.Debit(ctx, tx, o.UserID, o.Currency, o.Amount, &store.LedgerEntry{
		Kind: "purchase", RefType: strptr("order"), RefID: strptr(o.ID),
		IdempotencyKey: "purchase:" + o.ID,
	}); err != nil {
		if errors.Is(err, store.ErrDuplicateIdempotency) {
			return fmt.Errorf("order %s has a recorded purchase but is %s: %w", o.ID, o.Status, store.ErrIdempotencyConflict)
		}
		return err
	}
	if ok, err := s.st.TransitionOrder(ctx, tx, o.ID, payableStatuses, "paid"); err != nil || !ok {
		return errors.Join(err, ErrOrderNotPayable)
	}
	o.Status = "paid"
	return s.publishOrderPaid(ctx, tx, o, u.TelegramID, events.PaidManually)
}

// paymentRejected tells the user (through the bot) that a reviewer rejected
// their payment. No money moved; the order stays payable.
func (s *Service) paymentRejected(ctx context.Context, tx pgx.Tx, p events.PaymentEvent) error {
	u, err := s.st.GetUser(ctx, tx, p.UserID)
	if err != nil {
		return err
	}
	return s.publish(ctx, tx, events.PaymentRejected, events.PaymentRejectedEvent{
		UserID: p.UserID, TelegramID: u.TelegramID, IntentID: p.IntentID, OrderID: p.OrderID,
		Amount: p.Amount, Currency: p.Currency, Reason: p.Reason,
	})
}

// DeadLetter records events from source that could not be applied.
func (s *Service) DeadLetter(source string) eventbus.DeadLetterFunc {
	return func(ctx context.Context, m eventbus.Message, cause error) error {
		return s.st.InsertDeadLetter(ctx, s.st.Conn(), store.DeadLetter{
			Source: source, EventID: m.EventID, Topic: m.Topic, Payload: m.Payload, Error: cause.Error(),
		})
	}
}
