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
	"errors"
	"fmt"
	"net/http"
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

	// Both tested versions report panelVersion in server/status.
	ver, err := a.HealthCheck(ctx)
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	if ver == UnknownVersion || Untested(ver) {
		t.Fatalf("panel version %q: not detected, or outside %s..%s", ver, TestedMinVersion, TestedMaxVersion)
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

	// Turning a client off and on (staff, from the dashboard) keeps its
	// limits and identity.
	for _, on := range []bool{false, true} {
		if err := a.SetEnabled(ctx, email, on); err != nil {
			t.Fatalf("set enabled %v: %v", on, err)
		}
		st, err := a.Status(ctx, email)
		if err != nil || st.Enabled != on || st.ExpiryMs != exp || st.TotalBytes != 3<<30 {
			t.Fatalf("status after set enabled %v: %+v %v", on, st, err)
		}
	}
	if got, err := a.ClientSubID(ctx, email); err != nil || got != subID {
		t.Fatalf("subscription id after set enabled: %q %v", got, err)
	}
	if err := a.Delete(ctx, email); err != nil {
		t.Fatalf("delete: %v", err)
	}
	// A missing client is a refusal inside HTTP 200 (3x-ui's business
	// errors) saying "record not found": the provisioner relies on that text
	// to drop a reservation or a deleted client's row, and keeps them on any
	// other refusal (a busy panel).
	var ae *xui.APIError
	if _, err := a.ClientSubID(ctx, email); !errors.As(err, &ae) || ae.Status != http.StatusOK ||
		!strings.Contains(strings.ToLower(ae.Msg), "record not found") {
		t.Fatalf("lookup of a deleted client: %v", err)
	}
	if err := a.Delete(ctx, email); !errors.As(err, &ae) || ae.Status != http.StatusOK {
		t.Fatalf("delete of a deleted client: %v", err)
	}
}
