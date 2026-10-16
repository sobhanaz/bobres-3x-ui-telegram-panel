package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/license"
)

var checkNow = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

type fakeServers struct {
	list *domain.ServerList
	err  error
}

func (f fakeServers) ListServers(context.Context) (*domain.ServerList, error) { return f.list, f.err }

func healthyChecker() *coreChecker {
	return &coreChecker{
		pingDB:    func(context.Context) error { return nil },
		pingRedis: func(context.Context) error { return nil },
		effective: func(context.Context) (*domain.EffectiveLicense, error) {
			return &domain.EffectiveLicense{Effective: license.Effective{Basis: license.BasisTrial, Tier: license.TierImplicitTrial,
				TrialEndsAt: checkNow.Add(20 * 24 * time.Hour)}}, nil
		},
		servers: fakeServers{list: &domain.ServerList{Servers: []domain.Server{{Name: "de1", Enabled: true, Healthy: true, CheckedAt: 1}}}},
		alerts:  func(context.Context) ([]*store.AlertState, error) { return nil, nil },
		now:     func() time.Time { return checkNow },
	}
}

func byName(rs []checkResult) map[string]checkResult {
	m := map[string]checkResult{}
	for _, r := range rs {
		m[r.Name] = r
	}
	return m
}

func TestCoreChecksHealthy(t *testing.T) {
	rs := healthyChecker().run(context.Background())
	if len(rs) != 5 {
		t.Fatalf("%+v", rs)
	}
	for _, r := range rs {
		if r.Status != checkPass {
			t.Errorf("%+v", r)
		}
	}
	if got := byName(rs)["License"].Detail; got != "trial, 20 days left" {
		t.Errorf("license: %q", got)
	}
}

func TestCoreChecksFailing(t *testing.T) {
	c := healthyChecker()
	c.pingDB = func(context.Context) error { return errors.New("connection refused\nsecond line") }
	c.pingRedis = func(context.Context) error { return errors.New("dial redis://:hunter2@redis:6379: refused") }
	c.servers = fakeServers{list: &domain.ServerList{Servers: []domain.Server{
		{Name: "de1", Enabled: true, Healthy: true, CheckedAt: 1},
		{Name: "us1", Enabled: true, Healthy: false, CheckedAt: 1, HealthError: "login failed"},
		{Name: "off", Enabled: false},
	}}}
	c.alerts = func(context.Context) ([]*store.AlertState, error) {
		return []*store.AlertState{{Key: "server.us1", Detail: "login failed"}, {Key: "disk"}}, nil
	}
	r := byName(c.run(context.Background()))
	if r["Database"].Status != checkFail || strings.Contains(r["Database"].Detail, "second line") {
		t.Errorf("db: %+v", r["Database"])
	}
	if r["Redis"].Status != checkFail || strings.Contains(r["Redis"].Detail, "hunter2") {
		t.Errorf("redis must not show its password: %+v", r["Redis"])
	}
	if s := r["3x-ui servers"]; s.Status != checkFail || !strings.Contains(s.Detail, "1 of 2 healthy") || !strings.Contains(s.Detail, "us1: login failed") || strings.Contains(s.Detail, "off") {
		t.Errorf("servers: %+v", s)
	}
	if a := r["Watchdog"]; a.Status != checkWarn || !strings.Contains(a.Detail, "2 open") || !strings.Contains(a.Detail, "server.us1 (login failed)") {
		t.Errorf("alerts: %+v", a)
	}
}

func TestCoreServerChecks(t *testing.T) {
	c := healthyChecker()
	c.servers = nil
	if r := c.serverResults(context.Background())[0]; r.Status != checkSkip {
		t.Errorf("no client: %+v", r)
	}
	c.servers = fakeServers{err: errors.New("unavailable")}
	if r := c.serverResults(context.Background())[0]; r.Status != checkWarn || !strings.Contains(r.Detail, "bobres logs provisioner") {
		t.Errorf("unreachable: %+v", r)
	}
	c.servers = fakeServers{list: &domain.ServerList{}}
	if r := c.serverResults(context.Background())[0]; r.Status != checkWarn || !strings.Contains(r.Detail, "no server") {
		t.Errorf("none: %+v", r)
	}
	// a server never checked is not down
	c.servers = fakeServers{list: &domain.ServerList{Servers: []domain.Server{{Name: "new", Enabled: true}}}}
	if r := c.serverResults(context.Background())[0]; r.Status != checkPass {
		t.Errorf("unchecked: %+v", r)
	}
}

func TestCoreLicenseCheck(t *testing.T) {
	ctx := context.Background()
	with := func(e *domain.EffectiveLicense, err error) checkResult {
		c := healthyChecker()
		c.effective = func(context.Context) (*domain.EffectiveLicense, error) { return e, err }
		return c.license(ctx)
	}
	tok := func(end time.Time) *domain.LicenseStatus {
		return &domain.LicenseStatus{Token: &license.Token{ExpiresAt: end}, GraceEndsAt: end.Add(14 * 24 * time.Hour)}
	}
	if r := with(&domain.EffectiveLicense{Effective: license.Effective{Basis: license.BasisLicense, Tier: "pro"}, License: tok(checkNow.Add(90 * 24 * time.Hour))}, nil); r.Status != checkPass || !strings.Contains(r.Detail, "pro license, ends 2027-01-14") {
		t.Errorf("active: %+v", r)
	}
	if r := with(&domain.EffectiveLicense{Effective: license.Effective{Basis: license.BasisLicense, Tier: "pro"}, License: tok(checkNow.Add(5 * 24 * time.Hour))}, nil); r.Status != checkWarn || !strings.Contains(r.Detail, "ends in 5 days") {
		t.Errorf("ending: %+v", r)
	}
	if r := with(&domain.EffectiveLicense{Effective: license.Effective{Basis: license.BasisGrace, Tier: "pro"}, License: tok(checkNow.Add(-3 * 24 * time.Hour))}, nil); r.Status != checkFail || !strings.Contains(r.Detail, "grace period (11 days left)") {
		t.Errorf("grace: %+v", r)
	}
	if r := with(&domain.EffectiveLicense{Effective: license.Effective{Basis: license.BasisTrial, TrialEndsAt: checkNow.Add(2 * 24 * time.Hour)}}, nil); r.Status != checkWarn || !strings.Contains(r.Detail, "trial ends in 2 days") {
		t.Errorf("trial: %+v", r)
	}
	if r := with(&domain.EffectiveLicense{Effective: license.Effective{Basis: license.BasisFree, Tier: license.TierFree}}, nil); r.Status != checkWarn || !strings.Contains(r.Detail, "free tier") {
		t.Errorf("free: %+v", r)
	}
	if r := with(nil, errors.New("db down")); r.Status != checkFail {
		t.Errorf("error: %+v", r)
	}
}

func TestWriteChecks(t *testing.T) {
	rs := []checkResult{{"A", checkPass, "ok"}, {"B", checkWarn, "hmm"}}
	var out bytes.Buffer
	if code := writeChecks(&out, true, rs); code != 0 {
		t.Fatalf("exit %d", code)
	}
	var got []checkResult
	if err := json.Unmarshal(out.Bytes(), &got); err != nil || len(got) != 2 || got[1].Status != checkWarn {
		t.Fatalf("%v %+v", err, got)
	}
	out.Reset()
	if code := writeChecks(&out, false, append(rs, checkResult{"C", checkFail, "bad"})); code != 1 || !strings.Contains(out.String(), "FAIL  C") {
		t.Fatalf("%d %s", code, out.String())
	}
}

func TestScrubURL(t *testing.T) {
	for in, want := range map[string]string{
		"dial redis://:pw@redis:6379/0: refused": "dial redis://redis:6379/0: refused",
		"postgres://u:p@h/db failed":             "postgres://h/db failed",
		"no url here":                            "no url here",
		"https://example.com/x":                  "https://example.com/x",
	} {
		if got := scrubURL(in); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
}

// `core check --json` against a real database: the license is the trial, the
// provisioner is not there (a warning, not a failure), and the output is one
// JSON array.
func TestCheckCmdWithTheStore(t *testing.T) {
	coreEnv(t)
	t.Setenv("BOBRES_PROVISIONER_GRPC_ADDR", "127.0.0.1:1") // refused at once
	var out, errOut bytes.Buffer
	if code := checkCmd([]string{"--json"}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s %s", code, out.String(), errOut.String())
	}
	var rs []checkResult
	if err := json.Unmarshal(out.Bytes(), &rs); err != nil {
		t.Fatalf("not one JSON array: %v\n%s", err, out.String())
	}
	got := byName(rs)
	if got["Database"].Status != checkPass || got["License"].Status == checkFail || got["Watchdog"].Status != checkPass {
		t.Fatalf("%+v", rs)
	}
	if s := got["3x-ui servers"].Status; s != checkWarn && s != checkSkip {
		t.Fatalf("servers: %+v", got["3x-ui servers"])
	}
	out.Reset()
	if code := checkCmd(nil, &out, &errOut); code != 0 || !strings.Contains(out.String(), "PASS  Database") {
		t.Fatalf("text: %d %s", code, out.String())
	}
	if code := checkCmd([]string{"--bogus"}, &out, &errOut); code != 2 {
		t.Fatalf("usage: %d", code)
	}
}
