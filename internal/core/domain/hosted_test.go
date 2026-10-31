package domain

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/hosted"
)

const (
	testHostedToken  = "bm_fakeMetricsTokenFakeMetricsTokenFake0123" //nolint:gosec // made up //gitleaks:allow
	testHostedToken2 = "bm_secondFakeTokenSecondFakeTokenSecond456"  //gitleaks:allow
)

// fakeStores is the bot's way to the hosted stores: it answers per domain, and records the tokens it was given.
type fakeStores struct {
	mu     sync.Mutex
	status map[string]int
	body   map[string]string
	err    error
	tokens []string
}

func (f *fakeStores) FetchHostedMetrics(_ context.Context, domain, token string) (int, []byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tokens = append(f.tokens, token)
	if f.err != nil {
		return 0, nil, f.err
	}
	st, ok := f.status[domain]
	if !ok {
		st = 200
	}
	return st, []byte(f.body[domain]), nil
}

const storeMetrics = "bobres_build_info{version=\"0.20.1\"} 1\nbobres_service_up{service=\"core\"} 1\nbobres_service_up{service=\"bot\"} 1\n" +
	"bobres_orders_paid{window=\"24h\"} 7\nbobres_license_days_left{basis=\"license\"} 30\n"

func newHostedEnv(t *testing.T) (*vendorEnv, *fakeStores) {
	t.Helper()
	e := newVendorEnv(t)
	f := &fakeStores{status: map[string]int{}, body: map[string]string{"shop.example.com": storeMetrics}}
	e.svc.SetHostedTransport(f)
	return e, f
}

func TestHostedRegisterAndRead(t *testing.T) {
	e, f := newHostedEnv(t)
	ctx := context.Background()
	code, err := hosted.Registration{Customer: "Acme VPN", Domain: "shop.example.com", Version: "0.20.0", Channel: "beta",
		MetricsToken: testHostedToken}.Encode()
	if err != nil {
		t.Fatal(err)
	}
	h, err := e.svc.AddHostedStore(ctx, e.owner, HostedInput{Registration: code, Notes: "pilot 1"})
	if err != nil {
		t.Fatal(err)
	}
	if h.Customer != "Acme VPN" || h.Domain != "shop.example.com" || h.Version != "0.20.0" || h.Channel != "beta" || h.Notes != "pilot 1" ||
		h.MetricsTokenEnc == nil {
		t.Fatalf("store: %+v", h)
	}
	// The token is stored encrypted, never as it is, and not in the audit.
	var raw []byte
	if err := e.s.DB().QueryRow(ctx, `SELECT metrics_token_enc FROM core.hosted_stores WHERE id = $1`, h.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), testHostedToken) {
		t.Fatal("the metrics token is stored in clear")
	}
	var audit string
	if err := e.s.DB().QueryRow(ctx, `SELECT after::text FROM core.audit_log WHERE action = 'hosted.add'`).Scan(&audit); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(audit, testHostedToken) || !strings.Contains(audit, `"metrics_token": true`) {
		t.Fatalf("audit: %s", audit)
	}
	// The same domain twice is refused.
	if _, err := e.svc.AddHostedStore(ctx, e.owner, HostedInput{Customer: "Other", Domain: "SHOP.example.com"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate domain: %v", err)
	}
	// Reading it: up, with the live version and figures; the bot got the decrypted token.
	got, err := e.svc.CheckHostedStore(ctx, h.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Up == nil || !*got.Up || got.LastVersion != "0.20.1" || got.CheckedAt == nil || got.LastError != "" {
		t.Fatalf("check: %+v", got)
	}
	var fig hosted.Figures
	if err := json.Unmarshal(got.Figures, &fig); err != nil || fig.ServicesUp != 2 || fig.OrdersPaid24h == nil || *fig.OrdersPaid24h != 7 {
		t.Fatalf("figures: %s %v", got.Figures, err)
	}
	if len(f.tokens) != 1 || f.tokens[0] != testHostedToken {
		t.Fatalf("token given to the bot: %v", f.tokens)
	}
	// A refused token, a store that is off and an unreachable one are down, with the reason; the last version is kept.
	for status, want := range map[int]string{401: "refused the metrics token", 404: "metrics is off", 502: "answered 502"} {
		f.status["shop.example.com"] = status
		got, err := e.svc.CheckHostedStore(ctx, h.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Up == nil || *got.Up || !strings.Contains(got.LastError, want) || got.LastVersion != "0.20.1" {
			t.Errorf("%d: %+v", status, got)
		}
	}
	f.err = errors.New("the store did not answer")
	if got, _ := e.svc.CheckHostedStore(ctx, h.ID); got.Up == nil || *got.Up || got.LastError != "the store did not answer" {
		t.Fatalf("unreachable: %+v", got)
	}
	f.err, f.status["shop.example.com"], f.body["shop.example.com"] = nil, 200, "<html>login</html>"
	if got, _ := e.svc.CheckHostedStore(ctx, h.ID); got.Up == nil || *got.Up || !strings.Contains(got.LastError, "not a BOBRES") {
		t.Fatalf("not a store: %+v", got)
	}
}

func TestHostedEditTokenAndDelete(t *testing.T) {
	e, f := newHostedEnv(t)
	ctx := context.Background()
	h, err := e.svc.AddHostedStore(ctx, e.owner, HostedInput{Customer: "Acme", Domain: "shop.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	// Without a token it is not read, and says why.
	got, _ := e.svc.CheckHostedStore(ctx, h.ID)
	if got.Up == nil || *got.Up || !strings.Contains(got.LastError, "no metrics token") || len(f.tokens) != 0 {
		t.Fatalf("no token: %+v", got)
	}
	n, err := e.svc.CheckHostedStores(ctx)
	if err != nil || n != 0 {
		t.Fatalf("worker without tokens: %d %v", n, err)
	}
	up, err := e.svc.UpdateHostedStore(ctx, e.owner, h.ID, HostedInput{Customer: "Acme VPN", Version: "0.20.0", LicenseID: "lic_abc",
		Notes: "line one\nline two", MetricsToken: testHostedToken2})
	if err != nil {
		t.Fatal(err)
	}
	if up.Customer != "Acme VPN" || up.LicenseID != "lic_abc" || up.Domain != "shop.example.com" || up.MetricsTokenEnc == nil || up.Channel != "stable" {
		t.Fatalf("update: %+v", up)
	}
	if n, err := e.svc.CheckHostedStores(ctx); err != nil || n != 1 || f.tokens[0] != testHostedToken2 {
		t.Fatalf("worker: %d %v %v", n, err, f.tokens)
	}
	// Bad fields are refused.
	for _, in := range []HostedInput{{LicenseID: "nope"}, {Channel: "nightly"}, {Notes: strings.Repeat("x", 2001)}, {MetricsToken: "short"}} {
		if _, err := e.svc.UpdateHostedStore(ctx, e.owner, h.ID, in); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: %v", in, err)
		}
	}
	cleared, err := e.svc.UpdateHostedStore(ctx, e.owner, h.ID, HostedInput{ClearToken: true})
	if err != nil || cleared.MetricsTokenEnc != nil {
		t.Fatalf("clear: %+v %v", cleared, err)
	}
	if err := e.svc.DeleteHostedStore(ctx, e.owner, h.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CheckHostedStore(ctx, h.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
	var n2 int
	if err := e.s.DB().QueryRow(ctx, `SELECT count(*) FROM core.audit_log WHERE action LIKE 'hosted.%'`).Scan(&n2); err != nil || n2 != 4 {
		t.Fatalf("audits: %d %v", n2, err)
	}
}

// The "Hosted" license of a store: a pro license made for it, its id recorded; the key comes back once.
func TestHostedLicense(t *testing.T) {
	e, _ := newHostedEnv(t)
	ctx := context.Background()
	h, err := e.svc.AddHostedStore(ctx, e.owner, HostedInput{Customer: "Acme", Domain: "shop.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	info, got, err := e.svc.CreateHostedLicense(ctx, e.owner, h.ID, 12, "req-host-1")
	if err != nil {
		t.Fatal(err)
	}
	if info.Tier != "pro" || info.Key == "" || got.LicenseID != info.ID || info.Customer != "Hosted: Acme (shop.example.com)" {
		t.Fatalf("license: %+v store: %+v", info, got)
	}
	if _, _, err := e.svc.CreateHostedLicense(ctx, e.owner, h.ID, 12, "req-host-2"); !errors.Is(err, ErrState) {
		t.Fatalf("a second license: %v", err)
	}
	if _, _, err := e.svc.CreateHostedLicense(ctx, e.owner, h.ID, 0, "req-host-3"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("0 months: %v", err)
	}
}

// Without the vendor's own features none of it answers.
func TestHostedNeedsPrivateFeatures(t *testing.T) {
	svc, s := testService(t)
	off := false
	svc.cfg.PrivateFeaturesOverride = &off
	owner := staffMember(t, s, 9600, "owner")
	ctx := context.Background()
	if _, err := svc.AddHostedStore(ctx, owner, HostedInput{Customer: "A", Domain: "a.example.com"}); !errors.Is(err, ErrState) {
		t.Fatalf("add: %v", err)
	}
	if _, err := svc.ListHostedStores(ctx); !errors.Is(err, ErrState) {
		t.Fatalf("list: %v", err)
	}
	if _, _, err := svc.ListAffiliates(ctx, store.Page{}); !errors.Is(err, ErrState) {
		t.Fatalf("affiliates: %v", err)
	}
	if n, err := svc.CheckHostedStores(ctx); n != 0 || err != nil {
		t.Fatalf("worker: %d %v", n, err)
	}
}
