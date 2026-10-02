package domain

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// HandlePaymentEvent applies a payments-emitted event inside one tx with
// inbox dedup. Returns handled=false when the event was a duplicate.
func (s *Service) HandlePaymentEvent(ctx context.Context, messageID, topic string, payload []byte) (bool, error) {
	var dup bool
	err := s.st.WithTx(ctx, func(tx pgx.Tx) error {
		// Inbox claim: first writer wins; concurrent duplicates see ErrNoRows.
		var claimed bool
		scanErr := tx.QueryRow(ctx, `
			INSERT INTO core.inbox_core (message_id) VALUES ($1)
			ON CONFLICT (message_id) DO NOTHING RETURNING true`, messageID).Scan(&claimed)
		if scanErr != nil {
			if scanErr.Error() == "no rows in result set" {
				dup = true
				return nil
			}
			return scanErr
		}
		var body struct {
			OrderID  string `json:"order_id"`
			IntentID string `json:"intent_id"`
			UserID   string `json:"user_id"`
			Amount   int64  `json:"amount"`
			Currency string `json:"currency"`
		}
		if err := json.Unmarshal(payload, &body); err != nil {
			return fmt.Errorf("consumer: bad payload: %w", err)
		}
		switch topic {
		case "payment.succeeded.v1":
			if body.OrderID == "" {
				return fmt.Errorf("consumer: payment.succeeded without order_id")
			}
			return s.MarkOrderPaidCtx(ctx, tx, body.OrderID, "pay:"+body.IntentID)
		case "wallet.credited.v1":
			_, err := s.st.Credit(ctx, tx, body.UserID, body.Currency, body.Amount, &store.LedgerEntry{
				UserID:         body.UserID,
				Currency:       body.Currency,
				Amount:         body.Amount,
				Kind:           "topup",
				RefType:        strptr("intent"),
				RefID:          &body.IntentID,
				IdempotencyKey: "wc:" + body.IntentID,
			})
			if err != nil {
				if err == store.ErrDuplicateIdempotency {
					return nil
				}
				return err
			}
			return nil
		default:
			return fmt.Errorf("consumer: unknown topic %q", topic)
		}
	})
	if err != nil {
		return false, err
	}
	return !dup, nil
}

// MarkOrderPaidCtx is the tx-scoped variant of MarkOrderPaid.
func (s *Service) MarkOrderPaidCtx(ctx context.Context, tx pgx.Tx, orderID, idemKey string) error {
	o, err := s.st.GetOrder(ctx, tx, orderID)
	if err != nil {
		return err
	}
	if o.Status == "paid" || o.Status == "active" {
		return nil
	}
	if err := s.st.SetOrderStatus(ctx, tx, o.ID, "paid"); err != nil {
		return err
	}
	err = s.st.AppendLedger(ctx, tx, &store.LedgerEntry{
		UserID:         o.UserID,
		Currency:       o.Currency,
		Amount:         0,
		Kind:           "purchase",
		RefType:        strptr("order"),
		RefID:          &o.ID,
		IdempotencyKey: idemKey,
	})
	if err == store.ErrDuplicateIdempotency {
		return nil
	}
	return err
}
