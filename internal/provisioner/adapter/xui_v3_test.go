package adapter

import (
	"context"
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
	a, _ := testAdapter(t)
	v, err := a.HealthCheck(context.Background())
	if err != nil || v == "" {
		t.Fatalf("health: %v %q", err, v)
	}
}
