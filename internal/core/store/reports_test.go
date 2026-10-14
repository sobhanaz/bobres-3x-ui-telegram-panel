package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testdb"
	buuid "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
)

// The reports are checked against a model: every row is written to the
// database with an explicit time and also recorded in plain Go values, and the
// expected numbers come from those values (Go's own time zone arithmetic, no
// SQL), so a wrong bucket edge, a wrong time zone or a mixed currency shows up
// as a different number.

type mPurchase struct {
	order, user, plan, typ, cur, src string
	amt                              int64
	at                               time.Time
	refunded                         int64
}

type mFlow struct {
	cur   string
	amt   int64
	at    time.Time
	taken int64 // of a top-up: what orders took of it at the same instant
}

type mPlan struct{ id, fa, en, cur string }

type model struct {
	purchases []*mPurchase
	topups    []mFlow
	refunds   []mFlow
	rejected  []mFlow // by staff
	gateway   []mFlow // failed or expired at a gateway
	cancelled []mFlow
	trials    []time.Time
}

type seed struct {
	t     *testing.T
	s     *Store
	ctx   context.Context
	m     model
	users map[int]string
	subs  map[int]string
}

func newSeed(t *testing.T) *seed {
	t.Helper()
	s := testStore(t)
	ctx := context.Background()
	if err := testdb.Truncate(ctx, s.DB(), `core.outbox_core`); err != nil {
		t.Fatal(err)
	}
	return &seed{t: t, s: s, ctx: ctx, users: map[int]string{}, subs: map[int]string{}}
}

func (x *seed) exec(sql string, args ...any) {
	x.t.Helper()
	if _, err := x.s.DB().Exec(x.ctx, sql, args...); err != nil {
		x.t.Fatalf("seed: %v\n%s", err, sql)
	}
}

func (x *seed) user(n int) string {
	x.t.Helper()
	if id, ok := x.users[n]; ok {
		return id
	}
	id := buuid.MustV7().String()
	x.exec(`INSERT INTO core.users (id, telegram_id, username) VALUES ($1, $2, $3)`, id, 9000+n, fmt.Sprintf("user%d", n))
	x.users[n] = id
	return id
}

func (x *seed) plan(fa, en, cur string, trial bool) *mPlan {
	x.t.Helper()
	p := &mPlan{id: buuid.MustV7().String(), fa: fa, en: en, cur: cur}
	name, _ := json.Marshal(map[string]string{"fa": fa, "en": en})
	x.exec(`INSERT INTO core.plans (id, name_i18n, kind, price, currency, is_trial) VALUES ($1, $2, 'both', 1000, $3, $4)`, p.id, string(name), cur, trial)
	return p
}

func (x *seed) ledger(user, cur string, amt int64, kind, refType, refID, key string, at time.Time) {
	x.t.Helper()
	var rt, ri any
	if refType != "" {
		rt, ri = refType, refID
	}
	x.exec(`INSERT INTO core.ledger_entries (id, user_id, currency, amount, kind, ref_type, ref_id, idempotency_key, balance_after, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 0, $9)`, buuid.MustV7().String(), user, cur, amt, kind, rt, ri, key, at)
}

func (x *seed) event(topic string, payload map[string]any, at time.Time) {
	x.t.Helper()
	raw, _ := json.Marshal(payload)
	x.exec(`INSERT INTO core.outbox_core (topic, payload, event_id, created_at) VALUES ($1, $2::text::jsonb, $3, $4)`,
		topic, string(raw), buuid.MustV7().String(), at)
}

func (x *seed) order(user string, p *mPlan, typ, status string, amt int64, at time.Time, sub *string) string {
	x.t.Helper()
	id := buuid.MustV7().String()
	x.exec(`INSERT INTO core.orders (id, user_id, plan_id, type, status, amount, currency, idempotency_key, subscription_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10)`, id, user, p.id, typ, status, amt, p.cur, "k-"+id, sub, at)
	return id
}

// paid writes a paid order the way payments and the wallet do: the order,
// its purchase entry and its order.paid.v1 event, all at one instant.
func (x *seed) paid(n int, p *mPlan, typ string, amt int64, at time.Time, src string) *mPurchase {
	x.t.Helper()
	u := x.user(n)
	var sub *string
	if typ != "new" {
		s, ok := x.subs[n]
		if !ok {
			x.t.Fatalf("user %d has no service to %s", n, typ)
		}
		sub = &s
	}
	id := x.order(u, p, typ, "active", amt, at, sub)
	if typ == "new" && x.subs[n] == "" {
		sid := buuid.MustV7().String()
		x.exec(`INSERT INTO core.subscriptions (id, user_id, order_id, server_id, client_email, status) VALUES ($1, $2, $3, $4, $5, 'active')`,
			sid, u, id, buuid.MustV7().String(), "c-"+sid)
		x.subs[n] = sid
	}
	x.ledger(u, p.cur, -amt, "purchase", "order", id, "purchase:"+id, at)
	x.event("order.paid.v1", map[string]any{"order_id": id, "user_id": u, "plan_id": p.id, "amount": amt, "currency": p.cur, "source": src}, at)
	pu := &mPurchase{order: id, user: u, plan: p.id, typ: typ, cur: p.cur, src: src, amt: amt, at: at}
	x.m.purchases = append(x.m.purchases, pu)
	return pu
}

func (x *seed) trial(n int, p *mPlan, at time.Time) {
	x.t.Helper()
	u := x.user(n)
	id := x.order(u, p, "new", "paid", 0, at, nil)
	x.ledger(u, p.cur, 0, "trial_grant", "order", id, "trial_grant:"+id, at)
	x.event("order.paid.v1", map[string]any{"order_id": id, "user_id": u, "amount": 0, "currency": p.cur, "source": "trial"}, at)
	x.m.trials = append(x.m.trials, at)
}

// topup writes a payment's credit; taken is what orders paid at the same instant
// take of it (0: all of it stays in the wallet).
func (x *seed) topup(n int, cur string, amt int64, at time.Time, taken int64) {
	x.t.Helper()
	id := buuid.MustV7().String()
	x.ledger(x.user(n), cur, amt, "topup", "intent", id, "pay-in:"+id, at)
	x.m.topups = append(x.m.topups, mFlow{cur: cur, amt: amt, at: at, taken: taken})
}

func (x *seed) refund(pu *mPurchase, at time.Time, cancel bool) {
	x.t.Helper()
	x.ledger(pu.user, pu.cur, pu.amt, "refund", "order", pu.order, "refund:"+pu.order, at)
	status := "active"
	if cancel {
		status = "cancelled"
	}
	x.exec(`UPDATE core.orders SET refunded_at = $2, refund_amount = $3, status = $4, updated_at = $2 WHERE id = $1`, pu.order, at, pu.amt, status)
	pu.refunded = pu.amt
	x.m.refunds = append(x.m.refunds, mFlow{cur: pu.cur, amt: pu.amt, at: at})
}

// rejected writes a payment.rejected.v1 event. The model tells who rejected it the way the
// docs do: a manual provider means staff, any other provider a gateway, and an event with
// no provider is a gateway's when its reason is one payments gives for a failed gateway
// payment (written out here on purpose: the model must not borrow the code's own list).
func (x *seed) rejected(provider, reason string, n int, cur string, amt int64, at time.Time) {
	x.t.Helper()
	p := map[string]any{"user_id": x.user(n), "intent_id": buuid.MustV7().String(), "amount": amt, "currency": cur, "reason": reason}
	if provider != "" {
		p["provider"] = provider
	}
	x.event("payment.rejected.v1", p, at)
	staff := provider == "manual_card" || provider == "manual_crypto" || provider == "manual_zarinpal"
	if provider == "" {
		staff = true
		for _, r := range []string{"gateway_failed", "amount_mismatch", "expired", "not_found", "reversed", "payer_mismatch"} {
			if reason == r {
				staff = false
			}
		}
	}
	if staff {
		x.m.rejected = append(x.m.rejected, mFlow{cur: cur, amt: amt, at: at})
	} else {
		x.m.gateway = append(x.m.gateway, mFlow{cur: cur, amt: amt, at: at})
	}
}

// unpaid writes an order that ended without being paid (or is still open).
func (x *seed) unpaid(n int, p *mPlan, status string, amt int64, at time.Time) {
	x.t.Helper()
	x.order(x.user(n), p, "new", status, amt, at, nil)
	if status == "cancelled" || status == "expired" {
		x.m.cancelled = append(x.m.cancelled, mFlow{cur: p.cur, amt: amt, at: at})
	}
}

// ---- the expected numbers, from the model ----

func utc(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

// bucketKey is the local start date of the bucket an instant falls in.
func bucketKey(at time.Time, loc *time.Location, group string) string {
	l := at.In(loc)
	d := time.Date(l.Year(), l.Month(), l.Day(), 12, 0, 0, 0, time.UTC) // noon: no edge to cross
	switch group {
	case "week":
		for d.Weekday() != time.Monday {
			d = d.AddDate(0, 0, -1)
		}
	case "month":
		d = time.Date(d.Year(), d.Month(), 1, 12, 0, 0, 0, time.UTC)
	}
	return d.Format("2006-01-02")
}

// inRange says whether an instant's local date is in [from, to]: the model of a range is
// its dates (comparing the dates as text), never instants.
func inRange(at time.Time, loc *time.Location, from, to string) bool {
	d := at.In(loc).Format("2006-01-02")
	return d >= from && d <= to
}

type numbers struct {
	cells     map[string]ReportCell // "bucket|currency"
	customers map[string]int64
	trials    map[string]int64
}

func (x *seed) expect(tz, from, to, group string) numbers {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		x.t.Fatal(err)
	}
	out := numbers{cells: map[string]ReportCell{}, customers: map[string]int64{}, trials: map[string]int64{}}
	cell := func(at time.Time, cur string) (string, ReportCell) {
		k := bucketKey(at, loc, group) + "|" + cur
		return k, out.cells[k]
	}
	first := map[string]*mPurchase{}
	for _, p := range x.m.purchases {
		if f := first[p.user]; f == nil || p.at.Before(f.at) {
			first[p.user] = p
		}
		if !inRange(p.at, loc, from, to) {
			continue
		}
		k, c := cell(p.at, p.cur)
		c.Revenue += p.amt
		c.Orders++
		switch p.typ {
		case "new":
			c.NewOrders++
		case "renew":
			c.Renewals++
		case "traffic_topup":
			c.TrafficTopups++
		}
		out.cells[k] = c
	}
	for _, p := range first {
		if inRange(p.at, loc, from, to) {
			out.customers[bucketKey(p.at, loc, group)]++
		}
	}
	for _, at := range x.m.trials {
		if inRange(at, loc, from, to) {
			out.trials[bucketKey(at, loc, group)]++
		}
	}
	flows := func(list []mFlow, into func(*ReportCell) *ReportFlow) {
		for _, f := range list {
			rest := f.amt - f.taken
			if rest <= 0 || !inRange(f.at, loc, from, to) {
				continue
			}
			k, c := cell(f.at, f.cur)
			fl := into(&c)
			fl.Count++
			fl.Amount += rest
			out.cells[k] = c
		}
	}
	flows(x.m.topups, func(c *ReportCell) *ReportFlow { return &c.Topups })
	flows(x.m.refunds, func(c *ReportCell) *ReportFlow { return &c.Refunds })
	flows(x.m.rejected, func(c *ReportCell) *ReportFlow { return &c.Rejected })
	flows(x.m.gateway, func(c *ReportCell) *ReportFlow { return &c.GatewayFailed })
	flows(x.m.cancelled, func(c *ReportCell) *ReportFlow { return &c.Cancelled })
	return out
}

func (d *SalesData) flat() numbers {
	out := numbers{cells: map[string]ReportCell{}, customers: map[string]int64{}, trials: map[string]int64{}}
	for b, cur := range d.Cells {
		for c, cell := range cur {
			if *cell != (ReportCell{}) {
				out.cells[b+"|"+c] = *cell
			}
		}
	}
	for b, n := range d.NewCustomers {
		out.customers[b] = n
	}
	for b, n := range d.Trials {
		out.trials[b] = n
	}
	return out
}

// reportScenario seeds a store whose events sit on both sides of local
// midnight in Asia/Tehran (UTC+3:30) and America/New_York (UTC-4, then -5
// from 1 November 2026, when daylight saving ends).
func reportScenario(t *testing.T) (*seed, map[string]*mPlan) {
	x := newSeed(t)
	pl := map[string]*mPlan{
		"basic": x.plan("پایه", "Basic", "IRT", false),
		"pro":   x.plan("ویژه ⭐", "Pro", "IRT", false),
		"gb":    x.plan("بسته ترافیک", "Traffic pack", "IRT", false),
		"usdt":  x.plan("ویژه تتر", "Pro USDT", "USDT", false),
		"trial": x.plan("آزمایشی", "Trial", "IRT", true),
	}
	// Before every range: user 1's first purchase, so a later one is no new customer.
	x.paid(1, pl["basic"], "new", 100_000, utc("2026-09-20T10:00:00Z"), "gateway")
	// Tehran 30 Oct 00:30 (New York: 29 Oct).
	x.paid(7, pl["basic"], "new", 100_000, utc("2026-10-29T21:00:00Z"), "gateway")
	x.paid(1, pl["basic"], "renew", 100_000, utc("2026-10-30T10:00:00Z"), "gateway")
	x.topup(3, "IRT", 150_000, utc("2026-10-30T09:00:00Z"), 0)
	x.rejected("manual_card", "blurred", 2, "IRT", 250_000, utc("2026-10-31T10:00:00Z"))
	x.rejected("zarinpal", "expired", 3, "IRT", 90_000, utc("2026-10-31T11:00:00Z"))      // a gateway
	x.rejected("manual_crypto", "expired", 3, "IRT", 40_000, utc("2026-10-31T11:00:00Z")) // staff, in gateway words
	x.rejected("", "expired", 4, "USDT", 1_000_000, utc("2026-11-01T11:00:00Z"))          // before the provider was passed on: a gateway's reason
	x.rejected("", "no clear photo", 4, "USDT", 2_000_000, utc("2026-11-01T11:00:00Z"))   // before the provider was passed on: staff's words
	// Tehran 23:59:59 on 31 Oct, and its pay-in, taken for the order at once.
	x.topup(2, "IRT", 250_000, utc("2026-10-31T20:29:59Z"), 250_000)
	x.paid(2, pl["pro"], "new", 250_000, utc("2026-10-31T20:29:59Z"), "manual")
	// Tehran 00:00:00 on 1 Nov, paid from the wallet.
	x.paid(3, pl["basic"], "new", 100_000, utc("2026-10-31T20:30:00Z"), "wallet")
	x.trial(8, pl["trial"], utc("2026-10-31T20:30:00Z"))
	x.unpaid(7, pl["basic"], "cancelled", 100_000, utc("2026-10-31T21:00:00Z"))
	x.unpaid(7, pl["pro"], "awaiting_payment", 250_000, utc("2026-10-31T21:00:00Z"))
	// New York 1 Nov 00:00 (EDT).
	x.paid(1, pl["basic"], "renew", 100_000, utc("2026-11-01T04:00:00Z"), "gateway")
	// A purchase in USDT with a top-up in IRT at the same instant: the
	// currencies differ, so the top-up stayed in the wallet.
	x.paid(2, pl["usdt"], "renew", 1_000_000, utc("2026-11-01T10:00:00Z"), "wallet")
	x.topup(2, "IRT", 70_000, utc("2026-11-01T10:00:00Z"), 0)
	x.refund(x.m.purchases[2], utc("2026-11-02T10:00:00Z"), false)
	x.unpaid(8, pl["pro"], "expired", 250_000, utc("2026-11-02T12:00:00Z"))
	// New York 23:59:59 on 1 Nov (EST, a 25-hour day), then 2 Nov 00:00.
	x.topup(4, "USDT", 2_500_000, utc("2026-11-02T04:59:59Z"), 2_500_000)
	x.paid(4, pl["usdt"], "new", 2_500_000, utc("2026-11-02T04:59:59Z"), "manual")
	x.paid(2, pl["gb"], "traffic_topup", 30_000, utc("2026-11-02T05:00:00Z"), "wallet")
	x.topup(4, "USDT", 1_000_000, utc("2026-11-02T05:00:00Z"), 0)
	x.paid(5, pl["pro"], "new", 250_000, utc("2026-11-03T12:00:00Z"), "gateway")
	x.refund(x.m.purchases[len(x.m.purchases)-1], utc("2026-11-03T12:30:00Z"), true)
	// One user, two purchases at the same instant: one new customer.
	x.paid(6, pl["basic"], "new", 100_000, utc("2026-11-03T13:00:00Z"), "wallet")
	x.paid(6, pl["usdt"], "new", 3_000_000, utc("2026-11-03T13:00:00Z"), "wallet")
	// The user paid 130000 for a 100000 order: the order took its price, 30000 stayed.
	x.topup(6, "IRT", 130_000, utc("2026-11-03T13:00:00Z"), 100_000)
	x.trial(9, pl["trial"], utc("2026-11-03T22:00:00Z"))
	// Tehran 4 Nov 03:30, New York 3 Nov 19:00.
	x.paid(5, pl["pro"], "renew", 250_000, utc("2026-11-04T00:00:00Z"), "manual")
	// a second after the user's purchase, so it was not taken for it
	x.topup(5, "IRT", 400_000, utc("2026-11-04T00:00:01Z"), 0)
	x.rejected("stars", "payer_mismatch", 4, "USDT", 2_500_000, utc("2026-11-04T00:00:00Z"))
	return x, pl
}

func TestSalesSummaryMatchesTheModel(t *testing.T) {
	x, _ := reportScenario(t)
	for _, tz := range []string{"Asia/Tehran", "America/New_York", "UTC"} {
		for _, rg := range [][2]string{{"2026-10-30", "2026-11-03"}, {"2026-10-01", "2026-11-30"}, {"2026-11-04", "2026-11-04"}} {
			for _, group := range []string{"day", "week", "month"} {
				t.Run(tz+" "+rg[0]+" to "+rg[1]+" by "+group, func(t *testing.T) {
					got, err := x.s.SalesSummary(x.ctx, ReportQuery{From: rg[0], To: rg[1], TZ: tz, Group: group})
					if err != nil {
						t.Fatal(err)
					}
					want := x.expect(tz, rg[0], rg[1], group)
					if g := got.flat(); !reflect.DeepEqual(g, want) {
						t.Errorf("numbers differ\n got: %+v\nwant: %+v", g, want)
					}
				})
			}
		}
	}
}

// The edges: 23:59:59 and 00:00:00 local time fall on different days, and the
// same instants fall on other days in another zone.
func TestSalesSummaryDayEdges(t *testing.T) {
	x, _ := reportScenario(t)
	day := func(tz, from, to string) map[string]map[string]*ReportCell {
		t.Helper()
		got, err := x.s.SalesSummary(x.ctx, ReportQuery{From: from, To: to, TZ: tz, Group: "day"})
		if err != nil {
			t.Fatal(err)
		}
		return got.Cells
	}
	tehran := day("Asia/Tehran", "2026-10-30", "2026-11-03")
	if got := tehran["2026-10-31"]["IRT"].Revenue; got != 250_000 { // the 23:59:59 purchase only
		t.Errorf("Tehran 31 Oct = %d, want 250000", got)
	}
	if got := tehran["2026-11-01"]["IRT"].Revenue; got != 100_000+100_000 { // 00:00:00 (wallet) and 07:30 (renewal)
		t.Errorf("Tehran 1 Nov = %d, want 200000", got)
	}
	if tehran["2026-11-04"] != nil {
		t.Error("Tehran's range ends on 3 Nov, but 4 Nov is in it")
	}
	ny := day("America/New_York", "2026-10-30", "2026-11-03")
	if got := ny["2026-10-31"]["IRT"].Revenue; got != 250_000+100_000 { // 16:29:59 and 16:30:00 on the 31st
		t.Errorf("New York 31 Oct = %d, want 350000", got)
	}
	// 1 Nov is 25 hours long: 04:00Z to 05:00Z the next day.
	if got := ny["2026-11-01"]["USDT"].Revenue; got != 1_000_000+2_500_000 {
		t.Errorf("New York 1 Nov USDT = %d, want 3500000", got)
	}
	if got := ny["2026-11-03"]["IRT"].Revenue; got != 250_000+100_000+250_000 { // 12:00Z, 13:00Z, and 4 Nov 00:00Z = 19:00 on the 3rd
		t.Errorf("New York 3 Nov IRT = %d, want 600000", got)
	}
	if _, ok := ny["2026-10-29"]; ok {
		t.Error("29 Oct is before New York's range")
	}
}

// The numbers add up to what the ledger holds, summed by SQL on its own, and
// the currencies stay apart.
func TestSalesSummaryEqualsTheLedger(t *testing.T) {
	x, _ := reportScenario(t)
	got, err := x.s.SalesSummary(x.ctx, ReportQuery{From: "2026-10-01", To: "2026-11-30", TZ: "Asia/Tehran", Group: "week"})
	if err != nil {
		t.Fatal(err)
	}
	sum := map[string]ReportCell{}
	for _, cur := range got.Cells {
		for c, cell := range cur {
			s := sum[c]
			s.Revenue += cell.Revenue
			s.Orders += cell.Orders
			s.Refunds.Amount += cell.Refunds.Amount
			s.Refunds.Count += cell.Refunds.Count
			sum[c] = s
		}
	}
	rows, err := x.s.DB().Query(x.ctx, `
		SELECT currency, COALESCE(-sum(amount) FILTER (WHERE kind = 'purchase'), 0), count(*) FILTER (WHERE kind = 'purchase'),
			COALESCE(sum(amount) FILTER (WHERE kind = 'refund'), 0), count(*) FILTER (WHERE kind = 'refund')
		FROM core.ledger_entries
		WHERE created_at >= '2026-09-30T20:30:00Z' AND created_at < '2026-11-30T20:30:00Z'
		GROUP BY currency ORDER BY currency`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var cur string
		var rev, orders, ref, refN int64
		if err := rows.Scan(&cur, &rev, &orders, &ref, &refN); err != nil {
			t.Fatal(err)
		}
		seen++
		if s := sum[cur]; s.Revenue != rev || s.Orders != orders || s.Refunds.Amount != ref || s.Refunds.Count != refN {
			t.Errorf("%s: report %+v, ledger revenue %d orders %d refunds %d (%d)", cur, s, rev, orders, ref, refN)
		}
	}
	if seen != 2 || len(sum) != 2 {
		t.Errorf("currencies: ledger %d, report %d, want IRT and USDT only", seen, len(sum))
	}
	// Fixed figures, summed by hand from the scenario (the purchase of 20 Sep is before the range).
	if s := sum["IRT"]; s.Revenue != 1_280_000 || s.Orders != 9 || s.Refunds.Amount != 350_000 || s.Refunds.Count != 2 {
		t.Errorf("IRT revenue %d over %d orders", s.Revenue, s.Orders)
	}
	if s := sum["USDT"]; s.Revenue != 1_000_000+2_500_000+3_000_000 || s.Orders != 3 {
		t.Errorf("USDT revenue %d over %d orders", s.Revenue, s.Orders)
	}
}

func TestSalesSummaryCountsPeopleAndTopups(t *testing.T) {
	x, _ := reportScenario(t)
	got, err := x.s.SalesSummary(x.ctx, ReportQuery{From: "2026-10-30", To: "2026-11-03", TZ: "Asia/Tehran", Group: "month"})
	if err != nil {
		t.Fatal(err)
	}
	// User 1 bought before the range; users 2, 3, 4, 5, 6 and 7 bought first
	// in it (7 on 30 Oct local time); user 6 bought twice at one instant.
	if got.NewCustomers["2026-10-01"]+got.NewCustomers["2026-11-01"] != 6 {
		t.Errorf("new customers %v, want 6", got.NewCustomers)
	}
	if got.Trials["2026-10-01"]+got.Trials["2026-11-01"] != 1 { // user 9's trial is 4 Nov in Tehran
		t.Errorf("trials %v, want 1", got.Trials)
	}
	// Top-ups that stayed: 150000 (30 Oct), 70000 (the IRT one beside a USDT purchase) and
	// 30000 (what was left of 130000 after a 100000 order); and USDT 1000000.
	var irt, usdt ReportFlow
	for _, cur := range got.Cells {
		if c := cur["IRT"]; c != nil {
			irt.Count += c.Topups.Count
			irt.Amount += c.Topups.Amount
		}
		if c := cur["USDT"]; c != nil {
			usdt.Count += c.Topups.Count
			usdt.Amount += c.Topups.Amount
		}
	}
	if irt != (ReportFlow{3, 250_000}) || usdt != (ReportFlow{1, 1_000_000}) {
		t.Errorf("top-ups IRT %+v USDT %+v", irt, usdt)
	}
}

func TestSalesByMethodAddsUpToRevenue(t *testing.T) {
	x, _ := reportScenario(t)
	q := ReportQuery{From: "2026-10-01", To: "2026-11-30", TZ: "Asia/Tehran"}
	rows, err := x.s.SalesByMethod(x.ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]MethodRow{}
	for _, p := range x.m.purchases {
		k := p.src + "|" + p.cur
		m := want[k]
		m.Method, m.Currency = p.src, p.cur
		m.Orders++
		m.Revenue += p.amt
		want[k] = m
	}
	got := map[string]MethodRow{}
	for _, r := range rows {
		got[r.Method+"|"+r.Currency] = r
	}
	// the purchase of 2026-09-20 is before the range
	m := want["gateway|IRT"]
	m.Orders--
	m.Revenue -= 100_000
	want["gateway|IRT"] = m
	if !reflect.DeepEqual(got, want) {
		t.Errorf("by method\n got: %+v\nwant: %+v", got, want)
	}
	// A purchase whose event is missing is listed as "unknown", and the rows still add up.
	x.exec(`DELETE FROM core.outbox_core WHERE topic = 'order.paid.v1' AND (payload->>'order_id') = $1`, x.m.purchases[3].order)
	rows, err = x.s.SalesByMethod(x.ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	var total, unknown int64
	for _, r := range rows {
		if r.Currency == "IRT" {
			total += r.Revenue
			if r.Method == "unknown" {
				unknown = r.Revenue
			}
		}
	}
	if unknown != 250_000 || total != 1_280_000 {
		t.Errorf("unknown %d, IRT total %d", unknown, total)
	}
}

func TestTopPlans(t *testing.T) {
	x, pl := reportScenario(t)
	rows, err := x.s.TopPlans(x.ctx, ReportQuery{From: "2026-10-01", To: "2026-11-30", TZ: "Asia/Tehran"}, 20)
	if err != nil {
		t.Fatal(err)
	}
	type k struct {
		plan, cur string
		orders    int64
		revenue   int64
	}
	var got []k
	for _, r := range rows {
		got = append(got, k{r.PlanID, r.Currency, r.Orders, r.Revenue})
	}
	want := []k{
		{pl["pro"].id, "IRT", 3, 750_000},
		{pl["basic"].id, "IRT", 5, 500_000},
		{pl["gb"].id, "IRT", 1, 30_000},
		{pl["usdt"].id, "USDT", 3, 6_500_000},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("top plans\n got: %+v\nwant: %+v", got, want)
	}
	if rows[0].Name["fa"] != "ویژه ⭐" || rows[0].Name["en"] != "Pro" {
		t.Errorf("names: %v", rows[0].Name)
	}
	// A limit keeps the best of each currency.
	one, err := x.s.TopPlans(x.ctx, ReportQuery{From: "2026-10-01", To: "2026-11-30", TZ: "Asia/Tehran"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(one) != 2 || one[0].PlanID != pl["pro"].id || one[1].PlanID != pl["usdt"].id {
		t.Errorf("limit 1: %+v", one)
	}
}

func TestEachPaidOrderStreamsInOrder(t *testing.T) {
	x, pl := reportScenario(t)
	q := ReportQuery{From: "2026-10-30", To: "2026-11-03", TZ: "Asia/Tehran"}
	var got []PaidOrderRow
	more, err := x.s.EachPaidOrder(x.ctx, q, 100, func(r PaidOrderRow) error { got = append(got, r); return nil })
	if err != nil || more {
		t.Fatalf("err %v more %v", err, more)
	}
	var want []*mPurchase
	for _, p := range x.m.purchases {
		if inRange(p.at, mustLoc(t, "Asia/Tehran"), q.From, q.To) {
			want = append(want, p)
		}
	}
	sort.SliceStable(want, func(i, j int) bool { return want[i].at.Before(want[j].at) })
	if len(got) != len(want) {
		t.Fatalf("%d orders, want %d", len(got), len(want))
	}
	for i, p := range want {
		g := got[i]
		if g.OrderID != p.order || g.Amount != p.amt || g.Currency != p.cur || g.Type != p.typ || g.Method != p.src || !g.PaidAt.Equal(p.at) || g.Refunded != p.refunded {
			t.Errorf("row %d: %+v, want %+v", i, g, p)
		}
	}
	if got[2].PaidLocal != "2026-10-31 23:59:59" || got[3].PaidLocal != "2026-11-01 00:00:00" {
		t.Errorf("local times %q %q", got[2].PaidLocal, got[3].PaidLocal)
	}
	if got[0].PlanName["fa"] != "پایه" || got[0].Username == "" || got[0].TelegramID < 9000 {
		t.Errorf("first row: %+v", got[0])
	}
	_ = pl
	// A cap stops the stream and says there was more.
	n := 0
	more, err = x.s.EachPaidOrder(x.ctx, q, 3, func(PaidOrderRow) error { n++; return nil })
	if err != nil || !more || n != 3 {
		t.Errorf("cap: n %d more %v err %v", n, more, err)
	}
	// An error from the callback ends the stream and comes back.
	stop := errors.New("stop")
	if _, err := x.s.EachPaidOrder(x.ctx, q, 100, func(PaidOrderRow) error { return stop }); !errors.Is(err, stop) {
		t.Errorf("callback error: %v", err)
	}
}

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	l, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestReportsRejectAnUnknownTimezone(t *testing.T) {
	x := newSeed(t)
	if _, err := x.s.SalesSummary(x.ctx, ReportQuery{From: "2026-10-01", To: "2026-10-02", TZ: "Mars/Base", Group: "day"}); !errors.Is(err, ErrBadTimezone) {
		t.Errorf("summary: %v", err)
	}
	if _, err := x.s.EachPaidOrder(x.ctx, ReportQuery{From: "2026-10-01", To: "2026-10-02", TZ: "Mars/Base"}, 10, func(PaidOrderRow) error { return nil }); !errors.Is(err, ErrBadTimezone) {
		t.Errorf("export: %v", err)
	}
}

// A report cannot write, and its statements are time-limited.
func TestReportTransactionIsReadOnlyAndTimed(t *testing.T) {
	x := newSeed(t)
	err := x.s.readOnly(x.ctx, ReportTimeout, func(tx pgx.Tx) error {
		_, err := tx.Exec(x.ctx, `INSERT INTO core.users (id, telegram_id) VALUES ($1, 1)`, buuid.MustV7().String())
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Errorf("a report wrote: %v", err)
	}
	err = x.s.readOnly(x.ctx, 50*time.Millisecond, func(tx pgx.Tx) error {
		_, err := tx.Exec(x.ctx, `SELECT pg_sleep(2)`)
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "statement timeout") {
		t.Errorf("a slow statement ran on: %v", err)
	}
}

// America/Havana ends daylight saving on 1 Nov 2026 at 01:00, so the clocks
// show 00:xx twice that morning. A range is its local dates: the first 00:30
// and the second are both 1 Nov, and neither belongs to a range ending 31 Oct.
func TestSalesSummaryOnAnAmbiguousMidnight(t *testing.T) {
	x := newSeed(t)
	pl := x.plan("پایه", "Basic", "IRT", false)
	for i, at := range []string{
		"2026-11-01T03:30:00Z", // 31 Oct 23:30 (CDT)
		"2026-11-01T04:30:00Z", // 1 Nov 00:30, the first time
		"2026-11-01T05:30:00Z", // 1 Nov 00:30, the second time (CST)
		"2026-11-02T04:59:00Z", // 1 Nov 23:59 (CST)
		"2026-11-02T05:00:00Z", // 2 Nov 00:00
	} {
		x.paid(10+i, pl, "new", 1000*int64(i+1), utc(at), "wallet")
	}
	sum := func(from, to, group string) map[string]int64 {
		t.Helper()
		got, err := x.s.SalesSummary(x.ctx, ReportQuery{From: from, To: to, TZ: "America/Havana", Group: group})
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]int64{}
		for b, cur := range got.Cells {
			for _, c := range cur {
				out[b] += c.Revenue
			}
		}
		return out
	}
	for _, c := range []struct {
		from, to string
		want     map[string]int64
	}{
		{"2026-10-31", "2026-10-31", map[string]int64{"2026-10-31": 1000}}, // not the 00:30s: they are 1 Nov
		{"2026-11-01", "2026-11-01", map[string]int64{"2026-11-01": 2000 + 3000 + 4000}},
		{"2026-11-02", "2026-11-02", map[string]int64{"2026-11-02": 5000}},
		{"2026-10-31", "2026-11-02", map[string]int64{"2026-10-31": 1000, "2026-11-01": 9000, "2026-11-02": 5000}},
	} {
		if got := sum(c.from, c.to, "day"); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s to %s: %v, want %v", c.from, c.to, got, c.want)
		}
	}
	// A week or a month holds those hours too, whichever end of the range cuts it.
	if got := sum("2026-11-01", "2026-11-01", "month"); !reflect.DeepEqual(got, map[string]int64{"2026-11-01": 9000}) {
		t.Errorf("month: %v", got)
	}
	// And the model agrees on every range and grouping.
	for _, rg := range [][2]string{{"2026-10-31", "2026-10-31"}, {"2026-11-01", "2026-11-01"}, {"2026-11-01", "2026-11-02"}, {"2026-10-25", "2026-11-05"}} {
		for _, group := range []string{"day", "week", "month"} {
			got, err := x.s.SalesSummary(x.ctx, ReportQuery{From: rg[0], To: rg[1], TZ: "America/Havana", Group: group})
			if err != nil {
				t.Fatal(err)
			}
			if want := x.expect("America/Havana", rg[0], rg[1], group); !reflect.DeepEqual(got.flat(), want) {
				t.Errorf("%v by %s\n got: %+v\nwant: %+v", rg, group, got.flat(), want)
			}
		}
	}
	// The export and the top plans read the same dates.
	var orders int
	if _, err := x.s.EachPaidOrder(x.ctx, ReportQuery{From: "2026-11-01", To: "2026-11-01", TZ: "America/Havana"}, 100, func(PaidOrderRow) error { orders++; return nil }); err != nil || orders != 3 {
		t.Errorf("export: %d orders %v", orders, err)
	}
	if rows, err := x.s.TopPlans(x.ctx, ReportQuery{From: "2026-10-31", To: "2026-10-31", TZ: "America/Havana"}, 5); err != nil || len(rows) != 1 || rows[0].Orders != 1 {
		t.Errorf("plans: %+v %v", rows, err)
	}
	// New York's 25-hour day (1 Nov 2026) is still one day.
	ny, err := x.s.SalesSummary(x.ctx, ReportQuery{From: "2026-11-01", To: "2026-11-01", TZ: "America/New_York", Group: "day"})
	if err != nil {
		t.Fatal(err)
	}
	if want := x.expect("America/New_York", "2026-11-01", "2026-11-01", "day"); !reflect.DeepEqual(ny.flat(), want) {
		t.Errorf("New York\n got: %+v\nwant: %+v", ny.flat(), want)
	}
}

// The orders export finds how each order was paid through an index, one probe
// per order: at any volume, not only a small one. A plan that scanned the outbox
// for each order (or joined it whole) took minutes at 100,000 orders. The test
// seeds a modest volume, asks the planner (default settings) for its plan and
// checks the index is the way in, then checks the export is quick.
func TestOrdersExportFindsHowOrdersWerePaidThroughAnIndex(t *testing.T) {
	x := newSeed(t)
	const n = 6000
	x.exec(`INSERT INTO core.users (id, telegram_id, username) SELECT md5('u'||n)::uuid, 100000+n, 'u'||n FROM generate_series(1,200) n`)
	x.exec(`INSERT INTO core.plans (id, name_i18n, kind, price, currency) VALUES (md5('p')::uuid, '{"fa":"a","en":"b"}', 'both', 1000, 'IRT')`)
	x.exec(`INSERT INTO core.orders (id, user_id, plan_id, type, status, amount, currency, idempotency_key, created_at, updated_at)
		SELECT md5('o'||n)::uuid, md5('u'||(1+n%200))::uuid, md5('p')::uuid, 'new', 'active', 1000, 'IRT', 'k'||n,
			timestamptz '2026-10-01' + n * interval '2 minutes', timestamptz '2026-10-01' + n * interval '2 minutes'
		FROM generate_series(1,$1::int) n`, n)
	x.exec(`INSERT INTO core.ledger_entries (id, user_id, currency, amount, kind, ref_type, ref_id, idempotency_key, balance_after, created_at)
		SELECT md5('l'||n)::uuid, md5('u'||(1+n%200))::uuid, 'IRT', -1000, 'purchase', 'order', md5('o'||n)::uuid, 'purchase:'||n, 0,
			timestamptz '2026-10-01' + n * interval '2 minutes'
		FROM generate_series(1,$1::int) n`, n)
	x.exec(`INSERT INTO core.outbox_core (topic, payload, event_id, created_at)
		SELECT 'order.paid.v1', jsonb_build_object('order_id', md5('o'||n)::uuid::text, 'amount', 1000, 'currency', 'IRT', 'source', 'wallet'),
			md5('e'||n)::uuid, timestamptz '2026-10-01' + n * interval '2 minutes'
		FROM generate_series(1,$1::int) n`, n)
	x.exec(`INSERT INTO core.outbox_core (topic, payload, event_id, created_at)
		SELECT 'wallet.credited.v1', jsonb_build_object('order_id', md5('o'||(1+n%$1::int))::uuid::text, 'text', repeat('x', 60)),
			md5('n'||n)::uuid, timestamptz '2026-09-01' + n * interval '10 seconds'
		FROM generate_series(1,$2::int) n`, n, 4*n)
	x.exec(`ANALYZE`)

	rows, err := x.s.DB().Query(x.ctx, `EXPLAIN `+paidOrdersSQL(), "2026-09-01", "2026-12-31", "Asia/Tehran", 100001)
	if err != nil {
		t.Fatal(err)
	}
	var plan strings.Builder
	for rows.Next() {
		var l string
		if err := rows.Scan(&l); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(l + "\n")
	}
	rows.Close()
	if !strings.Contains(plan.String(), "outbox_core_order_paid_idx") || strings.Contains(plan.String(), "Materialize") && strings.Contains(plan.String(), "outbox_core x") {
		t.Fatalf("the export's plan does not look orders up by their id in the index:\n%s", plan.String())
	}

	start := time.Now()
	got := 0
	wallet := 0
	more, err := x.s.EachPaidOrder(x.ctx, ReportQuery{From: "2026-09-01", To: "2026-12-31", TZ: "Asia/Tehran"}, 100000, func(o PaidOrderRow) error {
		got++
		if o.Method == "wallet" {
			wallet++
		}
		return nil
	})
	if err != nil || more || got != n || wallet != n {
		t.Fatalf("export: %d rows (%d wallet), more %v, %v", got, wallet, more, err)
	}
	if took := time.Since(start); took > 10*time.Second { // about half a second here: the old plan needed minutes at ten times the rows
		t.Errorf("%d orders took %s", n, took)
	}
	// A cap stops it at once, with more to say.
	got = 0
	if more, err := x.s.EachPaidOrder(x.ctx, ReportQuery{From: "2026-09-01", To: "2026-12-31", TZ: "Asia/Tehran"}, 50, func(PaidOrderRow) error { got++; return nil }); err != nil || !more || got != 50 {
		t.Errorf("cap: %d %v %v", got, more, err)
	}
}

// Payments' reasons for a failed payment, and its manual providers, are
// written out in ManualProviders and GatewayFailureReasons. This reads payments'
// sources: a new reason, or a new manual provider, fails here until someone
// decides which line of the report it belongs on.
func TestRejectionListsMatchWhatPaymentsEmits(t *testing.T) {
	read := func(patterns ...string) map[string]string {
		t.Helper()
		out := map[string]string{}
		for _, pat := range patterns {
			files, err := filepath.Glob(pat)
			if err != nil {
				t.Fatal(err)
			}
			for _, path := range files {
				if strings.HasSuffix(path, "_test.go") {
					continue
				}
				b, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				out[path] = string(b)
			}
		}
		if len(out) == 0 {
			t.Fatalf("no source matches %v", patterns)
		}
		return out
	}
	found := map[string]bool{}
	constant := regexp.MustCompile(`(?m)^\s*Reason[A-Za-z]*\s*=\s*"([a-z_]+)"`)
	literal := regexp.MustCompile(`Reason:\s*"([a-z_]+)"`)
	for path, src := range read("../../payments/domain/*.go") {
		for _, m := range constant.FindAllStringSubmatch(src, -1) {
			found[m[1]] = true
		}
		_ = path
	}
	for _, src := range read("../../payments/gateway/*.go", "../../payments/gateway/*/*.go") {
		for _, m := range literal.FindAllStringSubmatch(src, -1) {
			found[m[1]] = true
		}
	}
	listed := map[string]bool{}
	for _, r := range GatewayFailureReasons {
		listed[r] = true
	}
	for r := range found {
		if !listed[r] {
			t.Errorf("payments emits the reason %q: put it on a line of the report (GatewayFailureReasons in reports.go)", r)
		}
	}
	for r := range listed {
		if !found[r] {
			t.Errorf("GatewayFailureReasons names %q, which payments does not emit any more", r)
		}
	}
	manual := map[string]bool{}
	for _, src := range read("../../payments/provider/*.go") {
		for _, m := range regexp.MustCompile(`(?m)^\s*Manual[A-Za-z]*\s*=\s*"([a-z_]+)"`).FindAllStringSubmatch(src, -1) {
			manual[m[1]] = true
		}
	}
	if len(manual) != len(ManualProviders) {
		t.Errorf("payments has the manual providers %v, ManualProviders is %v", manual, ManualProviders)
	}
	for _, p := range ManualProviders {
		if !manual[p] {
			t.Errorf("ManualProviders names %q, which payments does not have", p)
		}
	}
}

// PostgreSQL's statement limit is its own error, so the dashboard can say "choose
// a shorter range" instead of "something went wrong".
func TestReportStatementTimeoutIsNamed(t *testing.T) {
	x := newSeed(t)
	err := x.s.readOnly(x.ctx, 50*time.Millisecond, func(tx pgx.Tx) error {
		_, err := tx.Exec(x.ctx, `SELECT pg_sleep(2)`)
		return reportErr("report", err)
	})
	if !errors.Is(err, ErrReportTimeout) {
		t.Errorf("a statement that ran out of time: %v", err)
	}
	if err := reportErr("report", errors.New("boom")); errors.Is(err, ErrReportTimeout) || errors.Is(err, ErrBadTimezone) {
		t.Errorf("another failure: %v", err)
	}
}
