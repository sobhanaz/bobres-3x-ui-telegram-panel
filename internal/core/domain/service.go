// Package domain holds the core business rules: users, orders with
// idempotency, wallet payment, one trial per user, and applying payment
// events. Every state change and the event announcing it commit together.
package domain

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/eventbus"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
)

// trialOnceConstraint is the partial unique index enforcing one trial grant per user.
const trialOnceConstraint = "ledger_entries_trial_once_idx"

// Config tunes the domain.
type Config struct {
	// OwnerTelegramID is promoted to the owner role on first contact.
	OwnerTelegramID int64
}

// Service composes the store with business rules.
type Service struct {
	st   *store.Store
	ob   *eventbus.Outbox
	cfg  Config
	kick chan struct{} // wakes the provisioning worker
}

// New builds a Service publishing to core's outbox.
func New(st *store.Store, cfg Config) *Service {
	return &Service{st: st, ob: eventbus.NewOutbox("outbox_core"), cfg: cfg, kick: make(chan struct{}, 1)}
}

func (s *Service) publish(ctx context.Context, tx pgx.Tx, topic string, payload any) error {
	_, err := s.ob.Publish(ctx, tx, topic, payload)
	return err
}

// UpsertUserParams carries the inputs of UpsertUser.
type UpsertUserParams struct {
	TelegramID int64
	Username   string
	Language   string
	ReferredBy string
	// ReferralCode is the inviter's code from the bot's start link; like
	// ReferredBy it only counts when the user is created.
	ReferralCode string
}

// UpsertUser registers or refreshes a Telegram user. The configured owner is
// promoted to the owner role the first time they appear.
func (s *Service) UpsertUser(ctx context.Context, p UpsertUserParams) (*store.User, error) {
	if p.TelegramID <= 0 {
		return nil, invalid("telegram id required")
	}
	switch p.Language {
	case "", "fa", "en":
	default:
		return nil, invalid("language must be fa or en")
	}
	if len(p.Username) > 64 {
		return nil, invalid("username too long")
	}
	referredBy := p.ReferredBy
	if referredBy == "" && p.ReferralCode != "" {
		referredBy = s.referrerFor(ctx, p.ReferralCode) // applied only when the user is created
	}
	u, err := s.st.UpsertUser(ctx, s.st.Conn(), p.TelegramID, p.Username, p.Language, referredBy)
	if err != nil {
		return nil, err
	}
	if s.cfg.OwnerTelegramID != 0 && u.TelegramID == s.cfg.OwnerTelegramID && u.Role != "owner" {
		if err := s.st.SetUserRole(ctx, s.st.Conn(), u.ID, "owner", "active"); err != nil {
			return nil, err
		}
		u.Role, u.Status = "owner", "active"
	}
	return u, nil
}

// activeUser loads a user and refuses banned ones.
func (s *Service) activeUser(ctx context.Context, q pgx.Tx, userID string) (*store.User, error) {
	var (
		u   *store.User
		err error
	)
	if q != nil {
		u, err = s.st.GetUser(ctx, q, userID)
	} else {
		u, err = s.st.GetUser(ctx, s.st.Conn(), userID)
	}
	if err != nil {
		return nil, err
	}
	if u.Status != "active" {
		return nil, ErrUserBanned
	}
	return u, nil
}

// CreateOrderParams carries the inputs of CreateOrder.
type CreateOrderParams struct {
	UserID         string
	PlanID         string
	Type           string // new | renew | traffic_topup
	IdempotencyKey string
	SubscriptionID string // renew | traffic_topup
	DiscountCode   string
}

// CreateOrder validates the plan (and, for a renewal or top-up, the
// subscription), applies a discount code, and inserts an order. Retries with
// the same IdempotencyKey return the existing order unchanged. Trial plans
// are refused here: they go through StartTrial, which enforces one trial per
// user.
func (s *Service) CreateOrder(ctx context.Context, p CreateOrderParams) (*store.Order, error) {
	if p.IdempotencyKey == "" {
		return nil, invalid("idempotency key required")
	}
	if p.Type == "" {
		p.Type = OrderNew
	}
	u, err := s.activeUser(ctx, nil, p.UserID)
	if err != nil {
		return nil, err
	}
	plan, _, err := s.orderPlan(ctx, p.UserID, p.PlanID, p.Type, p.SubscriptionID)
	if err != nil {
		return nil, err
	}
	off, code, err := s.discountFor(ctx, p.UserID, plan, p.DiscountCode)
	if err != nil {
		return nil, err
	}
	amount := plan.Price - off
	free := amount == 0
	status := "awaiting_payment"
	if free {
		status = "paid"
	}
	var subID, discountCode *string
	if p.SubscriptionID != "" {
		subID = &p.SubscriptionID
	}
	if code != "" {
		discountCode = &code
	}
	var out *store.Order
	err = s.st.WithTx(ctx, func(tx pgx.Tx) error {
		o, inserted, err := s.st.CreateOrder(ctx, tx, &store.Order{
			UserID: p.UserID, PlanID: p.PlanID, Type: p.Type, Status: status,
			Amount: amount, Currency: plan.Currency, IdempotencyKey: p.IdempotencyKey,
			SubscriptionID: subID, DiscountCode: discountCode, DiscountAmount: off,
		})
		if err != nil {
			return err
		}
		out = o
		if free && inserted {
			return s.publishOrderPaid(ctx, tx, o, u.TelegramID, events.PaidFree)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if free {
		s.kickProvisioning()
	}
	return out, nil
}

// publishOrderPaid announces a paid order, in the transaction that marks it
// paid, after the effects of paying (discount use, referral reward).
func (s *Service) publishOrderPaid(ctx context.Context, tx pgx.Tx, o *store.Order, telegramID int64, source string) error {
	if err := s.orderPaidEffects(ctx, tx, o, source); err != nil {
		return err
	}
	return s.publish(ctx, tx, events.OrderPaid, events.OrderPaidEvent{
		OrderID: o.ID, UserID: o.UserID, TelegramID: telegramID, PlanID: o.PlanID,
		Amount: o.Amount, Currency: o.Currency, Source: source,
	})
}

// PayOrderWithWallet debits the buyer's wallet and marks the order paid in one
// transaction. The order row is locked first, so a double tap (or any
// concurrent retry) sees the order already paid and debits nothing. The
// purchase is recorded under "purchase:<order id>", so one order can never be
// charged twice even by a buggy caller. userID, when set, must own the order.
func (s *Service) PayOrderWithWallet(ctx context.Context, orderID, userID string) (*store.Order, error) {
	if orderID == "" {
		return nil, invalid("order id required")
	}
	var out *store.Order
	err := s.st.WithTx(ctx, func(tx pgx.Tx) error {
		o, err := s.st.LockOrder(ctx, tx, orderID)
		if err != nil {
			return err
		}
		if userID != "" && o.UserID != userID {
			return ErrForbidden
		}
		if isPaid(o.Status) {
			out = o // replay or double tap: already paid, nothing to do
			return nil
		}
		if !isPayable(o.Status) {
			return ErrOrderNotPayable
		}
		u, err := s.activeUser(ctx, tx, o.UserID)
		if err != nil {
			return err
		}
		_, err = s.st.Debit(ctx, tx, o.UserID, o.Currency, o.Amount, &store.LedgerEntry{
			Kind: "purchase", RefType: strptr("order"), RefID: strptr(o.ID),
			IdempotencyKey: "purchase:" + o.ID,
		})
		if errors.Is(err, store.ErrDuplicateIdempotency) {
			// The purchase is in the ledger but the order is unpaid: the data is
			// inconsistent. Refuse rather than guess.
			return fmt.Errorf("order %s has a recorded purchase but is %s: %w", o.ID, o.Status, store.ErrIdempotencyConflict)
		}
		if err != nil {
			return err
		}
		if ok, err := s.st.TransitionOrder(ctx, tx, o.ID, payableStatuses, "paid"); err != nil || !ok {
			return errors.Join(err, ErrOrderNotPayable)
		}
		o.Status = "paid"
		out = o
		return s.publishOrderPaid(ctx, tx, o, u.TelegramID, events.PaidByWallet)
	})
	if err != nil {
		return nil, err
	}
	s.kickProvisioning()
	return out, nil
}

// CanStartTrial reports whether the user has never ordered a trial plan.
func (s *Service) CanStartTrial(ctx context.Context, userID string) (bool, error) {
	n, err := s.st.CountTrialOrders(ctx, s.st.Conn(), userID)
	if err != nil {
		return false, err
	}
	return n == 0, nil
}

// StartTrial creates a zero-amount, instantly-paid order on an enabled trial
// plan and records a trial_grant ledger entry. The ledger_entries_trial_once_idx
// partial unique index closes the concurrency race: two simultaneous trials
// for one user cannot both commit. A retry with the same idempotency key
// returns the original order.
func (s *Service) StartTrial(ctx context.Context, userID, planID, idempotencyKey string) (*store.Order, error) {
	if idempotencyKey == "" {
		return nil, invalid("idempotency key required")
	}
	if prev, err := s.st.OrderByIdempotencyKey(ctx, s.st.Conn(), idempotencyKey); err == nil {
		if prev.UserID != userID || prev.PlanID != planID {
			return nil, store.ErrIdempotencyConflict
		}
		return prev, nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	u, err := s.activeUser(ctx, nil, userID)
	if err != nil {
		return nil, err
	}
	plan, err := s.st.GetPlan(ctx, s.st.Conn(), planID)
	if err != nil {
		return nil, err
	}
	if !plan.IsTrial {
		return nil, invalid("plan %s is not a trial plan", planID)
	}
	if !plan.Enabled {
		return nil, fmt.Errorf("%w: the free trial is turned off", ErrPlanUnavailable)
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
		o, _, err := s.st.CreateOrder(ctx, tx, &store.Order{
			UserID: userID, PlanID: planID, Type: "new", Status: "paid",
			Amount: 0, Currency: plan.Currency, IdempotencyKey: idempotencyKey,
		})
		if err != nil {
			return err
		}
		err = s.st.AppendLedger(ctx, tx, &store.LedgerEntry{
			UserID: userID, Currency: plan.Currency, Kind: "trial_grant",
			RefType: strptr("order"), RefID: strptr(o.ID), IdempotencyKey: "trial_grant:" + o.ID,
		})
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == trialOnceConstraint {
			return ErrTrialAlreadyUsed
		}
		if err != nil {
			return err
		}
		out = o
		return s.publishOrderPaid(ctx, tx, o, u.TelegramID, events.PaidTrial)
	})
	if err != nil {
		return nil, err
	}
	s.kickProvisioning()
	return out, nil
}
