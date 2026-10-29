package domain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/bits"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
)

// Cashback (docs/03-bot-ux.md, docs/14-reports.md): the owner can give a share of what a delivered order cost back to the buyer's wallet. It is off
// until the owner switches it on. When an order that earns it has been paid AND delivered, the wallet is credited floor(amount x percent / 100),
// cut to the owner's maximum per order, once: the credit is a ledger entry of kind cashback under the key cashback:<order id>, written in the
// transaction that marks the order delivered, so a retried delivery never pays twice. If the order is refunded later, the same refund takes the
// cashback back (kind cashback_reversal), as far as the wallet holds it.
//
// What earns it: a purchase, renewal or traffic package of a customer (not a reseller, who buys at tier prices and has targets instead), paid in
// Toman (the wallet's money), that cost more than nothing and at least the owner's minimum, and is not a free trial. An order paid entirely by a
// discount or reward code costs nothing and earns nothing. An order paid from money that came as cashback earns again: a customer is not asked
// where the money in their wallet came from. Renewals and traffic packages earn only while the owner lets renewals earn. The licenses and the
// digital accounts the owner's own store sells are not VPN orders and earn nothing.
const (
	SettingCashbackEnabled  = "cashback.enabled"
	SettingCashbackPercent  = "cashback.percent"
	SettingCashbackMax      = "cashback.max_per_order"
	SettingCashbackMinOrder = "cashback.min_order"
	SettingCashbackRenewals = "cashback.renewals"
)

// Limits of the cashback settings. The percent is kept in hundredths of a percent (basis points): 0.01% to 20%.
const (
	MinCashbackBP = 1
	MaxCashbackBP = 2000
	// MaxGrowthAmount is the largest amount (Toman) any growth setting takes: a cap, a minimum, a bonus, a prize.
	MaxGrowthAmount = 100_000_000_000
)

// CashbackConfig is the owner's cashback settings, read.
type CashbackConfig struct {
	Enabled bool
	// PercentBP is the share in hundredths of a percent (250 is 2.5%); 0 while unset.
	PercentBP int64
	// MaxPerOrder is the most one order earns, in Toman; 0: no maximum.
	MaxPerOrder int64
	// MinOrder is the least an order costs to earn, in Toman; 0: any order that costs something.
	MinOrder int64
	// Renewals: whether a renewal or a traffic package earns too.
	Renewals bool
}

// CashbackConfigOf reads the cashback settings from the settings map (a value that is not valid reads as unset: the settings are checked when they
// are saved, and a bad one must switch the feature off rather than pay on a guess).
func CashbackConfigOf(set map[string]string) CashbackConfig {
	c := CashbackConfig{Enabled: set[SettingCashbackEnabled] == "true", Renewals: set[SettingCashbackRenewals] != "false"}
	if bp, err := ParsePercentBP(set[SettingCashbackPercent]); err == nil {
		c.PercentBP = bp
	}
	c.MaxPerOrder = settingAmount(set[SettingCashbackMax])
	c.MinOrder = settingAmount(set[SettingCashbackMinOrder])
	return c
}

// Percent is the share as the owner wrote it ("2.5"), "" while unset.
func (c CashbackConfig) Percent() string {
	if c.PercentBP <= 0 {
		return ""
	}
	return FormatPercentBP(c.PercentBP)
}

// settingAmount reads a whole-number setting (Toman) from its stored text; 0 when unset or not a number.
func settingAmount(v string) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	if err != nil || n < 0 || n > MaxGrowthAmount {
		return 0
	}
	return n
}

// ParsePercentBP reads a percent typed by the owner ("5", "2.5", "۰٫۵", "7%") as hundredths of a percent. The decimal point may be written
// . , / or the Persian ٫ and ، (a percent has no thousands). At most two decimals; 0.01 to 20.
func ParsePercentBP(s string) (int64, error) {
	s = strings.TrimSuffix(strings.TrimSpace(latinDigits(s)), "%")
	s = strings.TrimSpace(strings.NewReplacer(",", ".", "/", ".", "٫", ".", "،", ".").Replace(s))
	whole, frac, _ := strings.Cut(s, ".")
	if (whole == "" && frac == "") || len(whole) > 3 || len(frac) > 2 {
		return 0, errors.New("not a percent")
	}
	for _, part := range []string{whole, frac} {
		for _, r := range part {
			if r < '0' || r > '9' {
				return 0, errors.New("not a percent")
			}
		}
	}
	w, _ := strconv.ParseInt("0"+whole, 10, 64)
	f, _ := strconv.ParseInt("0"+(frac + "00")[:2], 10, 64)
	bp := w*100 + f
	if bp < MinCashbackBP || bp > MaxCashbackBP {
		return 0, errors.New("out of range")
	}
	return bp, nil
}

// FormatPercentBP writes hundredths of a percent the short way: 250 is "2.5", 2000 is "20", 5 is "0.05".
func FormatPercentBP(bp int64) string {
	s := fmt.Sprintf("%d.%02d", bp/100, bp%100)
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}

// cashbackAmount is floor(base x bp / 10000), cut to most (0: no cut); capped says the cut applied. The product is worked out in 128 bits, so a
// huge base cannot wrap.
func cashbackAmount(base, bp, most int64) (amount int64, capped bool) {
	if base <= 0 || bp <= 0 {
		return 0, false
	}
	hi, lo := bits.Mul64(uint64(base), uint64(bp))
	q, _ := bits.Div64(hi, lo, 10000) // hi < 10000: bp is at most 2000 and base fits 63 bits; q is at most a fifth of base
	amount = int64(q)                 //nolint:gosec // q <= base < 2^63
	if most > 0 && amount > most {
		return most, true
	}
	return amount, false
}

// earns reports whether a delivered order earns cashback under these settings. buyer is the customer as they are now.
func (c CashbackConfig) earns(o *store.Order, plan *store.Plan, buyer *store.User) bool {
	switch {
	case !c.Enabled || c.PercentBP <= 0:
		return false
	case buyer == nil || buyer.Status != "active" || isReseller(buyer):
		return false
	case plan == nil || plan.IsTrial:
		return false
	case o.Currency != WalletCurrency || o.Amount <= 0 || o.Amount < c.MinOrder || o.RefundedAt != nil:
		return false
	case o.Type == OrderNew:
		return true
	case o.Type == OrderRenew || o.Type == OrderTrafficTopup:
		return c.Renewals
	}
	return false
}

// settingsIn reads every setting inside a transaction, as strings.
func (s *Service) settingsIn(ctx context.Context, tx pgx.Tx) (map[string]string, error) {
	raw, err := s.st.GetSettings(ctx, tx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(raw))
	for k, r := range raw {
		var v string
		if json.Unmarshal(r, &v) == nil {
			out[k] = v
		}
	}
	return out, nil
}

// afterDelivery runs in the transaction that marks an order delivered, next to the reward code: what the order earns its buyer besides the
// service. bulkID is the bulk purchase whose one message is still to come ("" for any other order): such a service is not told on its own.
func (s *Service) afterDelivery(ctx context.Context, tx pgx.Tx, o *store.Order, plan *store.Plan, bulkID string) error {
	// The customer as they are now, not as they were when the delivery was claimed (a panel call takes time, and a ban or a promotion can fall in it).
	buyer, err := s.st.GetUser(ctx, tx, o.UserID)
	if err != nil {
		return err
	}
	set, err := s.settingsIn(ctx, tx)
	if err != nil {
		return err
	}
	if err := s.issueCashback(ctx, tx, set, o, plan, buyer, bulkID); err != nil {
		return err
	}
	return s.recordSale(ctx, tx, set, o, plan, buyer)
}

// issueCashback credits the cashback an order earned, once, and queues the message that tells the buyer.
func (s *Service) issueCashback(ctx context.Context, tx pgx.Tx, set map[string]string, o *store.Order, plan *store.Plan, buyer *store.User, bulkID string) error {
	cfg := CashbackConfigOf(set)
	if !cfg.earns(o, plan, buyer) {
		return nil
	}
	amount, capped := cashbackAmount(o.Amount, cfg.PercentBP, cfg.MaxPerOrder)
	if amount <= 0 {
		return nil
	}
	entry := &store.LedgerEntry{Kind: "cashback", RefType: strptr("order"), RefID: strptr(o.ID), IdempotencyKey: "cashback:" + o.ID}
	w, err := s.st.Credit(ctx, tx, buyer.ID, o.Currency, amount, entry)
	if errors.Is(err, store.ErrDuplicateIdempotency) {
		return nil // this order earned already: nothing was written
	}
	if errors.Is(err, store.ErrIdempotencyConflict) {
		// The key is taken by an entry that is not this credit. Nothing moved; the service the customer paid for must not wait for a bonus.
		s.cfg.Log.Error("cashback: the ledger key is taken by another entry; no cashback was paid", "order", o.ID)
		return nil
	}
	if err != nil {
		return err
	}
	if err := s.st.InsertCashbackCredit(ctx, tx, &store.CashbackCredit{
		OrderID: o.ID, UserID: buyer.ID, Currency: o.Currency, BaseAmount: o.Amount, PercentBP: cfg.PercentBP, Amount: amount,
		Capped: capped, LedgerEntryID: entry.ID,
	}); err != nil {
		return err
	}
	if bulkID != "" {
		return nil // the bulk's one message carries the sum
	}
	return s.publish(ctx, tx, events.WalletCredited, events.WalletCreditedEvent{
		UserID: buyer.ID, TelegramID: buyer.TelegramID, OrderID: o.ID,
		Amount: amount, Currency: o.Currency, Balance: w.Balance, Reason: events.CreditCashback,
	})
}

// reverseCashback takes the cashback an order earned back, in the transaction of the order's refund: as far as the wallet holds it, never below
// zero. The refund has credited the wallet already, so the wallet nearly always holds it; when it does not (the buyer became a reseller in debt),
// what could not be taken is written to the audit log. It returns what was taken and the wallet as it is now (w nil: the order earned nothing).
func (s *Service) reverseCashback(ctx context.Context, tx pgx.Tx, actor *store.User, o *store.Order) (taken int64, w *store.Wallet, err error) {
	c, err := s.st.LockCashbackCredit(ctx, tx, o.ID)
	if errors.Is(err, store.ErrNotFound) {
		return 0, nil, nil
	}
	if err != nil {
		return 0, nil, err
	}
	if c.ReversedAt != nil {
		return 0, nil, nil // taken back already
	}
	entry := &store.LedgerEntry{Kind: "cashback_reversal", RefType: strptr("order"), RefID: strptr(o.ID), IdempotencyKey: "cashback_reversal:" + o.ID}
	taken, w, err = s.st.DebitAtMost(ctx, tx, c.UserID, c.Currency, c.Amount, entry)
	if err != nil {
		return 0, nil, err
	}
	var entryID *string
	if taken > 0 {
		entryID = &entry.ID
	}
	if _, err := s.st.MarkCashbackReversed(ctx, tx, o.ID, taken, entryID); err != nil {
		return 0, nil, err
	}
	detail := map[string]any{"credited": c.Amount, "taken": taken, "shortfall": c.Amount - taken, "currency": c.Currency}
	if err := s.audit(ctx, tx, actor, "order.cashback_reversal", "order", o.ID, detail, ""); err != nil {
		return 0, nil, err
	}
	return taken, w, nil
}

// CashbackStatus is what a customer may see of the cashback: whether it is on, its terms, and what they have earned so far.
type CashbackStatus struct {
	// On is true while the owner has it switched on with a percent: new orders earn.
	On          bool
	Percent     string // "2.5"; "" while off
	MaxPerOrder int64
	MinOrder    int64
	Renewals    bool
	// Earned is what the customer has earned in Toman, less what refunds took back.
	Earned int64
}

// CashbackStatus reads the owner's terms and a customer's total.
func (s *Service) CashbackStatus(ctx context.Context, userID string) (*CashbackStatus, error) {
	set, err := s.Settings(ctx)
	if err != nil {
		return nil, err
	}
	cfg := CashbackConfigOf(set)
	earned, err := s.CashbackEarned(ctx, userID)
	if err != nil {
		return nil, err
	}
	st := &CashbackStatus{Earned: earned}
	if cfg.Enabled && cfg.PercentBP > 0 {
		st.On, st.Percent, st.MaxPerOrder, st.MinOrder, st.Renewals = true, cfg.Percent(), cfg.MaxPerOrder, cfg.MinOrder, cfg.Renewals
	}
	return st, nil
}

// OrderCashback is the cashback an order earned, with what a refund took back; nil when it earned none.
func (s *Service) OrderCashback(ctx context.Context, orderID string) (*store.CashbackCredit, error) {
	c, err := s.st.GetCashbackCredit(ctx, s.st.Conn(), orderID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	return c, err
}

// CashbackEarned is what a customer has earned as cashback in Toman, less what refunds took back.
func (s *Service) CashbackEarned(ctx context.Context, userID string) (int64, error) {
	m, err := s.st.CashbackEarned(ctx, s.st.Conn(), userID)
	return m[WalletCurrency], err
}
