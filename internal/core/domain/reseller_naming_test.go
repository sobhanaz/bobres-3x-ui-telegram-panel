package domain

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/xui"
)

// namingEnv is a store with an owner, a tier and a plan, and resellers made on demand.
type namingEnv struct {
	t     *testing.T
	svc   *Service
	s     *store.Store
	owner *store.User
	tier  *store.ResellerTier
	plan  *store.Plan
}

func newNamingEnv(t *testing.T) *namingEnv {
	t.Helper()
	svc, s := testService(t)
	grant(svc)
	e := &namingEnv{t: t, svc: svc, s: s, owner: staffMember(t, s, 9801, "owner"), plan: planWithLimits(t, s, 30, 50<<30)}
	tier, err := svc.CreateTier(context.Background(), e.owner, TierInput{Name: "Gold", DiscountPercent: 10, CommissionPercent: 5}, "")
	if err != nil {
		t.Fatal(err)
	}
	e.tier = tier
	return e
}

// reseller makes the Telegram user tg a reseller (with credit).
func (e *namingEnv) reseller(tg int64) *store.User {
	e.t.Helper()
	u := seedUser(e.t, e.s, tg)
	if _, err := e.svc.MakeReseller(context.Background(), e.owner, u.ID, ResellerInput{TierID: e.tier.ID, CreditLimits: map[string]int64{"IRT": 10_000_000}}, "a shop"); err != nil {
		e.t.Fatal(err)
	}
	u.Role = "reseller"
	u.Status = "active"
	return u
}

// buy has u pay for one new service (a reseller on credit) and the worker deliver it; it returns the order.
func (e *namingEnv) buy(u *store.User, fp *fakeProvisioner, key string) *store.Order {
	e.t.Helper()
	ctx := context.Background()
	o, err := e.svc.CreateOrder(ctx, CreateOrderParams{UserID: u.ID, PlanID: e.plan.ID, IdempotencyKey: key})
	if err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.svc.PayOrderWithWallet(ctx, o.ID, u.ID); err != nil {
		e.t.Fatal(err)
	}
	if worked, err := e.svc.NewProvisionWorker(fp, nil).ProvisionNext(ctx); err != nil || !worked {
		e.t.Fatalf("provisioning: %v %v", worked, err)
	}
	return o
}

func TestResellerNamingNormalizes(t *testing.T) {
	for in, want := range map[string]string{"ab": "ab", " Sobhan ": "sobhan", "A1B2C3D4E5": "a1b2c3d4e5"} {
		if got, err := NormalizeConfigPrefix(in); err != nil || got != want {
			t.Errorf("prefix %q: %q %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "a", "abcdefghijk", "a-b", "a b", "u", "u12", "U9999", "نماینده", "a_b"} {
		if _, err := NormalizeConfigPrefix(bad); err == nil {
			t.Errorf("prefix %q was accepted", bad)
		}
	}
	for in, want := range map[string]string{"sobhanadmin": "SOBHANADMIN", " BOBRES-Sobhan_1 ": "SOBHAN_1", "ab": "AB"} {
		if got, err := NormalizePanelGroupName(in); err != nil || got != want {
			t.Errorf("group %q: %q %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "a", strings.Repeat("A", 25), "A-B", "A B", "نماینده", "BOBRES-"} {
		if _, err := NormalizePanelGroupName(bad); err == nil {
			t.Errorf("group %q was accepted", bad)
		}
	}
	if PanelGroupOf("") != "" || PanelGroupOf("SOBHANADMIN") != "BOBRES-SOBHANADMIN" || PanelGroupOf("X1") != xui.ResellerGroupPrefix+"X1" {
		t.Error("PanelGroupOf")
	}
	if resellerGroupPrefix != xui.ResellerGroupPrefix {
		t.Errorf("core's %q differs from the panel client's %q", resellerGroupPrefix, xui.ResellerGroupPrefix)
	}
	// A name with the longest prefix still fits Marzban's 32 characters.
	if n := len(clientEmailFor(strings.Repeat("a", MaxConfigPrefix), 9_999_999_999)); n > 32 {
		t.Errorf("the longest client name has %d characters", n)
	}
}

// The group name is the owner's to give; the prefix is the reseller's once and the owner's after; both are unique.
func TestResellerNamingRules(t *testing.T) {
	e := newNamingEnv(t)
	ctx := context.Background()
	r1, r2 := e.reseller(9811), e.reseller(9812)
	admin := staffMember(t, e.s, 9813, "admin")
	customer := seedUser(t, e.s, 9814)

	// Only the owner gives a group name.
	for name, actor := range map[string]*store.User{"the reseller": r1, "an admin": admin, "nobody": nil} {
		if err := e.svc.SetResellerPanelGroup(ctx, actor, r1.ID, "SOBHANADMIN"); !errors.Is(err, ErrForbidden) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := e.svc.SetResellerPanelGroup(ctx, e.owner, r1.ID, " sobhanadmin "); err != nil {
		t.Fatal(err)
	}
	if n, _ := e.svc.ResellerNaming(ctx, r1.ID); n.PanelGroup != "SOBHANADMIN" || n.GroupsSyncedAt != nil {
		t.Fatalf("naming %+v", n)
	}
	var fe *FieldError
	if err := e.svc.SetResellerPanelGroup(ctx, e.owner, r2.ID, "sobhanadmin"); !errors.As(err, &fe) || fe.Field != "panel_group" {
		t.Errorf("a name another reseller has: %v", err)
	}
	if err := e.svc.SetResellerPanelGroup(ctx, e.owner, r2.ID, "bad name!"); !errors.Is(err, ErrInvalid) {
		t.Errorf("a bad name: %v", err)
	}
	if err := e.svc.SetResellerPanelGroup(ctx, e.owner, customer.ID, "ALI"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a customer: %v", err)
	}

	// The prefix: the reseller once, then only the owner.
	if err := e.svc.SetResellerConfigPrefix(ctx, r1, r1.ID, "Sobhan"); err != nil {
		t.Fatal(err)
	}
	if n, _ := e.svc.ResellerNaming(ctx, r1.ID); n.ConfigPrefix != "sobhan" {
		t.Fatalf("prefix %q", n.ConfigPrefix)
	}
	if err := e.svc.SetResellerConfigPrefix(ctx, r1, r1.ID, "other"); !errors.Is(err, ErrState) {
		t.Errorf("a second prefix by the reseller: %v", err)
	}
	if err := e.svc.SetResellerConfigPrefix(ctx, r2, r2.ID, "sobhan"); !errors.As(err, &fe) || fe.Field != "config_prefix" {
		t.Errorf("a prefix another reseller has: %v", err)
	}
	for _, bad := range []string{"", "x", "u12", "has-dash"} {
		if err := e.svc.SetResellerConfigPrefix(ctx, r2, r2.ID, bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("reseller prefix %q: %v", bad, err)
		}
	}
	if err := e.svc.SetResellerConfigPrefix(ctx, r2, r1.ID, "mine"); !errors.Is(err, ErrForbidden) { // another reseller's
		t.Errorf("someone else's prefix: %v", err)
	}
	if err := e.svc.SetResellerConfigPrefix(ctx, customer, customer.ID, "ali"); !errors.Is(err, ErrForbidden) {
		t.Errorf("a customer's prefix: %v", err)
	}
	if err := e.svc.SetResellerConfigPrefix(ctx, admin, r1.ID, "ali"); !errors.Is(err, ErrForbidden) {
		t.Errorf("an admin's prefix: %v", err)
	}
	if err := e.svc.SetResellerConfigPrefix(ctx, e.owner, r1.ID, "newone"); err != nil {
		t.Fatalf("the owner changes it: %v", err)
	}
	if err := e.svc.SetResellerConfigPrefix(ctx, e.owner, r1.ID, ""); err != nil {
		t.Fatalf("the owner clears it: %v", err)
	}
	if n, _ := e.svc.ResellerNaming(ctx, r1.ID); n.ConfigPrefix != "" {
		t.Fatalf("prefix %q after clearing", n.ConfigPrefix)
	}
	if err := e.svc.SetResellerConfigPrefix(ctx, r1, r1.ID, "again"); err != nil { // none now: the reseller may set it once more
		t.Fatalf("after the owner cleared it: %v", err)
	}
	if err := e.svc.SetResellerPanelGroup(ctx, e.owner, r1.ID, ""); err != nil {
		t.Fatal(err)
	}
	if n, _ := e.svc.ResellerNaming(ctx, r1.ID); n.PanelGroup != "" {
		t.Fatalf("group %q after clearing", n.PanelGroup)
	}

	// Every change is in the audit log.
	var n int
	if err := e.s.DB().QueryRow(ctx, `SELECT count(*) FROM core.audit_log WHERE action IN ('reseller.panel_group', 'reseller.config_prefix') AND entity_id = $1`, r1.ID).Scan(&n); err != nil || n != 6 {
		t.Fatalf("audit entries %d %v", n, err)
	}
}

// A reseller's new configs carry their prefix and go in their group; a customer's (and a reseller's with nothing set) are named as ever
// and go in the default group; the name is made once, so a retried create keeps it; a renewal changes neither.
func TestResellerConfigsAreNamedAndGrouped(t *testing.T) {
	e := newNamingEnv(t)
	ctx := context.Background()
	r := e.reseller(9821)
	plain := e.reseller(9822)
	customer := seedUser(t, e.s, 9823)
	fund(t, e.s, customer.ID, e.plan.Price)
	if err := e.svc.SetResellerPanelGroup(ctx, e.owner, r.ID, "SOBHANADMIN"); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.SetResellerConfigPrefix(ctx, r, r.ID, "sob"); err != nil {
		t.Fatal(err)
	}

	fp := &fakeProvisioner{}
	e.buy(r, fp, "n-r")
	e.buy(plain, fp, "n-plain")
	e.buy(customer, fp, "n-customer")
	if len(fp.calls) != 3 || len(fp.groups) != 3 {
		t.Fatalf("calls %v groups %v", fp.calls, fp.groups)
	}
	email := func(i int) string { return strings.SplitN(fp.calls[i], "|", 2)[1] }
	if !strings.HasPrefix(email(0), "sob-u9821-") || fp.groups[0] != "BOBRES-SOBHANADMIN" {
		t.Errorf("the reseller's config: %q in %q", email(0), fp.groups[0])
	}
	for i, tg := range map[int]string{1: "u9822-", 2: "u9823-"} {
		if !strings.HasPrefix(email(i), tg) || fp.groups[i] != "" {
			t.Errorf("config %d: %q in %q", i, email(i), fp.groups[i])
		}
	}
	for i := range fp.calls {
		if err := xui.ValidateIdentifier(email(i)); err != nil {
			t.Errorf("%q is not a valid client name: %v", email(i), err)
		}
	}
	// The name is the one stored on the subscription, so it does not follow a later change of the prefix.
	if err := e.svc.SetResellerConfigPrefix(ctx, e.owner, r.ID, "other"); err != nil {
		t.Fatal(err)
	}
	sub, err := e.s.SubscriptionByOrder(ctx, e.s.Conn(), e.mustOrder(r.ID))
	if err != nil || !strings.HasPrefix(sub.ClientEmail, "sob-u9821-") {
		t.Fatalf("stored name %q %v", sub.ClientEmail, err)
	}
	e.buy(r, fp, "n-r2")
	if !strings.HasPrefix(email(3), "other-u9821-") {
		t.Errorf("the next config: %q", email(3))
	}
}

// mustOrder is the id of the first order of a user.
func (e *namingEnv) mustOrder(userID string) string {
	e.t.Helper()
	var id string
	if err := e.s.DB().QueryRow(context.Background(), `SELECT id::text FROM core.orders WHERE user_id = $1 ORDER BY created_at LIMIT 1`, userID).Scan(&id); err != nil {
		e.t.Fatal(err)
	}
	return id
}

// The configs a reseller made before they had a group are handed to the provisioner once the owner gives one, and again when
// the owner asks; a customer's are not; a failure leaves the reseller waiting; a group changed meanwhile is done next.
func TestResellerGroupSyncMovesExistingConfigs(t *testing.T) {
	e := newNamingEnv(t)
	ctx := context.Background()
	r := e.reseller(9831)
	customer := seedUser(t, e.s, 9832)
	fund(t, e.s, customer.ID, e.plan.Price*3)

	fp := &fakeProvisioner{}
	e.buy(r, fp, "s-1")
	e.buy(r, fp, "s-2")
	e.buy(customer, fp, "s-c")
	w := e.svc.NewResellerGroupSync(fp, nil)

	// No group yet: nothing to do.
	if n, err := w.Step(ctx); err != nil || n != 0 || fp.assignCalls != 0 {
		t.Fatalf("before a group: %d %v %d", n, err, fp.assignCalls)
	}
	if err := e.svc.SetResellerPanelGroup(ctx, e.owner, r.ID, "SOBHANADMIN"); err != nil {
		t.Fatal(err)
	}
	fp.groupErr = errors.New("provisioner is down")
	if n, err := w.Step(ctx); err == nil || n != 0 {
		t.Fatalf("a failing provisioner: %d %v", n, err)
	}
	fp.groupErr = nil
	if n, err := w.Step(ctx); err != nil || n != 1 {
		t.Fatalf("Step: %d %v", n, err)
	}
	subs, _ := e.s.SubscriptionIDsOf(ctx, e.s.Conn(), r.ID, "", 10)
	custSubs, _ := e.s.SubscriptionIDsOf(ctx, e.s.Conn(), customer.ID, "", 10)
	if len(subs) != 2 || len(custSubs) != 1 || len(fp.assigned) != 2 {
		t.Fatalf("reseller %v customer %v assigned %v", subs, custSubs, fp.assigned)
	}
	for _, id := range subs {
		if fp.assigned[id] != "BOBRES-SOBHANADMIN" {
			t.Errorf("subscription %s: %q", id, fp.assigned[id])
		}
	}
	// Done: another look does nothing.
	calls := fp.assignCalls
	if n, err := w.Step(ctx); err != nil || n != 0 || fp.assignCalls != calls {
		t.Fatalf("a second step: %d %v", n, err)
	}
	// The owner asks again, or changes the name: the reseller is done again, with the new group.
	if err := e.svc.RequestResellerGroupSync(ctx, e.owner, r.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.SetResellerPanelGroup(ctx, e.owner, r.ID, "NEWNAME"); err != nil {
		t.Fatal(err)
	}
	if n, err := w.Step(ctx); err != nil || n != 1 {
		t.Fatalf("after a change: %d %v", n, err)
	}
	for _, id := range subs {
		if fp.assigned[id] != "BOBRES-NEWNAME" {
			t.Errorf("subscription %s after the change: %q", id, fp.assigned[id])
		}
	}
	// Taking the group away hands them back to the default one only when asked (a reseller without a group is not listed).
	if err := e.svc.SetResellerPanelGroup(ctx, e.owner, r.ID, ""); err != nil {
		t.Fatal(err)
	}
	if n, _ := w.Step(ctx); n != 0 {
		t.Fatalf("a reseller without a group was done: %d", n)
	}
	if err := e.svc.RequestResellerGroupSync(ctx, e.owner, r.ID); !errors.Is(err, ErrInvalid) {
		t.Errorf("a sync request without a group: %v", err)
	}
	for name, actor := range map[string]*store.User{"the reseller": r, "nobody": nil} {
		if err := e.svc.RequestResellerGroupSync(ctx, actor, r.ID); !errors.Is(err, ErrForbidden) {
			t.Errorf("%s asked for a sync: %v", name, err)
		}
	}
}
