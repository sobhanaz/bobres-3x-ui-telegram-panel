package server

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	provisionerv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/provisioner/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/xui"
)

const resellerGroup = "BOBRES-SOBHANADMIN"

// A client made for a reseller is in the reseller's group from the start, keeps it through the calls core makes on it,
// and the group is remembered for the backfill; a create with no group is in the default one; a bad name is refused.
func TestCreateClientInAGroup(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if _, err := f.client.CreateClient(ctx, &provisionerv1.CreateClientRequest{SubscriptionId: sub1, Email: "ab-u1-aaaaaaaa", DurationDays: 30, TrafficBytes: 10 << 30, Group: resellerGroup}); err != nil {
		t.Fatal(err)
	}
	if g := groupOf(f.fake, "ab-u1-aaaaaaaa"); g != resellerGroup {
		t.Fatalf("a reseller's client is in %q", g)
	}
	if f.fake.Calls("groupBulkAdd") != 0 {
		t.Fatal("the client was moved after its creation instead of made in the group")
	}
	cm, err := f.st.GetClientMap(ctx, sub1)
	if err != nil || cm.PanelGroup != resellerGroup {
		t.Fatalf("the remembered group: %+v %v", cm, err)
	}
	if _, err := f.client.RenewClient(ctx, &provisionerv1.RenewClientRequest{SubscriptionId: sub1, AddDays: 5}); err != nil {
		t.Fatal(err)
	}
	if g := groupOf(f.fake, "ab-u1-aaaaaaaa"); g != resellerGroup {
		t.Fatalf("after a renewal: %q", g)
	}

	// No group: the default one, and nothing remembered.
	if _, err := f.client.CreateClient(ctx, &provisionerv1.CreateClientRequest{SubscriptionId: subID(2), Email: "u2-bbbbbbbb", DurationDays: 30}); err != nil {
		t.Fatal(err)
	}
	if g := groupOf(f.fake, "u2-bbbbbbbb"); g != xui.BotGroup {
		t.Fatalf("a customer's client is in %q", g)
	}
	if cm, _ := f.st.GetClientMap(ctx, subID(2)); cm == nil || cm.PanelGroup != "" {
		t.Fatalf("remembered %+v", cm)
	}

	// A name the panel would not keep as it is.
	for _, bad := range []string{" padded ", "tab\there", strings.Repeat("G", 65)} {
		_, err := f.client.CreateClient(ctx, &provisionerv1.CreateClientRequest{SubscriptionId: subID(3), Email: "u3-cccccccc", DurationDays: 30, Group: bad})
		if status.Code(err) != codes.InvalidArgument {
			t.Errorf("group %q: %v", bad, err)
		}
	}
}

// SetClientGroups moves a reseller's existing clients into the reseller's group, but only those in a group BOBRES owns: the old
// default name, none, the default, or another "BOBRES-NAME"; a group an operator chose stays. The panel is brought in line
// by the backfill, which then has nothing left to do; a name given back as "" returns the clients to the default group.
func TestSetClientGroupsMovesOnlyWhatIsOurs(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.hold(t, 1, "u1-legacy", xui.LegacyBotGroup)
	f.hold(t, 2, "u2-none", "")
	f.hold(t, 3, "u3-default", xui.BotGroup)
	f.hold(t, 4, "u4-vip", "vip")
	f.hold(t, 5, "u5-other", "BOBRES-OLDNAME")
	f.fake.Seed("stranger", "sub-x", []int{1}) // on the panel, not held
	f.srv.BackfillGroups(ctx)                  // the first pass: everything of ours is in the default group
	if g := groupOf(f.fake, "u1-legacy"); g != xui.BotGroup {
		t.Fatalf("the old default name was not renamed: %q", g)
	}
	if got := f.pendingBackfill(t); len(got) != 0 {
		t.Fatalf("pending: %v", got)
	}

	items := []*provisionerv1.ClientGroup{}
	for i := 1; i <= 5; i++ {
		items = append(items, &provisionerv1.ClientGroup{SubscriptionId: subID(i), Group: resellerGroup})
	}
	items = append(items, &provisionerv1.ClientGroup{SubscriptionId: subID(99), Group: resellerGroup}) // not held: skipped
	resp, err := f.client.SetClientGroups(ctx, &provisionerv1.SetClientGroupsRequest{Clients: items})
	if err != nil || resp.GetUpdated() != 5 {
		t.Fatalf("SetClientGroups: %v %v", resp, err)
	}
	if got := f.pendingBackfill(t); len(got) != 1 {
		t.Fatalf("the server is not waiting for the backfill: %v", got)
	}
	f.srv.BackfillGroups(ctx)
	for email, want := range map[string]string{
		"u1-legacy": resellerGroup, "u2-none": resellerGroup, "u3-default": resellerGroup, "u5-other": resellerGroup,
		"u4-vip": "vip", "stranger": "",
	} {
		if g := groupOf(f.fake, email); g != want {
			t.Errorf("%s is in %q, want %q", email, g, want)
		}
	}
	if got := f.pendingBackfill(t); len(got) != 0 {
		t.Fatalf("pending after: %v", got)
	}
	if cm, _ := f.st.GetClientMap(ctx, subID(3)); cm == nil || cm.PanelGroup != resellerGroup {
		t.Fatalf("the group is not remembered: %+v", cm)
	}

	// Given back: the default group again.
	if _, err := f.client.SetClientGroups(ctx, &provisionerv1.SetClientGroupsRequest{Clients: []*provisionerv1.ClientGroup{{SubscriptionId: subID(2)}}}); err != nil {
		t.Fatal(err)
	}
	f.srv.BackfillGroups(ctx)
	if g := groupOf(f.fake, "u2-none"); g != xui.BotGroup {
		t.Fatalf("a client given back is in %q", g)
	}

	// Bad calls.
	for name, req := range map[string]*provisionerv1.SetClientGroupsRequest{
		"a bad group": {Clients: []*provisionerv1.ClientGroup{{SubscriptionId: subID(1), Group: " x"}}},
		"no id":       {Clients: []*provisionerv1.ClientGroup{{Group: "BOBRES-X"}}},
		"too many":    {Clients: make([]*provisionerv1.ClientGroup, maxClientGroupsPerCall+1)},
	} {
		if req.Clients[0] == nil {
			for i := range req.Clients {
				req.Clients[i] = &provisionerv1.ClientGroup{SubscriptionId: subID(1)}
			}
		}
		if _, err := f.client.SetClientGroups(ctx, req); status.Code(err) != codes.InvalidArgument {
			t.Errorf("%s: %v", name, err)
		}
	}
}
