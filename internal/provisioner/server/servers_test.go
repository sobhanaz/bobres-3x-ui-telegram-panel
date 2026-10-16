package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	provisionerv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/provisioner/v1"
	bcrypto "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/crypto"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/provisioner/adapter"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/provisioner/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testdb"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/xui"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/xuifake"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// addPanel registers one more fake panel (it accepts panelTok).
func (f *fixture) addPanel(t *testing.T, name string, weight int32) (*xuifake.Server, *store.Server) {
	t.Helper()
	fake := xuifake.NewServer(panelTok)
	t.Cleanup(fake.Close)
	sv, err := f.st.AddServer(context.Background(), &store.Server{Name: name, BaseURL: fake.URL, Token: panelTok, Enabled: true, Weight: weight})
	if err != nil {
		t.Fatal(err)
	}
	return fake, sv
}

func subID(i int) string { return fmt.Sprintf("00000000-0000-7000-8000-%012d", i) }

func (f *fixture) create(t *testing.T, i int, candidates ...string) (*provisionerv1.Client, error) {
	t.Helper()
	return f.client.CreateClient(context.Background(), &provisionerv1.CreateClientRequest{
		SubscriptionId: subID(i), Email: fmt.Sprintf("u%d-new", i), DurationDays: 30, CandidateServerIds: candidates,
	})
}

func (f *fixture) disable(t *testing.T, sv *store.Server) {
	t.Helper()
	if _, err := f.client.UpdateServer(context.Background(), &provisionerv1.UpdateServerRequest{
		Id: sv.ID, Name: sv.Name, Enabled: false, Weight: sv.Weight, BaseUrl: sv.BaseURL, AllowPrivate: sv.AllowPrivate,
	}); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) listed(t *testing.T, id string) *provisionerv1.XUIServer {
	t.Helper()
	list, err := f.client.ListServers(context.Background(), &provisionerv1.ListServersRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, sv := range list.GetServers() {
		if sv.GetId() == id {
			return sv
		}
	}
	t.Fatalf("server %s not listed", id)
	return nil
}

// New clients go to the server with the fewest clients per weight (the
// older one on a tie); a plan's candidates narrow the choice.
func TestCreateClientPicksLeastLoaded(t *testing.T) {
	f := newFixture(t) // "fake", weight 100
	fakeB, b := f.addPanel(t, "b", 300)
	want := []string{f.server.ID, b.ID, b.ID, b.ID, f.server.ID}
	for i, w := range want {
		cl, err := f.create(t, i)
		if err != nil {
			t.Fatalf("client %d: %v", i, err)
		}
		if cl.GetServerId() != w {
			t.Fatalf("client %d went to %s, want %s", i, cl.GetServerId(), w)
		}
	}
	if !fakeB.Has("u1-new") || f.fake.Has("u1-new") || !f.fake.Has("u4-new") {
		t.Fatal("clients are not on the panels they are mapped to")
	}
	// Only b allowed, although it is the busier one now.
	cl, err := f.create(t, 10, b.ID)
	if err != nil || cl.GetServerId() != b.ID {
		t.Fatalf("candidates [b]: %v %+v", err, cl)
	}
	// Ids are compared in canonical form.
	cl, err = f.create(t, 11, strings.ToUpper(f.server.ID))
	if err != nil || cl.GetServerId() != f.server.ID {
		t.Fatalf("upper-case candidate: %v %+v", err, cl)
	}
	_, err = f.create(t, 12, "00000000-0000-7000-8000-00000000ffff", "garbage")
	if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "candidate") {
		t.Fatalf("no known candidate: %v", err)
	}
	if _, err := f.st.GetClientMap(context.Background(), subID(12)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a refused client left a row: %v", err)
	}
}

// A disabled server takes no new clients, picked or named, but its existing
// clients keep working; a server failing its health check is skipped.
func TestDisabledAndUnhealthyServers(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	fakeB, b := f.addPanel(t, "b", 100)
	if _, err := f.create(t, 1); err != nil { // the older server
		t.Fatal(err)
	}
	f.disable(t, f.server)
	for i := 2; i < 4; i++ {
		if cl, err := f.create(t, i); err != nil || cl.GetServerId() != b.ID {
			t.Fatalf("client %d with the first server disabled: %v %+v", i, err, cl)
		}
	}
	_, err := f.client.CreateClient(ctx, &provisionerv1.CreateClientRequest{SubscriptionId: subID(4), Email: "u4-new", ServerId: f.server.ID})
	if status.Code(err) != codes.FailedPrecondition || f.fake.Calls("add") != 1 {
		t.Fatalf("explicitly on a disabled server: %v", err)
	}
	if _, err := f.create(t, 5, f.server.ID); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("only a disabled candidate: %v", err)
	}
	// The existing client on the disabled server keeps working.
	exp := time.Now().Add(60 * 24 * time.Hour).Unix()
	if _, err := f.client.SetClientLimits(ctx, &provisionerv1.SetClientLimitsRequest{SubscriptionId: subID(1), ExpiresAt: exp, TrafficTotalBytes: 1 << 30}); err != nil {
		t.Fatalf("renewal on a disabled server: %v", err)
	}
	if _, err := f.client.GetLinks(ctx, &provisionerv1.GetLinksRequest{SubscriptionId: subID(1)}); err != nil {
		t.Fatalf("links on a disabled server: %v", err)
	}
	// An explicit enabled server is honoured, an unknown one is NotFound.
	if cl, err := f.client.CreateClient(ctx, &provisionerv1.CreateClientRequest{SubscriptionId: subID(6), Email: "u6-new", ServerId: b.ID}); err != nil || cl.GetServerId() != b.ID {
		t.Fatalf("explicit server: %v %+v", err, cl)
	}
	if _, err := f.client.CreateClient(ctx, &provisionerv1.CreateClientRequest{SubscriptionId: subID(7), Email: "u7-new", ServerId: "nope"}); status.Code(err) != codes.NotFound {
		t.Fatalf("malformed server id: %v", err)
	}

	// b fails its health check: nothing is left.
	fakeB.SetFailNext(1000)
	f.srv.CheckHealth(ctx)
	_, err = f.create(t, 8)
	if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "healthy") {
		t.Fatalf("no healthy server: %v", err)
	}
	fakeB.SetFailNext(0)
	f.srv.CheckHealth(ctx)
	if cl, err := f.create(t, 8); err != nil || cl.GetServerId() != b.ID {
		t.Fatalf("after b recovered: %v %+v", err, cl)
	}
}

func TestNoServerAtAll(t *testing.T) {
	f := newFixture(t)
	if _, err := f.st.DB().Exec(context.Background(), "TRUNCATE provisioner.client_map, provisioner.xui_servers CASCADE"); err != nil {
		t.Fatal(err)
	}
	_, err := f.create(t, 1)
	if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "no 3x-ui server") {
		t.Fatalf("no server: %v", err)
	}
	if _, err := f.client.ListInbounds(context.Background(), &provisionerv1.ListInboundsRequest{}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("inbounds of no server: %v", err)
	}
}

// The panel dropped the connection during create, so the client may or may
// not exist there: the reservation keeps the retry on that server, even
// once it is disabled and another server is emptier.
func TestRetryStaysOnTheReservedServer(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	fakeB, _ := f.addPanel(t, "b", 1000)
	f.fake.FailRoute("add", 1)
	f.fake.FailRoute("get", 1)
	if _, err := f.create(t, 1); status.Code(err) != codes.Unavailable {
		t.Fatalf("uncertain create: %v", err)
	}
	cm, err := f.st.GetClientMap(ctx, subID(1))
	if err != nil || !cm.Pending || cm.ServerID != f.server.ID || len(cm.InboundIDs) != 0 {
		t.Fatalf("reservation: %v %+v", err, cm)
	}
	// Not a client yet for the other calls.
	if _, err := f.client.GetLinks(ctx, &provisionerv1.GetLinksRequest{SubscriptionId: subID(1)}); status.Code(err) != codes.NotFound {
		t.Fatalf("links of a reservation: %v", err)
	}
	if _, err := f.client.GetUsage(ctx, &provisionerv1.GetUsageRequest{SubscriptionId: subID(1)}); status.Code(err) != codes.NotFound {
		t.Fatalf("usage of a reservation: %v", err)
	}
	f.disable(t, f.server)
	cl, err := f.create(t, 1)
	if err != nil || cl.GetServerId() != f.server.ID || len(cl.GetInboundIds()) != 2 {
		t.Fatalf("retry: %v %+v", err, cl)
	}
	if !f.fake.Has("u1-new") || fakeB.Calls("add") != 0 {
		t.Fatal("the retry left the reserved server")
	}
	if cm, _ = f.st.GetClientMap(ctx, subID(1)); cm.Pending || len(cm.InboundIDs) != 2 {
		t.Fatalf("after the retry: %+v", cm)
	}
}

// A crash after the panel created the client (no response, no map update):
// the retry finds the reservation and adopts the client on that server.
func TestRetryAdoptsOnTheReservedServer(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	fakeB, _ := f.addPanel(t, "b", 1000)
	xid := f.st.SubIDFor(subID(1))
	if _, _, err := f.st.ReserveClient(ctx, &store.ClientMap{SubscriptionID: subID(1), ServerID: f.server.ID, Email: "u1-new", XUISubID: &xid}); err != nil {
		t.Fatal(err)
	}
	f.fake.Seed("u1-new", xid, []int{1, 2})
	f.disable(t, f.server)
	cl, err := f.create(t, 1)
	if err != nil || cl.GetServerId() != f.server.ID || cl.GetXuiSubId() != xid {
		t.Fatalf("adopt: %v %+v", err, cl)
	}
	if fakeB.Calls("add") != 0 {
		t.Fatal("a second client was created elsewhere")
	}
	// The same email under another subscription is never someone's retry.
	_, err = f.client.CreateClient(ctx, &provisionerv1.CreateClientRequest{SubscriptionId: subID(1), Email: "u1-other"})
	if status.Code(err) != codes.AlreadyExists {
		t.Fatalf("another email for a provisioned subscription: %v", err)
	}
}

// When nothing of ours can be on the reserved panel, the reservation goes,
// so the retry picks a server again.
func TestReservationIsReleasedWhenNothingWasCreated(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	gone := func(i int) {
		t.Helper()
		if _, err := f.st.GetClientMap(ctx, subID(i)); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("reservation %d kept: %v", i, err)
		}
	}
	// Refused before the panel's create was called.
	f.fake.SetInboundsEnabled(false)
	if _, err := f.create(t, 1); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("no inbound: %v", err)
	}
	gone(1)
	f.fake.SetInboundsEnabled(true)
	// The create failed and the panel says it has no such client.
	f.fake.FailRoute("add", 1)
	if _, err := f.create(t, 2); status.Code(err) != codes.Unavailable {
		t.Fatalf("failed create: %v", err)
	}
	gone(2)
	// The email is someone else's on the panel.
	f.fake.Seed("u3-new", "somebody-elses-sub", []int{1})
	if _, err := f.create(t, 3); status.Code(err) != codes.AlreadyExists {
		t.Fatalf("foreign client: %v", err)
	}
	gone(3)
	// An earlier attempt's reservation, on a panel without our client.
	xid := f.st.SubIDFor(subID(4))
	if _, _, err := f.st.ReserveClient(ctx, &store.ClientMap{SubscriptionID: subID(4), ServerID: f.server.ID, Email: "u4-new", XUISubID: &xid}); err != nil {
		t.Fatal(err)
	}
	f.fake.SetInboundsEnabled(false)
	if _, err := f.create(t, 4); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("no inbound for an old reservation: %v", err)
	}
	gone(4)
	f.fake.SetInboundsEnabled(true)
	if _, err := f.create(t, 4); err != nil {
		t.Fatalf("retry after the release: %v", err)
	}
}

// Concurrent attempts for one subscription end with one client on one panel.
func TestConcurrentCreatesOfOneSubscription(t *testing.T) {
	f := newFixture(t)
	f.addPanel(t, "b", 100)
	const n = 8
	type result struct {
		server string
		err    error
	}
	results := make(chan result, n)
	for range n {
		go func() {
			cl, err := f.create(t, 1)
			results <- result{cl.GetServerId(), err}
		}()
	}
	first := ""
	for range n {
		r := <-results
		if r.err != nil {
			t.Fatalf("attempt: %v", r.err)
		}
		if first == "" {
			first = r.server
		}
		if r.server != first {
			t.Fatalf("attempts answered different servers: %s and %s", first, r.server)
		}
	}
	list, err := f.client.ListServers(context.Background(), &provisionerv1.ListServersRequest{})
	if err != nil {
		t.Fatal(err)
	}
	var total int64
	for _, sv := range list.GetServers() {
		total += sv.GetClients()
	}
	if total != 1 {
		t.Fatalf("clients mapped: %d", total)
	}
}

// Detached inbounds are skipped for new clients only.
func TestInboundAttach(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.create(t, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := f.client.SetInboundAttach(ctx, &provisionerv1.SetInboundAttachRequest{ServerId: f.server.ID, InboundId: 2, Attach: false}); err != nil {
		t.Fatal(err)
	}
	ins, err := f.client.ListInbounds(ctx, &provisionerv1.ListInboundsRequest{ServerId: f.server.ID})
	if err != nil || len(ins.GetInbounds()) != 3 {
		t.Fatalf("inbounds: %v %v", err, ins)
	}
	for _, in := range ins.GetInbounds() {
		if in.GetAttach() != (in.GetId() != 2) || in.GetProtocol() == "" || in.GetPort() == 0 {
			t.Errorf("inbound %+v", in)
		}
	}
	cl, err := f.create(t, 2)
	if err != nil || len(cl.GetInboundIds()) != 1 || cl.GetInboundIds()[0] != 1 {
		t.Fatalf("new client: %v %+v", err, cl)
	}
	if cm, err := f.st.GetClientMap(ctx, subID(1)); err != nil || len(cm.InboundIDs) != 2 {
		t.Fatalf("the existing client lost an inbound: %v %+v", err, cm)
	}
	if f.fake.Calls("update") != 0 {
		t.Fatal("the panel's clients were changed")
	}
	// Nothing left to attach to: refused, and nothing reserved.
	if _, err := f.client.SetInboundAttach(ctx, &provisionerv1.SetInboundAttachRequest{ServerId: f.server.ID, InboundId: 1, Attach: false}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.create(t, 3); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("every inbound detached: %v", err)
	}
	if _, err := f.st.GetClientMap(ctx, subID(3)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("reservation kept: %v", err)
	}
	if _, err := f.client.SetInboundAttach(ctx, &provisionerv1.SetInboundAttachRequest{ServerId: f.server.ID, InboundId: 1, Attach: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.create(t, 3); err != nil {
		t.Fatalf("attached again: %v", err)
	}
	for name, req := range map[string]*provisionerv1.SetInboundAttachRequest{
		"unknown server":   {ServerId: "00000000-0000-7000-8000-00000000ffff", InboundId: 1},
		"malformed server": {ServerId: "x", InboundId: 1},
	} {
		if _, err := f.client.SetInboundAttach(ctx, req); status.Code(err) != codes.NotFound {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := f.client.SetInboundAttach(ctx, &provisionerv1.SetInboundAttachRequest{ServerId: f.server.ID}); status.Code(err) != codes.InvalidArgument {
		t.Errorf("inbound 0: %v", err)
	}
}

// A plan's inbound choice decides the inbounds of a new client on a server:
// exactly those, even over a detached one; a choice for another server is
// not this one's; a choice that is partly gone attaches what is left; one
// that is wholly gone is refused and keeps nothing; no choice is the default.
func TestCreateClientInboundChoice(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	other := "00000000-0000-7000-8000-00000000ffff"
	create := func(i int, sel ...*provisionerv1.InboundSelection) (*provisionerv1.Client, error) {
		t.Helper()
		return f.client.CreateClient(ctx, &provisionerv1.CreateClientRequest{
			SubscriptionId: subID(i), Email: fmt.Sprintf("c%d-new", i), DurationDays: 30, Inbounds: sel,
		})
	}
	ids := func(cl *provisionerv1.Client) []int32 {
		got := append([]int32(nil), cl.GetInboundIds()...)
		slices.Sort(got)
		return got
	}

	// The fake panel has 1 and 2 enabled, 3 not. Detach 2, then choose it
	// (with 1, twice): the choice wins over the switch.
	if _, err := f.client.SetInboundAttach(ctx, &provisionerv1.SetInboundAttachRequest{ServerId: f.server.ID, InboundId: 2, Attach: false}); err != nil {
		t.Fatal(err)
	}
	cl, err := create(1, &provisionerv1.InboundSelection{ServerId: f.server.ID, InboundIds: []int32{2, 1, 2}})
	if err != nil || !slices.Equal(ids(cl), []int32{1, 2}) {
		t.Fatalf("chosen 1,2: %v %+v", err, cl)
	}
	if cm, err := f.st.GetClientMap(ctx, subID(1)); err != nil || len(cm.InboundIDs) != 2 {
		t.Fatalf("reservation: %v %+v", err, cm)
	}
	// 3 is disabled in the panel: left out (2 stays).
	cl, err = create(9, &provisionerv1.InboundSelection{ServerId: f.server.ID, InboundIds: []int32{3, 2}})
	if err != nil || !slices.Equal(ids(cl), []int32{2}) {
		t.Fatalf("chosen 2,3 (3 disabled): %v %+v", err, cl)
	}
	// The server id may be written in capitals.
	cl, err = create(2, &provisionerv1.InboundSelection{ServerId: strings.ToUpper(f.server.ID), InboundIds: []int32{1}})
	if err != nil || !slices.Equal(ids(cl), []int32{1}) {
		t.Fatalf("chosen 1: %v %+v", err, cl)
	}
	// A choice made for another server says nothing here: the default, 2 detached.
	cl, err = create(3, &provisionerv1.InboundSelection{ServerId: other, InboundIds: []int32{2}})
	if err != nil || !slices.Equal(ids(cl), []int32{1}) {
		t.Fatalf("another server's choice: %v %+v", err, cl)
	}
	// No choice at all: the same default.
	cl, err = create(4)
	if err != nil || !slices.Equal(ids(cl), []int32{1}) {
		t.Fatalf("no choice: %v %+v", err, cl)
	}
	// Partly gone: 1 and an inbound the panel does not have; 1 is attached.
	cl, err = create(5, &provisionerv1.InboundSelection{ServerId: f.server.ID, InboundIds: []int32{1, 99}})
	if err != nil || !slices.Equal(ids(cl), []int32{1}) {
		t.Fatalf("partly gone: %v %+v", err, cl)
	}
	// Wholly gone: refused, with the server named, and nothing is reserved;
	// there is no fall back to the default.
	_, err = create(6, &provisionerv1.InboundSelection{ServerId: f.server.ID, InboundIds: []int32{98, 99}})
	if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "edit the plan's inbounds") {
		t.Fatalf("wholly gone: %v", err)
	}
	if _, err := f.st.GetClientMap(ctx, subID(6)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("reservation kept: %v", err)
	}
	// Disabled in the panel counts as gone.
	f.fake.SetInboundsEnabled(false)
	if _, err := create(7, &provisionerv1.InboundSelection{ServerId: f.server.ID, InboundIds: []int32{1}}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("chosen inbound disabled: %v", err)
	}
	if _, err := f.st.GetClientMap(ctx, subID(7)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("reservation kept: %v", err)
	}
	f.fake.SetInboundsEnabled(true)
	if _, err := create(7, &provisionerv1.InboundSelection{ServerId: f.server.ID, InboundIds: []int32{1}}); err != nil {
		t.Fatalf("enabled again: %v", err)
	}
	// An id that is not positive is a malformed request.
	for _, bad := range []int32{0, -1} {
		if _, err := create(8, &provisionerv1.InboundSelection{ServerId: f.server.ID, InboundIds: []int32{1, bad}}); status.Code(err) != codes.InvalidArgument {
			t.Errorf("inbound %d: %v", bad, err)
		}
	}
	if _, err := f.st.GetClientMap(ctx, subID(8)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("reservation kept: %v", err)
	}
}

// No answer carries a token, and the listing reports health, version,
// clients and weight.
func TestListServers(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	fakeB, b := f.addPanel(t, "b", 250)
	if _, err := f.create(t, 1); err != nil {
		t.Fatal(err)
	}
	fakeB.SetPanelVersion("3.7.2")
	f.srv.CheckHealth(ctx)
	list, err := f.client.ListServers(ctx, &provisionerv1.ListServersRequest{})
	if err != nil || len(list.GetServers()) != 2 || list.GetTestedMinVersion() != "v3.8.5" || list.GetTestedMaxVersion() != "v3.9.0" {
		t.Fatalf("list: %v %+v", err, list)
	}
	raw, _ := proto.Marshal(list)
	if strings.Contains(string(raw), panelTok) {
		t.Fatal("ListServers returned a token")
	}
	a := f.listed(t, f.server.ID)
	if a.GetClients() != 1 || !a.GetHealthy() || a.GetCheckedAt() == 0 || a.GetPanelVersion() != "v3.9.0" || a.GetVersionUntested() ||
		!a.GetHasToken() || a.GetWeight() != 100 || a.GetSubBaseUrl() == "" {
		t.Fatalf("first server: %+v", a)
	}
	if lb := f.listed(t, b.ID); lb.GetPanelVersion() != "v3.7.2" || !lb.GetVersionUntested() || lb.GetWeight() != 250 || lb.GetClients() != 0 {
		t.Fatalf("b: %+v", lb)
	}
}

func TestUpdateServer(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	other := xuifake.NewServer(panelTok) // accepts the stored token: any use of it would count
	t.Cleanup(other.Close)
	base := &provisionerv1.UpdateServerRequest{Id: f.server.ID, Name: "fake", Enabled: true, Weight: 100, BaseUrl: f.fake.URL,
		SubBaseUrl: "https://sub.example.com:2096/sub/"}
	upd := func(change func(*provisionerv1.UpdateServerRequest)) (*provisionerv1.XUIServer, error) {
		req := proto.Clone(base).(*provisionerv1.UpdateServerRequest)
		change(req)
		return f.client.UpdateServer(ctx, req)
	}

	// Renaming, weighting and disabling never contact the panel.
	f.fake.SetFailNext(1000)
	sv, err := upd(func(r *provisionerv1.UpdateServerRequest) { r.Name, r.Weight, r.Enabled = "main", 700, false })
	if err != nil || sv.GetName() != "main" || sv.GetWeight() != 700 || sv.GetEnabled() || sv.GetManaged() != "dashboard" {
		t.Fatalf("settings: %v %+v", err, sv)
	}
	if f.fake.Calls("options")+f.fake.Calls("status") != 0 {
		t.Fatal("a settings change contacted the panel")
	}
	f.fake.SetFailNext(0)

	// The stored token goes to no other host.
	if _, err := upd(func(r *provisionerv1.UpdateServerRequest) { r.BaseUrl = other.URL }); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("new host without a token: %v", err)
	}
	if _, err := f.client.TestServer(ctx, &provisionerv1.TestServerRequest{Id: f.server.ID, BaseUrl: other.URL}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("testing a new host with the stored token: %v", err)
	}
	if other.Calls("options")+other.Calls("status") != 0 {
		t.Fatal("the stored token was sent to another host")
	}
	// The same host with another base path keeps it.
	if _, err := upd(func(r *provisionerv1.UpdateServerRequest) { r.BaseUrl = f.fake.URL + "/" }); err != nil {
		t.Fatalf("same host: %v", err)
	}

	// A wrong token fails the test and changes nothing.
	_, err = upd(func(r *provisionerv1.UpdateServerRequest) { r.ApiToken = "wrong-token" })
	if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "refused the API token") || strings.Contains(err.Error(), "wrong-token") {
		t.Fatalf("wrong token: %v", err)
	}
	if got, _ := f.st.GetServer(ctx, f.server.ID); got.Token != panelTok {
		t.Fatal("a failed test changed the token")
	}

	// Moving to another panel with its token: tested, then used.
	cached := func() adapter.PanelAdapter {
		f.srv.mu.Lock()
		defer f.srv.mu.Unlock()
		return f.srv.adapters[f.server.ID].a
	}
	if _, err := f.client.ListInbounds(ctx, &provisionerv1.ListInboundsRequest{ServerId: f.server.ID}); err != nil {
		t.Fatal(err)
	}
	before := cached()
	sv, err = upd(func(r *provisionerv1.UpdateServerRequest) { r.BaseUrl, r.ApiToken = other.URL, panelTok })
	if err != nil || sv.GetBaseUrl() != other.URL || !sv.GetHealthy() || sv.GetPanelVersion() != "v3.9.0" {
		t.Fatalf("new panel: %v %+v", err, sv)
	}
	if _, err := f.client.ListInbounds(ctx, &provisionerv1.ListInboundsRequest{ServerId: f.server.ID}); err != nil {
		t.Fatal(err)
	}
	if before == nil || cached() == before || other.Calls("options") < 2 {
		t.Fatal("the cached panel client was not rebuilt")
	}

	// Saved without testing: health starts over.
	sv, err = upd(func(r *provisionerv1.UpdateServerRequest) {
		r.BaseUrl, r.ApiToken, r.SkipCheck = other.URL, "not-tested", true
	})
	if err != nil || sv.GetCheckedAt() != 0 {
		t.Fatalf("skip check: %v %+v", err, sv)
	}

	// Stored now, with the token "not-tested".
	base.BaseUrl = other.URL
	if _, err := f.st.AddServer(ctx, &store.Server{Name: "taken", BaseURL: "https://t.test", Token: "t", Enabled: true}); err != nil { //gitleaks:allow test fixture
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		change func(*provisionerv1.UpdateServerRequest)
		code   codes.Code
	}{
		"name taken":     {func(r *provisionerv1.UpdateServerRequest) { r.Name = "taken" }, codes.AlreadyExists},
		"no name":        {func(r *provisionerv1.UpdateServerRequest) { r.Name = "" }, codes.InvalidArgument},
		"weight 0":       {func(r *provisionerv1.UpdateServerRequest) { r.Weight = 0 }, codes.InvalidArgument},
		"no url":         {func(r *provisionerv1.UpdateServerRequest) { r.BaseUrl = "" }, codes.InvalidArgument},
		"bad sub url":    {func(r *provisionerv1.UpdateServerRequest) { r.SubBaseUrl = "javascript:x" }, codes.InvalidArgument},
		"unknown server": {func(r *provisionerv1.UpdateServerRequest) { r.Id = "00000000-0000-7000-8000-00000000ffff" }, codes.NotFound},
		"malformed id":   {func(r *provisionerv1.UpdateServerRequest) { r.Id = "1 OR 1=1" }, codes.NotFound},
	} {
		if _, err := upd(c.change); status.Code(err) != c.code {
			t.Errorf("%s: want %v, got %v", name, c.code, err)
		}
	}
}

func TestTestServer(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	before, _ := f.st.ListServers(ctx)

	res, err := f.client.TestServer(ctx, &provisionerv1.TestServerRequest{BaseUrl: f.fake.URL, ApiToken: panelTok})
	if err != nil || !res.GetOk() || res.GetPanelVersion() != "v3.9.0" || res.GetInbounds() != 3 {
		t.Fatalf("candidate: %v %+v", err, res)
	}
	res, err = f.client.TestServer(ctx, &provisionerv1.TestServerRequest{BaseUrl: f.fake.URL, ApiToken: "wrong-token"})
	if err != nil || res.GetOk() || res.GetDetail() != "the panel refused the API token" {
		t.Fatalf("wrong token: %v %+v", err, res)
	}
	// An existing server with its stored settings, or with a new token.
	if res, err = f.client.TestServer(ctx, &provisionerv1.TestServerRequest{Id: f.server.ID}); err != nil || !res.GetOk() {
		t.Fatalf("stored settings: %v %+v", err, res)
	}
	if res, err = f.client.TestServer(ctx, &provisionerv1.TestServerRequest{Id: f.server.ID, ApiToken: "wrong-token"}); err != nil || res.GetOk() {
		t.Fatalf("new token: %v %+v", err, res)
	}
	// A panel that does not answer, and one that answers with an error page.
	down := xuifake.NewServer(panelTok)
	down.Close()
	if res, err = f.client.TestServer(ctx, &provisionerv1.TestServerRequest{BaseUrl: down.URL, ApiToken: panelTok}); err != nil || res.GetOk() || !strings.HasPrefix(res.GetDetail(), "panel unreachable") {
		t.Fatalf("down: %v %+v", err, res)
	}
	if res, err = f.client.TestServer(ctx, &provisionerv1.TestServerRequest{BaseUrl: f.fake.URL + "/wrong-base", ApiToken: panelTok}); err != nil || res.GetOk() || !strings.Contains(res.GetDetail(), "404") {
		t.Fatalf("wrong base path: %v %+v", err, res)
	}
	// Address policy: production settings refuse plain http to a public host.
	strict := New(f.st)
	if _, err := strict.TestServer(ctx, &provisionerv1.TestServerRequest{BaseUrl: "http://panel.example.com", ApiToken: "t"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("plain http: %v", err)
	}
	if res, err := strict.TestServer(ctx, &provisionerv1.TestServerRequest{BaseUrl: f.fake.URL, ApiToken: panelTok}); err == nil && res.GetOk() {
		t.Fatal("a loopback panel passed without allow_private")
	}
	for name, req := range map[string]*provisionerv1.TestServerRequest{
		"nothing":        {},
		"no token":       {BaseUrl: f.fake.URL},
		"not a url":      {BaseUrl: "::::", ApiToken: "t"},
		"unknown server": {Id: "00000000-0000-7000-8000-00000000ffff"},
	} {
		if _, err := f.client.TestServer(ctx, req); status.Code(err) == codes.OK {
			t.Errorf("%s: accepted", name)
		}
	}
	after, _ := f.st.ListServers(ctx)
	if len(after) != len(before) || !after[0].UpdatedAt.Equal(before[0].UpdatedAt) || after[0].CheckedAt != nil {
		t.Fatal("TestServer saved something")
	}
}

// AddServer tests the connection unless told not to, and records the result.
func TestAddServerTestsTheConnection(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	other := xuifake.NewServer(panelTok)
	t.Cleanup(other.Close)
	_, err := f.client.AddServer(ctx, &provisionerv1.AddServerRequest{Name: "b", BaseUrl: other.URL, ApiToken: "wrong-token"})
	if status.Code(err) != codes.FailedPrecondition || strings.Contains(err.Error(), "wrong-token") {
		t.Fatalf("wrong token: %v", err)
	}
	if n, _ := f.st.CountServers(ctx); n != 1 {
		t.Fatal("a server that failed its test was saved")
	}
	sv, err := f.client.AddServer(ctx, &provisionerv1.AddServerRequest{Name: "b", BaseUrl: other.URL, ApiToken: panelTok, Weight: 10})
	if err != nil || !sv.GetHealthy() || sv.GetPanelVersion() != "v3.9.0" || sv.GetWeight() != 10 || sv.GetManaged() != "dashboard" {
		t.Fatalf("add: %v %+v", err, sv)
	}
	raw, _ := proto.Marshal(sv)
	if strings.Contains(string(raw), panelTok) {
		t.Fatal("AddServer returned the token")
	}
	if !sv.GetEnabled() {
		t.Fatal("a request without enabled (an older caller) added the server switched off")
	}
}

// enabled=false saves the server switched off in the insert itself: it is
// never on, so no client lands on it, and it is still tested and listed.
func TestAddServerSwitchedOff(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	other := xuifake.NewServer(panelTok)
	t.Cleanup(other.Close)
	f.disable(t, f.server) // only the new server could take the client
	sv, err := f.client.AddServer(ctx, &provisionerv1.AddServerRequest{Name: "off", BaseUrl: other.URL, ApiToken: panelTok,
		Enabled: proto.Bool(false)})
	if err != nil || sv.GetEnabled() || !sv.GetHealthy() || sv.GetPanelVersion() != "v3.9.0" {
		t.Fatalf("add switched off: %v %+v", err, sv)
	}
	var enabled, untouched bool
	if err := f.st.DB().QueryRow(ctx, `SELECT enabled, updated_at = created_at FROM provisioner.xui_servers WHERE id = $1`,
		sv.GetId()).Scan(&enabled, &untouched); err != nil || enabled || !untouched {
		t.Fatalf("row: enabled %v, never updated %v, %v", enabled, untouched, err)
	}
	if _, err := f.create(t, 1); status.Code(err) != codes.FailedPrecondition || other.Calls("add") != 0 {
		t.Fatalf("a client on the switched-off server: %v", err)
	}
	if lv := f.listed(t, sv.GetId()); lv.GetEnabled() {
		t.Fatalf("listed: %+v", lv)
	}

	on, err := f.client.AddServer(ctx, &provisionerv1.AddServerRequest{Name: "on", BaseUrl: other.URL, ApiToken: panelTok,
		Enabled: proto.Bool(true), SkipCheck: true})
	if err != nil || !on.GetEnabled() {
		t.Fatalf("add switched on: %v %+v", err, on)
	}
}

func TestDeleteServer(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	_, b := f.addPanel(t, "b", 100)
	if _, err := f.client.CreateClient(ctx, &provisionerv1.CreateClientRequest{SubscriptionId: subID(1), Email: "u1-new", ServerId: b.ID}); err != nil {
		t.Fatal(err)
	}
	_, err := f.client.DeleteServer(ctx, &provisionerv1.DeleteServerRequest{Id: b.ID})
	if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "1 clients") {
		t.Fatalf("with a client: %v", err)
	}
	if _, err := f.client.DeleteClient(ctx, &provisionerv1.DeleteClientRequest{SubscriptionId: subID(1)}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.client.DeleteServer(ctx, &provisionerv1.DeleteServerRequest{Id: b.ID}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	for _, id := range []string{b.ID, "nope"} {
		if _, err := f.client.DeleteServer(ctx, &provisionerv1.DeleteServerRequest{Id: id}); status.Code(err) != codes.NotFound {
			t.Fatalf("delete %q again: %v", id, err)
		}
	}
	if _, err := f.create(t, 2); err != nil { // the remaining server takes new clients
		t.Fatal(err)
	}
}

// Deleting a subscription whose delivery never finished removes the
// reservation, and the client if an earlier attempt made one.
func TestDeleteClientOfAReservation(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	reserve := func(i int) string {
		xid := f.st.SubIDFor(subID(i))
		if _, _, err := f.st.ReserveClient(ctx, &store.ClientMap{SubscriptionID: subID(i), ServerID: f.server.ID, Email: fmt.Sprintf("u%d-new", i), XUISubID: &xid}); err != nil {
			t.Fatal(err)
		}
		return xid
	}
	reserve(1)
	if _, err := f.client.DeleteClient(ctx, &provisionerv1.DeleteClientRequest{SubscriptionId: subID(1)}); err != nil {
		t.Fatalf("nothing on the panel: %v", err)
	}
	f.fake.Seed("u2-new", reserve(2), []int{1})
	if _, err := f.client.DeleteClient(ctx, &provisionerv1.DeleteClientRequest{SubscriptionId: subID(2)}); err != nil || f.fake.Has("u2-new") {
		t.Fatalf("created by an earlier attempt: %v", err)
	}
	// A panel that does not answer: kept, the client may be there.
	reserve(3)
	f.fake.SetFailNext(2)
	if _, err := f.client.DeleteClient(ctx, &provisionerv1.DeleteClientRequest{SubscriptionId: subID(3)}); status.Code(err) != codes.Unavailable {
		t.Fatalf("panel down: %v", err)
	}
	for i, want := range map[int]bool{1: false, 2: false, 3: true} {
		if _, err := f.st.GetClientMap(ctx, subID(i)); (err == nil) != want {
			t.Errorf("row %d kept=%v: %v", i, want, err)
		}
	}
	// A delivered client the panel no longer has only leaves the map.
	if _, err := f.create(t, 4); err != nil {
		t.Fatal(err)
	}
	direct, err := adapter.NewXUIv3(f.fake.URL, panelTok, xui.AllowInsecureHTTP(), xui.AllowPrivateAddresses())
	if err != nil {
		t.Fatal(err)
	}
	if err := direct.Delete(ctx, "u4-new"); err != nil { // removed in the panel by hand
		t.Fatal(err)
	}
	if _, err := f.client.DeleteClient(ctx, &provisionerv1.DeleteClientRequest{SubscriptionId: subID(4)}); err != nil {
		t.Fatalf("client gone from the panel: %v", err)
	}
	if _, err := f.st.GetClientMap(ctx, subID(4)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("row kept: %v", err)
	}
	if _, err := f.client.DeleteClient(ctx, &provisionerv1.DeleteClientRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("no subscription id: %v", err)
	}
}

// The loop checks enabled servers only, records ok, error and version
// without touching updated_at, and stops with its context.
func TestHealthLoop(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	fakeB, b := f.addPanel(t, "b", 100)
	fakeOff, off := f.addPanel(t, "off", 100)
	f.disable(t, off)
	fakeB.SetFailNext(1000)
	offCalls := fakeOff.Calls("options")

	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- f.srv.RunHealth(runCtx, 20*time.Millisecond) }()
	deadline := time.Now().Add(5 * time.Second)
	for f.listed(t, b.ID).GetCheckedAt() == 0 || f.listed(t, f.server.ID).GetCheckedAt() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the loop checked nothing")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("loop: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the loop did not stop")
	}

	a := f.listed(t, f.server.ID)
	if !a.GetHealthy() || a.GetHealthError() != "" || a.GetPanelVersion() != "v3.9.0" {
		t.Fatalf("healthy server: %+v", a)
	}
	lb := f.listed(t, b.ID)
	if lb.GetHealthy() || !strings.Contains(lb.GetHealthError(), "500") {
		t.Fatalf("failing server: %+v", lb)
	}
	if lo := f.listed(t, off.ID); lo.GetCheckedAt() != 0 || fakeOff.Calls("options") != offCalls {
		t.Fatalf("a disabled server was checked: %+v", lo)
	}
	if got, _ := f.st.GetServer(ctx, f.server.ID); !got.UpdatedAt.Equal(f.server.UpdatedAt) {
		t.Fatal("a health check moved updated_at")
	}

	// Versions: unknown is not flagged, anything outside the range is.
	for v, want := range map[string]struct {
		stored   string
		untested bool
	}{
		"3.8.5": {"v3.8.5", false}, "3.9.0": {"v3.9.0", false}, "3.8.4": {"v3.8.4", true},
		"3.10.0": {"v3.10.0", true}, "": {"v3.x", false}, "dev+1a2b3c4d": {"dev+1a2b3c4d", true},
	} {
		f.fake.SetPanelVersion(v)
		f.srv.CheckHealth(ctx)
		if got := f.listed(t, f.server.ID); got.GetPanelVersion() != want.stored || got.GetVersionUntested() != want.untested {
			t.Errorf("panel says %q: stored %q untested %v", v, got.GetPanelVersion(), got.GetVersionUntested())
		}
	}

	// The status endpoint failing alone does not make a panel unhealthy.
	f.fake.FailRoute("status", 1)
	f.srv.CheckHealth(ctx)
	if got := f.listed(t, f.server.ID); !got.GetHealthy() || got.GetPanelVersion() != "v3.x" {
		t.Fatalf("status endpoint failed: %+v", got)
	}

	// The HealthCheck RPC records too, failures included.
	f.fake.SetFailNext(1000)
	hc, err := f.client.HealthCheck(ctx, &provisionerv1.HealthCheckRequest{ServerId: f.server.ID})
	if err != nil || hc.GetHealthy() || hc.GetDetail() != "panel unreachable" {
		t.Fatalf("HealthCheck: %v %+v", err, hc)
	}
	if got := f.listed(t, f.server.ID); got.GetHealthy() {
		t.Fatal("a failed HealthCheck was not recorded")
	}
}

// A token stored under another master key: the server is still listed and
// can be fixed by entering the token again.
func TestUnreadableToken(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	otherEnv, err := bcrypto.NewEnvelope([]byte("another-master-key-0123456789abcd"))
	if err != nil {
		t.Fatal(err)
	}
	otherStore, err := store.New(ctx, testdb.WithPoolCap(t, testdb.DSN(t)), otherEnv)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(otherStore.Close)
	fake := xuifake.NewServer(panelTok)
	t.Cleanup(fake.Close)
	sv, err := otherStore.AddServer(ctx, &store.Server{Name: "old-key", BaseURL: fake.URL, Token: panelTok, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := f.listed(t, sv.ID); !got.GetHasToken() {
		t.Fatalf("listed: %+v", got)
	}
	f.srv.CheckHealth(ctx)
	if got := f.listed(t, sv.ID); got.GetHealthy() || !strings.Contains(got.GetHealthError(), "decrypted") {
		t.Fatalf("health: %+v", got)
	}
	if _, err := f.client.TestServer(ctx, &provisionerv1.TestServerRequest{Id: sv.ID}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("test with the unreadable token: %v", err)
	}
	req := &provisionerv1.UpdateServerRequest{Id: sv.ID, Name: "old-key", Enabled: true, Weight: 100, BaseUrl: fake.URL}
	if _, err := f.client.UpdateServer(ctx, req); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("keep the unreadable token: %v", err)
	}
	req.ApiToken = panelTok
	if got, err := f.client.UpdateServer(ctx, req); err != nil || !got.GetHealthy() {
		t.Fatalf("token entered again: %v %+v", err, got)
	}
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (w *syncBuffer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

func (w *syncBuffer) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.String()
}

// Tokens reach no log line, answer, error or stored health error, on the
// failure paths too.
func TestTokensStayOut(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	var logs syncBuffer
	f.srv.SetLogger(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	const candidate = "candidate-secret-token-0000" //gitleaks:allow test fixture
	var seen []string
	note := func(m proto.Message, err error) {
		if m != nil {
			raw, _ := proto.Marshal(m)
			seen = append(seen, string(raw))
		}
		if err != nil {
			seen = append(seen, err.Error())
		}
	}
	note(f.client.TestServer(ctx, &provisionerv1.TestServerRequest{BaseUrl: f.fake.URL, ApiToken: candidate}))
	note(f.client.AddServer(ctx, &provisionerv1.AddServerRequest{Name: "x", BaseUrl: f.fake.URL, ApiToken: candidate}))
	note(f.client.UpdateServer(ctx, &provisionerv1.UpdateServerRequest{Id: f.server.ID, Name: "fake", Enabled: true, Weight: 100, BaseUrl: f.fake.URL, ApiToken: candidate}))
	note(f.client.AddServer(ctx, &provisionerv1.AddServerRequest{Name: "y", BaseUrl: "http://user:" + candidate + "@127.0.0.1:1", ApiToken: candidate}))
	f.fake.SetFailNext(1000)
	f.srv.CheckHealth(ctx)
	note(f.client.HealthCheck(ctx, &provisionerv1.HealthCheckRequest{}))
	note(f.create(t, 1))
	note(f.client.ListServers(ctx, &provisionerv1.ListServersRequest{}))
	note(f.client.ListInbounds(ctx, &provisionerv1.ListInboundsRequest{}))
	seen = append(seen, logs.String())
	if !strings.Contains(logs.String(), "3x-ui") {
		t.Fatal("nothing was logged: the test proves nothing")
	}
	for _, s := range seen {
		for _, tok := range []string{panelTok, candidate} {
			if strings.Contains(s, tok) {
				t.Fatalf("a token leaked: %q", s)
			}
		}
	}
}
