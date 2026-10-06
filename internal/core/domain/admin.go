package domain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/money"
)

// SettingKeys are the settings an admin may change from the bot (and later the
// dashboard). Values are plain text.
var SettingKeys = map[string]string{
	"payments.card_number":    "card number shown for card-to-card payments",
	"payments.card_holder":    "card holder name",
	"payments.usdt_trc20":     "USDT TRC20 deposit address",
	"payments.usdt_erc20":     "USDT ERC20 deposit address",
	"payments.usdt_rate":      "Toman per 1 USDT, to quote Toman prices in USDT",
	"payments.stars_rate":     "Toman per Telegram Star, to price plans in Stars (empty = no Stars)",
	"payments.zarinpal_link":  "your Zarinpal payment link (https://zarinp.al/...); customers pay there and send the receipt screenshot for approval",
	"referral.reward_percent": "percent of an invited user's first purchase credited to the inviter's wallet (empty or 0 = no referral program)",
	"branding.name":           "store name shown to users",
	"branding.support":        "support contact (e.g. @support)",
	"texts.fa.welcome":        "Persian welcome text",
	"texts.en.welcome":        "English welcome text",
}

const maxSettingLen = 1000

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
	var payload []byte
	if after != nil {
		b, err := json.Marshal(after)
		if err != nil {
			return err
		}
		payload = b
	}
	a := &store.Audit{ActorID: &actor.ID, Action: action, Entity: entity, After: payload}
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

// AdminSetUserStatus bans or unbans a user. The owner cannot be banned, and
// nobody can ban themselves.
func (s *Service) AdminSetUserStatus(ctx context.Context, actorTelegramID int64, userID, status, reason string) (*store.User, error) {
	actor, err := s.requireStaff(ctx, actorTelegramID)
	if err != nil {
		return nil, err
	}
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
	if target.ID == actor.ID || target.Role == "owner" {
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
	if err := validatePlan(p); err != nil {
		return nil, err
	}
	err = s.st.WithTx(ctx, func(tx pgx.Tx) error {
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

// AdminSetSetting changes one allowlisted setting.
func (s *Service) AdminSetSetting(ctx context.Context, actorTelegramID int64, key, value string) error {
	actor, err := s.requireStaff(ctx, actorTelegramID)
	if err != nil {
		return err
	}
	if _, ok := SettingKeys[key]; !ok {
		return invalid("unknown setting %q", key)
	}
	value = strings.TrimSpace(value)
	if len(value) > maxSettingLen {
		return invalid("setting value too long")
	}
	if key == "referral.reward_percent" && value != "" {
		if n, err := strconv.Atoi(value); err != nil || n < 0 || n > 100 {
			return invalid("referral.reward_percent must be a whole number from 0 to 100")
		}
	}
	if key == "payments.zarinpal_link" && value != "" {
		// Shown to customers as a link button: only a plain https link.
		if u, err := url.Parse(value); err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
			return invalid("payments.zarinpal_link must be an https link, e.g. https://zarinp.al/yourname")
		}
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return s.st.WithTx(ctx, func(tx pgx.Tx) error {
		if err := s.st.SetSetting(ctx, tx, key, raw); err != nil {
			return err
		}
		return s.audit(ctx, tx, actor, "setting.set", "settings", "", map[string]string{"key": key, "value": value}, "")
	})
}
