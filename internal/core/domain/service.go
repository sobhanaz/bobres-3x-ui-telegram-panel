// Package domain holds the core business rules: users, orders with
// idempotency, wallet payment, one trial per user, and applying payment
// events. Every state change and the event announcing it commit together.
package domain

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	bcrypto "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/crypto"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/eventbus"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/license"
)

// trialOnceConstraint is the partial unique index enforcing one trial grant per user.
const trialOnceConstraint = "ledger_entries_trial_once_idx"

// Config tunes the domain.
type Config struct {
	// OwnerTelegramIDs are the root admins: the store owners the server's
	// settings name (BOBRES_ADMIN_TELEGRAM_ID). Each is promoted to the owner
	// role on first contact and when core starts, and none can be changed
	// from the dashboard or the bot. The first is the main owner: the only
	// one who takes no staff seat, and the one `bobres admin password`
	// serves by default. Ids that are not positive, and repeats, are dropped.
	OwnerTelegramIDs []int64
	// Timezone is BOBRES_TIMEZONE: the zone of the sales reports' days when the
	// general.timezone setting is empty ("" or a name core cannot load: the
	// report default, Asia/Tehran).
	Timezone string
	// Log receives what needs an operator's eye outside a request (nil: the
	// default logger).
	Log *slog.Logger
	// License is what a license token is checked against.
	License LicenseConfig
	// Secrets (core's master key) keys the hashes gift codes are stored as, so
	// a copy of the database cannot be read back into codes. Nil: no gift
	// code can be made or redeemed (core says so; nothing is stored in clear).
	Secrets *bcrypto.Envelope
	// LeaderboardTTL is how long the top-inviters board is kept before it is
	// read again (0: 30 seconds; below 0: never kept, every call reads it).
	LeaderboardTTL time.Duration
	// VendorTransport carries the license sales' signed calls to the license server (the bot does, in production: core
	// has no internet access; nil until SetVendorTransport). VendorNow is their clock (nil: time.Now), for tests.
	VendorTransport VendorTransport
	VendorNow       func() time.Time
	// SupplierTransport carries the supplier calls of the owner's resale (the bot, in production; nil until
	// SetSupplierTransport).
	SupplierTransport SupplierTransport
	// RateTransport carries the live USDT rate from the bot (nil until SetRateTransport).
	RateTransport RateTransport
	// AutoCrypto says whether automatic USDT or TON is switched on in payments (nil until SetAutoCrypto): the live
	// rate is then kept fresh, as it is for a store with a manual crypto address.
	AutoCrypto func(ctx context.Context) bool
	// PrivateFeaturesOverride, when set, decides PrivateFeatures instead of the license. It exists for tests of the vendor's
	// own features; nothing sets it in a running install.
	PrivateFeaturesOverride *bool
}

func (c Config) vendorNow() time.Time {
	if c.VendorNow != nil {
		return c.VendorNow()
	}
	return time.Now()
}

// SetVendorTransport sets the way to the license server (the bot, in cmd/core).
func (s *Service) SetVendorTransport(tr VendorTransport) { s.cfg.VendorTransport = tr }

// Service composes the store with business rules.
type Service struct {
	st    *store.Store
	ob    *eventbus.Outbox
	cfg   Config
	kick  chan struct{} // wakes the provisioning worker
	bkick chan struct{} // wakes the broadcast worker
	lkick chan struct{} // wakes the subscription link worker
	// embedded is the license key ring built into core, read once.
	keysOnce sync.Once
	embedded license.KeyRing
	// boards keeps the top-inviters board for a short time (Config.LeaderboardTTL).
	boards boardCache
	// supplierBal keeps the supplier balance for a minute, so the dashboard showing it everywhere does not ask the
	// supplier at every page view.
	supplierBal supplierBalanceCache
}

// New builds a Service publishing to core's outbox.
func New(st *store.Store, cfg Config) *Service {
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	cfg.OwnerTelegramIDs = cleanOwnerIDs(cfg.OwnerTelegramIDs)
	return &Service{st: st, ob: eventbus.NewOutbox("outbox_core"), cfg: cfg, kick: make(chan struct{}, 1),
		bkick: make(chan struct{}, 1), lkick: make(chan struct{}, 1)}
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
	if s.IsConfiguredOwner(u) {
		if err := s.promoteOwner(ctx, u); err != nil {
			return nil, err
		}
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
	// Label is the name a reseller gives the service they buy for a
	// customer; kept on a reseller's new order only (ignored otherwise).
	Label string
	// LinkSecret is the secret of the link of a service that is not the buyer's own (see FindServiceByLink): a renewal of it needs it.
	LinkSecret string
}

// CreateOrder validates the plan (and, for a renewal or top-up, the
// subscription), prices it for the user (a reseller pays their tier's
// price), applies a discount code, and inserts an order. Retries with the
// same IdempotencyKey return the existing order unchanged. Trial plans are
// refused here: they go through StartTrial, which enforces one trial per
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
	plan, _, err := s.orderPlan(ctx, p.UserID, p.PlanID, p.Type, p.SubscriptionID, p.LinkSecret)
	if err != nil {
		return nil, err
	}
	// A new service (a customer's or a reseller's) must fit what the
	// effective entitlements allow (a reseller's needs the resellers feature,
	// every one a place among the active services, checked below in the
	// order's own transaction); a renewal or a top-up is never refused for it.
	var gate *slotGate
	if p.Type == OrderNew {
		if gate, err = s.serviceGate(ctx); err != nil {
			return nil, err
		}
		if err := s.requireReseller(ctx, gate, u, p.IdempotencyKey); err != nil {
			return nil, err
		}
	}
	price, err := s.priceFor(ctx, u, plan)
	if err != nil {
		return nil, err
	}
	off, code, err := s.discountFor(ctx, u, plan.ID, price, plan.Currency, p.DiscountCode)
	if err != nil {
		return nil, err
	}
	var label *string
	if isReseller(u) && p.Type == OrderNew {
		if label, err = normalizeLabel(p.Label); err != nil {
			return nil, err
		}
	}
	amount := price - off
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
		// The place among the active services is counted and taken in this
		// transaction, under the lock that makes concurrent purchases take
		// turns (before any other lock, so the order of locks is the same
		// everywhere).
		if gate != nil {
			if err := s.requireServiceSlot(ctx, tx, gate, p.IdempotencyKey); err != nil {
				return err
			}
		}
		// A reseller's price holds only while they are one: holding their row
		// makes a removal wait for this order (and then cancel it), and a
		// removal that came first leaves no row.
		if isReseller(u) {
			if err := s.st.HoldReseller(ctx, tx, u.ID); errors.Is(err, store.ErrNotFound) {
				return stateErr("your reseller account changed meanwhile; try again")
			} else if err != nil {
				return err
			}
		}
		o, inserted, err := s.st.CreateOrder(ctx, tx, &store.Order{
			UserID: p.UserID, PlanID: p.PlanID, Type: p.Type, Status: status,
			Amount: amount, Currency: plan.Currency, IdempotencyKey: p.IdempotencyKey,
			SubscriptionID: subID, DiscountCode: discountCode, DiscountAmount: off, Label: label,
		})
		if err != nil {
			return err
		}
		out = o
		if free && inserted {
			return s.publishOrderPaid(ctx, tx, o, u.TelegramID, events.PaidFree, "")
		}
		return nil
	})
	if err != nil {
		if gate != nil {
			s.noteRefusedPurchase(ctx, err, gate.eff.Now)
		}
		return nil, err
	}
	if free {
		s.kickProvisioning()
	}
	return out, nil
}

// publishOrderPaid announces a paid order, in the transaction that marks it
// paid, after the effects of paying (discount use, referral reward). provider
// is who took the money for a payment, "" for wallet, trial and free orders.
func (s *Service) publishOrderPaid(ctx context.Context, tx pgx.Tx, o *store.Order, telegramID int64, source, provider string) error {
	return s.publishOrderPaidIn(ctx, tx, o, telegramID, source, provider, 0)
}

// publishOrderPaidIn is publishOrderPaid for an order that may be one of a
// bulk purchase of bulkQuantity orders (0: it is not).
func (s *Service) publishOrderPaidIn(ctx context.Context, tx pgx.Tx, o *store.Order, telegramID int64, source, provider string, bulkQuantity int) error {
	if err := s.orderPaidEffects(ctx, tx, o, source); err != nil {
		return err
	}
	e := events.OrderPaidEvent{
		OrderID: o.ID, UserID: o.UserID, TelegramID: telegramID, PlanID: o.PlanID,
		Amount: o.Amount, Currency: o.Currency, Source: source, Provider: provider,
	}
	if o.BulkID != nil && o.BulkIndex != nil {
		e.BulkID, e.BulkIndex, e.BulkQuantity = *o.BulkID, int(*o.BulkIndex), bulkQuantity
	}
	return s.publish(ctx, tx, events.OrderPaid, e)
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
		// Before the balance check, so a customer short of money is not sent
		// to top up for a price the code no longer gives. Without the code's
		// lock: that is taken after the wallet's, when the order is marked
		// paid (orderPaidEffects), and checked again there.
		if err := s.checkDiscount(ctx, tx, o, false); err != nil {
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
		return s.publishOrderPaid(ctx, tx, o, u.TelegramID, events.PaidByWallet, "")
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
	// A free trial is a new service too.
	gate, err := s.serviceGate(ctx)
	if err != nil {
		return nil, err
	}
	var out *store.Order
	err = s.st.WithTx(ctx, func(tx pgx.Tx) error {
		if err := s.requireServiceSlot(ctx, tx, gate, idempotencyKey); err != nil {
			return err
		}
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
		return s.publishOrderPaid(ctx, tx, o, u.TelegramID, events.PaidTrial, "")
	})
	if err != nil {
		s.noteRefusedPurchase(ctx, err, gate.eff.Now)
		return nil, err
	}
	s.kickProvisioning()
	return out, nil
}
