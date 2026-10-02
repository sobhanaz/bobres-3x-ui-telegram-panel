// Package domain holds payments business rules: intent creation, receipt
// submission, admin review with ledger credit + outbox events.
package domain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/eventbus"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/provider"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/store"
)

// Service composes the payments store with business rules.
type Service struct {
	st *store.Store
	ob *eventbus.Outbox
}

// New builds a Service. ob may be nil in tests (events skipped).
func New(st *store.Store, ob *eventbus.Outbox) *Service { return &Service{st: st, ob: ob} }

// CreateIntent inserts a pending intent (idempotent).
func (s *Service) CreateIntent(ctx context.Context, in *store.Intent) (*store.Intent, error) {
	if in.IdempotencyKey == "" {
		return nil, errors.New("domain: idempotency key required")
	}
	switch in.Provider {
	case provider.Wallet, provider.ManualCard, provider.ManualCrypto:
	default:
		return nil, fmt.Errorf("domain: unsupported provider %q", in.Provider)
	}
	if in.Amount <= 0 {
		return nil, errors.New("domain: amount must be positive")
	}
	in.Status = "pending"
	return s.st.CreateIntent(ctx, nil, in)
}

// SubmitReceipt stores a card receipt and moves to confirming.
func (s *Service) SubmitReceipt(ctx context.Context, userID, intentID, fileID, reference string) (*store.Intent, error) {
	if fileID == "" || reference == "" {
		return nil, errors.New("domain: receipt file and reference required")
	}
	return s.submit(ctx, userID, intentID, provider.ManualCard, func(r *store.Receipt) {
		r.ReceiptFile = &fileID
		r.ReferenceNumber = &reference
	})
}

// SubmitTXID stores a crypto TXID and moves to confirming.
func (s *Service) SubmitTXID(ctx context.Context, userID, intentID, network, txid string) (*store.Intent, error) {
	if txid == "" || network == "" {
		return nil, errors.New("domain: network and txid required")
	}
	return s.submit(ctx, userID, intentID, provider.ManualCrypto, func(r *store.Receipt) {
		r.Network = &network
		r.TXID = &txid
	})
}

// submit is the shared proof-submission flow.
func (s *Service) submit(ctx context.Context, userID, intentID, wantProvider string, fill func(*store.Receipt)) (*store.Intent, error) {
	var out *store.Intent
	err := s.st.WithTx(ctx, func(tx pgx.Tx) error {
		in, err := s.st.GetIntent(ctx, tx, intentID)
		if err != nil {
			return err
		}
		if in.UserID != userID {
			return errors.New("domain: intent belongs to another user")
		}
		if in.Provider != wantProvider {
			return fmt.Errorf("domain: wrong proof type for provider %s", in.Provider)
		}
		r := &store.Receipt{IntentID: intentID}
		fill(r)
		if err := s.st.SaveReceipt(ctx, tx, r); err != nil {
			return err
		}
		out, err = s.st.TransitionConfirming(ctx, tx, intentID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Review applies an admin decision. Approve credits the ledger mirror and
// publishes payment.succeeded.v1 (+ wallet.credited.v1 for top-ups); reject
// publishes payment.rejected.v1. Status guards allow exactly one committed
// review per intent, so concurrent approvals can never double-credit.
func (s *Service) Review(ctx context.Context, reviewerID, intentID, decision, reason string) (*store.Intent, error) {
	if decision != "approved" && decision != "rejected" {
		return nil, errors.New("domain: decision must be approved|rejected")
	}
	var out *store.Intent
	err := s.st.WithTx(ctx, func(tx pgx.Tx) error {
		var (
			in  *store.Intent
			err error
		)
		if decision == "approved" {
			in, err = s.st.TransitionSucceeded(ctx, tx, intentID)
			if err != nil {
				return err
			}
			if err := s.st.AppendLedgerCredit(ctx, tx, in.UserID, in.Currency, in.Amount,
				"intent", in.ID, "approve:"+in.ID); err != nil {
				return err
			}
		} else {
			in, err = s.st.TransitionFailed(ctx, tx, intentID)
			if err != nil {
				return err
			}
		}
		if err := s.st.ReviewReceipt(ctx, tx, intentID, reviewerID, decision, reason); err != nil {
			return err
		}
		out = in
		return s.publishReviewEvents(ctx, tx, in, decision)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// publishReviewEvents writes the post-review events inside the review tx.
func (s *Service) publishReviewEvents(ctx context.Context, tx pgx.Tx, in *store.Intent, decision string) error {
	if s.ob == nil {
		return nil
	}
	body := map[string]any{
		"intent_id": in.ID,
		"user_id":   in.UserID,
		"provider":  in.Provider,
		"amount":    in.Amount,
		"currency":  in.Currency,
	}
	if in.OrderID != nil {
		body["order_id"] = *in.OrderID
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	topic := "payment.rejected.v1"
	if decision == "approved" {
		topic = "payment.succeeded.v1"
	}
	ptx := eventbus.PgxTx{Tx: tx}
	if err := s.ob.Publish(ctx, ptx, topic, payload); err != nil {
		return err
	}
	if decision == "approved" && in.OrderID == nil {
		cred, _ := json.Marshal(map[string]any{
			"user_id": in.UserID, "amount": in.Amount,
			"currency": in.Currency, "intent_id": in.ID,
		})
		if err := s.ob.Publish(ctx, ptx, "wallet.credited.v1", cred); err != nil {
			return fmt.Errorf("wallet.credited publish: %w", err)
		}
	}
	return nil
}
