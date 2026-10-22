//go:build integration

package adapter

// A reseller's clients in a group of their own, against a REAL 3x-ui panel (the variables of group_integration_test.go): a client
// is made in the group asked for and keeps it through every call BOBRES makes on it, and Regroup moves only the clients that are in
// a group BOBRES owns (none, the default group, its old name, another "BOBRES-NAME"), never one an operator chose.

import (
	"context"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/xui"
)

func TestIntegrationGroupedCreateKeepsItsGroup(t *testing.T) {
	p := newGroupPanel(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	const group = "BOBRES-SOBHANADMIN"
	email, sub := p.email("res"), p.prefix+"-sub-res"
	if err := p.a.CreateClientInGroup(ctx, group, email, sub, time.Now().Add(24*time.Hour).UnixMilli(), 1<<30, p.inbounds); err != nil {
		t.Fatalf("create: %v", err)
	}
	p.wantGroup(t, ctx, email, group, "after create")
	for _, step := range []struct {
		name string
		do   func() error
	}{
		{"a limits change", func() error { return p.a.SetLimits(ctx, email, time.Now().Add(48*time.Hour).UnixMilli(), 2<<30) }},
		{"a renewal", func() error { return p.a.Renew(ctx, email, 5, 1<<30) }},
		{"turning it off", func() error { return p.a.SetEnabled(ctx, email, false) }},
		{"turning it on", func() error { return p.a.SetEnabled(ctx, email, true) }},
		{"a traffic reset", func() error { return p.a.ResetTraffic(ctx, email) }},
	} {
		if err := step.do(); err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
		p.wantGroup(t, ctx, email, group, "after "+step.name)
	}
	// The default group when none is named.
	other := p.email("plain")
	if err := p.a.CreateClientInGroup(ctx, "", other, p.prefix+"-sub-plain", 0, 0, p.inbounds); err != nil {
		t.Fatal(err)
	}
	p.wantGroup(t, ctx, other, xui.BotGroup, "a create with no group")
}

func TestIntegrationRegroupMovesOnlyWhatIsOurs(t *testing.T) {
	p := newGroupPanel(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	held := map[string]string{ // email -> the group it is in now
		"legacy": xui.LegacyBotGroup, "none": "", "default": xui.BotGroup, "oldres": "BOBRES-OLDNAME", "vip": "an operator's group",
	}
	want := map[string]GroupWant{}
	emails := map[string]string{}
	for label, g := range held {
		email, sub := p.email(label), p.sub(label)
		p.add(t, ctx, email, sub, g)
		emails[label] = email
		want[email] = GroupWant{SubID: sub, Group: "BOBRES-NEWRES"}
	}
	// A client under another subscription id, and one BOBRES does not hold, are left alone.
	wrongSub := p.email("wrongsub")
	p.add(t, ctx, wrongSub, p.sub("someone-elses"), "")
	want[wrongSub] = GroupWant{SubID: p.sub("mine"), Group: "BOBRES-NEWRES"}
	stranger := p.email("stranger")
	p.add(t, ctx, stranger, p.sub("stranger"), "")

	res, err := p.a.Regroup(ctx, want)
	if err != nil || !res.Converged {
		t.Fatalf("Regroup: %+v %v", res, err)
	}
	if res.Tagged != 4 || res.Managed != len(want) {
		t.Fatalf("result %+v", res)
	}
	for _, label := range []string{"legacy", "none", "default", "oldres"} {
		p.wantGroup(t, ctx, emails[label], "BOBRES-NEWRES", label+" was moved")
	}
	p.wantGroup(t, ctx, emails["vip"], "an operator's group", "an operator's group")
	p.wantGroup(t, ctx, wrongSub, "", "another subscription id")
	p.wantGroup(t, ctx, stranger, "", "a client BOBRES does not hold")

	// A repeat changes nothing, and a group given back as "" is the default group.
	if res, err = p.a.Regroup(ctx, want); err != nil || res.Tagged != 0 || !res.Converged {
		t.Fatalf("a repeat: %+v %v", res, err)
	}
	back := map[string]GroupWant{emails["none"]: {SubID: p.sub("none"), Group: ""}}
	if res, err = p.a.Regroup(ctx, back); err != nil || res.Tagged != 1 {
		t.Fatalf("given back: %+v %v", res, err)
	}
	p.wantGroup(t, ctx, emails["none"], xui.BotGroup, "given back to the default group")

	// EnsureGroup puts a client in the group only when it is in none.
	free := p.email("free")
	p.add(t, ctx, free, p.sub("free"), "")
	if err := p.a.EnsureGroup(ctx, free, "BOBRES-ENS"); err != nil {
		t.Fatal(err)
	}
	p.wantGroup(t, ctx, free, "BOBRES-ENS", "a client in no group")
	if err := p.a.EnsureGroup(ctx, free, "BOBRES-OTHER"); err != nil {
		t.Fatal(err)
	}
	p.wantGroup(t, ctx, free, "BOBRES-ENS", "a client already in a group")
}
