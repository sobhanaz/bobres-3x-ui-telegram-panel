package domain

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
)

// fakeServerID is the server fakeProvisioner.CreateClient delivers to.
const fakeServerID = "00000000-0000-7000-8000-00000000c0de"

func (f *fakeProvisioner) CheckClientName(_ context.Context, name string) (NameCheck, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case f.checkErr != nil:
		return NameCheck{}, f.checkErr
	case f.panelNames[name]:
		return NameCheck{Reason: NameTaken}, nil
	case f.checkReason != "":
		return NameCheck{Reason: f.checkReason}, nil
	}
	return NameCheck{Available: true}, nil
}

func (f *fakeProvisioner) RenameClient(_ context.Context, subscriptionID, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.renameErr != nil {
		return f.renameErr
	}
	f.renamed = append(f.renamed, subscriptionID+"|"+name)
	return nil
}

func (f *fakeProvisioner) takeOnPanel(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.panelNames == nil {
		f.panelNames = map[string]bool{}
	}
	f.panelNames[name] = true
}

// renamingServers is a provisioner whose fake server renames (renames) or not.
func renamingServers(renames bool) *fakeServers {
	return &fakeServers{servers: []Server{{ID: fakeServerID, Name: "fake", Enabled: true, RenamesClients: renames}}}
}

// namesOn switches config names on (or off) for customers and resellers.
func namesOn(t *testing.T, svc *Service, owner *store.User, customers, resellers bool) {
	t.Helper()
	b := func(v bool) string {
		if v {
			return "true"
		}
		return "false"
	}
	if _, err := svc.SetSettings(context.Background(), owner, map[string]string{
		SettingConfigNamesCustomers: b(customers), SettingConfigNamesResellers: b(resellers),
	}); err != nil {
		t.Fatal(err)
	}
	svc.renames = renameCache{} // a test's servers are its own
}

func TestNormalizeConfigName(t *testing.T) {
	for in, want := range map[string]string{
		"sobhan1234": "sobhan1234", " Sobhan1234 ": "sobhan1234", "ab12": "ab12", "1abc": "1abc",
		"sobhan۱۲۳۴": "sobhan1234", "abcdefghij0123456789": "abcdefghij0123456789", "ubuntu22": "ubuntu22", "user1": "user1",
	} {
		if got, err := NormalizeConfigName(in); err != nil || got != want {
			t.Errorf("%q: %q %v", in, got, err)
		}
	}
	for in, reason := range map[string]string{
		"": NameLength, "ab1": NameLength, "abcdefghij01234567890": NameLength,
		"sob-han1": NameChars, "sob han1": NameChars, "سلام1234": NameChars, "sobhan_1": NameChars, "sob.han1": NameChars,
		"sobhanaz": NameMix, "12345678": NameMix,
		"u1234abc": NameAutomatic, "u1abc": NameAutomatic, "U42sobhan": NameAutomatic,
	} {
		_, err := NormalizeConfigName(in)
		if got := ConfigNameReason(err); got != reason || !errors.Is(err, ErrInvalid) {
			t.Errorf("%q: reason %q (%v), want %q", in, got, err, reason)
		}
	}
}

func TestCheckConfigName(t *testing.T) {
	e := newNamingEnv(t)
	ctx := context.Background()
	fp := &fakeProvisioner{}
	cust := seedUser(t, e.s, 7101)
	check := func(u *store.User, name string) *ConfigNameCheck {
		t.Helper()
		res, err := e.svc.CheckConfigName(ctx, fp, u.ID, name, "")
		if err != nil {
			t.Fatalf("check %q: %v", name, err)
		}
		return res
	}
	// Off by default, for everyone.
	if res := check(cust, "sobhan1234"); res.OK || res.Reason != NameOff {
		t.Fatalf("off by default: %+v", res)
	}
	namesOn(t, e.svc, e.owner, true, false)
	if res := check(cust, " Sobhan1234 "); !res.OK || res.Name != "sobhan1234" {
		t.Fatalf("a free name: %+v", res)
	}
	if res := check(cust, "abc"); res.OK || res.Reason != NameLength {
		t.Fatalf("too short: %+v", res)
	}
	// A reseller is off while only customers are on; on, their prefix stays in front.
	r := e.reseller(7102)
	if err := e.svc.SetResellerConfigPrefix(ctx, r, r.ID, "sob"); err != nil {
		t.Fatal(err)
	}
	if res := check(r, "sobhan1234"); res.Reason != NameOff {
		t.Fatalf("a reseller with reseller names off: %+v", res)
	}
	namesOn(t, e.svc, e.owner, true, true)
	if res := check(r, "sobhan1234"); !res.OK || res.Name != "sob-sobhan1234" {
		t.Fatalf("a reseller's name: %+v", res)
	}
	// Taken on a panel; a panel that cannot be asked, or a provisioner that does not answer: never "free".
	fp.takeOnPanel("sobhan1234")
	if res := check(cust, "sobhan1234"); res.OK || res.Reason != NameTaken {
		t.Fatalf("taken on a panel: %+v", res)
	}
	if res := check(r, "sobhan1234"); !res.OK {
		t.Fatalf("the reseller's full name is another name: %+v", res)
	}
	fp.checkReason = NameUnchecked
	if res := check(cust, "other1234"); res.OK || res.Reason != NameUnchecked {
		t.Fatalf("a panel not asked: %+v", res)
	}
	fp.checkReason, fp.checkErr = "", errors.New("provisioner down")
	if res := check(cust, "other1234"); res.OK || res.Reason != NameUnchecked {
		t.Fatalf("the provisioner down: %+v", res)
	}
	fp.checkErr = nil

	// An order on its way holds its name from other buyers (not from its own buyer).
	plan := planWithLimits(t, e.s, 30, 1<<30)
	if _, err := e.svc.CreateOrder(ctx, CreateOrderParams{UserID: cust.ID, PlanID: plan.ID, IdempotencyKey: "held", ClientName: "held1234", Names: fp}); err != nil {
		t.Fatal(err)
	}
	other := seedUser(t, e.s, 7103)
	if res := check(other, "held1234"); res.OK || res.Reason != NameTaken {
		t.Fatalf("a name an unpaid order holds: %+v", res)
	}
	if res := check(cust, "held1234"); !res.OK {
		t.Fatalf("the buyer's own order holds the name from them: %+v", res)
	}
	// A service's name is taken (in core's records, whatever the panels say).
	_, o := paidWalletOrder(t, e.svc, e.s, 7104, plan)
	if _, err := e.svc.NewProvisionWorker(fp, nil).ProvisionNext(ctx); err != nil {
		t.Fatal(err)
	}
	sub, err := e.s.SubscriptionByOrder(ctx, e.s.Conn(), o.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.s.SetSubscriptionClientEmail(ctx, e.s.Conn(), sub.ID, "mine5678"); err != nil {
		t.Fatal(err)
	}
	if res := check(cust, "mine5678"); res.OK || res.Reason != NameTaken {
		t.Fatalf("a service's name: %+v", res)
	}
	// Someone else's service is not found.
	if _, err := e.svc.CheckConfigName(ctx, fp, cust.ID, "abcd1234", sub.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("another user's service: %v", err)
	}
}

func TestOrderWithConfigNameIsDeliveredUnderIt(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	owner := staffMember(t, s, 7200, "owner")
	plan := planWithLimits(t, s, 30, 1<<30)
	u := seedUser(t, s, 7201)
	fund(t, s, u.ID, 10*plan.Price)
	fp := &fakeProvisioner{}

	// Off: the name is refused, and no order is made.
	_, err := svc.CreateOrder(ctx, CreateOrderParams{UserID: u.ID, PlanID: plan.ID, IdempotencyKey: "n1", ClientName: "sobhan1234", Names: fp})
	if ConfigNameReason(err) != NameOff {
		t.Fatalf("names off: %v", err)
	}
	namesOn(t, svc, owner, true, false)
	for name, reason := range map[string]string{"sob": NameLength, "u12abc": NameAutomatic} {
		if _, err := svc.CreateOrder(ctx, CreateOrderParams{UserID: u.ID, PlanID: plan.ID, IdempotencyKey: "bad-" + name, ClientName: name, Names: fp}); ConfigNameReason(err) != reason {
			t.Fatalf("%q: %v, want %s", name, err, reason)
		}
	}
	fp.takeOnPanel("taken1234")
	if _, err := svc.CreateOrder(ctx, CreateOrderParams{UserID: u.ID, PlanID: plan.ID, IdempotencyKey: "n2", ClientName: "taken1234", Names: fp}); ConfigNameReason(err) != NameTaken {
		t.Fatalf("a name a panel has: %v", err)
	}

	o, err := svc.CreateOrder(ctx, CreateOrderParams{UserID: u.ID, PlanID: plan.ID, IdempotencyKey: "n3", ClientName: "Sobhan1234", Names: fp})
	if err != nil || derefStr(o.ClientName) != "sobhan1234" {
		t.Fatalf("order = %+v, %v", o, err)
	}
	// A retry of the same order keeps it, even once its name is taken (by itself).
	again, err := svc.CreateOrder(ctx, CreateOrderParams{UserID: u.ID, PlanID: plan.ID, IdempotencyKey: "n3", ClientName: "Sobhan1234", Names: fp})
	if err != nil || again.ID != o.ID {
		t.Fatalf("retry = %+v, %v", again, err)
	}
	if _, err := svc.PayOrderWithWallet(ctx, o.ID, u.ID); err != nil {
		t.Fatal(err)
	}
	if worked, err := svc.NewProvisionWorker(fp, nil).ProvisionNext(ctx); err != nil || !worked {
		t.Fatalf("provisioning: %v %v", worked, err)
	}
	sub, err := s.SubscriptionByOrder(ctx, s.Conn(), o.ID)
	if err != nil || sub.ClientEmail != "sobhan1234" || sub.Status != "active" {
		t.Fatalf("service = %+v, %v", sub, err)
	}
	if fp.calls[len(fp.calls)-1] != sub.ID+"|sobhan1234" {
		t.Fatalf("panel calls: %v", fp.calls)
	}
	evs := outboxEvents(t, s, events.SubscriptionProvisioned)
	var ev events.SubscriptionProvisionedEvent
	if len(evs) != 1 || json.Unmarshal(evs[0], &ev) != nil || ev.ClientName != "sobhan1234" || ev.NameTaken != "" {
		t.Fatalf("event: %s", evs)
	}
	// After delivery the same retry still answers with the order.
	if again, err := svc.CreateOrder(ctx, CreateOrderParams{UserID: u.ID, PlanID: plan.ID, IdempotencyKey: "n3", ClientName: "sobhan1234", Names: fp}); err != nil || again.ID != o.ID {
		t.Fatalf("retry after delivery = %+v, %v", again, err)
	}
	// A renewal never takes a name.
	ren, err := svc.CreateOrder(ctx, CreateOrderParams{UserID: u.ID, PlanID: plan.ID, Type: OrderRenew, SubscriptionID: sub.ID, IdempotencyKey: "n4", ClientName: "renew1234", Names: fp})
	if err != nil || ren.ClientName != nil {
		t.Fatalf("renewal = %+v, %v", ren, err)
	}
	// No name: the automatic one, as ever.
	_, plain := paidWalletOrder(t, svc, s, 7202, plan)
	if _, err := svc.NewProvisionWorker(fp, nil).ProvisionNext(ctx); err != nil {
		t.Fatal(err)
	}
	if sub, _ := s.SubscriptionByOrder(ctx, s.Conn(), plain.ID); !strings.HasPrefix(sub.ClientEmail, "u7202-") {
		t.Fatalf("automatic name = %q", sub.ClientEmail)
	}
}

// A panel that took the name between the check and the delivery: the paid order is delivered under the automatic name
// and the customer is told, never left undelivered.
func TestConfigNameTakenAfterPaymentFallsBackToTheAutomaticName(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	owner := staffMember(t, s, 7300, "owner")
	namesOn(t, svc, owner, true, false)
	plan := planWithLimits(t, s, 30, 1<<30)
	u := seedUser(t, s, 7301)
	fund(t, s, u.ID, plan.Price)
	fp := &fakeProvisioner{}
	o, err := svc.CreateOrder(ctx, CreateOrderParams{UserID: u.ID, PlanID: plan.ID, IdempotencyKey: "late", ClientName: "late1234", Names: fp})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PayOrderWithWallet(ctx, o.ID, u.ID); err != nil {
		t.Fatal(err)
	}
	fp.takeOnPanel("late1234") // an operator made it in the panel meanwhile
	if worked, err := svc.NewProvisionWorker(fp, nil).ProvisionNext(ctx); err != nil || !worked {
		t.Fatalf("provisioning: %v %v", worked, err)
	}
	got, _ := s.GetOrder(ctx, s.Conn(), o.ID)
	sub, err := s.SubscriptionByOrder(ctx, s.Conn(), o.ID)
	if err != nil || got.Status != "active" || sub.Status != "active" || !strings.HasPrefix(sub.ClientEmail, "u7301-") {
		t.Fatalf("order %q, service %+v, %v", got.Status, sub, err)
	}
	evs := outboxEvents(t, s, events.SubscriptionProvisioned)
	var ev events.SubscriptionProvisionedEvent
	if len(evs) != 1 || json.Unmarshal(evs[0], &ev) != nil || ev.NameTaken != "late1234" || ev.ClientName != "" {
		t.Fatalf("event: %s", evs)
	}
	if len(outboxEvents(t, s, events.ProvisionFailed)) != 0 {
		t.Fatal("a delivery that fell back was announced as failed")
	}
}

// A name another service took in core's records before the delivery started: the automatic name, without asking the
// panel for the name at all.
func TestConfigNameTakenInCoreBeforeDelivery(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	owner := staffMember(t, s, 7400, "owner")
	namesOn(t, svc, owner, true, false)
	plan := planWithLimits(t, s, 30, 1<<30)
	fp := &fakeProvisioner{}
	u := seedUser(t, s, 7401)
	fund(t, s, u.ID, plan.Price)
	o, err := svc.CreateOrder(ctx, CreateOrderParams{UserID: u.ID, PlanID: plan.ID, IdempotencyKey: "core", ClientName: "core1234", Names: fp})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PayOrderWithWallet(ctx, o.ID, u.ID); err != nil {
		t.Fatal(err)
	}
	// Staff renamed another service to that name meanwhile.
	_, other := paidWalletOrder(t, svc, s, 7402, plan)
	w := svc.NewProvisionWorker(fp, nil)
	for i := 0; i < 2; i++ {
		if _, err := w.ProvisionNext(ctx); err != nil {
			t.Fatal(err)
		}
	}
	// Both are delivered by now; make the second order's case: give the first service's name away and deliver again.
	sub, _ := s.SubscriptionByOrder(ctx, s.Conn(), o.ID)
	if sub.ClientEmail != "core1234" {
		t.Fatalf("first service = %q", sub.ClientEmail)
	}
	otherSub, _ := s.SubscriptionByOrder(ctx, s.Conn(), other.ID)
	if err := s.SetSubscriptionClientEmail(ctx, s.Conn(), otherSub.ID, "core1234"); !errors.Is(err, store.ErrClientNameTaken) {
		t.Fatalf("two services with one name: %v", err)
	}
	u2 := seedUser(t, s, 7403)
	fund(t, s, u2.ID, plan.Price)
	if _, err := svc.CreateOrder(ctx, CreateOrderParams{UserID: u2.ID, PlanID: plan.ID, IdempotencyKey: "core2", ClientName: "core1234", Names: fp}); ConfigNameReason(err) != NameTaken {
		t.Fatalf("a delivered service's name: %v", err)
	}
	// An order whose name a service got after the order was made (the hold of an unpaid order is a day): automatic name.
	o3, err := svc.CreateOrder(ctx, CreateOrderParams{UserID: u2.ID, PlanID: plan.ID, IdempotencyKey: "core3", ClientName: "free5678", Names: fp})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetSubscriptionClientEmail(ctx, s.Conn(), otherSub.ID, "free5678"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PayOrderWithWallet(ctx, o3.ID, u2.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := w.ProvisionNext(ctx); err != nil {
		t.Fatal(err)
	}
	if sub3, _ := s.SubscriptionByOrder(ctx, s.Conn(), o3.ID); !strings.HasPrefix(sub3.ClientEmail, "u7403-") || sub3.Status != "active" {
		t.Fatalf("third service = %+v", sub3)
	}
}

func TestRenameOwnService(t *testing.T) {
	e := newNamingEnv(t)
	ctx := context.Background()
	fp := &fakeProvisioner{}
	servers := renamingServers(true)
	_, o := paidWalletOrder(t, e.svc, e.s, 7501, e.plan)
	if _, err := e.svc.NewProvisionWorker(fp, nil).ProvisionNext(ctx); err != nil {
		t.Fatal(err)
	}
	sub, _ := e.s.SubscriptionByOrder(ctx, e.s.Conn(), o.ID)
	cust, _ := e.s.GetUser(ctx, e.s.Conn(), sub.UserID)
	rename := func(name string) *RenameResult {
		t.Helper()
		res, err := e.svc.RenameOwnService(ctx, fp, servers, cust.ID, sub.ID, name)
		if err != nil {
			t.Fatalf("rename %q: %v", name, err)
		}
		return res
	}
	if e.svc.RenamableFor(ctx, servers, cust)(sub) {
		t.Fatal("renamable while config names are off")
	}
	if res := rename("sobhan1234"); res.OK || res.Reason != NameOff {
		t.Fatalf("names off: %+v", res)
	}
	namesOn(t, e.svc, e.owner, true, false)
	if !e.svc.RenamableFor(ctx, servers, cust)(sub) {
		t.Fatal("not renamable with config names on")
	}
	if res := rename("so"); res.OK || res.Reason != NameLength {
		t.Fatalf("a bad name: %+v", res)
	}
	fp.takeOnPanel("taken1234")
	if res := rename("taken1234"); res.OK || res.Reason != NameTaken {
		t.Fatalf("a taken name: %+v", res)
	}
	res := rename("Sobhan1234")
	if !res.OK || res.Name != "sobhan1234" || res.Service.ClientEmail != "sobhan1234" || res.Service.SubLink != sub.SubLink {
		t.Fatalf("rename = %+v", res)
	}
	if len(fp.renamed) != 1 || fp.renamed[0] != sub.ID+"|sobhan1234" {
		t.Fatalf("panel renames: %v", fp.renamed)
	}
	var audits int
	if err := e.s.Conn().QueryRow(ctx, `SELECT count(*) FROM core.audit_log WHERE action = 'subscription.rename' AND entity_id = $1 AND actor_id = $2`, sub.ID, cust.ID).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("audit entries: %d %v", audits, err)
	}
	// The same name again changes nothing on the panel.
	if res := rename("sobhan1234"); !res.OK || len(fp.renamed) != 1 {
		t.Fatalf("the same name again: %+v, %v", res, fp.renamed)
	}
	// A panel that refuses at the last moment: taken, and core keeps the name it had.
	fp.renameErr = ErrClientNameTaken
	if res := rename("race1234"); res.OK || res.Reason != NameTaken {
		t.Fatalf("a panel's refusal: %+v", res)
	}
	fp.renameErr = nil
	if got, _ := e.s.GetSubscription(ctx, e.s.Conn(), sub.ID); got.ClientEmail != "sobhan1234" {
		t.Fatalf("name after a refusal = %q", got.ClientEmail)
	}
	// A server that cannot keep the link (Marzban, S-UI): no Rename, and a rename is refused.
	e.svc.renames = renameCache{}
	marz := renamingServers(false)
	if e.svc.RenamableFor(ctx, marz, cust)(sub) {
		t.Fatal("renamable on a server that does not rename")
	}
	if res, err := e.svc.RenameOwnService(ctx, fp, marz, cust.ID, sub.ID, "other1234"); err != nil || res.OK || res.Reason != NameNotRenamable {
		t.Fatalf("on a server that does not rename: %+v %v", res, err)
	}
	// Someone else's service.
	stranger := seedUser(t, e.s, 7502)
	if _, err := e.svc.RenameOwnService(ctx, fp, servers, stranger.ID, sub.ID, "other1234"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("another user's service: %v", err)
	}
}

func TestResellerRenameKeepsThePrefix(t *testing.T) {
	e := newNamingEnv(t)
	ctx := context.Background()
	fp := &fakeProvisioner{}
	servers := renamingServers(true)
	namesOn(t, e.svc, e.owner, false, true)
	r := e.reseller(7601)
	if err := e.svc.SetResellerConfigPrefix(ctx, r, r.ID, "sob"); err != nil {
		t.Fatal(err)
	}
	o, err := e.svc.CreateOrder(ctx, CreateOrderParams{UserID: r.ID, PlanID: e.plan.ID, IdempotencyKey: "r1", Label: "Ali", ClientName: "ali1234", Names: fp})
	if err != nil || derefStr(o.ClientName) != "sob-ali1234" {
		t.Fatalf("order = %+v, %v", o, err)
	}
	if _, err := e.svc.PayOrderWithWallet(ctx, o.ID, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.NewProvisionWorker(fp, nil).ProvisionNext(ctx); err != nil {
		t.Fatal(err)
	}
	sub, _ := e.s.SubscriptionByOrder(ctx, e.s.Conn(), o.ID)
	if sub.ClientEmail != "sob-ali1234" {
		t.Fatalf("service name = %q", sub.ClientEmail)
	}
	res, err := e.svc.RenameOwnService(ctx, fp, servers, r.ID, sub.ID, "reza5678")
	if err != nil || !res.OK || res.Service.ClientEmail != "sob-reza5678" {
		t.Fatalf("rename = %+v, %v", res, err)
	}
	// Staff rename it too (whatever the settings), the prefix staying; a reason is needed and it is audited.
	namesOn(t, e.svc, e.owner, false, false)
	if _, err := e.svc.RenameSubscription(ctx, e.owner, fp, servers, sub.ID, "staff123", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("no reason: %v", err)
	}
	got, err := e.svc.RenameSubscription(ctx, e.owner, fp, servers, sub.ID, "staff123", "customer asked")
	if err != nil || got.ClientEmail != "sob-staff123" {
		t.Fatalf("staff rename = %+v, %v", got, err)
	}
	if _, err := e.svc.RenameSubscription(ctx, e.owner, fp, servers, sub.ID, "bad name", "x"); ConfigNameReason(err) != NameChars {
		t.Fatalf("staff, a bad name: %v", err)
	}
	e.svc.renames = renameCache{}
	if _, err := e.svc.RenameSubscription(ctx, e.owner, fp, renamingServers(false), sub.ID, "staff456", "x"); !errors.Is(err, ErrState) {
		t.Fatalf("staff, a server that does not rename: %v", err)
	}
}
