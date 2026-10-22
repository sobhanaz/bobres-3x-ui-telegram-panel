package domain

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
)

// Bulk purchase (the bot's "Bulk purchase"): a customer, or a reseller, buys
// N services of one plan at once and pays for all of them from the wallet.
//
// The model is the plainest that is correct: N ORDINARY orders, each priced as
// a normal purchase of the plan would price it for this buyer (a reseller's
// tier price, the store price for anyone else; no discount code, which works
// once per customer and so could cut the price of one service out of N), each
// paid by its own wallet debit (so refunds, commissions, the reports and the
// ledger see N orders), all made in ONE database transaction: either every
// order is made and paid for, or nothing is written. The provisioning worker
// then picks the orders up like any others, with its retries.
//
// What ties them together is a row (store.Bulk) holding the request's
// idempotency key (a retry or a double tap returns the first result and moves
// no money), and what the customer needs afterwards: the one message with every
// link (SettleBulks, announced as events.BulkReady).
//
// Only the wallet pays a bulk. A gateway pays one order of a fixed amount
// (the payments service is built around that); paying N orders with one
// payment would need a second order model. A customer whose wallet is short
// tops it up with any method (the existing top-up) and then buys.

// The settings of the bulk purchase.
const (
	SettingBulkEnabled = "bulk.enabled"
	SettingBulkMax     = "bulk.max_quantity"
)

// Quantities of a bulk purchase.
const (
	// MinBulkQuantity: a bulk purchase holds at least two services (one is a
	// normal purchase).
	MinBulkQuantity = 2
	// DefaultBulkMax is the most one purchase may hold until the owner sets
	// bulk.max_quantity.
	DefaultBulkMax = 20
	// MaxBulkQuantity is the most the owner can allow (the setting's maximum,
	// and what the code never goes beyond).
	MaxBulkQuantity = 100
)

// maxBulkKey bounds a request key; the orders' own keys are "<key>#<n>".
const maxBulkKey = 200

// BulkLimits are the owner's switch and limit for bulk purchases now.
func (s *Service) BulkLimits(ctx context.Context) (enabled bool, maxQuantity int) {
	enabled = s.setting(ctx, SettingBulkEnabled) != "false"
	maxQuantity = DefaultBulkMax
	if n := s.settingInt(ctx, SettingBulkMax); n >= MinBulkQuantity {
		maxQuantity = int(min(n, MaxBulkQuantity))
	}
	return enabled, maxQuantity
}

// bulkOrderKey is the idempotency key of the n-th order (from 1) of the bulk
// made with key.
func bulkOrderKey(key string, n int) string { return key + "#" + strconv.Itoa(n) }

// BulkQuoteParams describes a bulk purchase to price.
type BulkQuoteParams struct {
	UserID, PlanID string
	Quantity       int
}

// BulkQuote is a bulk purchase's price before it is made.
type BulkQuote struct {
	Quantity   int
	UnitAmount int64
	Amount     int64 // UnitAmount x Quantity
	Currency   string
	// MaxQuantity is the most the store allows in one purchase now.
	MaxQuantity int
}

// BulkParams carries the inputs of CreateBulk.
type BulkParams struct {
	UserID, PlanID string
	Quantity       int
	// IdempotencyKey makes a retry or a double tap return the first result
	// (required).
	IdempotencyKey string
	// LabelPrefix names the services "<prefix> 1" to "<prefix> N" ("": no
	// names).
	LabelPrefix string
	// ExpectedAmount, when positive, is the total the buyer was shown (in
	// ExpectedCurrency, when that is set): the purchase is refused if the price
	// came to anything else (the plan or the buyer's tier changed meanwhile).
	ExpectedAmount   int64
	ExpectedCurrency string
}

// BulkView is a bulk purchase with its plan's name and its services.
type BulkView struct {
	store.Bulk
	PlanName map[string]string
	Services []store.BulkService
	// Replayed: the request had been made before; nothing was charged again.
	Replayed bool
}

// bulkPlan is a bulk purchase checked and priced.
type bulkPlan struct {
	user   *store.User
	plan   *store.Plan
	unit   int64
	total  int64
	prefix *string
	labels []*string // one per order, or nil
}

// bulkPrefix checks the name a buyer typed for the services of a bulk
// purchase: one line of plain text, short enough that "<prefix> <n>" is a
// service name (at most MaxLabel characters) for the biggest n. Empty: no
// names (nil).
func bulkPrefix(prefix string, quantity int) (*string, error) {
	p, err := normalizeLabel(prefix)
	if err != nil || p == nil {
		return nil, err
	}
	if room := MaxLabel - 1 - len(strconv.Itoa(quantity)); utf8.RuneCountInString(*p) > room {
		return nil, invalid("a name for %d services is at most %d characters", quantity, room)
	}
	return p, nil
}

// bulkLabels names the services of a bulk purchase "<prefix> 1" to
// "<prefix> <quantity>" (nil without a prefix). The quantity has been checked
// against MaxBulkQuantity.
func bulkLabels(prefix *string, quantity int) []*string {
	if prefix == nil {
		return nil
	}
	out := make([]*string, quantity)
	for i := range out {
		l := *prefix + " " + strconv.Itoa(i+1)
		out[i] = &l
	}
	return out
}

// prepareBulk checks what a bulk purchase asks for and prices it: the switch
// and the limit, the quantity, the plan (a paid, enabled, regular plan) and the
// buyer's price for it.
func (s *Service) prepareBulk(ctx context.Context, u *store.User, planID string, quantity int, prefix string) (*bulkPlan, int, error) {
	enabled, maxQty := s.BulkLimits(ctx)
	if !enabled {
		return nil, 0, stateErr("bulk purchase is turned off")
	}
	if quantity < MinBulkQuantity || quantity > maxQty {
		return nil, 0, invalid("a bulk purchase is %d to %d services", MinBulkQuantity, maxQty)
	}
	plan, _, err := s.orderPlan(ctx, u.ID, planID, OrderNew, "", "")
	if err != nil {
		return nil, 0, err
	}
	unit, err := s.priceFor(ctx, u, plan)
	if err != nil {
		return nil, 0, err
	}
	if unit <= 0 {
		return nil, 0, fmt.Errorf("%w: a bulk purchase is for paid plans", ErrPlanUnavailable)
	}
	if unit > math.MaxInt64/int64(quantity) {
		return nil, 0, invalid("the total is too large")
	}
	p, err := bulkPrefix(prefix, quantity)
	if err != nil {
		return nil, 0, err
	}
	return &bulkPlan{user: u, plan: plan, unit: unit, total: unit * int64(quantity), prefix: p, labels: bulkLabels(p, quantity)}, maxQty, nil
}

// QuoteBulk prices a bulk purchase without making it, and fails as CreateBulk
// would for what can be known beforehand: the switch, the quantity, the plan,
// the license's places for that many services (checked again, under a lock,
// when the purchase is made). The buyer's wallet is not looked at: the bot
// shows it beside the total.
func (s *Service) QuoteBulk(ctx context.Context, p BulkQuoteParams) (*BulkQuote, error) {
	u, err := s.activeUser(ctx, nil, p.UserID)
	if err != nil {
		return nil, err
	}
	bp, maxQty, err := s.prepareBulk(ctx, u, p.PlanID, p.Quantity, "")
	if err != nil {
		return nil, err
	}
	gate, err := s.serviceGate(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.requireReseller(ctx, gate, u, ""); err != nil {
		return nil, err
	}
	if err := s.servicesFit(ctx, gate, p.Quantity); err != nil {
		s.noteRefusedPurchase(ctx, err, gate.eff.Now)
		return nil, err
	}
	return &BulkQuote{Quantity: p.Quantity, UnitAmount: bp.unit, Amount: bp.total, Currency: bp.plan.Currency, MaxQuantity: maxQty}, nil
}

// CreateBulk buys Quantity services of a plan and pays for them from the
// buyer's wallet, all or nothing. A retry with the same IdempotencyKey returns
// the first result (Replayed) and moves no money; the same key for anything
// else is store.ErrIdempotencyConflict. Refused when the wallet (a reseller's
// balance plus credit limit) cannot pay all of it (store.ErrInsufficientFunds),
// when the license has no places for that many new services at once, or when
// the price is not ExpectedAmount.
func (s *Service) CreateBulk(ctx context.Context, p BulkParams) (*BulkView, error) {
	if p.IdempotencyKey == "" || len(p.IdempotencyKey) > maxBulkKey {
		return nil, invalid("an idempotency key of 1 to %d characters is required", maxBulkKey)
	}
	u, err := s.activeUser(ctx, nil, p.UserID)
	if err != nil {
		return nil, err
	}
	prefix, err := bulkPrefix(p.LabelPrefix, p.Quantity)
	if err != nil {
		return nil, err
	}
	// A request made before: its result, whatever has changed since (the plan,
	// the price, the switch). Checked again inside the transaction, where a
	// request made at the same moment is found too.
	if b, err := s.st.BulkByKey(ctx, s.st.Conn(), p.IdempotencyKey); err == nil {
		return s.replayBulk(ctx, b, p, prefix)
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}

	bp, _, err := s.prepareBulk(ctx, u, p.PlanID, p.Quantity, p.LabelPrefix)
	if err != nil {
		return nil, err
	}
	if p.ExpectedAmount > 0 && (p.ExpectedAmount != bp.total || p.ExpectedCurrency != "" && p.ExpectedCurrency != bp.plan.Currency) {
		return nil, stateErr("the price changed since it was shown")
	}
	gate, err := s.serviceGate(ctx)
	if err != nil {
		return nil, err
	}
	first := bulkOrderKey(p.IdempotencyKey, 1)
	if err := s.requireReseller(ctx, gate, u, first); err != nil {
		return nil, err
	}
	// A wallet that cannot pay is refused before anything is written (the
	// debits in the transaction decide for real: this is only a cheaper no).
	w, err := s.st.GetWallet(ctx, s.st.Conn(), u.ID, bp.plan.Currency)
	if err != nil {
		return nil, err
	}
	if w.Balance+w.CreditLimit < bp.total {
		return nil, store.ErrInsufficientFunds
	}

	var (
		out      *store.Bulk
		replayed bool
	)
	err = s.st.WithTx(ctx, func(tx pgx.Tx) error {
		out, replayed = nil, false
		// The places are counted and taken under the lock that makes concurrent
		// purchases take turns, before any other lock, as CreateOrder does.
		if err := s.requireServiceSlots(ctx, tx, gate, first, p.Quantity); err != nil {
			return err
		}
		if isReseller(u) {
			if err := s.st.HoldReseller(ctx, tx, u.ID); errors.Is(err, store.ErrNotFound) {
				return stateErr("your reseller account changed meanwhile; try again")
			} else if err != nil {
				return err
			}
		}
		b, inserted, err := s.st.CreateBulk(ctx, tx, &store.Bulk{
			UserID: u.ID, PlanID: bp.plan.ID, Quantity: int32(p.Quantity), //nolint:gosec // 2 to MaxBulkQuantity
			UnitAmount: bp.unit, Amount: bp.total, Currency: bp.plan.Currency, LabelPrefix: bp.prefix, IdempotencyKey: p.IdempotencyKey,
		})
		if err != nil {
			return err
		}
		if !inserted { // the same request, made at the same moment, committed first
			out, replayed = b, true
			return nil
		}
		for i := 1; i <= p.Quantity; i++ {
			if err := s.makeBulkOrder(ctx, tx, u, bp, b, i); err != nil {
				return err
			}
		}
		out = b
		return s.auditChange(ctx, tx, u, "order.bulk", "bulk_order", b.ID, nil, map[string]any{
			"plan_id": bp.plan.ID, "quantity": b.Quantity, "unit_amount": b.UnitAmount, "amount": b.Amount,
			"currency": b.Currency, "label_prefix": derefStr(b.LabelPrefix),
		}, "")
	})
	if err != nil {
		// The same request made at the same moment may have committed first:
		// this one then met a wallet or a license already spent on that very
		// purchase, which is its answer, not a refusal.
		if b, gerr := s.st.BulkByKey(ctx, s.st.Conn(), p.IdempotencyKey); gerr == nil {
			return s.replayBulk(ctx, b, p, prefix)
		}
		s.noteRefusedPurchase(ctx, err, gate.eff.Now)
		return nil, err
	}
	if !replayed {
		s.kickProvisioning()
	}
	view, err := s.bulkView(ctx, out)
	if err != nil {
		return nil, err
	}
	view.Replayed = replayed
	return view, nil
}

// makeBulkOrder makes and pays the i-th order (from 1) of a bulk, in the
// transaction that makes the whole bulk: the order, its wallet debit (kept
// under "purchase:<order id>" like any wallet purchase, so one order can never
// be charged twice), and the announcement that it is paid.
func (s *Service) makeBulkOrder(ctx context.Context, tx pgx.Tx, u *store.User, bp *bulkPlan, b *store.Bulk, i int) error {
	idx := int32(i) //nolint:gosec // 1 to MaxBulkQuantity
	o := &store.Order{
		UserID: u.ID, PlanID: bp.plan.ID, Type: OrderNew, Status: "awaiting_payment",
		Amount: bp.unit, Currency: bp.plan.Currency, IdempotencyKey: bulkOrderKey(b.IdempotencyKey, i),
		BulkID: &b.ID, BulkIndex: &idx,
	}
	if bp.labels != nil {
		o.Label = bp.labels[i-1]
	}
	got, inserted, err := s.st.CreateOrder(ctx, tx, o)
	if err != nil {
		return err
	}
	if !inserted { // the key of an order is taken by something that is not this bulk's
		return store.ErrIdempotencyConflict
	}
	if _, err := s.st.Debit(ctx, tx, u.ID, got.Currency, got.Amount, &store.LedgerEntry{
		Kind: "purchase", RefType: strptr("order"), RefID: strptr(got.ID), IdempotencyKey: "purchase:" + got.ID,
	}); err != nil {
		if errors.Is(err, store.ErrDuplicateIdempotency) {
			return fmt.Errorf("order %s has a recorded purchase but is %s: %w", got.ID, got.Status, store.ErrIdempotencyConflict)
		}
		return err
	}
	if ok, err := s.st.TransitionOrder(ctx, tx, got.ID, payableStatuses, "paid"); err != nil || !ok {
		return errors.Join(err, ErrOrderNotPayable)
	}
	got.Status = "paid"
	return s.publishOrderPaidIn(ctx, tx, got, u.TelegramID, events.PaidByWallet, "", int(b.Quantity))
}

// replayBulk answers a request that was made before: the same purchase when
// its user, plan, quantity and name agree, else a conflict.
func (s *Service) replayBulk(ctx context.Context, b *store.Bulk, p BulkParams, prefix *string) (*BulkView, error) {
	if b.UserID != p.UserID || b.PlanID != p.PlanID || int(b.Quantity) != p.Quantity || derefStr(b.LabelPrefix) != derefStr(prefix) {
		return nil, store.ErrIdempotencyConflict
	}
	view, err := s.bulkView(ctx, b)
	if err != nil {
		return nil, err
	}
	view.Replayed = true
	return view, nil
}

// bulkView gathers a bulk with its plan's name and its services.
func (s *Service) bulkView(ctx context.Context, b *store.Bulk) (*BulkView, error) {
	plan, err := s.st.GetPlan(ctx, s.st.Conn(), b.PlanID)
	if err != nil {
		return nil, err
	}
	services, err := s.st.BulkServices(ctx, s.st.Conn(), b.ID)
	if err != nil {
		return nil, err
	}
	return &BulkView{Bulk: *b, PlanName: plan.NameI18n, Services: services}, nil
}

// GetBulk returns a bulk purchase with its services. userID, when set, must
// be the buyer: anyone else's bulk is not found (the links in it are the
// buyer's alone).
func (s *Service) GetBulk(ctx context.Context, bulkID, userID string) (*BulkView, error) {
	b, err := s.st.GetBulk(ctx, s.st.Conn(), bulkID)
	if err != nil {
		return nil, err
	}
	if userID != "" && b.UserID != userID {
		return nil, store.ErrNotFound
	}
	return s.bulkView(ctx, b)
}

// maxSettledAtOnce bounds how many bulks one pass of SettleBulks looks at.
const maxSettledAtOnce = 50

// SettleBulks announces the bulk purchases whose services have all come to a
// rest: each is delivered, or has failed delayedAfter attempts or more (the
// provisioning worker's alert threshold) and is retried in the background. For
// each such bulk it publishes events.BulkReady once (the bot then sends the
// customer every link in one message) and records that it did, so a
// service that is ready later is delivered on its own. A bulk with an order
// still on its way waits; a bulk with nothing left to tell (every order
// refunded) is closed quietly. It returns how many bulks it announced.
func (s *Service) SettleBulks(ctx context.Context, delayedAfter int) (int, error) {
	ids, err := s.st.UndeliveredBulks(ctx, s.st.Conn(), maxSettledAtOnce)
	if err != nil {
		return 0, err
	}
	announced := 0
	var errs []error
	for _, id := range ids {
		ok, err := s.settleBulk(ctx, id, delayedAfter)
		if err != nil {
			errs = append(errs, fmt.Errorf("bulk %s: %w", id, err))
			continue
		}
		if ok {
			announced++
		}
	}
	return announced, errors.Join(errs...)
}

func (s *Service) settleBulk(ctx context.Context, id string, delayedAfter int) (bool, error) {
	announced := false
	err := s.st.WithTx(ctx, func(tx pgx.Tx) error {
		announced = false
		b, err := s.st.LockBulk(ctx, tx, id)
		if err != nil {
			return err
		}
		if b.DeliveredAt != nil { // another pass announced it
			return nil
		}
		pr, err := s.st.BulkProgress(ctx, tx, id, delayedAfter)
		if err != nil {
			return err
		}
		if pr.Working > 0 {
			return nil
		}
		if ok, err := s.st.MarkBulkDelivered(ctx, tx, id); err != nil || !ok {
			return err
		}
		if len(pr.ReadyOrders) == 0 && pr.Delayed == 0 {
			return nil
		}
		u, err := s.st.GetUser(ctx, tx, b.UserID)
		if err != nil {
			return err
		}
		announced = true
		return s.publish(ctx, tx, events.BulkReady, events.BulkReadyEvent{
			BulkID: b.ID, UserID: b.UserID, TelegramID: u.TelegramID, PlanID: b.PlanID, Quantity: int(b.Quantity),
			ReadyOrders: pr.ReadyOrders, Delayed: pr.Delayed, Amount: b.Amount, Currency: b.Currency,
		})
	})
	return announced, err
}

// bulkIDForEvent is the bulk an event about a service is held for: its id
// while the bulk's one message is still to come, "" for any other order.
func (s *Service) bulkIDForEvent(ctx context.Context, tx pgx.Tx, o *store.Order) (string, error) {
	if o.BulkID == nil {
		return "", nil
	}
	b, err := s.st.GetBulk(ctx, tx, *o.BulkID)
	if err != nil {
		return "", err
	}
	if b.DeliveredAt != nil {
		return "", nil
	}
	return b.ID, nil
}
