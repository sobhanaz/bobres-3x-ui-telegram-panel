package domain

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// fakeServers is the provisioner's server registry: it keeps tokens (as the
// real one does) and never answers one. listErr: the provisioner does not
// answer; failUpdate and failDelete make those writes fail.
type fakeServers struct {
	mu                              sync.Mutex
	servers                         []Server
	tokens                          map[string]string
	probes                          []ServerProbe
	attach                          map[int32]bool
	next                            int
	listErr, failUpdate, failDelete error
	added                           func() // runs once a server is added
	ignoresEnabled                  bool   // an older provisioner: adds servers switched on
}

func (f *fakeServers) ListServers(context.Context) (*ServerList, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	return &ServerList{Servers: slices.Clone(f.servers), TestedMin: "v3.8.5", TestedMax: "v3.9.0"}, nil
}

// setListErr makes the provisioner stop (or start again) answering.
func (f *fakeServers) setListErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listErr = err
}

func (f *fakeServers) AddServer(_ context.Context, in ServerInput) (*Server, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.added != nil {
		defer f.added()
	}
	f.next++
	sv := Server{ID: fmt.Sprintf("00000000-0000-7000-8000-%012d", f.next), Name: in.Name, BaseURL: in.BaseURL,
		SubBaseURL: in.SubBaseURL, Enabled: in.Enabled || f.ignoresEnabled, AllowPrivate: in.AllowPrivate, Weight: in.Weight,
		Managed: "dashboard", HasToken: true}
	if f.tokens == nil {
		f.tokens = map[string]string{}
	}
	f.tokens[sv.ID] = in.Token
	f.servers = append(f.servers, sv)
	return &sv, nil
}

func (f *fakeServers) UpdateServer(ctx context.Context, id string, in ServerInput) (*Server, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if f.failUpdate != nil {
		return nil, f.failUpdate
	}
	for i := range f.servers {
		sv := &f.servers[i]
		if sv.ID != id {
			continue
		}
		sv.Name, sv.BaseURL, sv.SubBaseURL, sv.Enabled = in.Name, in.BaseURL, in.SubBaseURL, in.Enabled
		sv.AllowPrivate, sv.Weight = in.AllowPrivate, in.Weight
		if in.Token != "" {
			f.tokens[id] = in.Token
		}
		out := *sv
		return &out, nil
	}
	return nil, status.Error(codes.NotFound, "3x-ui server not found")
}

func (f *fakeServers) DeleteServer(ctx context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if f.failDelete != nil {
		return f.failDelete
	}
	for i, sv := range f.servers {
		if sv.ID == id {
			if sv.Clients > 0 {
				return status.Errorf(codes.FailedPrecondition, "the server still has %d clients: delete or move them first", sv.Clients)
			}
			f.servers = slices.Delete(f.servers, i, i+1)
			return nil
		}
	}
	return status.Error(codes.NotFound, "3x-ui server not found")
}

func (f *fakeServers) TestServer(_ context.Context, p ServerProbe) (*ServerTest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.probes = append(f.probes, p)
	return &ServerTest{OK: true, PanelVersion: "v3.9.0", Inbounds: 2}, nil
}

func (f *fakeServers) ListInbounds(context.Context, string) ([]Inbound, error) { return nil, nil }

func (f *fakeServers) SetInboundAttach(_ context.Context, _ string, inbound int32, attach bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.attach == nil {
		f.attach = map[int32]bool{}
	}
	f.attach[inbound] = attach
	return nil
}

func auditJSON(t *testing.T, s *store.Store, action string) []string {
	t.Helper()
	rows, err := s.DB().Query(context.Background(), `SELECT coalesce(before::text, '') || ' ' || coalesce(after::text, '')
		FROM core.audit_log WHERE action = $1 ORDER BY created_at, id`, action)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	return out
}

func TestNormalizeServer(t *testing.T) {
	ok := ServerInput{Name: " Germany 1 ", BaseURL: " https://de.example.com:2053/p ", Token: " tok ", Weight: 0}
	got, err := normalizeServer(ok, true)
	if err != nil || got.Name != "Germany 1" || got.BaseURL != "https://de.example.com:2053/p" || got.Token != "tok" || got.Weight != 100 {
		t.Fatalf("normalized: %+v %v", got, err)
	}
	if _, err := normalizeServer(ServerInput{Name: "x", BaseURL: "https://a.example", Weight: 50}, false); err != nil {
		t.Fatalf("an edit may keep the token: %v", err)
	}
	if _, err := normalizeServer(ServerInput{Name: "x", BaseURL: "http://10.0.0.5:2053", Token: "t", AllowPrivate: true}, true); err != nil {
		t.Fatalf("plain http on a private network: %v", err)
	}
	const secret = "s3cr3t-token"
	for field, in := range map[string]ServerInput{
		"name":         {Name: " ", BaseURL: "https://a.example", Token: "t"},
		"name ":        {Name: strings.Repeat("n", 65), BaseURL: "https://a.example", Token: "t"},
		"name  ":       {Name: "a\nb", BaseURL: "https://a.example", Token: "t"},
		"base_url":     {Name: "x", BaseURL: "ftp://a.example", Token: "t"},
		"base_url ":    {Name: "x", BaseURL: "http://a.example", Token: "t"},
		"base_url  ":   {Name: "x", BaseURL: "https://admin:" + secret + "@a.example", Token: "t"},
		"base_url   ":  {Name: "x", BaseURL: "", Token: "t"},
		"sub_base_url": {Name: "x", BaseURL: "https://a.example", SubBaseURL: "sub.example", Token: "t"},
		"token":        {Name: "x", BaseURL: "https://a.example", Token: ""},
		"token ":       {Name: "x", BaseURL: "https://a.example", Token: "two " + secret},
		"weight":       {Name: "x", BaseURL: "https://a.example", Token: "t", Weight: 1001},
		"weight ":      {Name: "x", BaseURL: "https://a.example", Token: "t", Weight: -1},
	} {
		_, err := normalizeServer(in, true)
		var fe *FieldError
		if !errors.As(err, &fe) || fe.Field != strings.TrimSpace(field) {
			t.Errorf("%q: %v", field, err)
		}
		if err != nil && strings.Contains(err.Error(), secret) {
			t.Errorf("%q: the error repeats a secret: %v", field, err)
		}
	}
	if _, err := normalizeServer(ServerInput{Name: "x", BaseURL: "https://a.example", Weight: 0}, false); err == nil {
		t.Error("an edit without a weight was accepted")
	}
}

// Every write is audited with the settings and "changed" for a token that
// was sent; no token ever reaches the audit log.
func TestServerWritesAreAuditedWithoutTheToken(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	own := owner(t, svc)
	fs := &fakeServers{}
	const tok1, tok2 = "first-panel-token-AAAA", "second-panel-token-BBBB"

	sv, err := svc.AddServer(ctx, own, fs, ServerInput{Name: "DE", BaseURL: "https://de.example.com", Token: tok1, Enabled: true})
	if err != nil || sv.Weight != 100 || !sv.Enabled {
		t.Fatalf("add: %+v %v", sv, err)
	}
	in := ServerInput{Name: "DE 1", BaseURL: "https://de.example.com", Weight: 200, Enabled: true}
	if _, err := svc.UpdateServer(ctx, own, fs, sv.ID, in); err != nil {
		t.Fatalf("rename: %v", err)
	}
	in.Token = tok2
	if _, err := svc.UpdateServer(ctx, own, fs, sv.ID, in); err != nil {
		t.Fatalf("new token: %v", err)
	}
	if fs.tokens[sv.ID] != tok2 {
		t.Fatal("the new token did not reach the provisioner")
	}
	if err := svc.SetInboundAttach(ctx, own, fs, sv.ID, 3, false); err != nil || fs.attach[3] {
		t.Fatalf("inbound: %v %v", err, fs.attach)
	}
	if err := svc.SetInboundAttach(ctx, own, fs, sv.ID, 0, false); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("inbound 0: %v", err)
	}
	if err := svc.DeleteServer(ctx, own, fs, sv.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	adds, updates := auditJSON(t, s, "server.add"), auditJSON(t, s, "server.update")
	if len(adds) != 1 || !strings.Contains(adds[0], `"token": "changed"`) || !strings.Contains(adds[0], `"name": "DE"`) {
		t.Fatalf("server.add: %v", adds)
	}
	if len(updates) != 2 || strings.Contains(updates[0], "token") || !strings.Contains(updates[1], `"token": "changed"`) ||
		!strings.Contains(updates[0], `"weight": 200`) {
		t.Fatalf("server.update: %v", updates)
	}
	if n := auditCount(t, s, "server.delete") + auditCount(t, s, "server.inbound"); n != 2 {
		t.Fatalf("delete and inbound entries: %d", n)
	}
	var all string
	if err := s.DB().QueryRow(ctx, `SELECT string_agg(coalesce(before::text,'') || coalesce(after::text,''), ' ') FROM core.audit_log`).Scan(&all); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(all, tok1) || strings.Contains(all, tok2) || strings.Contains(all, "panel-token") {
		t.Fatalf("a token reached the audit log: %s", all)
	}

	// A server added switched off is saved switched off.
	off, err := svc.AddServer(ctx, own, fs, ServerInput{Name: "Spare", BaseURL: "https://spare.example.com", Token: "t"})
	if err != nil || off.Enabled {
		t.Fatalf("added switched off: %+v %v", off, err)
	}
	// Refusals come back as they are.
	fs.servers[len(fs.servers)-1].Clients = 3
	if err := svc.DeleteServer(ctx, own, fs, off.ID); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("delete with clients: %v", err)
	}
	if err := svc.DeleteServer(ctx, own, fs, "00000000-0000-7000-8000-00000000ffff"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("delete unknown: %v", err)
	}
}

// Only the owner lets BOBRES reach a private address with a new address or
// token; an admin may still rename, reweight or switch off such a server.
func TestPrivateServersAreTheOwners(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	own := owner(t, svc)
	adm := staffMember(t, s, 9201, "admin")
	fs := &fakeServers{}
	priv := ServerInput{Name: "Home", BaseURL: "http://10.0.0.5:2053/p", Token: "t", AllowPrivate: true, Enabled: true}

	if _, err := svc.AddServer(ctx, adm, fs, priv); !errors.Is(err, ErrPrivateOwnerOnly) {
		t.Fatalf("admin adds a private server: %v", err)
	}
	sv, err := svc.AddServer(ctx, own, fs, priv)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := svc.AddServer(ctx, adm, fs, ServerInput{Name: "DE", BaseURL: "https://de.example.com", Token: "t", Enabled: true})
	if err != nil {
		t.Fatalf("admin adds a public server: %v", err)
	}
	keep := ServerInput{Name: "Home 2", BaseURL: "http://10.0.0.5:2053/p/", Weight: 5, AllowPrivate: true}
	if _, err := svc.UpdateServer(ctx, adm, fs, sv.ID, keep); err != nil {
		t.Fatalf("admin renames a private server: %v", err)
	}
	for name, in := range map[string]ServerInput{
		"a new token":   {Name: "Home", BaseURL: "http://10.0.0.5:2053/p", Weight: 5, AllowPrivate: true, Token: "other"},
		"a new address": {Name: "Home", BaseURL: "http://10.0.0.9:2053/p", Weight: 5, AllowPrivate: true},
	} {
		if _, err := svc.UpdateServer(ctx, adm, fs, sv.ID, in); !errors.Is(err, ErrPrivateOwnerOnly) {
			t.Errorf("admin, %s: %v", name, err)
		}
	}
	if _, err := svc.UpdateServer(ctx, adm, fs, pub.ID, ServerInput{Name: "DE", BaseURL: "http://10.0.0.7", Weight: 5, AllowPrivate: true}); !errors.Is(err, ErrPrivateOwnerOnly) {
		t.Fatalf("admin turns allow_private on: %v", err)
	}
	// Tests follow the same rule.
	if _, err := svc.TestServer(ctx, adm, fs, ServerProbe{ID: sv.ID}); err != nil {
		t.Fatalf("admin tests a private server as stored: %v", err)
	}
	for name, p := range map[string]ServerProbe{
		"candidate": {BaseURL: "http://10.0.0.1:5432", Token: "t", AllowPrivate: true},
		"new token": {ID: sv.ID, Token: "guess"},
		"new host":  {ID: sv.ID, BaseURL: "http://172.17.0.1:6379", AllowPrivate: true},
	} {
		if _, err := svc.TestServer(ctx, adm, fs, p); !errors.Is(err, ErrPrivateOwnerOnly) {
			t.Errorf("admin test, %s: %v", name, err)
		}
	}
	if _, err := svc.TestServer(ctx, own, fs, ServerProbe{BaseURL: "http://10.0.0.1:2053", Token: "t", AllowPrivate: true}); err != nil {
		t.Fatalf("owner test: %v", err)
	}
	if _, err := svc.TestServer(ctx, own, fs, ServerProbe{BaseURL: "https://a.example"}); err == nil {
		t.Fatal("a candidate without a token was tested")
	}
	if _, err := svc.TestServer(ctx, own, fs, ServerProbe{ID: "00000000-0000-7000-8000-00000000ffff"}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("test of an unknown server: %v", err)
	}
	// The stored connection is tried with the stored address and switch.
	last := fs.probes[0]
	if last.ID != sv.ID || last.BaseURL != "http://10.0.0.5:2053/p/" || !last.AllowPrivate || last.Token != "" {
		t.Fatalf("probe: %+v", last)
	}
}

// A server added switched off is saved off in one step, so it never takes
// new services, even for a moment, and nothing done afterwards can fail and
// leave it on. A cancelled request is still audited.
func TestNewServerSwitchedOffNeverStaysOnSilently(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	own := owner(t, svc)
	// Nothing switches the server off afterwards or takes it out again.
	busy := fmt.Errorf("provisioner: %w", status.Error(codes.Unavailable, "busy"))
	fs := &fakeServers{failUpdate: busy, failDelete: busy}
	in := ServerInput{Name: "Spare", BaseURL: "https://spare.example.com", Token: "t"}

	sv, err := svc.AddServer(ctx, own, fs, in)
	if err != nil || sv.Enabled || len(fs.servers) != 1 || fs.servers[0].Enabled {
		t.Fatalf("added switched off: %+v %v %+v", sv, err, fs.servers)
	}
	if e := auditJSON(t, s, "server.add"); len(e) != 1 || !strings.Contains(e[0], `"enabled": false`) {
		t.Fatalf("server.add: %v", e)
	}

	// An older provisioner (during an update) saves it switched on anyway:
	// staff are told, and the audit says it is on.
	fs.ignoresEnabled = true
	in.Name = "Spare 2"
	if sv, err := svc.AddServer(ctx, own, fs, in); !errors.Is(err, ErrState) || !strings.Contains(err.Error(), "takes new services") || sv != nil {
		t.Fatalf("older provisioner: %+v %v", sv, err)
	}
	if e := auditJSON(t, s, "server.add"); len(e) != 2 || !strings.Contains(e[1], `"enabled": true`) {
		t.Fatalf("the audit hides that it is on: %v", e)
	}

	fs.ignoresEnabled = false
	cctx, cancel := context.WithCancel(ctx)
	fs.added = cancel // the browser goes away while the panel is tested
	in.Name = "Spare 3"
	sv, err = svc.AddServer(cctx, own, fs, in)
	if err != nil || sv.Enabled || fs.servers[2].Enabled {
		t.Fatalf("cancelled: %+v %v", sv, err)
	}
	if auditCount(t, s, "server.add") != 3 {
		t.Fatal("the cancelled add was not audited")
	}
}

// A server a plan names (on sale or not) is not deleted: the plan's paid
// orders could never be delivered. The refusal names the plans, a few and
// then how many more; out of them, it goes.
func TestServerNamedByPlansIsNotDeleted(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	own := owner(t, svc)
	fs := &fakeServers{}
	de, _ := svc.AddServer(ctx, own, fs, ServerInput{Name: "DE", BaseURL: "https://de.example.com", Token: "t", Enabled: true})
	fr, _ := svc.AddServer(ctx, own, fs, ServerInput{Name: "FR", BaseURL: "https://fr.example.com", Token: "t", Enabled: true})
	days := int32(30)
	plan := func(name map[string]string, enabled bool, servers ...string) *store.Plan {
		t.Helper()
		p, err := svc.UpsertPlan(ctx, own, &store.Plan{NameI18n: name, Kind: "time", DurationDays: &days, Price: 1000,
			Currency: "IRT", Enabled: enabled, ServerIDs: servers})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	onSale := plan(map[string]string{"en": "DE 30d", "fa": "آلمان"}, true, de.ID)
	off := plan(map[string]string{"fa": "آلمان قدیم"}, false, strings.ToUpper(fr.ID), de.ID)
	plan(map[string]string{"en": "FR only"}, true, fr.ID)
	plan(map[string]string{"en": "Any"}, true)

	err := svc.DeleteServer(ctx, own, fs, de.ID)
	if !errors.Is(err, ErrState) || !strings.Contains(err.Error(), `"DE 30d"`) || !strings.Contains(err.Error(), `"آلمان قدیم"`) ||
		strings.Contains(err.Error(), "FR only") || strings.Contains(err.Error(), "Any") {
		t.Fatalf("delete named by plans: %v", err)
	}
	if len(fs.servers) != 2 || auditCount(t, s, "server.delete") != 0 {
		t.Fatalf("deleted anyway: %+v", fs.servers)
	}
	more := []*store.Plan{onSale, off}
	for i := range 3 {
		more = append(more, plan(map[string]string{"en": fmt.Sprintf("DE extra %d", i)}, true, de.ID))
	}
	if err := svc.DeleteServer(ctx, own, fs, de.ID); err == nil || !strings.HasSuffix(err.Error(), "and 2 more): take it out of them first") {
		t.Fatalf("five plans: %v", err)
	}
	for _, p := range more {
		p.ServerIDs = slices.DeleteFunc(p.ServerIDs, func(id string) bool { return id == de.ID })
		if _, err := svc.UpsertPlan(ctx, own, p); err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.DeleteServer(ctx, own, fs, strings.ToUpper(de.ID)); err != nil || len(fs.servers) != 1 {
		t.Fatalf("delete once no plan names it: %v %+v", err, fs.servers)
	}
}

// fakeLinks answers GetLinks from the current subscription address of a
// fake server, or with an error per subscription. during runs inside a call.
type fakeLinks struct {
	fakeProvisioner
	mu     sync.Mutex
	base   string
	errs   map[string]error
	got    []string
	during func(id string)
}

func (f *fakeLinks) GetLinks(_ context.Context, id string) (Links, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.got = append(f.got, id)
	if f.during != nil {
		f.during(id)
	}
	if err := f.errs[id]; err != nil {
		return Links{}, err
	}
	return Links{SubscriptionLink: f.base + id}, nil
}

// linkMark is a service's refresh mark: seconds until it is due (nil: not
// marked) and the fetches the panel did not answer.
func linkMark(t *testing.T, s *store.Store, id string) (*float64, int) {
	t.Helper()
	var (
		due   *float64
		tries int
	)
	if err := s.DB().QueryRow(context.Background(), `SELECT EXTRACT(EPOCH FROM sub_link_stale_at - now())::float8, sub_link_attempts
		FROM core.subscriptions WHERE id = $1`, id).Scan(&due, &tries); err != nil {
		t.Fatal(err)
	}
	return due, tries
}

// dueNow moves a service's mark to now (its wait is over).
func dueNow(t *testing.T, s *store.Store, id string) {
	t.Helper()
	if _, err := s.DB().Exec(context.Background(), `UPDATE core.subscriptions SET sub_link_stale_at = now() WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
}

func storedLink(t *testing.T, s *store.Store, id string) string {
	t.Helper()
	sub, err := s.GetSubscription(context.Background(), s.Conn(), id)
	if err != nil {
		t.Fatal(err)
	}
	return sub.SubLink
}

// subscriptionOn makes a delivered service on a server with a link.
func subscriptionOn(t *testing.T, svc *Service, s *store.Store, tg int64, server, link string) *store.Subscription {
	t.Helper()
	ctx := context.Background()
	u := seedUser(t, s, tg)
	pl := seedPlan(t, s, false, 1000)
	o, _, err := s.CreateOrder(ctx, s.Conn(), &store.Order{UserID: u.ID, PlanID: pl.ID, Type: "new", Status: "active",
		Amount: 1000, Currency: "IRT", IdempotencyKey: fmt.Sprintf("sub-on-%d", tg)})
	if err != nil {
		t.Fatal(err)
	}
	sub, err := s.CreateSubscription(ctx, s.Conn(), &store.Subscription{UserID: u.ID, OrderID: o.ID, ClientEmail: fmt.Sprintf("u%d-x", tg)})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ActivateSubscription(ctx, s.Conn(), sub.ID, server, "x", link, nil, nil); err != nil {
		t.Fatal(err)
	}
	return sub
}

// A new subscription address reaches the stored links of that server's
// services in the background; a rename does not touch them.
func TestServerAddressChangeRefreshesStoredLinks(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	own := owner(t, svc)
	fs := &fakeServers{}
	a, _ := svc.AddServer(ctx, own, fs, ServerInput{Name: "A", BaseURL: "https://a.example", SubBaseURL: "https://old.example/sub/", Token: "t", Enabled: true})
	b, _ := svc.AddServer(ctx, own, fs, ServerInput{Name: "B", BaseURL: "https://b.example", Token: "t", Enabled: true})
	onA := subscriptionOn(t, svc, s, 9301, a.ID, "https://old.example/sub/x1")
	onB := subscriptionOn(t, svc, s, 9302, b.ID, "https://b-link")

	fl := &fakeLinks{base: "https://new.example/sub/"}
	w := svc.NewSubLinkWorker(fl, fs, nil)
	if _, err := svc.UpdateServer(ctx, own, fs, a.ID, ServerInput{Name: "A2", BaseURL: "https://a.example", SubBaseURL: "https://old.example/sub/", Weight: 100, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if n, err := w.RefreshStale(ctx); err != nil || n != 0 {
		t.Fatalf("a rename refreshed links: %d %v", n, err)
	}
	select {
	case <-svc.lkick:
		t.Fatal("a rename woke the link worker")
	default:
	}
	if _, err := svc.UpdateServer(ctx, own, fs, a.ID, ServerInput{Name: "A2", BaseURL: "https://a.example", SubBaseURL: "https://new.example/sub/", Weight: 100, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-svc.lkick:
	default:
		t.Fatal("the link worker was not woken")
	}
	if n, err := w.RefreshStale(ctx); err != nil || n != 1 {
		t.Fatalf("refresh: %d %v", n, err)
	}
	if got, _ := s.GetSubscription(ctx, s.Conn(), onA.ID); got.SubLink != "https://new.example/sub/"+onA.ID {
		t.Fatalf("link on A: %q", got.SubLink)
	}
	if got, _ := s.GetSubscription(ctx, s.Conn(), onB.ID); got.SubLink != "https://b-link" {
		t.Fatalf("link on B changed: %q", got.SubLink)
	}
	if !slices.Equal(fl.got, []string{onA.ID}) {
		t.Fatalf("fetched: %v", fl.got)
	}
}

// Only a provisioner that does not answer pauses the refresh, before a
// fetch or during one, and nothing is counted then; a refusal (the
// provisioner does not know the service) keeps that service's link and
// moves on.
func TestSubLinkRefreshFailures(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	const server = "00000000-0000-7000-8000-0000000000aa"
	gone := subscriptionOn(t, svc, s, 9311, server, "https://old/gone")
	live := subscriptionOn(t, svc, s, 9312, server, "https://old/live")
	if _, err := s.MarkSubLinksStale(ctx, s.Conn(), server); err != nil {
		t.Fatal(err)
	}
	refused := fmt.Errorf("provisioner.ListServers: %w", status.Error(codes.Unavailable, "connection error"))
	fs := &fakeServers{listErr: refused}
	fl := &fakeLinks{base: "https://new/", errs: map[string]error{
		gone.ID: fmt.Errorf("provisioner.GetLinks: %w", status.Error(codes.NotFound, "client not provisioned")),
	}}
	w := svc.NewSubLinkWorker(fl, fs, nil)
	before, _ := s.StaleSubLinks(ctx, s.Conn(), 10)
	if _, err := w.RefreshStale(ctx); err == nil {
		t.Fatal("a provisioner that did not answer went unnoticed")
	}
	if after, _ := s.StaleSubLinks(ctx, s.Conn(), 10); !slices.Equal(after, before) || len(fl.got) != 0 {
		t.Fatalf("marks changed although the provisioner did not answer: %+v, fetched %v", after, fl.got)
	}

	// It goes away during a fetch: that one is not counted against the service.
	fs.setListErr(nil)
	fl.errs[live.ID] = fmt.Errorf("provisioner.GetLinks: %w", status.Error(codes.Unavailable, "connection error"))
	fl.during = func(id string) {
		if id == live.ID {
			fs.setListErr(refused)
		}
	}
	if _, err := w.RefreshStale(ctx); err == nil {
		t.Fatal("a provisioner that went away went unnoticed")
	}
	if due, tries := linkMark(t, s, live.ID); due == nil || *due > 0 || tries != 0 {
		t.Fatalf("live after the provisioner went away: due in %v s, %d attempts", due, tries)
	}

	fs.setListErr(nil)
	fl.during = nil
	delete(fl.errs, live.ID)
	for i := 0; i < 2; i++ {
		if _, err := w.RefreshStale(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if left, _ := s.StaleSubLinks(ctx, s.Conn(), 10); len(left) != 0 {
		t.Fatalf("left: %+v", left)
	}
	if got := storedLink(t, s, gone.ID); got != "https://old/gone" {
		t.Fatalf("a service the provisioner does not know lost its link: %q", got)
	}
	if got := storedLink(t, s, live.ID); got != "https://new/"+live.ID {
		t.Fatalf("live link: %q", got)
	}

	// Run picks marks up when woken and stops with its context.
	if _, err := s.MarkSubLinksStale(ctx, s.Conn(), server); err != nil {
		t.Fatal(err)
	}
	fl.base = "https://newer/"
	w.Interval = time.Hour
	rctx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- w.Run(rctx) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		got := storedLink(t, s, live.ID)
		if got == "https://newer/"+live.ID {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Run did not refresh: %q", got)
		}
		svc.kickSubLinks()
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// One service whose panel does not answer (the panel is down, or the client
// was deleted there) never holds up the rest: it waits, longer each time,
// while the others are refreshed, and after MaxAttempts it keeps its link.
// The links of a switched-on server whose last health check failed wait
// without asking its panel or counting, unless the provisioner builds them
// without the panel.
func TestSubLinkRefreshNeverStallsOnOnePanel(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	const srvA, srvB = "00000000-0000-7000-8000-0000000000a1", "00000000-0000-7000-8000-0000000000b2"
	fs := &fakeServers{servers: []Server{
		{ID: srvA, Enabled: true, Healthy: true, CheckedAt: 1}, // its links come from its panel
		{ID: srvB, Enabled: true, Healthy: true, CheckedAt: 1, SubBaseURL: "https://sub.b.example/sub/"},
	}}
	onA := subscriptionOn(t, svc, s, 9321, srvA, "https://old/a")
	onB := subscriptionOn(t, svc, s, 9322, srvB, "https://old/b")
	if _, err := s.MarkSubLinksStale(ctx, s.Conn(), srvA); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	if _, err := s.MarkSubLinksStale(ctx, s.Conn(), srvB); err != nil { // after A's: second in line
		t.Fatal(err)
	}
	fl := &fakeLinks{base: "https://new/", errs: map[string]error{
		onA.ID: fmt.Errorf("provisioner.GetLinks: %w", status.Error(codes.Unavailable, "panel links failed")),
	}}
	w := svc.NewSubLinkWorker(fl, fs, nil)
	w.Batch, w.MaxAttempts = 1, 3

	if n, err := w.RefreshStale(ctx); n != 1 || err != nil {
		t.Fatalf("first: %d %v", n, err)
	}
	if due, tries := linkMark(t, s, onA.ID); due == nil || *due < 50 || *due > 70 || tries != 1 {
		t.Fatalf("A after one failure: due in %v s, %d attempts", due, tries)
	}
	if n, err := w.RefreshStale(ctx); n != 1 || err != nil {
		t.Fatalf("second: %d %v", n, err)
	}
	if got := storedLink(t, s, onB.ID); got != "https://new/"+onB.ID {
		t.Fatalf("B waited behind A: %q", got)
	}
	if n, err := w.RefreshStale(ctx); n != 0 || err != nil {
		t.Fatalf("nothing due: %d %v", n, err)
	}
	// The wait doubles; the last attempt keeps the stored link.
	dueNow(t, s, onA.ID)
	if _, err := w.RefreshStale(ctx); err != nil {
		t.Fatal(err)
	}
	if due, tries := linkMark(t, s, onA.ID); due == nil || *due < 110 || *due > 130 || tries != 2 {
		t.Fatalf("A after two failures: due in %v s, %d attempts", due, tries)
	}
	dueNow(t, s, onA.ID)
	if _, err := w.RefreshStale(ctx); err != nil {
		t.Fatal(err)
	}
	if due, tries := linkMark(t, s, onA.ID); due != nil || tries != 0 || storedLink(t, s, onA.ID) != "https://old/a" {
		t.Fatalf("A after the last attempt: due in %v s, %d attempts, link %q", due, tries, storedLink(t, s, onA.ID))
	}
	if !slices.Equal(fl.got, []string{onA.ID, onB.ID, onA.ID, onA.ID}) {
		t.Fatalf("fetched: %v", fl.got)
	}

	// A's panel failed its health check: its links wait, uncounted, unasked.
	fs.servers[0].Healthy = false
	fl.got = nil
	if _, err := s.MarkSubLinksStale(ctx, s.Conn(), srvA); err != nil {
		t.Fatal(err)
	}
	if n, err := w.RefreshStale(ctx); n != 1 || err != nil || len(fl.got) != 0 {
		t.Fatalf("down panel: %d %v, fetched %v", n, err, fl.got)
	}
	if due, tries := linkMark(t, s, onA.ID); due == nil || *due < 290 || *due > 310 || tries != 0 {
		t.Fatalf("A while its panel is down: due in %v s, %d attempts", due, tries)
	}
	// B's links need no panel; a switched-off server is not checked any more,
	// so its old result does not hold its links back.
	fs.servers[1].Healthy = false
	fs.servers[0].Enabled = false
	delete(fl.errs, onA.ID)
	if _, err := s.MarkSubLinksStale(ctx, s.Conn(), srvB); err != nil {
		t.Fatal(err)
	}
	dueNow(t, s, onA.ID)
	for i := 0; i < 2; i++ {
		if _, err := w.RefreshStale(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if storedLink(t, s, onA.ID) != "https://new/"+onA.ID || len(fl.got) != 2 {
		t.Fatalf("A switched off: %q, fetched %v", storedLink(t, s, onA.ID), fl.got)
	}
	if left, _ := s.StaleSubLinks(ctx, s.Conn(), 10); len(left) != 0 {
		t.Fatalf("left: %+v", left)
	}

	// The waits: from Backoff, doubled, never above MaxBackoff.
	for n, want := range map[int]time.Duration{1: time.Minute, 2: 2 * time.Minute, 3: 4 * time.Minute, 7: time.Hour, 1000: time.Hour} {
		if got := svc.NewSubLinkWorker(fl, fs, nil).retryWait(n); got != want {
			t.Errorf("wait after %d failures: %v, want %v", n, got, want)
		}
	}
}

// Plans name their servers by id (each once); provisioning sends them as
// the candidates, and a plan saved without a list keeps its servers.
func TestPlanServersReachProvisioning(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	own := owner(t, svc)
	days := int32(30)
	traffic := int64(10 << 30)
	const a, b = "00000000-0000-7000-8000-0000000000a1", "00000000-0000-7000-8000-0000000000b2"
	p, err := svc.UpsertPlan(ctx, own, &store.Plan{NameI18n: map[string]string{"en": "M"}, Kind: "both", DurationDays: &days,
		TrafficBytes: &traffic, Price: 1000, Currency: "IRT", Enabled: true, ServerIDs: []string{strings.ToUpper(b), a, " " + b}})
	if err != nil || !slices.Equal(p.ServerIDs, []string{a, b}) {
		t.Fatalf("plan: %v %v", err, p.ServerIDs)
	}
	for _, bad := range [][]string{{"default"}, {a, "1"}} {
		q := *p
		q.ServerIDs = bad
		var fe *FieldError
		if _, err := svc.UpsertPlan(ctx, own, &q); !errors.As(err, &fe) || fe.Field != "server_ids" {
			t.Errorf("%v: %v", bad, err)
		}
	}
	// The bot's edits carry no servers: they stay.
	q := *p
	q.ServerIDs, q.Price = nil, 900
	if got, err := svc.UpsertPlan(ctx, own, &q); err != nil || !slices.Equal(got.ServerIDs, []string{a, b}) {
		t.Fatalf("kept: %v %v", err, got.ServerIDs)
	}
	if e := auditJSON(t, s, "plan.upsert"); len(e) != 2 || !strings.Contains(e[1], a) {
		t.Fatalf("plan.upsert audit: %v", e)
	}

	_, _ = paidWalletOrder(t, svc, s, 9401, p)
	fp := &fakeProvisioner{}
	if worked, err := svc.NewProvisionWorker(fp, nil).ProvisionNext(ctx); !worked || err != nil {
		t.Fatalf("provision: %v %v", worked, err)
	}
	if !slices.Equal(fp.candidates, []string{a, b}) {
		t.Fatalf("candidates: %v", fp.candidates)
	}
	// Back to any server: no candidates.
	q.ServerIDs = []string{}
	if _, err := svc.UpsertPlan(ctx, own, &q); err != nil {
		t.Fatal(err)
	}
	_, _ = paidWalletOrder(t, svc, s, 9402, p)
	if worked, err := svc.NewProvisionWorker(fp, nil).ProvisionNext(ctx); !worked || err != nil {
		t.Fatalf("provision: %v %v", worked, err)
	}
	if len(fp.candidates) != 0 {
		t.Fatalf("candidates of an any-server plan: %v", fp.candidates)
	}
}
