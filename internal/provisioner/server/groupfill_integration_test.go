//go:build integration

package server

// The provisioner service against a REAL 3x-ui panel and a real database: the
// calls core makes (create, renew, new limits, switch off and on, traffic
// reset, delete) leave every client in the bot group, and the backfill puts the
// clients BOBRES made before the groups into it without touching a group an
// operator chose or a client BOBRES does not hold. Variables as for the
// adapter's real-panel tests (XUI_TEST_URL, XUI_TEST_TOKEN, XUI_TEST_ALLOW_PRIVATE=1,
// XUI_TEST_REQUIRE=1) and the test database (BOBRES_TEST_DATABASE_URL,
// BOBRES_TEST_REQUIRE_DB=1):
//
//	go test -tags integration -run Integration ./internal/provisioner/server/

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	provisionerv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/provisioner/v1"
	bcrypto "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/crypto"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/migrate"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/provisioner/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testdb"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/xui"
)

// realFixture is a provisioner service whose only server is the real panel.
type realFixture struct {
	srv    *Server
	st     *store.Store
	server *store.Server
	panel  *xui.Client // the panel itself, to look at what BOBRES left there
	run    string      // makes the names of this run unique on a panel that lives on
	base   int64       // the same, for subscription ids
	subs   int64
	logs   *syncBuffer
	made   []string
}

func newRealFixture(t *testing.T) *realFixture {
	t.Helper()
	baseURL, token := os.Getenv("XUI_TEST_URL"), os.Getenv("XUI_TEST_TOKEN")
	if baseURL == "" || token == "" {
		if os.Getenv("XUI_TEST_REQUIRE") == "1" {
			t.Fatal("XUI_TEST_URL and XUI_TEST_TOKEN not set and XUI_TEST_REQUIRE=1")
		}
		t.Skip("XUI_TEST_URL and XUI_TEST_TOKEN not set")
	}
	var opts []xui.Option
	allowPrivate := os.Getenv("XUI_TEST_ALLOW_PRIVATE") == "1"
	if allowPrivate {
		opts = append(opts, xui.AllowPrivateAddresses())
	}
	dsn := testdb.DSN(t)
	ctx := context.Background()
	if err := migrate.Up(ctx, dsn, "provisioner"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	env, err := bcrypto.NewEnvelope([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.New(ctx, testdb.WithPoolCap(t, dsn), env)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(st.Close)
	if _, err := st.DB().Exec(ctx, "TRUNCATE provisioner.client_map, provisioner.provision_jobs, provisioner.xui_servers CASCADE"); err != nil {
		t.Fatal(err)
	}
	sv, err := st.AddServer(ctx, &store.Server{Name: "real", BaseURL: baseURL, Token: token, Enabled: true, AllowPrivate: allowPrivate})
	if err != nil {
		t.Fatal(err)
	}
	panel, err := xui.New(baseURL, token, opts...)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UnixNano()
	f := &realFixture{srv: New(st, opts...), st: st, server: sv, panel: panel, run: fmt.Sprintf("%d", now), base: now % 1_000_000_000_000, logs: &syncBuffer{}}
	f.srv.SetLogger(slog.New(slog.NewTextHandler(f.logs, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() {
		for _, e := range f.made {
			_ = panel.DeleteClient(context.Background(), e, false) // gone already is fine
		}
	})
	return f
}

// email is a name for a client of this run; it is deleted from the panel when the test ends.
func (f *realFixture) email(label string) string {
	e := fmt.Sprintf("it%s-%s", f.run, label)
	f.made = append(f.made, e)
	return e
}

// newSub is a subscription id of a service, new in this run.
func (f *realFixture) newSub() string {
	f.subs++
	return fmt.Sprintf("00000000-0000-7000-8000-%012d", f.base+f.subs)
}

func (f *realFixture) group(t *testing.T, email string) string {
	t.Helper()
	d, err := f.panel.GetClient(context.Background(), email)
	if err != nil {
		t.Fatalf("get %s: %v", email, err)
	}
	return d.Client.Group
}

// hold makes a client BOBRES holds as an install from before the groups left
// it: a row in the client map and the client on the panel, in `group`.
func (f *realFixture) hold(t *testing.T, label, group string) string {
	t.Helper()
	ctx := context.Background()
	email, sub := f.email(label), f.newSub()
	xid := f.st.SubIDFor(sub)
	ins, err := f.panel.InboundOptions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int
	for _, in := range ins {
		if in.Enable {
			ids = append(ids, in.ID)
		}
	}
	if len(ids) == 0 {
		t.Fatal("the panel has no enabled inbound; enable one for this test")
	}
	spec := xui.ClientSpec{Email: email, SubID: xid, Enable: true, Group: group, TotalGB: 1 << 30, ExpiryTime: time.Now().Add(24 * time.Hour).UnixMilli()}
	if err := f.panel.AddClient(ctx, spec, ids); err != nil {
		t.Fatalf("add %s: %v", email, err)
	}
	cm := &store.ClientMap{SubscriptionID: sub, ServerID: f.server.ID, Email: email, XUISubID: &xid, InboundIDs: toInt32s(ids)}
	if err := f.st.PutClientMap(ctx, cm); err != nil {
		t.Fatal(err)
	}
	return email
}

// The whole life of a service through the provisioner's calls: the client is made
// in the bot group and stays there. One a retried create finds on the panel,
// left by an attempt from before the groups, is put in it.
func TestIntegrationProvisionerKeepsTheBotGroup(t *testing.T) {
	f := newRealFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	sub, email := f.newSub(), f.email("life")

	cl, err := f.srv.CreateClient(ctx, &provisionerv1.CreateClientRequest{SubscriptionId: sub, Email: email, DurationDays: 30, TrafficBytes: 5 << 30})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	check := func(when string) {
		t.Helper()
		if g := f.group(t, email); g != xui.BotGroup {
			t.Fatalf("%s: group %q", when, g)
		}
	}
	check("after create")
	for _, step := range []struct {
		name string
		do   func() error
	}{
		{"renew", func() error {
			_, err := f.srv.RenewClient(ctx, &provisionerv1.RenewClientRequest{SubscriptionId: sub, AddDays: 5, AddBytes: 1 << 30})
			return err
		}},
		{"new limits", func() error {
			_, err := f.srv.SetClientLimits(ctx, &provisionerv1.SetClientLimitsRequest{SubscriptionId: sub, ExpiresAt: cl.GetExpiresAt() + 86400, TrafficTotalBytes: 20 << 30})
			return err
		}},
		{"switch off", func() error {
			_, err := f.srv.SetClientEnabled(ctx, &provisionerv1.SetClientEnabledRequest{SubscriptionId: sub, Enabled: false})
			return err
		}},
		{"switch on", func() error {
			_, err := f.srv.SetClientEnabled(ctx, &provisionerv1.SetClientEnabledRequest{SubscriptionId: sub, Enabled: true})
			return err
		}},
		{"traffic reset", func() error {
			_, err := f.srv.ResetTraffic(ctx, &provisionerv1.ResetTrafficRequest{SubscriptionId: sub})
			return err
		}},
	} {
		if err := step.do(); err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
		check("after " + step.name)
	}
	members, err := f.panel.GroupEmails(ctx, xui.BotGroup)
	if err != nil || !slices.Contains(members, email) {
		t.Fatalf("the panel's members of %s: %v %v", xui.BotGroup, members, err)
	}
	if _, err := f.srv.DeleteClient(ctx, &provisionerv1.DeleteClientRequest{SubscriptionId: sub}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if members, err := f.panel.GroupEmails(ctx, xui.BotGroup); err != nil || slices.Contains(members, email) {
		t.Fatalf("a deleted client is still a member: %v %v", members, err)
	}

	// An earlier attempt, from before the groups, made the client and lost its
	// answer (what it left: a reservation, and the client on the panel). The
	// retry puts the client in the group, unless it is in another. The panel
	// takes the create of its own client as a success that changes nothing.
	lostSub, lost := f.newSub(), f.email("lost")
	opSub, op := f.newSub(), f.email("operator")
	ins, err := f.panel.InboundOptions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int
	for _, in := range ins {
		if in.Enable {
			ids = append(ids, in.ID)
		}
	}
	for _, c := range []struct{ email, sub, group string }{{lost, lostSub, ""}, {op, opSub, "it-operator-" + f.run}} {
		xid := f.st.SubIDFor(c.sub)
		if _, _, err := f.st.ReserveClient(ctx, &store.ClientMap{SubscriptionID: c.sub, ServerID: f.server.ID, Email: c.email, XUISubID: &xid}); err != nil {
			t.Fatal(err)
		}
		spec := xui.ClientSpec{Email: c.email, SubID: xid, Enable: true, Group: c.group, TotalGB: 1 << 30}
		if err := f.panel.AddClient(ctx, spec, ids); err != nil {
			t.Fatal(err)
		}
		if _, err := f.srv.CreateClient(ctx, &provisionerv1.CreateClientRequest{SubscriptionId: c.sub, Email: c.email, DurationDays: 30}); err != nil {
			t.Fatalf("retrying %s: %v", c.email, err)
		}
	}
	if g := f.group(t, lost); g != xui.BotGroup {
		t.Fatalf("a client found again by a retried create is in %q", g)
	}
	if g := f.group(t, op); !strings.HasPrefix(g, "it-operator-") {
		t.Fatalf("an operator's group became %q", g)
	}
}

// The backfill on a real panel and a real database.
func TestIntegrationBackfillGroupsRealPanel(t *testing.T) {
	f := newRealFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	operator := "it-operator-" + f.run

	a, b := f.hold(t, "old-a", ""), f.hold(t, "old-b", "")
	op := f.hold(t, "old-op", operator)
	already := f.hold(t, "old-bot", xui.BotGroup)
	// A client on the panel that BOBRES does not hold.
	stranger := f.email("stranger")
	ins, err := f.panel.InboundOptions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.panel.AddClient(ctx, xui.ClientSpec{Email: stranger, SubID: "it-stranger-" + f.run, Enable: true}, []int{ins[0].ID}); err != nil {
		t.Fatal(err)
	}
	// A client BOBRES holds that was deleted on the panel by hand.
	gone, goneSub := f.email("gone"), f.newSub()
	xid := f.st.SubIDFor(goneSub)
	if err := f.st.PutClientMap(ctx, &store.ClientMap{SubscriptionID: goneSub, ServerID: f.server.ID, Email: gone, XUISubID: &xid, InboundIDs: []int32{}}); err != nil {
		t.Fatal(err)
	}

	f.srv.BackfillGroups(ctx)

	for email, want := range map[string]string{a: xui.BotGroup, b: xui.BotGroup, op: operator, already: xui.BotGroup, stranger: ""} {
		if g := f.group(t, email); g != want {
			t.Errorf("%s is in %q, want %q", email, g, want)
		}
	}
	refs, err := f.st.GroupBackfillPending(ctx)
	if err != nil || len(refs) != 0 {
		t.Fatalf("pending after the backfill: %v %v", refs, err)
	}
	if !strings.Contains(f.logs.String(), "backfill finished") || !strings.Contains(f.logs.String(), "tagged=2") ||
		!strings.Contains(f.logs.String(), "already_in_a_group=2") || !strings.Contains(f.logs.String(), "missing=1") {
		t.Fatalf("the summary line:\n%s", f.logs.String())
	}
}
