package server

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	provisionerv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/provisioner/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/provisioner/adapter"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/provisioner/store"
)

// renamesClients is what the adapters can do: a kind renames exactly when its
// adapter is a ClientRenamer.
func TestRenamesClientsFollowsTheAdapters(t *testing.T) {
	tokens := map[string]string{
		store.KindXUI: "panel-token-0123456789", store.KindMarzban: "admin:pw", store.KindSUI: suiPanelTok, store.KindHiddify: hiddifyAPIKey, //gitleaks:allow test fixture
	}
	for kind, token := range tokens {
		a, err := newAdapter(kind, "https://panel.example.com/path", token)
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		_, ok := a.(adapter.ClientRenamer)
		if ok != renamesClients(kind) {
			t.Errorf("%s: the adapter renames = %v, renamesClients = %v", kind, ok, renamesClients(kind))
		}
	}
	if renamesClients(store.KindMarzban) || renamesClients(store.KindSUI) || !renamesClients(store.KindXUI) || !renamesClients(store.KindHiddify) {
		t.Fatal("Marzban and S-UI must not rename; 3x-ui and Hiddify do")
	}
}

func TestCheckClientName(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	check := func(name string) *provisionerv1.CheckClientNameResponse {
		t.Helper()
		res, err := f.client.CheckClientName(ctx, &provisionerv1.CheckClientNameRequest{Name: name})
		if err != nil {
			t.Fatalf("check %q: %v", name, err)
		}
		return res
	}
	if _, err := f.client.CreateClient(ctx, &provisionerv1.CreateClientRequest{SubscriptionId: sub1, Email: "sobhan1234", DurationDays: 3}); err != nil {
		t.Fatal(err)
	}
	f.fake.Seed("stranger1", "", []int{1}) // made in the panel by an operator: BOBRES has no record of it

	if res := check("sobhan1234"); res.GetAvailable() || res.GetReason() != NameTaken {
		t.Fatalf("a name BOBRES holds = %+v", res)
	}
	if res := check("stranger1"); res.GetAvailable() || res.GetReason() != NameTaken {
		t.Fatalf("a name only the panel has = %+v", res)
	}
	if res := check("free1234"); !res.GetAvailable() || res.GetReason() != "" {
		t.Fatalf("a free name = %+v", res)
	}
	for _, bad := range []string{"", "has space", "a/b"} {
		if _, err := f.client.CheckClientName(ctx, &provisionerv1.CheckClientNameRequest{Name: bad}); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("check %q: %v", bad, err)
		}
	}

	// A disabled server is not asked; an enabled one that does not answer makes
	// the answer "unchecked", never "available".
	off, err := f.client.AddServer(ctx, &provisionerv1.AddServerRequest{Name: "off", BaseUrl: "http://127.0.0.1:1", ApiToken: "x-token-0123456789", //gitleaks:allow test fixture
		AllowPrivate: true, SkipCheck: true, Enabled: proto.Bool(false)})
	if err != nil {
		t.Fatal(err)
	}
	if res := check("free1234"); !res.GetAvailable() {
		t.Fatalf("a disabled server was asked: %+v", res)
	}
	if _, err := f.client.UpdateServer(ctx, &provisionerv1.UpdateServerRequest{Id: off.GetId(), Name: "off", Enabled: true, Weight: 100,
		BaseUrl: "http://127.0.0.1:1", AllowPrivate: true, SkipCheck: true}); err != nil {
		t.Fatal(err)
	}
	if res := check("free1234"); res.GetAvailable() || res.GetReason() != NameUnchecked {
		t.Fatalf("an enabled server that does not answer = %+v", res)
	}
	// A name some panel has is "taken" even while another cannot be asked.
	if res := check("stranger1"); res.GetReason() != NameTaken {
		t.Fatalf("taken on one panel, another down = %+v", res)
	}
}

func TestRenameClient(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	made, err := f.client.CreateClient(ctx, &provisionerv1.CreateClientRequest{SubscriptionId: sub1, Email: "u5-abcd1234", DurationDays: 3, TrafficBytes: 1 << 30})
	if err != nil {
		t.Fatal(err)
	}
	before, err := f.client.GetLinks(ctx, &provisionerv1.GetLinksRequest{SubscriptionId: sub1})
	if err != nil {
		t.Fatal(err)
	}
	if list, err := f.client.ListServers(ctx, &provisionerv1.ListServersRequest{}); err != nil || !list.GetServers()[0].GetRenamesClients() {
		t.Fatalf("a 3x-ui server must say it renames: %+v %v", list, err)
	}

	got, err := f.client.RenameClient(ctx, &provisionerv1.RenameClientRequest{SubscriptionId: sub1, NewName: "sobhan1234"})
	if err != nil {
		t.Fatalf("RenameClient: %v", err)
	}
	if got.GetEmail() != "sobhan1234" || got.GetXuiSubId() != made.GetXuiSubId() {
		t.Fatalf("renamed = %+v, made %+v", got, made)
	}
	if !f.fake.Has("sobhan1234") || f.fake.Has("u5-abcd1234") {
		t.Fatal("the panel was not renamed")
	}
	snap, _ := f.fake.Snapshot("sobhan1234")
	if snap.SubID != made.GetXuiSubId() || snap.TotalGB != 1<<30 || !snap.Enable {
		t.Fatalf("the renamed client = %+v", snap)
	}
	if lost := f.fake.LostFields(); len(lost) > 0 {
		t.Fatalf("the rename lost fields: %v", lost)
	}
	after, err := f.client.GetLinks(ctx, &provisionerv1.GetLinksRequest{SubscriptionId: sub1})
	if err != nil || after.GetSubscriptionLink() != before.GetSubscriptionLink() {
		t.Fatalf("the subscription link changed: %q, was %q (%v)", after.GetSubscriptionLink(), before.GetSubscriptionLink(), err)
	}
	if cm, err := f.st.GetClientMap(ctx, sub1); err != nil || cm.Email != "sobhan1234" {
		t.Fatalf("client map = %+v, %v", cm, err)
	}
	// The same rename again changes nothing.
	if _, err := f.client.RenameClient(ctx, &provisionerv1.RenameClientRequest{SubscriptionId: sub1, NewName: "sobhan1234"}); err != nil {
		t.Fatalf("the same rename again: %v", err)
	}

	// A name another client has, on the panel or in BOBRES's records, is refused.
	f.fake.Seed("stranger1", "", []int{1})
	if _, err := f.client.RenameClient(ctx, &provisionerv1.RenameClientRequest{SubscriptionId: sub1, NewName: "stranger1"}); status.Code(err) != codes.AlreadyExists {
		t.Fatalf("a stranger's name: %v", err)
	}
	const sub2 = "00000000-0000-7000-8000-0000000000a2"
	if _, err := f.client.CreateClient(ctx, &provisionerv1.CreateClientRequest{SubscriptionId: sub2, Email: "other5678", DurationDays: 3}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.client.RenameClient(ctx, &provisionerv1.RenameClientRequest{SubscriptionId: sub1, NewName: "other5678"}); status.Code(err) != codes.AlreadyExists {
		t.Fatalf("another subscription's name: %v", err)
	}
	if !f.fake.Has("sobhan1234") {
		t.Fatal("a refused rename changed the panel")
	}

	// The answer of a rename was lost after the panel changed: the retry records it.
	if err := f.st.RenameClientMap(ctx, sub1, "u5-abcd1234"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.client.RenameClient(ctx, &provisionerv1.RenameClientRequest{SubscriptionId: sub1, NewName: "sobhan1234"}); err != nil {
		t.Fatalf("retry after a lost answer: %v", err)
	}
	if cm, _ := f.st.GetClientMap(ctx, sub1); cm.Email != "sobhan1234" {
		t.Fatalf("the retry did not record the name: %+v", cm)
	}

	for _, c := range []struct {
		req  *provisionerv1.RenameClientRequest
		code codes.Code
	}{
		{&provisionerv1.RenameClientRequest{SubscriptionId: "00000000-0000-7000-8000-000000000999", NewName: "abcd1234"}, codes.NotFound},
		{&provisionerv1.RenameClientRequest{SubscriptionId: sub1, NewName: "no spaces"}, codes.InvalidArgument},
		{&provisionerv1.RenameClientRequest{NewName: "abcd1234"}, codes.InvalidArgument},
	} {
		if _, err := f.client.RenameClient(ctx, c.req); status.Code(err) != c.code {
			t.Fatalf("%+v: %v, want %v", c.req, err, c.code)
		}
	}
}

// Marzban cannot change a username: the rename is refused, the server says so,
// and the name check works on it like on any panel.
func TestRenameClientMarzbanRefused(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	fake, sv := f.marzbanPanel(t)
	if sv.GetRenamesClients() {
		t.Fatalf("a Marzban server says it renames: %+v", sv)
	}
	if _, err := f.client.CreateClient(ctx, &provisionerv1.CreateClientRequest{SubscriptionId: sub1, Email: "u42-abcd1234", DurationDays: 3}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.client.RenameClient(ctx, &provisionerv1.RenameClientRequest{SubscriptionId: sub1, NewName: "sobhan1234"}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("rename on Marzban: %v", err)
	}
	if !fake.Has("u42-abcd1234") || fake.Has("sobhan1234") {
		t.Fatal("the refused rename changed the panel")
	}
	if res, err := f.client.CheckClientName(ctx, &provisionerv1.CheckClientNameRequest{Name: "sobhan1234"}); err != nil || !res.GetAvailable() {
		t.Fatalf("a free name on Marzban = %+v, %v", res, err)
	}
	if _, err := f.st.DB().Exec(ctx, "DELETE FROM provisioner.client_map"); err != nil {
		t.Fatal(err)
	}
	if res, err := f.client.CheckClientName(ctx, &provisionerv1.CheckClientNameRequest{Name: "u42-abcd1234"}); err != nil || res.GetReason() != NameTaken {
		t.Fatalf("a name the Marzban panel has = %+v, %v", res, err)
	}
}
