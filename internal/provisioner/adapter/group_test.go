package adapter

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/xui"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/xuifake"
)

// Every client the adapter creates is in the bot group, and the calls BOBRES
// makes on it afterwards (the panel clears the group of a row sent without one)
// leave it there.
func TestCreatedClientIsInTheBotGroup(t *testing.T) {
	a, fake := testAdapter(t)
	ctx := context.Background()
	email := "u42-grp"
	exp := time.Now().Add(30 * 24 * time.Hour).UnixMilli()
	if err := a.CreateClient(ctx, email, "sub-grp", exp, 1<<30, []int{1, 2}); err != nil {
		t.Fatal(err)
	}
	inGroup := func(when string) {
		t.Helper()
		if g, _ := fake.Group(email); g != xui.BotGroup {
			t.Fatalf("%s: group %q", when, g)
		}
	}
	inGroup("after create")
	for _, step := range []struct {
		name string
		do   func() error
	}{
		{"renew", func() error { return a.Renew(ctx, email, 7, 1<<30) }},
		{"reset traffic", func() error { return a.ResetTraffic(ctx, email) }},
		{"set limits", func() error { return a.SetLimits(ctx, email, exp, 5<<30) }},
		{"switch off", func() error { return a.SetEnabled(ctx, email, false) }},
		{"switch on", func() error { return a.SetEnabled(ctx, email, true) }},
	} {
		if err := step.do(); err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
		inGroup("after " + step.name)
	}
	if lost := fake.LostFields(); len(lost) != 0 {
		t.Fatalf("an update lost %v", lost)
	}
}

// Marzban has no groups: its adapter is left alone by the backfill.
func TestMarzbanHasNoGroups(t *testing.T) {
	if _, ok := any((*Marzban)(nil)).(GroupTagger); ok {
		t.Fatal("the Marzban adapter claims to tag clients")
	}
}

// seedHeld puts the panel in the state the first start after the update finds:
// clients BOBRES made before it grouped them, one an operator put in a group of
// their own, one tagged already, and clients that are not BOBRES's.
func seedHeld(fake *xuifake.Server) map[string]string {
	fake.Seed("old-a", "sub-a", []int{1})
	fake.Seed("old-b", "sub-b", []int{1})
	fake.SeedGrouped("old-c", "sub-c", "vip", []int{1})        // an operator's group
	fake.SeedGrouped("old-d", "sub-d", xui.BotGroup, []int{1}) // tagged already
	fake.Seed("stranger", "sub-x", []int{1})                   // not a client BOBRES holds
	fake.Seed("taken", "someone-elses", []int{1})              // the email is held, the client is another's
	fake.Seed("nosub", "whatever", []int{1})                   // held, with no subscription id on record
	return map[string]string{
		"old-a": "sub-a", "old-b": "sub-b", "old-c": "sub-c", "old-d": "sub-d",
		"taken": "sub-taken", "gone": "sub-gone", "nosub": "",
	}
}

func TestTagUngroupedTagsOnlyWhatItShould(t *testing.T) {
	a, fake := testAdapter(t)
	ctx := context.Background()
	managed := seedHeld(fake)

	res, err := a.TagUngrouped(ctx, managed)
	if err != nil {
		t.Fatal(err)
	}
	if want := (TagResult{Managed: 7, Tagged: 3, Grouped: 2, Missing: 2, Converged: true}); res != want {
		t.Fatalf("result %+v, want %+v", res, want)
	}
	for email, want := range map[string]string{
		"old-a": xui.BotGroup, "old-b": xui.BotGroup, "nosub": xui.BotGroup, // tagged
		"old-c": "vip", "old-d": xui.BotGroup, // kept as they were
		"stranger": "", "taken": "", // never touched
	} {
		if g, _ := fake.Group(email); g != want {
			t.Errorf("%s is in %q, want %q", email, g, want)
		}
	}

	// A repeat finds nothing to do and sends nothing.
	calls := fake.Calls("groupBulkAdd")
	res, err = a.TagUngrouped(ctx, managed)
	if err != nil {
		t.Fatal(err)
	}
	if want := (TagResult{Managed: 7, Grouped: 5, Missing: 2, Converged: true}); res != want {
		t.Fatalf("second result %+v, want %+v", res, want)
	}
	if fake.Calls("groupBulkAdd") != calls {
		t.Fatal("a repeat sent a tagging")
	}
	// An operator moves one to another group: it stays there.
	fake.SetGroup("old-a", "mine")
	if _, err := a.TagUngrouped(ctx, managed); err != nil {
		t.Fatal(err)
	}
	if g, _ := fake.Group("old-a"); g != "mine" {
		t.Fatalf("an operator's choice became %q", g)
	}
}

// Many clients go in batches the panel accepts, and a client the first listing
// missed (a page that moved up when another client was deleted meanwhile) is
// found by the next.
func TestTagUngroupedBatchesAndLooksAgain(t *testing.T) {
	a, fake := testAdapter(t)
	managed := map[string]string{}
	for i := 0; i < 450; i++ {
		e := fmt.Sprintf("e%03d", i)
		fake.Seed(e, "sub-"+e, []int{1})
		managed[e] = "sub-" + e
	}
	fake.HideOnce("e100")
	res, err := a.TagUngrouped(context.Background(), managed)
	if err != nil {
		t.Fatal(err)
	}
	if want := (TagResult{Managed: 450, Tagged: 450, Converged: true}); res != want {
		t.Fatalf("result %+v, want %+v", res, want)
	}
	if got := fake.GroupBatches(); !slices.Equal(got, []int{200, 200, 49, 1}) {
		t.Fatalf("batches %v", got)
	}
	if got := fake.Calls("listPaged"); got != 9 { // three listings of three pages
		t.Fatalf("%d pages read", got)
	}
	if m := fake.GroupMembers(xui.BotGroup); len(m) != 450 {
		t.Fatalf("%d clients in the group", len(m))
	}
}

// A panel that answers and changes nothing does not end in "done": the caller
// is told it did not settle.
func TestTagUngroupedReportsAPanelThatIgnoresIt(t *testing.T) {
	a, fake := testAdapter(t)
	fake.Seed("i1", "sub-i1", []int{1})
	fake.IgnoreGroupAdds(true)
	res, err := a.TagUngrouped(context.Background(), map[string]string{"i1": "sub-i1"})
	if err != nil || res.Converged || res.Tagged != 0 {
		t.Fatalf("result %+v, %v", res, err)
	}
	if fake.Calls("listPaged") != 3 {
		t.Fatalf("%d listings; it looks a bounded number of times", fake.Calls("listPaged"))
	}
}

func TestTagUngroupedHoldingNothingCallsNoPanel(t *testing.T) {
	a, fake := testAdapter(t)
	res, err := a.TagUngrouped(context.Background(), nil)
	if err != nil || !res.Converged || res.Managed != 0 {
		t.Fatalf("result %+v, %v", res, err)
	}
	if fake.Calls("listPaged") != 0 || fake.Calls("groupBulkAdd") != 0 {
		t.Fatal("the panel was called for nothing")
	}
}

func TestTagUngroupedPanelFailures(t *testing.T) {
	a, fake := testAdapter(t)
	ctx := context.Background()
	managed := map[string]string{}
	for i := 0; i < 450; i++ {
		e := fmt.Sprintf("f%03d", i)
		fake.Seed(e, "sub-"+e, []int{1})
		managed[e] = "sub-" + e
	}
	// The listing fails: nothing is tagged.
	fake.FailRoute("listPaged", 1)
	if res, err := a.TagUngrouped(ctx, managed); err == nil || res.Tagged != 0 || res.Converged {
		t.Fatalf("listing failed: %+v %v", res, err)
	}
	if len(fake.GroupMembers(xui.BotGroup)) != 0 {
		t.Fatal("tagged without a listing")
	}
	// A batch fails half way: what went through stays, and the next run finishes.
	fake.FailRouteAfter("groupBulkAdd", 1, 1)
	res, err := a.TagUngrouped(ctx, managed)
	if err == nil || res.Tagged != 200 || res.Converged {
		t.Fatalf("a batch failed: %+v %v", res, err)
	}
	res, err = a.TagUngrouped(ctx, managed)
	if err != nil || res.Tagged != 250 || res.Grouped != 200 || !res.Converged {
		t.Fatalf("the run after it: %+v %v", res, err)
	}
	// A panel without groups.
	fake.SetNoGroups(true)
	if _, err := a.TagUngrouped(ctx, managed); err == nil {
		t.Fatal("a panel without groups tagged")
	}
}

func TestEnsureTagged(t *testing.T) {
	a, fake := testAdapter(t)
	ctx := context.Background()
	fake.Seed("t1", "sub-t1", []int{1})
	fake.SeedGrouped("t2", "sub-t2", "vip", []int{1})
	for i := 0; i < 2; i++ {
		if err := a.EnsureTagged(ctx, "t1"); err != nil {
			t.Fatal(err)
		}
	}
	if g, _ := fake.Group("t1"); g != xui.BotGroup || fake.Calls("groupBulkAdd") != 1 {
		t.Fatalf("t1: group %q after %d taggings", g, fake.Calls("groupBulkAdd"))
	}
	if err := a.EnsureTagged(ctx, "t2"); err != nil {
		t.Fatal(err)
	}
	if g, _ := fake.Group("t2"); g != "vip" {
		t.Fatalf("an operator's group became %q", g)
	}
	if err := a.EnsureTagged(ctx, "ghost"); err == nil {
		t.Fatal("a client the panel does not have")
	}
}
