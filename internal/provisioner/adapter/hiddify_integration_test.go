//go:build integration

package adapter

// Runs the Hiddify adapter against a REAL Hiddify Manager panel (CI: job
// "Hiddify integration", the ghcr.io/hiddify/hiddify-manager image pinned by
// tag and digest; not a required check). Needs:
//
//	HIDDIFY_TEST_URL=http://127.0.0.1:9000/<admin proxy path>
//	HIDDIFY_TEST_KEY=<admin UUID>          `hiddify-panel-cli all-configs` prints it (api_key)
//	HIDDIFY_TEST_ALLOW_PRIVATE=1           only if the panel is on a private address (plain http)
//	HIDDIFY_TEST_REQUIRE=1                 a missing variable fails instead of skipping (CI)
//	HIDDIFY_TEST_VERSION=v14.0.5           optional: the exact version the panel must report
//
//	go test -tags integration -run IntegrationHiddify ./internal/provisioner/adapter/
//
// Every test makes its own throwaway users and deletes them at the end.

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/hiddify"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/xui"
)

func realHiddify(t *testing.T) (*Hiddify, context.Context) {
	t.Helper()
	base, key := os.Getenv("HIDDIFY_TEST_URL"), os.Getenv("HIDDIFY_TEST_KEY")
	if base == "" || key == "" {
		if os.Getenv("HIDDIFY_TEST_REQUIRE") == "1" {
			t.Fatal("HIDDIFY_TEST_URL and HIDDIFY_TEST_KEY not set and HIDDIFY_TEST_REQUIRE=1")
		}
		t.Skip("HIDDIFY_TEST_URL and HIDDIFY_TEST_KEY not set")
	}
	var opts []xui.Option
	if os.Getenv("HIDDIFY_TEST_ALLOW_PRIVATE") == "1" {
		opts = append(opts, xui.AllowPrivateAddresses())
	}
	a, err := NewHiddify(base, key, opts...)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	t.Cleanup(cancel)
	return a, ctx
}

var hdITCounter atomic.Int64

func hdITClient(t *testing.T, ctx context.Context, a *Hiddify, expiryMs, traffic int64) (email, subID string) {
	t.Helper()
	email = fmt.Sprintf("bobres-it-%d-%d", time.Now().Unix(), hdITCounter.Add(1))
	subID = fmt.Sprintf("it%d", time.Now().UnixNano())
	if err := a.CreateClient(ctx, email, subID, expiryMs, traffic, nil); err != nil {
		t.Fatalf("create %s: %v", email, err)
	}
	t.Cleanup(func() {
		if err := a.Delete(context.Background(), email); err != nil && !hiddify.IsNotFound(err) {
			t.Logf("cleanup delete %s: %v (delete it by hand)", email, err)
		}
	})
	return email, subID
}

func TestIntegrationHiddifyHealth(t *testing.T) {
	a, ctx := realHiddify(t)
	v, err := a.HealthCheck(ctx)
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	if want := os.Getenv("HIDDIFY_TEST_VERSION"); want != "" && v != want {
		t.Fatalf("version = %q, want %q", v, want)
	}
	if ins, err := a.Inbounds(ctx); err != nil || len(ins) != 1 {
		t.Fatalf("inbounds = %+v, %v", ins, err)
	}
	bad, err := NewHiddify(os.Getenv("HIDDIFY_TEST_URL"), "11111111-2222-4333-8444-555555555555", xui.AllowPrivateAddresses())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bad.Inbounds(ctx); !hiddify.IsUnauthorized(err) {
		t.Fatalf("a wrong key: %v", err)
	}
}

func TestIntegrationHiddifyLifecycle(t *testing.T) {
	a, ctx := realHiddify(t)
	day := func(d time.Time) int64 { return utcDay(d).UnixMilli() }
	exp := day(time.Now().Add(30 * 24 * time.Hour)) // a midnight: read back exactly
	traffic := int64(10<<30 + 4321)
	email, subID := hdITClient(t, ctx, a, exp, traffic)

	if got, err := a.ClientSubID(ctx, email); err != nil || got != subID {
		t.Fatalf("ClientSubID = %q, %v", got, err)
	}
	u, err := a.user(ctx, email)
	if err != nil || !strings.EqualFold(u.UUID, HiddifyUserUUID(subID)) {
		t.Fatalf("user = %v, %v", u, err)
	}
	st, err := a.Status(ctx, email)
	if err != nil || st.TotalBytes != traffic || st.ExpiryMs != exp || !st.Enabled || st.UsedBytes != 0 {
		t.Fatalf("status = %+v, %v (want expiry %d)", st, err, exp)
	}
	links, err := a.Links(ctx, email)
	if err != nil || len(links) == 0 {
		t.Fatalf("links = %v, %v", links, err)
	}
	sl, err := a.SubscriptionLink(ctx, email, "")
	if err != nil || !strings.Contains(sl, strings.ToLower(u.UUID)) || strings.Contains(sl, "asn=") {
		t.Fatalf("subscription link = %q, %v", sl, err)
	}

	if err := a.SetLimits(ctx, email, 0, 0); err != nil {
		t.Fatalf("SetLimits unlimited: %v", err)
	}
	if st, _ := a.Status(ctx, email); st.ExpiryMs != 0 || st.TotalBytes != 0 {
		t.Fatalf("unlimited = %+v", st)
	}
	exp2 := day(time.Now().Add(60 * 24 * time.Hour))
	if err := a.SetLimits(ctx, email, exp2, 20<<30); err != nil {
		t.Fatalf("SetLimits: %v", err)
	}
	if st, _ := a.Status(ctx, email); st.ExpiryMs != exp2 || st.TotalBytes != 20<<30 || !st.Enabled {
		t.Fatalf("after SetLimits = %+v", st)
	}
	if err := a.Renew(ctx, email, 3, 1<<30); err != nil {
		t.Fatalf("Renew: %v", err)
	}
	if st, _ := a.Status(ctx, email); st.ExpiryMs != exp2+3*dayMs || st.TotalBytes != 21<<30 {
		t.Fatalf("after Renew = %+v", st)
	}
	if err := a.SetEnabled(ctx, email, false); err != nil {
		t.Fatalf("switch off: %v", err)
	}
	if st, _ := a.Status(ctx, email); st.Enabled {
		t.Fatal("still on")
	}
	if err := a.SetEnabled(ctx, email, true); err != nil {
		t.Fatalf("switch on: %v", err)
	}
	if err := a.ResetTraffic(ctx, email); err != nil {
		t.Fatalf("ResetTraffic: %v", err)
	}
	if st, _ := a.Status(ctx, email); !st.Enabled || st.UsedBytes != 0 {
		t.Fatalf("after reset = %+v", st)
	}

	// Deleted behind BOBRES's back (straight through the panel's API).
	if err := a.c.DeleteUser(ctx, u.UUID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Status(ctx, email); !hiddify.IsNotFound(err) {
		t.Fatalf("status of a user deleted on the panel: %v", err)
	}
	// Made again: the panel takes the deleted user's UUID over, so the customer's links stay.
	if err := a.CreateClient(ctx, email, subID, exp, 3<<30, nil); err != nil {
		t.Fatalf("made again: %v", err)
	}
	again, err := a.user(ctx, email)
	if err != nil || !strings.EqualFold(again.UUID, u.UUID) {
		t.Fatalf("made again = %v, %v", again, err)
	}
	if err := a.CreateClient(ctx, email, subID, exp, 3<<30, nil); err == nil || !hiddify.Answered(err) {
		t.Fatalf("a second create: %v", err)
	}
	if err := a.Delete(ctx, email); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := a.Status(ctx, email); !hiddify.IsNotFound(err) {
		t.Fatalf("after Delete: %v", err)
	}
}

func TestIntegrationHiddifyFirstUse(t *testing.T) {
	a, ctx := realHiddify(t)
	email, _ := hdITClient(t, ctx, a, -5*dayMs, 1<<30)
	if st, err := a.Status(ctx, email); err != nil || st.ExpiryMs != -5*dayMs {
		t.Fatalf("first use = %+v, %v", st, err)
	}
	if err := a.Renew(ctx, email, 2, 0); err != nil {
		t.Fatal(err)
	}
	if st, _ := a.Status(ctx, email); st.ExpiryMs != -7*dayMs {
		t.Fatalf("renewed = %+v", st)
	}
}
