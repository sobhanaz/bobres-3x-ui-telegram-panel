package domain

import (
	"context"
	"encoding/json"
	"math/big"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// Pricing the owner's digital accounts from the live USDT price (docs/supplier.md, "Pricing").
//
// A product's Toman price either follows the supplier's cost ("auto": cost x the store's USDT rate x (100 + the owner's markup) / 100,
// rounded UP to the next SupplierPriceStep) or is a price the owner typed ("fixed"). The rate is the one every other conversion of the
// store uses (UsdtRate: the live price, plus the payments margin, else the owner's own rate), so a product costs the same wherever the
// customer meets it. The owner's switch, the markup and the rate the prices were last worked out at are one internal state value.
//
// The prices follow the rate in steps: after every catalog read, and when the rate has moved half a percent from the one they were last
// worked out at, so a price does not flicker with every tick of the market. An unknown rate changes nothing: a product on sale never loses
// its price.

const (
	// supplierPricingKey is the internal state: {auto, markup, rate, at}.
	supplierPricingKey = "state.supplier.pricing"
	// SupplierPriceStep is what an automatic price is rounded up to, in Toman.
	SupplierPriceStep = 1000
	// MaxSupplierMarkup is the largest markup an owner may set (percent).
	MaxSupplierMarkup = 500

	// supplierRepriceMovePermille is how far the rate may move (per mille of the rate the prices were worked out at) before they follow it.
	supplierRepriceMovePermille = 5

	// PriceModeAuto and PriceModeFixed are a product's price modes.
	PriceModeAuto  = "auto"
	PriceModeFixed = "fixed"
)

// supplierPricingState is the owner's pricing choice and the rate it was last applied at.
type supplierPricingState struct {
	Auto   bool  `json:"auto"`
	Markup int64 `json:"markup"`
	// Rate is the Toman per USDT the automatic prices were last worked out at (0: never), At when (unix seconds).
	Rate int64 `json:"rate"`
	At   int64 `json:"at"`
}

func (s *Service) supplierPricingState(ctx context.Context) supplierPricingState {
	m, err := s.st.GetSettings(ctx, s.st.Conn())
	if err != nil {
		return supplierPricingState{}
	}
	return supplierPricingFrom(m)
}

func supplierPricingFrom(m map[string]json.RawMessage) supplierPricingState {
	var st supplierPricingState
	if raw, ok := m[supplierPricingKey]; !ok || json.Unmarshal(raw, &st) != nil {
		return supplierPricingState{}
	}
	st.Markup = max(0, min(st.Markup, MaxSupplierMarkup))
	return st
}

func (s *Service) putSupplierPricingState(ctx context.Context, tx pgx.Tx, st supplierPricingState) error {
	raw, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return s.st.SetSetting(ctx, tx, supplierPricingKey, raw)
}

// SupplierPriceAutoOn reports whether the owner switched the automatic pricing on (the rate worker keeps the rate fresh for it).
func (s *Service) SupplierPriceAutoOn(ctx context.Context) bool {
	return s.PrivateFeatures(ctx) && s.supplierPricingState(ctx).Auto
}

// supplierCostCurrencies are the supplier's cost currencies that count as one USDT each.
var supplierCostCurrencies = map[string]bool{"USDT": true, "USD": true, "USDC": true}

// SupplierCostFollows reports whether a product's price can follow the rate: the supplier's cost is read, above zero and in a dollar
// currency. (The SQL twin is store.supplierFollowable.)
func SupplierCostFollows(p *store.SupplierProduct) bool {
	return p.CostMicro != nil && *p.CostMicro > 0 && supplierCostCurrencies[p.CostCurrency]
}

// SupplierAutoPrice is the Toman price of one unit for a cost in millionths of a USDT: cost x rate x (100 + markup) / 100, rounded up to the
// next SupplierPriceStep. ok is false when it cannot be worked out (no cost or rate, a markup outside 0..MaxSupplierMarkup, or a price over
// the most core takes).
func SupplierAutoPrice(costMicro, rate, markup int64) (price int64, ok bool) {
	if costMicro <= 0 || rate <= 0 || markup < 0 || markup > MaxSupplierMarkup {
		return 0, false
	}
	n := new(big.Int).Mul(big.NewInt(costMicro), big.NewInt(rate))
	n.Mul(n, big.NewInt(100+markup))
	d := big.NewInt(usdtScale * 100 * SupplierPriceStep)
	q, r := new(big.Int).QuoRem(n, d, new(big.Int))
	if r.Sign() > 0 {
		q.Add(q, big.NewInt(1))
	}
	q.Mul(q, big.NewInt(SupplierPriceStep))
	if !q.IsInt64() || q.Sign() <= 0 || q.Int64() > supplierMaxPrice {
		return 0, false
	}
	return q.Int64(), true
}

// supplierRateMoved reports whether the rate is half a percent or more away from the one the prices were last worked out at.
func supplierRateMoved(applied, now int64) bool {
	if applied <= 0 {
		return now > 0
	}
	diff := now - applied
	if diff < 0 {
		diff = -diff
	}
	return diff*1000 >= applied*supplierRepriceMovePermille
}

// SupplierPricing is the pricing as the dashboard shows it.
type SupplierPricing struct {
	Auto   bool
	Markup int64
	// AppliedRate and AppliedAt: the rate the prices were last worked out at and when (unix seconds); 0 when never.
	AppliedRate, AppliedAt int64
	Counts                 store.SupplierPricingCounts
}

// SupplierPricing reads the owner's pricing choice and how the products are priced.
func (s *Service) SupplierPricing(ctx context.Context) (SupplierPricing, error) {
	st := s.supplierPricingState(ctx)
	counts, err := s.st.CountSupplierPricing(ctx, s.st.Conn())
	if err != nil {
		return SupplierPricing{}, err
	}
	return SupplierPricing{Auto: st.Auto, Markup: st.Markup, AppliedRate: st.Rate, AppliedAt: st.At, Counts: counts}, nil
}

// repriceSupplierProducts works out the price of every product that follows the rate at rate and markup and writes the ones that changed,
// in tx. It returns how many changed.
func (s *Service) repriceSupplierProducts(ctx context.Context, tx pgx.Tx, rate, markup int64) (int, error) {
	ps, err := s.st.ListSupplierAutoProducts(ctx, tx)
	if err != nil {
		return 0, err
	}
	changed := 0
	for i := range ps {
		p := &ps[i]
		price, ok := SupplierAutoPrice(*p.CostMicro, rate, markup)
		if !ok || (p.SellPrice != nil && *p.SellPrice == price) {
			continue
		}
		if err := s.st.SetSupplierAutoPrice(ctx, tx, p.ID, price); err != nil {
			return 0, err
		}
		changed++
	}
	return changed, nil
}

// SupplierPricingResult is what a change of the pricing did.
type SupplierPricingResult struct {
	// Changed is how many products got a new price; Switched how many were made to follow the rate; Replaced how many of those had a price
	// the owner typed, which is now replaced.
	Changed, Switched, Replaced int
}

// SetSupplierPricing saves the owner's choice (the switch and the markup, in percent) and, when it is on, prices every product that follows
// the rate at once. Turning it on needs a known USDT rate.
func (s *Service) SetSupplierPricing(ctx context.Context, actor *store.User, auto bool, markup int64) (SupplierPricingResult, error) {
	if err := s.requirePrivate(ctx); err != nil {
		return SupplierPricingResult{}, err
	}
	if markup < 0 || markup > MaxSupplierMarkup {
		return SupplierPricingResult{}, &FieldError{"markup", "a percent from 0 to 500"}
	}
	rate := s.UsdtRate(ctx) // before the transaction: a handler that holds a connection must not ask the pool for another
	if auto && rate <= 0 {
		return SupplierPricingResult{}, stateErr("the USDT rate is not known yet: read it in Gateways, or type one, first")
	}
	var res SupplierPricingResult
	err := s.st.WithTx(ctx, func(tx pgx.Tx) error {
		res = SupplierPricingResult{}
		st := supplierPricingFrom(mustSettings(s.st.GetSettings(ctx, tx)))
		before := st
		st.Auto, st.Markup = auto, markup
		if auto {
			n, err := s.repriceSupplierProducts(ctx, tx, rate, markup)
			if err != nil {
				return err
			}
			res.Changed = n
			st.Rate, st.At = rate, time.Now().Unix()
		}
		if err := s.putSupplierPricingState(ctx, tx, st); err != nil {
			return err
		}
		return s.auditChange(ctx, tx, actor, "supplier.pricing", "supplier", "pricing",
			map[string]any{"auto": before.Auto, "markup": before.Markup},
			map[string]any{"auto": auto, "markup": markup, "rate": rate, "changed": res.Changed}, "")
	})
	return res, err
}

// ApplySupplierPricing prices the products now at the store's rate and the saved markup. With all, every product that can follow the
// rate is first made to follow it, so the prices the owner typed are replaced. It needs the pricing to be on and the rate to be known.
func (s *Service) ApplySupplierPricing(ctx context.Context, actor *store.User, all bool) (SupplierPricingResult, error) {
	if err := s.requirePrivate(ctx); err != nil {
		return SupplierPricingResult{}, err
	}
	st := s.supplierPricingState(ctx)
	if !st.Auto {
		return SupplierPricingResult{}, stateErr("turn the automatic pricing on first")
	}
	rate := s.UsdtRate(ctx)
	if rate <= 0 {
		return SupplierPricingResult{}, stateErr("the USDT rate is not known yet: read it in Gateways, or type one, first")
	}
	var res SupplierPricingResult
	err := s.st.WithTx(ctx, func(tx pgx.Tx) error {
		res = SupplierPricingResult{}
		if all {
			var err error
			if res.Switched, res.Replaced, err = s.st.SwitchSupplierProductsToAuto(ctx, tx); err != nil {
				return err
			}
		}
		n, err := s.repriceSupplierProducts(ctx, tx, rate, st.Markup)
		if err != nil {
			return err
		}
		res.Changed = n
		st.Rate, st.At = rate, time.Now().Unix()
		if err := s.putSupplierPricingState(ctx, tx, st); err != nil {
			return err
		}
		return s.audit(ctx, tx, actor, "supplier.reprice", "supplier", "pricing",
			map[string]any{"all": all, "rate": rate, "markup": st.Markup, "changed": n, "switched": res.Switched, "replaced": res.Replaced}, "")
	})
	return res, err
}

// RepriceSupplierProducts is the worker's step: with the pricing on, it follows the rate. force (after a catalog read, which brings new
// products and new costs) prices at once, at the rate the prices were last worked out at while the rate has not moved half a percent from
// it; without force it acts only when the rate has moved that far. It never acts on an unknown rate.
func (s *Service) RepriceSupplierProducts(ctx context.Context, force bool) (int, error) {
	if !s.PrivateFeatures(ctx) {
		return 0, nil
	}
	st := s.supplierPricingState(ctx)
	if !st.Auto {
		return 0, nil
	}
	now := s.UsdtRate(ctx)
	if now <= 0 {
		return 0, nil
	}
	moved := supplierRateMoved(st.Rate, now)
	if !moved && !force {
		return 0, nil
	}
	rate := now
	if !moved {
		rate = st.Rate
	}
	changed := 0
	err := s.st.WithTx(ctx, func(tx pgx.Tx) error {
		changed = 0
		n, err := s.repriceSupplierProducts(ctx, tx, rate, st.Markup)
		if err != nil {
			return err
		}
		changed = n
		if moved {
			st.Rate, st.At = rate, time.Now().Unix()
			if err := s.putSupplierPricingState(ctx, tx, st); err != nil {
				return err
			}
		}
		if n == 0 {
			return nil
		}
		return s.audit(ctx, tx, nil, "supplier.reprice", "supplier", "pricing", map[string]any{"rate": rate, "markup": st.Markup, "changed": n}, "")
	})
	return changed, err
}

// mustSettings is the settings of a read that failed as none (the caller then sees the defaults, and a write after it fails on its own).
func mustSettings(m map[string]json.RawMessage, err error) map[string]json.RawMessage {
	if err != nil {
		return nil
	}
	return m
}
