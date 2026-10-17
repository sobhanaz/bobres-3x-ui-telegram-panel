package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tlsconf"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tlsprobe"
)

var screenNow = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

const (
	licenseActiveJSON = `{"state":"active","effective":{"basis":"license","tier":"pro","trial_days_left":0},
		"license":{"expires_at":1790000000,"grace_ends_at":1791000000,"license_ends_at":1792000000}}`
	sslStatusJSON = `{"state":{"mode":"auto"},"status":{"addresses":[{"role":"ip","served":true,"days_left":3},
		{"role":"domain","served":true,"days_left":61}]}}`
)

// scripted answers each core exec by its subcommand, compose ps with ps.
type scripted struct {
	ps, license, ssl string
	psErr, coreErr   error
	calls            [][]string
	mu               sync.Mutex // the status screen runs its checks in parallel
}

func (s *scripted) run(_ context.Context, _ string, args ...string) ([]byte, error) {
	s.mu.Lock()
	s.calls = append(s.calls, args)
	s.mu.Unlock()
	switch {
	case contains(args, "ps"):
		return []byte(s.ps), s.psErr
	case contains(args, "license"):
		return []byte(s.license), s.coreErr
	case contains(args, "ssl"):
		return []byte(s.ssl), s.coreErr
	}
	return nil, nil
}

func screenOps(s *scripted, stdin string) (*ops, *strings.Builder, *strings.Builder) {
	o, _, _ := testOps(stdin, &fakeOps{})
	var out, errOut strings.Builder
	o.out, o.errOut = &out, &errOut
	o.run = s.run
	o.now = func() time.Time { return screenNow }
	o.probeTLS = func(context.Context, string) tlsprobe.Result {
		return tlsprobe.Result{Reachable: true, Served: true, NameOK: true,
			Info: tlsconf.CertInfo{NotAfter: screenNow.Add(40*24*time.Hour + time.Hour)}}
	}
	return o, &out, &errOut
}

func TestParseLicenseInfo(t *testing.T) {
	st := parseLicenseInfo([]byte(licenseActiveJSON), screenNow)
	// the day the license itself ends, not the token's own expiry
	want := daysUntil(time.Unix(1792000000, 0), screenNow)
	if !st.Known || st.Basis != "license" || st.Tier != "pro" || st.DaysLeft == nil || *st.DaysLeft != want {
		t.Fatalf("active: %+v (want %d days)", st, want)
	}
	grace := `{"state":"grace","effective":{"basis":"grace","tier":"standard"},"license":{"expires_at":1,"grace_ends_at":` +
		jsonInt(screenNow.Add(5*24*time.Hour+time.Hour).Unix()) + `}}`
	if st := parseLicenseInfo([]byte(grace), screenNow); st.Basis != "grace" || st.DaysLeft == nil || *st.DaysLeft != 5 {
		t.Fatalf("grace: %+v", st)
	}
	trial := `{"state":"missing","effective":{"basis":"trial","tier":"trial","trial_days_left":9}}`
	if st := parseLicenseInfo([]byte(trial), screenNow); st.Basis != "trial" || st.DaysLeft == nil || *st.DaysLeft != 9 {
		t.Fatalf("trial: %+v", st)
	}
	// docker prints noise before the object: it is still read
	if st := parseLicenseInfo([]byte("time=x warn\n"+trial), screenNow); !st.Known {
		t.Fatalf("noise before the JSON: %+v", st)
	}
	for _, bad := range []string{"", "not json", "{}", `{"state":"active"}`, `[1,2]`} {
		if st := parseLicenseInfo([]byte(bad), screenNow); st.Known {
			t.Errorf("%q read as %+v", bad, st)
		}
	}
}

func jsonInt(n int64) string { b, _ := json.Marshal(n); return string(b) }

func TestParseSSLMode(t *testing.T) {
	mode, days := parseSSLMode([]byte(sslStatusJSON))
	if mode != "auto" || days == nil || *days != 61 {
		t.Fatalf("got %q %v (the domain's certificate, not the IP's)", mode, days)
	}
	if mode, days := parseSSLMode([]byte("garbage")); mode != "" || days != nil {
		t.Fatalf("garbage: %q %v", mode, days)
	}
}

func TestCollectStatusHealthy(t *testing.T) {
	dir := installDir(t)
	if err := os.MkdirAll(filepath.Join(dir, "backups"), 0o755); err != nil {
		t.Fatal(err)
	}
	bk := "bobres-20261015T120000Z.bkp"
	if err := os.WriteFile(filepath.Join(dir, "backups", bk), make([]byte, 2048), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &scripted{ps: healthyWithBackup, license: licenseActiveJSON, ssl: sslStatusJSON}
	o, _, _ := screenOps(s, "")
	env := map[string]string{"BOBRES_DOMAIN": "panel.example.com", "BOBRES_VERSION": "1.2.3", "COMPOSE_PROFILES": "backup"}
	info := o.collectStatus(context.Background(), dir, env)
	if info.Services.State != svcRunning || info.Services.Up != 9 || info.Services.Total != 9 || len(info.Services.Down) != 0 {
		t.Fatalf("services: %+v", info.Services)
	}
	if !info.License.Known || info.License.Tier != "pro" {
		t.Fatalf("license: %+v", info.License)
	}
	// the certificate this host serves (40 days), not core's 61
	if !info.HTTPS.Known || info.HTTPS.Mode != "auto" || info.HTTPS.DaysLeft == nil || *info.HTTPS.DaysLeft != 40 {
		t.Fatalf("https: %+v", info.HTTPS)
	}
	if info.Backup.AgeHours == nil || *info.Backup.AgeHours != 24 || info.Backup.SizeBytes != 2048 {
		t.Fatalf("backup: %+v", info.Backup)
	}
	if info.URL != "https://panel.example.com/admin" || info.DiskFreeGB == nil || *info.DiskFreeGB != 42 {
		t.Fatalf("url/disk: %q %v", info.URL, info.DiskFreeGB)
	}
	text := strings.Join(statusLines(info, func(_, t string) string { return t }), "\n")
	for _, want := range []string{"running (9/9 services)", "pro license, ", "auto, certificate expires in 40 days",
		"newest 24 hours ago (2.0 KB)", "https://panel.example.com/admin"} {
		if !strings.Contains(text, want) {
			t.Errorf("dashboard lacks %q:\n%s", want, text)
		}
	}
}

func TestCollectStatusUnknownWhenCoreAndDockerSilent(t *testing.T) {
	dir := installDir(t)
	s := &scripted{psErr: errors.New("docker: not running"), coreErr: errors.New("exit 1")}
	o, _, _ := screenOps(s, "")
	o.probeTLS = func(context.Context, string) tlsprobe.Result { return tlsprobe.Result{} }
	info := o.collectStatus(context.Background(), dir, map[string]string{"BOBRES_DOMAIN": "panel.example.com"})
	if info.Services.State != svcUnknown || info.License.Known || info.HTTPS.Known {
		t.Fatalf("%+v", info)
	}
	text := strings.Join(statusLines(info, func(_, t string) string { return t }), "\n")
	for _, want := range []string{"unknown (is Docker running?)", "unknown (core did not answer)", "none"} {
		if !strings.Contains(text, want) {
			t.Errorf("lacks %q:\n%s", want, text)
		}
	}
}

func TestCollectStatusNamesWhatIsDown(t *testing.T) {
	dir := installDir(t)
	ps := strings.Replace(healthyPS, `{"Service":"bot","State":"running","Health":"healthy"}`, `{"Service":"bot","State":"exited","Health":""}`, 1)
	o, _, _ := screenOps(&scripted{ps: ps}, "")
	info := o.collectStatus(context.Background(), dir, map[string]string{"BOBRES_DOMAIN": "panel.example.com"})
	if info.Services.State != svcPartly || info.Services.Up != 7 || len(info.Services.Down) != 1 || info.Services.Down[0] != "bot" {
		t.Fatalf("%+v", info.Services)
	}
	text := strings.Join(statusLines(info, func(_, t string) string { return t }), "\n")
	if !strings.Contains(text, "partly running (7/8 services) - down: bot") {
		t.Errorf("header: %s", text)
	}
	o, _, _ = screenOps(&scripted{ps: "[]"}, "")
	if info := o.collectStatus(context.Background(), dir, map[string]string{}); info.Services.State != svcStopped {
		t.Fatalf("nothing running: %+v", info.Services)
	}
}

func TestStatusJSON(t *testing.T) {
	dir := installDir(t)
	o, out, errOut := screenOps(&scripted{ps: healthyPS, license: licenseActiveJSON, ssl: sslStatusJSON}, "")
	if code := o.statusCmd([]string{"--dir", dir, "--json"}); code != 0 {
		t.Fatalf("exit %d: %s %s", code, out, errOut)
	}
	var v struct {
		Installed bool   `json:"installed"`
		Domain    string `json:"domain"`
		Version   string `json:"version"`
		Services  struct {
			State string `json:"state"`
			Up    int    `json:"up"`
		} `json:"services"`
		License struct {
			Known bool   `json:"known"`
			Tier  string `json:"tier"`
		} `json:"license"`
	}
	if err := json.Unmarshal([]byte(out.String()), &v); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if !v.Installed || v.Domain != "panel.example.com" || v.Version != "1.2.3" || v.Services.State != "running" || v.Services.Up != 8 || v.License.Tier != "pro" {
		t.Fatalf("%+v", v)
	}

	// a stopped store is exit 1, still JSON
	o, out, _ = screenOps(&scripted{ps: "[]"}, "")
	if code := o.statusCmd([]string{"--dir", dir, "--json"}); code != 1 || !json.Valid([]byte(out.String())) {
		t.Fatalf("stopped: %d %s", code, out)
	}
	// no install: exit 2, nothing on stdout
	o, out, errOut = screenOps(&scripted{}, "")
	if code := o.statusCmd([]string{"--dir", t.TempDir(), "--json"}); code != 2 || out.Len() != 0 || !strings.Contains(errOut.String(), "no install found") {
		t.Fatalf("no install: %d %q %q", code, out, errOut)
	}
}

func TestStatusTexts(t *testing.T) {
	plain := func(_, t string) string { return t }
	d := func(n int) *int { return &n }
	for _, c := range []struct {
		got, want string
	}{
		{licenseLine(licenseStatus{Known: true, Basis: "license", Tier: "pro", DaysLeft: d(1)}, plain), "pro license, 1 day left"},
		{licenseLine(licenseStatus{Known: true, Basis: "license", Tier: "pro", DaysLeft: d(-3)}, plain), "pro license, expired 3 days ago"},
		{licenseLine(licenseStatus{Known: true, Basis: "grace", Tier: "pro", DaysLeft: d(4)}, plain), "pro license expired, in its grace period: 4 days left"},
		{licenseLine(licenseStatus{Known: true, Basis: "grace", Tier: "pro", DaysLeft: d(-1)}, plain), "pro license expired"},
		{licenseLine(licenseStatus{Known: true, Basis: "trial", DaysLeft: d(9)}, plain), "trial, 9 days left"},
		{licenseLine(licenseStatus{Known: true, Basis: "free", Tier: "free"}, plain), "free tier"},
		{httpsText(httpsStatus{Known: true, Mode: "manual", DaysLeft: d(-2)}, plain), "manual, certificate expired 2 days ago"},
		{httpsText(httpsStatus{Known: true}, plain), "on, certificate expiry unknown"},
		{ageText(30 * time.Second), "just now"},
		{ageText(90 * time.Minute), "1 hour ago"},
		{ageText(5 * 24 * time.Hour), "5 days ago"},
	} {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
	// a stale backup and a soon-expiring certificate are painted as warnings
	painted := func(code, text string) string { return "<" + code + ">" + text }
	old := 72.0
	if got := backupText(backupStatus{Enabled: true, AgeHours: &old, SizeBytes: 10}, painted); !strings.HasPrefix(got, "<33>") {
		t.Errorf("stale backup: %q", got)
	}
	if got := httpsText(httpsStatus{Known: true, Mode: "auto", DaysLeft: d(5)}, painted); !strings.Contains(got, "<33>") {
		t.Errorf("soon: %q", got)
	}
}

// The menu's header is the dashboard, beside the beaver.
func TestMenuHeaderIsTheDashboard(t *testing.T) {
	dir := installDir(t)
	s := &scripted{ps: healthyPS, license: licenseActiveJSON, ssl: sslStatusJSON}
	m, out, _, _, _ := testMenu(dir, "0\n", &fakeOps{})
	m.ops.run = s.run
	m.ops.now = func() time.Time { return screenNow }
	if code := m.loop(); code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	for _, want := range []string{"BOBRES manager", "( o  o )", "Store     running (8/8 services)", "License   pro license", "HTTPS     ", "Backup    none", "Dashboard https://panel.example.com/admin"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("header lacks %q:\n%s", want, out)
		}
	}
}
