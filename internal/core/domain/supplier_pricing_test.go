package domain

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// setLiveRate makes the store's USDT rate the live price (fresh), with the payments margin in percent.
func (e *supplierEnv) setLiveRate(rate int64, margin string) {
	e.t.Helper()
	ctx := context.Background()
	raw, _ := json.Marshal(LiveRate{Rate: rate, At: time.Now().Unix(), Sources: []string{"test"}})
	if err := e.s.SetSetting(ctx, e.s.Conn(), liveRateKey, raw); err != nil {
		e.t.Fatal(err)
	}
	if err := e.svc.SetSetting(ctx, e.owner, SettingUSDTMargin, margin); err != nil {
		e.t.Fatal(err)
	}
}

func (e *supplierEnv) clearLiveRate() {
	e.t.Helper()
	if _, err := e.s.DB().Exec(context.Background(), `DELETE FROM core.settings WHERE key = $1`, liveRateKey); err != nil {
		e.t.Fatal(err)
	}
}

func (e *supplierEnv) product(id string) store.SupplierProduct {
	e.t.Helper()
	p, err := e.s.GetSupplierProduct(context.Background(), e.s.Conn(), id, false)
	if err != nil {
		e.t.Fatal(err)
	}
	return *p
}

func priceOf(p store.SupplierProduct) int64 {
	if p.SellPrice == nil {
		return -1
	}
	return *p.SellPrice
}

// The price is cost x rate x (100 + markup) / 100, rounded up to the next thousand Toman, worked out exactly.
func TestSupplierAutoPriceMath(t *testing.T) {
	for name, tc := range map[string]struct {
		cost, rate, markup int64
		want               int64
	}{
		"markup rounds up":          {2_600_000, 140_000, 20, 437_000}, // 436,800
		"another one":               {1_200_000, 140_000, 20, 202_000}, // 201,600
		"exact multiple stays":      {4_000_000, 140_000, 20, 672_000},
		"no markup, exact":          {1_000_000, 100_000, 0, 100_000},
		"no markup, rounds up":      {1_500_000, 100_001, 0, 151_000}, // 150,001.5
		"a tiny cost is a thousand": {1, 100_000, 0, 1_000},
		"a half unit":               {500_000, 135_100, 25, 85_000}, // 84,437.5
		"the biggest markup":        {1_000_000, 100_000, MaxSupplierMarkup, 600_000},
	} {
		got, ok := SupplierAutoPrice(tc.cost, tc.rate, tc.markup)
		if !ok || got != tc.want {
			t.Errorf("%s: %d %v, want %d", name, got, ok, tc.want)
		}
	}
	for name, tc := range map[string][3]int64{
		"no cost": {0, 100_000, 20}, "a negative cost": {-1, 100_000, 20}, "no rate": {1_000_000, 0, 20},
		"a negative markup": {1_000_000, 100_000, -1}, "too big a markup": {1_000_000, 100_000, MaxSupplierMarkup + 1},
		"a price over the most core takes": {9_000_000_000_000_000, 1_000_000, 100},
	} {
		if got, ok := SupplierAutoPrice(tc[0], tc[1], tc[2]); ok {
			t.Errorf("%s was priced: %d", name, got)
		}
	}
}

func TestSupplierRateMoved(t *testing.T) {
	for _, tc := range []struct {
		applied, now int64
		want         bool
	}{
		{100_000, 100_000, false}, {100_000, 100_499, false}, {100_000, 100_500, true}, {100_000, 99_501, false}, {100_000, 99_500, true},
		{0, 100_000, true}, {0, 0, false},
	} {
		if got := supplierRateMoved(tc.applied, tc.now); got != tc.want {
			t.Errorf("moved(%d, %d) = %v, want %v", tc.applied, tc.now, got, tc.want)
		}
	}
}

// The prices follow the store's rate (with its payments margin), in steps; a product the owner priced stays put; an unknown rate changes nothing.
func TestSupplierPricingFollowsTheRate(t *testing.T) {
	e := newSupplierEnv(t)
	ctx := context.Background()
	e.sync()
	e.setLiveRate(140_000, "0")

	// Before the owner turns it on, nothing is priced, however many products came in.
	if n, err := e.svc.RepriceSupplierProducts(ctx, true); err != nil || n != 0 {
		t.Fatalf("pricing off: %d %v", n, err)
	}
	if p := e.product("gem-1m"); p.SellPrice != nil || p.PriceMode != PriceModeAuto {
		t.Fatalf("a new product: %+v", p)
	}

	res, err := e.svc.SetSupplierPricing(ctx, e.owner, true, 20)
	if err != nil || res.Changed != 3 {
		t.Fatalf("turn on: %+v %v", res, err)
	}
	if got := []int64{priceOf(e.product("gem-1m")), priceOf(e.product("chat-1m")), priceOf(e.product("vpn-1y"))}; got[0] != 437_000 || got[1] != 672_000 || got[2] != 1_764_000 {
		t.Fatalf("prices: %v", got)
	}
	if p := e.product("gem-1m"); p.Enabled {
		t.Fatal("pricing switched a product on")
	}
	pr, _ := e.svc.SupplierPricing(ctx)
	if !pr.Auto || pr.Markup != 20 || pr.AppliedRate != 140_000 || pr.Counts.Auto != 3 || pr.Counts.Fixed != 0 {
		t.Fatalf("pricing: %+v", pr)
	}

	// The owner sells one and prices another by hand.
	e.sell("gem-1m", 437_000)
	if p := e.product("gem-1m"); p.PriceMode != PriceModeAuto {
		t.Fatalf("the same price typed again made it fixed: %+v", p)
	}
	hand := int64(500_000)
	if _, err := e.svc.UpdateSupplierProduct(ctx, e.owner, "chat-1m", SupplierEdit{SellPrice: &hand}); err != nil {
		t.Fatal(err)
	}
	if p := e.product("chat-1m"); p.PriceMode != PriceModeFixed || priceOf(p) != 500_000 {
		t.Fatalf("a typed price: %+v", p)
	}

	// A small move leaves the prices alone, also after a catalog read; half a percent moves them. The fixed one never moves.
	e.setLiveRate(140_300, "0")
	if n, err := e.svc.RepriceSupplierProducts(ctx, false); err != nil || n != 0 {
		t.Fatalf("a small move: %d %v", n, err)
	}
	e.sync()
	if priceOf(e.product("gem-1m")) != 437_000 || priceOf(e.product("vpn-1y")) != 1_764_000 {
		t.Fatal("a catalog read moved prices the rate had not moved enough for")
	}
	e.setLiveRate(150_000, "0")
	n, err := e.svc.RepriceSupplierProducts(ctx, false)
	if err != nil || n != 2 {
		t.Fatalf("a real move: %d %v", n, err)
	}
	if priceOf(e.product("gem-1m")) != 468_000 || priceOf(e.product("vpn-1y")) != 1_890_000 || priceOf(e.product("chat-1m")) != 500_000 {
		t.Fatalf("after the move: %d %d %d", priceOf(e.product("gem-1m")), priceOf(e.product("vpn-1y")), priceOf(e.product("chat-1m")))
	}
	if pr, _ = e.svc.SupplierPricing(ctx); pr.AppliedRate != 150_000 {
		t.Fatalf("applied rate: %d", pr.AppliedRate)
	}

	// An unknown rate changes nothing, and a product on sale keeps its price.
	e.clearLiveRate()
	if _, err := e.s.DB().Exec(ctx, `DELETE FROM core.settings WHERE key IN ('payments.usdt_rate')`); err != nil {
		t.Fatal(err)
	}
	if n, err := e.svc.RepriceSupplierProducts(ctx, true); err != nil || n != 0 {
		t.Fatalf("no rate: %d %v", n, err)
	}
	if p := e.product("gem-1m"); !p.Enabled || priceOf(p) != 468_000 {
		t.Fatalf("a product on sale lost its price: %+v", p)
	}
}

// The payments margin is part of the store's rate, so it is part of the price (the same rate as the wallet's conversions).
func TestSupplierPricingUsesTheStoresRate(t *testing.T) {
	e := newSupplierEnv(t)
	ctx := context.Background()
	e.sync()
	e.setLiveRate(100_000, "10") // the store charges 110,000
	if _, err := e.svc.SetSupplierPricing(ctx, e.owner, true, 0); err != nil {
		t.Fatal(err)
	}
	if got := priceOf(e.product("gem-1m")); got != 286_000 { // 2.6 x 110,000
		t.Fatalf("gem at 110,000: %d", got)
	}
	pr, _ := e.svc.SupplierPricing(ctx)
	if pr.AppliedRate != 110_000 {
		t.Fatalf("applied rate: %d", pr.AppliedRate)
	}
}

// A product that arrives after the pricing is on is priced at once and still comes in OFF.
func TestSupplierNewProductIsPricedButOff(t *testing.T) {
	e := newSupplierEnv(t)
	ctx := context.Background()
	e.setLiveRate(100_000, "0")
	if _, err := e.svc.SetSupplierPricing(ctx, e.owner, true, 20); err != nil {
		t.Fatal(err)
	}
	e.sync() // the pricing is on before the first read
	for id, want := range map[string]int64{"gem-1m": 312_000, "chat-1m": 480_000, "vpn-1y": 1_260_000} {
		if p := e.product(id); priceOf(p) != want || p.Enabled || p.PriceMode != PriceModeAuto {
			t.Errorf("%s: %+v, want %d off and auto", id, p, want)
		}
	}
	// A cost the supplier changes follows through the next read.
	e.fake.set(func() { e.fake.catalog = strings.Replace(e.fake.catalog, `"price":"2.60"`, `"price":"3.10"`, 1) })
	e.sync()
	if got := priceOf(e.product("gem-1m")); got != 372_000 {
		t.Fatalf("the new cost: %d", got)
	}
}

// "Apply to all" makes every product that can follow the rate follow it (the typed prices are replaced); one the supplier gives no dollar
// cost for stays the owner's.
func TestSupplierApplyToAll(t *testing.T) {
	e := newSupplierEnv(t)
	ctx := context.Background()
	e.fake.set(func() {
		e.fake.catalog = `{"products":[{"id":"gem-1m","name":"Gemini","price":"2.60","stock":5},{"id":"eur-1","name":"Euro thing","price":3,"currency":"EUR","stock":5},
			{"id":"free-1","name":"No cost"}]}`
	})
	if n, err := e.svc.SyncSupplierCatalog(ctx); err != nil || n != 3 {
		t.Fatalf("sync: %d %v", n, err)
	}
	e.setLiveRate(100_000, "0")
	hand, eur := int64(900_000), int64(700_000)
	for id, price := range map[string]int64{"gem-1m": hand, "eur-1": eur} {
		on := true
		if _, err := e.svc.UpdateSupplierProduct(ctx, e.owner, id, SupplierEdit{Enabled: &on, SellPrice: ptr64(price)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.svc.ApplySupplierPricing(ctx, e.owner, true); err == nil || !errors.Is(err, ErrState) {
		t.Fatalf("apply with the pricing off: %v", err)
	}
	if _, err := e.svc.SetSupplierPricing(ctx, e.owner, true, 10); err != nil {
		t.Fatal(err)
	}
	pr, _ := e.svc.SupplierPricing(ctx)
	// gem and eur carry the owner's prices (fixed); free was never priced (auto); eur and free have no dollar cost to follow.
	if pr.Counts.Auto != 1 || pr.Counts.Fixed != 2 || pr.Counts.HandPriced != 1 || pr.Counts.CannotFollow != 2 {
		t.Fatalf("counts before: %+v", pr.Counts)
	}
	// Without "all" nothing of the owner's is touched.
	if res, err := e.svc.ApplySupplierPricing(ctx, e.owner, false); err != nil || res.Switched != 0 || priceOf(e.product("gem-1m")) != 900_000 {
		t.Fatalf("apply (not all): %+v %v", res, err)
	}
	res, err := e.svc.ApplySupplierPricing(ctx, e.owner, true)
	if err != nil || res.Switched != 1 || res.Replaced != 1 || res.Changed != 1 {
		t.Fatalf("apply all: %+v %v", res, err)
	}
	if p := e.product("gem-1m"); p.PriceMode != PriceModeAuto || priceOf(p) != 286_000 || !p.Enabled {
		t.Fatalf("gem after: %+v", p)
	}
	if p := e.product("eur-1"); p.PriceMode != PriceModeFixed || priceOf(p) != 700_000 {
		t.Fatalf("a euro product follows the dollar rate: %+v", p)
	}
	if p := e.product("free-1"); p.SellPrice != nil {
		t.Fatalf("a product with no cost was priced: %+v", p)
	}
}

// What typing a price, or asking a product to follow the rate, does to its mode.
func TestSupplierProductPriceModeEdits(t *testing.T) {
	e := newSupplierEnv(t)
	ctx := context.Background()
	e.sync()
	mode := func(m string) *string { return &m }
	edit := func(id string, ed SupplierEdit) (*store.SupplierProduct, error) {
		return e.svc.UpdateSupplierProduct(ctx, e.owner, id, ed)
	}

	// Following needs a rate.
	if _, err := edit("gem-1m", SupplierEdit{PriceMode: mode(PriceModeAuto)}); err != nil {
		t.Fatalf("a product that is already auto: %v", err) // new products come in auto: nothing to switch
	}
	if _, err := edit("gem-1m", SupplierEdit{PriceMode: mode(PriceModeFixed), SellPrice: ptr64(400_000)}); err != nil {
		t.Fatal(err)
	}
	if _, err := edit("gem-1m", SupplierEdit{PriceMode: mode(PriceModeAuto)}); !errors.Is(err, ErrState) {
		t.Fatalf("follow without a rate: %v", err)
	}
	e.setLiveRate(100_000, "0")
	if _, err := e.svc.SetSupplierPricing(ctx, e.owner, false, 25); err != nil { // off, markup 25 saved
		t.Fatal(err)
	}
	p, err := edit("gem-1m", SupplierEdit{PriceMode: mode(PriceModeAuto), SellPrice: ptr64(1)}) // the typed price is ignored
	if err != nil || p.PriceMode != PriceModeAuto || priceOf(*p) != 325_000 {                   // 2.6 x 100,000 x 1.25
		t.Fatalf("follow: %+v %v", p, err)
	}
	// Texts and the switch do not change how it is priced; an unchanged price is not a typed one.
	on := true
	if p, err = edit("gem-1m", SupplierEdit{Enabled: &on, SellPrice: ptr64(325_000), Title: map[string]string{"fa": "الف"}}); err != nil || p.PriceMode != PriceModeAuto || !p.Enabled {
		t.Fatalf("texts and switch: %+v %v", p, err)
	}
	// A different price typed is the owner's.
	if p, err = edit("gem-1m", SupplierEdit{SellPrice: ptr64(330_000)}); err != nil || p.PriceMode != PriceModeFixed || priceOf(*p) != 330_000 {
		t.Fatalf("a typed price: %+v %v", p, err)
	}
	// Fixed with no price given keeps the price it has.
	if _, err = edit("gem-1m", SupplierEdit{PriceMode: mode(PriceModeAuto)}); err != nil {
		t.Fatal(err)
	}
	if p, err = edit("gem-1m", SupplierEdit{PriceMode: mode(PriceModeFixed)}); err != nil || p.PriceMode != PriceModeFixed || priceOf(*p) != 325_000 {
		t.Fatalf("fixed keeps the price: %+v %v", p, err)
	}
	// A product on sale cannot lose its price this way either.
	if _, err = edit("gem-1m", SupplierEdit{SellPrice: ptr64(0)}); err == nil {
		t.Fatal("the price of a product on sale was cleared")
	}
	for name, bad := range map[string]SupplierEdit{"another mode": {PriceMode: mode("sometimes")}} {
		if _, err := edit("gem-1m", bad); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	// A product with no dollar cost cannot follow.
	e.fake.set(func() { e.fake.catalog = `{"products":[{"id":"nocost","name":"No cost"}]}` })
	if _, err := e.svc.SyncSupplierCatalog(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := edit("nocost", SupplierEdit{PriceMode: mode(PriceModeFixed), SellPrice: ptr64(1000)}); err != nil {
		t.Fatal(err)
	}
	var fe *FieldError
	if _, err := edit("nocost", SupplierEdit{PriceMode: mode(PriceModeAuto)}); !errors.As(err, &fe) || fe.Field != "price_mode" {
		t.Fatalf("follow without a cost: %v", err)
	}
}

// The owner's choice is checked, needs a known rate to turn on, and is part of the license's private features.
func TestSupplierPricingRefusals(t *testing.T) {
	e := newSupplierEnv(t)
	ctx := context.Background()
	e.sync()
	var fe *FieldError
	for _, m := range []int64{-1, MaxSupplierMarkup + 1} {
		if _, err := e.svc.SetSupplierPricing(ctx, e.owner, false, m); !errors.As(err, &fe) || fe.Field != "markup" {
			t.Errorf("markup %d: %v", m, err)
		}
	}
	e.clearLiveRate()
	if _, err := e.svc.SetSupplierPricing(ctx, e.owner, true, 20); !errors.Is(err, ErrState) {
		t.Fatalf("on without a rate: %v", err)
	}
	if pr, _ := e.svc.SupplierPricing(ctx); pr.Auto {
		t.Fatal("a refused switch was saved")
	}
	if _, err := e.svc.SetSupplierPricing(ctx, e.owner, false, 20); err != nil { // the markup alone can be saved without a rate
		t.Fatal(err)
	}
	if pr, _ := e.svc.SupplierPricing(ctx); pr.Markup != 20 || pr.Auto {
		t.Fatalf("the markup: %+v", pr)
	}
	off := false
	e.svc.cfg.PrivateFeaturesOverride = &off
	if _, err := e.svc.SetSupplierPricing(ctx, e.owner, false, 5); !errors.Is(err, ErrState) {
		t.Fatalf("without the private features: %v", err)
	}
	if e.svc.SupplierPriceAutoOn(ctx) {
		t.Fatal("pricing is on without the private features")
	}
}
