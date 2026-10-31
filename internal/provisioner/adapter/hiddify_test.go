package adapter

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/hiddify"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/hiddifyfake"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/xui"
)

const hiddifyKey = "0b5e6a3c-2f4d-4e8a-9c1b-7d6e5f4a3b2c" //gitleaks:allow test fixture

func newHiddifyFake(t *testing.T) (*hiddifyfake.Server, *Hiddify) {
	t.Helper()
	fake := hiddifyfake.NewServer(hiddifyKey)
	t.Cleanup(fake.Close)
	a, err := NewHiddify(fake.URL, hiddifyKey, xui.AllowInsecureHTTP(), xui.AllowPrivateAddresses())
	if err != nil {
		t.Fatal(err)
	}
	return fake, a
}

func TestHiddifyUserUUID(t *testing.T) {
	a, b := HiddifyUserUUID("sub-a"), HiddifyUserUUID("sub-b")
	if a == b || !hiddify.ValidUUID(a) || a != HiddifyUserUUID("sub-a") || a[14] != '5' {
		t.Fatalf("uuids %q %q", a, b)
	}
}

func TestHiddifyLifecycle(t *testing.T) {
	fake, a := newHiddifyFake(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC)
	a.now = func() time.Time { return now }

	ins, err := a.Inbounds(ctx)
	if err != nil || len(ins) != 1 || ins[0].ID != HiddifyInboundID || !ins[0].Enabled {
		t.Fatalf("inbounds = %+v, %v", ins, err)
	}
	exp := time.Date(2026, 11, 29, 0, 0, 0, 0, time.UTC).UnixMilli()
	traffic := int64(10<<30 + 12345)
	if err := a.CreateClient(ctx, "u1-abcd", "sub1", exp, traffic, []int{HiddifyInboundID}); err != nil {
		t.Fatalf("create: %v", err)
	}
	sn, ok := fake.Snapshot("u1-abcd")
	if !ok || sn.UUID != HiddifyUserUUID("sub1") || sn.Comment != "bobres-sub:sub1" || sn.StartDate != "2026-10-30" || sn.PackageDays != 29 ||
		sn.LimitBytes != traffic || sn.Mode != "no_reset" || !sn.Enable {
		t.Fatalf("created = %+v", sn)
	}
	st, err := a.Status(ctx, "u1-abcd")
	if err != nil || st.ExpiryMs != exp || st.TotalBytes != traffic || !st.Enabled {
		t.Fatalf("status = %+v, %v (want expiry %d)", st, err, exp)
	}
	if id, err := a.ClientSubID(ctx, "u1-abcd"); err != nil || id != "sub1" {
		t.Fatalf("ClientSubID = %q %v", id, err)
	}
	links, err := a.Links(ctx, "u1-abcd")
	if err != nil || len(links) != 2 || !strings.HasPrefix(links[0], "vless://") {
		t.Fatalf("links = %v %v", links, err)
	}
	sl, err := a.SubscriptionLink(ctx, "u1-abcd", "")
	if err != nil || strings.Contains(sl, "asn=") || !strings.HasSuffix(sl, "/"+sn.UUID+"/auto/#u1-abcd") || !strings.Contains(sl, hiddifyfake.ClientPath) {
		t.Fatalf("subscription link = %q %v", sl, err)
	}
	if sl, _ := a.SubscriptionLink(ctx, "u1-abcd", "https://sub.example.com/cp/"); sl != "https://sub.example.com/cp/"+sn.UUID+"/auto/" {
		t.Fatalf("with a sub base = %q", sl)
	}
	if fake.Calls("GET users") != 1 {
		t.Fatalf("the client path was asked %d times", fake.Calls("GET users"))
	}

	fake.AddUsage("u1-abcd", 777)
	if u, err := a.Usage(ctx, "u1-abcd"); err != nil || u != 777 {
		t.Fatalf("usage = %d %v", u, err)
	}
	// An expiry in the afternoon ends at the midnight after that day.
	exp2 := time.Date(2026, 12, 15, 15, 0, 0, 0, time.UTC).UnixMilli()
	if err := a.SetLimits(ctx, "u1-abcd", exp2, 0); err != nil {
		t.Fatal(err)
	}
	if st, _ := a.Status(ctx, "u1-abcd"); st.ExpiryMs != time.Date(2026, 12, 16, 0, 0, 0, 0, time.UTC).UnixMilli() || st.TotalBytes != 0 || st.UsedBytes != 777 {
		t.Fatalf("after SetLimits = %+v", st)
	}
	if err := a.Renew(ctx, "u1-abcd", 1, 0); err != nil {
		t.Fatal(err)
	}
	if err := a.Renew(ctx, "u1-abcd", 0, 1<<30); err == nil || !strings.Contains(err.Error(), "unlimited") {
		t.Fatalf("renewing unlimited traffic: %v", err)
	}
	if st, _ := a.Status(ctx, "u1-abcd"); st.ExpiryMs != time.Date(2026, 12, 17, 0, 0, 0, 0, time.UTC).UnixMilli() {
		t.Fatalf("after Renew = %+v", st)
	}
	if err := a.SetLimits(ctx, "u1-abcd", 0, 5<<30); err != nil {
		t.Fatal(err)
	}
	if st, _ := a.Status(ctx, "u1-abcd"); st.ExpiryMs != 0 || st.TotalBytes != 5<<30 {
		t.Fatalf("unlimited expiry = %+v", st)
	}
	if err := a.Renew(ctx, "u1-abcd", 0, 1<<30); err != nil {
		t.Fatal(err)
	}
	if st, _ := a.Status(ctx, "u1-abcd"); st.TotalBytes != 6<<30 {
		t.Fatalf("renewed traffic = %+v", st)
	}
	if err := a.SetEnabled(ctx, "u1-abcd", false); err != nil {
		t.Fatal(err)
	}
	if st, _ := a.Status(ctx, "u1-abcd"); st.Enabled {
		t.Fatal("still enabled")
	}
	if err := a.ResetTraffic(ctx, "u1-abcd"); err != nil {
		t.Fatal(err)
	}
	if st, _ := a.Status(ctx, "u1-abcd"); st.Enabled || st.UsedBytes != 0 {
		t.Fatalf("after reset = %+v", st)
	}
	if err := a.Delete(ctx, "u1-abcd"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Status(ctx, "u1-abcd"); !hiddify.IsNotFound(err) || !hiddify.Answered(err) {
		t.Fatalf("deleted: %v", err)
	}
	if v := a.PanelVersion(ctx); v != "v14.0.5" {
		t.Fatalf("version = %q", v)
	}
}

func TestHiddifyExpiryMapping(t *testing.T) {
	a := &Hiddify{now: func() time.Time { return time.Date(2026, 10, 30, 23, 30, 0, 0, time.UTC) }}
	day := func(y int, m time.Month, d int) int64 { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC).UnixMilli() }
	for _, c := range []struct {
		in        int64
		start     any
		days      int64
		readBack  int64
		startNull bool
	}{
		{in: day(2026, 10, 31), start: "2026-10-30", days: 0, readBack: day(2026, 10, 31)},
		{in: day(2026, 10, 31) + 1, start: "2026-10-30", days: 1, readBack: day(2026, 11, 1)},
		{in: day(2026, 10, 1), start: "2026-09-30", days: 0, readBack: day(2026, 10, 1)}, // over already
		{in: 0, start: "2026-10-30", days: hiddifyUnlimitedDays, readBack: 0},
		{in: -3 * dayMs, startNull: true, days: 3, readBack: -3 * dayMs},
	} {
		got := a.hiddifyExpiry(c.in)
		if c.startNull {
			if got["start_date"] != nil {
				t.Errorf("%d: start = %v", c.in, got["start_date"])
			}
		} else if got["start_date"] != c.start {
			t.Errorf("%d: start = %v, want %v", c.in, got["start_date"], c.start)
		}
		if got["package_days"] != c.days {
			t.Errorf("%d: days = %v, want %d", c.in, got["package_days"], c.days)
		}
		days := got["package_days"].(int64)
		u := &hiddify.User{PackageDays: &days}
		if s, ok := got["start_date"].(string); ok {
			u.StartDate = &s
		}
		if st := hiddifyStatus(u); st.ExpiryMs != c.readBack {
			t.Errorf("%d: read back %d, want %d", c.in, st.ExpiryMs, c.readBack)
		}
	}
	// A date written by another JSON encoder reads the same.
	for _, s := range []string{"2026-10-30", "Fri, 30 Oct 2026 00:00:00 GMT", "2026-10-30T00:00:00Z"} {
		d, s := int64(3), s
		if st := hiddifyStatus(&hiddify.User{PackageDays: &d, StartDate: &s}); st.ExpiryMs != day(2026, 11, 3) {
			t.Errorf("start %q: expiry %d", s, st.ExpiryMs)
		}
	}
	unl, small := float64(hiddifyUnlimitedGB), 0.0
	if hiddifyStatus(&hiddify.User{UsageLimitGB: &unl}).TotalBytes != 0 || hiddifyStatus(&hiddify.User{UsageLimitGB: &small}).TotalBytes != 1 {
		t.Fatal("quota mapping")
	}
	if hiddifyQuota(0) != hiddifyUnlimitedGB || hiddifyQuota(1<<30) != 1 {
		t.Fatal("quota writing")
	}
}

func TestHiddifyNamesAndRetries(t *testing.T) {
	fake, a := newHiddifyFake(t)
	ctx := context.Background()
	// An empty panel answers its listing with 404 "You have no user": that is no user, not a failure.
	if _, err := a.Status(ctx, "nobody"); !hiddify.IsNotFound(err) {
		t.Fatalf("empty panel: %v", err)
	}
	if err := a.CreateClient(ctx, "u2", "s2", 0, 1<<30, nil); err != nil {
		t.Fatal(err)
	}
	// A retried create meets its own user: refused the way a taken name is, and the caller adopts it.
	err := a.CreateClient(ctx, "u2", "s2", 0, 1<<30, nil)
	if err == nil || !hiddify.Answered(err) {
		t.Fatalf("retry: %v", err)
	}
	if id, err := a.ClientSubID(ctx, "u2"); err != nil || id != "s2" {
		t.Fatalf("own user after a retry: %q %v", id, err)
	}
	// A name a stranger has is refused; the stranger is not ours.
	fake.Seed("8c0a3f8e-1111-4c4c-8d8d-000000000001", "taken", "")
	if err := a.CreateClient(ctx, "taken", "s3", 0, 0, nil); err == nil {
		t.Fatal("a stranger's name taken")
	}
	if id, err := a.ClientSubID(ctx, "taken"); err != nil || id != "" {
		t.Fatalf("stranger = %q %v", id, err)
	}
	// A stranger who takes the name of one of ours later does not hide ours.
	fake.Seed("8c0a3f8e-1111-4c4c-8d8d-000000000002", "u2", "a note")
	a.idx.forget("u2")
	a.idx.stale()
	if id, err := a.ClientSubID(ctx, "u2"); err != nil || id != "s2" {
		t.Fatalf("ours beside a stranger: %q %v", id, err)
	}
	// Deleted behind BOBRES's back, then made again: the same UUID (in the customer's links).
	sn, _ := fake.Snapshot("u2")
	fake.Remove("u2")
	fake.Remove("u2") // the stranger too
	if _, err := a.Status(ctx, "u2"); !hiddify.IsNotFound(err) {
		t.Fatalf("removed: %v", err)
	}
	if err := a.CreateClient(ctx, "u2", "s2", 0, 1<<30, nil); err != nil {
		t.Fatalf("made again: %v", err)
	}
	if again, _ := fake.Snapshot("u2"); again.UUID != sn.UUID {
		t.Fatalf("uuid %s, was %s", again.UUID, sn.UUID)
	}
	if err := a.CreateClient(ctx, "u3", "s4", 0, 0, []int{2}); err == nil {
		t.Fatal("an inbound that does not exist taken")
	}
	// Starts on first use.
	if err := a.CreateClient(ctx, "u4", "s5", -7*dayMs, 0, nil); err != nil {
		t.Fatal(err)
	}
	if sn, _ := fake.Snapshot("u4"); sn.StartDate != "" || sn.PackageDays != 7 {
		t.Fatalf("first use = %+v", sn)
	}
	if st, _ := a.Status(ctx, "u4"); st.ExpiryMs != -7*dayMs {
		t.Fatalf("first use status = %+v", st)
	}
}

func TestHiddifyRefusals(t *testing.T) {
	fake, _ := newHiddifyFake(t)
	ctx := context.Background()
	bad, err := NewHiddify(fake.URL, "11111111-2222-4333-8444-555555555555", xui.AllowInsecureHTTP(), xui.AllowPrivateAddresses())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bad.Inbounds(ctx); !hiddify.IsUnauthorized(err) {
		t.Fatalf("wrong key: %v", err)
	}
	if _, err := NewHiddify(fake.URL, "not-a-uuid", xui.AllowInsecureHTTP(), xui.AllowPrivateAddresses()); err == nil {
		t.Fatal("a key that is not a UUID taken")
	}
	if _, err := NewHiddify(fake.Server.URL, hiddifyKey, xui.AllowInsecureHTTP(), xui.AllowPrivateAddresses()); err == nil {
		t.Fatal("an address without the admin path taken")
	}
	// A pasted admin link (the path goes on with the admin's UUID) works.
	a, err := NewHiddify(fake.URL+"/"+hiddifyKey+"/admin/", hiddifyKey, xui.AllowInsecureHTTP(), xui.AllowPrivateAddresses())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.HealthCheck(ctx); err != nil {
		t.Fatalf("health through a pasted admin link: %v", err)
	}
}
