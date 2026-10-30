package domain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
)

// affEnv is a store that sells licenses with the affiliate program on, an affiliate and a buyer they brought.
type affEnv struct {
	*vendorEnv
	aff   *store.User
	buyer *store.User
	code  string
}

// standardMonth is what a standard license for a month costs in Toman at the test rate (4 USDT).
const standardMonth = 4 * testUsdtRate

func newAffEnv(t *testing.T) *affEnv {
	t.Helper()
	e := &affEnv{vendorEnv: newVendorEnv(t)}
	*e.clock = time.Now().UTC() // the refund window is measured against the order's own time (the database's)
	ctx := context.Background()
	if err := e.svc.SetSetting(ctx, e.owner, SettingAffiliateEnabled, "true"); err != nil {
		t.Fatal(err)
	}
	e.aff, e.buyer = seedUser(t, e.s, 9501), seedUser(t, e.s, 9502)
	info, err := e.svc.MyAffiliate(ctx, e.aff.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !affiliateCodeRe.MatchString(info.Code) || info.Percent != DefaultAffiliatePercent || info.Status != "active" {
		t.Fatalf("affiliate: %+v", info)
	}
	e.code = info.Code
	return e
}

func (e *affEnv) wallet(u *store.User) int64 {
	e.t.Helper()
	w, err := e.s.GetWallet(context.Background(), e.s.Conn(), u.ID, WalletCurrency)
	if err != nil {
		e.t.Fatal(err)
	}
	return w.Balance
}

func (e *affEnv) apply(u *store.User, code string) bool {
	e.t.Helper()
	ok, err := e.svc.ApplyAffiliateCode(context.Background(), u.ID, code)
	if err != nil {
		e.t.Fatal(err)
	}
	return ok
}

func (e *affEnv) commissions() int {
	e.t.Helper()
	var n int
	if err := e.s.DB().QueryRow(context.Background(), `SELECT count(*) FROM core.affiliate_commissions`).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func TestAffiliateEarnsOnTheFirstLicenseOnly(t *testing.T) {
	e := newAffEnv(t)
	ctx := context.Background()
	if !e.apply(e.buyer, "  "+e.code+" ") {
		t.Fatal("the link did not count")
	}
	e.fund(e.buyer, 20*usdt)
	p, err := e.buy(e.buyer, "standard", VendorMonth, "aff-1")
	if err != nil {
		t.Fatal(err)
	}
	if p.Order.Status != "done" || p.Order.AffiliateID == nil {
		t.Fatalf("order: %+v", p.Order)
	}
	if got := e.wallet(e.aff); got != standardMonth*20/100 {
		t.Fatalf("affiliate wallet: %d, want %d", got, standardMonth*20/100)
	}
	c, err := e.s.GetAffiliateCommission(ctx, e.s.Conn(), p.Order.ID, false)
	if err != nil || c.Amount != standardMonth/5 || c.Percent != 20 || c.BaseAmount != standardMonth || c.BuyerUserID != e.buyer.ID ||
		c.AffiliateUserID != e.aff.ID {
		t.Fatalf("commission: %+v %v", c, err)
	}
	if e, err := e.s.LedgerByKey(ctx, e.s.Conn(), "affiliate_commission:"+p.Order.ID); err != nil || e.Kind != "affiliate_commission" || e.Amount != standardMonth/5 {
		t.Fatalf("ledger: %+v %v", e, err)
	}
	// The affiliate is told, and the buyer is not named.
	var told bool
	for _, raw := range outboxEvents(t, e.s, events.WalletCredited) {
		var ev events.WalletCreditedEvent
		_ = json.Unmarshal(raw, &ev)
		if ev.Reason == events.CreditAffiliate {
			told = ev.UserID == e.aff.ID && ev.Amount == standardMonth/5 && ev.OrderID == ""
		}
	}
	if !told {
		t.Fatal("the affiliate was not told")
	}
	// A second license, and a renewal, of the same buyer earn nothing: the first payment only.
	if _, err := e.buy(e.buyer, "standard", VendorMonth, "aff-2"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.BuyVendorLicense(ctx, VendorBuyParams{UserID: e.buyer.ID, LicenseID: p.License.ID, Months: VendorMonth, IdempotencyKey: "aff-3"}); err != nil {
		t.Fatal(err)
	}
	if got := e.wallet(e.aff); got != standardMonth/5 || e.commissions() != 1 {
		t.Fatalf("after more purchases: wallet %d, commissions %d", got, e.commissions())
	}
	// The affiliate's own figures.
	info, err := e.svc.MyAffiliate(ctx, e.aff.ID)
	if err != nil || info.Referred != 1 || info.Licenses != 1 || info.Earned != standardMonth/5 || info.Paid != 0 || info.Balance != standardMonth/5 {
		t.Fatalf("figures: %+v %v", info, err)
	}
}

func TestAffiliateLinkGuards(t *testing.T) {
	e := newAffEnv(t)
	ctx := context.Background()
	// No self-referral: the affiliate's own link records nothing for them.
	if e.apply(e.aff, e.code) {
		t.Fatal("self-referral counted")
	}
	// Unknown and malformed codes, and the first link wins.
	other := seedUser(t, e.s, 9503)
	otherInfo, err := e.svc.MyAffiliate(ctx, other.ID)
	if err != nil {
		t.Fatal(err)
	}
	if e.apply(e.buyer, "nosuchcode") || e.apply(e.buyer, "x") || e.apply(e.buyer, "'; drop table") {
		t.Fatal("a bad code counted")
	}
	if !e.apply(e.buyer, e.code) || e.apply(e.buyer, otherInfo.Code) {
		t.Fatal("first link wins")
	}
	// A customer who bought a license before is not a new buyer.
	old := seedUser(t, e.s, 9504)
	e.fund(old, 10*usdt)
	if _, err := e.buy(old, "standard", VendorMonth, "old-1"); err != nil {
		t.Fatal(err)
	}
	if e.apply(old, e.code) {
		t.Fatal("a customer who bought before counted")
	}
	// A disabled affiliate's link records nobody new.
	if _, err := e.svc.UpdateAffiliate(ctx, e.owner, mustAffiliate(t, e, other).ID, nil, "disabled"); err != nil {
		t.Fatal(err)
	}
	late := seedUser(t, e.s, 9505)
	if e.apply(late, otherInfo.Code) {
		t.Fatal("a disabled affiliate's link counted")
	}
	// The program off: links count for nothing and /affiliate is refused.
	if err := e.svc.SetSetting(ctx, e.owner, SettingAffiliateEnabled, "false"); err != nil {
		t.Fatal(err)
	}
	if e.apply(late, e.code) {
		t.Fatal("a link counted while the program is off")
	}
	if _, err := e.svc.MyAffiliate(ctx, late.ID); !errors.Is(err, ErrState) {
		t.Fatalf("MyAffiliate while off: %v", err)
	}
}

func mustAffiliate(t *testing.T, e *affEnv, u *store.User) *store.Affiliate {
	t.Helper()
	a, err := e.s.GetAffiliateByUser(context.Background(), e.s.Conn(), u.ID)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// A self-referral that got onto an order anyway (written by hand) still earns nothing, and the database refuses such a commission.
func TestAffiliateNeverPaysTheBuyerThemselves(t *testing.T) {
	e := newAffEnv(t)
	ctx := context.Background()
	a := mustAffiliate(t, e, e.aff)
	if _, err := e.s.SetAffiliateReferral(ctx, e.s.Conn(), e.aff.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	e.fund(e.aff, 10*usdt)
	p, err := e.buy(e.aff, "standard", VendorMonth, "self-1")
	if err != nil {
		t.Fatal(err)
	}
	if p.Order.Status != "done" || e.commissions() != 0 {
		t.Fatalf("self purchase: %+v, commissions %d", p.Order, e.commissions())
	}
	err = e.s.WithTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO core.affiliate_commissions (order_id, affiliate_id, affiliate_user_id, buyer_user_id, currency, base_amount,
			percent, amount, ledger_entry_id) SELECT $1, $2, $3, $3, 'IRT', 1, 1, 1, id FROM core.ledger_entries LIMIT 1`, p.Order.ID, a.ID, e.aff.ID)
		return err
	})
	if err == nil {
		t.Fatal("the database took a commission paid to the buyer themselves")
	}
}

func TestAffiliatePercentOverride(t *testing.T) {
	e := newAffEnv(t)
	ctx := context.Background()
	a := mustAffiliate(t, e, e.aff)
	fifty := 50
	if _, err := e.svc.UpdateAffiliate(ctx, e.owner, a.ID, &fifty, "active"); err != nil {
		t.Fatal(err)
	}
	e.apply(e.buyer, e.code)
	e.fund(e.buyer, 10*usdt)
	if _, err := e.buy(e.buyer, "standard", VendorMonth, "pct-1"); err != nil {
		t.Fatal(err)
	}
	if got := e.wallet(e.aff); got != standardMonth/2 {
		t.Fatalf("50%%: %d", got)
	}
	// 0: this affiliate earns nothing.
	zero := 0
	if _, err := e.svc.UpdateAffiliate(ctx, e.owner, a.ID, &zero, "active"); err != nil {
		t.Fatal(err)
	}
	b2 := seedUser(t, e.s, 9510)
	e.apply(b2, e.code)
	e.fund(b2, 10*usdt)
	if _, err := e.buy(b2, "standard", VendorMonth, "pct-2"); err != nil {
		t.Fatal(err)
	}
	if got := e.wallet(e.aff); got != standardMonth/2 || e.commissions() != 1 {
		t.Fatalf("0%%: %d, %d", got, e.commissions())
	}
	// Bad values are refused, and every change is audited.
	for _, bad := range []int{-1, 101} {
		if _, err := e.svc.UpdateAffiliate(ctx, e.owner, a.ID, &bad, "active"); !errors.Is(err, ErrInvalid) {
			t.Errorf("percent %d: %v", bad, err)
		}
	}
	if _, err := e.svc.UpdateAffiliate(ctx, e.owner, a.ID, nil, "paused"); !errors.Is(err, ErrInvalid) {
		t.Errorf("status: %v", err)
	}
	var n int
	if err := e.s.DB().QueryRow(ctx, `SELECT count(*) FROM core.audit_log WHERE action = 'affiliate.update' AND entity_id = $1`, a.ID).Scan(&n); err != nil || n != 2 {
		t.Fatalf("audit: %d %v", n, err)
	}
}

// Finishing an order twice (a retried worker, a second process) pays once.
func TestAffiliateDoubleFinishPaysOnce(t *testing.T) {
	e := newAffEnv(t)
	ctx := context.Background()
	e.apply(e.buyer, e.code)
	e.fund(e.buyer, 10*usdt)
	e.fake.set(func() { e.fake.failWith = 503 }) // the license server is down: the order waits, paid
	p, err := e.buy(e.buyer, "standard", VendorMonth, "dbl-1")
	if err != nil {
		t.Fatal(err)
	}
	if p.Order.Status != "paid" || p.Order.AffiliateID == nil {
		t.Fatalf("order: %+v", p.Order)
	}
	if e.commissions() != 0 {
		t.Fatal("paid before the license existed")
	}
	e.fake.set(func() { e.fake.failWith = 0 })
	info := &VendorLicenseInfo{ID: "lic_dbl", Tier: "standard", Status: "active"}
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := e.svc.finishVendorOrder(ctx, p.Order.ID, info, "BOBRES-FAKE-DBL"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if _, err := e.svc.SettleVendorOrders(ctx); err != nil {
		t.Fatal(err)
	}
	if got := e.wallet(e.aff); got != standardMonth/5 || e.commissions() != 1 {
		t.Fatalf("after two finishes: %d, %d", got, e.commissions())
	}
}

// Two first purchases of one buyer at the same moment: one commission.
func TestAffiliateConcurrentFirstPurchases(t *testing.T) {
	e := newAffEnv(t)
	needConns(t, e.s, 4)
	e.apply(e.buyer, e.code)
	e.fund(e.buyer, 20*usdt)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := e.buy(e.buyer, "standard", VendorMonth, fmt.Sprintf("cc-%d", i))
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := e.wallet(e.aff); got != standardMonth/5 || e.commissions() != 1 {
		t.Fatalf("concurrent purchases: %d, %d", got, e.commissions())
	}
}

func (e *affEnv) boughtLicense(key string) *VendorPurchase {
	e.t.Helper()
	e.apply(e.buyer, e.code)
	e.fund(e.buyer, 10*usdt)
	p, err := e.buy(e.buyer, "standard", VendorMonth, key)
	if err != nil || p.Order.Status != "done" {
		e.t.Fatalf("purchase: %+v %v", p, err)
	}
	return p
}

func TestAffiliateRefundWithin30DaysTakesItBack(t *testing.T) {
	e := newAffEnv(t)
	ctx := context.Background()
	p := e.boughtLicense("ref-1")
	before := e.wallet(e.buyer)
	*e.clock = time.Now().UTC().Add(29 * 24 * time.Hour)
	o, err := e.svc.RefundSoldLicense(ctx, e.owner, p.License.ID, "chargeback")
	if err != nil {
		t.Fatal(err)
	}
	if o.Status != "refunded" {
		t.Fatalf("order: %+v", o)
	}
	if got := e.wallet(e.buyer); got != before+standardMonth {
		t.Fatalf("buyer: %d, want %d", got, before+standardMonth)
	}
	if got := e.wallet(e.aff); got != 0 {
		t.Fatalf("affiliate after the refund: %d", got)
	}
	l, _ := e.s.GetVendorLicense(ctx, e.s.Conn(), p.License.ID, false)
	if l.Status != "revoked" || e.fake.callCount("revoke") != 1 {
		t.Fatalf("license: %s, revokes %d", l.Status, e.fake.callCount("revoke"))
	}
	c, _ := e.s.GetAffiliateCommission(ctx, e.s.Conn(), p.Order.ID, false)
	if c.ReversedAmount == nil || *c.ReversedAmount != standardMonth/5 || c.ReversalEntryID == nil {
		t.Fatalf("commission: %+v", c)
	}
	// Refunding again changes nothing.
	if _, err := e.svc.RefundSoldLicense(ctx, e.owner, p.License.ID, "again"); err != nil {
		t.Fatal(err)
	}
	if got := e.wallet(e.buyer); got != before+standardMonth {
		t.Fatalf("buyer after a second refund: %d", got)
	}
	var n int
	if err := e.s.DB().QueryRow(ctx, `SELECT count(*) FROM core.audit_log WHERE action = 'vendor.refund'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("refund audits: %d %v", n, err)
	}
	info, _ := e.svc.MyAffiliate(ctx, e.aff.ID)
	if info.Earned != 0 || info.Licenses != 1 {
		t.Fatalf("figures after the refund: %+v", info)
	}
}

// The affiliate spent part of the commission: the refund takes what is left, never below zero, and the shortfall is audited.
func TestAffiliateRefundNeverBelowZero(t *testing.T) {
	e := newAffEnv(t)
	ctx := context.Background()
	p := e.boughtLicense("short-1")
	spend := int64(30_000)
	if err := e.s.WithTx(ctx, func(tx pgx.Tx) error {
		_, err := e.s.Debit(ctx, tx, e.aff.ID, WalletCurrency, spend, &store.LedgerEntry{Kind: "purchase", IdempotencyKey: "spend-aff"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.RefundSoldLicense(ctx, e.owner, p.License.ID, "fraud"); err != nil {
		t.Fatal(err)
	}
	if got := e.wallet(e.aff); got != 0 {
		t.Fatalf("affiliate wallet: %d", got)
	}
	var detail map[string]int64
	var raw []byte
	if err := e.s.DB().QueryRow(ctx, `SELECT after FROM core.audit_log WHERE action = 'affiliate.reversal'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var full map[string]any
	_ = json.Unmarshal(raw, &full)
	detail = map[string]int64{}
	for _, k := range []string{"credited", "taken", "shortfall"} {
		if v, ok := full[k].(float64); ok {
			detail[k] = int64(v)
		}
	}
	if detail["credited"] != standardMonth/5 || detail["taken"] != standardMonth/5-spend || detail["shortfall"] != spend {
		t.Fatalf("audit: %v", full)
	}
}

func TestAffiliateRefundAfter30DaysKeepsIt(t *testing.T) {
	e := newAffEnv(t)
	ctx := context.Background()
	p := e.boughtLicense("late-1")
	*e.clock = time.Now().UTC().Add(31 * 24 * time.Hour)
	if _, err := e.svc.RefundSoldLicense(ctx, e.owner, p.License.ID, "goodwill"); err != nil {
		t.Fatal(err)
	}
	if got := e.wallet(e.aff); got != standardMonth/5 {
		t.Fatalf("affiliate kept: %d", got)
	}
	c, _ := e.s.GetAffiliateCommission(ctx, e.s.Conn(), p.Order.ID, false)
	if c.ReversedAt != nil {
		t.Fatalf("reversed after the window: %+v", c)
	}
	var kept bool
	if err := e.s.DB().QueryRow(ctx, `SELECT (after->>'affiliate_kept')::boolean FROM core.audit_log WHERE action = 'vendor.refund'`).Scan(&kept); err != nil || !kept {
		t.Fatalf("audit kept: %v %v", kept, err)
	}
}

func TestAffiliatePayouts(t *testing.T) {
	e := newAffEnv(t)
	ctx := context.Background()
	e.boughtLicense("pay-1")
	a := mustAffiliate(t, e, e.aff)
	earned := int64(standardMonth / 5)
	if err := e.svc.SetAffiliateSettings(ctx, e.owner, true, 20, 50_000); err != nil {
		t.Fatal(err)
	}
	// Below the minimum, above what was earned, and bad input are refused before any money moves.
	if _, err := e.svc.RecordAffiliatePayout(ctx, e.owner, a.ID, 40_000, "", "req-small-1"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("below the minimum: %v", err)
	}
	e.fund(e.aff, 10*usdt) // money of their own in the wallet is not commission to pay out
	if _, err := e.svc.RecordAffiliatePayout(ctx, e.owner, a.ID, earned+1, "", "req-big-01"); !errors.Is(err, ErrState) {
		t.Fatalf("more than earned: %v", err)
	}
	if _, err := e.svc.RecordAffiliatePayout(ctx, e.owner, a.ID, 60_000, "", "short"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad request id: %v", err)
	}
	before := e.wallet(e.aff)
	p, err := e.svc.RecordAffiliatePayout(ctx, e.owner, a.ID, 60_000, "USDT TRC20 tx abc", "req-pay-01")
	if err != nil {
		t.Fatal(err)
	}
	// The same request again is the same payout.
	again, err := e.svc.RecordAffiliatePayout(ctx, e.owner, a.ID, 60_000, "USDT TRC20 tx abc", "req-pay-01")
	if err != nil || again.ID != p.ID {
		t.Fatalf("replay: %+v %v", again, err)
	}
	if got := e.wallet(e.aff); got != before-60_000 {
		t.Fatalf("wallet: %d, want %d", got, before-60_000)
	}
	// What is left to pay is earned - paid: 20,000 is below the minimum, so nothing more can be paid.
	if _, err := e.svc.RecordAffiliatePayout(ctx, e.owner, a.ID, 50_000, "", "req-pay-02"); !errors.Is(err, ErrState) {
		t.Fatalf("second payout over the rest: %v", err)
	}
	d, err := e.svc.GetAffiliateDetail(ctx, a.ID)
	if err != nil || d.Stats.Paid[WalletCurrency] != 60_000 || len(d.Payouts) != 1 || len(d.Commissions) != 1 || d.Percent != 20 {
		t.Fatalf("detail: %+v %v", d, err)
	}
	rows, total, err := e.svc.ListAffiliates(ctx, store.Page{Limit: 10})
	if err != nil || total != 1 || rows[0].Earned != earned || rows[0].Paid != 60_000 || rows[0].Licenses != 1 || rows[0].Referred != 1 {
		t.Fatalf("list: %+v %d %v", rows, total, err)
	}
}

// Two payouts at once that together exceed what was earned: one goes through.
func TestAffiliateConcurrentPayouts(t *testing.T) {
	e := newAffEnv(t)
	needConns(t, e.s, 4)
	ctx := context.Background()
	e.boughtLicense("cp-1")
	a := mustAffiliate(t, e, e.aff)
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := 0
	for i := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := e.svc.RecordAffiliatePayout(ctx, e.owner, a.ID, 50_000, "", fmt.Sprintf("req-conc-%d", i)); err == nil {
				mu.Lock()
				ok++
				mu.Unlock()
			} else if !errors.Is(err, ErrState) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if ok != 1 || e.wallet(e.aff) != standardMonth/5-50_000 {
		t.Fatalf("concurrent payouts: %d went through, wallet %d", ok, e.wallet(e.aff))
	}
}

// The program switched off between the purchase and the delivery pays nothing new.
func TestAffiliateOffAtDeliveryPaysNothing(t *testing.T) {
	e := newAffEnv(t)
	ctx := context.Background()
	e.apply(e.buyer, e.code)
	e.fund(e.buyer, 10*usdt)
	e.fake.set(func() { e.fake.failWith = 503 })
	if _, err := e.buy(e.buyer, "standard", VendorMonth, "off-1"); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.SetSetting(ctx, e.owner, SettingAffiliateEnabled, "false"); err != nil {
		t.Fatal(err)
	}
	e.fake.set(func() { e.fake.failWith = 0 })
	*e.clock = e.clock.Add(time.Hour)
	if _, err := e.svc.SettleVendorOrders(ctx); err != nil {
		t.Fatal(err)
	}
	if e.commissions() != 0 || e.wallet(e.aff) != 0 {
		t.Fatalf("paid while off: %d", e.wallet(e.aff))
	}
}

func TestAffiliateAmount(t *testing.T) {
	for _, c := range []struct {
		base int64
		pct  int
		want int64
	}{{400_000, 20, 80_000}, {99, 20, 19}, {1, 100, 1}, {1, 1, 0}, {0, 20, 0}, {100, 0, 0}, {100, 101, 0},
		{9_223_372_036_854_775_807, 100, 9_223_372_036_854_775_807}, {9_223_372_036_854_775_807, 50, 4_611_686_018_427_387_903}} {
		if got := affiliateAmount(c.base, c.pct); got != c.want {
			t.Errorf("affiliateAmount(%d, %d) = %d, want %d", c.base, c.pct, got, c.want)
		}
	}
	cfg := AffiliateConfigOf(map[string]string{SettingAffiliatePercent: "35", SettingAffiliateEnabled: "true", SettingAffiliateMinPayout: "1000"})
	if !cfg.Enabled || cfg.Percent != 35 || cfg.MinPayout != 1000 {
		t.Fatalf("config: %+v", cfg)
	}
	if AffiliateConfigOf(map[string]string{SettingAffiliatePercent: "abc"}).Percent != DefaultAffiliatePercent {
		t.Fatal("a bad percent is not the default")
	}
}
