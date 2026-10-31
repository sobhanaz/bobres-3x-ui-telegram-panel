package adapter

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/sui"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/suifake"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/xui"
)

const suiTok = "s-ui-token-0123456789abcdef" //gitleaks:allow test fixture

func newSUIFake(t *testing.T) (*suifake.Server, *SUI) {
	t.Helper()
	fake := suifake.NewServer(suiTok)
	t.Cleanup(fake.Close)
	a, err := NewSUI(fake.URL, suiTok, xui.AllowInsecureHTTP(), xui.AllowPrivateAddresses())
	if err != nil {
		t.Fatal(err)
	}
	return fake, a
}

func TestSUILifecycle(t *testing.T) {
	fake, a := newSUIFake(t)
	ctx := context.Background()
	exp := time.Now().Add(30 * 24 * time.Hour).UnixMilli()
	if err := a.CreateClient(ctx, "u1-abcd", "sub1", exp, 10<<30, []int{1, 2}); err != nil {
		t.Fatalf("create: %v", err)
	}
	sn, ok := fake.Snapshot("u1-abcd")
	if !ok || sn.Group != xui.BotGroup || sn.Desc != "bobres-sub:sub1" || !slices.Equal(sn.Inbounds, []int{1, 2}) || !sn.HasConfig || sn.Volume != 10<<30 || sn.Expiry != exp/1000 {
		t.Fatalf("created = %+v", sn)
	}
	if id, err := a.ClientSubID(ctx, "u1-abcd"); err != nil || id != "sub1" {
		t.Fatalf("ClientSubID = %q, %v", id, err)
	}
	st, err := a.Status(ctx, "u1-abcd")
	if err != nil || st.TotalBytes != 10<<30 || st.ExpiryMs != exp/1000*1000 || !st.Enabled || st.UsedBytes != 0 {
		t.Fatalf("status = %+v, %v", st, err)
	}
	links, err := a.Links(ctx, "u1-abcd")
	if err != nil || len(links) != 2 || !strings.HasPrefix(links[0], "vless://") || !strings.Contains(links[0], "127.0.0.1:2443") {
		t.Fatalf("links = %v, %v", links, err)
	}

	fake.AddUsage("u1-abcd", 5000)
	if u, err := a.Usage(ctx, "u1-abcd"); err != nil || u != 5000 {
		t.Fatalf("usage = %d, %v", u, err)
	}
	// An edit keeps the panel's counters.
	exp2 := time.Now().Add(60 * 24 * time.Hour).Unix() * 1000
	if err := a.SetLimits(ctx, "u1-abcd", exp2, 20<<30); err != nil {
		t.Fatal(err)
	}
	if sn, _ := fake.Snapshot("u1-abcd"); sn.Down != 5000 || sn.Up != 0 || sn.Volume != 20<<30 || sn.Expiry != exp2/1000 || !sn.Enable {
		t.Fatalf("after SetLimits = %+v", sn)
	}
	if err := a.Renew(ctx, "u1-abcd", 2, 1<<30); err != nil {
		t.Fatal(err)
	}
	if sn, _ := fake.Snapshot("u1-abcd"); sn.Expiry != exp2/1000+2*86400 || sn.Volume != 21<<30 || sn.Down != 5000 {
		t.Fatalf("after Renew = %+v", sn)
	}
	if err := a.SetEnabled(ctx, "u1-abcd", false); err != nil {
		t.Fatal(err)
	}
	if st, _ := a.Status(ctx, "u1-abcd"); st.Enabled || st.UsedBytes != 5000 {
		t.Fatalf("disabled = %+v", st)
	}
	if err := a.SetEnabled(ctx, "u1-abcd", true); err != nil {
		t.Fatal(err)
	}
	if err := a.ResetTraffic(ctx, "u1-abcd"); err != nil {
		t.Fatal(err)
	}
	if sn, _ := fake.Snapshot("u1-abcd"); sn.Down != 0 || sn.TotalDown != 5000 || !sn.Enable {
		t.Fatalf("after reset = %+v", sn)
	}
	if err := a.Delete(ctx, "u1-abcd"); err != nil {
		t.Fatal(err)
	}
	if fake.Has("u1-abcd") {
		t.Fatal("still on the panel")
	}
	if _, err := a.Status(ctx, "u1-abcd"); !sui.IsNotFound(err) || !sui.Answered(err) {
		t.Fatalf("status of a deleted client: %v", err)
	}
}

func TestSUICreateDefaultsAndRefusals(t *testing.T) {
	fake, a := newSUIFake(t)
	ctx := context.Background()
	ins, err := a.Inbounds(ctx)
	if err != nil || len(ins) != 3 || !ins[0].Enabled || ins[2].Enabled || ins[0].Port != 2443 || ins[0].Protocol != "vless" {
		t.Fatalf("inbounds = %+v, %v", ins, err)
	}
	// No inbound given: every inbound that takes clients.
	if err := a.CreateClientInGroup(ctx, "BOBRES-ALI", "u2-x", "s2", 0, 0, nil); err != nil {
		t.Fatal(err)
	}
	if sn, _ := fake.Snapshot("u2-x"); !slices.Equal(sn.Inbounds, []int{1, 2}) || sn.Group != "BOBRES-ALI" || sn.Volume != 0 || sn.Expiry != 0 {
		t.Fatalf("created = %+v", sn)
	}
	// A name someone else has: the panel refuses, and the client is not ours.
	fake.Seed("taken", "", "vip")
	err = a.CreateClient(ctx, "taken", "s3", 0, 0, []int{1})
	if err == nil || !sui.Answered(err) || sui.IsNotFound(err) {
		t.Fatalf("create over a stranger: %v", err)
	}
	if id, err := a.ClientSubID(ctx, "taken"); err != nil || id != "" {
		t.Fatalf("stranger's sub id = %q, %v", id, err)
	}
	// Starts on first use: whole days of delayed start.
	if err := a.CreateClient(ctx, "u3-y", "s4", -36*3600*1000, 1<<30, []int{1}); err != nil {
		t.Fatal(err)
	}
	if st, _ := a.Status(ctx, "u3-y"); st.ExpiryMs != -2*dayMs {
		t.Fatalf("delayed start = %+v", st)
	}
	if err := a.Renew(ctx, "u3-y", 3, 0); err != nil {
		t.Fatal(err)
	}
	if sn, _ := fake.Snapshot("u3-y"); !sn.DelayStart || sn.ResetDays != 5 || sn.Expiry != 0 {
		t.Fatalf("renewed delayed start = %+v", sn)
	}
	if err := a.Renew(ctx, "u2-x", 1, 0); err == nil || !strings.Contains(err.Error(), "unlimited") {
		t.Fatalf("renew of an unlimited client: %v", err)
	}
	if err := a.SetLimits(ctx, "u3-y", -1, 0); err == nil {
		t.Fatal("negative limits taken")
	}
	// A wrong token.
	bad, _ := NewSUI(fake.URL, "wrong-token", xui.AllowInsecureHTTP(), xui.AllowPrivateAddresses())
	if _, err := bad.Inbounds(ctx); !sui.IsUnauthorized(err) {
		t.Fatalf("wrong token: %v", err)
	}
	if v := a.PanelVersion(ctx); v != "v1.6.6" {
		t.Fatalf("version = %q", v)
	}
	if _, err := NewSUI(fake.URL, "two words", xui.AllowInsecureHTTP(), xui.AllowPrivateAddresses()); err == nil {
		t.Fatal("a token with a space taken")
	}
	if _, err := NewSUI("http://203.0.113.5:2095/app", suiTok); err == nil {
		t.Fatal("plain http to a public address taken")
	}
}

// A client deleted behind BOBRES's back and made again (another id under the same
// name) is found again: the remembered id is checked.
func TestSUIStaleIDAndMissing(t *testing.T) {
	fake, a := newSUIFake(t)
	ctx := context.Background()
	if err := a.CreateClient(ctx, "u4", "s4", 0, 1<<30, []int{1}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Status(ctx, "u4"); err != nil {
		t.Fatal(err)
	}
	fake.Remove("u4")
	if _, err := a.Status(ctx, "u4"); !sui.IsNotFound(err) {
		t.Fatalf("removed: %v", err)
	}
	// Asked again and again, a missing client does not list the panel each time.
	before := fake.Calls("clients")
	for range 5 {
		if _, err := a.Status(ctx, "u4"); !sui.IsNotFound(err) {
			t.Fatal(err)
		}
	}
	if n := fake.Calls("clients") - before; n > 0 {
		t.Fatalf("%d listings for a missing client", n)
	}
	// Made again (as RecreateClient does): found under its new id.
	if err := a.CreateClient(ctx, "u4", "s4", 0, 1<<30, []int{1}); err != nil {
		t.Fatal(err)
	}
	if id, err := a.ClientSubID(ctx, "u4"); err != nil || id != "s4" {
		t.Fatalf("recreated: %q %v", id, err)
	}
	fake.Remove("u4")
	fake.Seed("u4", "", "")
	if id, err := a.ClientSubID(ctx, "u4"); err != nil || id != "" {
		t.Fatalf("a stranger under the name: %q %v", id, err)
	}
}

func TestSUIResetTrafficComesBackOnlyFromTheQuota(t *testing.T) {
	fake, a := newSUIFake(t)
	ctx := context.Background()
	for _, n := range []string{"over", "off"} {
		if err := a.CreateClient(ctx, n, "s-"+n, 0, 1000, []int{1}); err != nil {
			t.Fatal(err)
		}
	}
	fake.AddUsage("over", 2000)
	fake.Review() // the panel's job switches it off
	if err := a.SetEnabled(ctx, "off", false); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"over", "off"} {
		if err := a.ResetTraffic(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	if sn, _ := fake.Snapshot("over"); !sn.Enable || sn.Down != 0 {
		t.Fatalf("over its quota: %+v", sn)
	}
	if sn, _ := fake.Snapshot("off"); sn.Enable {
		t.Fatalf("switched off by staff: %+v", sn)
	}
}

func TestSUISubscriptionLink(t *testing.T) {
	fake, a := newSUIFake(t)
	ctx := context.Background()
	if err := a.CreateClient(ctx, "u5", "s5", 0, 0, []int{1}); err != nil {
		t.Fatal(err)
	}
	if l, err := a.SubscriptionLink(ctx, "u5", ""); err != nil || l != "http://127.0.0.1:2096/sub/u5" {
		t.Fatalf("default = %q, %v", l, err)
	}
	fake.SetSetting("subDomain", "sub.example.com")
	fake.SetSetting("subKeyFile", "/k")
	fake.SetSetting("subCertFile", "/c")
	fake.SetSetting("subPort", "443")
	if l, _ := a.SubscriptionLink(ctx, "u5", ""); l != "https://sub.example.com/sub/u5" {
		t.Fatalf("with a domain = %q", l)
	}
	fake.SetSetting("subURI", "https://cdn.example.com/s/")
	if l, _ := a.SubscriptionLink(ctx, "u5", ""); l != "https://cdn.example.com/s/u5" {
		t.Fatalf("subURI = %q", l)
	}
	if l, _ := a.SubscriptionLink(ctx, "u5", "https://x.example.com/sub/"); l != "https://x.example.com/sub/u5" {
		t.Fatalf("subBase = %q", l)
	}
	if _, err := a.SubscriptionLink(ctx, "nobody", ""); !sui.IsNotFound(err) {
		t.Fatalf("missing: %v", err)
	}
	if _, err := a.SubLinks(ctx, "s5"); err == nil {
		t.Fatal("SubLinks offered")
	}
}

func TestSUIRegroup(t *testing.T) {
	fake, a := newSUIFake(t)
	ctx := context.Background()
	for _, n := range []string{"g1", "g2", "g3"} {
		if err := a.CreateClient(ctx, n, "s-"+n, 0, 0, []int{1}); err != nil {
			t.Fatal(err)
		}
	}
	fake.SetGroup("g1", xui.LegacyBotGroup)
	fake.SetGroup("g2", "vip") // an operator's group
	fake.SetGroup("g3", "")
	fake.Seed("stranger", "", "")
	res, err := a.Regroup(ctx, map[string]GroupWant{
		"g1": {SubID: "s-g1"}, "g2": {SubID: "s-g2"}, "g3": {SubID: "s-g3", Group: "BOBRES-ALI"},
		"stranger": {SubID: "s-x"}, "gone": {SubID: "s-gone"},
	})
	if err != nil || !res.Converged || res.Tagged != 2 || res.Missing != 2 || res.Grouped != 1 {
		t.Fatalf("regroup = %+v, %v", res, err)
	}
	for n, want := range map[string]string{"g1": xui.BotGroup, "g2": "vip", "g3": "BOBRES-ALI", "stranger": ""} {
		if sn, _ := fake.Snapshot(n); sn.Group != want {
			t.Errorf("%s group = %q, want %q", n, sn.Group, want)
		}
	}
	res, err = a.Regroup(ctx, map[string]GroupWant{"g1": {SubID: "s-g1"}})
	if err != nil || res.Tagged != 0 || !res.Converged {
		t.Fatalf("again = %+v, %v", res, err)
	}
	fake.SetGroup("g1", "")
	if err := a.EnsureGroup(ctx, "g1", ""); err != nil {
		t.Fatal(err)
	}
	if err := a.EnsureGroup(ctx, "g2", ""); err != nil {
		t.Fatal(err)
	}
	if s1, _ := fake.Snapshot("g1"); s1.Group != xui.BotGroup {
		t.Fatalf("EnsureGroup = %q", s1.Group)
	}
	if s2, _ := fake.Snapshot("g2"); s2.Group != "vip" {
		t.Fatalf("EnsureGroup moved an operator's group: %q", s2.Group)
	}
}
