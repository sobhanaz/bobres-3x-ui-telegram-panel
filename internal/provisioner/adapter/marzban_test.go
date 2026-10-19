package adapter

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"slices"
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
	if len(snap.Protocols) != 1 || snap.Protocols[0] != "vless" || !slices.Equal(snap.Tags["vless"], []string{"VLESS TCP REALITY"}) {
		t.Fatalf("protocols = %v, tags = %v", snap.Protocols, snap.Tags)
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
	if err := a.Delete(ctx, email); !marzban.IsNotFound(err) {
		t.Fatalf("Delete of a deleted user = %v, want not found", err)
	}
}

func TestMarzbanOnHoldAndUnlimited(t *testing.T) {
	a, fake := marzbanAdapter(t)
	ctx := context.Background()

	// Negative expiry: the clock starts on first use. No inbound chosen means
	// all of them, every tag named.
	if err := a.CreateClient(ctx, "u11-hold0001", "s", -3*86400*1000, 0, nil); err != nil {
		t.Fatalf("CreateClient on hold: %v", err)
	}
	snap, _ := fake.Snapshot("u11-hold0001")
	if snap.Status != "on_hold" || snap.HoldDuration != 3*86400 || len(snap.Protocols) != 3 {
		t.Fatalf("on-hold = %+v", snap)
	}
	st, _ := a.Status(ctx, "u11-hold0001")
	if !st.Enabled || st.ExpiryMs != -3*86400*1000 {
		t.Fatalf("on-hold status = %+v", st)
	}
	if err := a.Renew(ctx, "u11-hold0001", 2, 0); err != nil {
		t.Fatal(err)
	}
	if snap, _ = fake.Snapshot("u11-hold0001"); snap.HoldDuration != 5*86400 {
		t.Fatalf("hold after renew = %d", snap.HoldDuration)
	}

	// The first connection starts the clock; the client then reads as active
	// with an absolute expiry.
	before := time.Now().Unix()
	fake.AddUsage("u11-hold0001", 10)
	st, _ = a.Status(ctx, "u11-hold0001")
	if !st.Enabled || st.ExpiryMs < (before+5*86400)*1000 {
		t.Fatalf("status after first use = %+v", st)
	}

	// Unlimited: Renew refuses, as the 3x-ui adapter does.
	if err := a.CreateClient(ctx, "u11-unlim001", "s", 0, 0, nil); err != nil {
		t.Fatal(err)
	}
	err := a.Renew(ctx, "u11-unlim001", 7, 0)
	if err == nil || !strings.Contains(err.Error(), "renew skipped") {
		t.Fatalf("Renew unlimited = %v", err)
	}
}

func TestMarzbanLimitedClientRecoversOnRenew(t *testing.T) {
	a, fake := marzbanAdapter(t)
	ctx := context.Background()
	email := "u12-limit0001"
	if err := a.CreateClient(ctx, email, "s", time.Now().Add(time.Hour).UnixMilli(), 1000, nil); err != nil {
		t.Fatal(err)
	}
	fake.AddUsage(email, 2000)
	if st, _ := a.Status(ctx, email); st.Enabled {
		t.Fatalf("limited client reported enabled: %+v", st)
	}
	if err := a.Renew(ctx, email, 0, 5000); err != nil {
		t.Fatal(err)
	}
	if st, _ := a.Status(ctx, email); !st.Enabled {
		t.Fatalf("not active after more quota: %+v", st)
	}
}

// An expired client is active again when SetLimits gives it a new expiry (a
// renewal), and Renew alone cannot do it while the new expiry is still past.
func TestMarzbanExpiredClientRecoversOnSetLimits(t *testing.T) {
	a, fake := marzbanAdapter(t)
	ctx := context.Background()
	email := "u13-expire001"
	if err := a.CreateClient(ctx, email, "s", time.Now().Add(time.Hour).UnixMilli(), 1<<30, nil); err != nil {
		t.Fatal(err)
	}
	if err := a.SetLimits(ctx, email, time.Now().Add(-time.Minute).UnixMilli(), 1<<30); err != nil {
		t.Fatal(err)
	}
	if st, _ := a.Status(ctx, email); st.Enabled {
		t.Fatalf("a client past its expiry reads as enabled: %+v", st)
	}
	if snap, _ := fake.Snapshot(email); snap.Status != "expired" {
		t.Fatalf("status = %q, want expired", snap.Status)
	}
	if err := a.SetLimits(ctx, email, time.Now().Add(72*time.Hour).UnixMilli(), 1<<30); err != nil {
		t.Fatal(err)
	}
	if st, _ := a.Status(ctx, email); !st.Enabled {
		t.Fatalf("not active after a renewal: %+v", st)
	}
}

// The panel's review job (every 10 s there) marks a client expired once its
// time has passed, and a renewal brings it back.
func TestMarzbanClientExpiresWithTheClock(t *testing.T) {
	a, fake := marzbanAdapter(t)
	ctx := context.Background()
	start := time.Now()
	fake.SetClock(func() time.Time { return start })
	email := "u13-clock0001"
	if err := a.CreateClient(ctx, email, "s", start.Add(time.Hour).UnixMilli(), 0, nil); err != nil {
		t.Fatal(err)
	}
	fake.SetClock(func() time.Time { return start.Add(2 * time.Hour) })
	if st, _ := a.Status(ctx, email); !st.Enabled {
		t.Fatal("expired before the review job ran")
	}
	fake.Review()
	if st, _ := a.Status(ctx, email); st.Enabled {
		t.Fatalf("still enabled after its time: %+v", st)
	}
	if err := a.SetLimits(ctx, email, start.Add(48*time.Hour).UnixMilli(), 0); err != nil {
		t.Fatal(err)
	}
	if st, _ := a.Status(ctx, email); !st.Enabled {
		t.Fatalf("not active after a renewal: %+v", st)
	}
}

func TestMarzbanUnknownInboundAndStrangerSubID(t *testing.T) {
	a, fake := marzbanAdapter(t)
	ctx := context.Background()
	if err := a.CreateClient(ctx, "u13-nope0001", "s", 0, 0, []int{12345}); err == nil {
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

// Real Marzban v0.8 attaches no inbound to a user that names none, so a client
// meant for all of them (or for the same one twice) must carry the tags.
func TestMarzbanInboundsAreAlwaysNamed(t *testing.T) {
	a, fake := marzbanAdapter(t)
	ctx := context.Background()
	if err := a.CreateClient(ctx, "u14-all000001", "s", 0, 0, nil); err != nil {
		t.Fatal(err)
	}
	if links, err := a.Links(ctx, "u14-all000001"); err != nil || len(links) != 3 {
		t.Fatalf("a client for all inbounds has %d links, %v", len(links), err)
	}
	ins, _ := a.Inbounds(ctx)
	if err := a.CreateClient(ctx, "u14-two000001", "s", 0, 0, []int{ins[0].ID, ins[0].ID, ins[1].ID}); err != nil {
		t.Fatal(err)
	}
	snap, _ := fake.Snapshot("u14-two000001")
	if n := len(snap.Tags[ins[0].Protocol]) + len(snap.Tags[ins[1].Protocol]); n != 2 || len(snap.Protocols) != 2 {
		t.Fatalf("tags = %v, protocols = %v", snap.Tags, snap.Protocols)
	}
}

func TestMarzbanSubscriptionLink(t *testing.T) {
	a, fake := marzbanAdapter(t)
	ctx := context.Background()
	if err := a.CreateClient(ctx, "u14-sub00001", "s", 0, 0, nil); err != nil {
		t.Fatal(err)
	}
	// The panel makes a new token for every read; with a stopped clock the
	// reads below agree.
	fake.SetClock(func() time.Time { return time.Unix(1_800_000_000, 0) })
	rel, err := a.SubscriptionLink(ctx, "u14-sub00001", "")
	if err != nil || !strings.HasPrefix(rel, fake.URL+"/sub/") {
		t.Fatalf("relative link = %q, %v", rel, err)
	}
	tok := rel[strings.LastIndex(rel, "/")+1:]
	over, _ := a.SubscriptionLink(ctx, "u14-sub00001", "https://cdn.example/s/")
	if over != "https://cdn.example/s/"+tok {
		t.Fatalf("override = %q", over)
	}
	fake.SetSubscriptionPrefix("https://sub.example")
	abs, _ := a.SubscriptionLink(ctx, "u14-sub00001", "")
	if abs != "https://sub.example/sub/"+tok {
		t.Fatalf("absolute = %q", abs)
	}
}

// The link the adapter hands out serves the client's share links.
func TestMarzbanSubscriptionLinkServesTheLinks(t *testing.T) {
	a, _ := marzbanAdapter(t)
	ctx := context.Background()
	if err := a.CreateClient(ctx, "u14-serve0001", "s", 0, 0, nil); err != nil {
		t.Fatal(err)
	}
	link, err := a.SubscriptionLink(ctx, "u14-serve0001", "")
	if err != nil {
		t.Fatal(err)
	}
	hc := &http.Client{Timeout: 10 * time.Second}
	resp, err := hc.Get(link)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	dec, derr := base64.StdEncoding.DecodeString(string(raw))
	if resp.StatusCode != http.StatusOK || derr != nil || len(strings.Split(string(dec), "\n")) != 3 {
		t.Fatalf("subscription = %d %q (%v)", resp.StatusCode, raw, derr)
	}
	resp2, err := hc.Get(link + "x") // a token that does not verify
	if err != nil {
		t.Fatal(err)
	}
	_ = resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("a wrong token gave HTTP %d, want 404", resp2.StatusCode)
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

// Marzban refuses to put an on-hold client back on hold unless the request
// repeats the duration (HTTP 422), so turning a never-used client off and on
// must keep it waiting for its first use.
func TestMarzbanEnableKeepsTheHold(t *testing.T) {
	a, fake := marzbanAdapter(t)
	ctx := context.Background()
	email := "u15-holdon001"
	if err := a.CreateClient(ctx, email, "s", -2*86400*1000, 1<<30, nil); err != nil {
		t.Fatal(err)
	}
	if err := a.SetEnabled(ctx, email, false); err != nil {
		t.Fatal(err)
	}
	if st, _ := a.Status(ctx, email); st.Enabled {
		t.Fatalf("still enabled: %+v", st)
	}
	if err := a.SetEnabled(ctx, email, true); err != nil {
		t.Fatalf("enable: %v", err)
	}
	snap, _ := fake.Snapshot(email)
	if snap.Status != "on_hold" || snap.HoldDuration != 2*86400 || snap.Expire != 0 {
		t.Fatalf("after off and on = %+v, want on hold for two days with no expiry", snap)
	}

	// A client that has been used comes back active, not on hold.
	if err := a.CreateClient(ctx, "u15-used0001", "s", time.Now().Add(time.Hour).UnixMilli(), 0, nil); err != nil {
		t.Fatal(err)
	}
	if err := a.SetEnabled(ctx, "u15-used0001", false); err != nil {
		t.Fatal(err)
	}
	if err := a.SetEnabled(ctx, "u15-used0001", true); err != nil {
		t.Fatal(err)
	}
	if snap, _ := fake.Snapshot("u15-used0001"); snap.Status != "active" {
		t.Fatalf("status = %q, want active", snap.Status)
	}
}

// The panel's reset turns every user on that is not expired; resetting the
// traffic must not switch a disabled client back on or end an on-hold wait.
func TestMarzbanResetKeepsTheStatus(t *testing.T) {
	a, fake := marzbanAdapter(t)
	ctx := context.Background()

	if err := a.CreateClient(ctx, "u16-off00001", "s", time.Now().Add(time.Hour).UnixMilli(), 1000, nil); err != nil {
		t.Fatal(err)
	}
	fake.AddUsage("u16-off00001", 300)
	if err := a.SetEnabled(ctx, "u16-off00001", false); err != nil {
		t.Fatal(err)
	}
	if err := a.ResetTraffic(ctx, "u16-off00001"); err != nil {
		t.Fatal(err)
	}
	if snap, _ := fake.Snapshot("u16-off00001"); snap.Status != "disabled" || snap.Used != 0 {
		t.Fatalf("disabled client after a reset = %+v", snap)
	}

	if err := a.CreateClient(ctx, "u16-hold0001", "s", -86400*1000, 1000, nil); err != nil {
		t.Fatal(err)
	}
	if err := a.ResetTraffic(ctx, "u16-hold0001"); err != nil {
		t.Fatal(err)
	}
	if snap, _ := fake.Snapshot("u16-hold0001"); snap.Status != "on_hold" || snap.HoldDuration != 86400 || snap.Expire != 0 {
		t.Fatalf("on-hold client after a reset = %+v", snap)
	}

	// Over its quota, a client is active again: its counter is zero.
	if err := a.CreateClient(ctx, "u16-over0001", "s", time.Now().Add(time.Hour).UnixMilli(), 1000, nil); err != nil {
		t.Fatal(err)
	}
	fake.AddUsage("u16-over0001", 5000)
	if snap, _ := fake.Snapshot("u16-over0001"); snap.Status != "limited" {
		t.Fatalf("status = %q, want limited", snap.Status)
	}
	if err := a.ResetTraffic(ctx, "u16-over0001"); err != nil {
		t.Fatal(err)
	}
	if snap, _ := fake.Snapshot("u16-over0001"); snap.Status != "active" || snap.Used != 0 {
		t.Fatalf("limited client after a reset = %+v", snap)
	}

	// An expired client stays expired.
	if err := a.CreateClient(ctx, "u16-late0001", "s", time.Now().Add(time.Hour).UnixMilli(), 0, nil); err != nil {
		t.Fatal(err)
	}
	if err := a.SetLimits(ctx, "u16-late0001", time.Now().Add(-time.Hour).UnixMilli(), 0); err != nil {
		t.Fatal(err)
	}
	if err := a.ResetTraffic(ctx, "u16-late0001"); err != nil {
		t.Fatal(err)
	}
	if snap, _ := fake.Snapshot("u16-late0001"); snap.Status != "expired" {
		t.Fatalf("expired client after a reset = %+v", snap)
	}
	if err := a.ResetTraffic(ctx, "u16-nobody001"); !marzban.IsNotFound(err) {
		t.Fatalf("reset of a missing client = %v, want not found", err)
	}
}

// A user made through the panel's SUDO_USERNAME account has no owner admin;
// the panel then deletes the row, answers 500 and leaves the account in the
// running core. The adapter disables it first and counts the 500 as done.
func TestMarzbanDeleteOfAnOwnerlessUser(t *testing.T) {
	a, fake := marzbanAdapter(t)
	ctx := context.Background()
	fake.SetOwnerless(true)
	for _, email := range []string{"u17-ghost0001", "u17-ghost0002"} {
		if err := a.CreateClient(ctx, email, "s", time.Now().Add(time.Hour).UnixMilli(), 0, nil); err != nil {
			t.Fatal(err)
		}
	}
	if snap, _ := fake.Snapshot("u17-ghost0001"); snap.Owner != "" {
		t.Fatalf("owner = %q, want none", snap.Owner)
	}
	if err := a.Delete(ctx, "u17-ghost0001"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if fake.Has("u17-ghost0001") || len(fake.Ghosts()) != 0 {
		t.Fatalf("has = %v, accounts left in the core: %v", fake.Has("u17-ghost0001"), fake.Ghosts())
	}
	// A user already off is deleted the same way.
	if err := a.SetEnabled(ctx, "u17-ghost0002", false); err != nil {
		t.Fatal(err)
	}
	if err := a.Delete(ctx, "u17-ghost0002"); err != nil || len(fake.Ghosts()) != 0 {
		t.Fatalf("Delete of a disabled one: %v, accounts left: %v", err, fake.Ghosts())
	}
	if err := a.Delete(ctx, "u17-ghost0002"); !marzban.IsNotFound(err) {
		t.Fatalf("Delete of a deleted user = %v, want not found", err)
	}

	// A panel that fails is not a deletion: the user stays and the error is
	// reported.
	fake.SetOwnerless(false)
	if err := a.CreateClient(ctx, "u17-owned0001", "s", 0, 0, nil); err != nil {
		t.Fatal(err)
	}
	fake.SetFailNext(1)
	if err := a.Delete(ctx, "u17-owned0001"); !marzban.IsServerError(err) || !fake.Has("u17-owned0001") {
		t.Fatalf("Delete during an outage = %v, has = %v", err, fake.Has("u17-owned0001"))
	}
}

// The local rule is the panel's, so a name the panel would refuse never
// leaves the server (and never takes a reservation).
func TestMarzbanRefusesWhatThePanelRefuses(t *testing.T) {
	a, fake := marzbanAdapter(t)
	err := a.CreateClient(context.Background(), "u1-abcd1234", "s", 0, 0, nil)
	if !errors.Is(err, marzban.ErrInvalidUsername) {
		t.Fatalf("CreateClient(u1-...) = %v, want an invalid username", err)
	}
	if n := fake.Calls("create"); n != 0 {
		t.Fatalf("the panel was asked %d times", n)
	}
}
