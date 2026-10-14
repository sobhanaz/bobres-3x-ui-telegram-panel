package domain

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/license"
)

// A staff member promoted to owner keeps their seat: the two seat holders of
// the free tier promoted to owner leave the count at two, and a third staff
// member is still refused. The owner the server names takes none.
func TestOwnersAreSeatsExceptTheConfiguredOne(t *testing.T) {
	ctx := context.Background()
	w := newGateWorld(t)
	w.endTrial()
	a, err := w.addStaff("admin")
	if err != nil {
		t.Fatal(err)
	}
	b, err := w.addStaff("support")
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range []*store.User{a, b} {
		if _, err := w.svc.SetStaffRole(ctx, w.own, u.ID, "owner", "promote"); err != nil {
			t.Fatalf("promote: %v", err)
		}
	}
	if n, err := w.s.CountStaffSeats(ctx, w.s.Conn(), ownerTG); err != nil || n != 2 {
		t.Fatalf("seats after both were promoted to owner: %d %v (the configured owner is not one)", n, err)
	}
	for _, role := range []string{"admin", "support", "owner"} {
		_, err := w.addStaff(role)
		wantRefusal(t, "a third staff member ("+role+")", err, "dashboard_staff_seats", "the free tier allows 2 staff seats")
	}
	// Seen from the license usage too (the dashboard and the CLI show it).
	if u, err := w.svc.LicenseUsage(ctx, nil, nil); err != nil || u.StaffSeats != 2 {
		t.Fatalf("usage: %+v %v", u, err)
	}
	// An install that names no owner counts every owner.
	if n, _ := w.s.CountStaffSeats(ctx, w.s.Conn(), 0); n != 3 {
		t.Fatalf("with no configured owner: %d", n)
	}
}

// A trial that "began in the future" is clamped to now, written back, so it
// then really ends 30 days later (and never starts over).
func TestFutureTrialStartIsClamped(t *testing.T) {
	ctx := context.Background()
	w := newGateWorld(t)
	setTrialStart(t, w.s, w.now.Add(10*24*time.Hour))
	eff := effectiveOf(t, w.svc)
	if eff.Basis != license.BasisTrial || eff.TrialDaysLeft() != license.TrialDays || !eff.TrialEndsAt.Equal(w.now.Add(30*24*time.Hour)) {
		t.Fatalf("a start 10 days ahead: %s, %d days left, ends %s", eff.Basis, eff.TrialDaysLeft(), eff.TrialEndsAt)
	}
	start, err := w.s.TrialStartedAt(ctx, w.s.Conn())
	if err != nil || !start.Equal(w.now) {
		t.Fatalf("the stored start: %s %v, want %s", start, err, w.now)
	}
	// 29 days on it is running, 31 days on it is over.
	for _, c := range []struct {
		after time.Duration
		basis license.Basis
	}{{29 * 24 * time.Hour, license.BasisTrial}, {31 * 24 * time.Hour, license.BasisFree}} {
		w.svc.cfg.License.Now = func() time.Time { return w.now.Add(c.after) }
		if got := effectiveOf(t, w.svc); got.Basis != c.basis {
			t.Fatalf("%s on: %s, want %s", c.after, got.Basis, c.basis)
		}
	}
	// An earlier start is never moved.
	w.svc.cfg.License.Now = func() time.Time { return w.now }
	setTrialStart(t, w.s, w.now.Add(-5*24*time.Hour))
	if eff := effectiveOf(t, w.svc); eff.TrialDaysLeft() != 25 {
		t.Fatalf("a start 5 days ago: %d days left", eff.TrialDaysLeft())
	}
	if start, _ := w.s.TrialStartedAt(ctx, w.s.Conn()); !start.Equal(w.now.Add(-5 * 24 * time.Hour)) {
		t.Fatalf("an earlier start was moved: %s", start)
	}
}

// A reseller's purchase of a new service is refused once the effective
// entitlements have no resellers (a trial that ended, a license without
// them): the reseller keeps their account, pays and renews what they already
// bought, and a retry of an order they made is not a new purchase. The
// refusal is the customer-safe one (PurchasesPaused) when it reaches the bot.
func TestResellerPurchaseNeedsResellers(t *testing.T) {
	ctx := context.Background()
	w := newGateWorld(t)
	shop, err := w.makeReseller() // in the trial
	if err != nil {
		t.Fatal(err)
	}
	plan := seedPlan(t, w.s, false, 1000)
	fund(t, w.s, shop.ID, 100_000)
	made, err := w.svc.CreateOrder(ctx, CreateOrderParams{UserID: shop.ID, PlanID: plan.ID, IdempotencyKey: "shop-1", Label: "a customer"})
	if err != nil {
		t.Fatalf("a reseller's order in the trial: %v", err)
	}
	service := w.activeServices(1)[0]

	w.endTrial()
	_, err = w.svc.CreateOrder(ctx, CreateOrderParams{UserID: shop.ID, PlanID: plan.ID, IdempotencyKey: "shop-2", Label: "another"})
	wantRefusal(t, "a reseller's new purchase on the free tier", err, GateResellerPurchase, "the free tier does not include resellers", "buy or renew a license")
	if !IsPurchaseRefusal(err) {
		t.Fatalf("the bot would show the staff's words for %v", err)
	}
	if got, _ := w.s.GetUser(ctx, w.s.Conn(), shop.ID); got.Role != ResellerRole {
		t.Fatalf("the reseller is %q", got.Role)
	}
	// What exists is untouched: a retry returns the order, it can be paid, and
	// a renewal is not a new service.
	if again, err := w.svc.CreateOrder(ctx, CreateOrderParams{UserID: shop.ID, PlanID: plan.ID, IdempotencyKey: "shop-1", Label: "a customer"}); err != nil || again.ID != made.ID {
		t.Fatalf("a retry of the order made in the trial: %v", err)
	}
	if paid, err := w.svc.PayOrderWithWallet(ctx, made.ID, shop.ID); err != nil || paid.Status != "paid" {
		t.Fatalf("paying the order made in the trial: %+v %v", paid, err)
	}
	if _, err := w.svc.CreateOrder(ctx, CreateOrderParams{UserID: service.UserID, PlanID: plan.ID, Type: OrderRenew, SubscriptionID: service.ID, IdempotencyKey: "shop-renew"}); err != nil {
		t.Fatalf("a renewal: %v", err)
	}
	// A license with resellers lets the reseller buy again; an ended one does not.
	w.install(func(t *license.Token) { t.Entitlements = license.Entitlements{Resellers: true} })
	if _, err := w.svc.CreateOrder(ctx, CreateOrderParams{UserID: shop.ID, PlanID: plan.ID, IdempotencyKey: "shop-3", Label: "another"}); err != nil {
		t.Fatalf("a reseller's purchase under a license with resellers: %v", err)
	}
	w.install(func(t *license.Token) { t.ExpiresAt = w.now.Add(-30 * 24 * time.Hour) })
	_, err = w.svc.CreateOrder(ctx, CreateOrderParams{UserID: shop.ID, PlanID: plan.ID, IdempotencyKey: "shop-4", Label: "another"})
	wantRefusal(t, "a reseller's purchase after the license ended", err, GateResellerPurchase)
	// A customer is not affected by the resellers entitlement.
	cust := seedUser(t, w.s, 944001)
	if _, err := w.svc.CreateOrder(ctx, CreateOrderParams{UserID: cust.ID, PlanID: plan.ID, IdempotencyKey: "cust-1"}); err != nil {
		t.Fatalf("a customer's purchase on the free tier: %v", err)
	}
}

// A lapsed install stops OFFERING the premium payment methods, with no word
// to the customer: automatic USDT needs crypto_watcher and every gateway must
// be in the effective gateways; the list and the intent creation both leave
// it out. Payments already started settle, and the others stay on.
func TestLapsedInstallStopsOfferingPremiumMethods(t *testing.T) {
	ctx := context.Background()
	w := newGateWorld(t)
	setSetting(t, w.s, "payments.stars_rate", "1500")
	setSetting(t, w.s, "payments.usdt_rate", "60000")
	pay := &fakePayments{gateways: []string{Zarinpal, Stars, USDTAuto}}
	methods := func() []string {
		t.Helper()
		ms, err := w.svc.PaymentMethods(ctx, pay, "IRT", 150_000)
		if err != nil {
			t.Fatal(err)
		}
		slices.Sort(ms)
		return ms
	}
	all := []string{Stars, USDTAuto, Zarinpal}
	if got := methods(); !slices.Equal(got, all) {
		t.Fatalf("in the trial: %v", got)
	}
	cust := seedUser(t, w.s, 945001)
	plan := seedPlan(t, w.s, false, 150_001)
	o, err := w.svc.CreateOrder(ctx, CreateOrderParams{UserID: cust.ID, PlanID: plan.ID, IdempotencyKey: "m-1"})
	if err != nil {
		t.Fatal(err)
	}
	started, err := w.svc.CreatePaymentIntent(ctx, pay, CreatePaymentIntentParams{UserID: cust.ID, OrderID: o.ID, Provider: USDTAuto, IdempotencyKey: "m-int-1"})
	if err != nil {
		t.Fatalf("an automatic USDT intent in the trial: %v", err)
	}

	// The trial is over: USDT is no longer offered; Zarinpal and Stars are.
	w.endTrial()
	if got := methods(); !slices.Equal(got, []string{Stars, Zarinpal}) {
		t.Fatalf("on the free tier: %v", got)
	}
	_, err = w.svc.CreatePaymentIntent(ctx, pay, CreatePaymentIntentParams{UserID: cust.ID, OrderID: o.ID, Provider: USDTAuto, IdempotencyKey: "m-int-2"})
	if !errors.Is(err, ErrState) || gateOf(err) != "" || strings.Contains(strings.ToLower(err.Error()), "licen") || strings.Contains(err.Error(), "tier") {
		t.Fatalf("a stale menu asking for automatic USDT: %v (the customer must not read licensing)", err)
	}
	if len(pay.started) != 1 {
		t.Fatalf("payments was asked to start %d intents", len(pay.started))
	}
	for _, p := range []string{Zarinpal, Stars} {
		if _, err := w.svc.CreatePaymentIntent(ctx, pay, CreatePaymentIntentParams{UserID: cust.ID, OrderID: o.ID, Provider: p, IdempotencyKey: "m-" + p}); err != nil {
			t.Fatalf("%s on the free tier: %v", p, err)
		}
	}
	// The intent made in the trial still settles.
	ev := payEvent(t, "m-paid", events.PaymentsPaymentSucceeded, events.PaymentEvent{IntentID: started.IntentID, OrderID: o.ID, UserID: cust.ID,
		Provider: USDTAuto, Amount: 150_001, Currency: "IRT"})
	if err := w.svc.HandlePaymentEvent(ctx, ev); err != nil {
		t.Fatalf("an automatic USDT payment started in the trial: %v", err)
	}
	if got, _ := w.s.GetOrder(ctx, w.s.Conn(), o.ID); got.Status != "paid" {
		t.Fatalf("the order is %s", got.Status)
	}

	// A license names the methods it includes: the gateway alone is not
	// enough for automatic USDT (it needs crypto_watcher too).
	w.install(func(t *license.Token) {
		t.Entitlements = license.Entitlements{Gateways: []string{USDTAuto}}
	})
	if got := methods(); !slices.Equal(got, []string{Stars, Zarinpal}) {
		t.Fatalf("a license naming usdt_trc20_auto without crypto_watcher: %v", got)
	}
	w.install(func(t *license.Token) {
		t.Entitlements = license.Entitlements{Gateways: []string{USDTAuto}, CryptoWatcher: true}
	})
	if got := methods(); !slices.Equal(got, all) {
		t.Fatalf("a license with the watcher: %v", got)
	}
	// The manual methods are the free tier's: they stay (and need no licensed list).
	w.install(func(t *license.Token) { t.ExpiresAt = w.now.Add(-30 * 24 * time.Hour) })
	setSetting(t, w.s, "payments.card_number", "6037-9911-0000-0000")
	card := &fakePayments{id: "00000000-0000-7000-8000-0000abcdef12"}
	if _, err := w.svc.CreatePaymentIntent(ctx, card, CreatePaymentIntentParams{UserID: cust.ID, Provider: "manual_card", Amount: 5000, Currency: "IRT", IdempotencyKey: "m-card"}); err != nil {
		t.Fatalf("card to card after the license ended: %v", err)
	}
}

// A transient error reading the refused purchases does not delete the open
// limit notice: the license scope is left as it was, as when its other
// sources do not answer.
func TestLimitNoticeSurvivesATransientError(t *testing.T) {
	ctx := context.Background()
	a := newAlertWorld(t, WatchdogSources{})
	a.svc.cfg.License.Now = func() time.Time { return a.now }
	setTrialStart(t, a.st, a.now.Add(-90*24*time.Hour))
	w := &gateWorld{t: t, svc: a.svc, s: a.st, now: a.now, tg: 990000}
	w.v = licensed(t, a.svc, a.now)
	w.activeServices(license.FreeMaxActiveServices)
	if err := a.st.NoteLimitHit(ctx, a.st.Conn(), a.now); err != nil {
		t.Fatal(err)
	}
	a.step()
	a.after(time.Minute)
	a.step()
	a.expectSent(1)
	if st := a.state("license.limit"); st == nil || st.State != store.AlertProblem {
		t.Fatalf("the open notice: %+v", st)
	}

	// The column is unreadable for a while (a stand-in for a lost connection).
	if _, err := a.st.DB().Exec(ctx, `ALTER TABLE core.install RENAME COLUMN limit_hit_at TO limit_hit_at_away`); err != nil {
		t.Fatal(err)
	}
	restored := false
	restore := func() {
		if !restored {
			restored = true
			if _, err := a.st.DB().Exec(ctx, `ALTER TABLE core.install RENAME COLUMN limit_hit_at_away TO limit_hit_at`); err != nil {
				t.Error(err)
			}
		}
	}
	defer restore()
	p := probeOf(a, a.w.license)
	if p.covered {
		t.Fatalf("the license scope was reported as read while the refused purchases were not: %+v", p)
	}
	for range 3 {
		a.after(time.Minute)
		a.step()
	}
	if st := a.state("license.limit"); st == nil || st.State != store.AlertProblem {
		t.Fatalf("the open notice was lost on a transient error: %+v", st)
	}
	a.expectSent(1)

	// Back to normal: still open, nothing re-announced, nothing deleted.
	restore()
	a.after(time.Minute)
	a.step()
	if st := a.state("license.limit"); st == nil || st.State != store.AlertProblem || len(a.sent()) != 1 {
		t.Fatalf("after the error: %+v, %d sent", st, len(a.sent()))
	}
	if p := probeOf(a, a.w.license); !p.covered {
		t.Fatalf("not covered after it came back: %+v", p)
	}
}

// The trial's end is announced: ending (last 7 days, reminded like an
// expiring license) and ended (once). A license installed meanwhile ends the
// notice quietly; one that already ended says it itself.
func TestTrialAlerts(t *testing.T) {
	ctx := context.Background()
	day := 24 * time.Hour
	run := func(t *testing.T) *alertWorld {
		t.Helper()
		a := newAlertWorld(t, WatchdogSources{})
		a.svc.cfg.License.Now = func() time.Time { return a.now }
		return a
	}
	startedAgo := func(a *alertWorld, d time.Duration) { setTrialStart(a.t, a.st, a.now.Add(-d)) }

	t.Run("quiet until the last 7 days", func(t *testing.T) {
		a := run(t)
		startedAgo(a, 22*day) // 8 days left
		a.step()
		a.after(time.Minute)
		a.step()
		a.expectSent(0)
		if p := probeOf(a, a.w.license); len(p.checks) != 0 || !p.covered {
			t.Fatalf("8 days left: %+v", p)
		}
	})

	t.Run("ending: announced, reminded daily, then ended once", func(t *testing.T) {
		a := run(t)
		startedAgo(a, 23*day+time.Hour) // 7 days minus an hour left
		ends := a.now.Add(7*day - time.Hour)
		a.step()
		a.after(time.Minute)
		a.step()
		got := a.expectSent(1)[0]
		if got.Key != "license.trial" || got.TitleKey != "alert.trial_ending" || got.Severity != events.SeverityWarning || got.State != events.AlertProblem ||
			got.Repeat || got.Args["days"] != "7" || got.Args["date"] != fmt.Sprint(ends.Unix()) {
			t.Fatalf("ending: %+v", got)
		}
		// The 6-hour reminder and then one a day, with the days left going down.
		a.after(6 * time.Hour)
		a.step()
		a.after(24 * time.Hour)
		a.step()
		if all := a.expectSent(3); !all[1].Repeat || !all[2].Repeat || all[2].Args["days"] != "6" || all[2].TitleKey != "alert.trial_ending" {
			t.Fatalf("reminders: %+v", all)
		}
		// The trial ends: announced as new, once; no reminders after it.
		a.now = ends.Add(time.Hour)
		a.step()
		all := a.sent()
		last := all[len(all)-1]
		if len(all) != 4 || last.TitleKey != "alert.trial_ended" || last.State != events.AlertProblem || last.Repeat || last.Key != "license.trial" {
			t.Fatalf("ended: %d sent, last %+v", len(all), last)
		}
		for range 6 {
			a.after(25 * time.Hour)
			a.step()
		}
		a.expectSent(4)
		// A week after the end the notice closes quietly (no "fixed" message).
		a.after(2 * day)
		a.step()
		a.after(alertFixAfter + time.Minute)
		a.step()
		a.expectSent(4)
		if st := a.state("license.trial"); st != nil {
			t.Fatalf("the notice stayed after its week: %+v", st)
		}
	})

	t.Run("ended while core was off is announced once when it starts", func(t *testing.T) {
		a := run(t)
		startedAgo(a, 32*day)
		a.step()
		a.after(time.Minute)
		a.step()
		got := a.expectSent(1)[0]
		if got.TitleKey != "alert.trial_ended" || got.Repeat {
			t.Fatalf("ended: %+v", got)
		}
		a.after(30 * time.Hour)
		a.step()
		a.expectSent(1)
	})

	t.Run("a license ends the notice quietly", func(t *testing.T) {
		a := run(t)
		v := licensed(t, a.svc, a.now)
		a.svc.cfg.License.Now = func() time.Time { return a.now }
		startedAgo(a, 25*day)
		a.step()
		a.after(time.Minute)
		a.step()
		if got := a.expectSent(1)[0]; got.TitleKey != "alert.trial_ending" {
			t.Fatalf("ending: %+v", got)
		}
		install(t, a.st, v.token(nil))
		a.after(time.Minute)
		a.step()
		a.after(alertFixAfter + time.Minute)
		a.step()
		a.expectSent(1)
		if st := a.state("license.trial"); st != nil {
			t.Fatalf("the trial notice stayed under a license: %+v", st)
		}
	})

	t.Run("a license that ended speaks for itself", func(t *testing.T) {
		a := run(t)
		v := licensed(t, a.svc, a.now)
		a.svc.cfg.License.Now = func() time.Time { return a.now }
		startedAgo(a, 90*day)
		install(t, a.st, v.token(func(tok *license.Token) { tok.ExpiresAt = a.now.Add(-30 * day) }))
		a.step()
		a.after(time.Minute)
		a.step()
		if got := a.expectSent(1)[0]; got.Key != "license.expiry" || got.TitleKey != "alert.license_expired" {
			t.Fatalf("an expired license: %+v", got)
		}
		if p := probeOf(a, a.w.license); len(p.checks) != 1 {
			t.Fatalf("checks: %+v", p.checks)
		}
	})
	_ = ctx
}

// trialCheck says what the trial's end needs, by basis and days.
func TestTrialCheck(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	free := func(ended time.Duration) *EffectiveLicense {
		return &EffectiveLicense{Effective: license.Effective{Basis: license.BasisFree, TrialEndsAt: now.Add(-ended)}, License: &LicenseStatus{State: LicenseMissing}}
	}
	trial := func(left time.Duration) *EffectiveLicense {
		return &EffectiveLicense{Effective: license.Effective{Basis: license.BasisTrial, TrialEndsAt: now.Add(left)}, License: &LicenseStatus{State: LicenseMissing}}
	}
	for _, c := range []struct {
		name  string
		eff   *EffectiveLicense
		title string // "": nothing to say
		days  string
	}{
		{"30 days left", trial(30 * day), "", ""},
		{"8 days left", trial(8 * day), "", ""},
		{"exactly 7 days left", trial(7 * day), "alert.trial_ending", "7"},
		{"6 days and a bit", trial(6*day + time.Hour), "alert.trial_ending", "7"},
		{"the last day", trial(day), "alert.trial_ending", "1"},
		{"the last minute", trial(time.Minute), "alert.trial_ending", "1"},
		{"just ended", free(time.Minute), "alert.trial_ended", ""},
		{"ended 6 days ago", free(6 * day), "alert.trial_ended", ""},
		{"ended a week ago", free(7 * day), "", ""},
		{"an old install", free(400 * day), "", ""},
		{"the trial start unknown", &EffectiveLicense{Effective: license.Effective{Basis: license.BasisFree}, License: &LicenseStatus{State: LicenseMissing}}, "", ""},
		{"a license in force", &EffectiveLicense{Effective: license.Effective{Basis: license.BasisLicense, TrialEndsAt: now.Add(day)}, License: &LicenseStatus{State: LicenseActive}}, "", ""},
	} {
		got, ok := trialCheck(c.eff, now)
		if ok != (c.title != "") || got.title != c.title || got.args["days"] != c.days || (ok && (!got.problem || got.key != "license.trial")) {
			t.Errorf("%s: %+v ok=%v", c.name, got, ok)
		}
		if ok && got.title == "alert.trial_ended" && !got.once {
			t.Errorf("%s: the ended notice repeats", c.name)
		}
	}
}
