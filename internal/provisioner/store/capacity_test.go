package store

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
)

// withLimit sets a server's client limit the way the dashboard does: through UpdateServer, which replaces every setting.
func withLimit(t *testing.T, s *Store, sv *Server, limit int32) {
	t.Helper()
	sv.MaxClients = limit
	sv.Token = "tok-" + sv.Name //gitleaks:allow test fixture
	if err := s.UpdateServer(context.Background(), sv, false); err != nil {
		t.Fatal(err)
	}
}

// The limit is part of the server's row, in a listing too, and 0 (every server before the limit existed) is none.
func TestServerLimitRoundTrip(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	plain := addServer(t, s, "plain", 0)
	capped, err := s.AddServer(ctx, &Server{Name: "capped", BaseURL: "https://capped.test", Token: "tok-capped", Enabled: true, MaxClients: 250}) //gitleaks:allow test fixture
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.ListServers(ctx)
	if err != nil || len(list) != 2 || list[0].MaxClients != 0 || list[1].MaxClients != 250 || list[0].ID != plain.ID || list[1].ID != capped.ID {
		t.Fatalf("listed: %v %+v", err, list)
	}
	withLimit(t, s, capped, 7)
	got, err := s.GetServer(ctx, capped.ID)
	if err != nil || got.MaxClients != 7 {
		t.Fatalf("after an update: %v %+v", err, got)
	}
	// The table refuses a limit outside 0..1,000,000, whatever the application checked.
	for _, bad := range []int32{-1, 1_000_001} {
		capped.MaxClients = bad
		if err := s.UpdateServer(ctx, capped, false); err == nil {
			t.Errorf("a limit of %d was stored", bad)
		}
	}
}

// A server with a limit is skipped once its clients (a reservation counts) have reached it; the rest is as before: the least clients
// per weight, and no server that is off or failing. When every server that would do is full the answer says so, which is not
// "no server": the plan has servers, they are only full.
func TestPickServerSkipsFullServers(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a := addServer(t, s, "a", 300)
	b := addServer(t, s, "b", 100)
	c := addServer(t, s, "c", 100)
	withLimit(t, s, a, 3)
	withLimit(t, s, c, 2)
	pick := func(cands ...string) string {
		t.Helper()
		sv, err := s.PickServer(ctx, cands)
		if err != nil {
			t.Fatalf("pick %v: %v", cands, err)
		}
		return sv.Name
	}
	if got := pick(); got != "a" { // empty, all three: the oldest
		t.Fatalf("all empty: %s", got)
	}
	mapClients(t, s, a, 1, 0)  // a 1/300, with room (3 is its limit)
	mapClients(t, s, b, 2, 10) // b 2/100
	mapClients(t, s, c, 1, 20) // c 1/100, with room (2 is its limit)
	if got := pick(); got != "a" {
		t.Fatalf("a has room and the least per weight: %s", got)
	}
	mapClients(t, s, a, 2, 30)     // a is at 3 of 3
	if got := pick(); got != "c" { // c 0.01 beats b 0.02
		t.Fatalf("a is full, c has the least per weight: %s", got)
	}
	mapClients(t, s, c, 1, 40) // c is at 2 of 2
	if got := pick(); got != "b" {
		t.Fatalf("only b has room: %s", got)
	}
	mapClients(t, s, b, 500, 100) // a server with no limit takes whatever comes
	if got := pick(); got != "b" {
		t.Fatalf("b has no limit: %s", got)
	}

	// Only full servers among the candidates: not ErrNotFound, but a FullError naming how many.
	_, err := s.PickServer(ctx, []string{a.ID, c.ID})
	var full *FullError
	if !errors.Is(err, ErrServerFull) || !errors.As(err, &full) || full.Servers != 2 {
		t.Fatalf("a and c are full: %v", err)
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatal("a full server is not a missing one")
	}
	_, err = s.PickServer(ctx, []string{a.ID})
	if !errors.As(err, &full) || full.Servers != 1 || full.Name != "a" || full.Clients != 3 || full.Max != 3 {
		t.Fatalf("a alone: %v %+v", err, full)
	}

	// Raising the limit gives the room back at once; lowering it below the clients a server has takes it away, and its clients stay.
	withLimit(t, s, a, 4)
	if got := pick(a.ID, c.ID); got != "a" {
		t.Fatalf("a's limit was raised: %s", got)
	}
	withLimit(t, s, a, 1)
	if _, err := s.PickServer(ctx, []string{a.ID}); !errors.Is(err, ErrServerFull) {
		t.Fatalf("a's limit is below its clients: %v", err)
	}
	if list, _ := s.ListServers(ctx); list[0].Clients != 3 {
		t.Fatalf("lowering a limit moved clients: %d", list[0].Clients)
	}
	withLimit(t, s, a, 0) // no limit again
	if got := pick(a.ID, c.ID); got != "a" {
		t.Fatalf("a has no limit: %s", got)
	}

	// A full server that is also off, or failing, is simply not a candidate: the answer is about the servers that would do.
	off := addServer(t, s, "off", 100)
	off.Enabled, off.Token = false, "tok-off"
	if err := s.UpdateServer(ctx, off, false); err != nil {
		t.Fatal(err)
	}
	sick := addServer(t, s, "sick", 100)
	if err := s.RecordHealth(ctx, sick.ID, sick.UpdatedAt, false, "", "panel unreachable"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PickServer(ctx, []string{off.ID, sick.ID}); !errors.Is(err, ErrNotFound) || errors.Is(err, ErrServerFull) {
		t.Fatalf("a server that is off or failing: %v", err)
	}
	// Full ones among them do not hide the one that has room.
	if got := pick(a.ID, c.ID, off.ID, sick.ID); got != "a" {
		t.Fatalf("a has room among servers that do not count: %s", got)
	}
	withLimit(t, s, a, 3) // full again
	if _, err := s.PickServer(ctx, []string{a.ID, c.ID, off.ID, sick.ID}); !errors.As(err, &full) || full.Servers != 2 {
		t.Fatalf("only the two that are on and well count: %v %+v", err, full)
	}
}

// A reservation is refused past the limit, whatever the pick saw; one for a subscription that has a row already needs no place.
func TestReserveClientRespectsTheLimit(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a := addServer(t, s, "a", 0)
	free := addServer(t, s, "free", 0)
	withLimit(t, s, a, 2)
	reserve := func(srv *Server, i int) (*ClientMap, bool, error) {
		xid := fmt.Sprintf("x%d", i)
		return s.ReserveClient(ctx, &ClientMap{SubscriptionID: fmt.Sprintf("00000000-0000-7000-8000-%012d", i), ServerID: srv.ID, Email: fmt.Sprintf("u%d-r", i), XUISubID: &xid})
	}
	for i := 1; i <= 2; i++ {
		if _, fresh, err := reserve(a, i); err != nil || !fresh {
			t.Fatalf("reservation %d: %v %v", i, err, fresh)
		}
	}
	_, _, err := reserve(a, 3)
	var full *FullError
	if !errors.As(err, &full) || !errors.Is(err, ErrServerFull) || full.Name != "a" || full.Clients != 2 || full.Max != 2 || full.Servers != 1 {
		t.Fatalf("a third place: %v %+v", err, full)
	}
	if _, err := s.GetClientMap(ctx, "00000000-0000-7000-8000-000000000003"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a refused reservation left a row: %v", err)
	}
	// A retry of a subscription that has its place gets it back, with the server full.
	again, fresh, err := reserve(a, 1)
	if err != nil || fresh || again.ServerID != a.ID {
		t.Fatalf("a retry on a full server: %v %v %+v", err, fresh, again)
	}
	// ... and so does a retry that picked another server this time.
	again, fresh, err = reserve(free, 2)
	if err != nil || fresh || again.ServerID != a.ID {
		t.Fatalf("a retry that picked another server: %v %v %+v", err, fresh, again)
	}
	// Another server is not affected, and a place that comes free (a client deleted, a reservation released) is taken.
	if _, _, err := reserve(free, 4); err != nil {
		t.Fatalf("the other server: %v", err)
	}
	if err := s.ReleaseReservation(ctx, "00000000-0000-7000-8000-000000000001"); err != nil {
		t.Fatal(err)
	}
	if _, fresh, err := reserve(a, 3); err != nil || !fresh {
		t.Fatalf("the released place: %v %v", err, fresh)
	}
	// A delivered client is a client too.
	if err := s.PutClientMap(ctx, &ClientMap{SubscriptionID: "00000000-0000-7000-8000-000000000003", ServerID: a.ID, Email: "u3-r", InboundIDs: []int32{1}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := reserve(a, 5); !errors.Is(err, ErrServerFull) {
		t.Fatalf("full with a delivered client and a reservation: %v", err)
	}
	// A limit of 0 is no limit, and a server below its clients (the limit was lowered) takes no more.
	withLimit(t, s, a, 0)
	if _, fresh, err := reserve(a, 5); err != nil || !fresh {
		t.Fatalf("no limit: %v %v", err, fresh)
	}
	withLimit(t, s, a, 1)
	if _, _, err := reserve(a, 6); !errors.Is(err, ErrServerFull) {
		t.Fatalf("a limit below the clients: %v", err)
	}
}

// Many creates at once never take a server past its limit: the reservations of a server go one at a time, so the place that is left is
// taken by exactly one of them. (Without the lock each would count the clients before any other had written its row.) A trigger
// that makes each insert take 20 ms stretches the time between the count and the row, so a missing lock cannot go unseen.
func TestConcurrentReservationsNeverExceedTheLimit(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	for _, q := range []string{
		`CREATE OR REPLACE FUNCTION provisioner.slow_reservation() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_sleep(0.02); RETURN NEW; END $$`,
		`CREATE TRIGGER slow_reservation BEFORE INSERT ON provisioner.client_map FOR EACH ROW EXECUTE FUNCTION provisioner.slow_reservation()`,
	} {
		if _, err := s.DB().Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_, _ = s.DB().Exec(context.Background(), `DROP TRIGGER IF EXISTS slow_reservation ON provisioner.client_map`)
		_, _ = s.DB().Exec(context.Background(), `DROP FUNCTION IF EXISTS provisioner.slow_reservation()`)
	})
	const limit, tries = 5, 40
	a := addServer(t, s, "a", 0)
	withLimit(t, s, a, limit)
	mapClients(t, s, a, 1, 900) // a place is taken before the race begins
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		created int
		refused int
	)
	start := make(chan struct{})
	for i := 0; i < tries; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			xid := fmt.Sprintf("x%d", i)
			_, fresh, err := s.ReserveClient(ctx, &ClientMap{SubscriptionID: fmt.Sprintf("00000000-0000-7000-8000-%012d", i+1), ServerID: a.ID, Email: fmt.Sprintf("u%d-c", i), XUISubID: &xid})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil && fresh:
				created++
			case errors.Is(err, ErrServerFull):
				refused++
			default:
				t.Errorf("reservation %d: %v %v", i, err, fresh)
			}
		}()
	}
	close(start)
	wg.Wait()
	if created != limit-1 || refused != tries-(limit-1) {
		t.Fatalf("%d reserved and %d refused, want %d and %d", created, refused, limit-1, tries-(limit-1))
	}
	var n int
	if err := s.DB().QueryRow(ctx, `SELECT count(*) FROM provisioner.client_map WHERE server_id = $1`, a.ID).Scan(&n); err != nil || n != limit {
		t.Fatalf("the server holds %d clients, want exactly its limit of %d (%v)", n, limit, err)
	}
}
