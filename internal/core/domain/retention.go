package domain

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/money"
)

// Order types.
const (
	OrderNew          = "new"
	OrderRenew        = "renew"
	OrderTrafficTopup = "traffic_topup"
)

var (
	// ErrNotExtendable: the subscription cannot be renewed or topped up (yet).
	ErrNotExtendable = errors.New("domain: subscription cannot be extended")
	// ErrDiscount: a discount code cannot be used; see DiscountError.Reason.
	ErrDiscount = errors.New("domain: discount code not usable")
)

// DiscountError says why a code cannot be used: unknown, expired, used_up,
// already_used or currency. Its message is "discount: <reason>", which the
// bot reads back.
type DiscountError struct{ Reason string }

func (e *DiscountError) Error() string { return "discount: " + e.Reason }
func (e *DiscountError) Unwrap() error { return ErrDiscount }

var discountCodeRe = regexp.MustCompile(`^[A-Z0-9_-]{3,32}$`)

// orderPlan checks what an order may be: a new order takes a regular plan; a
// renewal takes a regular plan for one of the user's delivered subscriptions;
// a top-up takes a traffic package for a delivered, running subscription that
// has a traffic limit.
func (s *Service) orderPlan(ctx context.Context, userID, planID, typ, subID string) (*store.Plan, *store.Subscription, error) {
	switch typ {
	case OrderNew, OrderRenew, OrderTrafficTopup:
	default:
		return nil, nil, invalid("unknown order type %q", typ)
	}
	plan, err := s.st.GetPlan(ctx, s.st.Conn(), planID)
	if err != nil {
		return nil, nil, err
	}
	switch {
	case plan.IsTrial:
		return nil, nil, fmt.Errorf("%w: trial plans are started with StartTrial", ErrPlanUnavailable)
	case !plan.Enabled:
		return nil, nil, fmt.Errorf("%w: plan is disabled", ErrPlanUnavailable)
	}
	if typ == OrderNew {
		if subID != "" {
			return nil, nil, invalid("a new order does not extend a subscription")
		}
		if plan.IsTopup {
			return nil, nil, fmt.Errorf("%w: a traffic package is bought for a subscription", ErrPlanUnavailable)
		}
		return plan, nil, nil
	}
	if subID == "" {
		return nil, nil, invalid("%s needs the subscription to extend", typ)
	}
	sub, err := s.st.GetSubscription(ctx, s.st.Conn(), subID)
	if err != nil {
		return nil, nil, err
	}
	if sub.UserID != userID {
		return nil, nil, ErrForbidden
	}
	switch {
	case sub.ServerID == "" || sub.Status == "pending":
		return nil, nil, fmt.Errorf("%w: it is not delivered yet", ErrNotExtendable)
	case sub.Status == "disabled":
		return nil, nil, fmt.Errorf("%w: it is disabled", ErrNotExtendable)
	}
	if typ == OrderRenew {
		if plan.IsTopup {
			return nil, nil, fmt.Errorf("%w: renew with a plan, not a traffic package", ErrPlanUnavailable)
		}
		return plan, sub, nil
	}
	switch {
	case !plan.IsTopup:
		return nil, nil, fmt.Errorf("%w: pick a traffic package", ErrPlanUnavailable)
	case sub.TrafficTotal == nil:
		return nil, nil, fmt.Errorf("%w: its traffic is unlimited", ErrNotExtendable)
	case sub.ExpiresAt != nil && !sub.ExpiresAt.After(time.Now()):
		return nil, nil, fmt.Errorf("%w: it expired, renew it instead", ErrNotExtendable)
	}
	return plan, sub, nil
}

// discountFor applies a discount code to a plan for a user and returns the
// amount taken off and the normalized code ("" when code is empty).
func (s *Service) discountFor(ctx context.Context, userID string, plan *store.Plan, code string) (int64, string, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		return 0, "", nil
	}
	if !discountCodeRe.MatchString(code) {
		return 0, "", &DiscountError{"unknown"}
	}
	d, err := s.st.GetDiscount(ctx, s.st.Conn(), code)
	if errors.Is(err, store.ErrNotFound) {
		return 0, "", &DiscountError{"unknown"}
	}
	if err != nil {
		return 0, "", err
	}
	switch {
	case !d.Enabled:
		return 0, "", &DiscountError{"unknown"}
	case d.ExpiresAt != nil && !time.Now().Before(*d.ExpiresAt):
		return 0, "", &DiscountError{"expired"}
	case d.MaxUses != nil && d.Used >= *d.MaxUses:
		return 0, "", &DiscountError{"used_up"}
	}
	used, err := s.st.HasRedeemed(ctx, s.st.Conn(), code, userID)
	if err != nil {
		return 0, "", err
	}
	if used {
		return 0, "", &DiscountError{"already_used"}
	}
	var off int64
	switch {
	case d.Percent != nil:
		off = plan.Price * int64(*d.Percent) / 100
	case d.Amount != nil:
		if d.Currency == nil || *d.Currency != plan.Currency {
			return 0, "", &DiscountError{"currency"}
		}
		off = min(*d.Amount, plan.Price)
	}
	return off, code, nil
}

// QuoteParams describes an order to price.
type QuoteParams struct {
	UserID, PlanID, Type, SubscriptionID, DiscountCode string
}

// Quote is an order's price before it is created.
type Quote struct {
	ListPrice, Discount, Total int64
	Currency                   string
	DiscountCode               string
}

// QuoteOrder prices an order without creating it (it fails as CreateOrder
// would), so the bot can show what a discount code takes off.
func (s *Service) QuoteOrder(ctx context.Context, p QuoteParams) (*Quote, error) {
	if p.Type == "" {
		p.Type = OrderNew
	}
	if _, err := s.activeUser(ctx, nil, p.UserID); err != nil {
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
	return &Quote{ListPrice: plan.Price, Discount: off, Total: plan.Price - off, Currency: plan.Currency, DiscountCode: code}, nil
}

// extendTargets is what a renewal or top-up sets, computed from the
// subscription as it is now: a renewal adds the plan's days to the time left
// (counted from now when it already expired) and the plan's traffic to the
// quota, so nothing unused is lost; a top-up adds traffic only. A
// subscription without expiry or traffic limit keeps it that way.
func extendTargets(typ string, sub *store.Subscription, plan *store.Plan, now time.Time) (*time.Time, *int64) {
	days, bytes := planLimits(plan)
	expires, traffic := sub.ExpiresAt, sub.TrafficTotal
	if typ == OrderRenew && days > 0 && expires != nil {
		base := *expires
		if base.Before(now) {
			base = now
		}
		t := base.Add(time.Duration(days) * 24 * time.Hour).UTC().Truncate(time.Second)
		expires = &t
	}
	if bytes > 0 && traffic != nil {
		t := *traffic + bytes
		traffic = &t
	}
	return expires, traffic
}

// orderPaidEffects runs in the transaction that marks an order paid: the
// discount code's use is counted, and a real purchase (not a trial or a free
// order) may reward whoever invited the buyer.
func (s *Service) orderPaidEffects(ctx context.Context, tx pgx.Tx, o *store.Order, source string) error {
	if o.DiscountCode != nil {
		if _, err := s.st.RecordRedemption(ctx, tx, *o.DiscountCode, o.UserID, o.ID); err != nil {
			return err
		}
	}
	if o.Amount > 0 && source != events.PaidTrial && source != events.PaidFree {
		return s.rewardReferrer(ctx, tx, o)
	}
	return nil
}

// rewardReferrer credits the inviter of the buyer with referral.reward_percent
// of the buyer's first paid purchase. The ledger key is per invited user, so
// only one purchase per invited user is ever rewarded.
func (s *Service) rewardReferrer(ctx context.Context, tx pgx.Tx, o *store.Order) error {
	pct := s.settingInt(ctx, "referral.reward_percent")
	if pct <= 0 || pct > 100 {
		return nil
	}
	buyer, err := s.st.GetUser(ctx, tx, o.UserID)
	if err != nil {
		return err
	}
	if buyer.ReferredBy == nil {
		return nil
	}
	inviter, err := s.st.GetUser(ctx, tx, *buyer.ReferredBy)
	if err != nil {
		return err
	}
	reward := o.Amount * pct / 100
	if inviter.Status != "active" || reward <= 0 {
		return nil
	}
	w, err := s.st.Credit(ctx, tx, inviter.ID, o.Currency, reward, &store.LedgerEntry{
		Kind: "referral", RefType: strptr("user"), RefID: strptr(buyer.ID),
		IdempotencyKey: "referral:" + buyer.ID,
	})
	// The key exists already: an earlier purchase was rewarded (a different
	// amount reads as a conflict). Nothing was written, so the payment goes on.
	if errors.Is(err, store.ErrDuplicateIdempotency) || errors.Is(err, store.ErrIdempotencyConflict) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.publish(ctx, tx, events.WalletCredited, events.WalletCreditedEvent{
		UserID: inviter.ID, TelegramID: inviter.TelegramID, OrderID: o.ID,
		Amount: reward, Currency: o.Currency, Balance: w.Balance, Reason: events.CreditReferral,
	})
}

// ReferralInfo is what a user sees about their invitations.
type ReferralInfo struct {
	Code              string
	Invited, Rewarded int
	Earned            map[string]int64 // per currency
	RewardPercent     int64
}

// Referral returns a user's invite code (created on first request) and stats.
func (s *Service) Referral(ctx context.Context, userID string) (*ReferralInfo, error) {
	u, err := s.activeUser(ctx, nil, userID)
	if err != nil {
		return nil, err
	}
	code := ""
	if u.RefCode != nil {
		code = *u.RefCode
	}
	for i := 0; code == "" && i < 5; i++ {
		code, err = s.st.SetRefCode(ctx, s.st.Conn(), userID, newRefCode())
		if errors.Is(err, store.ErrRefCodeTaken) {
			continue
		}
		if err != nil {
			return nil, err
		}
	}
	if code == "" {
		return nil, errors.New("could not draw a free invite code")
	}
	invited, rewarded, earned, err := s.st.ReferralStats(ctx, s.st.Conn(), userID)
	if err != nil {
		return nil, err
	}
	return &ReferralInfo{Code: code, Invited: invited, Rewarded: rewarded, Earned: earned,
		RewardPercent: s.settingInt(ctx, "referral.reward_percent")}, nil
}

// newRefCode draws an 8-character invite code (lower case, no look-alikes).
func newRefCode() string {
	const alphabet = "abcdefghjkmnpqrstuvwxyz23456789"
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}

var refCodeRe = regexp.MustCompile(`^[a-z0-9]{4,16}$`)

// referrerFor resolves an invite code to the inviter's id ("" when unknown).
func (s *Service) referrerFor(ctx context.Context, code string) string {
	code = strings.ToLower(strings.TrimSpace(code))
	if !refCodeRe.MatchString(code) {
		return ""
	}
	u, err := s.st.UserByRefCode(ctx, s.st.Conn(), code)
	if err != nil || u.Status != "active" {
		return ""
	}
	return u.ID
}

// AdminUpsertDiscount creates a discount code or changes its terms.
func (s *Service) AdminUpsertDiscount(ctx context.Context, actorTelegramID int64, d *store.Discount) (*store.Discount, error) {
	actor, err := s.requireStaff(ctx, actorTelegramID)
	if err != nil {
		return nil, err
	}
	d.Code = strings.ToUpper(strings.TrimSpace(d.Code))
	if !discountCodeRe.MatchString(d.Code) {
		return nil, invalid("a code is 3-32 letters, digits, _ or -")
	}
	switch {
	case (d.Percent == nil) == (d.Amount == nil):
		return nil, invalid("a discount is either a percentage or an amount")
	case d.Percent != nil && (*d.Percent < 1 || *d.Percent > 100):
		return nil, invalid("the percentage must be 1-100")
	case d.Amount != nil && (*d.Amount <= 0 || d.Currency == nil || money.Scale(*d.Currency) < 0):
		return nil, invalid("an amount discount needs a positive amount and a known currency")
	case d.MaxUses != nil && *d.MaxUses <= 0:
		return nil, invalid("max uses must be positive (or unlimited)")
	}
	if d.Percent != nil {
		d.Currency = nil
	}
	var out *store.Discount
	err = s.st.WithTx(ctx, func(tx pgx.Tx) error {
		got, err := s.st.UpsertDiscount(ctx, tx, d)
		if err != nil {
			return err
		}
		out = got
		return s.audit(ctx, tx, actor, "discount.upsert", "discount", "", got, "") // entity ids are uuids; the code is in the payload
	})
	return out, err
}

// AdminListDiscounts lists the discount codes.
func (s *Service) AdminListDiscounts(ctx context.Context, actorTelegramID int64) ([]store.Discount, error) {
	if _, err := s.requireStaff(ctx, actorTelegramID); err != nil {
		return nil, err
	}
	return s.st.ListDiscounts(ctx, s.st.Conn())
}
