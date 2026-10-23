package domain

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/license"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/money"
)

// Selling BOBRES licenses from the owner's own store.
//
// A buyer pays from their wallet (any top-up method fills it, automatic USDT included) and core asks the license
// server (the signed admin API, vendor_client.go) for a key. The model is the plainest that is correct, as the bulk
// purchase's is:
//
//  1. ONE transaction takes the money and writes the order (status "paid"): either both happen or neither, and the
//     request's idempotency key makes a double tap or a retry one purchase.
//  2. Then the license server is asked, outside any transaction, with a `ref` made from the order's id, so asking
//     again never makes a second license or extends twice. The answer is written, with the order's "done", in one
//     transaction. A license server that is down leaves the order "paid" and a worker asks again with a growing wait.
//  3. If the server refuses the request for good (any 4xx but 408 and 429: a wrong key, a revoked license, a request
//     it will never accept), the money goes back in one transaction and the order is "refunded". The buyer never
//     holds a paid order without a license for long, and never loses money to a refusal.
//
// The activation key is stored encrypted with core's master key, so the buyer can be shown it again; the license
// server keeps only its hash.

// The settings of the licence sales. They are on the owner's install only: elsewhere there is no admin key.
const (
	SettingVendorEnabled = "vendor.enabled"
)

// Months a license can be bought for, and what each costs: a month is the tier's monthly price, a year its yearly
// price (ten months).
const (
	VendorMonth = 1
	VendorYear  = 12
)

// vendorAttemptsCap is where the wait between two asks stops growing; vendorMaxWait is that wait.
const (
	vendorAttemptsCap = 7
	vendorMaxWait     = time.Hour
	vendorFirstWait   = 30 * time.Second
	vendorSettleBatch = 20
)

// VendorTier is a tier that is for sale and what it costs, in minor units of Currency.
type VendorTier struct {
	Name     string
	Currency string
	Monthly  int64
	Yearly   int64
}

// Price is what Months months of the tier cost (0: the term is not for sale).
func (t VendorTier) Price(months int) int64 {
	switch months {
	case VendorMonth:
		return t.Monthly
	case VendorYear:
		return t.Yearly
	}
	return 0
}

// VendorTiers are the tiers with a price set in internal/license/tiers.json, standard first.
func (s *Service) VendorTiers() ([]VendorTier, error) {
	presets, err := license.LoadPresets()
	if err != nil {
		return nil, err
	}
	var out []VendorTier
	for _, name := range []string{license.TierStandard, license.TierPro} {
		p, ok := presets.Get(name)
		if !ok || p.Price == nil || !p.Price.IsSet() {
			continue
		}
		cur := strings.ToUpper(strings.TrimSpace(p.Price.Currency))
		monthly, err := parseMinor(p.Price.Monthly, cur)
		if err != nil {
			return nil, fmt.Errorf("tier %s monthly price: %w", name, err)
		}
		yearly, err := parseMinor(p.Price.Yearly, cur)
		if err != nil {
			return nil, fmt.Errorf("tier %s yearly price: %w", name, err)
		}
		out = append(out, VendorTier{Name: name, Currency: cur, Monthly: monthly, Yearly: yearly})
	}
	return out, nil
}

// parseMinor reads a decimal price ("4", "4.50") into minor units of currency.
func parseMinor(s, currency string) (int64, error) {
	scale := money.Scale(currency)
	whole, frac, _ := strings.Cut(strings.TrimSpace(s), ".")
	if whole == "" || len(frac) > scale {
		return 0, fmt.Errorf("%q is not a price in %s", s, currency)
	}
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil || w < 0 {
		return 0, fmt.Errorf("%q is not a price in %s", s, currency)
	}
	for len(frac) < scale {
		frac += "0"
	}
	var f int64
	if scale > 0 {
		if f, err = strconv.ParseInt(frac, 10, 64); err != nil || f < 0 {
			return 0, fmt.Errorf("%q is not a price in %s", s, currency)
		}
	}
	unit := int64(math.Pow10(scale))
	if w > (math.MaxInt64-f)/unit {
		return 0, fmt.Errorf("%q is too large", s)
	}
	return w*unit + f, nil
}

// PrivateFeatures reports whether this install's license grants the vendor's own features (selling licenses, managing the
// license server, reselling a supplier's accounts). Everything of those features asks it: without it they are hidden in the
// dashboard and the bot and refused by core, whatever the settings say.
func (s *Service) PrivateFeatures(ctx context.Context) bool {
	if s.cfg.PrivateFeaturesOverride != nil {
		return *s.cfg.PrivateFeaturesOverride
	}
	eff, err := s.Effective(ctx)
	return err == nil && eff.Entitlements.PrivateFeatures
}

func (s *Service) requirePrivate(ctx context.Context) error {
	if !s.PrivateFeatures(ctx) {
		return stateErr("this feature is not part of this store's license")
	}
	return nil
}

// VendorAvailable reports whether core can sell licenses at all: it has the master key that protects the admin key
// and the activation keys.
func (s *Service) VendorAvailable() bool { return s.cfg.Secrets != nil }

// VendorOn reports whether license sales are on: the owner switched them on and set the admin key.
func (s *Service) VendorOn(ctx context.Context) bool {
	if !s.VendorAvailable() || !s.PrivateFeatures(ctx) || s.setting(ctx, SettingVendorEnabled) != "true" {
		return false
	}
	_, err := s.st.GetVendorConfig(ctx, s.st.Conn())
	return err == nil
}

// SetVendorConfig stores the admin private key (base64, from `licensesrv adminkey`), encrypted. It is write-only:
// nothing returns the key. The audit says who changed it, never the key. The requests it signs go through the bot to
// the license server the bot is configured for.
func (s *Service) SetVendorConfig(ctx context.Context, actor *store.User, privateKey string) error {
	if err := s.requirePrivate(ctx); err != nil {
		return err
	}
	if s.cfg.Secrets == nil {
		return stateErr("core has no master key, so it cannot keep the admin key")
	}
	if _, err := license.ParseAdminPrivateKey(privateKey); err != nil {
		return &FieldError{"admin_key", "not an admin private key (32 bytes of base64, from licensesrv adminkey)"}
	}
	enc, err := s.cfg.Secrets.Encrypt([]byte(strings.TrimSpace(privateKey)))
	if err != nil {
		return err
	}
	return s.st.WithTx(ctx, func(tx pgx.Tx) error {
		if err := s.st.SetVendorConfig(ctx, tx, enc); err != nil {
			return err
		}
		return s.audit(ctx, tx, actor, "vendor.config", "vendor", "config", map[string]any{"admin_key": "changed"}, "")
	})
}

// vendorClient builds the client for the stored key.
func (s *Service) vendorClient(ctx context.Context) (VendorClient, error) {
	if s.cfg.Secrets == nil {
		return nil, stateErr("core has no master key")
	}
	c, err := s.st.GetVendorConfig(ctx, s.st.Conn())
	if errors.Is(err, store.ErrNotFound) {
		return nil, stateErr("the license server is not set up")
	}
	if err != nil {
		return nil, err
	}
	raw, err := s.cfg.Secrets.Decrypt(c.AdminKeyEnc)
	if err != nil {
		return nil, fmt.Errorf("the stored admin key cannot be read: %w", err)
	}
	key, err := license.ParseAdminPrivateKey(string(raw))
	if err != nil {
		return nil, err
	}
	if s.cfg.VendorTransport == nil {
		return nil, stateErr("core has no way to reach the license server (the bot)")
	}
	return newVendorClient(s.cfg.VendorTransport, key, s.cfg.vendorNow), nil
}

// VendorBuyParams carries the inputs of BuyVendorLicense.
type VendorBuyParams struct {
	UserID string
	// Tier and Months say what is bought; for a renewal LicenseID names the license (its tier is kept).
	Tier      string
	Months    int
	LicenseID string // a store.VendorLicense id: a renewal; empty: a new license
	// IdempotencyKey makes a retry or a double tap return the first result (required).
	IdempotencyKey string
	// ExpectedAmount, when positive, is the price the buyer was shown: the purchase is refused if it came to another.
	ExpectedAmount int64
	// ClaimIntent, when set, is the creation time of the customer's intent this purchase carries out: the intent is forgotten in the same
	// transaction as the debit, and the purchase is not made (errVendorIntentGone) when it was replaced or used meanwhile.
	ClaimIntent *time.Time
}

// errVendorIntentGone: the intent a purchase was to carry out is no longer there (bought by hand, replaced or used by another worker).
var errVendorIntentGone = errors.New("domain: the license intent is gone")

// VendorPurchase is the state of a purchase after BuyVendorLicense.
type VendorPurchase struct {
	Order   store.VendorOrder
	License *store.VendorLicense
	// Key is the activation key of a license bought (or re-keyed) here: set when the order is done.
	Key string
	// Replayed: the request had been made before; no money moved this time.
	Replayed bool
}

// Pending reports that the buyer has paid and the license server has not answered yet.
func (p *VendorPurchase) Pending() bool { return p.Order.Status == "paid" }

func vendorCustomer(u *store.User) string {
	name := "tg:" + strconv.FormatInt(u.TelegramID, 10)
	if u.Username != "" {
		name += " @" + u.Username
	}
	return name
}

// BuyVendorLicense sells a license (or a renewal) for the buyer's wallet.
func (s *Service) BuyVendorLicense(ctx context.Context, p VendorBuyParams) (*VendorPurchase, error) {
	if !s.VendorOn(ctx) {
		return nil, stateErr("licenses are not for sale")
	}
	if s.cfg.VendorTransport == nil {
		return nil, stateErr("core has no way to reach the license server, so it sells nothing") // before any money moves
	}
	if p.IdempotencyKey == "" || len(p.IdempotencyKey) > 180 {
		return nil, invalid("an idempotency key is required (at most 180 characters)")
	}
	if p.Months != VendorMonth && p.Months != VendorYear {
		return nil, invalid("months is %d or %d", VendorMonth, VendorYear)
	}
	u, err := s.st.GetUser(ctx, s.st.Conn(), p.UserID)
	if err != nil {
		return nil, err
	}
	if u.Status == "banned" {
		return nil, ErrUserBanned
	}
	var lic *store.VendorLicense
	kind := "new"
	if p.LicenseID != "" {
		kind = "renew"
		if lic, err = s.st.GetVendorLicense(ctx, s.st.Conn(), p.LicenseID, false); err != nil {
			return nil, err
		}
		if lic.UserID != u.ID {
			return nil, ErrForbidden
		}
		if lic.Status != "active" {
			return nil, stateErr("this license is not active, so it cannot be renewed")
		}
		p.Tier = lic.Tier
	}
	tiers, err := s.VendorTiers()
	if err != nil {
		return nil, err
	}
	var tier *VendorTier
	for i := range tiers {
		if tiers[i].Name == p.Tier {
			tier = &tiers[i]
		}
	}
	if tier == nil {
		return nil, invalid("tier is not for sale")
	}
	list := tier.Price(p.Months)
	if list <= 0 {
		return nil, invalid("that term is not for sale")
	}
	// The license is priced in USDT and charged from the Toman wallet at the store's rate.
	amount, rate, err := s.VendorToman(ctx, tier, list)
	if err != nil {
		return nil, err
	}
	if p.ExpectedAmount > 0 && p.ExpectedAmount != amount {
		return nil, stateErr("the price changed: look at it again")
	}

	order := &store.VendorOrder{UserID: u.ID, Kind: kind, Tier: tier.Name, Months: p.Months, Amount: amount,
		Currency: tomanCurrency, ListAmount: list, ListCurrency: tier.Currency, Rate: rate, IdempotencyKey: "vendor:" + p.IdempotencyKey}
	if lic != nil {
		order.VendorLicenseID = &lic.ID
	}
	var got *store.VendorOrder
	var inserted bool
	err = s.st.WithTx(ctx, func(tx pgx.Tx) error {
		var err error
		if got, inserted, err = s.st.CreateVendorOrder(ctx, tx, order); err != nil || !inserted {
			return err
		}
		switch {
		case p.ClaimIntent != nil:
			ok, err := s.st.ClaimVendorIntent(ctx, tx, u.ID, *p.ClaimIntent)
			if err != nil {
				return err
			}
			if !ok {
				return errVendorIntentGone
			}
		case !strings.HasPrefix(p.IdempotencyKey, vendorIntentKeyBase):
			// The customer bought by hand: what they meant to buy after a top-up is no longer a thing to do by itself.
			if err := s.st.DeleteVendorIntent(ctx, tx, u.ID); err != nil {
				return err
			}
		}
		_, err = s.st.Debit(ctx, tx, u.ID, got.Currency, got.Amount, &store.LedgerEntry{
			Kind: "license_purchase", RefType: strptr("vendor_order"), RefID: strptr(got.ID), IdempotencyKey: "license_purchase:" + got.ID,
		})
		return err
	})
	if err != nil {
		return nil, err
	}
	if inserted {
		if got, err = s.fulfillVendorOrder(ctx, got.ID); err != nil {
			// The money is taken and the order exists; the worker will ask again. The buyer is told it is pending.
			s.cfg.Log.Warn("license order not fulfilled yet", "order", got.ID, "err", err)
		}
	}
	return s.vendorPurchase(ctx, got, !inserted)
}

// vendorToman is what a license's list price costs in Toman now, and the rate it was worked out at. A tier priced in Toman (not today's
// presets) costs its own price. Without a USDT rate nothing is sold: refused before any money moves.
func (s *Service) VendorToman(ctx context.Context, tier *VendorTier, list int64) (toman, rate int64, err error) {
	if tier.Currency == tomanCurrency {
		return list, 0, nil
	}
	if tier.Currency != "USDT" {
		return 0, 0, invalid("tier is priced in an unknown currency")
	}
	toman, rate, err = s.TomanForUsdt(ctx, list)
	if errors.Is(err, ErrNoUsdtRate) {
		return 0, 0, stateErr("the USDT rate is not known, so licenses cannot be priced now")
	}
	return toman, rate, err
}

// vendorPurchase describes order's state, with its license and key once it is done.
func (s *Service) vendorPurchase(ctx context.Context, o *store.VendorOrder, replayed bool) (*VendorPurchase, error) {
	out := &VendorPurchase{Order: *o, Replayed: replayed}
	if o.Status != "done" || o.VendorLicenseID == nil {
		return out, nil
	}
	l, err := s.st.GetVendorLicense(ctx, s.st.Conn(), *o.VendorLicenseID, false)
	if err != nil {
		return nil, err
	}
	out.License = l
	if o.Kind == "new" {
		key, err := s.cfg.Secrets.Decrypt(l.KeyEnc)
		if err != nil {
			return nil, err
		}
		out.Key = string(key)
	}
	return out, nil
}

// ShowVendorKey is the activation key of a license the user bought, for "My licenses".
func (s *Service) ShowVendorKey(ctx context.Context, userID, licenseID string) (*store.VendorLicense, string, error) {
	if s.cfg.Secrets == nil {
		return nil, "", stateErr("core has no master key")
	}
	l, err := s.st.GetVendorLicense(ctx, s.st.Conn(), licenseID, false)
	if err != nil {
		return nil, "", err
	}
	if l.UserID != userID {
		return nil, "", ErrForbidden
	}
	key, err := s.cfg.Secrets.Decrypt(l.KeyEnc)
	if err != nil {
		return nil, "", err
	}
	return l, string(key), nil
}

// MyVendorLicenses lists a customer's licenses.
func (s *Service) MyVendorLicenses(ctx context.Context, userID string) ([]store.VendorLicense, error) {
	return s.st.UserVendorLicenses(ctx, s.st.Conn(), userID)
}

// fulfillVendorOrder asks the license server for what a paid order bought and records the answer. It is safe to
// call again and again (the refs make the server's side once-only, and the final write checks the order is still
// "paid" under its lock). It returns the order as it is afterwards; an error means the order is still "paid".
func (s *Service) fulfillVendorOrder(ctx context.Context, orderID string) (*store.VendorOrder, error) {
	o, err := s.st.GetVendorOrder(ctx, s.st.Conn(), orderID, false)
	if err != nil || o.Status != "paid" {
		return o, err
	}
	info, key, err := s.askVendor(ctx, o)
	if err != nil {
		var ve *VendorError
		if errors.As(err, &ve) && ve.Final() {
			s.cfg.Log.Warn("license server refused an order; refunding", "order", o.ID, "status", ve.Status, "code", ve.Code)
			return s.refundVendorOrder(ctx, o, ve.Error())
		}
		next := s.cfg.vendorNow().Add(vendorWait(o.Attempts))
		if derr := s.st.DeferVendorOrder(ctx, s.st.Conn(), o.ID, next, trimErr(err)); derr != nil {
			s.cfg.Log.Error("license order: recording the failed attempt", "order", o.ID, "err", derr)
		}
		return o, err
	}
	return s.finishVendorOrder(ctx, o.ID, info, key)
}

func trimErr(err error) string {
	m := err.Error()
	if len(m) > 480 {
		m = m[:480]
	}
	return m
}

func vendorWait(attempts int) time.Duration {
	w := vendorFirstWait << min(attempts, vendorAttemptsCap)
	return min(w, vendorMaxWait)
}

// askVendor makes the calls an order needs. For a new license it returns the key too (re-keying when the server
// made the license in an earlier attempt whose answer was lost).
func (s *Service) askVendor(ctx context.Context, o *store.VendorOrder) (*VendorLicenseInfo, string, error) {
	cl, err := s.vendorClient(ctx)
	if err != nil {
		return nil, "", err
	}
	switch o.Kind {
	case "new":
		u, err := s.st.GetUser(ctx, s.st.Conn(), o.UserID)
		if err != nil {
			return nil, "", err
		}
		info, err := cl.Create(ctx, "buy-"+o.ID, vendorCustomer(u), o.Tier, o.Months)
		if err != nil {
			return nil, "", err
		}
		if info.Key == "" {
			// The server made it in an attempt whose answer never reached us: a new key for a license nobody has
			// activated is the way back.
			if info, err = cl.Rekey(ctx, "rekey-"+o.ID, info.ID); err != nil {
				return nil, "", err
			}
			if info.Key == "" {
				return nil, "", errors.New("the key of this license was made and lost; an operator must re-key it by hand")
			}
		}
		return info, info.Key, nil
	case "renew":
		l, err := s.st.GetVendorLicense(ctx, s.st.Conn(), derefStr(o.VendorLicenseID), false)
		if err != nil {
			return nil, "", err
		}
		info, err := cl.Extend(ctx, "renew-"+o.ID, l.LicenseID, o.Months)
		return info, "", err
	}
	return nil, "", fmt.Errorf("order %s has kind %q", o.ID, o.Kind)
}

// finishVendorOrder writes the server's answer and marks the order done, atomically.
func (s *Service) finishVendorOrder(ctx context.Context, orderID string, info *VendorLicenseInfo, key string) (*store.VendorOrder, error) {
	var out *store.VendorOrder
	err := s.st.WithTx(ctx, func(tx pgx.Tx) error {
		o, err := s.st.GetVendorOrder(ctx, tx, orderID, true)
		if err != nil {
			return err
		}
		out = o
		if o.Status != "paid" {
			return nil // another run finished it first
		}
		switch o.Kind {
		case "new":
			enc, err := s.cfg.Secrets.Encrypt([]byte(key))
			if err != nil {
				return err
			}
			l := &store.VendorLicense{UserID: o.UserID, LicenseID: info.ID, Tier: o.Tier, EndsAt: info.EndsAt, Status: "active", KeyEnc: enc}
			if err := s.st.CreateVendorLicense(ctx, tx, l); err != nil {
				return err
			}
			o.VendorLicenseID = &l.ID
		case "renew":
			if err := s.st.SetVendorLicenseEnd(ctx, tx, derefStr(o.VendorLicenseID), info.EndsAt); err != nil {
				return err
			}
		}
		if err := s.st.FinishVendorOrder(ctx, tx, o.ID, "done", o.VendorLicenseID, ""); err != nil {
			return err
		}
		o.Status = "done"
		return s.publishVendorSettled(ctx, tx, o, info.EndsAt)
	})
	return out, err
}

// refundVendorOrder gives the money back and closes the order as refunded.
func (s *Service) refundVendorOrder(ctx context.Context, o *store.VendorOrder, why string) (*store.VendorOrder, error) {
	var out *store.VendorOrder
	err := s.st.WithTx(ctx, func(tx pgx.Tx) error {
		cur, err := s.st.GetVendorOrder(ctx, tx, o.ID, true)
		if err != nil {
			return err
		}
		out = cur
		if cur.Status != "paid" {
			return nil
		}
		if len(why) > 480 {
			why = why[:480]
		}
		if _, err := s.st.Credit(ctx, tx, cur.UserID, cur.Currency, cur.Amount, &store.LedgerEntry{
			Kind: "license_refund", RefType: strptr("vendor_order"), RefID: strptr(cur.ID), IdempotencyKey: "license_refund:" + cur.ID,
		}); err != nil && !errors.Is(err, store.ErrDuplicateIdempotency) {
			return err
		}
		if err := s.st.FinishVendorOrder(ctx, tx, cur.ID, "refunded", nil, why); err != nil {
			return err
		}
		cur.Status, cur.LastError = "refunded", why
		return s.publishVendorSettled(ctx, tx, cur, nil)
	})
	return out, err
}

// SettleVendorOrders asks the license server again for paid orders that wait (the worker's step). It returns how
// many it handled.
func (s *Service) SettleVendorOrders(ctx context.Context) (int, error) {
	if s.cfg.Secrets == nil {
		return 0, nil
	}
	due, err := s.st.DueVendorOrders(ctx, s.st.Conn(), s.cfg.vendorNow(), vendorSettleBatch)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, id := range due {
		if ctx.Err() != nil {
			return n, ctx.Err()
		}
		n++
		if _, err := s.fulfillVendorOrder(ctx, id); err != nil {
			s.cfg.Log.Warn("license order still waiting", "order", id, "err", err)
		}
	}
	return n, nil
}

// RunVendorWorker settles waiting license orders every half minute until ctx ends.
func (s *Service) RunVendorWorker(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := s.SettleVendorOrders(ctx); err != nil && ctx.Err() == nil {
				s.cfg.Log.Warn("license orders", "err", err)
			}
		}
	}
}

// ---- Buying after a top-up, in one flow

const (
	// vendorIntentKeyBase starts the idempotency key of a purchase made from an intent (the order's key is "vendor:" + this + ...).
	vendorIntentKeyBase = "intent:"
	// vendorIntentTTL is how long an intent waits for the payment to land (a manual payment can wait a night for its review).
	vendorIntentTTL = 48 * time.Hour
	// vendorIntentEvery is how often the worker looks at the intents.
	vendorIntentEvery = 30 * time.Second
	vendorIntentBatch = 100
)

// SetVendorIntent records what a customer meant to buy when they tapped a payment method for the shortfall of a license: a new license of a
// tier for months, or (licenseID) the renewal of one of theirs. When the payment lands and the wallet covers the price the worker makes the
// purchase by itself and the bot tells them. A new intent replaces the last one; buying by hand forgets it.
func (s *Service) SetVendorIntent(ctx context.Context, userID, tier string, months int, licenseID string) error {
	if !s.VendorOn(ctx) {
		return stateErr("licenses are not for sale")
	}
	if months != VendorMonth && months != VendorYear {
		return invalid("months is %d or %d", VendorMonth, VendorYear)
	}
	u, err := s.st.GetUser(ctx, s.st.Conn(), userID)
	if err != nil {
		return err
	}
	if u.Status == "banned" {
		return ErrUserBanned
	}
	in := &store.VendorIntent{UserID: u.ID, Tier: tier, Months: months, ExpiresAt: s.cfg.vendorNow().Add(vendorIntentTTL)}
	if licenseID != "" {
		lic, err := s.st.GetVendorLicense(ctx, s.st.Conn(), licenseID, false)
		if err != nil {
			return err
		}
		if lic.UserID != u.ID {
			return ErrForbidden
		}
		if lic.Status != "active" {
			return stateErr("this license is not active, so it cannot be renewed")
		}
		in.Tier, in.LicenseID = lic.Tier, &lic.ID
	}
	tiers, err := s.VendorTiers()
	if err != nil {
		return err
	}
	var price int64
	for _, t := range tiers {
		if t.Name == in.Tier {
			price = t.Price(months)
		}
	}
	if price <= 0 {
		return invalid("that tier and term are not for sale")
	}
	return s.st.SetVendorIntent(ctx, s.st.Conn(), in)
}

// SettleVendorIntents makes the purchases whose payment has landed: for each live intent that has a top-up credited after it was made and
// whose customer's wallet covers the price. It returns how many it made. An intent that can never be bought (the customer is banned, the
// license ended or is not theirs) is forgotten; one that failed for a reason that may pass (the database, a balance that moved) is kept
// until it expires. Each purchase forgets its intent in the transaction that takes the money.
func (s *Service) SettleVendorIntents(ctx context.Context) (int, error) {
	if !s.VendorOn(ctx) || s.cfg.Secrets == nil {
		return 0, nil
	}
	intents, err := s.st.LiveVendorIntents(ctx, s.st.Conn(), s.cfg.vendorNow(), vendorIntentBatch)
	if err != nil || len(intents) == 0 {
		return 0, err
	}
	tiers, err := s.VendorTiers()
	if err != nil {
		return 0, err
	}
	made := 0
	for _, in := range intents {
		var tier *VendorTier
		for i := range tiers {
			if tiers[i].Name == in.Tier {
				tier = &tiers[i]
			}
		}
		if tier == nil || tier.Price(in.Months) <= 0 {
			_ = s.st.DeleteVendorIntent(ctx, s.st.Conn(), in.UserID)
			continue
		}
		if s.vendorIntentIsDead(ctx, &in) {
			_ = s.st.DeleteVendorIntent(ctx, s.st.Conn(), in.UserID)
			continue
		}
		price, _, perr := s.VendorToman(ctx, tier, tier.Price(in.Months))
		if perr != nil {
			continue // no rate now: it waits
		}
		w, err := s.st.GetWallet(ctx, s.st.Conn(), in.UserID, tomanCurrency)
		if err != nil || w.Balance < price {
			continue // the payment has not landed yet
		}
		created := in.CreatedAt
		p := VendorBuyParams{UserID: in.UserID, Tier: in.Tier, Months: in.Months, ClaimIntent: &created,
			IdempotencyKey: fmt.Sprintf("%s%s:%d", vendorIntentKeyBase, in.UserID, in.CreatedAt.UnixMicro())}
		if in.LicenseID != nil {
			p.LicenseID = *in.LicenseID
		}
		switch _, err = s.BuyVendorLicense(ctx, p); {
		case err == nil:
			made++
		case errors.Is(err, errVendorIntentGone):
			// bought by hand, replaced or taken by another worker meanwhile: nothing to do
		case errors.Is(err, ErrUserBanned), errors.Is(err, ErrForbidden), errors.Is(err, ErrInvalid):
			s.cfg.Log.Warn("license intent can never be bought: forgotten", "user", in.UserID, "err", err)
			_ = s.st.DeleteVendorIntent(ctx, s.st.Conn(), in.UserID)
		default:
			s.cfg.Log.Warn("license intent not bought yet", "user", in.UserID, "err", err)
		}
	}
	return made, nil
}

// vendorIntentIsDead says an intent can never be bought: its customer is banned or gone, or the license it renews is not theirs or not active.
func (s *Service) vendorIntentIsDead(ctx context.Context, in *store.VendorIntent) bool {
	u, err := s.st.GetUser(ctx, s.st.Conn(), in.UserID)
	if err != nil {
		return errors.Is(err, store.ErrNotFound)
	}
	if u.Status == "banned" {
		return true
	}
	if in.LicenseID == nil {
		return false
	}
	lic, err := s.st.GetVendorLicense(ctx, s.st.Conn(), *in.LicenseID, false)
	if err != nil {
		return errors.Is(err, store.ErrNotFound)
	}
	return lic.UserID != u.ID || lic.Status != "active"
}

// RunVendorIntentWorker keeps looking at the intents while the store sells licenses.
func (s *Service) RunVendorIntentWorker(ctx context.Context) {
	t := time.NewTicker(vendorIntentEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := s.SettleVendorIntents(ctx); err != nil && ctx.Err() == nil {
				s.cfg.Log.Warn("license intents", "err", err)
			}
		}
	}
}

// publishVendorSettled queues the message that tells a customer their by-itself purchase is done or was refunded. Orders bought by hand
// answer in the chat that asked, so they publish nothing.
func (s *Service) publishVendorSettled(ctx context.Context, tx pgx.Tx, o *store.VendorOrder, endsAt *time.Time) error {
	if !strings.HasPrefix(o.IdempotencyKey, "vendor:"+vendorIntentKeyBase) {
		return nil
	}
	u, err := s.st.GetUser(ctx, tx, o.UserID)
	if err != nil {
		return err
	}
	ev := events.VendorOrderSettledEvent{OrderID: o.ID, UserID: o.UserID, TelegramID: u.TelegramID, Kind: o.Kind, Tier: o.Tier, Months: o.Months,
		Status: o.Status, LicenseID: derefStr(o.VendorLicenseID)}
	if endsAt != nil {
		ev.EndsAt = endsAt.Unix()
	}
	return s.publish(ctx, tx, events.VendorOrderSettled, ev)
}
