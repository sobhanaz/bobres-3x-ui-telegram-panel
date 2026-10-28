package domain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
)

// targetsEnv is a store with an owner, a tier of resellers (10% off a plan of 1,000: 900 Toman) and targets switched on with two tiers: 2 services
// pay 1,000 Toman and 4 pay 5,000. The clock stands at the middle of October 2026 in Tehran.
type targetsEnv struct {
	*namingEnv
	fp *fakeProvisioner
	at time.Time
}

func newTargetsEnv(t *testing.T) *targetsEnv {
	t.Helper()
	e := &targetsEnv{namingEnv: newNamingEnv(t), fp: &fakeProvisioner{}, at: time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)}
	e.svc.cfg.Now = func() time.Time { return e.at }
	e.set(map[string]string{SettingTargetsEnabled: "true", SettingTargetsTiers: `[{"threshold":2,"bonus":1000},{"threshold":4,"bonus":5000}]`})
	return e
}

func (e *targetsEnv) set(values map[string]string) {
	e.t.Helper()
	if _, err := e.svc.SetSettings(context.Background(), e.owner, values); err != nil {
		e.t.Fatal(err)
	}
}

var targetKeys int

func nextKey() string { targetKeys++; return fmt.Sprintf("tg-%d", targetKeys) }

// order makes an order of plan for u and pays it from the wallet (a reseller's on credit), without delivering it.
func (e *targetsEnv) order(u *store.User, plan *store.Plan, typ, subscription string) *store.Order {
	e.t.Helper()
	ctx := context.Background()
	o, err := e.svc.CreateOrder(ctx, CreateOrderParams{UserID: u.ID, PlanID: plan.ID, Type: typ, SubscriptionID: subscription, IdempotencyKey: nextKey()})
	if err != nil {
		e.t.Fatal(err)
	}
	if o.Status != "paid" {
		if o, err = e.svc.PayOrderWithWallet(ctx, o.ID, u.ID); err != nil {
			e.t.Fatal(err)
		}
	}
	return o
}

func (e *targetsEnv) deliver() {
	e.t.Helper()
	if worked, err := e.svc.NewProvisionWorker(e.fp, nil).ProvisionNext(context.Background()); err != nil || !worked {
		e.t.Fatalf("provisioning: %v %v", worked, err)
	}
}

// sell has the reseller buy one new service and the worker deliver it.
func (e *targetsEnv) sell(u *store.User) *store.Order {
	e.t.Helper()
	o := e.order(u, e.plan, OrderNew, "")
	e.deliver()
	return o
}

func (e *targetsEnv) progress(u *store.User) *TargetProgress {
	e.t.Helper()
	p, err := e.svc.ResellerTargets(context.Background(), u.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	return p
}

func (e *targetsEnv) balance(u *store.User) int64 {
	e.t.Helper()
	w, err := e.s.GetWallet(context.Background(), e.s.Conn(), u.ID, "IRT")
	if err != nil {
		e.t.Fatal(err)
	}
	return w.Balance
}

func (e *targetsEnv) bonuses(u *store.User) []string {
	e.t.Helper()
	rows, err := e.s.DB().Query(context.Background(), `SELECT idempotency_key || '=' || amount FROM core.ledger_entries WHERE user_id = $1 AND kind = 'target_bonus' ORDER BY idempotency_key`, u.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

func (e *targetsEnv) consistent(u *store.User) {
	e.t.Helper()
	if bal, sum, _ := ledger(e.t, e.s, u.ID); bal != sum {
		e.t.Fatalf("the wallet holds %d but its ledger adds up to %d", bal, sum)
	}
}

func TestTargetTiersAreRead(t *testing.T) {
	good := map[string][]TargetTier{
		``:                                 nil,
		`null`:                             nil,
		`[]`:                               {},
		`[{"threshold":10,"bonus":50000}]`: {{10, 50000}},
		` [ { "bonus" : 70 , "threshold" : 5 } ] `:                         {{5, 70}},
		`[{"threshold":25,"bonus":150000},{"threshold":10,"bonus":50000}]`: {{10, 50000}, {25, 150000}},
	}
	for in, want := range good {
		got, err := ParseTargetTiers(in)
		if err != nil || len(got) != len(want) {
			t.Errorf("ParseTargetTiers(%q) = %v, %v; want %v", in, got, err, want)
			continue
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("ParseTargetTiers(%q)[%d] = %v, want %v", in, i, got[i], want[i])
			}
		}
	}
	many := make([]string, MaxTargetTiers+1)
	for i := range many {
		many[i] = fmt.Sprintf(`{"threshold":%d,"bonus":1}`, i+1)
	}
	for _, bad := range []string{
		`{}`, `{"threshold":1,"bonus":1}`, `[1]`, `["a"]`, `[{"threshold":"2","bonus":10}]`, `[{"threshold":1.5,"bonus":10}]`,
		`[{"threshold":0,"bonus":10}]`, `[{"threshold":-1,"bonus":10}]`, `[{"threshold":100001,"bonus":10}]`, `[{"threshold":1,"bonus":0}]`,
		`[{"threshold":1,"bonus":-5}]`, `[{"threshold":1,"bonus":100000000001}]`, `[{"threshold":1}]`, `[{"bonus":5}]`,
		`[{"threshold":1,"bonus":5,"extra":1}]`, `[{"threshold":2,"bonus":5},{"threshold":2,"bonus":9}]`, `[{"threshold":1,"bonus":5}] x`,
		`[{"threshold":1,"bonus":5}][{"threshold":2,"bonus":5}]`, `[{"threshold":1,"bonus":5}`, `nope`, `[` + strings.Join(many, ",") + `]`,
	} {
		if got, err := ParseTargetTiers(bad); err == nil {
			t.Errorf("ParseTargetTiers(%q) = %v: accepted", bad, got)
		}
	}
	// the stored text is the sorted, compact form
	if got := FormatTargetTiers([]TargetTier{{25, 150000}, {10, 50000}}); got != `[{"threshold":10,"bonus":50000},{"threshold":25,"bonus":150000}]` {
		t.Errorf("FormatTargetTiers: %s", got)
	}
	if FormatTargetTiers(nil) != "" {
		t.Error("an empty list is written as nothing")
	}
}

func TestTargetSettingsAreChecked(t *testing.T) {
	if got, err := NormalizeSetting(SettingTargetsTiers, ` [ {"bonus":1000, "threshold": 5}, {"threshold":2,"bonus":10} ] `); err != nil ||
		got != `[{"threshold":2,"bonus":10},{"threshold":5,"bonus":1000}]` {
		t.Errorf("tiers: %q %v", got, err)
	}
	if got, err := NormalizeSetting(SettingTargetsTiers, ``); err != nil || got != "" {
		t.Errorf("empty tiers: %q %v", got, err)
	}
	if got, err := NormalizeSetting(SettingTargetsTiers, `[]`); err != nil || got != "" {
		t.Errorf("an empty list: %q %v", got, err)
	}
	var fe *FieldError
	if _, err := NormalizeSetting(SettingTargetsTiers, `[{"threshold":0,"bonus":1}]`); !errors.As(err, &fe) || fe.Field != SettingTargetsTiers {
		t.Errorf("a tier of 0: %v", err)
	}
	if got, err := NormalizeSetting(SettingTargetsCalendar, "jalali"); err != nil || got != "jalali" {
		t.Errorf("calendar: %q %v", got, err)
	}
	if _, err := NormalizeSetting(SettingTargetsCalendar, "hijri"); err == nil {
		t.Error("a calendar the reports do not have")
	}
	if got, err := NormalizeSetting(SettingTargetsEnabled, "on"); err != nil || got != "true" {
		t.Errorf("switch: %q %v", got, err)
	}
	// Off by default, nothing to pay without tiers, and a value that is not valid never pays.
	if c := TargetsConfigOf(map[string]string{}); c.Enabled || c.On() || c.Calendar != CalendarGregorian {
		t.Errorf("defaults: %+v", c)
	}
	if c := TargetsConfigOf(map[string]string{SettingTargetsEnabled: "true"}); c.On() {
		t.Errorf("targets without tiers pay: %+v", c)
	}
	if c := TargetsConfigOf(map[string]string{SettingTargetsEnabled: "true", SettingTargetsTiers: "garbage"}); c.On() {
		t.Errorf("a bad list pays: %+v", c)
	}
}

// A month is read in the store's zone and calendar: the same instant is October in one zone and November in another, and a Jalali month starts on
// the 1st of its own month.
func TestTargetMonthFollowsTheZoneAndTheCalendar(t *testing.T) {
	tehran, _ := time.LoadLocation("Asia/Tehran")
	for _, c := range []struct {
		at       string
		loc      *time.Location
		calendar string
		label    string
		from, to string
	}{
		{"2026-10-31T20:29:00Z", tehran, CalendarGregorian, "2026-10", "2026-10-01", "2026-10-31"},
		{"2026-10-31T20:31:00Z", tehran, CalendarGregorian, "2026-11", "2026-11-01", "2026-11-30"},
		{"2026-10-31T20:31:00Z", time.UTC, CalendarGregorian, "2026-10", "2026-10-01", "2026-10-31"},
		{"2026-12-31T23:59:00Z", time.UTC, CalendarGregorian, "2026-12", "2026-12-01", "2026-12-31"},
		{"2026-02-10T00:00:00Z", time.UTC, CalendarGregorian, "2026-02", "2026-02-01", "2026-02-28"},
		// 22 Oct 2026 is 30 Mehr 1405, 23 Oct is 1 Aban
		{"2026-10-22T10:00:00Z", tehran, CalendarJalali, "1405-07", "2026-09-23", "2026-10-22"},
		{"2026-10-23T10:00:00Z", tehran, CalendarJalali, "1405-08", "2026-10-23", "2026-11-21"},
		{"2026-10-22T20:29:00Z", tehran, CalendarJalali, "1405-07", "2026-09-23", "2026-10-22"},
		{"2026-10-22T20:31:00Z", tehran, CalendarJalali, "1405-08", "2026-10-23", "2026-11-21"},
	} {
		at, _ := time.Parse(time.RFC3339, c.at)
		p := targetPeriodAt(at, c.loc, c.calendar)
		if p.Label != c.label || p.From != c.from || p.To != c.to {
			t.Errorf("%s in %s, %s: %s %s..%s; want %s %s..%s", c.at, c.loc, c.calendar, p.Label, p.From, p.To, c.label, c.from, c.to)
		}
		// the month ends at local midnight, after the last day
		end := p.EndsAt.In(c.loc)
		wantEnd, _ := time.ParseInLocation(dateLayout, p.To, c.loc)
		if !end.Equal(wantEnd.AddDate(0, 0, 1)) || !p.EndsAt.After(at) {
			t.Errorf("%s: the month ends at %v", c.at, end)
		}
	}
	// The service reads the zone from the settings, as the reports do.
	e := newTargetsEnv(t)
	e.at = time.Date(2026, 10, 31, 21, 0, 0, 0, time.UTC) // 00:30 on 1 Nov in Tehran
	if p := e.progress(e.reseller(9301)); p.Period != "2026-11" {
		t.Errorf("Tehran: %s", p.Period)
	}
	e.set(map[string]string{"general.timezone": "UTC"})
	if p := e.progress(e.reseller(9302)); p.Period != "2026-10" {
		t.Errorf("UTC: %s", p.Period)
	}
	e.set(map[string]string{SettingTargetsCalendar: "jalali"})
	if p := e.progress(e.reseller(9303)); p.Period != "1405-08" || p.Calendar != CalendarJalali {
		t.Errorf("Jalali: %+v", p)
	}
}

// The services a reseller sells in a month pay a tier's bonus when the count reaches it: once, in the transaction of the delivery that reached it, with
// a message, and the count starts again the next month.
func TestResellerIsPaidTheBonusOfEachTierOnce(t *testing.T) {
	e := newTargetsEnv(t)
	ctx := context.Background()
	r := e.reseller(9311)
	before := e.balance(r)

	e.sell(r)
	if p := e.progress(r); !p.On || p.Sold != 1 || p.Remaining != 1 || p.Next == nil || p.Next.Threshold != 2 || p.BonusPaid != 0 || len(e.bonuses(r)) != 0 {
		t.Fatalf("after one: %+v", p)
	}
	e.sell(r) // two services: the first tier
	p := e.progress(r)
	if p.Sold != 2 || p.Remaining != 2 || p.Next == nil || p.Next.Threshold != 4 || p.BonusPaid != 1000 || p.Period != "2026-10" {
		t.Fatalf("after two: %+v", p)
	}
	if !p.Tiers[0].Reached || !p.Tiers[0].Paid || p.Tiers[1].Reached || p.Tiers[1].Paid || p.Tiers[0].Bonus != 1000 || p.Tiers[1].Bonus != 5000 {
		t.Fatalf("tiers after two: %+v", p.Tiers)
	}
	if got := e.bonuses(r); len(got) != 1 || got[0] != fmt.Sprintf("target:%s:2026-10:2=1000", r.ID) {
		t.Fatalf("bonuses: %v", got)
	}
	e.sell(r)
	e.sell(r) // four: the second tier
	if p = e.progress(r); p.Sold != 4 || p.Next != nil || p.Remaining != 0 || p.BonusPaid != 6000 {
		t.Fatalf("after four: %+v", p)
	}
	e.sell(r) // five: nothing more this month
	if p = e.progress(r); p.Sold != 5 || p.BonusPaid != 6000 || len(e.bonuses(r)) != 2 {
		t.Fatalf("after five: %+v %v", p, e.bonuses(r))
	}
	// 5 services at 900 on credit, and 6,000 of bonus
	if got := e.balance(r) - before; got != 6000-5*900 {
		t.Fatalf("balance moved by %d", got)
	}
	e.consistent(r)

	// each bonus was told, with the count and the month
	var told []events.WalletCreditedEvent
	for _, raw := range outboxEvents(t, e.s, events.WalletCredited) {
		var ev events.WalletCreditedEvent
		if err := json.Unmarshal(raw, &ev); err != nil {
			t.Fatal(err)
		}
		if ev.Reason == events.CreditTargetBonus {
			told = append(told, ev)
		}
	}
	if len(told) != 2 || told[0].Amount != 1000 || told[0].TargetSales != 2 || told[0].TargetThreshold != 2 || told[0].TargetMonth != "2026-10" ||
		told[0].TelegramID != r.TelegramID || told[0].Currency != "IRT" || told[1].Amount != 5000 || told[1].TargetSales != 4 {
		t.Fatalf("events: %+v", told)
	}
	if told[1].Balance != told[0].Balance-900*2+5000 {
		t.Errorf("the balance in the message: %d then %d", told[0].Balance, told[1].Balance)
	}
	// the ledger entry names the reseller and the target, and the record says how it was reached
	var refType, kind string
	if err := e.s.DB().QueryRow(ctx, `SELECT ref_type, kind FROM core.ledger_entries WHERE idempotency_key = $1`, fmt.Sprintf("target:%s:2026-10:4", r.ID)).Scan(&refType, &kind); err != nil || refType != "user" || kind != "target_bonus" {
		t.Fatalf("the entry: %s %s %v", refType, kind, err)
	}
	bs, err := e.s.TargetBonuses(ctx, e.s.Conn(), r.ID, "2026-10")
	if err != nil || len(bs) != 2 || bs[0].Threshold != 2 || bs[0].Sales != 2 || bs[1].Threshold != 4 || bs[1].Sales != 4 || bs[1].Amount != 5000 {
		t.Fatalf("records: %+v %v", bs, err)
	}

	// The next month starts again from nothing, and pays again at the same tiers.
	e.at = time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	if p = e.progress(r); p.Period != "2026-11" || p.Sold != 0 || p.BonusPaid != 0 {
		t.Fatalf("a new month: %+v", p)
	}
	e.sell(r)
	e.sell(r)
	if got := e.bonuses(r); len(got) != 3 || got[2] != fmt.Sprintf("target:%s:2026-11:2=1000", r.ID) {
		t.Fatalf("bonuses after a second month: %v", got)
	}
	e.consistent(r)
}

// Only a new service a reseller bought counts: not a renewal or a traffic package, not a customer's purchase, not an order that cost nothing.
func TestOnlyNewServicesOfResellersCount(t *testing.T) {
	e := newTargetsEnv(t)
	ctx := context.Background()
	r := e.reseller(9321)
	customer := seedUser(t, e.s, 9322)
	e.fundNowFor(customer, 10_000)

	first := e.sell(r)
	sub, err := e.s.SubscriptionByOrder(ctx, e.s.Conn(), first.ID)
	if err != nil {
		t.Fatal(err)
	}
	// a renewal
	e.order(r, e.plan, OrderRenew, sub.ID)
	e.deliver()
	// a customer's own purchase
	e.order(customer, e.plan, OrderNew, "")
	e.deliver()
	if p := e.progress(r); p.Sold != 1 || len(e.bonuses(r)) != 0 {
		t.Fatalf("only the one new service counts: %+v", p)
	}
	var n int
	if err := e.s.DB().QueryRow(ctx, `SELECT count(*) FROM core.reseller_sales WHERE user_id = $1`, customer.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("a customer's purchase was counted: %d %v", n, err)
	}

	// which orders count, by the rule itself
	trial := *e.plan
	trial.IsTrial = true
	topup := *e.plan
	topup.IsTopup = true
	banned := *r
	banned.Status = "banned"
	refunded := time.Now()
	for name, tc := range map[string]struct {
		o     store.Order
		plan  *store.Plan
		buyer *store.User
		want  bool
	}{
		"a new service":     {store.Order{Type: "new", Amount: 900}, e.plan, r, true},
		"a renewal":         {store.Order{Type: "renew", Amount: 900}, e.plan, r, false},
		"a traffic package": {store.Order{Type: "traffic_topup", Amount: 900}, e.plan, r, false},
		"a free order":      {store.Order{Type: "new", Amount: 0}, e.plan, r, false},
		"a trial plan":      {store.Order{Type: "new", Amount: 900}, &trial, r, false},
		"a package plan":    {store.Order{Type: "new", Amount: 900}, &topup, r, false},
		"a customer":        {store.Order{Type: "new", Amount: 900}, e.plan, customer, false},
		"a banned reseller": {store.Order{Type: "new", Amount: 900}, e.plan, &banned, false},
		"a refunded order":  {store.Order{Type: "new", Amount: 900, RefundedAt: &refunded}, e.plan, r, false},
		"no one":            {store.Order{Type: "new", Amount: 900}, e.plan, nil, false},
	} {
		if got := countsAsSale(&tc.o, tc.plan, tc.buyer); got != tc.want {
			t.Errorf("%s: %v, want %v", name, got, tc.want)
		}
	}
}

// fundNowFor puts amount in a customer's wallet under a key of its own.
func (e *targetsEnv) fundNowFor(u *store.User, amount int64) {
	e.t.Helper()
	err := e.s.WithTx(context.Background(), func(tx pgx.Tx) error {
		_, err := e.s.Credit(context.Background(), tx, u.ID, "IRT", amount, &store.LedgerEntry{Kind: "topup", IdempotencyKey: "fund-" + nextKey()})
		return err
	})
	if err != nil {
		e.t.Fatal(err)
	}
}

// Off by default, and sales are counted only while the targets are on: switched on in the middle of a month they count from then; switched off,
// nothing is counted or paid, and what was paid stays.
func TestSalesAreCountedOnlyWhileTargetsAreOn(t *testing.T) {
	e := newTargetsEnv(t)
	e.set(map[string]string{SettingTargetsEnabled: "false"})
	r := e.reseller(9331)
	e.sell(r)
	e.sell(r)
	if p := e.progress(r); p.On || p.Sold != 0 {
		t.Fatalf("off: %+v", p)
	}
	var n int
	if err := e.s.DB().QueryRow(context.Background(), `SELECT count(*) FROM core.reseller_sales`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("sales recorded while off: %d %v", n, err)
	}
	e.set(map[string]string{SettingTargetsEnabled: "true"})
	e.sell(r) // the first it counts
	if p := e.progress(r); p.Sold != 1 || len(e.bonuses(r)) != 0 {
		t.Fatalf("switched on: %+v", p)
	}
	e.sell(r)
	if got := e.bonuses(r); len(got) != 1 {
		t.Fatalf("bonuses: %v", got)
	}
	e.set(map[string]string{SettingTargetsEnabled: "false"})
	e.sell(r)
	e.sell(r)
	if got := e.bonuses(r); len(got) != 1 {
		t.Fatalf("a bonus was paid while off: %v", got)
	}
	// Tiers emptied: switched on, nothing to pay, nothing counted.
	e.set(map[string]string{SettingTargetsEnabled: "true", SettingTargetsTiers: ""})
	e.sell(r)
	if p := e.progress(r); p.On {
		t.Fatalf("no tiers: %+v", p)
	}
	e.consistent(r)
}

// A refund takes a sale out of the month's count, but a bonus already paid stays (and a tier is not paid again when the count comes back to it).
func TestRefundLowersTheCountButNotTheBonusPaid(t *testing.T) {
	e := newTargetsEnv(t)
	ctx := context.Background()
	r := e.reseller(9341)
	a := e.sell(r)
	e.sell(r) // two: tier 1 paid
	if len(e.bonuses(r)) != 1 {
		t.Fatalf("bonuses: %v", e.bonuses(r))
	}
	balance := e.balance(r)
	if _, err := e.svc.RefundOrder(ctx, e.owner, e.fp, a.ID, "the customer left"); err != nil {
		t.Fatal(err)
	}
	p := e.progress(r)
	if p.Sold != 1 || p.BonusPaid != 1000 || !p.Tiers[0].Paid || p.Tiers[0].Reached {
		t.Fatalf("after the refund: %+v", p)
	}
	if got := e.balance(r); got != balance+900 { // the order's price came back, the bonus did not go
		t.Fatalf("the refund moved the balance by %d", got-balance)
	}
	// Selling another brings the count back to two: tier 1 is not paid twice.
	e.sell(r)
	if p = e.progress(r); p.Sold != 2 || p.BonusPaid != 1000 || len(e.bonuses(r)) != 1 {
		t.Fatalf("back at two: %+v %v", p, e.bonuses(r))
	}
	e.consistent(r)
}

// The owner may edit the tiers in the middle of a month: a tier is told by its threshold, so no edit can pay one twice.
func TestEditingTheTiersNeverPaysATierTwice(t *testing.T) {
	e := newTargetsEnv(t)
	r := e.reseller(9351)
	e.sell(r)
	e.sell(r) // tier "2 for 1,000" paid
	paid := func() string { return strings.Join(e.bonuses(r), ",") }
	base := fmt.Sprintf("target:%s:2026-10:2=1000", r.ID)
	if paid() != base {
		t.Fatalf("paid: %s", paid())
	}

	// A tier added below the one paid: the next delivery pays it, once, and the first is not paid again.
	e.set(map[string]string{SettingTargetsTiers: `[{"threshold":1,"bonus":300},{"threshold":2,"bonus":1000},{"threshold":4,"bonus":5000}]`})
	e.sell(r)
	want := fmt.Sprintf("target:%s:2026-10:1=300,%s", r.ID, base)
	if paid() != want {
		t.Fatalf("after adding a tier: %s", paid())
	}
	e.sell(r) // four: the top tier
	// The bonus of a tier already paid is raised, the tier moved to another place in the list, and removed and added back.
	e.set(map[string]string{SettingTargetsTiers: `[{"threshold":2,"bonus":9999},{"threshold":4,"bonus":9999},{"threshold":1,"bonus":9999}]`})
	e.sell(r)
	e.set(map[string]string{SettingTargetsTiers: `[{"threshold":4,"bonus":5000}]`})
	e.sell(r)
	e.set(map[string]string{SettingTargetsTiers: `[{"threshold":1,"bonus":300},{"threshold":2,"bonus":1000},{"threshold":4,"bonus":5000}]`})
	e.sell(r)
	if got := len(e.bonuses(r)); got != 3 {
		t.Fatalf("%d bonuses: %s", got, paid())
	}
	// A new tier above the count pays when it is reached, not before.
	e.set(map[string]string{SettingTargetsTiers: `[{"threshold":2,"bonus":1000},{"threshold":10,"bonus":20000}]`})
	e.sell(r) // seven
	if got := len(e.bonuses(r)); got != 3 {
		t.Fatalf("%d bonuses before the tier is reached: %s", got, paid())
	}
	e.sell(r)
	e.sell(r)
	e.sell(r) // ten
	if got := len(e.bonuses(r)); got != 4 || !strings.Contains(paid(), ":10=20000") {
		t.Fatalf("%d bonuses at ten: %s", got, paid())
	}
	e.consistent(r)
}

// A delivery that is replayed counts once and pays once; a delivery of many services at once pays each tier exactly once.
func TestBonusesArePaidOnceUnderConcurrency(t *testing.T) {
	e := newTargetsEnv(t)
	needConns(t, e.s, 10)
	ctx := context.Background()
	r := e.reseller(9361)

	// the same delivery over and over
	o := e.sell(r)
	for range 3 {
		if err := e.s.WithTx(ctx, func(tx pgx.Tx) error { return e.svc.afterDelivery(ctx, tx, o, e.plan, "") }); err != nil {
			t.Fatal(err)
		}
	}
	if p := e.progress(r); p.Sold != 1 {
		t.Fatalf("a replay counted: %+v", p)
	}

	// six services delivered at the same time: sold 1 + 6 = 7
	for range 6 {
		e.order(r, e.plan, OrderNew, "")
	}
	var wg sync.WaitGroup
	errs := make(chan error, 6)
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := e.svc.NewProvisionWorker(&fakeProvisioner{}, nil).ProvisionNext(ctx)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("provisioning: %v", err)
		}
	}
	var delivered int64
	if err := e.s.DB().QueryRow(ctx, `SELECT count(*) FROM core.orders WHERE user_id = $1 AND status = 'active'`, r.ID).Scan(&delivered); err != nil {
		t.Fatal(err)
	}
	p := e.progress(r)
	if p.Sold != delivered {
		t.Fatalf("%d delivered but %d counted", delivered, p.Sold)
	}
	wantBonuses := 0
	for _, tier := range p.Tiers {
		if tier.Reached {
			wantBonuses++
		}
	}
	if got := e.bonuses(r); len(got) != wantBonuses {
		t.Fatalf("%d tiers reached but %d bonuses: %v", wantBonuses, len(got), got)
	}
	e.consistent(r)
}

// A reseller removed before the delivery commits is a customer by then: nothing is counted for them.
func TestSaleOfAResellerRemovedMeanwhileIsNotCounted(t *testing.T) {
	e := newTargetsEnv(t)
	ctx := context.Background()
	r := e.reseller(9371)
	e.order(r, e.plan, OrderNew, "")
	// they owe the 900 of the service: the store is paid, and the reseller is removed
	if _, _, err := e.svc.RecordResellerPayment(ctx, e.owner, r.ID, 900, "IRT", "paid in cash", nextKey()); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.RemoveReseller(ctx, e.owner, r.ID, "closed the shop"); err != nil {
		t.Fatal(err)
	}
	// the removal cancels orders still waiting for payment; a paid one is delivered as ever
	e.deliver()
	var n int
	if err := e.s.DB().QueryRow(ctx, `SELECT count(*) FROM core.reseller_sales WHERE user_id = $1`, r.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("a removed reseller's sale was counted: %d %v", n, err)
	}
	if _, err := e.svc.ResellerTargets(ctx, r.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("progress of a customer: %v", err)
	}
}

// The reseller's panel carries the month; a customer has none.
func TestResellerInfoCarriesTheMonth(t *testing.T) {
	e := newTargetsEnv(t)
	ctx := context.Background()
	r := e.reseller(9381)
	e.sell(r)
	info, err := e.svc.ResellerInfo(ctx, r.ID)
	if err != nil || info.Targets == nil || info.Targets.Sold != 1 || info.Targets.Next == nil || info.Targets.Next.Threshold != 2 {
		t.Fatalf("info: %+v %v", info, err)
	}
	e.set(map[string]string{SettingTargetsEnabled: "false"})
	if info, err = e.svc.ResellerInfo(ctx, r.ID); err != nil || info.Targets != nil {
		t.Fatalf("targets off: %+v %v", info, err)
	}
	if _, err := e.svc.ResellerTargets(ctx, seedUser(t, e.s, 9382).ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a customer's targets: %v", err)
	}
}

// The month's key carries the calendar: Jalali sales are the Jalali month's, and a Gregorian count does not mix with them.
func TestJalaliMonthKeepsItsOwnCount(t *testing.T) {
	e := newTargetsEnv(t)
	e.set(map[string]string{SettingTargetsCalendar: "jalali"})
	e.at = time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC) // 28 Mehr 1405
	r := e.reseller(9391)
	e.sell(r)
	e.sell(r) // tier 1, paid under the Jalali month
	if got := e.bonuses(r); len(got) != 1 || got[0] != fmt.Sprintf("target:%s:1405-07:2=1000", r.ID) {
		t.Fatalf("bonuses: %v", got)
	}
	e.at = time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC) // 2 Aban: a new month
	if p := e.progress(r); p.Period != "1405-08" || p.Sold != 0 {
		t.Fatalf("the next Jalali month: %+v", p)
	}
	e.sell(r)
	e.sell(r)
	if got := e.bonuses(r); len(got) != 2 {
		t.Fatalf("bonuses: %v", got)
	}
}
