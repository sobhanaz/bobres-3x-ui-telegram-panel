//go:build integration

package server

// The provisioner end to end, through gRPC as core calls it, against a REAL
// S-UI or Hiddify panel (CI jobs "S-UI integration" and "Hiddify integration"),
// on a real database: create, links, usage, limits, renew, switch off and on,
// reset, a client deleted on the panel behind BOBRES's back (client_missing),
// RecreateClient and delete (panelFlow). The variables are the adapter
// integration tests' (internal/provisioner/adapter/sui_integration_test.go and
// hiddify_integration_test.go).

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/hiddify"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/provisioner/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/sui"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/xui"
)

func realPanelEnv(t *testing.T, prefix, tokenVar string) (baseURL, token string) {
	t.Helper()
	baseURL, token = os.Getenv(prefix+"_TEST_URL"), os.Getenv(prefix+"_TEST_"+tokenVar)
	if baseURL == "" || token == "" {
		if os.Getenv(prefix+"_TEST_REQUIRE") == "1" {
			t.Fatalf("%s_TEST_URL and %s_TEST_%s not set and %s_TEST_REQUIRE=1", prefix, prefix, tokenVar, prefix)
		}
		t.Skipf("%s_TEST_URL and %s_TEST_%s not set", prefix, prefix, tokenVar)
	}
	return baseURL, token
}

func TestIntegrationSUIServerFlow(t *testing.T) {
	baseURL, token := realPanelEnv(t, "SUI", "TOKEN")
	f := newFixture(t)
	sv := f.onlyPanel(t, store.KindSUI, baseURL, token)
	if want := os.Getenv("SUI_TEST_VERSION"); want != "" && sv.GetPanelVersion() != want {
		t.Fatalf("version = %q, want %q", sv.GetPanelVersion(), want)
	}
	c, err := sui.New(baseURL, token, xui.AllowPrivateAddresses())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	cleanup := func(email string) {
		rows, err := c.Clients(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rows {
			if r.Name == email {
				if err := c.Save(ctx, "del", r.ID); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	cleanup(panelFlowEmail) // a run that failed half way may have left it
	t.Cleanup(func() { cleanup(panelFlowEmail) })
	panelFlow(t, f, store.KindSUI, cleanup)
}

func TestIntegrationHiddifyServerFlow(t *testing.T) {
	baseURL, key := realPanelEnv(t, "HIDDIFY", "KEY")
	f := newFixture(t)
	sv := f.onlyPanel(t, store.KindHiddify, baseURL, key)
	if want := os.Getenv("HIDDIFY_TEST_VERSION"); want != "" && sv.GetPanelVersion() != want {
		t.Fatalf("version = %q, want %q", sv.GetPanelVersion(), want)
	}
	c, err := hiddify.New(baseURL, key, xui.AllowPrivateAddresses())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	cleanup := func(email string) {
		users, err := c.Users(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, u := range users {
			if u.Name == email {
				if err := c.DeleteUser(ctx, u.UUID); err != nil && !hiddify.IsNotFound(err) {
					t.Fatal(err)
				}
			}
		}
	}
	cleanup(panelFlowEmail)
	t.Cleanup(func() { cleanup(panelFlowEmail) })
	panelFlow(t, f, store.KindHiddify, cleanup)
}
