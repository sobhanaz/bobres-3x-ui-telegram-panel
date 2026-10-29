package server

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	provisionerv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/provisioner/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/provisioner/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// limit sets a server's client limit the way the dashboard does: an update that carries every setting.
func (f *fixture) limit(t *testing.T, sv *store.Server, n int32) {
	t.Helper()
	if _, err := f.client.UpdateServer(context.Background(), &provisionerv1.UpdateServerRequest{
		Id: sv.ID, Name: sv.Name, Enabled: true, Weight: sv.Weight, BaseUrl: sv.BaseURL, AllowPrivate: sv.AllowPrivate, MaxClients: proto.Int32(n),
	}); err != nil {
		t.Fatalf("limit %s to %d: %v", sv.Name, n, err)
	}
}

// A full server is passed over when a new client is picked, whatever its weight, and a plan that names only it waits.
func TestCreateClientSkipsAFullServer(t *testing.T) {
	f := newFixture(t) // "fake", weight 100
	fakeB, b := f.addPanel(t, "b", 100)
	f.limit(t, f.server, 2)
	for i := 0; i < 4; i++ { // the older server takes the first, b the second, and so on: 2 and 2
		if _, err := f.create(t, i); err != nil {
			t.Fatalf("client %d: %v", i, err)
		}
	}
	if l := f.listed(t, f.server.ID); l.GetClients() != 2 || l.GetMaxClients() != 2 {
		t.Fatalf("listed: %+v", l)
	}
	// The fake server is full now, b is not: every client goes to b, however loaded it is.
	for i := 4; i < 8; i++ {
		cl, err := f.create(t, i)
		if err != nil || cl.GetServerId() != b.ID {
			t.Fatalf("client %d with the first server full: %v %+v", i, err, cl)
		}
	}
	if l := f.listed(t, b.ID); l.GetClients() != 6 || l.GetMaxClients() != 0 {
		t.Fatalf("b: %+v", l)
	}
	// A plan whose only server is full waits: a refusal that says why, and nothing left behind.
	_, err := f.create(t, 20, f.server.ID)
	if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), `server "fake" is full (2 of 2 clients)`) {
		t.Fatalf("only a full candidate: %v", err)
	}
	if _, err := f.st.GetClientMap(context.Background(), subID(20)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a refused client left a row: %v", err)
	}
	if f.fake.Has("u20-new") || fakeB.Has("u20-new") {
		t.Fatal("a refused client was made on a panel")
	}
	// The clients it has keep working.
	if _, err := f.client.GetUsage(context.Background(), &provisionerv1.GetUsageRequest{SubscriptionId: subID(0)}); err != nil {
		t.Fatalf("usage of a client on the full server: %v", err)
	}
}

// A request that names a full server is refused with the reason, not sent to another server; a free one is honoured.
func TestExplicitServerThatIsFull(t *testing.T) {
	f := newFixture(t)
	_, b := f.addPanel(t, "b", 100)
	f.limit(t, f.server, 1)
	ctx := context.Background()
	if _, err := f.client.CreateClient(ctx, &provisionerv1.CreateClientRequest{SubscriptionId: subID(1), Email: "u1-new", ServerId: f.server.ID}); err != nil {
		t.Fatalf("the one place: %v", err)
	}
	_, err := f.client.CreateClient(ctx, &provisionerv1.CreateClientRequest{SubscriptionId: subID(2), Email: "u2-new", ServerId: f.server.ID})
	if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "is full (1 of 1 clients)") || !strings.Contains(err.Error(), "raise its limit") {
		t.Fatalf("a full server named: %v", err)
	}
	if f.fake.Has("u2-new") {
		t.Fatal("a refused client was made on the full panel")
	}
	if cl, err := f.client.CreateClient(ctx, &provisionerv1.CreateClientRequest{SubscriptionId: subID(3), Email: "u3-new", ServerId: b.ID}); err != nil || cl.GetServerId() != b.ID {
		t.Fatalf("a server with room: %v %+v", err, cl)
	}
	// A retry of the client that has its place is not refused for the server being full.
	if cl, err := f.client.CreateClient(ctx, &provisionerv1.CreateClientRequest{SubscriptionId: subID(1), Email: "u1-new", ServerId: f.server.ID}); err != nil || cl.GetServerId() != f.server.ID {
		t.Fatalf("a retry on the full server: %v %+v", err, cl)
	}
}

// Every candidate full: the order waits (a refusal, like no healthy server), and goes through as soon as a limit is raised or a
// server is added, with nothing reserved meanwhile.
func TestAllServersFull(t *testing.T) {
	f := newFixture(t)
	fakeB, b := f.addPanel(t, "b", 100)
	f.limit(t, f.server, 1)
	f.limit(t, b, 1)
	for i := 1; i <= 2; i++ {
		if _, err := f.create(t, i); err != nil {
			t.Fatalf("client %d: %v", i, err)
		}
	}
	_, err := f.create(t, 3)
	if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "every server a new client may go to is full (2 servers") {
		t.Fatalf("all full: %v", err)
	}
	_, err = f.create(t, 3, f.server.ID, b.ID)
	if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "is full") {
		t.Fatalf("both candidates full: %v", err)
	}
	if _, err := f.st.GetClientMap(context.Background(), subID(3)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a client that waits holds a place: %v", err)
	}
	// The retry after the owner raised b's limit goes to b.
	f.limit(t, b, 2)
	cl, err := f.create(t, 3)
	if err != nil || cl.GetServerId() != b.ID || !fakeB.Has("u3-new") {
		t.Fatalf("after raising the limit: %v %+v", err, cl)
	}
	// ... and the next waits again.
	if _, err := f.create(t, 4); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("full again: %v", err)
	}
	// An unlimited server added meanwhile takes it.
	_, c := f.addPanel(t, "c", 100)
	if cl, err := f.create(t, 4); err != nil || cl.GetServerId() != c.ID {
		t.Fatalf("a server with no limit was added: %v %+v", err, cl)
	}
}

// An update from a caller that does not know the limit (it leaves the field unset) keeps it; one that sends 0 lifts it.
func TestUpdateKeepsTheLimitUnlessItIsSent(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.limit(t, f.server, 5)
	f.disable(t, f.server) // sends no max_clients
	if l := f.listed(t, f.server.ID); l.GetMaxClients() != 5 || l.GetEnabled() {
		t.Fatalf("after an update without the field: %+v", l)
	}
	f.limit(t, f.server, 0)
	if l := f.listed(t, f.server.ID); l.GetMaxClients() != 0 {
		t.Fatalf("after sending 0: %+v", l)
	}
	for _, bad := range []int32{-1, 1_000_001} {
		_, err := f.client.UpdateServer(ctx, &provisionerv1.UpdateServerRequest{
			Id: f.server.ID, Name: f.server.Name, Enabled: true, Weight: f.server.Weight, BaseUrl: f.server.BaseURL, MaxClients: proto.Int32(bad),
		})
		if status.Code(err) != codes.InvalidArgument || !strings.Contains(err.Error(), "max_clients") {
			t.Errorf("a limit of %d: %v", bad, err)
		}
		_, err = f.client.AddServer(ctx, &provisionerv1.AddServerRequest{
			Name: "x", BaseUrl: f.fake.URL, ApiToken: panelTok, AllowPrivate: true, SkipCheck: true, MaxClients: bad,
		})
		if status.Code(err) != codes.InvalidArgument || !strings.Contains(err.Error(), "max_clients") {
			t.Errorf("a new server with a limit of %d: %v", bad, err)
		}
	}
	added, err := f.client.AddServer(ctx, &provisionerv1.AddServerRequest{
		Name: "limited", BaseUrl: f.fake.URL, ApiToken: panelTok, AllowPrivate: true, SkipCheck: true, MaxClients: 40,
	})
	if err != nil || added.GetMaxClients() != 40 {
		t.Fatalf("add with a limit: %v %+v", err, added)
	}
}

// Creates at the same moment never take servers past their limits: of 20 for two servers with 3 and 4 places, exactly 7 are made, the
// others wait, and each panel holds only what its server may.
func TestConcurrentCreatesNeverExceedTheLimits(t *testing.T) {
	f := newFixture(t)
	fakeB, b := f.addPanel(t, "b", 100)
	f.limit(t, f.server, 3)
	f.limit(t, b, 4)
	const tries = 20
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		on      = map[string]int{}
		waiting int
	)
	start := make(chan struct{})
	for i := 1; i <= tries; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			cl, err := f.create(t, i)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				on[cl.GetServerId()]++
			case status.Code(err) == codes.FailedPrecondition && strings.Contains(err.Error(), "full"):
				waiting++
			default:
				t.Errorf("create %d: %v", i, err)
			}
		}()
	}
	close(start)
	wg.Wait()
	if on[f.server.ID] != 3 || on[b.ID] != 4 || waiting != tries-7 {
		t.Fatalf("placed %v, %d waiting; want 3 and 4 placed, %d waiting", on, waiting, tries-7)
	}
	if l := f.listed(t, f.server.ID); l.GetClients() != 3 {
		t.Fatalf("the first server holds %d", l.GetClients())
	}
	if l := f.listed(t, b.ID); l.GetClients() != 4 {
		t.Fatalf("b holds %d", l.GetClients())
	}
	if f.fake.Calls("add") != 3 || fakeB.Calls("add") != 4 {
		t.Fatalf("clients made on the panels: %d and %d", f.fake.Calls("add"), fakeB.Calls("add"))
	}
}
