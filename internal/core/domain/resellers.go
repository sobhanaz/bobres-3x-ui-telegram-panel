package domain

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/money"
	buuid "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
)

// Resellers (milestone 5). A reseller is a customer the owner or an admin
// made one: they buy services for their own customers in the bot at their
// tier's price, on credit down to a limit per currency, and earn a commission
// on every purchase by a customer who joined through their invite link
// (whenever that customer joined). They never use the dashboard.
//
// The license's "resellers" feature is not checked yet: the License page
// (milestone 6) brings it.

// ResellerRole is the role of a user who resells.
const ResellerRole = "reseller"

// MaxLabel is the longest name a reseller can give a service, in characters.
const MaxLabel = 64

// Limits of a reseller's terms.
const (
	maxTierDiscount   = 90
	maxCommission     = 50
	maxTierName       = 40
	maxResellerNote   = 500
	resellerStaleText = "someone changed this reseller meanwhile; reload and try again"
)

func isReseller(u *store.User) bool { return u.Role == ResellerRole }

// resellerPricing is what an active reseller pays: their tier's discount and
// fixed prices. ok is false for anyone else, who pays the store price.
func (s *Service) resellerPricing(ctx context.Context, u *store.User) (discount int32, fixed map[string]int64, ok bool, err error) {
	if u.Status != "active" || !isReseller(u) {
		return 0, nil, false, nil
	}
	t, err := s.st.ResellerTerms(ctx, s.st.Conn(), u.ID)
	if errors.Is(err, store.ErrNotFound) {
		return 0, nil, false, nil
	}
	if err != nil {
		return 0, nil, false, err
	}
	fixed, err = s.st.TierPrices(ctx, s.st.Conn(), t.TierID)
	if err != nil {
		return 0, nil, false, err
	}
	return t.DiscountPercent, fixed, true, nil
}

// tierPrice is a plan's price in a tier: its fixed price when the tier has
// one, else the store price less the discount (rounded down).
func tierPrice(plan *store.Plan, discount int32, fixed map[string]int64) int64 {
	if p, ok := fixed[plan.ID]; ok {
		return p
	}
	return plan.Price * int64(100-discount) / 100
}

// priceFor is what a user pays for a plan before any discount code: an active
// reseller pays their tier's price, everyone else the store price.
func (s *Service) priceFor(ctx context.Context, u *store.User, plan *store.Plan) (int64, error) {
	discount, fixed, ok, err := s.resellerPricing(ctx, u)
	if err != nil || !ok {
		return plan.Price, err
	}
	return tierPrice(plan, discount, fixed), nil
}

// PlanPrices is what a user pays for each plan (the bot's plan lists): the
// tier price for an active reseller, the store price for anyone else.
func (s *Service) PlanPrices(ctx context.Context, userID string, plans []store.Plan) ([]int64, error) {
	u, err := s.st.GetUser(ctx, s.st.Conn(), userID)
	if err != nil {
		return nil, err
	}
	discount, fixed, ok, err := s.resellerPricing(ctx, u)
	if err != nil {
		return nil, err
	}
	out := make([]int64, len(plans))
	for i := range plans {
		out[i] = plans[i].Price
		if ok {
			out[i] = tierPrice(&plans[i], discount, fixed)
		}
	}
	return out, nil
}

// normalizeLabel checks the name of a service: one line of plain text, at
// most MaxLabel characters. Empty clears it (nil).
func normalizeLabel(label string) (*string, error) {
	label = strings.TrimSpace(label)
	switch {
	case label == "":
		return nil, nil // no name
	case !utf8.ValidString(label) || strings.ContainsFunc(label, unicode.IsControl):
		return nil, invalid("a service name is one line of plain text")
	case utf8.RuneCountInString(label) > MaxLabel:
		return nil, invalid("a service name is at most %d characters", MaxLabel)
	}
	return &label, nil
}

// SetSubscriptionLabel names one of a reseller's services ("" clears the
// name). Only resellers name services, and only their own: anyone else's
// service is not found.
func (s *Service) SetSubscriptionLabel(ctx context.Context, userID, subscriptionID, label string) (*store.Subscription, error) {
	u, err := s.activeUser(ctx, nil, userID)
	if err != nil {
		return nil, err
	}
	if !isReseller(u) {
		return nil, ErrForbidden
	}
	name, err := normalizeLabel(label)
	if err != nil {
		return nil, err
	}
	if _, err := buuid.Parse(subscriptionID); err != nil {
		return nil, store.ErrNotFound
	}
	sub, err := s.st.GetSubscription(ctx, s.st.Conn(), subscriptionID)
	if err != nil {
		return nil, err
	}
	if sub.UserID != u.ID || sub.Status == "deleted" {
		return nil, store.ErrNotFound
	}
	if err := s.st.SetSubscriptionLabel(ctx, s.st.Conn(), sub.ID, name); err != nil {
		return nil, err
	}
	sub.Label = name
	return sub, nil
}

// ResellerInfo is what a reseller sees on their panel in the bot.
type ResellerInfo struct {
	Terms     store.ResellerTerms
	Wallets   []store.Wallet   // with their credit limits; IRT always listed
	Customers int64            // users who joined through their link
	Earned    map[string]int64 // commissions per currency
	RefCode   string
}

// ResellerInfo returns a reseller's terms, balances and earnings; anyone who
// is not a reseller is not found. The invite code is created on first use.
func (s *Service) ResellerInfo(ctx context.Context, userID string) (*ResellerInfo, error) {
	u, err := s.activeUser(ctx, nil, userID)
	if err != nil {
		return nil, err
	}
	if !isReseller(u) {
		return nil, store.ErrNotFound
	}
	terms, err := s.st.ResellerTerms(ctx, s.st.Conn(), u.ID)
	if err != nil {
		return nil, err
	}
	wallets, err := s.st.ListWallets(ctx, s.st.Conn(), u.ID)
	if err != nil {
		return nil, err
	}
	if !slices.ContainsFunc(wallets, func(w store.Wallet) bool { return w.Currency == "IRT" }) {
		wallets = append([]store.Wallet{{UserID: u.ID, Currency: "IRT"}}, wallets...)
	}
	customers, err := s.st.CountInvited(ctx, s.st.Conn(), u.ID)
	if err != nil {
		return nil, err
	}
	earned, err := s.st.CommissionEarned(ctx, s.st.Conn(), u.ID)
	if err != nil {
		return nil, err
	}
	code, err := s.refCode(ctx, u)
	if err != nil {
		return nil, err
	}
	return &ResellerInfo{Terms: *terms, Wallets: wallets, Customers: customers, Earned: earned, RefCode: code}, nil
}

// payCommission credits a reseller their commission on a paid order of a
// customer who joined through their link: every order pays, once (ledger key
// commission:<order id>). Only an active reseller is paid.
func (s *Service) payCommission(ctx context.Context, tx pgx.Tx, o *store.Order, reseller *store.User) error {
	if reseller.Status != "active" {
		return nil
	}
	terms, err := s.st.ResellerTerms(ctx, tx, reseller.ID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	amount := o.Amount * int64(terms.CommissionPercent) / 100
	if amount <= 0 {
		return nil
	}
	w, err := s.st.Credit(ctx, tx, reseller.ID, o.Currency, amount, &store.LedgerEntry{
		Kind: "commission", RefType: strptr("order"), RefID: strptr(o.ID), IdempotencyKey: "commission:" + o.ID,
	})
	if errors.Is(err, store.ErrDuplicateIdempotency) {
		return nil // paid already: nothing was written
	}
	if err != nil {
		return err // a conflict means the books disagree: the payment rolls back
	}
	return s.publish(ctx, tx, events.WalletCredited, events.WalletCreditedEvent{
		UserID: reseller.ID, TelegramID: reseller.TelegramID, OrderID: o.ID,
		Amount: amount, Currency: o.Currency, Balance: w.Balance, Reason: events.CreditCommission,
	})
}

// ResellerInput is a reseller's terms as staff set them.
type ResellerInput struct {
	TierID string
	// CreditLimits per currency (minor units); currencies left out keep
	// their limit.
	CreditLimits      map[string]int64
	CommissionPercent *int32 // nil: the tier's
	Note              string
}

func (in *ResellerInput) normalize() error {
	if in.CreditLimits == nil {
		in.CreditLimits = map[string]int64{}
	}
	in.Note = strings.TrimSpace(in.Note)
	if utf8.RuneCountInString(in.Note) > maxResellerNote {
		return &FieldError{"note", fmt.Sprintf("at most %d characters", maxResellerNote)}
	}
	if c := in.CommissionPercent; c != nil && (*c < 0 || *c > maxCommission) {
		return &FieldError{"commission_percent", fmt.Sprintf("0 to %d, or empty for the tier's", maxCommission)}
	}
	for _, cur := range slices.Sorted(maps.Keys(in.CreditLimits)) {
		lim := in.CreditLimits[cur]
		switch {
		case money.Scale(cur) < 0:
			return &FieldError{"credit_limit." + cur, "unknown currency"}
		case lim < 0 || lim > maxTopupMinor:
			return &FieldError{"credit_limit." + cur, "out of range"}
		}
	}
	return nil
}

// resellerTier loads the tier a reseller is put on; a missing one is the
// field's error.
func (s *Service) resellerTier(ctx context.Context, tx pgx.Tx, id string) (*store.ResellerTier, error) {
	if _, err := buuid.Parse(id); err != nil {
		return nil, &FieldError{"tier_id", "pick a tier"}
	}
	t, err := s.st.GetTier(ctx, tx, id, false)
	if errors.Is(err, store.ErrNotFound) {
		return nil, &FieldError{"tier_id", "pick a tier"}
	}
	return t, err
}

// resellerAudit is a reseller's terms as the audit log records them.
func resellerAudit(r *store.Reseller, tier string, limits map[string]int64) map[string]any {
	return map[string]any{"role": ResellerRole, "tier_id": r.TierID, "tier": tier,
		"commission_percent": r.CommissionPercent, "credit_limits": limits, "note": r.Note}
}

func limitsOf(wallets []store.Wallet) map[string]int64 {
	out := map[string]int64{}
	for _, w := range wallets {
		out[w.Currency] = w.CreditLimit
	}
	return out
}

// MakeReseller makes an active customer a reseller on a tier, with credit
// limits per currency. A staff member must be removed from the staff first.
func (s *Service) MakeReseller(ctx context.Context, actor *store.User, userID string, in ResellerInput, reason string) (*store.Reseller, error) {
	reason, err := needReasonText(reason)
	if err != nil {
		return nil, err
	}
	if err := in.normalize(); err != nil {
		return nil, err
	}
	t, err := s.st.GetUser(ctx, s.st.Conn(), userID)
	if err != nil {
		return nil, err
	}
	switch {
	case s.IsConfiguredOwner(t):
		return nil, stateErr("the store owner cannot be a reseller")
	case isReseller(t):
		return nil, stateErr("this person is a reseller already")
	case t.Role != "user":
		return nil, stateErr("only customers can become resellers; remove them from the staff first")
	case t.Status != "active":
		return nil, stateErr("a banned customer cannot become a reseller; unban them first")
	}
	r := &store.Reseller{UserID: t.ID, TierID: in.TierID, CommissionPercent: in.CommissionPercent, Note: in.Note}
	err = s.st.WithTx(ctx, func(tx pgx.Tx) error {
		tier, err := s.resellerTier(ctx, tx, in.TierID)
		if err != nil {
			return err
		}
		if err := s.st.SetRole(ctx, tx, t.ID, "user", ResellerRole); errors.Is(err, store.ErrNotFound) {
			return stateErr("this person changed meanwhile; reload and try again")
		} else if err != nil {
			return err
		}
		if err := s.st.InsertReseller(ctx, tx, r); err != nil {
			return err
		}
		for cur, lim := range in.CreditLimits {
			if err := s.st.SetCreditLimit(ctx, tx, t.ID, cur, lim); err != nil {
				return err
			}
		}
		return s.auditChange(ctx, tx, actor, "reseller.add", "reseller", t.ID,
			map[string]string{"role": "user"}, resellerAudit(r, tier.Name, in.CreditLimits), reason)
	})
	if err != nil {
		return nil, err
	}
	return r, nil
}

// UpdateReseller changes a reseller's terms read at version. A credit limit
// cannot go below what they owe in that currency.
func (s *Service) UpdateReseller(ctx context.Context, actor *store.User, userID string, version int, in ResellerInput, reason string) (*store.Reseller, error) {
	reason, err := needReasonText(reason)
	if err != nil {
		return nil, err
	}
	if err := in.normalize(); err != nil {
		return nil, err
	}
	r := &store.Reseller{UserID: userID, TierID: in.TierID, CommissionPercent: in.CommissionPercent, Note: in.Note}
	err = s.st.WithTx(ctx, func(tx pgx.Tx) error {
		old, err := s.st.GetReseller(ctx, tx, userID, true)
		if err != nil {
			return err
		}
		if old.Version != version {
			return stateErr(resellerStaleText)
		}
		oldTier, err := s.st.GetTier(ctx, tx, old.TierID, false)
		if err != nil {
			return err
		}
		tier, err := s.resellerTier(ctx, tx, in.TierID)
		if err != nil {
			return err
		}
		wallets, err := s.st.LockWallets(ctx, tx, userID)
		if err != nil {
			return err
		}
		before, after := limitsOf(wallets), limitsOf(wallets)
		for _, cur := range slices.Sorted(maps.Keys(in.CreditLimits)) {
			lim := in.CreditLimits[cur]
			for _, w := range wallets {
				if w.Currency == cur && -w.Balance > lim {
					return &FieldError{"credit_limit." + cur, "lower than what they owe now (" + formatMoney(-w.Balance, cur) + ")"}
				}
			}
			after[cur] = lim
		}
		if err := s.st.UpdateReseller(ctx, tx, r, version); errors.Is(err, store.ErrStale) {
			return stateErr(resellerStaleText)
		} else if err != nil {
			return err
		}
		for cur, lim := range in.CreditLimits {
			if err := s.st.SetCreditLimit(ctx, tx, userID, cur, lim); err != nil {
				return err
			}
		}
		return s.auditChange(ctx, tx, actor, "reseller.terms", "reseller", userID,
			resellerAudit(old, oldTier.Name, before), resellerAudit(r, tier.Name, after), reason)
	})
	if err != nil {
		return nil, err
	}
	return r, nil
}

// RemoveReseller makes a reseller a customer again: refused while they owe
// money; their credit limits go to 0. Their unpaid orders are cancelled:
// they carry tier prices, and paying one as a customer would buy at the
// tier price and pay their inviter. A payment that still arrives for one
// stays in their wallet (order_not_payable).
func (s *Service) RemoveReseller(ctx context.Context, actor *store.User, userID, reason string) error {
	reason, err := needReasonText(reason)
	if err != nil {
		return err
	}
	return s.st.WithTx(ctx, func(tx pgx.Tx) error {
		// The row lock waits for any order being created at a tier price
		// (CreateOrder holds the row), so the cancel below sees that order too.
		r, err := s.st.GetReseller(ctx, tx, userID, true)
		if err != nil {
			return err
		}
		// Orders before wallets, the order PayOrderWithWallet locks them in.
		cancelled, err := s.st.CancelOpenOrders(ctx, tx, userID)
		if err != nil {
			return err
		}
		tier, err := s.st.GetTier(ctx, tx, r.TierID, false)
		if err != nil {
			return err
		}
		wallets, err := s.st.LockWallets(ctx, tx, userID)
		if err != nil {
			return err
		}
		for _, w := range wallets {
			if w.Balance < 0 {
				return stateErr("they owe %s; record their payment first", formatMoney(-w.Balance, w.Currency))
			}
		}
		if err := s.st.ClearCreditLimits(ctx, tx, userID); err != nil {
			return err
		}
		if err := s.st.DeleteReseller(ctx, tx, userID); err != nil {
			return err
		}
		if err := s.st.SetRole(ctx, tx, userID, ResellerRole, "user"); errors.Is(err, store.ErrNotFound) {
			return stateErr("this person changed meanwhile; reload and try again")
		} else if err != nil {
			return err
		}
		after := map[string]any{"role": "user"}
		if cancelled > 0 {
			after["cancelled_orders"] = cancelled
		}
		return s.auditChange(ctx, tx, actor, "reseller.remove", "reseller", userID,
			resellerAudit(r, tier.Name, limitsOf(wallets)), after, reason)
	})
}

// RecordResellerPayment records money a reseller paid the store: a
// settlement credit (ledger key settlement:<key>) that lowers what they owe.
// A retry with the same key returns the first entry and changes nothing.
func (s *Service) RecordResellerPayment(ctx context.Context, actor *store.User, userID string, amount int64, currency, reason, key string) (*store.Wallet, *store.LedgerEntry, error) {
	reason, err := needReasonText(reason)
	if err != nil {
		return nil, nil, err
	}
	switch {
	case amount <= 0 || amount > maxTopupMinor:
		return nil, nil, invalid("the amount must be positive")
	case money.Scale(currency) < 0:
		return nil, nil, invalid("unsupported currency %q", currency)
	case key == "":
		return nil, nil, invalid("idempotency key required")
	}
	var (
		w *store.Wallet
		e = &store.LedgerEntry{Kind: "settlement", RefType: strptr("admin"), RefID: strptr(actor.ID),
			IdempotencyKey: "settlement:" + key}
	)
	err = s.st.WithTx(ctx, func(tx pgx.Tx) error {
		if _, err := s.st.GetReseller(ctx, tx, userID, false); err != nil {
			return err
		}
		u, err := s.st.GetUser(ctx, tx, userID)
		if err != nil {
			return err
		}
		w, err = s.st.Credit(ctx, tx, userID, currency, amount, e)
		if errors.Is(err, store.ErrDuplicateIdempotency) {
			return nil // a retry: e is the first entry
		}
		if err != nil {
			return err
		}
		if err := s.audit(ctx, tx, actor, "reseller.payment", "reseller", userID,
			map[string]any{"amount": amount, "currency": currency, "balance": w.Balance}, reason); err != nil {
			return err
		}
		return s.publish(ctx, tx, events.WalletCredited, events.WalletCreditedEvent{
			UserID: userID, TelegramID: u.TelegramID, Amount: amount, Currency: currency,
			Balance: w.Balance, Reason: events.CreditSettlement,
		})
	})
	if err != nil {
		return nil, nil, err
	}
	return w, e, nil
}

// formatMoney is an amount for a message to staff ("125000 IRT").
func formatMoney(amount int64, currency string) string {
	a, err := money.New(amount, currency)
	if err != nil {
		return fmt.Sprintf("%d %s", amount, currency)
	}
	return money.Format(a)
}

// TierInput is a reseller tier as staff set it.
type TierInput struct {
	Name              string
	DiscountPercent   int32
	CommissionPercent int32
	Prices            map[string]int64 // plan id -> fixed price (minor units of the plan's currency)
	// Currencies: plan id -> the currency each price was read in (the plan's
	// then); a plan whose currency changed since is refused.
	Currencies map[string]string
}

func (s *Service) normalizeTier(ctx context.Context, in *TierInput) error {
	in.Name = strings.TrimSpace(in.Name)
	switch n := utf8.RuneCountInString(in.Name); {
	case n == 0 || n > maxTierName || strings.ContainsFunc(in.Name, unicode.IsControl):
		return &FieldError{"name", fmt.Sprintf("1 to %d characters", maxTierName)}
	case in.DiscountPercent < 0 || in.DiscountPercent > maxTierDiscount:
		return &FieldError{"discount_percent", fmt.Sprintf("0 to %d", maxTierDiscount)}
	case in.CommissionPercent < 0 || in.CommissionPercent > maxCommission:
		return &FieldError{"commission_percent", fmt.Sprintf("0 to %d", maxCommission)}
	}
	for _, id := range slices.Sorted(maps.Keys(in.Prices)) {
		field := "prices." + id
		if _, err := buuid.Parse(id); err != nil {
			return &FieldError{field, "no such plan"}
		}
		plan, err := s.st.GetPlan(ctx, s.st.Conn(), id)
		if errors.Is(err, store.ErrNotFound) {
			return &FieldError{field, "no such plan"}
		}
		if err != nil {
			return err
		}
		if plan.IsTrial {
			return &FieldError{field, "the free trial has no price"}
		}
		if p := in.Prices[id]; p < 0 || p > maxTopupMinor {
			return &FieldError{field, "out of range"}
		}
		if in.Currencies == nil {
			in.Currencies = map[string]string{}
		}
		if c := in.Currencies[id]; c != "" && c != plan.Currency {
			return &FieldError{field, "the plan's currency changed; reload"}
		}
		in.Currencies[id] = plan.Currency
	}
	return nil
}

// optionalReason is a reason staff may give (at most 500 characters).
func optionalReason(reason string) (string, error) {
	if strings.TrimSpace(reason) == "" {
		return "", nil
	}
	return needReasonText(reason)
}

func tierAudit(t *store.ResellerTier) map[string]any {
	return map[string]any{"name": t.Name, "discount_percent": t.DiscountPercent,
		"commission_percent": t.CommissionPercent, "prices": t.Prices}
}

func tierWriteErr(err error) error {
	switch {
	case errors.Is(err, store.ErrTierNameTaken):
		return &FieldError{"name", "another tier has this name"}
	case errors.Is(err, store.ErrStale):
		return stateErr("someone changed this tier meanwhile; reload it")
	}
	return err
}

// ListTiers lists the reseller tiers with their prices and reseller counts.
func (s *Service) ListTiers(ctx context.Context) ([]store.TierRow, error) {
	return s.st.ListTiers(ctx, s.st.Conn())
}

// CreateTier adds a reseller tier.
func (s *Service) CreateTier(ctx context.Context, actor *store.User, in TierInput, reason string) (*store.ResellerTier, error) {
	reason, err := optionalReason(reason)
	if err != nil {
		return nil, err
	}
	if err := s.normalizeTier(ctx, &in); err != nil {
		return nil, err
	}
	t := &store.ResellerTier{Name: in.Name, DiscountPercent: in.DiscountPercent, CommissionPercent: in.CommissionPercent,
		Prices: in.Prices, Currencies: in.Currencies}
	if t.Prices == nil {
		t.Prices = map[string]int64{}
	}
	err = s.st.WithTx(ctx, func(tx pgx.Tx) error {
		if err := s.st.InsertTier(ctx, tx, t); err != nil {
			return tierWriteErr(err)
		}
		return s.auditChange(ctx, tx, actor, "reseller_tier.upsert", "reseller_tier", t.ID, nil, tierAudit(t), reason)
	})
	if err != nil {
		return nil, err
	}
	return t, nil
}

// UpdateTier changes a tier read at version, with its whole list of fixed
// prices (a plan left out has none).
func (s *Service) UpdateTier(ctx context.Context, actor *store.User, id string, version int, in TierInput, reason string) (*store.ResellerTier, error) {
	reason, err := optionalReason(reason)
	if err != nil {
		return nil, err
	}
	if _, err := buuid.Parse(id); err != nil {
		return nil, store.ErrNotFound
	}
	if err := s.normalizeTier(ctx, &in); err != nil {
		return nil, err
	}
	t := &store.ResellerTier{ID: id, Name: in.Name, DiscountPercent: in.DiscountPercent, CommissionPercent: in.CommissionPercent,
		Prices: in.Prices, Currencies: in.Currencies}
	if t.Prices == nil {
		t.Prices = map[string]int64{}
	}
	err = s.st.WithTx(ctx, func(tx pgx.Tx) error {
		old, err := s.st.GetTier(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if old.Version != version {
			return tierWriteErr(store.ErrStale)
		}
		if err := s.st.UpdateTier(ctx, tx, t, version); err != nil {
			return tierWriteErr(err)
		}
		return s.auditChange(ctx, tx, actor, "reseller_tier.upsert", "reseller_tier", id, tierAudit(old), tierAudit(t), reason)
	})
	if err != nil {
		return nil, err
	}
	return t, nil
}

// DeleteTier deletes a tier nobody is on.
func (s *Service) DeleteTier(ctx context.Context, actor *store.User, id, reason string) error {
	reason, err := optionalReason(reason)
	if err != nil {
		return err
	}
	if _, err := buuid.Parse(id); err != nil {
		return store.ErrNotFound
	}
	return s.st.WithTx(ctx, func(tx pgx.Tx) error {
		old, err := s.st.GetTier(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if err := s.st.DeleteTier(ctx, tx, id); errors.Is(err, store.ErrTierInUse) {
			return stateErr("resellers are on this tier; move them to another tier first")
		} else if err != nil {
			return err
		}
		return s.auditChange(ctx, tx, actor, "reseller_tier.delete", "reseller_tier", id, tierAudit(old), nil, reason)
	})
}
