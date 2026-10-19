//go:build integration

package adapter

// Runs the Marzban adapter against a REAL Marzban panel (CI: job "Marzban
// integration", the gozargah/marzban image pinned by tag and digest). Needs:
//
//	MARZBAN_TEST_URL=http://127.0.0.1:8000     the panel, with its base path if any
//	MARZBAN_TEST_USER=<admin>                   an admin made by `marzban cli admin create` (sudo or
//	                                            not; not the SUDO_USERNAME account)
//	MARZBAN_TEST_PASSWORD=<its password>
//	MARZBAN_TEST_ALLOW_PRIVATE=1                only if the panel is on a private address (plain http)
//	MARZBAN_TEST_REQUIRE=1                      a missing variable fails instead of skipping (CI)
//	MARZBAN_TEST_VERSION=v0.8.4                 optional: the exact version HealthCheck must report
//	MARZBAN_TEST_ENV_USER / MARZBAN_TEST_ENV_PASSWORD
//	                                            optional: the panel's SUDO_USERNAME account, for the
//	                                            tests of deleting a user that has no owner admin
//
//	go test -tags integration -run IntegrationMarzban ./internal/provisioner/adapter/
//
// marzban_traffic_integration_test.go sends real traffic and needs two more
// variables. Every test creates its own throwaway clients (email
// bobres-it-<unix time>-<n>) and deletes them at the end, also on failure.
// tools/marzbandev prepares a fresh panel (and local.sh runs one in Docker).

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/marzban"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/xui"
)

// marzbanEnv reads a variable of the real-panel tests; without the three that
// are needed the test skips, unless MARZBAN_TEST_REQUIRE=1 (set in CI), so a
// renamed variable cannot turn the job green without running anything.
func marzbanEnv(t *testing.T) (baseURL, user, pass string) {
	t.Helper()
	baseURL, user, pass = os.Getenv("MARZBAN_TEST_URL"), os.Getenv("MARZBAN_TEST_USER"), os.Getenv("MARZBAN_TEST_PASSWORD")
	if baseURL == "" || user == "" || pass == "" {
		if os.Getenv("MARZBAN_TEST_REQUIRE") == "1" {
			t.Fatal("MARZBAN_TEST_URL, MARZBAN_TEST_USER and MARZBAN_TEST_PASSWORD not set and MARZBAN_TEST_REQUIRE=1")
		}
		t.Skip("MARZBAN_TEST_URL, MARZBAN_TEST_USER and MARZBAN_TEST_PASSWORD not set")
	}
	return baseURL, user, pass
}

func marzbanOptions() []xui.Option {
	if os.Getenv("MARZBAN_TEST_ALLOW_PRIVATE") == "1" {
		return []xui.Option{xui.AllowPrivateAddresses()}
	}
	return nil
}

func realMarzban(t *testing.T) (*Marzban, context.Context) {
	t.Helper()
	baseURL, user, pass := marzbanEnv(t)
	a, err := NewMarzban(baseURL, user+":"+pass, marzbanOptions()...)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)
	return a, ctx
}

// envAccountMarzban is an adapter signed in as the panel's SUDO_USERNAME
// account, whose users have no owner admin (MARZBAN_TEST_ENV_USER and
// MARZBAN_TEST_ENV_PASSWORD).
func envAccountMarzban(t *testing.T) (*Marzban, context.Context) {
	t.Helper()
	baseURL, _, _ := marzbanEnv(t)
	envUser, envPass := os.Getenv("MARZBAN_TEST_ENV_USER"), os.Getenv("MARZBAN_TEST_ENV_PASSWORD")
	if envUser == "" || envPass == "" {
		if os.Getenv("MARZBAN_TEST_REQUIRE") == "1" {
			t.Fatal("MARZBAN_TEST_ENV_USER and MARZBAN_TEST_ENV_PASSWORD not set and MARZBAN_TEST_REQUIRE=1")
		}
		t.Skip("MARZBAN_TEST_ENV_USER and MARZBAN_TEST_ENV_PASSWORD not set")
	}
	a, err := NewMarzban(baseURL, envUser+":"+envPass, marzbanOptions()...)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)
	return a, ctx
}

var itCounter atomic.Int64

// itClient creates a client on the panel and removes it when the test ends.
func itClient(t *testing.T, ctx context.Context, a *Marzban, expiryMs, traffic int64, ids []int) (email, subID string) {
	t.Helper()
	email = fmt.Sprintf("bobres-it-%d-%d", time.Now().Unix(), itCounter.Add(1))
	subID = fmt.Sprintf("it%d", time.Now().UnixNano())
	if err := a.CreateClient(ctx, email, subID, expiryMs, traffic, ids); err != nil {
		t.Fatalf("create %s: %v", email, err)
	}
	t.Cleanup(func() {
		if err := a.Delete(context.Background(), email); err != nil && !marzban.IsNotFound(err) {
			t.Logf("cleanup delete %s: %v (delete it by hand)", email, err)
		}
	})
	return email, subID
}

// schemeOf is the share-link scheme the panel uses for a protocol.
func schemeOf(protocol string) string {
	if protocol == "shadowsocks" {
		return "ss"
	}
	return protocol
}

func linkSchemes(t *testing.T, links []string) []string {
	t.Helper()
	var out []string
	for _, l := range links {
		scheme, _, ok := strings.Cut(l, "://")
		if !ok || scheme == "" {
			t.Fatalf("not a share link: %q", l)
		}
		if !slices.Contains(out, scheme) {
			out = append(out, scheme)
		}
	}
	slices.Sort(out)
	return out
}

func TestIntegrationMarzbanHealth(t *testing.T) {
	a, ctx := realMarzban(t)
	ver, err := a.HealthCheck(ctx)
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	if !semver.MatchString(ver) {
		t.Fatalf("version %q is not vX.Y.Z (the panel reports it without the v; NormalizeVersion adds it)", ver)
	}
	if want := os.Getenv("MARZBAN_TEST_VERSION"); want != "" && ver != want {
		t.Fatalf("panel version %q, want %q", ver, want)
	}
	t.Logf("Marzban %s", ver)
	if got := a.PanelVersion(ctx); got != ver {
		t.Fatalf("PanelVersion = %q, HealthCheck = %q", got, ver)
	}

	ins, err := a.Inbounds(ctx)
	if err != nil || len(ins) == 0 {
		t.Fatalf("inbounds: %v %v", ins, err)
	}
	ids := map[int]bool{}
	for _, in := range ins {
		if in.ID <= 0 || ids[in.ID] || in.Tag == "" || in.Protocol == "" || !in.Enabled {
			t.Fatalf("inbound %+v (ids so far %v)", in, ids)
		}
		ids[in.ID] = true
	}
	again, err := a.Inbounds(ctx)
	if err != nil || !slices.Equal(ins, again) {
		t.Fatalf("inbound ids changed between two reads: %v vs %v (%v)", ins, again, err)
	}
	t.Logf("%d inbound(s): %+v", len(ins), ins)
}

func TestIntegrationMarzbanLifecycle(t *testing.T) {
	a, ctx := realMarzban(t)
	ins, err := a.Inbounds(ctx)
	if err != nil || len(ins) == 0 {
		t.Fatalf("inbounds: %v %v", ins, err)
	}
	var ids []int
	var protocols []string
	for _, in := range ins {
		ids = append(ids, in.ID)
		if s := schemeOf(in.Protocol); !slices.Contains(protocols, s) {
			protocols = append(protocols, s)
		}
	}
	slices.Sort(protocols)

	expiry := time.Now().Add(24 * time.Hour).UnixMilli()
	email, subID := itClient(t, ctx, a, expiry, 1<<30, ids)

	// What the panel holds: the limits in its own units, the client enabled and unused.
	st, err := a.Status(ctx, email)
	if err != nil || st.ExpiryMs != expiry/1000*1000 || st.TotalBytes != 1<<30 || st.UsedBytes != 0 || !st.Enabled {
		t.Fatalf("status of a new client: %+v %v (want expiry %d)", st, err, expiry/1000*1000)
	}
	if got, err := a.ClientSubID(ctx, email); err != nil || got != subID {
		t.Fatalf("client sub id: %q %v", got, err)
	}
	if _, err := a.SubLinks(ctx, subID); err == nil {
		t.Fatal("SubLinks answered: Marzban subscriptions are addressed by the panel's own token")
	}
	if used, err := a.Usage(ctx, email); err != nil || used != 0 {
		t.Fatalf("usage of a new client: %d %v", used, err)
	}

	// The share links: one scheme per protocol the client was attached to
	// (the failure this guards against is a client with no link at all).
	links, err := a.Links(ctx, email)
	if err != nil || len(links) < len(ins) || !slices.Equal(linkSchemes(t, links), protocols) {
		t.Fatalf("links of a client on %d inbound(s) %v: %v %v", len(ins), protocols, links, err)
	}

	// The subscription link: the panel's own, reachable, serving those links.
	sub, err := a.SubscriptionLink(ctx, email, "")
	if err != nil {
		t.Fatalf("subscription link: %v", err)
	}
	assertSubscriptionServes(t, ctx, a, sub, len(links))
	tok := sub[strings.LastIndex(sub, "/")+1:]
	if over, err := a.SubscriptionLink(ctx, email, "https://sub.example.test/sub/"); err != nil ||
		!strings.HasPrefix(over, "https://sub.example.test/sub/") || len(over) <= len("https://sub.example.test/sub/") {
		t.Fatalf("subscription link with a base URL: %q %v (token %q)", over, err, tok)
	}

	// Renewal and top-up add to what the panel holds.
	if err := a.Renew(ctx, email, 1, 1<<30); err != nil {
		t.Fatalf("renew: %v", err)
	}
	st, err = a.Status(ctx, email)
	if err != nil || st.ExpiryMs != expiry/1000*1000+86400*1000 || st.TotalBytes != 2<<30 || !st.Enabled {
		t.Fatalf("status after renew: %+v %v", st, err)
	}

	// Absolute limits (renewals, top-ups): the values land exactly, twice in a
	// row changes nothing more, and the client keeps its identity (same
	// subscription id, same share links, which embed the protocol UUIDs).
	exp := time.Now().Add(72 * time.Hour).Truncate(time.Second).UnixMilli()
	for i := 0; i < 2; i++ {
		if err := a.SetLimits(ctx, email, exp, 3<<30); err != nil {
			t.Fatalf("set limits #%d: %v", i+1, err)
		}
	}
	st, err = a.Status(ctx, email)
	if err != nil || st.ExpiryMs != exp || st.TotalBytes != 3<<30 || !st.Enabled {
		t.Fatalf("status after set limits: %+v %v", st, err)
	}
	if got, err := a.ClientSubID(ctx, email); err != nil || got != subID {
		t.Fatalf("subscription id after set limits: %q %v", got, err)
	}
	if after, err := a.Links(ctx, email); err != nil || !slices.Equal(after, links) {
		t.Fatalf("share links changed by set limits (identity lost?):\nbefore %v\nafter  %v (%v)", links, after, err)
	}

	// Turning a client off and on (staff, from the dashboard) keeps its limits
	// and identity.
	for _, on := range []bool{false, true} {
		if err := a.SetEnabled(ctx, email, on); err != nil {
			t.Fatalf("set enabled %v: %v", on, err)
		}
		st, err := a.Status(ctx, email)
		if err != nil || st.Enabled != on || st.ExpiryMs != exp || st.TotalBytes != 3<<30 {
			t.Fatalf("status after set enabled %v: %+v %v", on, st, err)
		}
	}

	// A traffic reset keeps a disabled client disabled (the panel's own reset
	// would switch it on).
	if err := a.SetEnabled(ctx, email, false); err != nil {
		t.Fatal(err)
	}
	if err := a.ResetTraffic(ctx, email); err != nil {
		t.Fatalf("reset traffic: %v", err)
	}
	if st, err := a.Status(ctx, email); err != nil || st.Enabled || st.UsedBytes != 0 || st.ExpiryMs != exp {
		t.Fatalf("status of a disabled client after a reset: %+v %v", st, err)
	}
	if err := a.SetEnabled(ctx, email, true); err != nil {
		t.Fatal(err)
	}
	if err := a.ResetTraffic(ctx, email); err != nil {
		t.Fatalf("reset traffic: %v", err)
	}
	if st, err := a.Status(ctx, email); err != nil || !st.Enabled || st.UsedBytes != 0 {
		t.Fatalf("status after a reset: %+v %v", st, err)
	}

	// Delete, and what the provisioner relies on afterwards: a missing client
	// is an HTTP 404 on every lookup and on a second delete.
	if err := a.Delete(ctx, email); err != nil {
		t.Fatalf("delete: %v", err)
	}
	for name, err := range map[string]error{
		"status": func() error { _, e := a.Status(ctx, email); return e }(),
		"subid":  func() error { _, e := a.ClientSubID(ctx, email); return e }(),
		"links":  func() error { _, e := a.Links(ctx, email); return e }(),
		"delete": a.Delete(ctx, email),
		"reset":  a.ResetTraffic(ctx, email),
		"renew":  a.Renew(ctx, email, 1, 0),
	} {
		if !marzban.IsNotFound(err) {
			t.Fatalf("%s of a deleted client: %v, want HTTP 404", name, err)
		}
	}
	if _, err := a.SubscriptionLink(ctx, email, ""); !marzban.IsNotFound(err) {
		t.Fatalf("subscription link of a deleted client: %v", err)
	}
}

// assertSubscriptionServes fetches the subscription URL the way a client app
// does and counts the share links in it. A link that points elsewhere than the
// panel under test (the panel has a subscription URL prefix) cannot be fetched
// from here and is skipped.
func assertSubscriptionServes(t *testing.T, ctx context.Context, a *Marzban, link string, want int) {
	t.Helper()
	u, err := url.Parse(link)
	if err != nil || u.Host == "" {
		t.Fatalf("subscription link %q: %v", link, err)
	}
	if base, _ := url.Parse(a.base); base == nil || !strings.EqualFold(base.Host, u.Host) {
		t.Logf("subscription link %q is not on the panel's own address: not fetched", link)
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("User-Agent", "bobres-integration-test")
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("fetch the subscription: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the subscription link answered HTTP %d: %q", resp.StatusCode, raw)
	}
	dec, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("the subscription body is not base64: %v (%.60q)", err, raw)
	}
	lines := strings.Split(strings.TrimSpace(string(dec)), "\n")
	if len(lines) != want {
		t.Fatalf("the subscription holds %d link(s), want %d: %q", len(lines), want, dec)
	}
	linkSchemes(t, lines)
}

// A client meant for one inbound carries that inbound's links and nothing else.
func TestIntegrationMarzbanInboundChoice(t *testing.T) {
	a, ctx := realMarzban(t)
	ins, err := a.Inbounds(ctx)
	if err != nil {
		t.Fatal(err)
	}
	protocols := map[string]bool{}
	for _, in := range ins {
		protocols[in.Protocol] = true
	}
	if len(protocols) < 2 {
		t.Skipf("the panel serves %d protocol(s); the choice needs two (tools/marzbandev adds them)", len(protocols))
	}
	for _, pick := range ins {
		email, _ := itClient(t, ctx, a, 0, 0, []int{pick.ID})
		links, err := a.Links(ctx, email)
		if err != nil || len(links) == 0 {
			t.Fatalf("links of a client on %q: %v %v", pick.Tag, links, err)
		}
		if got := linkSchemes(t, links); !slices.Equal(got, []string{schemeOf(pick.Protocol)}) {
			t.Fatalf("a client on %q (%s) has links of %v: %v", pick.Tag, pick.Protocol, got, links)
		}
	}
	if err := a.CreateClient(ctx, "bobres-it-unknown", "s", 0, 0, []int{1}); err == nil {
		_ = a.Delete(ctx, "bobres-it-unknown")
		t.Fatal("an unknown inbound id was accepted")
	}
}

// A client whose clock starts at its first use ("on hold") keeps waiting
// through staff switching it off and on and through a traffic reset, and
// SetLimits turns it into one with a fixed expiry.
func TestIntegrationMarzbanFirstUse(t *testing.T) {
	a, ctx := realMarzban(t)
	email, _ := itClient(t, ctx, a, -3*86400*1000, 1<<30, nil)
	want := func(step string) {
		t.Helper()
		st, err := a.Status(ctx, email)
		if err != nil || !st.Enabled || st.ExpiryMs != -3*86400*1000 || st.TotalBytes != 1<<30 {
			t.Fatalf("%s: %+v %v, want a client on hold for three days", step, st, err)
		}
	}
	want("created")
	if links, err := a.Links(ctx, email); err != nil || len(links) == 0 {
		t.Fatalf("a client on hold has no links: %v %v", links, err)
	}
	if sub, err := a.SubscriptionLink(ctx, email, ""); err != nil || sub == "" {
		t.Fatalf("subscription link of a client on hold: %q %v", sub, err)
	}

	if err := a.SetEnabled(ctx, email, false); err != nil {
		t.Fatal(err)
	}
	if st, err := a.Status(ctx, email); err != nil || st.Enabled {
		t.Fatalf("disabled: %+v %v", st, err)
	}
	if err := a.ResetTraffic(ctx, email); err != nil { // while off
		t.Fatal(err)
	}
	if st, err := a.Status(ctx, email); err != nil || st.Enabled {
		t.Fatalf("a reset switched a disabled client on: %+v %v", st, err)
	}
	if err := a.SetEnabled(ctx, email, true); err != nil {
		t.Fatalf("enable a client on hold: %v", err)
	}
	want("enabled again")
	if err := a.ResetTraffic(ctx, email); err != nil {
		t.Fatal(err)
	}
	want("reset")
	if err := a.Renew(ctx, email, 2, 0); err != nil {
		t.Fatal(err)
	}
	if st, err := a.Status(ctx, email); err != nil || !st.Enabled || st.ExpiryMs != -5*86400*1000 {
		t.Fatalf("renewed: %+v %v, want five days on hold", st, err)
	}

	exp := time.Now().Add(48 * time.Hour).Truncate(time.Second).UnixMilli()
	if err := a.SetLimits(ctx, email, exp, 2<<30); err != nil {
		t.Fatal(err)
	}
	if st, err := a.Status(ctx, email); err != nil || !st.Enabled || st.ExpiryMs != exp || st.TotalBytes != 2<<30 {
		t.Fatalf("after SetLimits: %+v %v", st, err)
	}
}

// Past its expiry a client is off; a renewal (SetLimits) turns it on.
func TestIntegrationMarzbanExpiry(t *testing.T) {
	a, ctx := realMarzban(t)
	email, _ := itClient(t, ctx, a, time.Now().Add(time.Hour).UnixMilli(), 1<<30, nil)
	past := time.Now().Add(-time.Hour).Truncate(time.Second).UnixMilli()
	if err := a.SetLimits(ctx, email, past, 1<<30); err != nil {
		t.Fatal(err)
	}
	if st, err := a.Status(ctx, email); err != nil || st.Enabled || st.ExpiryMs != past {
		t.Fatalf("a client past its expiry: %+v %v", st, err)
	}
	if err := a.ResetTraffic(ctx, email); err != nil {
		t.Fatal(err)
	}
	if st, err := a.Status(ctx, email); err != nil || st.Enabled {
		t.Fatalf("a reset revived an expired client: %+v %v", st, err)
	}
	next := time.Now().Add(72 * time.Hour).Truncate(time.Second).UnixMilli()
	if err := a.SetLimits(ctx, email, next, 1<<30); err != nil {
		t.Fatal(err)
	}
	if st, err := a.Status(ctx, email); err != nil || !st.Enabled || st.ExpiryMs != next {
		t.Fatalf("after the renewal: %+v %v", st, err)
	}
}

func TestIntegrationMarzbanUnlimited(t *testing.T) {
	a, ctx := realMarzban(t)
	email, _ := itClient(t, ctx, a, 0, 0, nil)
	if st, err := a.Status(ctx, email); err != nil || !st.Enabled || st.ExpiryMs != 0 || st.TotalBytes != 0 {
		t.Fatalf("unlimited client: %+v %v", st, err)
	}
	if err := a.Renew(ctx, email, 7, 0); err == nil || !strings.Contains(err.Error(), "renew skipped") {
		t.Fatalf("renew of an unlimited client: %v", err)
	}
	if err := a.Renew(ctx, email, 0, 1<<20); err == nil || !strings.Contains(err.Error(), "renew skipped") {
		t.Fatalf("top-up of an unlimited client: %v", err)
	}
	// Limits can be put on it afterwards, and taken off again.
	if err := a.SetLimits(ctx, email, time.Now().Add(time.Hour).UnixMilli(), 1<<20); err != nil {
		t.Fatal(err)
	}
	if err := a.SetLimits(ctx, email, 0, 0); err != nil {
		t.Fatal(err)
	}
	if st, err := a.Status(ctx, email); err != nil || !st.Enabled || st.ExpiryMs != 0 || st.TotalBytes != 0 {
		t.Fatalf("unlimited again: %+v %v", st, err)
	}
}

func TestIntegrationMarzbanRefusals(t *testing.T) {
	baseURL, user, pass := marzbanEnv(t)
	a, ctx := realMarzban(t)

	// A wrong password is a refusal the dashboard can name.
	bad, err := NewMarzban(baseURL, user+":"+pass+"-wrong", marzbanOptions()...)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bad.HealthCheck(ctx); !marzban.IsUnauthorized(err) {
		t.Fatalf("health check with a wrong password: %v, want unauthorized", err)
	}

	email, _ := itClient(t, ctx, a, 0, 0, nil)
	// The same username again: a conflict.
	ins, err := a.Inbounds(ctx)
	if err != nil || len(ins) == 0 {
		t.Fatalf("inbounds: %v %v", ins, err)
	}
	if err := a.CreateClient(ctx, email, "other", 0, 0, []int{ins[0].ID}); !marzban.IsConflict(err) {
		t.Fatalf("duplicate create: %v, want conflict", err)
	}
	// A client of somebody else's is not ours: the note tells.
	if got, err := a.ClientSubID(ctx, email); err != nil || !strings.HasPrefix(got, "it") {
		t.Fatalf("client sub id: %q %v", got, err)
	}
	// A name the panel refuses never reaches it.
	if err := a.CreateClient(ctx, "u1-abcd1234", "s", 0, 0, nil); !errors.Is(err, marzban.ErrInvalidUsername) {
		t.Fatalf("create of u1-abcd1234: %v", err)
	}
}

// A user made through the panel's SUDO_USERNAME account has no owner admin.
// Marzban v0.8 deletes the row of such a user and fails with HTTP 500 before
// it removes the account from the running core; the adapter disables the user
// first and counts that 500 as a deletion.
func TestIntegrationMarzbanOwnerlessDelete(t *testing.T) {
	a, ctx := envAccountMarzban(t)
	baseURL, _, _ := marzbanEnv(t)
	email, _ := itClient(t, ctx, a, time.Now().Add(time.Hour).UnixMilli(), 1<<30, nil)
	c, err := marzban.New(baseURL, os.Getenv("MARZBAN_TEST_ENV_USER"), os.Getenv("MARZBAN_TEST_ENV_PASSWORD"), marzbanOptions()...)
	if err != nil {
		t.Fatal(err)
	}
	u, err := c.GetUser(ctx, email)
	if err != nil || u.HasOwner() {
		t.Fatalf("a user made by the environment account: %+v %v, want none owning it", u, err)
	}
	if err := a.Delete(ctx, email); err != nil {
		t.Fatalf("delete of a user without an owner: %v", err)
	}
	if _, err := c.GetUser(ctx, email); !marzban.IsNotFound(err) {
		t.Fatalf("the user is still there: %v", err)
	}
	// The panel's own delete is what fails: this is what the adapter works around.
	email2, _ := itClient(t, ctx, a, time.Now().Add(time.Hour).UnixMilli(), 0, nil)
	if err := c.DeleteUser(ctx, email2); !marzban.IsServerError(err) {
		t.Logf("Marzban deleted a user without an owner cleanly (%v): the workaround may be unnecessary on this version", err)
	}
	if _, err := c.GetUser(ctx, email2); !marzban.IsNotFound(err) {
		t.Fatalf("the user is still there after the panel's own delete: %v", err)
	}
}
