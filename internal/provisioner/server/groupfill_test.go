package server

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	provisionerv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/provisioner/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/provisioner/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/xui"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/xuifake"
)

// logTo makes the server's log readable by the test.
func (f *fixture) logTo() *syncBuffer {
	var logs syncBuffer
	f.srv.SetLogger(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo})))
	return &logs
}

// hold makes a client BOBRES holds on the fixture's server the way an install
// from before the groups left it: a row in the client map and the client on
// the panel, in `group` ("" = none).
func (f *fixture) hold(t *testing.T, i int, email, group string) {
	t.Helper()
	f.holdOn(t, f.server, f.fake, i, email, group)
}

func (f *fixture) holdOn(t *testing.T, sv *store.Server, fake *xuifake.Server, i int, email, group string) {
	t.Helper()
	xid := f.st.SubIDFor(subID(i))
	cm := &store.ClientMap{SubscriptionID: subID(i), ServerID: sv.ID, Email: email, XUISubID: &xid, InboundIDs: []int32{1}}
	if err := f.st.PutClientMap(context.Background(), cm); err != nil {
		t.Fatal(err)
	}
	fake.SeedGrouped(email, xid, group, []int{1})
}

func (f *fixture) pendingBackfill(t *testing.T) []string {
	t.Helper()
	refs, err := f.st.GroupBackfillPending(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, r := range refs {
		names = append(names, r.Name)
	}
	return names
}

func groupOf(fake *xuifake.Server, email string) string {
	g, _ := fake.Group(email)
	return g
}

// eventually waits for cond, for as long as a slow machine needs.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(15 * time.Second); !cond(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

// The first start after the update: the clients BOBRES holds and finds in no
// group are put in the bot group; a client an operator put in a group keeps it;
// what BOBRES does not hold, and a client that is not ours under a held email,
// are not touched. One summary line says what was done, and a second run
// does not go to the panel at all.
func TestBackfillGroupsTagsHeldClients(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	logs := f.logTo()

	f.hold(t, 1, "u1-old", "")
	f.hold(t, 2, "u2-old", "")
	f.hold(t, 3, "u3-op", "vip")
	f.hold(t, 4, "u4-tagged", xui.BotGroup)
	if err := f.st.PutClientMap(ctx, &store.ClientMap{SubscriptionID: subID(5), ServerID: f.server.ID, Email: "u5-gone", InboundIDs: []int32{1}}); err != nil {
		t.Fatal(err) // held, deleted on the panel by hand
	}
	f.fake.Seed("stranger", "sub-x", []int{1}) // on the panel, not held
	// Reservations: a create whose answer was lost before the update (the client
	// is on the panel with our subscription id), and one whose email is another client's.
	for i, e := range []string{"u6-pend", "u7-foreign"} {
		xid := f.st.SubIDFor(subID(6 + i))
		if _, _, err := f.st.ReserveClient(ctx, &store.ClientMap{SubscriptionID: subID(6 + i), ServerID: f.server.ID, Email: e, XUISubID: &xid}); err != nil {
			t.Fatal(err)
		}
	}
	f.fake.Seed("u6-pend", f.st.SubIDFor(subID(6)), []int{1})
	f.fake.Seed("u7-foreign", "someone-elses", []int{1})

	if got := f.pendingBackfill(t); !slices.Equal(got, []string{"fake"}) {
		t.Fatalf("pending before: %v", got)
	}
	f.srv.BackfillGroups(ctx)

	for email, want := range map[string]string{
		"u1-old": xui.BotGroup, "u2-old": xui.BotGroup, "u6-pend": xui.BotGroup, // tagged
		"u3-op": "vip", "u4-tagged": xui.BotGroup, // as they were
		"stranger": "", "u7-foreign": "", // not touched
	} {
		if g := groupOf(f.fake, email); g != want {
			t.Errorf("%s is in %q, want %q", email, g, want)
		}
	}
	if got := f.pendingBackfill(t); len(got) != 0 {
		t.Fatalf("pending after: %v", got)
	}
	line := ""
	for _, l := range strings.Split(logs.String(), "\n") {
		if strings.Contains(l, "backfill finished") {
			line = l
		}
	}
	for _, want := range []string{"server=fake", "group=BOBRES", "managed=7", "tagged=3", "already_in_a_group=2", "missing=2"} {
		if !strings.Contains(line, want) {
			t.Errorf("summary line lacks %q: %s", want, line)
		}
	}
	if strings.Count(logs.String(), "backfill finished") != 1 {
		t.Fatalf("want one summary line:\n%s", logs.String())
	}

	// Done is done: no listing, no tagging on the next runs.
	pages, batches := f.fake.Calls("listPaged"), len(f.fake.GroupBatches())
	f.srv.BackfillGroups(ctx)
	f.srv.BackfillGroups(ctx)
	if f.fake.Calls("listPaged") != pages || len(f.fake.GroupBatches()) != batches {
		t.Fatal("a server that is done was visited again")
	}
}

// A panel that is down leaves the server pending and is tried again; the
// failure is logged once, not on every attempt.
func TestBackfillGroupsRetriesWhenThePanelIsDown(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	logs := f.logTo()
	f.hold(t, 1, "u1-old", "")
	f.fake.FailRoute("listPaged", 3)
	for i := 0; i < 3; i++ {
		f.srv.BackfillGroups(ctx)
		if got := f.pendingBackfill(t); !slices.Equal(got, []string{"fake"}) {
			t.Fatalf("attempt %d: pending %v", i+1, got)
		}
		if g := groupOf(f.fake, "u1-old"); g != "" {
			t.Fatalf("tagged while the panel was down: %q", g)
		}
	}
	if n := strings.Count(logs.String(), "backfill not finished"); n != 1 {
		t.Fatalf("the same failure logged %d times:\n%s", n, logs.String())
	}
	f.srv.BackfillGroups(ctx)
	if g := groupOf(f.fake, "u1-old"); g != xui.BotGroup || len(f.pendingBackfill(t)) != 0 {
		t.Fatalf("after the panel came back: group %q, pending %v", g, f.pendingBackfill(t))
	}
	if strings.Count(logs.String(), "backfill finished") != 1 {
		t.Fatalf("logs:\n%s", logs.String())
	}
}

// A panel that does not have groups (a 3x-ui older than the tested ones) is
// never marked done, and does not fill the log.
func TestBackfillGroupsOnAPanelWithoutGroups(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	logs := f.logTo()
	f.hold(t, 1, "u1-old", "")
	f.fake.SetNoGroups(true)
	for i := 0; i < 3; i++ {
		f.srv.BackfillGroups(ctx)
	}
	if got := f.pendingBackfill(t); !slices.Equal(got, []string{"fake"}) {
		t.Fatalf("pending: %v", got)
	}
	if n := strings.Count(logs.String(), "backfill not finished"); n != 1 || !strings.Contains(logs.String(), "404") {
		t.Fatalf("logs:\n%s", logs.String())
	}
	f.fake.SetNoGroups(false) // the panel is updated
	f.srv.BackfillGroups(ctx)
	if g := groupOf(f.fake, "u1-old"); g != xui.BotGroup {
		t.Fatalf("after the update of the panel: %q", g)
	}
}

// A panel that changes nothing when asked is not "done", and the server is
// tried again later.
func TestBackfillGroupsThatDoNotTakeEffect(t *testing.T) {
	f := newFixture(t)
	logs := f.logTo()
	f.hold(t, 1, "u1-old", "")
	f.fake.IgnoreGroupAdds(true)
	f.srv.BackfillGroups(context.Background())
	if got := f.pendingBackfill(t); !slices.Equal(got, []string{"fake"}) || !strings.Contains(logs.String(), "kept changing") {
		t.Fatalf("pending %v, logs:\n%s", got, logs.String())
	}
}

// A server that holds no client of ours needs no call to the panel: a server
// being added is recorded as done at once. A Marzban server has no groups and
// is never visited.
func TestBackfillGroupsHoldingNothingAndMarzban(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	logs := f.logTo()
	if _, err := f.st.AddServer(ctx, &store.Server{Name: "mz", BaseURL: f.fake.URL, Token: "user:pass", Enabled: true, Kind: store.KindMarzban}); err != nil {
		t.Fatal(err)
	}
	f.srv.BackfillGroups(ctx)
	if f.fake.Calls("listPaged") != 0 || f.fake.Calls("groups") != 0 {
		t.Fatal("the panel was called although BOBRES holds no client there")
	}
	if got := f.pendingBackfill(t); len(got) != 0 {
		t.Fatalf("pending: %v", got)
	}
	var untouched bool
	if err := f.st.DB().QueryRow(ctx, `SELECT group_backfilled_at IS NULL FROM provisioner.xui_servers WHERE kind = 'marzban'`).Scan(&untouched); err != nil || !untouched {
		t.Fatalf("the Marzban server was recorded: %v %v", untouched, err)
	}
	if !strings.Contains(logs.String(), "managed=0") {
		t.Fatalf("logs:\n%s", logs.String())
	}
}

// Past the panel's limit of 200 emails a call, the clients go in batches, and
// a switched-off server is done too.
func TestBackfillGroupsInBatches(t *testing.T) {
	f := newFixture(t)
	f.disable(t, f.server)
	for i := 0; i < 450; i++ {
		f.hold(t, i+1, fmt.Sprintf("u%d-old", i+1), "")
	}
	f.srv.BackfillGroups(context.Background())
	if got := f.fake.GroupBatches(); !slices.Equal(got, []int{200, 200, 50}) {
		t.Fatalf("batches %v", got)
	}
	if n := len(f.fake.GroupMembers(xui.BotGroup)); n != 450 {
		t.Fatalf("%d clients in the group", n)
	}
	if got := f.pendingBackfill(t); len(got) != 0 {
		t.Fatalf("pending: %v", got)
	}
}

// Several servers are each done on their own.
func TestBackfillGroupsPerServer(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	fakeB, b := f.addPanel(t, "b", 100)
	f.hold(t, 1, "u1-a", "")
	f.holdOn(t, b, fakeB, 2, "u2-b", "")
	fakeB.FailRoute("listPaged", 1) // b is down for the first run
	f.srv.BackfillGroups(ctx)
	if groupOf(f.fake, "u1-a") != xui.BotGroup || groupOf(fakeB, "u2-b") != "" {
		t.Fatalf("groups: a %q, b %q", groupOf(f.fake, "u1-a"), groupOf(fakeB, "u2-b"))
	}
	if got := f.pendingBackfill(t); !slices.Equal(got, []string{"b"}) {
		t.Fatalf("pending: %v", got)
	}
	f.srv.BackfillGroups(ctx)
	if groupOf(fakeB, "u2-b") != xui.BotGroup || len(f.pendingBackfill(t)) != 0 {
		t.Fatal("b was not finished")
	}
	// a was not asked again.
	if f.fake.Calls("listPaged") != 2 { // the listing, and the one that checks it
		t.Fatalf("a was listed %d times", f.fake.Calls("listPaged"))
	}
}

// The worker runs on its three triggers: at start, when a server is added, and
// when a server that did not answer answers again. The tick is an hour, so
// only those wake it.
func TestGroupBackfillRunsOnItsTriggers(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	f.hold(t, 1, "u1-old", "")

	finished := make(chan error, 1)
	go func() { finished <- f.srv.RunGroupBackfill(ctx, time.Hour) }()
	defer func() {
		cancel()
		if err := <-finished; err != nil {
			t.Error(err)
		}
	}()

	// Start.
	eventually(t, "the backfill at start", func() bool {
		return groupOf(f.fake, "u1-old") == xui.BotGroup && len(f.pendingBackfill(t)) == 0
	})

	// A server is added (through the service, as the dashboard does): it is
	// recorded at once.
	fakeB := xuifake.NewServer(panelTok)
	t.Cleanup(fakeB.Close)
	if _, err := f.srv.AddServer(ctx, &provisionerv1.AddServerRequest{Name: "b", BaseUrl: fakeB.URL, ApiToken: panelTok, AllowPrivate: true}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the backfill of a new server", func() bool { return len(f.pendingBackfill(t)) == 0 })

	// A server whose panel does not answer, then does: found by the health check.
	fakeC := xuifake.NewServer(panelTok)
	t.Cleanup(fakeC.Close)
	c, err := f.st.AddServer(ctx, &store.Server{Name: "c", BaseURL: fakeC.URL, Token: panelTok, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	f.holdOn(t, c, fakeC, 2, "u2-old", "")
	fakeC.FailRoute("listPaged", 1000)
	f.srv.kickGroupBackfill()
	eventually(t, "the failed attempt on c", func() bool { return fakeC.Calls("listPaged") > 0 })
	if got := f.pendingBackfill(t); !slices.Equal(got, []string{"c"}) {
		t.Fatalf("pending: %v", got)
	}
	if err := f.st.RecordHealth(ctx, c.ID, c.UpdatedAt, false, "", "down"); err != nil { // the health loop saw it fail
		t.Fatal(err)
	}
	fakeC.FailRoute("listPaged", 0)
	f.srv.CheckHealth(ctx) // answers again: healthy again wakes the worker
	eventually(t, "the backfill of the server that came back", func() bool {
		return groupOf(fakeC, "u2-old") == xui.BotGroup && len(f.pendingBackfill(t)) == 0
	})
}

// A client made before the update, whose create was retried after it, is put in
// the bot group; one an operator put elsewhere is not moved. A panel refuses
// the create of a client it has, or (as real 3x-ui panels do) takes it as a
// success that changes nothing: both end the same.
func TestRetriedCreateOfAnOlderClientIsInTheBotGroup(t *testing.T) {
	for name, reuse := range map[string]bool{"panel refuses the duplicate": false, "panel reuses the client": true} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()
			f.fake.SetAddReusesClient(reuse)
			for _, c := range []struct {
				i           int
				email       string
				group, want string
			}{{8, "u8-lost", "", xui.BotGroup}, {9, "u9-op", "vip", "vip"}} {
				// What the earlier attempt left: the reservation, and the client on the panel.
				xid := f.st.SubIDFor(subID(c.i))
				if _, _, err := f.st.ReserveClient(ctx, &store.ClientMap{SubscriptionID: subID(c.i), ServerID: f.server.ID, Email: c.email, XUISubID: &xid}); err != nil {
					t.Fatal(err)
				}
				f.fake.SeedGrouped(c.email, xid, c.group, []int{1})
				if _, err := f.client.CreateClient(ctx, &provisionerv1.CreateClientRequest{SubscriptionId: subID(c.i), Email: c.email, DurationDays: 30}); err != nil {
					t.Fatalf("retrying %s: %v", c.email, err)
				}
				if g := groupOf(f.fake, c.email); g != c.want {
					t.Errorf("%s is in %q after the retry, want %q", c.email, g, c.want)
				}
			}
		})
	}
}

// A first attempt that works needs no extra call to the panel: the client is
// made in the group, and nothing looks at it again.
func TestFirstCreateDoesNotLookAgain(t *testing.T) {
	f := newFixture(t)
	if _, err := f.create(t, 1); err != nil {
		t.Fatal(err)
	}
	if g := groupOf(f.fake, "u1-new"); g != xui.BotGroup {
		t.Fatalf("group %q", g)
	}
	if f.fake.Calls("groupBulkAdd") != 0 {
		t.Fatal("a new client was tagged after its creation instead of in it")
	}
}

// A new client is in the bot group, and the calls core makes on it afterwards
// keep it there (a row updated without its group would lose it).
func TestProvisionedClientStaysInTheBotGroup(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	cl, err := f.client.CreateClient(ctx, &provisionerv1.CreateClientRequest{SubscriptionId: sub1, Email: "u42-grp", DurationDays: 30, TrafficBytes: 10 << 30})
	if err != nil {
		t.Fatal(err)
	}
	if g := groupOf(f.fake, "u42-grp"); g != xui.BotGroup {
		t.Fatalf("new client in %q", g)
	}
	check := func(when string) {
		t.Helper()
		if g := groupOf(f.fake, "u42-grp"); g != xui.BotGroup {
			t.Fatalf("%s: group %q", when, g)
		}
	}
	if _, err := f.client.RenewClient(ctx, &provisionerv1.RenewClientRequest{SubscriptionId: sub1, AddDays: 5}); err != nil {
		t.Fatal(err)
	}
	check("after a renewal")
	if _, err := f.client.SetClientLimits(ctx, &provisionerv1.SetClientLimitsRequest{SubscriptionId: sub1, ExpiresAt: cl.GetExpiresAt() + 86400, TrafficTotalBytes: 20 << 30}); err != nil {
		t.Fatal(err)
	}
	check("after new limits")
	for _, on := range []bool{false, true} {
		if _, err := f.client.SetClientEnabled(ctx, &provisionerv1.SetClientEnabledRequest{SubscriptionId: sub1, Enabled: on}); err != nil {
			t.Fatal(err)
		}
		check(fmt.Sprintf("after switching it %v", on))
	}
	if _, err := f.client.ResetTraffic(ctx, &provisionerv1.ResetTrafficRequest{SubscriptionId: sub1}); err != nil {
		t.Fatal(err)
	}
	check("after a traffic reset")
	if lost := f.fake.LostFields(); len(lost) != 0 {
		t.Fatalf("lost: %v", lost)
	}
}
