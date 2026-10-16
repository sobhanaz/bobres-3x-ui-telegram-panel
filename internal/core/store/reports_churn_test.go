package store

import (
	"reflect"
	"strings"
	"testing"
	"time"

	buuid "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
)

// Churn is checked against a Go model of the definition in docs/14-reports.md,
// in the same way as the sales numbers: every service is written to the
// database and recorded as a plain value, and the expected buckets come from
// Go's own time zone arithmetic.

type mService struct {
	user    int
	trial   bool
	status  string
	exp     *time.Time
	renewal bool // a paid renewal waits to be applied
}

type churnSeed struct {
	*seed
	svcs []mService
}

// service writes a subscription of user n (with its own order on plan p) and,
// when renewal is not "", a renewal order for it in that status.
func (c *churnSeed) service(n int, p *mPlan, trial bool, status string, exp *time.Time, renewal string) {
	c.t.Helper()
	u := c.user(n)
	at := utc("2026-08-01T10:00:00Z")
	oid := c.order(u, p, "new", "active", 1000, at, nil)
	sid := buuid.MustV7().String()
	c.exec(`INSERT INTO core.subscriptions (id, user_id, order_id, server_id, client_email, status, expires_at, created_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		sid, u, oid, buuid.MustV7().String(), "c-"+sid, status, exp, at)
	pending := false
	if renewal != "" {
		c.order(u, p, "renew", renewal, 1000, at.Add(time.Hour), &sid)
		pending = renewal == "paid" || renewal == "provisioning" || renewal == "provision_failed"
	}
	c.svcs = append(c.svcs, mService{user: n, trial: trial, status: status, exp: exp, renewal: pending})
}

func at(s string) *time.Time { t := utc(s); return &t }

type churnNumbers struct {
	services, customers, risk map[string]int64
}

func (c *churnSeed) expect(now time.Time, grace int, tz, from, to, group string) churnNumbers {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		c.t.Fatal(err)
	}
	cutoff := now.Add(-time.Duration(grace) * 24 * time.Hour)
	out := churnNumbers{services: map[string]int64{}, customers: map[string]int64{}, risk: map[string]int64{}}
	live := func(s mService) bool {
		return s.status != "pending" && s.status != "disabled" && s.status != "deleted" && !s.trial
	}
	for _, s := range c.svcs {
		if s.exp == nil || !live(s) || s.renewal || s.exp.After(now) || !inRange(*s.exp, loc, from, to) {
			continue
		}
		if s.exp.After(cutoff) {
			out.risk[bucketKey(*s.exp, loc, group)]++
		} else {
			out.services[bucketKey(*s.exp, loc, group)]++
		}
	}
	type person struct {
		last time.Time
		open bool
		any  bool
	}
	people := map[int]*person{}
	for _, s := range c.svcs {
		if !live(s) {
			continue
		}
		p := people[s.user]
		if p == nil {
			p = &person{}
			people[s.user] = p
		}
		if s.exp == nil || s.renewal {
			p.open = true
			continue
		}
		if !p.any || s.exp.After(p.last) {
			p.last, p.any = *s.exp, true
		}
	}
	for _, p := range people {
		if !p.open && p.any && !p.last.After(cutoff) && inRange(p.last, loc, from, to) {
			out.customers[bucketKey(p.last, loc, group)]++
		}
	}
	return out
}

func (d *SalesData) churnFlat() churnNumbers {
	return churnNumbers{services: nz(d.ChurnedServices), customers: nz(d.ChurnedCustomers), risk: nz(d.AtRisk)}
}

func nz(m map[string]int64) map[string]int64 {
	out := map[string]int64{}
	for k, v := range m {
		if v != 0 {
			out[k] = v
		}
	}
	return out
}

var churnNow = utc("2026-11-10T12:00:00Z") // grace 7 days: the cutoff is 2026-11-03T12:00:00Z

func churnScenario(t *testing.T) *churnSeed {
	c := &churnSeed{seed: newSeed(t)}
	paid := c.plan("پایه", "Basic", "IRT", false)
	trial := c.plan("آزمایشی", "Trial", "IRT", true)
	// 20: ended, not renewed: churn (a service and a customer).
	c.service(20, paid, false, "expired", at("2026-10-20T10:00:00Z"), "")
	// 21: ended, but a paid renewal waits: not churn, nor at risk; an unpaid renewal does not count.
	c.service(21, paid, false, "expired", at("2026-10-25T10:00:00Z"), "paid")
	c.service(40, paid, false, "expired", at("2026-10-25T11:00:00Z"), "provision_failed")
	c.service(41, paid, false, "expired", at("2026-10-25T12:00:00Z"), "awaiting_payment")
	// 22: a trial is no churn.
	c.service(22, trial, true, "expired", at("2026-10-22T10:00:00Z"), "")
	// 23: ended inside the grace period: at risk.
	c.service(23, paid, false, "expired", at("2026-11-06T10:00:00Z"), "")
	// 24: deleted, disabled and pending services are no churn.
	c.service(24, paid, false, "deleted", at("2026-10-21T10:00:00Z"), "")
	c.service(24, paid, false, "disabled", at("2026-10-21T11:00:00Z"), "")
	c.service(24, paid, false, "pending", at("2026-10-21T12:00:00Z"), "")
	// 25: one service ended, another is live: the service is churn, the customer is not.
	c.service(25, paid, false, "expired", at("2026-10-10T10:00:00Z"), "")
	c.service(25, paid, false, "active", at("2026-12-01T10:00:00Z"), "")
	// 26: two services ended; the person churns once, on the later expiry (Tehran 1 Nov 00:15, New York 31 Oct 16:45).
	c.service(26, paid, false, "expired", at("2026-10-31T20:45:00Z"), "")
	c.service(26, paid, false, "expired", at("2026-10-15T10:00:00Z"), "")
	// 27: no expiry (unlimited): nothing. 28: one ended, one unlimited: the service churns, the customer does not.
	c.service(27, paid, false, "active", nil, "")
	c.service(28, paid, false, "expired", at("2026-10-12T10:00:00Z"), "")
	c.service(28, paid, false, "active", nil, "")
	// 30: live. 31: an ended trial next to an ended paid service: the paid one decides.
	c.service(30, paid, false, "active", at("2026-12-01T10:00:00Z"), "")
	c.service(31, trial, true, "expired", at("2026-11-02T10:00:00Z"), "")
	c.service(31, paid, false, "expired", at("2026-10-18T10:00:00Z"), "")
	// 32: a trial still running next to an ended paid service: the trial is not a live service.
	c.service(32, trial, true, "active", at("2026-12-20T10:00:00Z"), "")
	c.service(32, paid, false, "depleted", at("2026-10-19T10:00:00Z"), "")
	// The edges of the grace period: exactly the cutoff churns, a second later is at risk; exactly now is
	// at risk, a second after now has not ended.
	c.service(33, paid, false, "expired", at("2026-11-03T12:00:00Z"), "")
	c.service(34, paid, false, "expired", at("2026-11-03T12:00:01Z"), "")
	c.service(35, paid, false, "expired", at("2026-11-10T12:00:00Z"), "")
	c.service(36, paid, false, "active", at("2026-11-10T12:00:01Z"), "")
	// Another customer's two expiries on different local days (Tehran 30 Oct 23:59:59 and 31 Oct 00:00:00).
	c.service(37, paid, false, "expired", at("2026-10-30T20:29:59Z"), "")
	c.service(38, paid, false, "expired", at("2026-10-30T20:30:00Z"), "")
	return c
}

func TestChurnMatchesTheModel(t *testing.T) {
	c := churnScenario(t)
	for _, grace := range []int{7, 0, 30} {
		for _, tz := range []string{"Asia/Tehran", "America/New_York", "UTC"} {
			for _, rg := range [][2]string{{"2026-10-01", "2026-11-30"}, {"2026-10-30", "2026-11-03"}, {"2026-11-10", "2026-11-10"}} {
				for _, group := range []string{"day", "week", "month"} {
					got, err := c.s.SalesSummary(c.ctx, ReportQuery{From: rg[0], To: rg[1], TZ: tz, Group: group, ChurnAsOf: churnNow, ChurnGraceDays: grace})
					if err != nil {
						t.Fatal(err)
					}
					want := c.expect(churnNow, grace, tz, rg[0], rg[1], group)
					if g := got.churnFlat(); !reflect.DeepEqual(g, want) {
						t.Errorf("grace %d %s %s to %s by %s\n got: %+v\nwant: %+v", grace, tz, rg[0], rg[1], group, g, want)
					}
				}
			}
		}
	}
}

// The model above and the SQL could share a mistake: the numbers of the whole
// scenario are also written out by hand (UTC, one month bucket, grace 7).
func TestChurnFixedNumbers(t *testing.T) {
	c := churnScenario(t)
	got, err := c.s.SalesSummary(c.ctx, ReportQuery{From: "2026-10-01", To: "2026-11-30", TZ: "UTC", Group: "month", ChurnAsOf: churnNow, ChurnGraceDays: 7})
	if err != nil {
		t.Fatal(err)
	}
	// Churned services in October: 20, 25 (the ended one), 26 (both), 28 (the ended one), 31 (the paid one),
	// 32 (depleted; its trial is ignored), 37, 38 and 41 (its renewal is unpaid) = 10. In November: 33 (at the
	// cutoff) = 1. Not counted: 21, 40 (renewal waiting), 22 (trial), 24 (deleted, disabled, pending), 27, 30, 36.
	if got.ChurnedServices["2026-10-01"] != 10 || got.ChurnedServices["2026-11-01"] != 1 {
		t.Errorf("churned services %v, want October 10 and November 1", got.ChurnedServices)
	}
	// At risk, ended after the cutoff but not after now: 23, 34 and 35.
	if got.AtRisk["2026-11-01"] != 3 || len(got.AtRisk) != 1 {
		t.Errorf("at risk %v, want November 3", got.AtRisk)
	}
	// Customers: 20, 26, 31, 32, 37, 38, 41 and 33 (November); not 25 or 28 (another live service), 21 or 40
	// (renewal waiting), 23, 34, 35 (inside the grace period).
	if got.ChurnedCustomers["2026-10-01"] != 7 || got.ChurnedCustomers["2026-11-01"] != 1 {
		t.Errorf("churned customers %v, want October 7 and November 1", got.ChurnedCustomers)
	}
}

// A person is counted once, in the bucket of their last expiry; the services are
// counted each in their own.
func TestChurnCountsAPersonOnceAtTheirLastExpiry(t *testing.T) {
	c := churnScenario(t)
	got, err := c.s.SalesSummary(c.ctx, ReportQuery{From: "2026-10-01", To: "2026-10-31", TZ: "Asia/Tehran", Group: "day", ChurnAsOf: churnNow, ChurnGraceDays: 7})
	if err != nil {
		t.Fatal(err)
	}
	// User 26: services end on 15 Oct and (Tehran) 1 Nov; the 1 Nov one is outside this range, so the person
	// is not in it, while the 15 Oct service is.
	if got.ChurnedServices["2026-10-15"] < 1 {
		t.Errorf("15 Oct services %v", got.ChurnedServices)
	}
	if n := got.ChurnedCustomers["2026-10-15"]; n != 0 {
		t.Errorf("a person whose last expiry is on 1 Nov was counted on 15 Oct: %d", n)
	}
	wide, err := c.s.SalesSummary(c.ctx, ReportQuery{From: "2026-10-01", To: "2026-11-02", TZ: "Asia/Tehran", Group: "day", ChurnAsOf: churnNow, ChurnGraceDays: 7})
	if err != nil {
		t.Fatal(err)
	}
	if n := wide.ChurnedCustomers["2026-11-01"]; n != 1 {
		t.Errorf("user 26 on 1 Nov (Tehran): %d, want 1", n)
	}
}

// Churn is read through the index of migration 00020.
func TestChurnUsesTheExpiryIndex(t *testing.T) {
	c := newSeed(t)
	// SET on a pool is per connection: run the plan in one transaction.
	tx, err := c.s.DB().Begin(c.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(c.ctx) //nolint:errcheck // nothing to keep
	if _, err := tx.Exec(c.ctx, `SET LOCAL enable_seqscan = off`); err != nil {
		t.Fatal(err)
	}
	rows, err := tx.Query(c.ctx, `EXPLAIN `+churnServiceSQL(), "2026-10-01", "2026-11-30", "UTC", "day", churnNow, churnNow.Add(-7*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	plan := ""
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		plan += line + "\n"
	}
	if !strings.Contains(plan, "subscriptions_expires_idx") {
		t.Errorf("the churn query does not use subscriptions_expires_idx:\n%s", plan)
	}
}
