package domain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/money"
)

// requireStaff loads the actor and checks they are an active admin or owner.
func (s *Service) requireStaff(ctx context.Context, actorTelegramID int64) (*store.User, error) {
	if actorTelegramID <= 0 {
		return nil, ErrForbidden
	}
	u, err := s.st.GetUserByTelegramID(ctx, s.st.Conn(), actorTelegramID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrForbidden
	}
	if err != nil {
		return nil, err
	}
	if u.Status != "active" || (u.Role != "admin" && u.Role != "owner") {
		return nil, ErrForbidden
	}
	return u, nil
}

func (s *Service) audit(ctx context.Context, q pgx.Tx, actor *store.User, action, entity, entityID string, after any, reason string) error {
	return s.auditChange(ctx, q, actor, action, entity, entityID, nil, after, reason)
}

// auditChange writes an audit entry with the item's state before and after.
// A nil actor is the system (a configured rule, not a person).
func (s *Service) auditChange(ctx context.Context, q pgx.Tx, actor *store.User, action, entity, entityID string, before, after any, reason string) error {
	enc := func(v any) ([]byte, error) {
		if v == nil {
			return nil, nil
		}
		return json.Marshal(v)
	}
	b, err := enc(before)
	if err != nil {
		return err
	}
	payload, err := enc(after)
	if err != nil {
		return err
	}
	a := &store.Audit{Action: action, Entity: entity, Before: b, After: payload}
	if actor != nil {
		a.ActorID = &actor.ID
	}
	if entityID != "" {
		a.EntityID = &entityID
	}
	if reason != "" {
		a.Reason = &reason
	}
	if q != nil {
		return s.st.WriteAudit(ctx, q, a)
	}
	return s.st.WriteAudit(ctx, s.st.Conn(), a)
}

// Stats are the admin panel's headline figures. PendingPayments is -1 and
// PanelHealthy false (with a detail) when a backend could not be reached.
type Stats struct {
	store.UserCounts
	PendingPayments int64
	PanelHealthy    bool
	PanelDetail     string
}

// AdminStats gathers the figures; an unreachable backend degrades the result
// instead of failing it.
func (s *Service) AdminStats(ctx context.Context, actorTelegramID int64, pay PaymentsClient, prov Provisioner) (*Stats, error) {
	if _, err := s.requireStaff(ctx, actorTelegramID); err != nil {
		return nil, err
	}
	c, err := s.st.Counts(ctx, s.st.Conn(), time.Now().Add(-24*time.Hour))
	if err != nil {
		return nil, err
	}
	out := &Stats{UserCounts: c, PendingPayments: -1}
	if pending, err := pay.ListPending(ctx, 200); err == nil {
		out.PendingPayments = int64(len(pending))
	}
	if ok, detail, err := prov.Health(ctx); err != nil {
		out.PanelDetail = "provisioner unreachable"
	} else {
		out.PanelHealthy, out.PanelDetail = ok, detail
	}
	return out, nil
}

// PendingView is a pending payment with its payer.
type PendingView struct {
	PendingPayment
	User *store.User
}

// AdminListPendingPayments returns the manual review queue, oldest first.
func (s *Service) AdminListPendingPayments(ctx context.Context, actorTelegramID int64, pay PaymentsClient, limit int) ([]PendingView, error) {
	if _, err := s.requireStaff(ctx, actorTelegramID); err != nil {
		return nil, err
	}
	pending, err := pay.ListPending(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]PendingView, 0, len(pending))
	for _, p := range pending {
		v := PendingView{PendingPayment: p}
		if u, err := s.st.GetUser(ctx, s.st.Conn(), p.UserID); err == nil {
			v.User = u
		}
		out = append(out, v)
	}
	return out, nil
}

// AdminReviewPayment approves or rejects a manual payment as the actor.
func (s *Service) AdminReviewPayment(ctx context.Context, actorTelegramID int64, pay PaymentsClient, intentID, decision, reason string) (string, error) {
	actor, err := s.requireStaff(ctx, actorTelegramID)
	if err != nil {
		return "", err
	}
	return s.ReviewPayment(ctx, actor, pay, intentID, decision, reason)
}

// ReviewPayment approves or rejects a manual payment. Like every staff
// operation taking an actor, the caller has checked that the actor may.
func (s *Service) ReviewPayment(ctx context.Context, actor *store.User, pay PaymentsClient, intentID, decision, reason string) (string, error) {
	if decision != "approved" && decision != "rejected" {
		return "", invalid("decision must be approved or rejected")
	}
	status, err := pay.Review(ctx, actor.ID, intentID, decision, reason)
	if err != nil {
		return "", err
	}
	// The review already happened; an audit failure is logged by the caller
	// through the returned error but does not undo it.
	if err := s.audit(ctx, nil, actor, "payment.review", "payment_intent", intentID, map[string]string{"decision": decision}, reason); err != nil {
		return status, fmt.Errorf("payment reviewed but audit failed: %w", err)
	}
	return status, nil
}

// UserView is what an admin sees about a user.
type UserView struct {
	User          *store.User
	Wallets       []store.Wallet
	Subscriptions int64
	Orders        int64
}

// AdminFindUser looks a user up by Telegram id or @username.
func (s *Service) AdminFindUser(ctx context.Context, actorTelegramID int64, query string) (*UserView, error) {
	if _, err := s.requireStaff(ctx, actorTelegramID); err != nil {
		return nil, err
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, invalid("query required")
	}
	var (
		u   *store.User
		err error
	)
	if id, perr := strconv.ParseInt(query, 10, 64); perr == nil {
		u, err = s.st.GetUserByTelegramID(ctx, s.st.Conn(), id)
	} else {
		u, err = s.st.GetUserByUsername(ctx, s.st.Conn(), query)
	}
	if err != nil {
		return nil, err
	}
	wallets, err := s.st.ListWallets(ctx, s.st.Conn(), u.ID)
	if err != nil {
		return nil, err
	}
	subs, orders, err := s.st.UserActivity(ctx, s.st.Conn(), u.ID)
	if err != nil {
		return nil, err
	}
	return &UserView{User: u, Wallets: wallets, Subscriptions: subs, Orders: orders}, nil
}

// AdminAdjustBalance credits (delta > 0) or debits (delta < 0) a wallet with
// an "adjust" ledger entry. A reason is mandatory; the key makes retries safe.
func (s *Service) AdminAdjustBalance(ctx context.Context, actorTelegramID int64, userID string, delta int64, currency, reason, idemKey string) (*store.Wallet, error) {
	actor, err := s.requireStaff(ctx, actorTelegramID)
	if err != nil {
		return nil, err
	}
	return s.AdjustBalance(ctx, actor, userID, delta, currency, reason, idemKey)
}

// AdjustBalance is AdminAdjustBalance for an actor the caller has checked.
func (s *Service) AdjustBalance(ctx context.Context, actor *store.User, userID string, delta int64, currency, reason, idemKey string) (*store.Wallet, error) {
	reason = strings.TrimSpace(reason)
	switch {
	case delta == 0 || delta > maxTopupMinor || delta < -maxTopupMinor:
		return nil, invalid("adjustment out of range")
	case money.Scale(currency) < 0:
		return nil, invalid("unsupported currency %q", currency)
	case reason == "":
		return nil, invalid("a reason is required for balance changes")
	case idemKey == "":
		return nil, invalid("idempotency key required")
	}
	target, err := s.st.GetUser(ctx, s.st.Conn(), userID)
	if err != nil {
		return nil, err
	}
	var w *store.Wallet
	err = s.st.WithTx(ctx, func(tx pgx.Tx) error {
		e := &store.LedgerEntry{Kind: "adjust", RefType: strptr("admin"), RefID: strptr(actor.ID), IdempotencyKey: "adjust:" + idemKey}
		var err error
		if delta > 0 {
			w, err = s.st.Credit(ctx, tx, userID, currency, delta, e)
		} else {
			w, err = s.st.Debit(ctx, tx, userID, currency, -delta, e)
		}
		if errors.Is(err, store.ErrDuplicateIdempotency) {
			return nil // a retry: already applied
		}
		if err != nil {
			return err
		}
		if err := s.audit(ctx, tx, actor, "wallet.adjust", "user", userID,
			map[string]any{"delta": delta, "currency": currency, "balance": w.Balance}, reason); err != nil {
			return err
		}
		if delta < 0 {
			return nil
		}
		return s.publish(ctx, tx, events.WalletCredited, events.WalletCreditedEvent{
			UserID: userID, TelegramID: target.TelegramID, Amount: delta, Currency: currency,
			Balance: w.Balance, Reason: events.CreditAdminAdjust,
		})
	})
	if err != nil {
		return nil, err
	}
	if w == nil { // replay
		return s.st.GetWallet(ctx, s.st.Conn(), userID, currency)
	}
	return w, nil
}

// AdminSetUserStatus bans or unbans a customer. Nobody can ban themselves or
// a staff member (owner, admin, support): staff are managed by the owner.
func (s *Service) AdminSetUserStatus(ctx context.Context, actorTelegramID int64, userID, status, reason string) (*store.User, error) {
	actor, err := s.requireStaff(ctx, actorTelegramID)
	if err != nil {
		return nil, err
	}
	return s.SetUserStatus(ctx, actor, userID, status, reason)
}

// SetUserStatus is AdminSetUserStatus for an actor the caller has checked.
// Staff cannot ban staff: demote them first (an owner's decision).
func (s *Service) SetUserStatus(ctx context.Context, actor *store.User, userID, status, reason string) (*store.User, error) {
	if status != "active" && status != "banned" {
		return nil, invalid("status must be active or banned")
	}
	if strings.TrimSpace(reason) == "" {
		return nil, invalid("a reason is required")
	}
	target, err := s.st.GetUser(ctx, s.st.Conn(), userID)
	if err != nil {
		return nil, err
	}
	if target.ID == actor.ID || IsStaffRole(target.Role) {
		return nil, ErrForbidden
	}
	err = s.st.WithTx(ctx, func(tx pgx.Tx) error {
		if err := s.st.SetUserStatus(ctx, tx, userID, status); err != nil {
			return err
		}
		return s.audit(ctx, tx, actor, "user.status", "user", userID, map[string]string{"status": status}, reason)
	})
	if err != nil {
		return nil, err
	}
	target.Status = status
	return target, nil
}

// AdminUpsertPlan creates (empty ID) or updates a plan.
func (s *Service) AdminUpsertPlan(ctx context.Context, actorTelegramID int64, p *store.Plan) (*store.Plan, error) {
	actor, err := s.requireStaff(ctx, actorTelegramID)
	if err != nil {
		return nil, err
	}
	return s.UpsertPlan(ctx, actor, p)
}

// UpsertPlan is AdminUpsertPlan for an actor the caller has checked.
func (s *Service) UpsertPlan(ctx context.Context, actor *store.User, p *store.Plan) (*store.Plan, error) {
	if err := validatePlan(p); err != nil {
		return nil, err
	}
	if p.ID != "" {
		if _, err := s.st.GetPlan(ctx, s.st.Conn(), p.ID); err != nil {
			return nil, err // editing a plan that does not exist
		}
	}
	err := s.st.WithTx(ctx, func(tx pgx.Tx) error {
		if err := s.st.UpsertPlan(ctx, tx, p); err != nil {
			return err
		}
		return s.audit(ctx, tx, actor, "plan.upsert", "plan", p.ID, p, "")
	})
	if err != nil {
		return nil, err
	}
	return p, nil
}

func validatePlan(p *store.Plan) error {
	if p.NameI18n["en"] == "" && p.NameI18n["fa"] == "" {
		return invalid("plan needs an English or Persian name")
	}
	switch p.Kind {
	case "traffic", "time", "both":
	default:
		return invalid("plan kind must be traffic, time or both")
	}
	if p.Price < 0 || p.Price > maxTopupMinor || money.Scale(p.Currency) < 0 {
		return invalid("plan price or currency out of range")
	}
	if p.IsTrial && p.Price != 0 {
		return invalid("a trial plan must be free")
	}
	if p.IsTopup && (p.IsTrial || p.Kind != "traffic") {
		return invalid("a traffic package is a paid plan of kind traffic")
	}
	if p.DurationDays != nil && (*p.DurationDays < 0 || *p.DurationDays > 3650) {
		return invalid("duration out of range")
	}
	if p.TrafficBytes != nil && *p.TrafficBytes < 0 {
		return invalid("traffic out of range")
	}
	if (p.Kind == "time" || p.Kind == "both") && (p.DurationDays == nil || *p.DurationDays == 0) {
		return invalid("a %s plan needs a duration", p.Kind)
	}
	if (p.Kind == "traffic" || p.Kind == "both") && (p.TrafficBytes == nil || *p.TrafficBytes == 0) {
		return invalid("a %s plan needs a traffic limit", p.Kind)
	}
	return nil
}
