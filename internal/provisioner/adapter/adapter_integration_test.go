//go:build integration

package adapter

// Runs the v3 adapter against a REAL 3x-ui panel. Needs:
//
//	XUI_TEST_URL=https://panel.example.com:2053/basepath
//	XUI_TEST_TOKEN=<API token from Settings > Security>
//	XUI_TEST_ALLOW_PRIVATE=1   (only if the panel is on a private address)
//
//	go test -tags integration -run Integration ./internal/provisioner/adapter/
//
// It creates one throwaway client (email bobres-it-<unix time>) and deletes it
// at the end, also on failure.

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/xui"
)

func TestIntegrationRealPanel(t *testing.T) {
	baseURL, token := os.Getenv("XUI_TEST_URL"), os.Getenv("XUI_TEST_TOKEN")
	if baseURL == "" || token == "" {
		if os.Getenv("XUI_TEST_REQUIRE") == "1" { // CI: a missing variable must fail, not pass
			t.Fatal("XUI_TEST_URL and XUI_TEST_TOKEN not set and XUI_TEST_REQUIRE=1")
		}
		t.Skip("XUI_TEST_URL and XUI_TEST_TOKEN not set")
	}
	var opts []xui.Option
	if os.Getenv("XUI_TEST_ALLOW_PRIVATE") == "1" {
		opts = append(opts, xui.AllowPrivateAddresses())
	}
	a, err := NewXUIv3(baseURL, token, opts...)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if _, err := a.HealthCheck(ctx); err != nil {
		t.Fatalf("health: %v", err)
	}
	ins, err := a.Inbounds(ctx)
	if err != nil {
		t.Fatalf("inbounds: %v", err)
	}
	var ids []int
	for _, in := range ins {
		if in.Enabled {
			ids = append(ids, in.ID)
		}
	}
	if len(ids) == 0 {
		t.Fatal("the panel has no enabled inbound; enable one for this test")
	}

	email := fmt.Sprintf("bobres-it-%d", time.Now().Unix())
	subID := fmt.Sprintf("it%d", time.Now().UnixNano())
	expiry := time.Now().Add(24 * time.Hour).UnixMilli()
	if err := a.CreateClient(ctx, email, subID, expiry, 1<<30, ids); err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() {
		if err := a.Delete(context.Background(), email); err != nil {
			t.Logf("cleanup delete %s: %v (delete it by hand)", email, err)
		}
	})

	if got, err := a.ClientSubID(ctx, email); err != nil || got != subID {
		t.Fatalf("client sub id: %q %v", got, err)
	}
	if links, err := a.SubLinks(ctx, subID); err != nil || len(links) == 0 {
		t.Fatalf("sub links: %v %v", links, err)
	}
	links, err := a.Links(ctx, email)
	if err != nil || len(links) != len(ids) {
		t.Fatalf("links: %d for %d inbounds, %v", len(links), len(ids), err)
	}
	if used, err := a.Usage(ctx, email); err != nil || used != 0 {
		t.Fatalf("usage of a new client: %d %v", used, err)
	}
	if err := a.Renew(ctx, email, 1, 1<<30); err != nil {
		t.Fatalf("renew: %v", err)
	}
	if err := a.ResetTraffic(ctx, email); err != nil {
		t.Fatalf("reset traffic: %v", err)
	}

	// Absolute limits (renewals, top-ups): the values land exactly, twice in a
	// row changes nothing more, and the client keeps its identity (same
	// subscription id, same share links, which embed the protocol UUID).
	exp := time.Now().Add(72 * time.Hour).Truncate(time.Second).UnixMilli()
	for i := 0; i < 2; i++ {
		if err := a.SetLimits(ctx, email, exp, 3<<30); err != nil {
			t.Fatalf("set limits #%d: %v", i+1, err)
		}
	}
	st, err := a.Status(ctx, email)
	if err != nil || st.ExpiryMs != exp || st.TotalBytes != 3<<30 || !st.Enabled {
		t.Fatalf("status after set limits: %+v %v", st, err)
	}
	if got, err := a.ClientSubID(ctx, email); err != nil || got != subID {
		t.Fatalf("subscription id after set limits: %q %v", got, err)
	}
	after, err := a.Links(ctx, email)
	if err != nil || strings.Join(after, "\n") != strings.Join(links, "\n") {
		t.Fatalf("share links changed by set limits (identity lost?):\nbefore %v\nafter  %v (%v)", links, after, err)
	}
	if err := a.Delete(ctx, email); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := a.ClientSubID(ctx, email); err == nil {
		t.Fatal("client still exists after delete")
	}
}
