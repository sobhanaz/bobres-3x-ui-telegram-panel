package store

import (
	"context"
	"slices"
	"testing"
	"time"
)

const (
	srvA = "00000000-0000-7000-8000-0000000000a1"
	srvB = "00000000-0000-7000-8000-0000000000b2"
)

// A plan's servers: read with the plan (empty = any), replaced when a save
// carries a list, kept when it carries none.
func TestPlanServers(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	p := &Plan{NameI18n: map[string]string{"en": "S"}, Kind: "both", Price: 1, Currency: "IRT", Enabled: true}
	if err := s.UpsertPlan(ctx, s.Conn(), p); err != nil {
		t.Fatal(err)
	}
	ids := func() []string {
		t.Helper()
		got, err := s.GetPlan(ctx, s.Conn(), p.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got.ServerIDs
	}
	if got := ids(); got == nil || len(got) != 0 {
		t.Fatalf("a new plan: %#v", got)
	}
	p.ServerIDs = []string{srvB, srvA}
	if err := s.UpsertPlan(ctx, s.Conn(), p); err != nil {
		t.Fatal(err)
	}
	if got := ids(); !slices.Equal(got, []string{srvA, srvB}) {
		t.Fatalf("two servers: %v", got)
	}
	list, err := s.ListPlans(ctx, s.Conn(), true)
	if err != nil || len(list) != 1 || !slices.Equal(list[0].ServerIDs, []string{srvA, srvB}) {
		t.Fatalf("list: %v %+v", err, list)
	}
	// nil keeps them (the bot's plan edits carry no servers).
	p.ServerIDs, p.Price = nil, 2
	if err := s.UpsertPlan(ctx, s.Conn(), p); err != nil {
		t.Fatal(err)
	}
	if got := ids(); !slices.Equal(got, []string{srvA, srvB}) {
		t.Fatalf("kept: %v", got)
	}
	p.ServerIDs = []string{srvB}
	if err := s.UpsertPlan(ctx, s.Conn(), p); err != nil {
		t.Fatal(err)
	}
	if got := ids(); !slices.Equal(got, []string{srvB}) {
		t.Fatalf("one taken out: %v", got)
	}
	p.ServerIDs = []string{}
	if err := s.UpsertPlan(ctx, s.Conn(), p); err != nil {
		t.Fatal(err)
	}
	if got := ids(); len(got) != 0 {
		t.Fatalf("back to any server: %v", got)
	}
	p.ServerIDs = []string{"default"}
	if err := s.UpsertPlan(ctx, s.Conn(), p); err == nil {
		t.Fatal("a server id that is not a uuid was stored")
	}
}

// Stale links: only a server's services on the panel are marked; a link is
// stored and the mark cleared unless the service was marked again after the
// fetch began.
func TestStaleSubLinks(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	u, err := s.UpsertUser(ctx, s.Conn(), 4101, "", "fa", "")
	if err != nil {
		t.Fatal(err)
	}
	p := &Plan{NameI18n: map[string]string{"en": "S"}, Kind: "both", Price: 1, Currency: "IRT", Enabled: true}
	if err := s.UpsertPlan(ctx, s.Conn(), p); err != nil {
		t.Fatal(err)
	}
	mk := func(server, status, link string) string {
		t.Helper()
		o, _, err := s.CreateOrder(ctx, s.Conn(), &Order{UserID: u.ID, PlanID: p.ID, Type: "new", Status: "active",
			Amount: 1, Currency: "IRT", IdempotencyKey: "k-" + server + status + link})
		if err != nil {
			t.Fatal(err)
		}
		sub, err := s.CreateSubscription(ctx, s.Conn(), &Subscription{UserID: u.ID, OrderID: o.ID, ClientEmail: "e-" + o.ID})
		if err != nil {
			t.Fatal(err)
		}
		if server != "" {
			if err := s.ActivateSubscription(ctx, s.Conn(), sub.ID, server, "x", link, nil, nil); err != nil {
				t.Fatal(err)
			}
		}
		if status != "active" {
			if err := s.SetSubscriptionStatus(ctx, s.Conn(), sub.ID, status); err != nil {
				t.Fatal(err)
			}
		}
		return sub.ID
	}
	onA := mk(srvA, "active", "https://old/a")
	offA := mk(srvA, "disabled", "https://old/b")
	goneA := mk(srvA, "deleted", "https://old/c")
	onB := mk(srvB, "active", "https://old/d")
	pending := mk("", "pending", "")

	n, err := s.MarkSubLinksStale(ctx, s.Conn(), srvA)
	if err != nil || n != 2 {
		t.Fatalf("marked %d: %v", n, err)
	}
	stale, err := s.StaleSubLinks(ctx, s.Conn(), 10)
	if err != nil || len(stale) != 2 {
		t.Fatalf("stale: %v %+v", err, stale)
	}
	marked := []string{stale[0].ID, stale[1].ID}
	slices.Sort(marked)
	want := []string{onA, offA}
	slices.Sort(want)
	if !slices.Equal(marked, want) {
		t.Fatalf("marked %v, want %v (not %s, %s, %s)", marked, want, goneA, onB, pending)
	}

	// Stored and cleared.
	first := stale[0]
	if ok, err := s.RefreshSubLink(ctx, s.Conn(), first.ID, "https://new/1", first.StaleAt); err != nil || !ok {
		t.Fatalf("refresh: %v %v", ok, err)
	}
	if sub, _ := s.GetSubscription(ctx, s.Conn(), first.ID); sub.SubLink != "https://new/1" {
		t.Fatalf("link: %q", sub.SubLink)
	}
	// Marked again while the second was being fetched: the old fetch is not
	// stored and the mark stays.
	second := stale[1]
	time.Sleep(2 * time.Millisecond)
	if _, err := s.MarkSubLinksStale(ctx, s.Conn(), srvA); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.RefreshSubLink(ctx, s.Conn(), second.ID, "https://stale/2", second.StaleAt); err != nil || ok {
		t.Fatalf("a fetch from before the second change was stored: %v %v", ok, err)
	}
	if sub, _ := s.GetSubscription(ctx, s.Conn(), second.ID); sub.SubLink == "https://stale/2" {
		t.Fatal("stale link stored")
	}
	// An empty link keeps the stored one and clears the mark.
	again, _ := s.StaleSubLinks(ctx, s.Conn(), 10)
	for _, l := range again {
		if ok, err := s.RefreshSubLink(ctx, s.Conn(), l.ID, "", l.StaleAt); err != nil || !ok {
			t.Fatalf("keep: %v %v", ok, err)
		}
	}
	if left, _ := s.StaleSubLinks(ctx, s.Conn(), 10); len(left) != 0 {
		t.Fatalf("left: %+v", left)
	}
	orig := map[string]string{onA: "https://old/a", offA: "https://old/b"}
	if sub, _ := s.GetSubscription(ctx, s.Conn(), second.ID); sub.SubLink != orig[second.ID] {
		t.Fatalf("kept link: %q, want %q", sub.SubLink, orig[second.ID])
	}
}

// A link put back waits until its next try, counting a failed fetch only
// when told; a new mark is due at once and starts the count over, and a
// mark set after the fetch began is not put back.
func TestPostponeSubLink(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	u, err := s.UpsertUser(ctx, s.Conn(), 4111, "", "fa", "")
	if err != nil {
		t.Fatal(err)
	}
	p := &Plan{NameI18n: map[string]string{"en": "S"}, Kind: "both", Price: 1, Currency: "IRT", Enabled: true}
	if err := s.UpsertPlan(ctx, s.Conn(), p); err != nil {
		t.Fatal(err)
	}
	o, _, err := s.CreateOrder(ctx, s.Conn(), &Order{UserID: u.ID, PlanID: p.ID, Type: "new", Status: "active",
		Amount: 1, Currency: "IRT", IdempotencyKey: "k-postpone"})
	if err != nil {
		t.Fatal(err)
	}
	sub, err := s.CreateSubscription(ctx, s.Conn(), &Subscription{UserID: u.ID, OrderID: o.ID, ClientEmail: "e-postpone"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ActivateSubscription(ctx, s.Conn(), sub.ID, srvA, "x", "https://old/p", nil, nil); err != nil {
		t.Fatal(err)
	}
	due := func() []StaleSubLink {
		t.Helper()
		got, err := s.StaleSubLinks(ctx, s.Conn(), 10)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if _, err := s.MarkSubLinksStale(ctx, s.Conn(), srvA); err != nil {
		t.Fatal(err)
	}
	first := due()
	if len(first) != 1 || first[0].ServerID != srvA || first[0].Attempts != 0 {
		t.Fatalf("marked: %+v", first)
	}

	if ok, err := s.PostponeSubLink(ctx, s.Conn(), sub.ID, first[0].StaleAt, time.Hour, true); err != nil || !ok {
		t.Fatalf("postpone: %v %v", ok, err)
	}
	if got := due(); len(got) != 0 {
		t.Fatalf("due while it waits: %+v", got)
	}
	var tries int
	var wait float64
	if err := s.DB().QueryRow(ctx, `SELECT sub_link_attempts, EXTRACT(EPOCH FROM sub_link_stale_at - now())::float8
		FROM core.subscriptions WHERE id = $1`, sub.ID).Scan(&tries, &wait); err != nil {
		t.Fatal(err)
	}
	if tries != 1 || wait < 3590 || wait > 3610 {
		t.Fatalf("after a failed fetch: %d attempts, due in %v s", tries, wait)
	}
	// Its wait is over: due again with the attempt counted; an uncounted
	// wait leaves the count.
	if _, err := s.DB().Exec(ctx, `UPDATE core.subscriptions SET sub_link_stale_at = now() - interval '1 second' WHERE id = $1`, sub.ID); err != nil {
		t.Fatal(err)
	}
	again := due()
	if len(again) != 1 || again[0].Attempts != 1 {
		t.Fatalf("due again: %+v", again)
	}
	if ok, err := s.PostponeSubLink(ctx, s.Conn(), sub.ID, again[0].StaleAt, time.Minute, false); err != nil || !ok {
		t.Fatalf("postpone uncounted: %v %v", ok, err)
	}
	// Marked again (a new address): due now, count from zero; the fetch
	// that began before it is not put back.
	if _, err := s.MarkSubLinksStale(ctx, s.Conn(), srvA); err != nil {
		t.Fatal(err)
	}
	marked := due()
	if len(marked) != 1 || marked[0].Attempts != 0 {
		t.Fatalf("marked again: %+v", marked)
	}
	if ok, err := s.PostponeSubLink(ctx, s.Conn(), sub.ID, again[0].StaleAt, time.Hour, true); err != nil || ok {
		t.Fatalf("an old fetch put the new mark back: %v %v", ok, err)
	}
	if got := due(); len(got) != 1 || got[0].Attempts != 0 {
		t.Fatalf("after the old fetch: %+v", got)
	}
	// A stored link clears the mark and the count.
	if ok, err := s.PostponeSubLink(ctx, s.Conn(), sub.ID, marked[0].StaleAt, 0, true); err != nil || !ok {
		t.Fatal(err)
	}
	last := due()
	if len(last) != 1 || last[0].Attempts != 1 {
		t.Fatalf("no wait: %+v", last)
	}
	if ok, err := s.RefreshSubLink(ctx, s.Conn(), sub.ID, "https://new/p", last[0].StaleAt); err != nil || !ok {
		t.Fatalf("refresh: %v %v", ok, err)
	}
	if err := s.DB().QueryRow(ctx, `SELECT sub_link_attempts FROM core.subscriptions WHERE id = $1`, sub.ID).Scan(&tries); err != nil || tries != 0 {
		t.Fatalf("attempts after a stored link: %d %v", tries, err)
	}
}
