package adapter

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/xui"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/xuifake"
)

func testAdapter(t *testing.T) (*XUIv3, *xuifake.Server) {
	t.Helper()
	fake := xuifake.NewServer("panel-token")
	t.Cleanup(fake.Close)
	a, err := NewXUIv3(fake.URL, "panel-token",
		xui.AllowInsecureHTTP(), xui.AllowPrivateAddresses())
	if err != nil {
		t.Fatal(err)
	}
	return a, fake
}

func TestCreateRenewUsageResetDelete(t *testing.T) {
	a, _ := testAdapter(t)
	ctx := context.Background()
	email := "u42-abcd"

	// Finite expiry: bulkAdjust intentionally skips "add days to unlimited".
	expiryMs := time.Now().Add(30 * 24 * time.Hour).UnixMilli()
	if err := a.CreateClient(ctx, email, "sub1", expiryMs, 1<<30, []int{1, 2}); err != nil {
		t.Fatalf("CreateClient: %v", err)
	}
	if err := a.Renew(ctx, email, 7, 0); err != nil {
		t.Fatalf("Renew: %v", err)
	}
	if _, err := a.Usage(ctx, email); err != nil {
		t.Fatalf("Usage: %v", err)
	}
	if err := a.ResetTraffic(ctx, email); err != nil {
		t.Fatalf("ResetTraffic: %v", err)
	}
	if err := a.Delete(ctx, email); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := a.Usage(ctx, email); err == nil {
		t.Fatal("usage available after delete")
	}
}

func TestInboundsFiltersAndLinks(t *testing.T) {
	a, _ := testAdapter(t)
	ctx := context.Background()

	ins, err := a.Inbounds(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ins) != 3 {
		t.Fatalf("inbounds = %d, want 3", len(ins))
	}
	var enabled int
	for _, in := range ins {
		if in.Enabled {
			enabled++
		}
	}
	if enabled != 2 {
		t.Errorf("enabled inbounds = %d, want 2", enabled)
	}

	if err := a.CreateClient(ctx, "u42-links", "sub2", 0, 0, []int{1}); err != nil {
		t.Fatal(err)
	}
	links, err := a.Links(ctx, "u42-links")
	if err != nil {
		t.Fatal(err)
	}
	if len(links) == 0 {
		t.Error("no links returned")
	}
}

func TestHealthCheck(t *testing.T) {
	a, fake := testAdapter(t)
	ctx := context.Background()
	v, err := a.HealthCheck(ctx)
	if err != nil || v != "v3.9.0" {
		t.Fatalf("health: %v %q", err, v)
	}
	// The version is informational: without it the panel is still healthy.
	fake.FailRoute("status", 1)
	if v, err := a.HealthCheck(ctx); err != nil || v != UnknownVersion {
		t.Fatalf("status failing: %v %q", err, v)
	}
	fake.SetPanelVersion("")
	if v := a.PanelVersion(ctx); v != UnknownVersion {
		t.Fatalf("no version reported: %q", v)
	}
	// The inbound listing is what provisioning needs: it decides health.
	fake.FailRoute("options", 1)
	if _, err := a.HealthCheck(ctx); err == nil {
		t.Fatal("healthy without the inbound listing")
	}
	inbounds, err := a.Inbounds(ctx)
	if err != nil || inbounds[0].Protocol != "vless" || inbounds[0].Port != 443 {
		t.Fatalf("inbound details: %v %+v", err, inbounds)
	}
}

func TestVersions(t *testing.T) {
	for in, want := range map[string]string{
		"3.9.0": "v3.9.0", "v3.8.5": "v3.8.5", " 3.10.2 ": "v3.10.2", "": UnknownVersion,
		"dev+1a2b3c4d": "dev+1a2b3c4d", strings.Repeat("x", 40): strings.Repeat("x", 32),
	} {
		if got := NormalizeVersion(in); got != want {
			t.Errorf("NormalizeVersion(%q) = %q, want %q", in, got, want)
		}
	}
	for v, want := range map[string]bool{
		TestedMinVersion: false, TestedMaxVersion: false, "v3.8.9": false, UnknownVersion: false, "": false,
		"v3.8.4": true, "v3.9.1": true, "v3.10.0": true, "v2.9.9": true, "v4.0.0": true, "dev+1a2b3c4d": true,
	} {
		if got := Untested(v); got != want {
			t.Errorf("Untested(%q) = %v, want %v", v, got, want)
		}
	}
}
