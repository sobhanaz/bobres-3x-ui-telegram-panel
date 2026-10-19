//go:build integration

package adapter

// Real traffic through a real Marzban panel: a client of the adapter connects
// to the panel's Shadowsocks inbound with the credentials the panel made for
// it (ss_integration_test.go), and the tests watch what the panel's jobs make
// of it. This is the only way to see usage counted, a quota enforced and a
// switched-off client really kept out. Needs, besides the variables of
// marzban_integration_test.go:
//
//	MARZBAN_TEST_SS_ADDR=127.0.0.1:1080       the panel's Shadowsocks inbound, as reached from the test
//	MARZBAN_TEST_SS_TARGET=127.0.0.1:8000     an HTTP server the PANEL can reach (its own address
//	                                          will do), and no rule in its Xray routing that blocks
//	                                          private addresses (tools/marzbandev removes it)
//
// The panel's jobs run every 10 seconds by default (JOB_RECORD_USER_USAGES_INTERVAL
// and JOB_REVIEW_USERS_INTERVAL); CI sets them to 2.

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

func trafficEnv(t *testing.T) (ssAddr, target string) {
	t.Helper()
	ssAddr, target = os.Getenv("MARZBAN_TEST_SS_ADDR"), os.Getenv("MARZBAN_TEST_SS_TARGET")
	if ssAddr == "" || target == "" {
		if os.Getenv("MARZBAN_TEST_REQUIRE") == "1" {
			t.Fatal("MARZBAN_TEST_SS_ADDR and MARZBAN_TEST_SS_TARGET not set and MARZBAN_TEST_REQUIRE=1")
		}
		t.Skip("MARZBAN_TEST_SS_ADDR and MARZBAN_TEST_SS_TARGET not set")
	}
	return ssAddr, target
}

// shadowsocksInbound is the id of the panel's Shadowsocks inbound.
func shadowsocksInbound(t *testing.T, ctx context.Context, a *Marzban) int {
	t.Helper()
	ins, err := a.Inbounds(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range ins {
		if in.Protocol == "shadowsocks" {
			return in.ID
		}
	}
	t.Skip("the panel has no Shadowsocks inbound")
	return 0
}

// ssPassword is the password the panel made for a client's Shadowsocks account.
func ssPassword(t *testing.T, ctx context.Context, a *Marzban, email string) string {
	t.Helper()
	u, err := a.c.GetUser(ctx, email)
	if err != nil {
		t.Fatal(err)
	}
	var acct struct {
		Password string `json:"password"`
		Method   string `json:"method"`
	}
	if err := json.Unmarshal(u.Proxies["shadowsocks"], &acct); err != nil || acct.Password == "" {
		t.Fatalf("shadowsocks account of %s: %s %v", email, u.Proxies["shadowsocks"], err)
	}
	if acct.Method != "chacha20-ietf-poly1305" {
		t.Skipf("the panel made a %q account; the test client speaks chacha20-ietf-poly1305", acct.Method)
	}
	return acct.Password
}

const trafficRequest = "GET / HTTP/1.0\r\nHost: panel\r\nConnection: close\r\n\r\n"

// passes reports whether the panel's core carries this client's traffic: the
// request comes back as an HTTP answer.
func passes(ssAddr, password, target string) (bool, int) {
	got, err := ssRequest(ssAddr, password, []byte(trafficRequest), target, 4*time.Second)
	return err == nil && strings.HasPrefix(string(got), "HTTP/1."), len(got)
}

// waitUntil polls cond until it holds or the time is up.
func waitUntil(t *testing.T, within time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("%s did not happen within %s", what, within)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func TestIntegrationMarzbanTrafficQuota(t *testing.T) {
	ssAddr, target := trafficEnv(t)
	a, ctx := realMarzban(t)
	email, _ := itClient(t, ctx, a, time.Now().Add(time.Hour).UnixMilli(), 4096, []int{shadowsocksInbound(t, ctx, a)})
	pw := ssPassword(t, ctx, a, email)

	// Traffic passes, the panel counts it, and past the quota the client is
	// limited: the status says so and the core stops carrying it.
	if ok, n := passes(ssAddr, pw, target); !ok {
		t.Fatalf("a new client's request did not get through (%d bytes back)", n)
	}
	waitUntil(t, 60*time.Second, "the usage being counted", func() bool {
		used, err := a.Usage(ctx, email)
		return err == nil && used > 0
	})
	waitUntil(t, 60*time.Second, "the client being limited", func() bool {
		st, err := a.Status(ctx, email)
		return err == nil && !st.Enabled
	})
	st, err := a.Status(ctx, email)
	if err != nil || st.Enabled || st.UsedBytes < st.TotalBytes || st.TotalBytes != 4096 {
		t.Fatalf("a client over its quota: %+v %v", st, err)
	}
	waitUntil(t, 20*time.Second, "the core dropping the limited client", func() bool {
		ok, _ := passes(ssAddr, pw, target)
		return !ok
	})
	used := st.UsedBytes

	// More quota: the client is active again and the core carries it again
	// (the panel adds the account back in the background).
	if err := a.Renew(ctx, email, 0, 10<<20); err != nil {
		t.Fatalf("top-up: %v", err)
	}
	if st, err := a.Status(ctx, email); err != nil || !st.Enabled || st.TotalBytes != 4096+10<<20 || st.UsedBytes < used {
		t.Fatalf("after the top-up: %+v %v", st, err)
	}
	waitUntil(t, 20*time.Second, "the core carrying the client again", func() bool {
		ok, _ := passes(ssAddr, pw, target)
		return ok
	})
	waitUntil(t, 60*time.Second, "the usage growing", func() bool {
		u, err := a.Usage(ctx, email)
		return err == nil && u > used
	})

	// A reset zeroes the counter and keeps the client carried.
	if err := a.ResetTraffic(ctx, email); err != nil {
		t.Fatal(err)
	}
	if st, err := a.Status(ctx, email); err != nil || !st.Enabled || st.UsedBytes != 0 {
		t.Fatalf("after the reset: %+v %v", st, err)
	}
	if ok, _ := passes(ssAddr, pw, target); !ok {
		t.Fatal("after the reset the core no longer carries the client")
	}
}

// Off means off: a disabled client is kept out by the core, a traffic reset
// does not let it back in, and switching it on does.
func TestIntegrationMarzbanTrafficDisabled(t *testing.T) {
	ssAddr, target := trafficEnv(t)
	a, ctx := realMarzban(t)
	email, _ := itClient(t, ctx, a, time.Now().Add(time.Hour).UnixMilli(), 1<<30, []int{shadowsocksInbound(t, ctx, a)})
	pw := ssPassword(t, ctx, a, email)
	if ok, _ := passes(ssAddr, pw, target); !ok {
		t.Fatal("a new client's request did not get through")
	}

	blocked := func(step string) {
		t.Helper()
		waitUntil(t, 20*time.Second, "the core keeping the client out ("+step+")", func() bool {
			ok, _ := passes(ssAddr, pw, target)
			return !ok
		})
		if ok, _ := passes(ssAddr, pw, target); ok { // and it stays out
			t.Fatalf("%s: the core carries a disabled client", step)
		}
	}
	if err := a.SetEnabled(ctx, email, false); err != nil {
		t.Fatal(err)
	}
	blocked("disabled")
	if err := a.ResetTraffic(ctx, email); err != nil {
		t.Fatal(err)
	}
	if st, err := a.Status(ctx, email); err != nil || st.Enabled {
		t.Fatalf("a reset switched a disabled client on: %+v %v", st, err)
	}
	blocked("reset while disabled")

	if err := a.SetEnabled(ctx, email, true); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 20*time.Second, "the core carrying the client again", func() bool {
		ok, _ := passes(ssAddr, pw, target)
		return ok
	})

	// Deleted means gone from the core as well.
	if err := a.Delete(ctx, email); err != nil {
		t.Fatal(err)
	}
	blocked("deleted")
}

// A client made through the panel's SUDO_USERNAME account has no owner admin,
// and the panel's own delete of it fails (HTTP 500) before it drops the account
// from the core: a deleted customer would keep connecting. The adapter's
// delete must leave the core without the account.
func TestIntegrationMarzbanTrafficOwnerlessDelete(t *testing.T) {
	ssAddr, target := trafficEnv(t)
	a, ctx := envAccountMarzban(t)
	email, _ := itClient(t, ctx, a, time.Now().Add(time.Hour).UnixMilli(), 1<<30, []int{shadowsocksInbound(t, ctx, a)})
	pw := ssPassword(t, ctx, a, email)
	if ok, _ := passes(ssAddr, pw, target); !ok {
		t.Fatal("a new client's request did not get through")
	}
	if err := a.Delete(ctx, email); err != nil {
		t.Fatalf("delete: %v", err)
	}
	waitUntil(t, 20*time.Second, "the core dropping the deleted client", func() bool {
		ok, _ := passes(ssAddr, pw, target)
		return !ok
	})
	if ok, _ := passes(ssAddr, pw, target); ok {
		t.Fatal("the core carries a deleted client")
	}
}

// The panel starts the clock of an on-hold client when it first connects.
func TestIntegrationMarzbanTrafficFirstUse(t *testing.T) {
	ssAddr, target := trafficEnv(t)
	a, ctx := realMarzban(t)
	email, _ := itClient(t, ctx, a, -2*86400*1000, 0, []int{shadowsocksInbound(t, ctx, a)})
	pw := ssPassword(t, ctx, a, email)
	if st, err := a.Status(ctx, email); err != nil || !st.Enabled || st.ExpiryMs != -2*86400*1000 {
		t.Fatalf("on hold: %+v %v", st, err)
	}
	// An on-hold client is carried (it is not off).
	before := time.Now().Unix()
	if ok, _ := passes(ssAddr, pw, target); !ok {
		t.Fatal("a client on hold could not connect")
	}
	waitUntil(t, 60*time.Second, "the clock starting", func() bool {
		st, err := a.Status(ctx, email)
		return err == nil && st.ExpiryMs > 0
	})
	st, err := a.Status(ctx, email)
	want := (before + 2*86400) * 1000
	if err != nil || !st.Enabled || st.ExpiryMs < want-5000 || st.ExpiryMs > want+120_000 {
		t.Fatalf("after the first use: %+v %v, want an expiry near %d", st, err, want)
	}
	// It is an ordinary client now, which a renewal extends in absolute terms.
	if err := a.Renew(ctx, email, 1, 0); err != nil {
		t.Fatal(err)
	}
	if got, err := a.Status(ctx, email); err != nil || got.ExpiryMs != st.ExpiryMs+86400*1000 {
		t.Fatalf("renewed: %+v %v", got, err)
	}
	if links, err := a.Links(ctx, email); err != nil || !slices.ContainsFunc(links, func(l string) bool { return strings.HasPrefix(l, "ss://") }) {
		t.Fatalf("links: %v %v", links, err)
	}
}
