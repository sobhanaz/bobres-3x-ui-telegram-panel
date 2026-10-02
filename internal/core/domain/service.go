// Package domain holds the core business rules: order creation with
// idempotency, wallet payment in one transaction, and one-trial-per-user.
package domain

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// ErrTrialAlreadyUsed blocks a second free trial for the same user.
var ErrTrialAlreadyUsed = errors.New("domain: trial already used")

// ErrOrderNotPayable is returned when an order is not in a payable state.
var ErrOrderNotPayable = errors.New("domain: order not payable")

// trialOnceConstraint is the partial unique index enforcing one trial grant per user.
const trialOnceConstraint = "ledger_entries_trial_once_idx"

// Service composes the store with business rules.
type Service struct {
	st *store.Store
}

// New builds a Service.
func New(st *store.Store) *Service { return &Service{st: st} }

// CreateOrderParams carries the inputs of CreateOrder.
type CreateOrderParams struct {
	UserID         string
	PlanID         string
	Type           string // new | renew | traffic_topup
	IdempotencyKey string
}

// CreateOrder validates the plan and inserts an order. Retries with the same
// IdempotencyKey return the existing order unchanged.
func (s *Service) CreateOrder(ctx context.Context, p CreateOrderParams) (*store.Order, error) {
	if p.IdempotencyKey == "" {
		return nil, errors.New("domain: idempotency key required")
	}
	if p.Type == "" {
		p.Type = "new"
	}
	plan, err := s.st.GetPlan(ctx, s.st.Conn(), p.PlanID)
	if err != nil {
		return nil, err
	}
	if !plan.Enabled && !plan.IsTrial {
		return nil, fmt.Errorf("domain: plan disabled")
	}
	status := "awaiting_payment"
	if plan.Price == 0 {
		status = "paid"
	}
	return s.st.CreateOrder(ctx, s.st.Conn(), &store.Order{
		UserID:         p.UserID,
		PlanID:         p.PlanID,
		Type:           p.Type,
		Status:         status,
		Amount:         plan.Price,
		Currency:       plan.Currency,
		IdempotencyKey: p.IdempotencyKey,
	})
}

// PayOrderWithWallet debits the buyer's wallet and marks the order paid, all
// in one transaction. Safe to retry with the same IdempotencyKey.
func (s *Service) PayOrderWithWallet(ctx context.Context, orderID, idempotencyKey string) (*store.Order, error) {
	if idempotencyKey == "" {
		return nil, errors.New("domain: idempotency key required")
	}
	var out *store.Order
	err := s.st.WithTx(ctx, func(tx pgx.Tx) error {
		o, err := s.st.GetOrder(ctx, tx, orderID)
		if err != nil {
			return err
		}
		if o.Status == "paid" || o.Status == "active" {
			out = o
			return nil
		}
		if o.Status != "awaiting_payment" && o.Status != "created" {
			return ErrOrderNotPayable
		}
		_, err = s.st.Debit(ctx, tx, o.UserID, o.Currency, o.Amount, &store.LedgerEntry{
			UserID:         o.UserID,
			Currency:       o.Currency,
			Amount:         o.Amount,
			Kind:           "purchase",
			RefType:        strptr("order"),
			RefID:          &o.ID,
			IdempotencyKey: idempotencyKey,
		})
		if errors.Is(err, store.ErrDuplicateIdempotency) {
			err = nil // replay: ledger exists; status update below is idempotent
		}
		if err != nil {
			return err
		}
		if err := s.st.SetOrderStatus(ctx, tx, o.ID, "paid"); err != nil {
			return err
		}
		o.Status = "paid"
		out = o
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// MarkOrderPaid records a manual-payment approval: flips status to paid and
// writes the purchase ledger entry (amount 0 in core; money entered via topup
// credit in payments, mirrored by wallet.credited).
func (s *Service) MarkOrderPaid(ctx context.Context, orderID, idempotencyKey string) error {
	return s.st.WithTx(ctx, func(tx pgx.Tx) error {
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
			IdempotencyKey: idempotencyKey,
		})
		if errors.Is(err, store.ErrDuplicateIdempotency) {
			return nil
		}
		return err
	})
}

// CanStartTrial reports whether the user has never ordered a trial plan.
func (s *Service) CanStartTrial(ctx context.Context, userID string) (bool, error) {
	n, err := s.st.CountTrialOrders(ctx, s.st.Conn(), userID)
	if err != nil {
		return false, err
	}
	return n == 0, nil
}

// StartTrial creates a zero-amount, instantly-paid order on the trial plan
// and records a trial_grant ledger entry. The ledger_entries_trial_once_idx
// partial unique index closes the concurrency race: two simultaneous trials
// for one user cannot both commit.
func (s *Service) StartTrial(ctx context.Context, userID, planID, idempotencyKey string) (*store.Order, error) {
	if idempotencyKey == "" {
		return nil, errors.New("domain: idempotency key required")
	}
	plan, err := s.st.GetPlan(ctx, s.st.Conn(), planID)
	if err != nil {
		return nil, err
	}
	if !plan.IsTrial {
		return nil, fmt.Errorf("domain: plan %s is not a trial", planID)
	}
	ok, err := s.CanStartTrial(ctx, userID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrTrialAlreadyUsed
	}
	var out *store.Order
	err = s.st.WithTx(ctx, func(tx pgx.Tx) error {
		o, err := s.st.CreateOrder(ctx, tx, &store.Order{
			UserID:         userID,
			PlanID:         planID,
			Type:           "new",
			Status:         "paid",
			Amount:         0,
			Currency:       plan.Currency,
			IdempotencyKey: idempotencyKey,
		})
		if err != nil {
			return err
		}
		err = s.st.AppendLedger(ctx, tx, &store.LedgerEntry{
			UserID:         userID,
			Currency:       plan.Currency,
			Amount:         0,
			Kind:           "trial_grant",
			RefType:        strptr("order"),
			RefID:          &o.ID,
			IdempotencyKey: "trial_grant:" + o.ID,
		})
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" &&
				pgErr.ConstraintName == trialOnceConstraint {
				return ErrTrialAlreadyUsed
			}
			if errors.Is(err, store.ErrDuplicateIdempotency) {
				out = o
				return nil
			}
			return err
		}
		out = o
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func strptr(s string) *string { return &s }
