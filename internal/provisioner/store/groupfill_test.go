package store

import (
	"context"
	"slices"
	"testing"
	"time"
)

// The store side of the group backfill: which servers are still to do, the
// clients a server holds, and recording a server as done.
func TestGroupBackfillBookkeeping(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a, err := s.AddServer(ctx, &Server{Name: "a", BaseURL: "https://a.test", Token: "tok-a", Enabled: true}) //gitleaks:allow test fixture
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.AddServer(ctx, &Server{Name: "b", BaseURL: "https://b.test", Token: "tok-b", Enabled: false}) //gitleaks:allow test fixture
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddServer(ctx, &Server{Name: "mz", BaseURL: "https://m.test", Token: "u:p", Enabled: true, Kind: KindMarzban}); err != nil {
		t.Fatal(err)
	}
	names := func() []string {
		t.Helper()
		refs, err := s.GroupBackfillPending(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, r := range refs {
			out = append(out, r.Name)
		}
		return out
	}
	// A new server is to do (switched off or on), a Marzban one never.
	if got := names(); !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("pending: %v", got)
	}

	// Its clients: reservations too, with the derived subscription id or none.
	xid := "sub-1"
	for i, cm := range []*ClientMap{
		{SubscriptionID: "00000000-0000-7000-8000-000000000601", ServerID: a.ID, Email: "u1-b", XUISubID: &xid, InboundIDs: []int32{1}},
		{SubscriptionID: "00000000-0000-7000-8000-000000000602", ServerID: a.ID, Email: "u2-a", InboundIDs: []int32{1}},
		{SubscriptionID: "00000000-0000-7000-8000-000000000603", ServerID: b.ID, Email: "u3-other"},
	} {
		if i < 2 {
			if err := s.PutClientMap(ctx, cm); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if _, _, err := s.ReserveClient(ctx, cm); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.ServerClients(ctx, a.ID)
	if err != nil || !slices.Equal(got, []MappedClient{{Email: "u1-b", XUISubID: "sub-1"}, {Email: "u2-a"}}) {
		t.Fatalf("clients of a: %+v %v", got, err)
	}
	if got, err := s.ServerClients(ctx, b.ID); err != nil || !slices.Equal(got, []MappedClient{{Email: "u3-other"}}) {
		t.Fatalf("clients of b (a reservation): %+v %v", got, err)
	}
	if got, err := s.ServerClients(ctx, "00000000-0000-7000-8000-0000000006ff"); err != nil || len(got) != 0 {
		t.Fatalf("clients of no server: %+v %v", got, err)
	}

	// Recording a server moves neither updated_at (it keys the cached panel
	// client) nor anyone else, and recording twice keeps the first time.
	before, err := s.GetServer(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkGroupBackfilled(ctx, a.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	var first, second time.Time
	if err := s.DB().QueryRow(ctx, `SELECT group_backfilled_at FROM provisioner.xui_servers WHERE id = $1`, a.ID).Scan(&first); err != nil || first.IsZero() {
		t.Fatalf("not recorded: %v %v", first, err)
	}
	if err := s.MarkGroupBackfilled(ctx, a.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.DB().QueryRow(ctx, `SELECT group_backfilled_at FROM provisioner.xui_servers WHERE id = $1`, a.ID).Scan(&second); err != nil || !first.Equal(second) {
		t.Fatalf("recording again moved the time: %v -> %v (%v)", first, second, err)
	}
	after, err := s.GetServer(ctx, a.ID)
	if err != nil || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("updated_at moved: %v -> %v (%v)", before.UpdatedAt, after.UpdatedAt, err)
	}
	if got := names(); !slices.Equal(got, []string{"b"}) {
		t.Fatalf("pending after a is done: %v", got)
	}
	if err := s.MarkGroupBackfilled(ctx, "00000000-0000-7000-8000-0000000006ff", time.Now()); err != nil {
		t.Fatalf("a server that is gone: %v", err)
	}
}

// A pass that began before SetClientGroups asked for another one must not record the server as done: the change would be lost.
func TestBackfillMarkIsNotLostToAChangeMadeDuringAPass(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a, err := s.AddServer(ctx, &Server{Name: "race", BaseURL: "https://race.test", Token: "tok-race", Enabled: true}) //gitleaks:allow test fixture
	if err != nil {
		t.Fatal(err)
	}
	xid := s.SubIDFor("00000000-0000-7000-8000-0000000007a1")
	if err := s.PutClientMap(ctx, &ClientMap{SubscriptionID: "00000000-0000-7000-8000-0000000007a1", ServerID: a.ID, Email: "u1-race", XUISubID: &xid, InboundIDs: []int32{1}}); err != nil {
		t.Fatal(err)
	}
	pass := time.Now() // a pass begins
	if n, err := s.SetClientGroups(ctx, []GroupAssignment{{SubscriptionID: "00000000-0000-7000-8000-0000000007a1", Group: "BOBRES-X"}}); err != nil || n != 1 {
		t.Fatalf("SetClientGroups: %d %v", n, err)
	}
	if err := s.MarkGroupBackfilled(ctx, a.ID, pass); err != nil { // ...and ends after the change
		t.Fatal(err)
	}
	if refs, _ := s.GroupBackfillPending(ctx); len(refs) != 1 {
		t.Fatalf("the change was lost: %d servers pending", len(refs))
	}
	if err := s.MarkGroupBackfilled(ctx, a.ID, time.Now()); err != nil { // a pass that began after it
		t.Fatal(err)
	}
	if refs, _ := s.GroupBackfillPending(ctx); len(refs) != 0 {
		t.Fatalf("pending after a later pass: %d", len(refs))
	}
	cm, err := s.GetClientMap(ctx, "00000000-0000-7000-8000-0000000007a1")
	if err != nil || cm.PanelGroup != "BOBRES-X" {
		t.Fatalf("%+v %v", cm, err)
	}
}
