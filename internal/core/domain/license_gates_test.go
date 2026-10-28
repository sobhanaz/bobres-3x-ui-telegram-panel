package domain

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/license"
)

// gateWorld is an install to try the gates on: a fixed clock, a vendor key
// core trusts, and its trial running (it began a day ago).
type gateWorld struct {
	t    *testing.T
	svc  *Service
	s    *store.Store
	own  *store.User
	tier *store.ResellerTier
	v    *vendor
	fs   *fakeServers
	fg   *fakeGateways
	now  time.Time
	tg   int64
}

func newGateWorld(t *testing.T) *gateWorld {
	t.Helper()
	svc, s := testService(t)
	own := owner(t, svc)
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	w := &gateWorld{t: t, svc: svc, s: s, own: own, tier: seedTier(t, svc, own, "Gold", 10, 5, nil),
		fs: &fakeServers{}, fg: &fakeGateways{cfg: GatewayConfig{Provider: Zarinpal}}, now: now, tg: 930000}
	w.v = licensed(t, svc, now)
	setTrialStart(t, s, now.Add(-24*time.Hour))
	return w
}

// endTrial: the trial is over, so (without a license) the free tier applies.
func (w *gateWorld) endTrial() { endTrial(w.t, w.s, w.now) }

// install saves a license this install trusts, changed by edit.
func (w *gateWorld) install(edit func(*license.Token)) { install(w.t, w.s, w.v.token(edit)) }

func (w *gateWorld) addServer(name string) error {
	_, err := w.svc.AddServer(context.Background(), w.own, w.fs, ServerInput{Name: name, BaseURL: "https://" + name + ".example.com",
		Token: "t-" + name, Enabled: true})
	return err
}

func (w *gateWorld) addStaff(role string) (*store.User, error) {
	w.tg++
	u := seedUser(w.t, w.s, w.tg)
	return w.svc.AddStaff(context.Background(), w.own, u.ID, role, "help")
}

func (w *gateWorld) makeReseller() (*store.User, error) {
	w.tg++
	u := seedUser(w.t, w.s, w.tg)
	_, err := w.svc.MakeReseller(context.Background(), w.own, u.ID, ResellerInput{TierID: w.tier.ID}, "a shop")
	u.Role = ResellerRole
	return u, err
}

func (w *gateWorld) gateway(provider string, on bool) error {
	w.fg.cfg.Provider = provider
	_, err := w.svc.SetGatewayConfig(context.Background(), w.own, w.fg, GatewayUpdate{Provider: provider, Enabled: on})
	return err
}

// gateOf is the gate a refusal names ("" when err is not a license refusal).
func gateOf(err error) string {
	var lr *LicenseRefusal
	if errors.As(err, &lr) && errors.Is(err, ErrState) {
		return lr.Gate
	}
	return ""
}

// wantRefusal checks err is a refusal at gate whose words contain every one
// of words (they name the limit and say what to do).
func wantRefusal(t *testing.T, what string, err error, gate string, words ...string) {
	t.Helper()
	if gateOf(err) != gate {
		t.Fatalf("%s: %v (gate %q), want a refusal at %q", what, err, gateOf(err), gate)
	}
	for _, w := range words {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("%s: %q does not say %q", what, err, w)
		}
	}
}

// Every gate refuses a NEW action beyond what is in effect, with words that
// name the limit and say to buy or renew a license, and changes nothing.
func TestLicenseGates(t *testing.T) {
	ctx := context.Background()
	const buy = "buy or renew a license"

	t.Run("free tier", func(t *testing.T) {
		w := newGateWorld(t)
		w.endTrial()
		if err := w.addServer("de"); err != nil {
			t.Fatalf("the free tier's server: %v", err)
		}
		wantRefusal(t, "second server", w.addServer("nl"), "max_servers", "the free tier allows 1 server", buy)
		if len(w.fs.servers) != 1 {
			t.Fatalf("servers: %d", len(w.fs.servers))
		}
		for _, role := range []string{"admin", "support"} {
			if _, err := w.addStaff(role); err != nil {
				t.Fatalf("seat (%s): %v", role, err)
			}
		}
		_, err := w.addStaff("admin")
		wantRefusal(t, "third seat", err, "dashboard_staff_seats", "the free tier allows 2 staff seats", buy)
		if n, _ := w.s.CountStaffSeats(ctx, w.s.Conn(), ownerTG); n != 2 {
			t.Fatalf("seats: %d", n)
		}
		// Another owner takes a seat like anyone else (only the owner the server
		// names takes none), so owners are no way round the seats.
		_, err = w.addStaff("owner")
		wantRefusal(t, "a third seat, as an owner", err, "dashboard_staff_seats", "the free tier allows 2 staff seats", buy)
		u, err := w.makeReseller()
		wantRefusal(t, "reseller", err, "resellers", "the free tier does not include resellers", buy)
		if got, _ := w.s.GetUser(ctx, w.s.Conn(), u.ID); got.Role != "user" {
			t.Fatalf("the refused reseller is %q", got.Role)
		}
		// Automatic USDT is a paid feature; Zarinpal and the manual methods
		// are on the free tier, and switching one off is always allowed.
		wantRefusal(t, "usdt", w.gateway(USDTAuto, true), "crypto_watcher", "the free tier does not include the automatic USDT watcher", buy)
		if w.fg.sets != 0 {
			t.Fatalf("a refused gateway was saved (%d writes)", w.fg.sets)
		}
		if err := w.gateway(Zarinpal, true); err != nil {
			t.Fatalf("zarinpal on the free tier: %v", err)
		}
		for _, on := range []string{"false", "true"} {
			if err := w.svc.SetSetting(ctx, w.own, "payments.manual_card.enabled", on); err != nil {
				t.Fatalf("card %s: %v", on, err)
			}
		}
		// The wallet is always allowed.
		if err := w.svc.requireGateway(ctx, "wallet"); err != nil {
			t.Fatalf("wallet: %v", err)
		}
	})

	t.Run("trial", func(t *testing.T) {
		w := newGateWorld(t)
		for _, name := range []string{"de", "nl", "fr", "us"} {
			if err := w.addServer(name); err != nil {
				t.Fatalf("server %s in the trial: %v", name, err)
			}
		}
		for i := range 5 {
			if _, err := w.addStaff("admin"); err != nil {
				t.Fatalf("seat %d in the trial: %v", i+1, err)
			}
		}
		_, err := w.addStaff("support")
		wantRefusal(t, "sixth seat", err, "dashboard_staff_seats", "the trial allows 5 staff seats", buy)
		if _, err := w.makeReseller(); err != nil {
			t.Fatalf("reseller in the trial: %v", err)
		}
		if err := w.gateway(USDTAuto, true); err != nil {
			t.Fatalf("usdt in the trial: %v", err)
		}
		if eff := effectiveOf(t, w.svc); !eff.PoweredBy() {
			t.Fatal("the trial hides Powered by BOBRES")
		}
	})

	t.Run("license", func(t *testing.T) {
		w := newGateWorld(t)
		// A license below the trial in every way it can be: 2 servers, 3 seats,
		// no resellers, no automatic USDT, Stars named (the free tier's
		// methods are kept anyway). The trial is running, but a valid license
		// is what counts.
		w.install(func(t *license.Token) {
			t.Entitlements = license.Entitlements{MaxServers: 2, MaxActiveServices: license.Unlimited, DashboardStaffSeat: 3,
				Gateways: []string{"Stars"}}
		})
		_, err := w.makeReseller()
		wantRefusal(t, "reseller", err, "resellers", "the license does not include resellers", buy)
		for _, name := range []string{"de", "nl"} {
			if err := w.addServer(name); err != nil {
				t.Fatalf("server %s: %v", name, err)
			}
		}
		wantRefusal(t, "third server", w.addServer("fr"), "max_servers", "the license allows 2 servers", buy)
		admin, err := w.addStaff("admin")
		if err != nil {
			t.Fatalf("first seat: %v", err)
		}
		for _, role := range []string{"support", "admin"} {
			if _, err := w.addStaff(role); err != nil {
				t.Fatalf("seat (%s): %v", role, err)
			}
		}
		_, err = w.addStaff("support")
		wantRefusal(t, "fourth seat", err, "dashboard_staff_seats", "the license allows 3 staff seats (the main owner takes none)", buy)
		_, err = w.addStaff("owner")
		wantRefusal(t, "an owner takes a seat too", err, "dashboard_staff_seats")
		// A role change between admin, support and owner takes no seat and
		// frees none: promoting a seat holder to owner leaves the seats full.
		if _, err := w.svc.SetStaffRole(ctx, w.own, admin.ID, "owner", "more"); err != nil {
			t.Fatalf("a seat becoming owner: %v", err)
		}
		if n, _ := w.s.CountStaffSeats(ctx, w.s.Conn(), ownerTG); n != 3 {
			t.Fatalf("seats after a promotion to owner: %d", n)
		}
		_, err = w.addStaff("support")
		wantRefusal(t, "a seat after a promotion", err, "dashboard_staff_seats")
		if _, err := w.svc.SetStaffRole(ctx, w.own, admin.ID, "support", "less"); err != nil {
			t.Fatalf("an owner stepping down: %v", err)
		}
		// Automatic USDT is not in this license (the trial's is not either): off.
		wantRefusal(t, "usdt on", w.gateway(USDTAuto, true), "crypto_watcher", "the license does not include the automatic USDT watcher", buy)
		if w.fg.sets != 0 {
			t.Fatalf("a refused gateway was saved")
		}
		// What the free tier has stays on: Zarinpal and the manual methods
		// (never lower than free), switching one off is always allowed.
		if err := w.gateway(Zarinpal, true); err != nil {
			t.Fatalf("zarinpal under a license that names only Stars: %v", err)
		}
		for _, on := range []string{"false", "true"} {
			if err := w.svc.SetSetting(ctx, w.own, "payments.manual_card.enabled", on); err != nil {
				t.Fatalf("card %s: %v", on, err)
			}
		}
		if _, err := w.svc.SetSettings(ctx, w.own, map[string]string{"payments.stars.enabled": "false", "payments.card_number": ""}); err != nil {
			t.Fatalf("stars off: %v", err)
		}
		if err := w.svc.SetSetting(ctx, w.own, "payments.stars.enabled", ""); err != nil {
			t.Fatalf("stars back on (licensed): %v", err)
		}
		// Unlimited servers allow any number.
		w.install(nil)
		if err := w.addServer("fr"); err != nil {
			t.Fatalf("unlimited: %v", err)
		}
	})

	// A license never leaves less than the free tier: a limit it omits (0)
	// or sets under the free tier's is raised, its gateway list is joined to
	// the free tier's, and an all-zero license locks nothing.
	t.Run("a license is never below the free tier", func(t *testing.T) {
		w := newGateWorld(t)
		w.endTrial()
		w.install(func(t *license.Token) {
			t.Entitlements = license.Entitlements{MaxServers: 2, Resellers: true, Gateways: []string{"zarinpal"}, DashboardStaffSeat: 3}
		})
		eff := effectiveOf(t, w.svc)
		if eff.Basis != license.BasisLicense || eff.Entitlements.MaxActiveServices != license.FreeMaxActiveServices {
			t.Fatalf("a license that omits max_active_services: %s %+v", eff.Basis, eff.Entitlements)
		}
		plan := seedPlan(t, w.s, false, 1000)
		buyer := seedUser(t, w.s, 940001)
		if _, err := w.svc.CreateOrder(ctx, CreateOrderParams{UserID: buyer.ID, PlanID: plan.ID, IdempotencyKey: "floor-1"}); err != nil {
			t.Fatalf("a new order under a license that omits max_active_services: %v", err)
		}
		if err := w.svc.SetSetting(ctx, w.own, "payments.manual_card.enabled", "true"); err != nil {
			t.Fatalf("manual_card, which the free tier allows: %v", err)
		}
		for _, p := range []string{"manual_card", "manual_crypto", "manual_zarinpal", Stars, Zarinpal, IDPay, NextPay, Aqayepardakht} {
			if err := w.svc.requireGateway(ctx, p); err != nil {
				t.Errorf("%s under a license that names only zarinpal: %v", p, err)
			}
		}
		wantRefusal(t, "usdt", w.svc.requireGateway(ctx, USDTAuto), "crypto_watcher")

		// An all-zero license is the free tier, not a lock.
		w.install(func(t *license.Token) { t.Entitlements = license.Entitlements{} })
		eff = effectiveOf(t, w.svc)
		free := license.FreeEntitlements()
		if eff.Basis != license.BasisLicense || eff.Entitlements.MaxServers != free.MaxServers ||
			eff.Entitlements.MaxActiveServices != free.MaxActiveServices || eff.Entitlements.DashboardStaffSeat != free.DashboardStaffSeat ||
			len(eff.Entitlements.Gateways) != len(free.Gateways) {
			t.Fatalf("an all-zero license: %+v", eff.Entitlements)
		}
		if err := w.addServer("de"); err != nil {
			t.Fatalf("the free tier's server under an all-zero license: %v", err)
		}
		if _, err := w.addStaff("admin"); err != nil {
			t.Fatalf("a seat under an all-zero license: %v", err)
		}
		if _, err := w.svc.CreateOrder(ctx, CreateOrderParams{UserID: buyer.ID, PlanID: plan.ID, IdempotencyKey: "floor-2"}); err != nil {
			t.Fatalf("an order under an all-zero license: %v", err)
		}
		// And in its grace period.
		w.install(func(t *license.Token) {
			t.Entitlements = license.Entitlements{}
			t.ExpiresAt = w.now.Add(-24 * time.Hour)
		})
		if eff = effectiveOf(t, w.svc); eff.Basis != license.BasisGrace || eff.Entitlements.MaxActiveServices != free.MaxActiveServices {
			t.Fatalf("an all-zero license in grace: %s %+v", eff.Basis, eff.Entitlements)
		}
	})

	t.Run("a license that ended falls to the free tier", func(t *testing.T) {
		w := newGateWorld(t)
		w.endTrial()
		w.install(func(t *license.Token) { t.ExpiresAt = w.now.Add(-30 * 24 * time.Hour) })
		if err := w.addServer("de"); err != nil {
			t.Fatal(err)
		}
		_, resellerErr := w.makeReseller()
		for what, err := range map[string]error{"server": w.addServer("fr"), "reseller": resellerErr} {
			if gateOf(err) == "" || !strings.Contains(err.Error(), "the free tier") {
				t.Errorf("%s: %v", what, err)
			}
		}
		// While the trial still runs, the same license falls to the trial.
		setTrialStart(t, w.s, w.now.Add(-24*time.Hour))
		if _, err := w.makeReseller(); err != nil {
			t.Fatalf("an ended license under a running trial: %v", err)
		}
	})

	t.Run("grace keeps what the license grants", func(t *testing.T) {
		w := newGateWorld(t)
		w.endTrial()
		w.install(func(t *license.Token) { t.ExpiresAt = w.now.Add(-24 * time.Hour) })
		if _, err := w.makeReseller(); err != nil {
			t.Fatalf("grace: %v", err)
		}
	})
}

// activeServices makes n delivered services, each owned by a new customer.
func (w *gateWorld) activeServices(n int) []*store.Subscription {
	w.t.Helper()
	ctx := context.Background()
	pl := seedPlan(w.t, w.s, false, 1000)
	// Ten days from the real clock, not from w.now: w.now is a fixed date that only the license
	// checks read, while orders ask the real clock whether a service has expired (a traffic
	// top-up is refused for an expired one). From w.now these services expired on their own.
	expires, total := time.Now().Add(10*24*time.Hour), int64(50<<30)
	var out []*store.Subscription
	for range n {
		w.tg++
		u := seedUser(w.t, w.s, w.tg)
		o, _, err := w.s.CreateOrder(ctx, w.s.Conn(), &store.Order{UserID: u.ID, PlanID: pl.ID, Type: "new", Status: "active",
			Amount: 1000, Currency: "IRT", IdempotencyKey: fmt.Sprintf("svc-%d", w.tg)})
		if err != nil {
			w.t.Fatal(err)
		}
		sub, err := w.s.CreateSubscription(ctx, w.s.Conn(), &store.Subscription{UserID: u.ID, OrderID: o.ID, ClientEmail: fmt.Sprintf("u%d-x", w.tg)})
		if err != nil {
			w.t.Fatal(err)
		}
		if err := w.s.ActivateSubscription(ctx, w.s.Conn(), sub.ID, "00000000-0000-7000-8000-000000000001", "x", "https://link/"+sub.ID, &expires, &total); err != nil {
			w.t.Fatal(err)
		}
		sub.UserID = u.ID
		out = append(out, sub)
	}
	return out
}

func ordersCount(t *testing.T, s *store.Store) int {
	t.Helper()
	var n int
	if err := s.DB().QueryRow(context.Background(), `SELECT count(*) FROM core.orders`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// What exists keeps working over a limit. An install in its trial grows
// beyond the free tier (servers, staff, a reseller, automatic USDT, 30
// services); when the trial ends nothing is removed or switched off, and
// only the NEW actions beyond the free tier are refused: a new service, a
// reseller's purchase and a free trial, while customers keep renewing,
// topping up and paying for orders they already made.
func TestExistingDataKeepsWorkingOverTheLimit(t *testing.T) {
	ctx := context.Background()
	w := newGateWorld(t)
	for _, name := range []string{"de", "nl", "fr"} {
		if err := w.addServer(name); err != nil {
			t.Fatal(err)
		}
	}
	var staff []*store.User
	for _, role := range []string{"admin", "admin", "support", "support"} {
		u, err := w.addStaff(role)
		if err != nil {
			t.Fatal(err)
		}
		staff = append(staff, u)
	}
	shop, err := w.makeReseller()
	if err != nil {
		t.Fatal(err)
	}
	if err := w.gateway(USDTAuto, true); err != nil {
		t.Fatal(err)
	}
	subs := w.activeServices(30)
	plan := seedPlan(t, w.s, false, 1000)
	topup := &store.Plan{NameI18n: map[string]string{"en": "10 GB"}, Kind: "traffic", Price: 500, Currency: "IRT", Enabled: true,
		IsTopup: true, TrafficBytes: new(int64(10 << 30))}
	if err := w.s.UpsertPlan(ctx, w.s.Conn(), topup); err != nil {
		t.Fatal(err)
	}
	// An order made while the trial ran, not paid yet.
	early := seedUser(t, w.s, 950001)
	pending, err := w.svc.CreateOrder(ctx, CreateOrderParams{UserID: early.ID, PlanID: plan.ID, IdempotencyKey: "early"})
	if err != nil {
		t.Fatalf("an order in the trial: %v", err)
	}
	fund(t, w.s, early.ID, 5000)

	// The trial ends: the free tier's 1 server, 2 seats and 25 services are
	// all over, and the install has 3 servers, 4 seats and 30 services.
	w.endTrial()
	eff := effectiveOf(t, w.svc)
	if eff.Basis != license.BasisFree {
		t.Fatalf("after the trial: %s", eff.Basis)
	}

	// Refused: only what is new.
	wantRefusal(t, "a new server", w.addServer("us"), "max_servers")
	_, err = w.addStaff("support")
	wantRefusal(t, "new staff", err, "dashboard_staff_seats")
	buyer := seedUser(t, w.s, 950002)
	before := ordersCount(t, w.s)
	_, err = w.svc.CreateOrder(ctx, CreateOrderParams{UserID: buyer.ID, PlanID: plan.ID, IdempotencyKey: "late"})
	wantRefusal(t, "a new order", err, GateActiveServices, "the free tier allows 25 active services")
	// The free tier has no resellers: the existing reseller keeps their
	// account and what they bought, and cannot buy more.
	_, err = w.svc.CreateOrder(ctx, CreateOrderParams{UserID: shop.ID, PlanID: plan.ID, IdempotencyKey: "reseller-late", Label: "a customer"})
	wantRefusal(t, "a reseller's purchase", err, GateResellerPurchase, "the free tier does not include resellers")
	_, err = w.svc.StartTrial(ctx, buyer.ID, seedPlan(t, w.s, true, 0).ID, "trial-late")
	wantRefusal(t, "a free trial", err, GateActiveServices)
	if ordersCount(t, w.s) != before {
		t.Fatalf("refused purchases left orders behind: %d, was %d", ordersCount(t, w.s), before)
	}

	// Kept: everything that exists, and everything that renews it.
	if n, _ := w.s.CountStaffSeats(ctx, w.s.Conn(), ownerTG); n != 4 {
		t.Fatalf("staff seats: %d", n)
	}
	for _, u := range staff {
		got, err := w.s.GetUser(ctx, w.s.Conn(), u.ID)
		if err != nil || !IsStaffRole(got.Role) {
			t.Fatalf("staff access lost: %+v %v", got, err)
		}
	}
	if _, err := w.svc.SetStaffRole(ctx, w.own, staff[0].ID, "support", "swap"); err != nil {
		t.Fatalf("an existing seat changing role: %v", err)
	}
	if list, _ := w.fs.ListServers(ctx); len(list.Servers) != 3 {
		t.Fatalf("servers: %d", len(list.Servers))
	}
	if got, _ := w.s.GetUser(ctx, w.s.Conn(), shop.ID); got.Role != ResellerRole {
		t.Fatalf("the reseller is %q", got.Role)
	}
	if _, err := w.svc.UpdateReseller(ctx, w.own, shop.ID, 1, ResellerInput{TierID: w.tier.ID}, "same tier"); err != nil {
		t.Fatalf("an existing reseller is still managed: %v", err)
	}
	if got, err := w.svc.CreateOrder(ctx, CreateOrderParams{UserID: subs[0].UserID, PlanID: plan.ID, Type: OrderRenew,
		SubscriptionID: subs[0].ID, IdempotencyKey: "renew-0"}); err != nil || got.Type != OrderRenew {
		t.Fatalf("a renewal over the limit: %v", err)
	}
	if got, err := w.svc.CreateOrder(ctx, CreateOrderParams{UserID: subs[1].UserID, PlanID: topup.ID, Type: OrderTrafficTopup,
		SubscriptionID: subs[1].ID, IdempotencyKey: "topup-1"}); err != nil || got.Type != OrderTrafficTopup {
		t.Fatalf("a top-up over the limit: %v", err)
	}
	if _, err := w.svc.ExtendSubscription(ctx, w.own, ExtendParams{SubscriptionID: subs[2].ID, Days: 7, Reason: "a gift",
		IdempotencyKey: "gift-2"}); err != nil {
		t.Fatalf("staff extending a service over the limit: %v", err)
	}
	if got, err := w.svc.CreateOrder(ctx, CreateOrderParams{UserID: early.ID, PlanID: plan.ID, IdempotencyKey: "early"}); err != nil || got.ID != pending.ID {
		t.Fatalf("a retry of an order made before the limit: %v", err)
	}
	if paid, err := w.svc.PayOrderWithWallet(ctx, pending.ID, early.ID); err != nil || paid.Status != "paid" {
		t.Fatalf("paying an order made before the limit: %+v %v", paid, err)
	}
	// 30 delivered, and the paid order of the customer who made it in the
	// trial still waits for its delivery: it holds its place.
	if n, _ := w.s.CountActiveServices(ctx, w.s.Conn()); n != 31 {
		t.Fatalf("services: %d", n)
	}
	// Automatic USDT stays on, and a license that raises the limits lets the
	// refused actions through.
	if !w.svc.methodEnabled(ctx, "manual_card") {
		t.Fatal("a payment method was switched off")
	}
	// A license with resellers but still 25 services: the reseller's purchase
	// meets the services limit instead.
	w.install(func(t *license.Token) { t.Entitlements = license.Entitlements{Resellers: true} })
	_, err = w.svc.CreateOrder(ctx, CreateOrderParams{UserID: shop.ID, PlanID: plan.ID, IdempotencyKey: "reseller-late-2", Label: "a customer"})
	wantRefusal(t, "a reseller's purchase under a license with resellers", err, GateActiveServices)
	w.install(func(t *license.Token) { t.Entitlements.MaxActiveServices = 500 })
	if _, err := w.svc.CreateOrder(ctx, CreateOrderParams{UserID: buyer.ID, PlanID: plan.ID, IdempotencyKey: "late"}); err != nil {
		t.Fatalf("a new order under a license: %v", err)
	}
	if _, err := w.svc.CreateOrder(ctx, CreateOrderParams{UserID: shop.ID, PlanID: plan.ID, IdempotencyKey: "reseller-late-3", Label: "a customer"}); err != nil {
		t.Fatalf("a reseller's purchase under a license: %v", err)
	}
}

// slot is the check a new order meets, in a transaction of its own (what
// CreateOrder does before it inserts).
func (w *gateWorld) slot(key string) error {
	ctx := context.Background()
	gate, err := w.svc.serviceGate(ctx)
	if err != nil {
		w.t.Fatal(err)
	}
	return w.s.WithTx(ctx, func(tx pgx.Tx) error { return w.svc.requireServiceSlot(ctx, tx, gate, key) })
}

// -1 allows any number; nothing refuses a service below the limit, and a
// license limit under the free tier's is raised to it.
func TestServiceSlotBoundaries(t *testing.T) {
	for _, c := range []struct {
		name  string
		limit int
		have  int
		ok    bool
	}{
		{"one place left", 30, 29, true},
		{"at the limit", 30, 30, false},
		{"over the limit", 30, 31, false},
		{"unlimited", license.Unlimited, 40, true},
		{"a limit under the free tier's counts as the free tier's", 3, 24, true},
		{"and is full there", 3, 25, false},
		{"none omitted: the free tier's", 0, 24, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newGateWorld(t)
			w.install(func(t *license.Token) { t.Entitlements.MaxActiveServices = c.limit })
			w.activeServices(c.have)
			err := w.slot("")
			if c.ok && err != nil || !c.ok && gateOf(err) != GateActiveServices {
				t.Fatalf("%d of %d: %v", c.have, c.limit, err)
			}
		})
	}
}

// The places taken are the services on their way too, not only the ones
// delivered: a pending subscription, a paid order that has none yet and a
// new order awaiting payment made within 2 hours. An unpaid order older than
// that, a renewal and a top-up hold none.
func TestServiceSlotsCountWhatIsOnItsWay(t *testing.T) {
	ctx := context.Background()
	w := newGateWorld(t)
	pl := seedPlan(t, w.s, false, 1000)
	order := func(status, typ string, sub *store.Subscription) *store.Order {
		t.Helper()
		w.tg++
		u := seedUser(t, w.s, w.tg)
		o := &store.Order{UserID: u.ID, PlanID: pl.ID, Type: typ, Status: status, Amount: 1000, Currency: "IRT", IdempotencyKey: fmt.Sprintf("way-%d", w.tg)}
		if sub != nil {
			o.SubscriptionID = &sub.ID
		}
		got, _, err := w.s.CreateOrder(ctx, w.s.Conn(), o)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	count := func(want int, what string) {
		t.Helper()
		if n, err := w.s.CountActiveServices(ctx, w.s.Conn()); err != nil || n != want {
			t.Fatalf("%s: %d places (%v), want %d", what, n, err, want)
		}
	}
	count(0, "nothing")
	delivered := w.activeServices(1)
	count(1, "a delivered service")

	pending := order("provisioning", "new", nil)
	sub, err := w.s.CreateSubscription(ctx, w.s.Conn(), &store.Subscription{UserID: pending.UserID, OrderID: pending.ID, ClientEmail: "way-pending-x"})
	if err != nil {
		t.Fatal(err)
	}
	count(2, "a subscription being delivered (pending)")
	order("paid", "new", nil)
	count(3, "a paid order with no subscription yet")
	order("provisioning", "new", nil)
	count(4, "an order being delivered with no subscription yet")
	awaiting := order("awaiting_payment", "new", nil)
	count(5, "a new order awaiting payment, just made")
	order("created", "new", nil)
	count(6, "a new order just created")

	// Orders that hold no place.
	order("awaiting_payment", "renew", delivered[0])
	order("paid", "traffic_topup", delivered[0])
	order("cancelled", "new", nil)
	order("provision_failed", "new", nil) // staff retries or refunds it: not a place of its own
	count(6, "a renewal, a top-up, a cancelled order and a failed one hold none")
	// An unpaid order holds its place for 2 hours, then it does not.
	if _, err := w.s.DB().Exec(ctx, `UPDATE core.orders SET created_at = now() - interval '121 minutes' WHERE id = $1`, awaiting.ID); err != nil {
		t.Fatal(err)
	}
	count(5, "an unpaid order older than 2 hours")
	if _, err := w.s.DB().Exec(ctx, `UPDATE core.orders SET created_at = now() - interval '119 minutes' WHERE id = $1`, awaiting.ID); err != nil {
		t.Fatal(err)
	}
	count(6, "an unpaid order of 119 minutes")
	// The pending subscription's own order is not counted twice, and once it
	// is delivered it is one active service.
	if _, err := w.s.DB().Exec(ctx, `UPDATE core.subscriptions SET status = 'active' WHERE id = $1`, sub.ID); err != nil {
		t.Fatal(err)
	}
	count(6, "the pending subscription delivered")
}

// A purchase is refused when the services on their way fill the limit: at 24
// of 25 only one of many orders made together passes (the check and the
// insert are one step under a lock), and one customer cannot pre-create
// several orders to hold places.
func TestConcurrentOrdersCannotOvershootTheLimit(t *testing.T) {
	ctx := context.Background()
	w := newGateWorld(t)
	w.endTrial()
	w.activeServices(license.FreeMaxActiveServices - 1)
	plan := seedPlan(t, w.s, false, 1000)

	const n = 12
	buyers := make([]*store.User, n)
	for i := range buyers {
		buyers[i] = seedUser(t, w.s, int64(941000+i))
	}
	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		passed    int
		refused   int
		otherErrs []error
	)
	// The purchases queue for pool connections (a small pool is fine: the
	// one that holds the lock needs no second connection); the bound turns a
	// deadlock into a failure instead of a hang.
	rctx, cancelRace := context.WithTimeout(ctx, time.Minute)
	defer cancelRace()
	start := make(chan struct{})
	for i := range n {
		wg.Go(func() {
			<-start
			_, err := w.svc.CreateOrder(rctx, CreateOrderParams{UserID: buyers[i].ID, PlanID: plan.ID, IdempotencyKey: fmt.Sprintf("race-%d", i)})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				passed++
			case gateOf(err) == GateActiveServices:
				refused++
			default:
				otherErrs = append(otherErrs, err)
			}
		})
	}
	close(start)
	wg.Wait()
	if len(otherErrs) != 0 {
		t.Fatalf("other errors: %v", otherErrs)
	}
	if passed != 1 || refused != n-1 {
		t.Fatalf("%d orders passed and %d were refused at 24 of 25, want 1 and %d", passed, refused, n-1)
	}
	if got := ordersCount(t, w.s); got != license.FreeMaxActiveServices {
		t.Fatalf("%d orders in all, want %d (24 delivered services' and the one that passed)", got, license.FreeMaxActiveServices)
	}
	if at, _ := w.s.LimitHitAt(ctx, w.s.Conn()); at == nil {
		t.Fatal("the refused purchases were not noted for the staff alert")
	}

	// The same, with the timing fixed: a purchase has counted (24 of 25) and
	// not inserted its order yet while eleven more arrive. They wait for it
	// instead of counting the same 24, and are refused once its order is in.
	//
	// Every row the test needs is made BEFORE the transaction that holds the
	// lock opens: from then on the test goroutine may use only that
	// transaction. A helper that took a pool connection now would wait behind
	// purchases that hold connections while they wait for the lock, and with a
	// small pool nothing would ever free one.
	w3 := newGateWorld(t)
	w3.endTrial()
	w3.activeServices(license.FreeMaxActiveServices - 1)
	plan = seedPlan(t, w3.s, false, 1000)
	first3 := seedUser(t, w3.s, 943000)
	waiters := make([]*store.User, n-1)
	for i := range waiters {
		waiters[i] = seedUser(t, w3.s, int64(943001+i))
	}
	maxConns := int(w3.s.DB().Config().MaxConns)
	if maxConns < 2 {
		t.Fatalf("the pool has %d connection(s): this test needs 2 at least (the one holding the lock and one purchase waiting for it)", maxConns)
	}

	hctx, cancelHold := context.WithTimeout(ctx, time.Minute)
	defer cancelHold()
	tx, err := w3.s.DB().Begin(hctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after Commit
	gate, err := w3.svc.serviceGate(hctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := w3.svc.requireServiceSlot(hctx, tx, gate, "held-0"); err != nil {
		t.Fatalf("the first purchase at 24 of 25: %v", err)
	}
	results := make(chan error, n-1)
	for i, buyer := range waiters {
		go func() {
			_, err := w3.svc.CreateOrder(hctx, CreateOrderParams{UserID: buyer.ID, PlanID: plan.ID, IdempotencyKey: fmt.Sprintf("held-%d", i+1)})
			results <- err
		}()
	}
	// Prove they wait for the lock (and not for the timing): the server
	// shows their lock requests as not granted. Each takes a pool connection
	// for its transaction, so at most the pool less the held connection can
	// wait at once; the rest queue for a connection and take their turn as
	// those finish. The count is read through the held transaction.
	want := min(n-1, maxConns-1)
	var got int
	for got < want {
		select {
		case err := <-results:
			t.Fatalf("a purchase did not wait for the one that counted first: %v", err)
		case <-hctx.Done():
			t.Fatalf("%d of %d purchases are waiting for the lock after a minute (pool of %d)", got, want, maxConns)
		case <-time.After(10 * time.Millisecond):
		}
		if err := tx.QueryRow(hctx, `SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND NOT granted
			AND database = (SELECT oid FROM pg_database WHERE datname = current_database())`).Scan(&got); err != nil {
			t.Fatal(err)
		}
	}
	if got > want {
		t.Fatalf("%d purchases wait for the lock with a pool of %d and one connection held", got, maxConns)
	}
	select {
	case err := <-results:
		t.Fatalf("a purchase did not wait for the one that counted first: %v", err)
	default:
	}
	if _, _, err := w3.s.CreateOrder(hctx, tx, &store.Order{UserID: first3.ID, PlanID: plan.ID, Type: "new", Status: "awaiting_payment",
		Amount: 1000, Currency: "IRT", IdempotencyKey: "held-0"}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(hctx); err != nil {
		t.Fatal(err)
	}
	for range n - 1 {
		select {
		case err := <-results:
			wantRefusal(t, "a purchase that waited", err, GateActiveServices)
		case <-hctx.Done():
			t.Fatal("a purchase that waited never finished after the lock was released")
		}
	}

	// One customer cannot hold the last places by making many orders: the
	// first takes the place, the next ones (other keys) are refused, and a
	// retry of the first (its own key) returns it.
	w2 := newGateWorld(t) // a fresh database: its own plan
	w2.endTrial()
	w2.activeServices(license.FreeMaxActiveServices - 2)
	plan = seedPlan(t, w2.s, false, 1000)
	one := seedUser(t, w2.s, 942001)
	first, err := w2.svc.CreateOrder(ctx, CreateOrderParams{UserID: one.ID, PlanID: plan.ID, IdempotencyKey: "hold-1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w2.svc.CreateOrder(ctx, CreateOrderParams{UserID: one.ID, PlanID: plan.ID, IdempotencyKey: "hold-2"}); err != nil {
		t.Fatalf("the second place: %v", err)
	}
	_, err = w2.svc.CreateOrder(ctx, CreateOrderParams{UserID: one.ID, PlanID: plan.ID, IdempotencyKey: "hold-3"})
	wantRefusal(t, "a third order of one customer", err, GateActiveServices)
	if again, err := w2.svc.CreateOrder(ctx, CreateOrderParams{UserID: one.ID, PlanID: plan.ID, IdempotencyKey: "hold-1"}); err != nil || again.ID != first.ID {
		t.Fatalf("a retry of an order made at the limit: %v", err)
	}
}

// A purchase refused for the limit raises ONE staff alert (license_limit)
// about a day, never a "fixed" message, and the alert ends when a license
// raises the limit.
func TestLimitAlertOnceADay(t *testing.T) {
	ctx := context.Background()
	a := newAlertWorld(t, WatchdogSources{})
	w := &gateWorld{t: t, svc: a.svc, s: a.st, now: a.now, tg: 960000}
	w.v = licensed(t, a.svc, a.now)
	a.svc.cfg.License.Now = func() time.Time { return a.now }
	// The trial ended long ago: its own notice is not what this test is about.
	setTrialStart(t, a.st, a.now.Add(-90*24*time.Hour))
	w.activeServices(license.FreeMaxActiveServices)
	buyer := seedUser(t, a.st, 970001)
	plan := seedPlan(t, a.st, false, 1000)
	refuse := func(key string) {
		t.Helper()
		_, err := a.svc.CreateOrder(ctx, CreateOrderParams{UserID: buyer.ID, PlanID: plan.ID, IdempotencyKey: key})
		wantRefusal(t, "a new order", err, GateActiveServices)
	}

	// No purchase refused yet: nothing to say.
	a.step()
	a.after(time.Minute)
	a.step()
	a.expectSent(0)

	// Several customers are turned away in a minute: one alert, after the
	// check has seen it twice (as for any problem).
	refuse("o1")
	refuse("o2")
	a.step()
	a.expectSent(0)
	a.after(time.Minute)
	refuse("o3")
	a.step()
	got := a.expectSent(1)[0]
	if got.Key != "license.limit" || got.TitleKey != "alert.license_limit" || got.Severity != events.SeverityWarning ||
		got.State != events.AlertProblem || got.Repeat || got.Args["limit"] != "25" || got.Args["name"] != "active_services" {
		t.Fatalf("the alert: %+v", got)
	}

	// Hours later (still refused now and then): no 6-hour reminder.
	for range 20 {
		a.after(time.Hour)
		refuse(fmt.Sprintf("h%d", len(a.sent())))
		a.step()
	}
	a.expectSent(1)
	// A day after the first: one reminder, with the new count of hours.
	a.after(5 * time.Hour)
	refuse("next-day")
	a.step()
	got = a.expectSent(2)[1]
	if !got.Repeat || got.State != events.AlertProblem || got.TitleKey != "alert.license_limit" {
		t.Fatalf("the daily reminder: %+v", got)
	}

	// Nobody is refused for a day: the alert ends quietly (no "fixed").
	a.after(25 * time.Hour)
	a.step()
	a.after(alertFixAfter + time.Minute)
	a.step()
	a.expectSent(2)
	if st := a.state("license.limit"); st == nil || st.State != store.AlertOK {
		t.Fatalf("the closed alert: %+v", st)
	}
	// A refusal long after the last announcement is announced again.
	refuse("again")
	a.after(time.Minute)
	a.step()
	a.after(time.Minute)
	refuse("again-2")
	a.step()
	got = a.expectSent(3)[2]
	if got.Repeat || got.State != events.AlertProblem {
		t.Fatalf("the next day's alert: %+v", got)
	}

	// A license that raises the limit ends it too (quietly).
	install(t, a.st, w.v.token(func(tok *license.Token) { tok.Entitlements.MaxActiveServices = 500 }))
	a.after(time.Minute)
	a.step()
	a.after(alertFixAfter + time.Minute)
	a.step()
	a.expectSent(3)
	if st := a.state("license.limit"); st == nil || st.State != store.AlertOK {
		t.Fatalf("after a license: %+v", st)
	}
}
