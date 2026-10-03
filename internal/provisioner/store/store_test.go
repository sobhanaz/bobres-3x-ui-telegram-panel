package store

import (
	"context"
	"errors"
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
	s, err := New(ctx, dsn, env)
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

	sv, err := s.AddServer(ctx, &Server{Name: "main", BaseURL: "https://panel.test", Token: "sekrit-token", Enabled: true,
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

	if err := s.TouchHealth(ctx, sv.ID, "3.2.1"); err != nil {
		t.Fatal(err)
	}
	got2, _ := s.FirstServer(ctx)
	if got2.PanelVersion != "3.2.1" {
		t.Errorf("panel version = %q", got2.PanelVersion)
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
