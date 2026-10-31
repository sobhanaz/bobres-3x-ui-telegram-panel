//go:build integration

package adapter

// Runs the S-UI adapter against a REAL S-UI panel (CI: job "S-UI integration",
// the alireza7/s-ui image pinned by tag and digest). Needs:
//
//	SUI_TEST_URL=http://127.0.0.1:2095/app   the panel with its web path
//	SUI_TEST_TOKEN=<API token>                tools/suidev makes one
//	SUI_TEST_ALLOW_PRIVATE=1                  only if the panel is on a private address (plain http)
//	SUI_TEST_REQUIRE=1                        a missing variable fails instead of skipping (CI)
//	SUI_TEST_VERSION=v1.6.6                   optional: the exact version the panel must report
//	SUI_TEST_PROXY=127.0.0.1:2445             optional: the mixed inbound tools/suidev adds, for
//	                                          the test that sends real traffic
//	SUI_TEST_SUB=http://127.0.0.1:2096/sub/   optional: the panel's subscription address, fetched
//
//	go test -tags integration -run IntegrationSUI ./internal/provisioner/adapter/
//
// Every test makes its own throwaway clients and deletes them at the end.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/sui"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/xui"
)

func realSUI(t *testing.T) (*SUI, context.Context) {
	t.Helper()
	base, tok := os.Getenv("SUI_TEST_URL"), os.Getenv("SUI_TEST_TOKEN")
	if base == "" || tok == "" {
		if os.Getenv("SUI_TEST_REQUIRE") == "1" {
			t.Fatal("SUI_TEST_URL and SUI_TEST_TOKEN not set and SUI_TEST_REQUIRE=1")
		}
		t.Skip("SUI_TEST_URL and SUI_TEST_TOKEN not set")
	}
	var opts []xui.Option
	if os.Getenv("SUI_TEST_ALLOW_PRIVATE") == "1" {
		opts = append(opts, xui.AllowPrivateAddresses())
	}
	a, err := NewSUI(base, tok, opts...)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	t.Cleanup(cancel)
	return a, ctx
}

var suiITCounter atomic.Int64

func suiITClient(t *testing.T, ctx context.Context, a *SUI, expiryMs, traffic int64, ids []int) (email, subID string) {
	t.Helper()
	email = fmt.Sprintf("bobres-it-%d-%d", time.Now().Unix(), suiITCounter.Add(1))
	subID = fmt.Sprintf("it%d", time.Now().UnixNano())
	if err := a.CreateClient(ctx, email, subID, expiryMs, traffic, ids); err != nil {
		t.Fatalf("create %s: %v", email, err)
	}
	t.Cleanup(func() {
		if err := a.Delete(context.Background(), email); err != nil && !sui.IsNotFound(err) {
			t.Logf("cleanup delete %s: %v (delete it by hand)", email, err)
		}
	})
	return email, subID
}

// suiRemoveBehind deletes a client straight through the panel's API, as an
// operator does in the panel, without the adapter.
func suiRemoveBehind(t *testing.T, ctx context.Context, a *SUI, email string) {
	t.Helper()
	rows, err := a.c.Clients(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.Name == email {
			if err := a.c.Save(ctx, "del", r.ID); err != nil {
				t.Fatal(err)
			}
			return
		}
	}
	t.Fatalf("%s is not on the panel", email)
}

func TestIntegrationSUIHealthAndInbounds(t *testing.T) {
	a, ctx := realSUI(t)
	v, err := a.HealthCheck(ctx)
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	if want := os.Getenv("SUI_TEST_VERSION"); want != "" && v != want {
		t.Fatalf("version = %q, want %q", v, want)
	}
	ins, err := a.Inbounds(ctx)
	if err != nil {
		t.Fatal(err)
	}
	usable := 0
	for _, in := range ins {
		if in.Enabled {
			usable++
			if in.ID <= 0 || in.Tag == "" || in.Protocol == "" {
				t.Errorf("inbound %+v", in)
			}
		}
	}
	if usable < 2 {
		t.Fatalf("inbounds = %+v: the panel needs at least two that take clients (tools/suidev adds them)", ins)
	}
	bad, err := NewSUI(os.Getenv("SUI_TEST_URL"), "not-the-token", xui.AllowPrivateAddresses())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bad.Inbounds(ctx); !sui.IsUnauthorized(err) {
		t.Fatalf("a wrong token: %v", err)
	}
}

func TestIntegrationSUILifecycle(t *testing.T) {
	a, ctx := realSUI(t)
	exp := time.Now().Add(30*24*time.Hour).Unix() * 1000
	email, subID := suiITClient(t, ctx, a, exp, 10<<30, nil)

	if got, err := a.ClientSubID(ctx, email); err != nil || got != subID {
		t.Fatalf("ClientSubID = %q, %v", got, err)
	}
	st, err := a.Status(ctx, email)
	if err != nil || st.TotalBytes != 10<<30 || st.ExpiryMs != exp || !st.Enabled || st.UsedBytes != 0 {
		t.Fatalf("status = %+v, %v", st, err)
	}
	links, err := a.Links(ctx, email)
	if err != nil || len(links) < 2 {
		t.Fatalf("links = %v, %v", links, err)
	}
	for _, l := range links {
		if !strings.Contains(l, "://") {
			t.Fatalf("not a share link: %q", l)
		}
	}
	sl, err := a.SubscriptionLink(ctx, email, "")
	if err != nil || !strings.HasSuffix(sl, "/"+email) {
		t.Fatalf("subscription link = %q, %v", sl, err)
	}
	if sub := os.Getenv("SUI_TEST_SUB"); sub != "" {
		fetchOK(t, ctx, strings.TrimRight(sub, "/")+"/"+email)
	}

	exp2 := time.Now().Add(60*24*time.Hour).Unix() * 1000
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
	if st, _ := a.Status(ctx, email); !st.Enabled || st.UsedBytes != 0 || st.TotalBytes != 21<<30 {
		t.Fatalf("after reset = %+v", st)
	}

	// Deleted behind BOBRES's back: the answer says no such client.
	suiRemoveBehind(t, ctx, a, email)
	if _, err := a.Status(ctx, email); !sui.IsNotFound(err) {
		t.Fatalf("status of a client deleted on the panel: %v", err)
	}
	if _, err := a.ClientSubID(ctx, email); !sui.IsNotFound(err) {
		t.Fatalf("sub id of a client deleted on the panel: %v", err)
	}
	// Made again under the same name and subscription id (RecreateClient).
	if err := a.CreateClient(ctx, email, subID, exp, 3<<30, nil); err != nil {
		t.Fatalf("made again: %v", err)
	}
	if got, err := a.ClientSubID(ctx, email); err != nil || got != subID {
		t.Fatalf("made again: %q %v", got, err)
	}
	// A second create of the same name is refused by the panel.
	if err := a.CreateClient(ctx, email, subID, exp, 3<<30, nil); err == nil || !sui.Answered(err) {
		t.Fatalf("a second create: %v", err)
	}
	if err := a.Delete(ctx, email); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := a.Status(ctx, email); !sui.IsNotFound(err) {
		t.Fatalf("after Delete: %v", err)
	}
}

func TestIntegrationSUIDelayedStartAndGroups(t *testing.T) {
	a, ctx := realSUI(t)
	email, _ := suiITClient(t, ctx, a, -2*dayMs, 1<<30, nil)
	if st, err := a.Status(ctx, email); err != nil || st.ExpiryMs != -2*dayMs {
		t.Fatalf("delayed start = %+v, %v", st, err)
	}
	if err := a.Renew(ctx, email, 1, 0); err != nil {
		t.Fatal(err)
	}
	if st, _ := a.Status(ctx, email); st.ExpiryMs != -3*dayMs {
		t.Fatalf("renewed delayed start = %+v", st)
	}
	r, err := a.record(ctx, email)
	if err != nil || r.Str("group") != xui.BotGroup {
		t.Fatalf("group = %q, %v", r.Str("group"), err)
	}
	email2 := fmt.Sprintf("bobres-it-%d-%d", time.Now().Unix(), suiITCounter.Add(1))
	if err := a.CreateClientInGroup(ctx, "BOBRES-IT", email2, "itg", 0, 0, nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Delete(context.Background(), email2) })
	res, err := a.Regroup(ctx, map[string]GroupWant{email: {Group: "BOBRES-MOVED"}, email2: {SubID: "itg"}})
	if err != nil || !res.Converged || res.Tagged != 2 {
		t.Fatalf("regroup = %+v, %v", res, err)
	}
	for e, want := range map[string]string{email: "BOBRES-MOVED", email2: xui.BotGroup} {
		if r, _ := a.record(ctx, e); r.Str("group") != want {
			t.Errorf("%s group = %q, want %q", e, r.Str("group"), want)
		}
	}
}

// Real traffic through the mixed inbound (HTTP proxy with the client's own
// login) shows in Usage, and ResetTraffic zeroes it.
func TestIntegrationSUITraffic(t *testing.T) {
	a, ctx := realSUI(t)
	proxy := os.Getenv("SUI_TEST_PROXY")
	if proxy == "" {
		if os.Getenv("SUI_TEST_REQUIRE") == "1" {
			t.Fatal("SUI_TEST_PROXY not set and SUI_TEST_REQUIRE=1")
		}
		t.Skip("SUI_TEST_PROXY not set")
	}
	email, _ := suiITClient(t, ctx, a, 0, 0, nil)
	r, err := a.record(ctx, email)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]map[string]any
	if err := json.Unmarshal(r["config"], &cfg); err != nil {
		t.Fatal(err)
	}
	pw, _ := cfg["mixed"]["password"].(string)
	hc := &http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{
		Proxy: http.ProxyURL(&url.URL{Scheme: "http", User: url.UserPassword(email, pw), Host: proxy}),
	}}
	target := strings.TrimRight(os.Getenv("SUI_TEST_URL"), "/") + "/"
	deadline := time.Now().Add(90 * time.Second)
	var used int64
	for time.Now().Before(deadline) {
		for range 5 {
			if resp, err := hc.Get(target); err == nil { //nolint:gosec // the test panel the CI job started
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
			} else {
				t.Logf("request through the proxy: %v", err)
			}
		}
		if used, err = a.Usage(ctx, email); err != nil {
			t.Fatal(err)
		}
		if used > 0 {
			break
		}
		time.Sleep(3 * time.Second)
	}
	if used == 0 {
		t.Fatal("no traffic counted after 90 seconds")
	}
	if err := a.ResetTraffic(ctx, email); err != nil {
		t.Fatal(err)
	}
	if st, _ := a.Status(ctx, email); st.UsedBytes != 0 || !st.Enabled {
		t.Fatalf("after reset = %+v", st)
	}
}

func fetchOK(t *testing.T, ctx context.Context, u string) {
	t.Helper()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil) //nolint:gosec // the test panel the CI job started
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req) //nolint:gosec // the test panel the CI job started
	if err != nil {
		t.Fatalf("fetch the subscription: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK || len(b) == 0 {
		t.Fatalf("subscription answered %d with %d bytes", resp.StatusCode, len(b))
	}
}
