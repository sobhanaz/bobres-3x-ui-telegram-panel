package store

import (
	"context"
	"errors"
	"fmt"
	"testing"

	bcrypto "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/crypto"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/migrate"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testdb"
)

func TestMain(m *testing.M) { testdb.Main(m) }

func testStore(t *testing.T) *Store {
	t.Helper()
	dsn := testdb.DSN(t)
	ctx := context.Background()
	if err := migrate.Up(ctx, dsn, "provisioner"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	env, err := bcrypto.NewEnvelope([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(ctx, testdb.WithPoolCap(t, dsn), env)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(s.Close)
	for _, tbl := range []string{"client_map", "provision_jobs", "xui_servers"} {
		if _, err := s.DB().Exec(ctx, "TRUNCATE provisioner."+tbl+" CASCADE"); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func TestServerTokenEncryptedAtRest(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	sv, err := s.AddServer(ctx, &Server{Name: "main", BaseURL: "https://panel.test", Token: "sekrit-token", Enabled: true, //nolint:gosec // test fixture
		SubBaseURL: "https://sub.panel.test/sub/", AllowPrivate: true})
	if err != nil {
		t.Fatal(err)
	}

	// Raw DB value must not contain the token.
	var enc []byte
	if err := s.DB().QueryRow(ctx, `SELECT api_token_enc FROM provisioner.xui_servers WHERE id=$1`, sv.ID).Scan(&enc); err != nil {
		t.Fatal(err)
	}
	if string(enc) == "sekrit-token" || len(enc) == 0 {
		t.Fatal("token stored unencrypted")
	}

	got, err := s.FirstServer(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.Token != "sekrit-token" || got.Name != "main" || !got.Enabled || got.SubBaseURL == "" || !got.AllowPrivate {
		t.Errorf("bad server round trip: %+v", got)
	}

	if err := s.RecordHealth(ctx, sv.ID, sv.UpdatedAt, true, "v3.2.1", ""); err != nil {
		t.Fatal(err)
	}
	got2, _ := s.FirstServer(ctx)
	if got2.PanelVersion != "v3.2.1" || got2.HealthOK == nil || !*got2.HealthOK || got2.CheckedAt == nil {
		t.Errorf("after a health check: %+v", got2)
	}

	// A listing never decrypts the token: it only says one is set.
	list, err := s.ListServers(ctx)
	if err != nil || len(list) != 1 || list[0].Token != "" || !list[0].HasToken || list[0].Weight != DefaultWeight || list[0].Managed != ManagedEnv {
		t.Fatalf("list: %v %+v", err, list)
	}
}

func TestClientMapCRUD(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	sv, _ := s.AddServer(ctx, &Server{Name: "m", BaseURL: "https://p", Token: "fake", Enabled: true}) //gitleaks:allow test fixture

	cm := &ClientMap{SubscriptionID: "00000000-0000-7000-8000-000000000001", ServerID: sv.ID, Email: "u1-abc", InboundIDs: []int32{1, 2}}
	if err := s.PutClientMap(ctx, cm); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetClientMap(ctx, "00000000-0000-7000-8000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	if got.Email != "u1-abc" || len(got.InboundIDs) != 2 {
		t.Errorf("bad client map: %+v", got)
	}

	if _, err := s.GetClientMap(ctx, "00000000-0000-7000-8000-0000000000ff"); !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}

	if err := s.DeleteClientMap(ctx, "00000000-0000-7000-8000-000000000001"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetClientMap(ctx, "00000000-0000-7000-8000-000000000001"); !errors.Is(err, ErrNotFound) {
		t.Errorf("after delete: %v", err)
	}
}

func TestEnqueueJob(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	subID := "00000000-0000-7000-8000-000000000009"
	if err := s.EnqueueJob(ctx, &Job{SubscriptionID: &subID, Action: "create"}); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := s.DB().QueryRow(ctx, `SELECT status FROM provisioner.provision_jobs WHERE subscription_id=$1`, subID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "pending" {
		t.Errorf("status = %q", status)
	}
}

func addServer(t *testing.T, s *Store, name string, weight int32) *Server {
	t.Helper()
	sv, err := s.AddServer(context.Background(), &Server{Name: name, BaseURL: "https://" + name + ".test", Token: "tok-" + name, Enabled: true, Weight: weight}) //gitleaks:allow test fixture
	if err != nil {
		t.Fatal(err)
	}
	return sv
}

func mapClients(t *testing.T, s *Store, sv *Server, n int, from int) {
	t.Helper()
	for i := from; i < from+n; i++ {
		cm := &ClientMap{SubscriptionID: fmt.Sprintf("00000000-0000-7000-8000-%012d", i), ServerID: sv.ID, Email: fmt.Sprintf("u%d-x", i), InboundIDs: []int32{1}}
		if err := s.PutClientMap(context.Background(), cm); err != nil {
			t.Fatal(err)
		}
	}
}

func TestServerNamesAreUnique(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a := addServer(t, s, "a", 0)
	addServer(t, s, "b", 0)
	if _, err := s.AddServer(ctx, &Server{Name: "a", BaseURL: "https://x.test", Token: "t", Enabled: true}); !errors.Is(err, ErrNameTaken) { //gitleaks:allow test fixture
		t.Fatalf("duplicate add: %v", err)
	}
	a.Name = "b"
	if err := s.UpdateServer(ctx, a, false); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("rename onto another server: %v", err)
	}
	a.ID = "00000000-0000-7000-8000-0000000000ff"
	a.Name = "c"
	if err := s.UpdateServer(ctx, a, false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown server: %v", err)
	}
	if _, err := s.AddServer(ctx, &Server{Name: "w", BaseURL: "https://w.test", Token: "t", Weight: 1001}); err == nil { //gitleaks:allow test fixture
		t.Fatal("weight 1001 accepted")
	}
}

// The least clients per weight wins, oldest first on a tie; disabled and
// failing servers and servers outside the candidates are never picked.
func TestPickServer(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a := addServer(t, s, "a", 100)
	b := addServer(t, s, "b", 300)
	off := addServer(t, s, "off", 1000)
	off.Enabled, off.Token = false, "tok-off"
	if err := s.UpdateServer(ctx, off, false); err != nil {
		t.Fatal(err)
	}
	sick := addServer(t, s, "sick", 1000)
	if err := s.RecordHealth(ctx, sick.ID, sick.UpdatedAt, false, "", "panel unreachable"); err != nil {
		t.Fatal(err)
	}
	pick := func(cands []string) string {
		t.Helper()
		sv, err := s.PickServer(ctx, cands)
		if err != nil {
			t.Fatalf("pick %v: %v", cands, err)
		}
		return sv.Name
	}
	if got := pick(nil); got != "a" { // both empty: the older one
		t.Fatalf("empty servers: %s", got)
	}
	mapClients(t, s, a, 1, 0) // a 1/100 > b 0/300
	if got := pick(nil); got != "b" {
		t.Fatalf("a has a client: %s", got)
	}
	mapClients(t, s, b, 3, 10) // b 3/300 = a 1/100: the older one
	if got := pick([]string{}); got != "a" {
		t.Fatalf("tie: %s", got)
	}
	mapClients(t, s, a, 1, 20) // a 2/100 > b 3/300
	if got := pick(nil); got != "b" {
		t.Fatalf("a twice as loaded: %s", got)
	}
	if got := pick([]string{a.ID}); got != "a" {
		t.Fatalf("only a allowed: %s", got)
	}
	for _, cands := range [][]string{{off.ID}, {sick.ID}, {"not-a-uuid"}, {"00000000-0000-7000-8000-0000000000ff"}} {
		if _, err := s.PickServer(ctx, cands); !errors.Is(err, ErrNotFound) {
			t.Errorf("candidates %v: want ErrNotFound, got %v", cands, err)
		}
	}
	// Never checked counts as healthy; a passed check brings a server back.
	if err := s.RecordHealth(ctx, sick.ID, sick.UpdatedAt, true, "v3.9.0", ""); err != nil {
		t.Fatal(err)
	}
	if got := pick(nil); got != "sick" {
		t.Fatalf("recovered server with no clients: %s", got)
	}
}

func TestReserveClient(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a := addServer(t, s, "a", 0)
	b := addServer(t, s, "b", 0)
	sub := "00000000-0000-7000-8000-000000000001"
	xid := "sub-1"
	cm, fresh, err := s.ReserveClient(ctx, &ClientMap{SubscriptionID: sub, ServerID: a.ID, Email: "u1-a", XUISubID: &xid})
	if err != nil || !fresh || !cm.Pending || cm.ServerID != a.ID {
		t.Fatalf("reserve: %v %v %+v", err, fresh, cm)
	}
	// A second attempt (even one that picked another server) gets the first.
	again, fresh, err := s.ReserveClient(ctx, &ClientMap{SubscriptionID: sub, ServerID: b.ID, Email: "u1-a", XUISubID: &xid})
	if err != nil || fresh || again.ServerID != a.ID || !again.Pending {
		t.Fatalf("second reserve: %v %v %+v", err, fresh, again)
	}
	if list, _ := s.ListServers(ctx); list[0].Clients != 1 { // reservations count as load
		t.Fatalf("clients of a: %d", list[0].Clients)
	}
	// The email is taken on that server by another subscription.
	if _, _, err := s.ReserveClient(ctx, &ClientMap{SubscriptionID: "00000000-0000-7000-8000-000000000002", ServerID: a.ID, Email: "u1-a", XUISubID: &xid}); !errors.Is(err, ErrEmailTaken) {
		t.Fatalf("same email on the server: %v", err)
	}
	// Delivered: no longer pending, and never released.
	if err := s.PutClientMap(ctx, &ClientMap{SubscriptionID: sub, ServerID: a.ID, Email: "u1-a", XUISubID: &xid, InboundIDs: []int32{1}}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReleaseReservation(ctx, sub); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetClientMap(ctx, sub)
	if err != nil || got.Pending || len(got.InboundIDs) != 1 {
		t.Fatalf("delivered client after a release: %v %+v", err, got)
	}
	// A reservation is released.
	other := "00000000-0000-7000-8000-000000000003"
	if _, _, err := s.ReserveClient(ctx, &ClientMap{SubscriptionID: other, ServerID: b.ID, Email: "u3-b", XUISubID: &xid}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReleaseReservation(ctx, other); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetClientMap(ctx, other); !errors.Is(err, ErrNotFound) {
		t.Fatalf("released reservation: %v", err)
	}
	// A server deleted in between.
	if _, _, err := s.ReserveClient(ctx, &ClientMap{SubscriptionID: other, ServerID: "00000000-0000-7000-8000-0000000000ff", Email: "u3-b"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown server: %v", err)
	}
}

func TestDeleteServer(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a := addServer(t, s, "a", 0)
	mapClients(t, s, a, 2, 0)
	if err := s.SetInboundAttach(ctx, a.ID, 2, false); err != nil {
		t.Fatal(err)
	}
	if n, err := s.DeleteServer(ctx, a.ID); !errors.Is(err, ErrServerInUse) || n != 2 {
		t.Fatalf("delete with clients: %d %v", n, err)
	}
	for i := 0; i < 2; i++ {
		if err := s.DeleteClientMap(ctx, fmt.Sprintf("00000000-0000-7000-8000-%012d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.DeleteServer(ctx, a.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	var settings int
	if err := s.DB().QueryRow(ctx, `SELECT count(*) FROM provisioner.server_inbounds`).Scan(&settings); err != nil || settings != 0 {
		t.Fatalf("inbound settings left: %d %v", settings, err)
	}
	if _, err := s.DeleteServer(ctx, a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
}

// Health checks never move updated_at (cached panel clients stay), and a
// check of a connection edited meanwhile is dropped.
func TestRecordHealth(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a := addServer(t, s, "a", 0)
	if err := s.RecordHealth(ctx, a.ID, a.UpdatedAt, false, "", "panel unreachable"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetServer(ctx, a.ID)
	if !got.UpdatedAt.Equal(a.UpdatedAt) || got.HealthOK == nil || *got.HealthOK || got.HealthError != "panel unreachable" {
		t.Fatalf("after a failed check: %+v", got)
	}
	// A failed check keeps the version of the last good one.
	if err := s.RecordHealth(ctx, a.ID, a.UpdatedAt, true, "v3.9.0", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordHealth(ctx, a.ID, a.UpdatedAt, false, "", "down"); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.GetServer(ctx, a.ID); got.PanelVersion != "v3.9.0" {
		t.Fatalf("version after a failure: %q", got.PanelVersion)
	}
	old := a.UpdatedAt
	a.BaseURL = "https://a2.test"
	if err := s.UpdateServer(ctx, a, true); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetServer(ctx, a.ID)
	if got.UpdatedAt.Equal(old) || got.HealthOK != nil || got.CheckedAt != nil || got.HealthError != "" || got.Managed != ManagedDashboard {
		t.Fatalf("a new connection starts unchecked and dashboard-managed: %+v", got)
	}
	if err := s.RecordHealth(ctx, a.ID, old, true, "v3.8.5", ""); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.GetServer(ctx, a.ID); got.HealthOK != nil {
		t.Fatalf("a check of the old connection was stored: %+v", got)
	}
	// Renaming keeps the health.
	if err := s.RecordHealth(ctx, a.ID, got.UpdatedAt, true, "v3.9.0", ""); err != nil {
		t.Fatal(err)
	}
	got.Name = "renamed"
	if err := s.UpdateServer(ctx, got, false); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.GetServer(ctx, a.ID); got.HealthOK == nil || !*got.HealthOK || got.Token != "tok-a" {
		t.Fatalf("after a rename: %+v", got)
	}
}

func TestInboundAttach(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a := addServer(t, s, "a", 0)
	if m, err := s.InboundAttach(ctx, a.ID); err != nil || len(m) != 0 {
		t.Fatalf("no settings: %v %v", m, err)
	}
	for _, on := range []bool{false, true, false} {
		if err := s.SetInboundAttach(ctx, a.ID, 7, on); err != nil {
			t.Fatal(err)
		}
	}
	if m, err := s.InboundAttach(ctx, a.ID); err != nil || len(m) != 1 || m[7] {
		t.Fatalf("settings: %v %v", m, err)
	}
	if err := s.SetInboundAttach(ctx, "00000000-0000-7000-8000-0000000000ff", 7, false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown server: %v", err)
	}
}

func TestServerKind(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	x := addServer(t, s, "kind-x", 0)
	m, err := s.AddServer(ctx, &Server{Name: "kind-m", BaseURL: "https://m.test", Token: "admin:pw", Enabled: true, Kind: KindMarzban}) //gitleaks:allow test fixture
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddServer(ctx, &Server{Name: "kind-bad", BaseURL: "https://b.test", Token: "t", Kind: "nope"}); err == nil { //gitleaks:allow test fixture
		t.Error("unknown kind stored")
	}
	for id, want := range map[string]string{x.ID: KindXUI, m.ID: KindMarzban} {
		got, err := s.GetServer(ctx, id)
		if err != nil || got.Kind != want {
			t.Errorf("server %s kind = %q, %v; want %q", id, got.Kind, err, want)
		}
	}
	if got, _ := s.GetServer(ctx, m.ID); got.Token != "admin:pw" { //gitleaks:allow test fixture
		t.Errorf("marzban login did not round-trip: %q", got.Token)
	}
}
