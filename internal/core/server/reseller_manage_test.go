package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	commonv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/common/v1"
	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// panelFake is the provisioner as the reseller RPCs use it: it records the
// panel calls and answers a usage read.
type panelFake struct {
	domain.Provisioner
	domain.ServerAdmin
	mu  sync.Mutex
	ops []string
	err error
}

func (p *panelFake) op(id, what string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return p.err
	}
	p.ops = append(p.ops, id+"|"+what)
	return nil
}

func (p *panelFake) SetEnabled(_ context.Context, id string, on bool) error {
	if on {
		return p.op(id, "enable")
	}
	return p.op(id, "disable")
}
func (p *panelFake) Delete(_ context.Context, id string) error { return p.op(id, "delete") }
func (p *panelFake) Usage(_ context.Context, _ string) (domain.Usage, error) {
	return domain.Usage{TotalBytes: 50 << 30, ExpiresAt: time.Now().Add(20 * 24 * time.Hour).Unix(), Enabled: true}, nil
}
func (p *panelFake) done() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.ops...)
}

// A reseller's management calls: the list with filters and counts, pause and
// resume, delete, notes and the customers who joined through the link, each
// refused for anyone else, for a service that is not theirs, for a banned
// reseller and once resellers left the license.
func TestResellerManagementOverGRPC(t *testing.T) {
	prov := &panelFake{}
	f := newFixtureWith(t, func(s *Server) { s.SetProvisioner(prov) })
	c := f.client(t, botToken)
	ctx := context.Background()
	// The trial runs (resellers are in it); other tests leave it as they found it.
	f.exec(t, `UPDATE core.install SET trial_started_at = now(), limit_hit_at = NULL`)
	t.Cleanup(func() { f.exec(t, `UPDATE core.install SET trial_started_at = now(), limit_hit_at = NULL`) })

	dom := domain.New(f.st, domain.Config{})
	plan := &store.Plan{NameI18n: map[string]string{"en": "M"}, Kind: "time", DurationDays: ptr32(30), Price: 100_000, Currency: "IRT", Enabled: true}
	if err := f.st.UpsertPlan(ctx, f.st.Conn(), plan); err != nil {
		t.Fatal(err)
	}
	adminU, _ := c.UpsertUser(ctx, &corev1.UpsertUserRequest{TelegramId: 7060})
	if err := f.st.SetUserRole(ctx, f.st.Conn(), adminU.GetId(), "admin", "active"); err != nil {
		t.Fatal(err)
	}
	admin, _ := f.st.GetUser(ctx, f.st.Conn(), adminU.GetId())
	tier, err := dom.CreateTier(ctx, admin, domain.TierInput{Name: "Gold", DiscountPercent: 20, CommissionPercent: 10}, "")
	if err != nil {
		t.Fatal(err)
	}
	mkReseller := func(tg int64) *corev1.User {
		t.Helper()
		u, _ := c.UpsertUser(ctx, &corev1.UpsertUserRequest{TelegramId: tg})
		if _, err := dom.MakeReseller(ctx, admin, u.GetId(), domain.ResellerInput{TierID: tier.ID}, "shop"); err != nil {
			t.Fatal(err)
		}
		return u
	}
	r, r2 := mkReseller(7061), mkReseller(7062)
	cust, _ := c.UpsertUser(ctx, &corev1.UpsertUserRequest{TelegramId: 7063})

	n := 0
	seed := func(userID, label, statusName string) string {
		t.Helper()
		n++
		o, _, err := f.st.CreateOrder(ctx, f.st.Conn(), &store.Order{UserID: userID, PlanID: plan.ID, Type: "new", Status: "active",
			Amount: 1, Currency: "IRT", IdempotencyKey: fmt.Sprintf("mgmt-%d", n)})
		if err != nil {
			t.Fatal(err)
		}
		var name *string
		if label != "" {
			name = &label
		}
		sub, err := f.st.CreateSubscription(ctx, f.st.Conn(), &store.Subscription{UserID: userID, OrderID: o.ID, ClientEmail: fmt.Sprintf("mgmt-%d", n), Label: name})
		if err != nil {
			t.Fatal(err)
		}
		exp := time.Now().Add(20 * 24 * time.Hour)
		total := int64(50 << 30)
		if err := f.st.ActivateSubscription(ctx, f.st.Conn(), sub.ID, "00000000-0000-7000-8000-000000000001", "x", "https://sub.example/"+sub.ID, &exp, &total); err != nil {
			t.Fatal(err)
		}
		if statusName != "active" {
			f.exec(t, `UPDATE core.subscriptions SET status = $2, traffic_used_bytes = 7 WHERE id = $1`, sub.ID, statusName)
		}
		time.Sleep(5 * time.Millisecond)
		return sub.ID
	}
	active := seed(r.GetId(), "Ali", "active")
	seed(r.GetId(), "Sara", "expiring_soon")
	seed(r.GetId(), "Reza", "expired")
	seed(r.GetId(), "Mina", "depleted")
	seed(r.GetId(), "Omid", "disabled")
	theirs := seed(r2.GetId(), "Other", "active")
	custSub := seed(cust.GetId(), "", "active")

	page := func(p, size int32) *commonv1.Pagination { return &commonv1.Pagination{Page: p, PageSize: size} }
	list := func(userID, statusName, query string, p *commonv1.Pagination) (*corev1.ListResellerServicesResponse, error) {
		return c.ListResellerServices(ctx, &corev1.ListResellerServicesRequest{UserId: userID, Status: statusName, Query: query, Pagination: p})
	}

	// The list: counts for every status, each filter, a search, a page.
	all, err := list(r.GetId(), "", "", nil)
	if err != nil || len(all.GetSubscriptions()) != 5 || all.GetPageInfo().GetTotal() != 5 || all.GetPageInfo().GetPageSize() != 10 || all.GetPageInfo().GetPage() != 1 {
		t.Fatalf("all: %v %+v", err, all)
	}
	if k := all.GetCounts(); k.GetTotal() != 5 || k.GetActive() != 1 || k.GetExpiringSoon() != 1 || k.GetExpired() != 2 || k.GetPaused() != 1 || k.GetPending() != 0 {
		t.Fatalf("counts: %+v", k)
	}
	if first := all.GetSubscriptions()[0]; first.GetLabel() != "Omid" || first.GetStatus() != "disabled" || first.GetTrafficUsedBytes() != 7 ||
		first.GetTrafficTotalBytes() != 50<<30 || first.GetSubscriptionLink() == "" {
		t.Fatalf("newest first, with its usage: %+v", first)
	}
	for statusName, want := range map[string]int{"active": 1, "expiring_soon": 1, "expired": 2, "paused": 1, "pending": 0} {
		got, err := list(r.GetId(), statusName, "", nil)
		if err != nil || len(got.GetSubscriptions()) != want || got.GetPageInfo().GetTotal() != int64(want) || got.GetCounts().GetTotal() != 5 {
			t.Errorf("status %s: %v %+v", statusName, err, got)
		}
	}
	if got, err := list(r.GetId(), "expired", "MIN", nil); err != nil || len(got.GetSubscriptions()) != 1 || got.GetSubscriptions()[0].GetLabel() != "Mina" {
		t.Errorf("search in a status: %v %+v", err, got)
	}
	if got, err := list(r.GetId(), "", "", page(2, 2)); err != nil || len(got.GetSubscriptions()) != 2 || got.GetPageInfo().GetTotal() != 5 || got.GetPageInfo().GetPage() != 2 {
		t.Errorf("page 2 of 2 a page: %v %+v", err, got)
	}
	if got, err := list(r.GetId(), "", "", page(1, 500)); err != nil || got.GetPageInfo().GetPageSize() != 50 {
		t.Errorf("page size capped at 50: %v %+v", err, got.GetPageInfo())
	}
	if _, err := list(r.GetId(), "deleted", "", nil); status.Code(err) != codes.InvalidArgument {
		t.Errorf("status deleted: %v", err)
	}
	if _, err := list(r.GetId(), "", strings.Repeat("x", 2001), nil); status.Code(err) != codes.InvalidArgument {
		t.Errorf("a long search: %v", err)
	}
	if got, err := list(r2.GetId(), "", "", nil); err != nil || len(got.GetSubscriptions()) != 1 || got.GetSubscriptions()[0].GetId() != theirs {
		t.Errorf("another reseller's list: %v %+v", err, got)
	}

	// Pause and resume: the panel client is turned off and on, the audit log
	// says who and where, and a second call changes nothing.
	got, err := c.SetResellerServiceEnabled(ctx, &corev1.SetResellerServiceEnabledRequest{UserId: r.GetId(), SubscriptionId: active, Enabled: false})
	if err != nil || got.GetStatus() != "disabled" || got.GetId() != active {
		t.Fatalf("pause: %v %+v", err, got)
	}
	if _, err := c.SetResellerServiceEnabled(ctx, &corev1.SetResellerServiceEnabledRequest{UserId: r.GetId(), SubscriptionId: active, Enabled: false}); err != nil {
		t.Fatalf("pause again: %v", err)
	}
	if ops := prov.done(); len(ops) != 1 || ops[0] != active+"|disable" {
		t.Fatalf("panel calls: %v", ops)
	}
	rows, _, _ := f.st.ListAudit(ctx, f.st.Conn(), store.AuditFilter{Action: "subscription.disable", EntityID: active}, store.Page{})
	if len(rows) != 1 || rows[0].ActorID == nil || *rows[0].ActorID != r.GetId() || rows[0].Source != store.SourceBot {
		t.Fatalf("audit: %+v", rows)
	}
	if got, err := c.SetResellerServiceEnabled(ctx, &corev1.SetResellerServiceEnabledRequest{UserId: r.GetId(), SubscriptionId: active, Enabled: true}); err != nil || got.GetStatus() != "active" {
		t.Fatalf("resume: %v %+v", err, got)
	}
	// What staff paused, the reseller cannot resume.
	if _, err := dom.SetSubscriptionEnabled(ctx, admin, prov, active, false, "abuse"); err != nil {
		t.Fatal(err)
	}
	_, err = c.SetResellerServiceEnabled(ctx, &corev1.SetResellerServiceEnabledRequest{UserId: r.GetId(), SubscriptionId: active, Enabled: true})
	if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "paused by support") {
		t.Fatalf("resumed what staff paused: %v", err)
	}
	// The panel does not answer: Unavailable, nothing recorded.
	seeded := seed(r.GetId(), "Panel", "active")
	prov.mu.Lock()
	prov.err = status.Error(codes.Unavailable, "panel down")
	prov.mu.Unlock()
	if _, err := c.SetResellerServiceEnabled(ctx, &corev1.SetResellerServiceEnabledRequest{UserId: r.GetId(), SubscriptionId: seeded, Enabled: false}); status.Code(err) != codes.Unavailable {
		t.Fatalf("panel down: %v", err)
	}
	prov.mu.Lock()
	prov.err = nil
	prov.mu.Unlock()
	if sub, _ := f.st.GetSubscription(ctx, f.st.Conn(), seeded); sub.Status != "active" {
		t.Fatalf("a failed pause changed the status: %s", sub.Status)
	}

	// Notes: set, one line, 200 characters, cleared; they come back on the list.
	if got, err := c.SetSubscriptionNote(ctx, &corev1.SetSubscriptionNoteRequest{UserId: r.GetId(), SubscriptionId: active, Note: " pays on the 5th "}); err != nil || got.GetNote() != "pays on the 5th" {
		t.Fatalf("note: %v %+v", err, got)
	}
	if got, _ := list(r.GetId(), "paused", "", nil); len(got.GetSubscriptions()) != 2 {
		t.Fatalf("paused services: %+v", got.GetSubscriptions())
	}
	if got, _ := list(r.GetId(), "", "ali", nil); got.GetSubscriptions()[0].GetNote() != "pays on the 5th" {
		t.Fatalf("the note on the list: %+v", got.GetSubscriptions())
	}
	for name, note := range map[string]string{"two lines": "a\nb", "too long": strings.Repeat("x", 201)} {
		if _, err := c.SetSubscriptionNote(ctx, &corev1.SetSubscriptionNoteRequest{UserId: r.GetId(), SubscriptionId: active, Note: note}); status.Code(err) != codes.InvalidArgument {
			t.Errorf("%s: %v", name, err)
		}
	}
	if got, err := c.SetSubscriptionNote(ctx, &corev1.SetSubscriptionNoteRequest{UserId: r.GetId(), SubscriptionId: active}); err != nil || got.GetNote() != "" {
		t.Fatalf("clear: %v %+v", err, got)
	}

	// Delete: gone from the list and the panel, audited; again is fine.
	before := len(prov.done())
	if _, err := c.DeleteResellerService(ctx, &corev1.DeleteResellerServiceRequest{UserId: r.GetId(), SubscriptionId: seeded}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := c.DeleteResellerService(ctx, &corev1.DeleteResellerServiceRequest{UserId: r.GetId(), SubscriptionId: seeded}); err != nil {
		t.Fatalf("delete again: %v", err)
	}
	if ops := prov.done(); len(ops) != before+1 || ops[before] != seeded+"|delete" {
		t.Fatalf("panel calls: %v", ops)
	}
	if got, _ := list(r.GetId(), "", "", nil); got.GetCounts().GetTotal() != 5 || len(got.GetSubscriptions()) != 5 {
		t.Fatalf("the list after the delete: %+v", got.GetCounts())
	}
	rows, _, _ = f.st.ListAudit(ctx, f.st.Conn(), store.AuditFilter{Action: "subscription.delete", EntityID: seeded}, store.Page{})
	if len(rows) != 1 || *rows[0].ActorID != r.GetId() || rows[0].Source != store.SourceBot {
		t.Fatalf("audit of the delete: %+v", rows)
	}

	// Not theirs: another reseller's, a customer's, a missing and a malformed id.
	prov.mu.Lock()
	prov.ops = nil
	prov.mu.Unlock()
	for name, id := range map[string]string{"another reseller's": theirs, "a customer's": custSub, "missing": "00000000-0000-0000-0000-0000000000aa", "malformed": "x"} {
		if _, err := c.SetResellerServiceEnabled(ctx, &corev1.SetResellerServiceEnabledRequest{UserId: r.GetId(), SubscriptionId: id}); status.Code(err) != codes.NotFound {
			t.Errorf("pause %s: %v", name, err)
		}
		if _, err := c.DeleteResellerService(ctx, &corev1.DeleteResellerServiceRequest{UserId: r.GetId(), SubscriptionId: id}); status.Code(err) != codes.NotFound {
			t.Errorf("delete %s: %v", name, err)
		}
		if _, err := c.SetSubscriptionNote(ctx, &corev1.SetSubscriptionNoteRequest{UserId: r.GetId(), SubscriptionId: id, Note: "x"}); status.Code(err) != codes.NotFound {
			t.Errorf("note on %s: %v", name, err)
		}
	}
	if ops := prov.done(); len(ops) != 0 {
		t.Fatalf("the panel was called for services that are not theirs: %v", ops)
	}
	for _, id := range []string{theirs, custSub} {
		if sub, _ := f.st.GetSubscription(ctx, f.st.Conn(), id); sub.Status != "active" || sub.Note != nil {
			t.Errorf("a service that is not theirs changed: %+v", sub)
		}
	}

	// Anyone who is not a reseller: refused, everything.
	for _, who := range []string{cust.GetId(), adminU.GetId()} {
		if _, err := list(who, "", "", nil); status.Code(err) != codes.PermissionDenied {
			t.Errorf("list as %s: %v", who, err)
		}
		if _, err := c.SetResellerServiceEnabled(ctx, &corev1.SetResellerServiceEnabledRequest{UserId: who, SubscriptionId: custSub}); status.Code(err) != codes.PermissionDenied {
			t.Errorf("pause as %s: %v", who, err)
		}
		if _, err := c.DeleteResellerService(ctx, &corev1.DeleteResellerServiceRequest{UserId: who, SubscriptionId: custSub}); status.Code(err) != codes.PermissionDenied {
			t.Errorf("delete as %s: %v", who, err)
		}
		if _, err := c.ListInvitedCustomers(ctx, &corev1.ListInvitedCustomersRequest{UserId: who}); status.Code(err) != codes.PermissionDenied {
			t.Errorf("invited customers as %s: %v", who, err)
		}
	}
	if _, err := list("00000000-0000-0000-0000-0000000000bb", "", "", nil); status.Code(err) != codes.NotFound {
		t.Errorf("an unknown user: %v", err)
	}

	// Banned: refused as banned (the bot words it so).
	if err := f.st.SetUserStatus(ctx, f.st.Conn(), r2.GetId(), "banned"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.DeleteResellerService(ctx, &corev1.DeleteResellerServiceRequest{UserId: r2.GetId(), SubscriptionId: theirs}); status.Code(err) != codes.PermissionDenied ||
		!strings.Contains(err.Error(), "banned") {
		t.Errorf("a banned reseller: %v", err)
	}
	if err := f.st.SetUserStatus(ctx, f.st.Conn(), r2.GetId(), "active"); err != nil {
		t.Fatal(err)
	}

	// Resellers left the license: refused with the plain "forbidden".
	f.exec(t, `UPDATE core.install SET trial_started_at = now() - interval '31 days'`)
	_, errList := list(r.GetId(), "", "", nil)
	_, errPause := c.SetResellerServiceEnabled(ctx, &corev1.SetResellerServiceEnabledRequest{UserId: r.GetId(), SubscriptionId: active, Enabled: true})
	_, errDelete := c.DeleteResellerService(ctx, &corev1.DeleteResellerServiceRequest{UserId: r.GetId(), SubscriptionId: active})
	_, errNote := c.SetSubscriptionNote(ctx, &corev1.SetSubscriptionNoteRequest{UserId: r.GetId(), SubscriptionId: active, Note: "x"})
	_, errInvited := c.ListInvitedCustomers(ctx, &corev1.ListInvitedCustomersRequest{UserId: r.GetId()})
	for name, err := range map[string]error{"list": errList, "pause": errPause, "delete": errDelete, "note": errNote, "invited": errInvited} {
		if status.Code(err) != codes.PermissionDenied || strings.Contains(strings.ToLower(err.Error()), "licen") {
			t.Errorf("%s after the license: %v", name, err)
		}
	}
	if sub, _ := f.st.GetSubscription(ctx, f.st.Conn(), active); sub.Status != "disabled" || sub.Note != nil {
		t.Errorf("a refused call changed the service: %+v", sub)
	}
}

// The customers who joined through a reseller's link, over gRPC.
func TestInvitedCustomersOverGRPC(t *testing.T) {
	f := newFixtureWith(t, func(s *Server) { s.SetProvisioner(&panelFake{}) })
	c := f.client(t, botToken)
	ctx := context.Background()
	f.exec(t, `UPDATE core.install SET trial_started_at = now(), limit_hit_at = NULL`)
	t.Cleanup(func() { f.exec(t, `UPDATE core.install SET trial_started_at = now(), limit_hit_at = NULL`) })

	dom := domain.New(f.st, domain.Config{})
	plan := &store.Plan{NameI18n: map[string]string{"en": "M"}, Kind: "time", DurationDays: ptr32(30), Price: 100_000, Currency: "IRT", Enabled: true}
	if err := f.st.UpsertPlan(ctx, f.st.Conn(), plan); err != nil {
		t.Fatal(err)
	}
	adminU, _ := c.UpsertUser(ctx, &corev1.UpsertUserRequest{TelegramId: 7160})
	if err := f.st.SetUserRole(ctx, f.st.Conn(), adminU.GetId(), "admin", "active"); err != nil {
		t.Fatal(err)
	}
	admin, _ := f.st.GetUser(ctx, f.st.Conn(), adminU.GetId())
	tier, err := dom.CreateTier(ctx, admin, domain.TierInput{Name: "Gold", DiscountPercent: 20, CommissionPercent: 10}, "")
	if err != nil {
		t.Fatal(err)
	}
	r, _ := c.UpsertUser(ctx, &corev1.UpsertUserRequest{TelegramId: 7161})
	r2, _ := c.UpsertUser(ctx, &corev1.UpsertUserRequest{TelegramId: 7162})
	for _, u := range []*corev1.User{r, r2} {
		if _, err := dom.MakeReseller(ctx, admin, u.GetId(), domain.ResellerInput{TierID: tier.ID}, "shop"); err != nil {
			t.Fatal(err)
		}
	}
	// Three customers joined through r's link, one through r2's, one through nobody's.
	var mine []*store.User
	for i := range 3 {
		u, err := f.st.UpsertUser(ctx, f.st.Conn(), int64(7170+i), fmt.Sprintf("cust%d", i), "fa", r.GetId())
		if err != nil {
			t.Fatal(err)
		}
		f.exec(t, `UPDATE core.users SET created_at = now() - make_interval(hours => $2::int) WHERE id = $1`, u.ID, 10-i)
		mine = append(mine, u)
	}
	if _, err := f.st.UpsertUser(ctx, f.st.Conn(), 7180, "theirs", "fa", r2.GetId()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.UpsertUser(ctx, f.st.Conn(), 7181, "nobodys", "fa", ""); err != nil {
		t.Fatal(err)
	}
	// The newest of mine buys once at the store price: a 10% commission.
	buyer := mine[2]
	f.exec(t, `INSERT INTO core.wallets (user_id, currency, balance) VALUES ($1, 'IRT', 500000)`, buyer.ID)
	o, err := dom.CreateOrder(ctx, domain.CreateOrderParams{UserID: buyer.ID, PlanID: plan.ID, IdempotencyKey: "inv-grpc-1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dom.PayOrderWithWallet(ctx, o.ID, buyer.ID); err != nil {
		t.Fatal(err)
	}

	got, err := c.ListInvitedCustomers(ctx, &corev1.ListInvitedCustomersRequest{UserId: r.GetId()})
	if err != nil || got.GetPageInfo().GetTotal() != 3 || len(got.GetCustomers()) != 3 || got.GetPageInfo().GetPageSize() != 10 {
		t.Fatalf("list: %v %+v", err, got)
	}
	first := got.GetCustomers()[0]
	if first.GetTelegramId() != 7172 || first.GetUsername() != "cust2" || first.GetPaidOrders() != 1 ||
		len(first.GetSpent()) != 1 || first.GetSpent()[0].GetAmount() != 100_000 || first.GetSpent()[0].GetCurrency() != "IRT" ||
		len(first.GetCommission()) != 1 || first.GetCommission()[0].GetAmount() != 10_000 || first.GetJoinedAt() == 0 {
		t.Fatalf("the newest customer: %+v", first)
	}
	for _, cu := range got.GetCustomers() {
		if cu.GetTelegramId() == 7180 || cu.GetTelegramId() == 7181 {
			t.Fatalf("someone who did not join through the link: %+v", cu)
		}
	}
	if second := got.GetCustomers()[1]; second.GetPaidOrders() != 0 || len(second.GetSpent()) != 0 || len(second.GetCommission()) != 0 {
		t.Fatalf("a customer who bought nothing: %+v", second)
	}
	p2, err := c.ListInvitedCustomers(ctx, &corev1.ListInvitedCustomersRequest{UserId: r.GetId(), Pagination: &commonv1.Pagination{Page: 2, PageSize: 2}})
	if err != nil || len(p2.GetCustomers()) != 1 || p2.GetCustomers()[0].GetTelegramId() != 7170 || p2.GetPageInfo().GetTotal() != 3 {
		t.Fatalf("page 2: %v %+v", err, p2)
	}
	other, err := c.ListInvitedCustomers(ctx, &corev1.ListInvitedCustomersRequest{UserId: r2.GetId()})
	if err != nil || len(other.GetCustomers()) != 1 || other.GetCustomers()[0].GetTelegramId() != 7180 {
		t.Fatalf("the other reseller's: %v %+v", err, other)
	}
	if err := (func() error {
		_, err := c.ListInvitedCustomers(ctx, &corev1.ListInvitedCustomersRequest{UserId: "x"})
		return err
	})(); status.Code(err) != codes.NotFound {
		t.Fatalf("a malformed user id: %v", err)
	}
}

// Without a provisioner the panel-touching calls say so instead of failing
// halfway.
func TestResellerPanelCallsNeedTheProvisioner(t *testing.T) {
	f := newFixture(t)
	c := f.client(t, botToken)
	ctx := context.Background()
	if _, err := c.SetResellerServiceEnabled(ctx, &corev1.SetResellerServiceEnabledRequest{UserId: "x", SubscriptionId: "y"}); status.Code(err) != codes.FailedPrecondition {
		t.Errorf("pause: %v", err)
	}
	if _, err := c.DeleteResellerService(ctx, &corev1.DeleteResellerServiceRequest{UserId: "x", SubscriptionId: "y"}); status.Code(err) != codes.FailedPrecondition {
		t.Errorf("delete: %v", err)
	}
	if !errors.Is(domain.ErrPausedByStaff, domain.ErrState) {
		t.Error("ErrPausedByStaff is not a state error")
	}
}

func (f *fixture) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if _, err := f.st.DB().Exec(context.Background(), q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}
