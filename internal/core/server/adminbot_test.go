package server

import (
	"context"
	"testing"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// fakeAdminProv answers the server registry calls of the menu from memory.
type fakeAdminProv struct {
	domain.Provisioner
	domain.ServerAdmin
	servers []domain.Server
}

func (f *fakeAdminProv) ListServers(context.Context) (*domain.ServerList, error) {
	return &domain.ServerList{Servers: append([]domain.Server(nil), f.servers...)}, nil
}

func (f *fakeAdminProv) UpdateServer(_ context.Context, id string, in domain.ServerInput) (*domain.Server, error) {
	for i := range f.servers {
		if f.servers[i].ID == id {
			f.servers[i].Enabled, f.servers[i].Weight = in.Enabled, in.Weight
			out := f.servers[i]
			return &out, nil
		}
	}
	return nil, status.Error(codes.NotFound, "no such server")
}

func TestAdminMenuCallsOverGRPC(t *testing.T) {
	prov := &fakeAdminProv{servers: []domain.Server{{ID: "00000000-0000-7000-8000-000000000001", Name: "DE",
		BaseURL: "https://de.example.com", Enabled: true, Weight: 100, Healthy: true, CheckedAt: 1}}}
	f := newFixtureWith(t, func(s *Server) { s.SetProvisioner(prov) })
	c := f.client(t, botToken)
	ctx := context.Background()
	mk := func(tg int64, role string) {
		t.Helper()
		u, err := c.UpsertUser(ctx, &corev1.UpsertUserRequest{TelegramId: tg})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.st.DB().Exec(ctx, `UPDATE core.users SET role = $2 WHERE id = $1`, u.GetId(), role); err != nil {
			t.Fatal(err)
		}
	}
	mk(7001, "owner")
	mk(7002, "admin")
	mk(7003, "user")
	code := func(err error) codes.Code { return status.Code(err) }

	// A customer is refused everywhere.
	if _, err := c.AdminTodayStats(ctx, &corev1.AdminTodayStatsRequest{ActorTelegramId: 7003}); code(err) != codes.PermissionDenied {
		t.Fatalf("today as a customer: %v", err)
	}
	if _, err := c.AdminListSettingSpecs(ctx, &corev1.AdminListSettingSpecsRequest{ActorTelegramId: 7003}); code(err) != codes.PermissionDenied {
		t.Fatalf("specs as a customer: %v", err)
	}

	// Today's numbers and the settings an admin may change.
	today, err := c.AdminTodayStats(ctx, &corev1.AdminTodayStatsRequest{ActorTelegramId: 7002})
	if err != nil || today.GetToday().GetDate() == "" || today.GetYesterday().GetDate() == "" {
		t.Fatalf("today: %v %v", today, err)
	}
	adminSpecs, err := c.AdminListSettingSpecs(ctx, &corev1.AdminListSettingSpecsRequest{ActorTelegramId: 7002})
	if err != nil || len(adminSpecs.GetSpecs()) == 0 {
		t.Fatalf("specs: %v %v", adminSpecs, err)
	}
	ownerSpecs, err := c.AdminListSettingSpecs(ctx, &corev1.AdminListSettingSpecsRequest{ActorTelegramId: 7001})
	if err != nil || len(ownerSpecs.GetSpecs()) <= len(adminSpecs.GetSpecs()) {
		t.Fatalf("the owner sees more: %d vs %d (%v)", len(ownerSpecs.GetSpecs()), len(adminSpecs.GetSpecs()), err)
	}

	// Servers: owner and admin (servers.write); anyone else is refused.
	if l, err := c.AdminListServers(ctx, &corev1.AdminListServersRequest{ActorTelegramId: 7002}); err != nil || len(l.GetServers()) != 1 {
		t.Fatalf("servers as an admin: %v %v", l, err)
	}
	if _, err := c.AdminListServers(ctx, &corev1.AdminListServersRequest{ActorTelegramId: 7999}); code(err) != codes.PermissionDenied {
		t.Fatalf("servers as a stranger: %v", err)
	}
	ls, err := c.AdminListServers(ctx, &corev1.AdminListServersRequest{ActorTelegramId: 7001})
	if err != nil || len(ls.GetServers()) != 1 || !ls.GetServers()[0].GetHealthKnown() || !ls.GetServers()[0].GetHealthy() {
		t.Fatalf("servers: %v %v", ls, err)
	}
	id := ls.GetServers()[0].GetId()
	if _, err := c.AdminSetServer(ctx, &corev1.AdminSetServerRequest{ActorTelegramId: 7999, ServerId: id}); code(err) != codes.PermissionDenied {
		t.Fatalf("set server as a stranger: %v", err)
	}
	sv, err := c.AdminSetServer(ctx, &corev1.AdminSetServerRequest{ActorTelegramId: 7001, ServerId: id, Enabled: false, Weight: 40})
	if err != nil || sv.GetEnabled() || sv.GetWeight() != 40 {
		t.Fatalf("set server: %v %v", sv, err)
	}
	if _, err := c.AdminSetServer(ctx, &corev1.AdminSetServerRequest{ActorTelegramId: 7001, ServerId: "00000000-0000-7000-8000-0000000000ff"}); code(err) != codes.NotFound {
		t.Fatalf("unknown server: %v", err)
	}

	// Staff.
	if _, err := c.AdminListStaff(ctx, &corev1.AdminListStaffRequest{ActorTelegramId: 7002}); code(err) != codes.PermissionDenied {
		t.Fatalf("staff as an admin: %v", err)
	}
	if _, err := c.AdminAddStaff(ctx, &corev1.AdminAddStaffRequest{ActorTelegramId: 7001, TargetTelegramId: 7003, Role: "support"}); code(err) != codes.InvalidArgument {
		t.Fatalf("no reason: %v", err)
	}
	if _, err := c.AdminAddStaff(ctx, &corev1.AdminAddStaffRequest{ActorTelegramId: 7001, TargetTelegramId: 1, Role: "support", Reason: "x"}); code(err) != codes.NotFound {
		t.Fatalf("a stranger: %v", err)
	}
	m, err := c.AdminAddStaff(ctx, &corev1.AdminAddStaffRequest{ActorTelegramId: 7001, TargetTelegramId: 7003, Role: "support", Reason: "front desk"})
	if err != nil || m.GetRole() != "support" {
		t.Fatalf("add: %v %v", m, err)
	}
	st, err := c.AdminListStaff(ctx, &corev1.AdminListStaffRequest{ActorTelegramId: 7001})
	if err != nil || len(st.GetStaff()) != 3 {
		t.Fatalf("list: %v %v", st, err)
	}
	if _, err := c.AdminRemoveStaff(ctx, &corev1.AdminRemoveStaffRequest{ActorTelegramId: 7001, TargetTelegramId: 7003, Reason: "left"}); err != nil {
		t.Fatal(err)
	}

	// Broadcast: check, then refuse a bad one.
	req := &corev1.AdminBroadcastRequest{ActorTelegramId: 7002, Segment: "all", Texts: map[string]string{"en": "Hi", "fa": "سلام"}}
	chk, err := c.AdminBroadcast(ctx, req)
	if err != nil || chk.GetAudience() < 1 || chk.GetBroadcastId() != "" {
		t.Fatalf("check: %v %v", chk, err)
	}
	req.Segment = "nobody"
	if _, err := c.AdminBroadcast(ctx, req); code(err) != codes.InvalidArgument {
		t.Fatalf("bad segment: %v", err)
	}
	req.Segment, req.ActorTelegramId = "all", 7003
	if _, err := c.AdminBroadcast(ctx, req); code(err) != codes.PermissionDenied {
		t.Fatalf("broadcast as a customer: %v", err)
	}
}

func TestAdminServersNeedTheProvisioner(t *testing.T) {
	f := newFixture(t)
	c := f.client(t, botToken)
	if _, err := c.AdminListServers(context.Background(), &corev1.AdminListServersRequest{ActorTelegramId: 1}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("without a provisioner: %v", err)
	}
}
