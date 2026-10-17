package domain

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
)

func adminUser(t *testing.T, svc *Service, s *store.Store, tg int64, role string) {
	t.Helper()
	u := seedUser(t, s, tg)
	if _, err := s.DB().Exec(context.Background(), `UPDATE core.users SET role = $2 WHERE id = $1`, u.ID, role); err != nil {
		t.Fatal(err)
	}
}

func TestAdminTodayReadsTodayPerCurrency(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	owner(t, svc)
	plan := seedPlan(t, s, false, 700)
	u := seedUser(t, s, 5101)
	o, err := svc.CreateOrder(ctx, CreateOrderParams{UserID: u.ID, PlanID: plan.ID, IdempotencyKey: "today-1"})
	if err != nil {
		t.Fatal(err)
	}
	ev := payEvent(t, "today-e1", events.PaymentsPaymentSucceeded, events.PaymentEvent{
		IntentID: "00000000-0000-7000-8000-000000000001", OrderID: o.ID, UserID: u.ID, Provider: "zarinpal", Amount: 700, Currency: "IRT"})
	if err := svc.HandlePaymentEvent(ctx, ev); err != nil {
		t.Fatal(err)
	}

	got, err := svc.AdminToday(ctx, ownerTG, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got.TZ == "" || got.Today.Date == "" || got.Yesterday.Date == "" || got.Today.Date == got.Yesterday.Date {
		t.Fatalf("days: %+v", got)
	}
	if len(got.Today.Lines) != 1 || got.Today.Lines[0] != (DayLine{Currency: "IRT", Revenue: 700, Orders: 1}) {
		t.Fatalf("today lines: %+v", got.Today.Lines)
	}
	if got.Today.NewCustomers < 1 {
		t.Fatalf("new customers: %d", got.Today.NewCustomers)
	}
	if len(got.Yesterday.Lines) != 0 {
		t.Fatalf("yesterday: %+v", got.Yesterday.Lines)
	}
	if _, err := svc.AdminToday(ctx, 5101, time.Now()); !errors.Is(err, ErrForbidden) {
		t.Fatalf("a customer: %v", err)
	}
}

func TestAdminSettingSpecsFollowTheRole(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	owner(t, svc)
	adminUser(t, svc, s, 5201, "admin")
	adminUser(t, svc, s, 5202, "support")
	if err := svc.AdminSetSetting(ctx, ownerTG, "payments.card_number", "6037-9911-0000-0000"); err != nil {
		t.Fatal(err)
	}

	all, err := svc.AdminSettingSpecs(ctx, ownerTG)
	if err != nil {
		t.Fatal(err)
	}
	mine, err := svc.AdminSettingSpecs(ctx, 5201)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) == 0 || len(mine) == 0 || len(mine) >= len(all) {
		t.Fatalf("owner %d, admin %d", len(all), len(mine))
	}
	find := func(list []AdminSettingView, key string) *AdminSettingView {
		for i := range list {
			if list[i].Key == key {
				return &list[i]
			}
		}
		return nil
	}
	if v := find(all, "payments.card_number"); v == nil || v.Value != "6037-9911-0000-0000" {
		t.Fatalf("owner sees the card: %+v", v)
	}
	if find(mine, "payments.card_number") != nil {
		t.Fatal("an admin was offered the owner's payment details")
	}
	if find(mine, "maintenance.enabled") == nil {
		t.Fatal("an admin lost a plain setting")
	}
	if _, err := svc.AdminSettingSpecs(ctx, 5202); !errors.Is(err, ErrForbidden) {
		t.Fatalf("support: %v", err)
	}
	// What is offered passes the setter's own check, so the menu and the
	// dashboard cannot disagree: the owner sets every spec to its default.
	for _, v := range all {
		if v.Kind == "bool" || v.Kind == "enum" || v.Default == "" {
			continue
		}
		if err := svc.AdminSetSetting(ctx, ownerTG, v.Key, v.Default); err != nil {
			t.Errorf("%s: its own default is refused: %v", v.Key, err)
		}
	}
}

func TestAdminServersFollowTheDashboardAndNeverTouchSecrets(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	own := owner(t, svc)
	adminUser(t, svc, s, 5301, "admin")
	fs := &fakeServers{}
	sv, err := svc.AddServer(ctx, own, fs, ServerInput{Name: "DE", BaseURL: "https://de.example.com", Token: "secret-token-AAAA", Weight: 50, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	list, err := svc.AdminListServers(ctx, ownerTG, fs)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v %v", list, err)
	}
	// An admin has servers.write on the dashboard, so the bot offers it too.
	if l, err := svc.AdminListServers(ctx, 5301, fs); err != nil || len(l) != 1 {
		t.Fatalf("an admin lists servers: %v %v", l, err)
	}
	if _, err := svc.AdminListServers(ctx, 9999, fs); !errors.Is(err, ErrForbidden) {
		t.Fatalf("a stranger lists servers: %v", err)
	}

	off, err := svc.AdminSetServer(ctx, ownerTG, fs, sv.ID, false, 0)
	if err != nil || off.Enabled || off.Weight != 50 || off.BaseURL != "https://de.example.com" || off.Name != "DE" {
		t.Fatalf("switch off: %+v %v", off, err)
	}
	if fs.tokens[sv.ID] != "secret-token-AAAA" {
		t.Fatal("the stored token changed")
	}
	if on, err := svc.AdminSetServer(ctx, ownerTG, fs, sv.ID, true, 300); err != nil || !on.Enabled || on.Weight != 300 {
		t.Fatalf("switch on and weigh: %+v %v", on, err)
	}
	if _, err := svc.AdminSetServer(ctx, ownerTG, fs, sv.ID, true, 5000); !errors.Is(err, ErrInvalid) && err == nil {
		t.Fatalf("a weight over the limit: %v", err)
	}
	if _, err := svc.AdminSetServer(ctx, ownerTG, fs, sv.ID, true, -1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a negative weight: %v", err)
	}
	if _, err := svc.AdminSetServer(ctx, ownerTG, fs, "00000000-0000-7000-8000-00000000ffff", true, 0); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
	if n := auditCount(t, s, "server.update"); n != 2 {
		t.Fatalf("audit rows: %d", n)
	}
	for _, c := range auditJSON(t, s, "server.update") {
		if strings.Contains(c, "secret-token") {
			t.Fatalf("a token in the audit log: %s", c)
		}
	}
}

func TestAdminStaffIsTheOwnersAndNeedsAReason(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	owner(t, svc)
	adminUser(t, svc, s, 5401, "admin")
	seedUser(t, s, 5402)

	if _, err := svc.AdminListStaff(ctx, 5401); !errors.Is(err, ErrForbidden) {
		t.Fatalf("an admin lists staff: %v", err)
	}
	if _, err := svc.AdminAddStaff(ctx, 5401, 5402, "support", "help"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("an admin adds staff: %v", err)
	}
	if _, err := svc.AdminAddStaff(ctx, ownerTG, 5402, "support", " "); !errors.Is(err, ErrInvalid) {
		t.Fatalf("no reason: %v", err)
	}
	if _, err := svc.AdminAddStaff(ctx, ownerTG, 5402, "boss", "x"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a made-up role: %v", err)
	}
	if _, err := svc.AdminAddStaff(ctx, ownerTG, 987654, "support", "x"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("never started the bot: %v", err)
	}
	e, err := svc.AdminAddStaff(ctx, ownerTG, 5402, "support", "front desk")
	if err != nil || e.Role != "support" || e.TelegramID != 5402 {
		t.Fatalf("add: %+v %v", e, err)
	}
	if e, err = svc.AdminAddStaff(ctx, ownerTG, 5402, "admin", "promoted"); err != nil || e.Role != "admin" {
		t.Fatalf("change the role: %+v %v", e, err)
	}
	list, err := svc.AdminListStaff(ctx, ownerTG)
	if err != nil || len(list) != 3 {
		t.Fatalf("list: %+v %v", list, err)
	}
	var owners int
	for _, m := range list {
		if m.Role == "owner" && m.ConfiguredOwner {
			owners++
		}
	}
	if owners != 1 {
		t.Fatalf("configured owner flag: %+v", list)
	}
	if err := svc.AdminRemoveStaff(ctx, ownerTG, 5402, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("remove without a reason: %v", err)
	}
	if err := svc.AdminRemoveStaff(ctx, ownerTG, 5402, "left the team"); err != nil {
		t.Fatal(err)
	}
	if err := svc.AdminRemoveStaff(ctx, ownerTG, ownerTG, "oops"); err == nil {
		t.Fatal("the last owner removed themselves")
	}
	if n := auditCount(t, s, "staff.add") + auditCount(t, s, "staff.role") + auditCount(t, s, "staff.remove"); n != 3 {
		t.Fatalf("audit rows: %d", n)
	}
}

func TestAdminBroadcastChecksThenSends(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	owner(t, svc)
	adminUser(t, svc, s, 5501, "admin")
	adminUser(t, svc, s, 5502, "support")
	seedUser(t, s, 5503)
	in := BroadcastInput{Segment: "all", Texts: map[string]string{"en": "Hello <b>all</b>", "fa": "سلام"}}

	if _, err := svc.AdminBroadcast(ctx, 5502, in, false); !errors.Is(err, ErrForbidden) {
		t.Fatalf("support: %v", err)
	}
	p, err := svc.AdminBroadcast(ctx, 5501, in, false)
	if err != nil || p.Total < 1 || p.BroadcastID != "" {
		t.Fatalf("check: %+v %v", p, err)
	}
	var n int
	if err := s.DB().QueryRow(ctx, `SELECT count(*) FROM core.broadcasts`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("a check saved something: %d %v", n, err)
	}
	bad := in
	bad.Segment = "nobody"
	if _, err := svc.AdminBroadcast(ctx, 5501, bad, true); err == nil {
		t.Fatal("a made-up segment was sent")
	}
	empty := in
	empty.Texts = map[string]string{}
	if _, err := svc.AdminBroadcast(ctx, 5501, empty, true); err == nil {
		t.Fatal("a broadcast with no text was sent")
	}
	sent, err := svc.AdminBroadcast(ctx, 5501, in, true)
	if err != nil || sent.BroadcastID == "" {
		t.Fatalf("send: %+v %v", sent, err)
	}
	b, err := s.GetBroadcast(ctx, s.Conn(), sent.BroadcastID, false)
	if err != nil || b.Status == "draft" {
		t.Fatalf("saved broadcast: %+v %v", b, err)
	}
}
