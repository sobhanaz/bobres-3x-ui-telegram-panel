package domain

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/license"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/version"
)

// fakeLicenseServer is the license server behind the bot: it keeps what it
// was sent and answers answer (or err); during, when set, runs before it
// answers.
type fakeLicenseServer struct {
	mu          sync.Mutex
	activations []LicenseActivation
	refreshes   []string
	answer      string
	err         error
	during      func()
}

func (f *fakeLicenseServer) ActivateLicense(_ context.Context, a LicenseActivation) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.activations = append(f.activations, a)
	return f.answer, f.err
}

func (f *fakeLicenseServer) RefreshLicense(_ context.Context, token string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refreshes = append(f.refreshes, token)
	if f.during != nil {
		f.during()
	}
	return f.answer, f.err
}

func (f *fakeLicenseServer) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.activations) + len(f.refreshes)
}

const licKey = "BOBRES-7K2Q-9XMV-4HTD"

func TestActivateLicense(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	own := owner(t, svc)
	now := time.Now().UTC()
	v := licensed(t, svc, now)
	missing, err := svc.License(ctx)
	if err != nil {
		t.Fatal(err)
	}
	id := missing.InstallID
	bound := func(t *license.Token) { t.InstallID = id }
	f := &fakeLicenseServer{answer: v.token(bound)}

	if _, err := svc.ActivateLicense(ctx, f, own, licKey, " "); !errors.Is(err, ErrInvalid) {
		t.Fatalf("no reason: %v", err)
	}
	for _, key := range []string{"", "  ", "BOBRES 7K2Q", "BOBRES-7K2Q\x00", "BOBRES-۷K2Q", strings.Repeat("A", maxLicenseKey+1)} {
		if _, err := svc.ActivateLicense(ctx, f, own, key, "bought it"); fieldOf(err) != "key" {
			t.Errorf("key %q: %v", key, err)
		}
	}
	var se *LicenseServerError
	if _, err := svc.ActivateLicense(ctx, nil, own, licKey, "bought it"); !errors.As(err, &se) || se.Code != LicenseNotConfigured || se.Status != 503 {
		t.Fatalf("no bot: %v", err)
	}
	svc.cfg.License.Domain = ""
	if _, err := svc.ActivateLicense(ctx, f, own, licKey, "bought it"); !errors.Is(err, ErrState) {
		t.Fatalf("no domain: %v", err)
	}
	svc.cfg.License.Domain = "Panel.Example.com"
	if f.calls() != 0 {
		t.Fatalf("a refused request reached the license server: %+v", f.activations)
	}

	// The server's refusal comes back as it is; nothing is stored.
	f.err = &LicenseServerError{Status: 403, Code: "invalid_key", Message: "this license key is not valid"}
	if _, err := svc.ActivateLicense(ctx, f, own, licKey, "bought it"); !errors.As(err, &se) || se.Code != "invalid_key" || !se.Refused() {
		t.Fatalf("refused: %v", err)
	}
	f.err = nil
	// A token that does not verify here is not installed either.
	var le *LicenseError
	for name, c := range map[string]struct {
		answer, reason string
	}{
		"another domain":  {v.token(func(t *license.Token) { bound(t); t.Domain = "shop.example.org" }), "domain"},
		"another install": {v.token(func(t *license.Token) { t.InstallID = "0b5c0000-0000-0000-0000-000000000001" }), "install_id"},
		"expired":         {v.token(func(t *license.Token) { bound(t); t.ExpiresAt = now.Add(-60 * 24 * time.Hour) }), "expired"},
		"garbage":         {"not-a-token", "malformed"},
	} {
		f.answer = c.answer
		if _, err := svc.ActivateLicense(ctx, f, own, licKey, "bought it"); !errors.As(err, &le) || le.Reason != c.reason {
			t.Errorf("%s: %v", name, err)
		}
	}
	if st, _ := svc.License(ctx); st.State != LicenseMissing {
		t.Fatalf("a refused token was stored: %+v", st)
	}

	raw := v.token(bound)
	f.answer = "\n" + raw + "\n"
	st, err := svc.ActivateLicense(store.WithSource(ctx, store.SourceDashboard), f, own, "  "+licKey+"\n", "bought the standard plan")
	if err != nil || st.State != LicenseActive || st.Source != store.LicenseActivation || st.RefreshedAt != nil ||
		st.UpdatedBy == nil || *st.UpdatedBy != own.ID {
		t.Fatalf("activate: %+v %v", st, err)
	}
	last := f.activations[len(f.activations)-1]
	if last != (LicenseActivation{Key: licKey, InstallID: id, Domain: licDomain, Version: version.Version}) {
		t.Fatalf("sent: %+v", last)
	}
	entries := auditJSON(t, s, "license.set")
	if len(entries) != 1 || !strings.Contains(entries[0], `"source": "activation"`) || !strings.Contains(entries[0], `"license_id": "lic_1"`) {
		t.Fatalf("audit: %v", entries)
	}
	if strings.Contains(entries[0], licKey) || strings.Contains(entries[0], strings.Split(raw, ".")[1]) {
		t.Fatalf("the key or the token reached the audit: %s", entries[0])
	}
}

// refresher is a refresh through f whose clock is at *at.
func refresher(svc *Service, f *fakeLicenseServer, at *time.Time) *LicenseRefresher {
	w := svc.NewLicenseRefresher(f, nil)
	w.now = func() time.Time { return *at }
	return w
}

func TestLicenseRefresh(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	own := owner(t, svc)
	now := time.Now().UTC()
	v := licensed(t, svc, now)
	st, err := svc.License(ctx)
	if err != nil {
		t.Fatal(err)
	}
	id := st.InstallID
	// Each refresh answers a token issued an hour after the one before.
	issued := now.Add(-90 * 24 * time.Hour)
	next := func(edit func(*license.Token)) string {
		issued = issued.Add(time.Hour)
		at := issued
		return v.token(func(t *license.Token) {
			t.InstallID, t.IssuedAt = id, at
			if edit != nil {
				edit(t)
			}
		})
	}
	f := &fakeLicenseServer{}
	clock := now
	w := refresher(svc, f, &clock)

	// Nothing installed, or a token pasted by hand: nothing is asked.
	if ok, err := w.Step(ctx); ok || err != nil {
		t.Fatalf("no license: %v %v", ok, err)
	}
	if _, err := svc.SetLicense(ctx, own, next(nil), "pasted"); err != nil {
		t.Fatal(err)
	}
	clock = now.Add(48 * time.Hour)
	if ok, err := w.Step(ctx); ok || err != nil || f.calls() != 0 {
		t.Fatalf("a pasted license: %v %v %d calls", ok, err, f.calls())
	}

	// An activated one (from the CLI here) is renewed a day after it came.
	activated := next(nil)
	if _, err := svc.SetActivatedLicense(store.WithSource(ctx, store.SourceCLI), nil, activated, "activated"); err != nil {
		t.Fatal(err)
	}
	clock = time.Now().Add(23 * time.Hour)
	if ok, err := w.Step(ctx); ok || err != nil || f.calls() != 0 {
		t.Fatalf("not due yet: %v %v %d calls", ok, err, f.calls())
	}
	clock = time.Now().Add(25 * time.Hour)
	renewal := next(nil)
	f.answer = renewal
	ok, err := w.Step(ctx)
	if !ok || err != nil || len(f.refreshes) != 1 || f.refreshes[0] != activated {
		t.Fatalf("refresh: %v %v %v", ok, err, f.refreshes)
	}
	row, err := s.GetLicense(ctx, s.Conn())
	if err != nil || row.Token != renewal || row.Source != store.LicenseRefresh || row.RefreshedAt == nil || row.UpdatedBy != nil {
		t.Fatalf("after the refresh: %+v %v", row, err)
	}
	// The same grant with a later expiry is not audited.
	if n := auditCount(t, s, "license.refresh"); n != 0 {
		t.Fatalf("an unchanged grant was audited %d times", n)
	}
	// Refreshed just now: not due again for a day.
	clock = time.Now().Add(time.Hour)
	if ok, err := w.Step(ctx); ok || err != nil || len(f.refreshes) != 1 {
		t.Fatalf("again at once: %v %v %v", ok, err, f.refreshes)
	}

	// An answer that may not replace it changes nothing, and the next try
	// waits three hours.
	clock = time.Now().Add(26 * time.Hour)
	older := v.token(func(t *license.Token) { t.InstallID, t.IssuedAt = id, now.Add(-90*24*time.Hour) })
	for name, c := range map[string]struct {
		answer string
		err    error
		why    string
	}{
		"older":           {older, nil, "not newer"},
		"the same":        {renewal, nil, "not newer"},
		"another license": {next(func(t *license.Token) { t.LicenseID = "lic_2" }), nil, "another license (lic_2)"},
		"another domain":  {next(func(t *license.Token) { t.Domain = "shop.example.org" }), nil, "(domain)"},
		"another install": {next(func(t *license.Token) { t.InstallID = "0b5c0000-0000-0000-0000-000000000001" }), nil, "(install_id)"},
		"expired":         {next(func(t *license.Token) { t.ExpiresAt = now.Add(-60 * 24 * time.Hour) }), nil, "expired"},
		"garbage":         {"not-a-token", nil, "(malformed)"},
		"empty":           {"", nil, "not a license token"},
		"a refusal":       {"", &LicenseServerError{Status: 403, Code: "revoked", Message: "revoked"}, "revoked"},
		"no answer":       {"", &LicenseServerError{Status: 502, Code: LicenseUnreachable, Message: "the bot did not answer"}, "unreachable"},
	} {
		w.wait = time.Time{}
		f.answer, f.err = c.answer, c.err
		calls := f.calls()
		if ok, err := w.Step(ctx); ok || err == nil || !strings.Contains(err.Error(), c.why) {
			t.Errorf("%s: %v %v", name, ok, err)
		}
		if row, _ := s.GetLicense(ctx, s.Conn()); row.Token != renewal {
			t.Errorf("%s: the token changed", name)
		}
		if ok, err := w.Step(ctx); ok || err != nil || f.calls() != calls+1 {
			t.Errorf("%s: asked again before three hours: %v %v", name, ok, err)
		}
		if err := ctx.Err(); err != nil {
			t.Fatal(err)
		}
	}
	f.err = nil

	// A new tier or new entitlements are audited, by the system, without
	// the token.
	w.wait = time.Time{}
	pro := next(func(t *license.Token) {
		t.Tier = license.TierPro
		t.Entitlements.MaxServers = 9
	})
	f.answer = pro
	if ok, err := w.Step(ctx); !ok || err != nil {
		t.Fatalf("a new grant: %v %v", ok, err)
	}
	entries := auditJSON(t, s, "license.refresh")
	if len(entries) != 1 || !strings.Contains(entries[0], `"tier": "standard"`) || !strings.Contains(entries[0], `"tier": "pro"`) ||
		!strings.Contains(entries[0], `"max_servers": 9`) || strings.Contains(entries[0], strings.Split(pro, ".")[1]) {
		t.Fatalf("audit: %v", entries)
	}
	var source string
	var actorID *string
	if err := s.DB().QueryRow(ctx, `SELECT source, actor_id::text FROM core.audit_log WHERE action = 'license.refresh'`).
		Scan(&source, &actorID); err != nil || source != store.SourceSystem || actorID != nil {
		t.Fatalf("audit source: %q %v %v", source, actorID, err)
	}

	// A token installed while the server answered (the owner activated
	// again) stays.
	clock = time.Now().Add(50 * time.Hour)
	meanwhile := next(nil)
	f.answer = next(nil)
	f.during = func() {
		if err := s.SetLicense(ctx, s.Conn(), meanwhile, &own.ID, store.LicenseActivation); err != nil {
			t.Error(err)
		}
	}
	if ok, err := w.Step(ctx); ok || err != nil {
		t.Fatalf("replaced meanwhile: %v %v", ok, err)
	}
	f.during = nil
	if row, _ := s.GetLicense(ctx, s.Conn()); row.Token != meanwhile || row.Source != store.LicenseActivation || row.RefreshedAt != nil {
		t.Fatalf("the token installed meanwhile was overwritten: %+v", row)
	}

	// A token that no longer verifies here (the domain changed) is sent
	// nowhere.
	svc.cfg.License.Domain = "shop.example.org"
	calls := f.calls()
	if ok, err := w.Step(ctx); ok || err != nil || f.calls() != calls {
		t.Fatalf("another domain now: %v %v", ok, err)
	}
}

// Run waits, then looks on its ticker, and stops with its context.
func TestLicenseRefresherRun(t *testing.T) {
	svc, _ := testService(t)
	w := svc.NewLicenseRefresher(&fakeLicenseServer{}, nil)
	w.first, w.every = time.Millisecond, time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := w.Run(ctx); err != nil {
		t.Fatal(err)
	}
}
