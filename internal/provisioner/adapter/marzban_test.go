package adapter

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/marzban"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/marzbanfake"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/xui"
)

func marzbanAdapter(t *testing.T) (*Marzban, *marzbanfake.Server) {
	t.Helper()
	fake := marzbanfake.NewServer("admin", "s3cret:pw")
	t.Cleanup(fake.Close)
	a, err := NewMarzban(fake.URL, "admin:s3cret:pw", xui.AllowInsecureHTTP(), xui.AllowPrivateAddresses())
	if err != nil {
		t.Fatal(err)
	}
	return a, fake
}

func TestMarzbanLifecycle(t *testing.T) {
	a, fake := marzbanAdapter(t)
	ctx := context.Background()
	email := "u42-abcd1234"
	expiry := time.Now().Add(30 * 24 * time.Hour).UnixMilli()

	ins, err := a.Inbounds(ctx)
	if err != nil || len(ins) != 3 {
		t.Fatalf("Inbounds = %v, %v", ins, err)
	}
	for _, in := range ins {
		if in.ID <= 0 {
			t.Fatalf("inbound id %d is not positive", in.ID)
		}
	}
	vless := ins[0].ID
	for _, in := range ins {
		if in.Protocol == "vless" {
			vless = in.ID
		}
	}

	if err := a.CreateClient(ctx, email, "sub1", expiry, 1<<30, []int{vless}); err != nil {
		t.Fatalf("CreateClient: %v", err)
	}
	snap, _ := fake.Snapshot(email)
	if snap.Expire != expiry/1000 || snap.DataLimit != 1<<30 || snap.Status != "active" {
		t.Fatalf("created = %+v", snap)
	}
	if len(snap.Protocols) != 1 || snap.Protocols[0] != "vless" {
		t.Fatalf("protocols = %v", snap.Protocols)
	}
	if id, err := a.ClientSubID(ctx, email); err != nil || id != "sub1" {
		t.Fatalf("ClientSubID = %q, %v", id, err)
	}
	if err := a.CreateClient(ctx, email, "sub1", expiry, 1<<30, []int{vless}); !marzban.IsConflict(err) {
		t.Fatalf("duplicate create = %v, want conflict", err)
	}

	if err := a.Renew(ctx, email, 7, 1<<20); err != nil {
		t.Fatalf("Renew: %v", err)
	}
	snap, _ = fake.Snapshot(email)
	if snap.Expire != expiry/1000+7*86400 || snap.DataLimit != 1<<30+1<<20 {
		t.Fatalf("renewed = %+v", snap)
	}

	fake.AddUsage(email, 500)
	if u, err := a.Usage(ctx, email); err != nil || u != 500 {
		t.Fatalf("Usage = %d, %v", u, err)
	}
	st, err := a.Status(ctx, email)
	if err != nil || !st.Enabled || st.UsedBytes != 500 || st.ExpiryMs != (expiry/1000+7*86400)*1000 {
		t.Fatalf("Status = %+v, %v", st, err)
	}

	if err := a.SetEnabled(ctx, email, false); err != nil {
		t.Fatal(err)
	}
	if st, _ := a.Status(ctx, email); st.Enabled {
		t.Fatal("still enabled")
	}
	if err := a.SetLimits(ctx, email, expiry, 2<<30); err != nil {
		t.Fatal(err)
	}
	if st, _ := a.Status(ctx, email); !st.Enabled || st.TotalBytes != 2<<30 {
		t.Fatalf("SetLimits did not re-enable: %+v", st)
	}

	links, err := a.Links(ctx, email)
	if err != nil || len(links) != 1 || !strings.HasPrefix(links[0], "vless://") {
		t.Fatalf("Links = %v, %v", links, err)
	}
	if err := a.ResetTraffic(ctx, email); err != nil {
		t.Fatal(err)
	}
	if u, _ := a.Usage(ctx, email); u != 0 {
		t.Fatalf("usage after reset = %d", u)
	}
	if err := a.Delete(ctx, email); err != nil || fake.Has(email) {
		t.Fatalf("Delete: %v has=%v", err, fake.Has(email))
	}
	if _, err := a.Status(ctx, email); !marzban.IsNotFound(err) {
		t.Fatalf("Status after delete = %v, want not found", err)
	}
}

func TestMarzbanOnHoldAndUnlimited(t *testing.T) {
	a, fake := marzbanAdapter(t)
	ctx := context.Background()

	// Negative expiry: the clock starts on first use.
	if err := a.CreateClient(ctx, "u1-hold0001", "s", -3*86400*1000, 0, nil); err != nil {
		t.Fatalf("CreateClient on hold: %v", err)
	}
	snap, _ := fake.Snapshot("u1-hold0001")
	if snap.Status != "on_hold" || snap.HoldDuration != 3*86400 || len(snap.Protocols) != 3 {
		t.Fatalf("on-hold = %+v", snap)
	}
	st, _ := a.Status(ctx, "u1-hold0001")
	if !st.Enabled || st.ExpiryMs != -3*86400*1000 {
		t.Fatalf("on-hold status = %+v", st)
	}
	if err := a.Renew(ctx, "u1-hold0001", 2, 0); err != nil {
		t.Fatal(err)
	}
	if snap, _ = fake.Snapshot("u1-hold0001"); snap.HoldDuration != 5*86400 {
		t.Fatalf("hold after renew = %d", snap.HoldDuration)
	}

	// Unlimited: Renew refuses, as the 3x-ui adapter does.
	if err := a.CreateClient(ctx, "u1-unlim001", "s", 0, 0, nil); err != nil {
		t.Fatal(err)
	}
	err := a.Renew(ctx, "u1-unlim001", 7, 0)
	if err == nil || !strings.Contains(err.Error(), "renew skipped") {
		t.Fatalf("Renew unlimited = %v", err)
	}
}

func TestMarzbanLimitedClientRecoversOnRenew(t *testing.T) {
	a, fake := marzbanAdapter(t)
	ctx := context.Background()
	email := "u2-limit0001"
	if err := a.CreateClient(ctx, email, "s", time.Now().Add(time.Hour).UnixMilli(), 1000, nil); err != nil {
		t.Fatal(err)
	}
	fake.AddUsage(email, 2000)
	if st, _ := a.Status(ctx, email); st.Enabled && st.UsedBytes >= st.TotalBytes {
		// limited counts as not enabled
		t.Fatalf("limited client reported enabled: %+v", st)
	}
	if err := a.Renew(ctx, email, 0, 5000); err != nil {
		t.Fatal(err)
	}
	if st, _ := a.Status(ctx, email); !st.Enabled {
		t.Fatalf("not active after more quota: %+v", st)
	}
}

func TestMarzbanUnknownInboundAndStrangerSubID(t *testing.T) {
	a, fake := marzbanAdapter(t)
	ctx := context.Background()
	if err := a.CreateClient(ctx, "u3-nope0001", "s", 0, 0, []int{12345}); err == nil {
		t.Fatal("unknown inbound id accepted")
	}
	fake.Seed("operator-user", "made by hand", 0, 0)
	if id, err := a.ClientSubID(ctx, "operator-user"); err != nil || id != "" {
		t.Fatalf("stranger sub id = %q, %v", id, err)
	}
}

func TestMarzbanInboundIDsAreStable(t *testing.T) {
	a, _ := marzbanAdapter(t)
	first, _ := a.Inbounds(context.Background())
	second, _ := a.Inbounds(context.Background())
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("inbound %d changed: %+v vs %+v", i, first[i], second[i])
		}
	}
	seen := map[int]bool{}
	for _, in := range first {
		if seen[in.ID] {
			t.Fatalf("duplicate id %d", in.ID)
		}
		seen[in.ID] = true
	}
}

func TestMarzbanSubscriptionLink(t *testing.T) {
	a, fake := marzbanAdapter(t)
	ctx := context.Background()
	if err := a.CreateClient(ctx, "u4-sub00001", "s", 0, 0, nil); err != nil {
		t.Fatal(err)
	}
	rel, err := a.SubscriptionLink(ctx, "u4-sub00001", "")
	if err != nil || !strings.HasPrefix(rel, fake.URL+"/sub/") {
		t.Fatalf("relative link = %q, %v", rel, err)
	}
	tok := rel[strings.LastIndex(rel, "/")+1:]
	over, _ := a.SubscriptionLink(ctx, "u4-sub00001", "https://cdn.example/s/")
	if over != "https://cdn.example/s/"+tok {
		t.Fatalf("override = %q", over)
	}
	fake.SetSubscriptionPrefix("https://sub.example")
	abs, _ := a.SubscriptionLink(ctx, "u4-sub00001", "")
	if abs != "https://sub.example/sub/"+tok {
		t.Fatalf("absolute = %q", abs)
	}
}

func TestMarzbanHealthAndVersion(t *testing.T) {
	a, fake := marzbanAdapter(t)
	ctx := context.Background()
	v, err := a.HealthCheck(ctx)
	if err != nil || v == "" {
		t.Fatalf("HealthCheck = %q, %v", v, err)
	}
	if v != "v0.8.4" {
		t.Fatalf("version = %q", v)
	}
	fake.SetFailNext(5)
	if got := a.PanelVersion(ctx); got != "" {
		t.Fatalf("PanelVersion on failure = %q", got)
	}
}

func TestMarzbanRelogsInWhenTokenExpires(t *testing.T) {
	a, fake := marzbanAdapter(t)
	ctx := context.Background()
	if _, err := a.Inbounds(ctx); err != nil {
		t.Fatal(err)
	}
	before := fake.Logins()
	fake.ExpireTokens()
	if _, err := a.Inbounds(ctx); err != nil {
		t.Fatalf("after expiry: %v", err)
	}
	if fake.Logins() != before+1 {
		t.Fatalf("logins %d -> %d, want one re-login", before, fake.Logins())
	}
}

func TestMarzbanWrongPassword(t *testing.T) {
	fake := marzbanfake.NewServer("admin", "right")
	t.Cleanup(fake.Close)
	a, err := NewMarzban(fake.URL, "admin:wrong", xui.AllowInsecureHTTP(), xui.AllowPrivateAddresses())
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.HealthCheck(context.Background())
	if !marzban.IsUnauthorized(err) {
		t.Fatalf("HealthCheck = %v, want unauthorized", err)
	}
}

func TestNewMarzbanRejectsBadCredentials(t *testing.T) {
	for _, c := range []string{"", "nocolon", ":pw", "user:"} {
		if _, err := NewMarzban("https://panel.example", c); err == nil {
			t.Errorf("credentials %q accepted", c)
		}
	}
}
