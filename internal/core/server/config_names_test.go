package server

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const namesServerID = "00000000-0000-7000-8000-0000000000aa"

// namesProv is the provisioner as config names use it: one server that renames, the names a panel has, and the renames.
type namesProv struct {
	domain.Provisioner
	domain.ServerAdmin
	mu      sync.Mutex
	taken   map[string]bool
	renamed []string
}

func (p *namesProv) ListServers(context.Context) (*domain.ServerList, error) {
	return &domain.ServerList{Servers: []domain.Server{{ID: namesServerID, Name: "s1", Enabled: true, RenamesClients: true}}}, nil
}
func (p *namesProv) CheckClientName(_ context.Context, name string) (domain.NameCheck, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.taken[name] {
		return domain.NameCheck{Reason: domain.NameTaken}, nil
	}
	return domain.NameCheck{Available: true}, nil
}
func (p *namesProv) RenameClient(_ context.Context, id, name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.renamed = append(p.renamed, id+"|"+name)
	return nil
}

// The bot's config name calls: the check (off, the rules, taken), an order that carries the name, a service that says it
// can be renamed, and the rename, refused for a service that is not the caller's.
func TestConfigNamesOverGRPC(t *testing.T) {
	prov := &namesProv{taken: map[string]bool{"taken123": true}}
	f := newFixtureWith(t, func(s *Server) { s.SetProvisioner(prov) })
	c := f.client(t, botToken)
	ctx := context.Background()
	t.Cleanup(func() { f.exec(t, `DELETE FROM core.settings WHERE key LIKE 'config_names.%'`) })
	u, err := c.UpsertUser(ctx, &corev1.UpsertUserRequest{TelegramId: 7801})
	if err != nil {
		t.Fatal(err)
	}
	plan := &store.Plan{NameI18n: map[string]string{"en": "M"}, Kind: "time", DurationDays: ptr32(30), Price: 100_000, Currency: "IRT", Enabled: true}
	if err := f.st.UpsertPlan(ctx, f.st.Conn(), plan); err != nil {
		t.Fatal(err)
	}
	o, _, err := f.st.CreateOrder(ctx, f.st.Conn(), &store.Order{UserID: u.GetId(), PlanID: plan.ID, Type: "new", Status: "active", Amount: 1, Currency: "IRT", IdempotencyKey: "names-1"})
	if err != nil {
		t.Fatal(err)
	}
	sub, err := f.st.CreateSubscription(ctx, f.st.Conn(), &store.Subscription{UserID: u.GetId(), OrderID: o.ID, ClientEmail: "u7801-abcdefgh"})
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(48 * time.Hour)
	if err := f.st.ActivateSubscription(ctx, f.st.Conn(), sub.ID, namesServerID, "sub1", "https://sub.example/x", &expires, nil, nil); err != nil {
		t.Fatal(err)
	}

	check := func(name string) *corev1.ConfigNameCheck {
		t.Helper()
		res, err := c.CheckConfigName(ctx, &corev1.CheckConfigNameRequest{UserId: u.GetId(), Name: name})
		if err != nil {
			t.Fatalf("check %q: %v", name, err)
		}
		return res
	}
	if res := check("sara1234"); res.GetOk() || res.GetReason() != domain.NameOff {
		t.Fatalf("off: %+v", res)
	}
	list, err := c.ListSubscriptions(ctx, &corev1.ListSubscriptionsRequest{UserId: u.GetId()})
	if err != nil || len(list.GetSubscriptions()) != 1 || list.GetSubscriptions()[0].GetRenamable() {
		t.Fatalf("renamable while off: %+v %v", list, err)
	}
	f.exec(t, `INSERT INTO core.settings (key, value) VALUES ('config_names.customers', '"true"')`)
	if res := check("sara"); res.GetReason() != domain.NameMix {
		t.Fatalf("no digit: %+v", res)
	}
	if res := check("taken123"); res.GetReason() != domain.NameTaken {
		t.Fatalf("taken: %+v", res)
	}
	if res := check("Sara1234"); !res.GetOk() || res.GetName() != "sara1234" {
		t.Fatalf("free: %+v", res)
	}
	// An order carries the name; one core refuses is InvalidArgument naming the reason.
	if _, err := c.CreateOrder(ctx, &corev1.CreateOrderRequest{UserId: u.GetId(), PlanId: plan.ID, IdempotencyKey: "names-2", ClientName: "taken123"}); status.Code(err) != codes.InvalidArgument ||
		!strings.HasSuffix(status.Convert(err).Message(), "config name: taken") {
		t.Fatalf("an order with a taken name: %v", err)
	}
	ord, err := c.CreateOrder(ctx, &corev1.CreateOrderRequest{UserId: u.GetId(), PlanId: plan.ID, IdempotencyKey: "names-3", ClientName: "sara1234"})
	if err != nil || ord.GetClientName() != "sara1234" {
		t.Fatalf("an order with a name: %+v %v", ord, err)
	}

	list, err = c.ListSubscriptions(ctx, &corev1.ListSubscriptionsRequest{UserId: u.GetId()})
	if err != nil || !list.GetSubscriptions()[0].GetRenamable() {
		t.Fatalf("not renamable while on: %+v %v", list, err)
	}
	res, err := c.RenameService(ctx, &corev1.RenameServiceRequest{UserId: u.GetId(), SubscriptionId: sub.ID, Name: "mine5678"})
	if err != nil || !res.GetOk() || res.GetService().GetClientEmail() != "mine5678" || res.GetService().GetSubscriptionLink() != "https://sub.example/x" {
		t.Fatalf("rename: %+v %v", res, err)
	}
	if len(prov.renamed) != 1 || prov.renamed[0] != sub.ID+"|mine5678" {
		t.Fatalf("panel renames: %v", prov.renamed)
	}
	if res, err := c.RenameService(ctx, &corev1.RenameServiceRequest{UserId: u.GetId(), SubscriptionId: sub.ID, Name: "u1abc"}); err != nil || res.GetOk() || res.GetReason() != domain.NameAutomatic {
		t.Fatalf("an automatic-looking name: %+v %v", res, err)
	}
	other, _ := c.UpsertUser(ctx, &corev1.UpsertUserRequest{TelegramId: 7802})
	if _, err := c.RenameService(ctx, &corev1.RenameServiceRequest{UserId: other.GetId(), SubscriptionId: sub.ID, Name: "abcd1234"}); status.Code(err) != codes.NotFound {
		t.Fatalf("another user's service: %v", err)
	}
}
