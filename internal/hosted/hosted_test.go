package hosted

import (
	"strings"
	"testing"
)

const testToken = "bm_fakeMetricsTokenFakeMetricsTokenFake0123" //nolint:gosec // made up //gitleaks:allow

func TestRegistrationRoundTrip(t *testing.T) {
	r := Registration{Customer: "Acme VPN", Domain: "Shop.Example.com", Version: "0.20.0", Channel: "stable", LicenseID: "lic_abc",
		MetricsToken: testToken, Created: 1_790_000_000}
	code, err := r.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(code, CodePrefix) {
		t.Fatalf("code: %s", code)
	}
	// A terminal may wrap it, and the owner may copy spaces around it.
	wrapped := "  " + code[:20] + "\n" + code[20:] + " \n"
	got, err := Decode(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	r.Domain = "shop.example.com"
	if *got != r {
		t.Fatalf("decoded %+v", got)
	}
}

func TestDecodeRefuses(t *testing.T) {
	good, _ := Registration{Customer: "A", Domain: "a.example.com"}.Encode()
	for name, code := range map[string]string{
		"empty":     "",
		"no prefix": strings.TrimPrefix(good, CodePrefix),
		"damaged":   CodePrefix + "!!!",
		"not json":  CodePrefix + "bm90IGpzb24",
		"too long":  CodePrefix + strings.Repeat("A", 5000),
	} {
		if _, err := Decode(code); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	for _, bad := range []Registration{
		{Customer: "", Domain: "a.example.com"},
		{Customer: "A", Domain: "localhost"},
		{Customer: "A", Domain: "10.0.0.1"},
		{Customer: "A", Domain: "a.example.com:443"},
		{Customer: "A", Domain: "a.example.com", Channel: "nightly"},
		{Customer: "A", Domain: "a.example.com", LicenseID: "x"},
		{Customer: "A", Domain: "a.example.com", MetricsToken: "short"},
		{Customer: "A\nB", Domain: "a.example.com"},
	} {
		if _, err := bad.Encode(); err == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
}

const exposition = `# HELP bobres_build_info The version of core.
# TYPE bobres_build_info gauge
bobres_build_info{version="0.20.0"} 1
bobres_service_up{service="core"} 1
bobres_service_up{service="bot"} 1
bobres_service_up{service="payments"} 0
bobres_database_up 1
bobres_queue_size{queue="pending_payments"} 3
bobres_orders_paid{window="1h"} 2
bobres_orders_paid{window="24h"} 41
bobres_alerts_open{severity="critical"} 1
bobres_alerts_open{severity="warning"} 2
bobres_license_days_left{basis="license"} 212
bobres_backup_age_seconds 3600
bobres_backup_last_run_ok 1
bobres_panel_up{server="de-1"} 1
bobres_panel_up{server="nl \"2\""} 0
bobres_host_disk_used_ratio{mount="/"} 0.42
bobres_host_disk_used_ratio{mount="/var"} 0.81
bobres_weird NaN
`

func TestParseMetrics(t *testing.T) {
	f, err := ParseMetrics([]byte(exposition))
	if err != nil {
		t.Fatal(err)
	}
	if f.Version != "0.20.0" || f.ServicesUp != 2 || f.ServicesTotal != 3 || f.DatabaseUp == nil || !*f.DatabaseUp ||
		f.OrdersPaid24h == nil || *f.OrdersPaid24h != 41 || f.AlertsCritical != 1 || f.AlertsWarning != 2 ||
		f.LicenseDaysLeft == nil || *f.LicenseDaysLeft != 212 || f.LicenseBasis != "license" || f.BackupAgeSeconds == nil ||
		*f.BackupAgeSeconds != 3600 || f.BackupLastOK == nil || !*f.BackupLastOK || f.PanelsUp != 1 || f.PanelsTotal != 2 ||
		f.DiskUsedRatio == nil || *f.DiskUsedRatio != 0.81 || f.QueuePending == nil || *f.QueuePending != 3 {
		t.Fatalf("figures: %+v", f)
	}
	if f.Healthy() {
		t.Error("a store with a service down and a critical alert is healthy")
	}
	ok, _ := ParseMetrics([]byte("bobres_build_info{version=\"1.0.0\"} 1\nbobres_service_up{service=\"core\"} 1\n"))
	if !ok.Healthy() {
		t.Errorf("healthy store: %+v", ok)
	}
	for name, body := range map[string]string{
		"not bobres": "go_goroutines 12\n",
		"html":       "<html><body>login</body></html>",
		"too long":   strings.Repeat("x", 2<<20),
	} {
		if _, err := ParseMetrics([]byte(body)); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestValidDomain(t *testing.T) {
	for d, want := range map[string]bool{"shop.example.com": true, "a.b": true, "example": false, "1.2.3.4": false, "-a.example.com": false,
		"a_b.example.com": false, "xn--mgbh0fb.xn--mgba3a4f16a": true} {
		if got := ValidDomain(d); got != want {
			t.Errorf("%s: %v", d, got)
		}
	}
}
