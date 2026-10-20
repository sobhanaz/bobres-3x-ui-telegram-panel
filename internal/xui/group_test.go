package xui_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/xui"
)

func TestBotGroupName(t *testing.T) {
	// The owner-facing name: it is on every panel BOBRES has served, and in the docs.
	if xui.BotGroup != "bobres_bot" {
		t.Fatalf("BotGroup = %q", xui.BotGroup)
	}
}

func TestAddClientStoresTheGroup(t *testing.T) {
	ctx := context.Background()
	c, s := setup(t)
	sp := spec("g1")
	sp.Group = xui.BotGroup
	if err := c.AddClient(ctx, sp, []int{1}); err != nil {
		t.Fatal(err)
	}
	if g, _ := s.Group("g1"); g != xui.BotGroup {
		t.Fatalf("group on the panel: %q", g)
	}
	d, err := c.GetClient(ctx, "g1")
	if err != nil || d.Client.Group != xui.BotGroup {
		t.Fatalf("get: %v %+v", err, d)
	}
	// A client given no group has none: the label is only ever what the caller names.
	if err := c.AddClient(ctx, spec("g2"), []int{1}); err != nil {
		t.Fatal(err)
	}
	if g, _ := s.Group("g2"); g != "" {
		t.Fatalf("a client made without a group is in %q", g)
	}
}

func TestAddClientRefusesABadGroup(t *testing.T) {
	ctx := context.Background()
	c, s := setup(t)
	for _, g := range []string{" lead", "trail ", "two\nlines", "bell\x07", strings.Repeat("g", 65)} {
		sp := spec("bad")
		sp.Group = g
		if err := c.AddClient(ctx, sp, []int{1}); !errors.Is(err, xui.ErrInvalidGroup) {
			t.Errorf("group %q: %v", g, err)
		}
	}
	if s.Calls("add") != 0 {
		t.Fatalf("a bad group reached the panel (%d calls)", s.Calls("add"))
	}
	sp := spec("long")
	sp.Group = strings.Repeat("g", 64) // the longest
	if err := c.AddClient(ctx, sp, []int{1}); err != nil {
		t.Fatalf("a 64-character group: %v", err)
	}
}

// Every call BOBRES makes on a client afterwards leaves its group: the two that
// send the whole row back (the panel clears the group of a row without one),
// the panel's own renewal, and the traffic reset.
func TestRewritesKeepTheGroup(t *testing.T) {
	ctx := context.Background()
	c, s := setup(t)
	sp := spec("k1")
	sp.Group = xui.BotGroup
	if err := c.AddClient(ctx, sp, []int{1, 2}); err != nil {
		t.Fatal(err)
	}
	exp := time.Now().Add(72 * time.Hour).UnixMilli()
	for _, step := range []struct {
		name string
		do   func() error
	}{
		{"set limits", func() error { return c.SetLimits(ctx, "k1", exp, 2*gib) }},
		{"switch off", func() error { return c.SetEnabled(ctx, "k1", false) }},
		{"switch on", func() error { return c.SetEnabled(ctx, "k1", true) }},
		{"renew", func() error {
			_, err := c.BulkAdjust(ctx, xui.AdjustRequest{Emails: []string{"k1"}, AddDays: 1, AddBytes: gib})
			return err
		}},
		{"reset traffic", func() error { return c.ResetTraffic(ctx, "k1") }},
	} {
		if err := step.do(); err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
		if g, _ := s.Group("k1"); g != xui.BotGroup {
			t.Fatalf("after %s the group is %q", step.name, g)
		}
	}
	if lost := s.LostFields(); len(lost) != 0 {
		t.Fatalf("an update lost the client's identity or group: %v", lost)
	}
	// A client an operator put in another group keeps THAT group through the same calls.
	s.SeedGrouped("k2", "sub-k2", "vip", []int{1})
	if err := c.SetLimits(ctx, "k2", exp, gib); err != nil {
		t.Fatal(err)
	}
	if err := c.SetEnabled(ctx, "k2", false); err != nil {
		t.Fatal(err)
	}
	if g, _ := s.Group("k2"); g != "vip" {
		t.Fatalf("an operator's group became %q", g)
	}
}

func TestAddToGroupBatchesAndCounts(t *testing.T) {
	ctx := context.Background()
	c, s := setup(t)
	const n = 450
	emails := make([]string, n)
	for i := range emails {
		emails[i] = fmt.Sprintf("b%03d", i)
		s.Seed(emails[i], "sub-"+emails[i], []int{1})
	}
	got, err := c.AddToGroup(ctx, emails, xui.BotGroup)
	if err != nil || got != n {
		t.Fatalf("AddToGroup: %d %v", got, err)
	}
	if b := s.GroupBatches(); !slices.Equal(b, []int{200, 200, 50}) {
		t.Fatalf("calls carried %v emails; the panel is sent at most %d at a time", b, xui.MaxGroupBatch)
	}
	if m := s.GroupMembers(xui.BotGroup); len(m) != n {
		t.Fatalf("%d members", len(m))
	}
	// Again: nothing changes, and a client the panel does not know is not counted.
	if got, err := c.AddToGroup(ctx, append([]string{"ghost"}, emails[:5]...), xui.BotGroup); err != nil || got != 0 {
		t.Fatalf("repeat: %d %v", got, err)
	}
	// Nobody: no call at all.
	calls := s.Calls("groupBulkAdd")
	if got, err := c.AddToGroup(ctx, nil, xui.BotGroup); err != nil || got != 0 || s.Calls("groupBulkAdd") != calls {
		t.Fatalf("empty list: %d %v, %d calls more", got, err, s.Calls("groupBulkAdd")-calls)
	}
}

// bulkAdd is not "add to the group if it has none": it moves a client out of
// the group it is in. The caller decides whom to send.
func TestAddToGroupMovesAClientOutOfItsGroup(t *testing.T) {
	c, s := setup(t)
	s.SeedGrouped("o1", "sub-o1", "vip", []int{1})
	got, err := c.AddToGroup(context.Background(), []string{"o1"}, xui.BotGroup)
	if err != nil || got != 1 {
		t.Fatalf("AddToGroup: %d %v", got, err)
	}
	if g, _ := s.Group("o1"); g != xui.BotGroup {
		t.Fatalf("group: %q", g)
	}
}

func TestAddToGroupStopsAtAFailedBatchAndResumes(t *testing.T) {
	ctx := context.Background()
	c, s := setup(t)
	emails := make([]string, 450)
	for i := range emails {
		emails[i] = fmt.Sprintf("p%03d", i)
		s.Seed(emails[i], "sub-"+emails[i], []int{1})
	}
	s.FailRouteAfter("groupBulkAdd", 1, 1) // the first batch goes through, the second fails
	got, err := c.AddToGroup(ctx, emails, xui.BotGroup)
	if err == nil || got != 200 {
		t.Fatalf("a failing second batch: %d %v", got, err)
	}
	if m := s.GroupMembers(xui.BotGroup); len(m) != 200 {
		t.Fatalf("%d members after the failure", len(m))
	}
	// The retry counts only what it changes.
	if got, err := c.AddToGroup(ctx, emails, xui.BotGroup); err != nil || got != 250 {
		t.Fatalf("resume: %d %v", got, err)
	}
}

func TestAddToGroupRefusesBadInputBeforeTheCall(t *testing.T) {
	ctx := context.Background()
	c, s := setup(t)
	s.Seed("ok1", "sub-ok1", []int{1})
	if _, err := c.AddToGroup(ctx, []string{"ok1", "bad email!"}, xui.BotGroup); !errors.Is(err, xui.ErrInvalidIdentifier) {
		t.Fatalf("bad email: %v", err)
	}
	for _, g := range []string{"", "  ", " x"} {
		if _, err := c.AddToGroup(ctx, []string{"ok1"}, g); !errors.Is(err, xui.ErrInvalidGroup) {
			t.Fatalf("group %q: %v", g, err)
		}
	}
	if s.Calls("groupBulkAdd") != 0 {
		t.Fatal("a bad request reached the panel")
	}
}

// clients/update sends the row it read, and the panel writes the group of what
// it receives. A tagging that lands between that read and that write would be
// undone by it, so AddToGroup waits for an update running on the same client.
func TestAddToGroupDoesNotInterleaveWithAnUpdate(t *testing.T) {
	ctx := context.Background()
	c, s := setup(t)
	if err := c.AddClient(ctx, spec("w1"), []int{1}); err != nil { // made before the group existed
		t.Fatal(err)
	}
	s.SetLatency(60 * time.Millisecond)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := c.SetLimits(ctx, "w1", time.Now().Add(time.Hour).UnixMilli(), gib); err != nil {
			t.Error(err)
		}
	}()
	time.Sleep(20 * time.Millisecond) // the update has read the row and not written it yet
	if _, err := c.AddToGroup(ctx, []string{"w1"}, xui.BotGroup); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if g, _ := s.Group("w1"); g != xui.BotGroup {
		t.Fatalf("the update undid the tagging: group %q", g)
	}
	if lost := s.LostFields(); len(lost) != 0 {
		t.Fatalf("lost: %v", lost)
	}
}

func TestListClientsReadsEveryPage(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct{ clients, pages int }{{0, 1}, {1, 1}, {200, 1}, {201, 2}, {400, 2}, {450, 3}} {
		t.Run(fmt.Sprintf("%d clients", tc.clients), func(t *testing.T) {
			c, s := setup(t)
			for i := 0; i < tc.clients; i++ {
				e := fmt.Sprintf("c%04d", i)
				if i%3 == 0 {
					s.SeedGrouped(e, "sub-"+e, "g", []int{1, 2})
				} else {
					s.Seed(e, "sub-"+e, []int{1})
				}
			}
			rows, err := c.ListClients(ctx)
			if err != nil || len(rows) != tc.clients {
				t.Fatalf("ListClients: %d rows, %v", len(rows), err)
			}
			for i, r := range rows {
				e := fmt.Sprintf("c%04d", i)
				wantGroup, wantInbounds := "", 1
				if i%3 == 0 {
					wantGroup, wantInbounds = "g", 2
				}
				if r.Email != e || r.SubID != "sub-"+e || r.Group != wantGroup || len(r.InboundIDs) != wantInbounds || !r.Enable {
					t.Fatalf("row %d: %+v", i, r)
				}
			}
			if got := s.Calls("listPaged"); got != tc.pages {
				t.Fatalf("%d pages read, want %d", got, tc.pages)
			}
		})
	}
}

// A panel that keeps answering with a page and a total it never reaches does
// not hold the caller forever.
func TestListClientsStopsOnARunawayPanel(t *testing.T) {
	var mu sync.Mutex
	pages := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		pages++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"obj":{"items":[{"email":"same","subId":"s","enable":true}],"total":1000000}}`))
	}))
	defer srv.Close()
	defer xui.SetMaxListPagesForTest(xui.SetMaxListPagesForTest(4))
	if _, err := newClient(t, srv.URL, token).ListClients(context.Background()); err == nil || !strings.Contains(err.Error(), "does not end") {
		t.Fatalf("a listing that never ends: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if pages != 4 {
		t.Fatalf("%d pages read", pages)
	}
}

func TestGroupsAndGroupEmails(t *testing.T) {
	ctx := context.Background()
	c, s := setup(t)
	s.SeedGrouped("m2", "sub-m2", xui.BotGroup, []int{1})
	s.SeedGrouped("m1", "sub-m1", xui.BotGroup, []int{1})
	s.SeedGrouped("v1", "sub-v1", "VIP", []int{1})
	s.Seed("n1", "sub-n1", []int{1})
	groups, err := c.Groups(ctx)
	if err != nil || !slices.Equal(groups, []xui.GroupInfo{{Name: xui.BotGroup, ClientCount: 2}, {Name: "VIP", ClientCount: 1}}) {
		t.Fatalf("Groups: %+v %v", groups, err)
	}
	if e, err := c.GroupEmails(ctx, xui.BotGroup); err != nil || !slices.Equal(e, []string{"m1", "m2"}) {
		t.Fatalf("GroupEmails: %v %v", e, err)
	}
	if e, err := c.GroupEmails(ctx, "nobody"); err != nil || e == nil || len(e) != 0 {
		t.Fatalf("an empty group: %#v %v", e, err)
	}
	if _, err := c.GroupEmails(ctx, " x"); !errors.Is(err, xui.ErrInvalidGroup) {
		t.Fatalf("a bad group name: %v", err)
	}
}

func TestAddToGroupIfNone(t *testing.T) {
	ctx := context.Background()
	c, s := setup(t)
	s.Seed("n1", "sub-n1", []int{1})
	s.SeedGrouped("n2", "sub-n2", "vip", []int{1})
	if did, err := c.AddToGroupIfNone(ctx, "n1", xui.BotGroup); err != nil || !did {
		t.Fatalf("a client with no group: %v %v", did, err)
	}
	if did, err := c.AddToGroupIfNone(ctx, "n1", xui.BotGroup); err != nil || did {
		t.Fatalf("again: %v %v", did, err)
	}
	if did, err := c.AddToGroupIfNone(ctx, "n2", xui.BotGroup); err != nil || did {
		t.Fatalf("a client in an operator's group: %v %v", did, err)
	}
	if g, _ := s.Group("n2"); g != "vip" {
		t.Fatalf("an operator's group became %q", g)
	}
	if _, err := c.AddToGroupIfNone(ctx, "ghost", xui.BotGroup); err == nil {
		t.Fatal("a client the panel does not have")
	}
	if _, err := c.AddToGroupIfNone(ctx, "bad email!", xui.BotGroup); !errors.Is(err, xui.ErrInvalidIdentifier) {
		t.Fatalf("bad email: %v", err)
	}
}

// A panel without client groups answers 404 on their routes: the error says so
// and nothing else is assumed.
func TestGroupCallsOnAPanelWithoutGroups(t *testing.T) {
	ctx := context.Background()
	c, s := setup(t)
	s.Seed("x1", "sub-x1", []int{1})
	s.SetNoGroups(true)
	var ae *xui.APIError
	if _, err := c.ListClients(ctx); !errors.As(err, &ae) || ae.Status != http.StatusNotFound {
		t.Fatalf("ListClients: %v", err)
	}
	if _, err := c.AddToGroup(ctx, []string{"x1"}, xui.BotGroup); !errors.As(err, &ae) || ae.Status != http.StatusNotFound {
		t.Fatalf("AddToGroup: %v", err)
	}
	if _, err := c.Groups(ctx); !errors.As(err, &ae) || ae.Status != http.StatusNotFound {
		t.Fatalf("Groups: %v", err)
	}
	// What a client does without them still works, the group label is ignored.
	if err := c.SetEnabled(ctx, "x1", false); err != nil {
		t.Fatal(err)
	}
}
