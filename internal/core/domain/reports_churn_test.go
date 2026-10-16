package domain

import (
	"context"
	"strings"
	"testing"
	"time"

	buuid "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
)

func TestChurnGraceRequest(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		in   string
		want int
		bad  bool
	}{
		{"", DefaultChurnGraceDays, false},
		{"0", 0, false},
		{"14", 14, false},
		{"90", 90, false},
		{"91", 0, true},
		{"-1", 0, true},
		{"x", 0, true},
		{"7.5", 0, true},
	} {
		r, err := NewReportRange(ReportRequest{ChurnGraceDays: c.in}, "UTC", now)
		if c.bad {
			if err == nil || !strings.Contains(err.Error(), "churn_grace_days") {
				t.Errorf("%q: error %v", c.in, err)
			}
			continue
		}
		if err != nil || r.ChurnGraceDays != c.want || !r.AsOf.Equal(now) {
			t.Errorf("%q: %+v %v", c.in, r, err)
		}
		if q := r.Query(); q.ChurnGraceDays != c.want || !q.ChurnAsOf.Equal(now) {
			t.Errorf("%q: the store's query is %+v", c.in, q)
		}
	}
}

// The report carries churn per bucket and in total, and the grace period moves
// a service between "at risk" and "churned".
func TestSalesReportChurn(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	x := &reportSeeder{t: t, s: s, users: map[int]string{}, subs: map[int]string{}, plans: map[string]string{}}
	at := func(v string) time.Time {
		tm, err := time.Parse(time.RFC3339, v)
		if err != nil {
			t.Fatal(err)
		}
		return tm
	}
	now := at("2026-11-10T12:00:00Z")
	// Users 1 and 2 ended on 20 and 21 Oct, user 3 on 8 Nov (2 days ago); user 4 has two services, the
	// later one still running; user 5 renewed (paid, not yet applied).
	for n, exp := range map[int]string{1: "2026-10-20T10:00:00Z", 2: "2026-10-21T10:00:00Z", 3: "2026-11-08T10:00:00Z", 5: "2026-10-22T10:00:00Z"} {
		x.order(n, "IRT", "new", "active", 100, at("2026-08-01T10:00:00Z"))
		x.exec(`UPDATE core.subscriptions SET expires_at = $2, status = 'expired' WHERE id = $1`, x.subs[n], at(exp))
	}
	x.order(4, "IRT", "new", "active", 100, at("2026-08-01T10:00:00Z"))
	x.exec(`UPDATE core.subscriptions SET expires_at = $2, status = 'expired' WHERE id = $1`, x.subs[4], at("2026-10-21T11:00:00Z"))
	second := x.order(40, "IRT", "new", "active", 100, at("2026-08-02T10:00:00Z"))
	x.exec(`UPDATE core.subscriptions SET user_id = $2, expires_at = $3 WHERE order_id = $1`, second, x.user(4), at("2026-12-20T10:00:00Z"))
	x.exec(`INSERT INTO core.orders (id, user_id, plan_id, type, status, amount, currency, idempotency_key, subscription_id, created_at, updated_at)
		VALUES ($1, $2, $3, 'renew', 'paid', 100, 'IRT', $4, $5, $6, $6)`, buuid.MustV7().String(), x.user(5), x.plan("IRT"), "k-renew", x.subs[5], at("2026-11-09T10:00:00Z"))

	for _, grace := range []string{"7", "0", "30"} {
		rng, err := NewReportRange(ReportRequest{From: "2026-10-01", To: "2026-11-30", Group: GroupMonth, TZ: "UTC", ChurnGraceDays: grace}, "UTC", now)
		if err != nil {
			t.Fatal(err)
		}
		rep, err := svc.SalesReport(ctx, rng)
		if err != nil {
			t.Fatal(err)
		}
		// Grace 7: services 1, 2 and 4's first one churned (October), 3 is at risk (November).
		// Grace 0: 3 has churned as well. Grace 30: nothing has churned yet; the three of October and 3 are at risk.
		oct, nov := rep.Buckets[0], rep.Buckets[1]
		if oct.Date != "2026-10-01" || nov.Date != "2026-11-01" {
			t.Fatalf("buckets %s %s", oct.Date, nov.Date)
		}
		type row struct{ services, customers, risk int64 }
		got := [2]row{{oct.ChurnedServices, oct.ChurnedCustomers, oct.AtRisk}, {nov.ChurnedServices, nov.ChurnedCustomers, nov.AtRisk}}
		var want [2]row
		switch grace {
		case "7":
			// Customers: 1 and 2 (user 4 has a live service, user 5 a renewal waiting).
			want = [2]row{{3, 2, 0}, {0, 0, 1}}
		case "0":
			want = [2]row{{3, 2, 0}, {1, 1, 0}}
		case "30":
			want = [2]row{{0, 0, 3}, {0, 0, 1}}
		}
		if got != want {
			t.Errorf("grace %s: %+v, want %+v", grace, got, want)
		}
		tot := rep.Totals
		if tot.ChurnedServices != oct.ChurnedServices+nov.ChurnedServices || tot.ChurnedCustomers != oct.ChurnedCustomers+nov.ChurnedCustomers ||
			tot.AtRisk != oct.AtRisk+nov.AtRisk {
			t.Errorf("grace %s: totals %+v", grace, tot)
		}
	}
}
