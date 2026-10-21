package handler

import (
	"context"
	"slices"
	"strings"
	"testing"

	corestore "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// A store with plan groups lists the groups first; a customer reads a group's text, picks a plan of it, and Back
// from the plan returns to the group. The plans with no group are under "Other plans". A store with none of it lists
// the plans flat, as it always did, and deleting a group puts its plans back there.
func TestPlanGroupsStepBeforeThePlans(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	owner.text("/plan_add 99000 IRT 7 20 Weekly 20GB | هفتگی ۲۰ گیگ")
	owner.text("/plan_add 250000 IRT 90 200 Quarterly 200GB | سه ماهه ۲۰۰ گیگ")
	ctx := context.Background()
	actor := w.storeUser(ownerID)

	// No group yet: the plans, flat.
	u := w.customer(2301, "grp")
	u.tap("buy")
	if rows := planRowsOf(u.shown(), "plan:"); len(rows) != 3 {
		t.Fatalf("a store without groups lists %d plans: %v", len(rows), u.shown().labels())
	}
	if slices.ContainsFunc(callbacks(u.shown()), func(c string) bool { return strings.HasPrefix(c, "pg:") }) {
		t.Fatalf("a group button with no group: %v", callbacks(u.shown()))
	}

	// Two plans go into a group with a text (markup in it is shown as written, never interpreted).
	g, err := w.stack.Domain.UpsertPlanGroup(ctx, actor, &corestore.PlanGroup{
		NameI18n:        map[string]string{"en": "Starter", "fa": "شروع"},
		DescriptionI18n: map[string]string{"en": "For <b>light</b> use.\nNo refunds.", "fa": "برای مصرف سبک"},
		Sort:            1,
	})
	if err != nil {
		t.Fatal(err)
	}
	empty, err := w.stack.Domain.UpsertPlanGroup(ctx, actor, &corestore.PlanGroup{NameI18n: map[string]string{"en": "Nothing here"}, Sort: 2})
	if err != nil {
		t.Fatal(err)
	}
	all, err := w.stack.Store.ListPlans(ctx, w.stack.Store.Conn(), true)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range all {
		if p.NameI18n["en"] == "Quarterly 200GB" {
			continue
		}
		p := p
		p.GroupID = &g.ID
		if _, err := w.stack.Domain.UpsertPlan(ctx, actor, &p); err != nil {
			t.Fatal(err)
		}
	}

	// The customer first sees the groups (one with a plan on sale), then Other plans.
	u.tap("buy")
	m := u.shown()
	checkKeyboardLimits(t, "the group list", m)
	u.shownHas("Choose a category")
	if got := callbacks(m); !slices.Equal(got, []string{"pg:" + g.ID, "pg:-", "home"}) {
		t.Fatalf("the group list: %v (%v)", got, m.labels())
	}
	u.lastButton("📂 Starter")
	u.lastButton("📦 Other plans")
	if slices.Contains(callbacks(m), "pg:"+empty.ID) {
		t.Fatal("a group with no plan on sale is listed")
	}

	// A group: its text, then its plans in the store's layout.
	u.tap("pg:" + g.ID)
	u.shownHas("<b>Starter</b>")
	u.shownHas("For &lt;b&gt;light&lt;/b&gt; use.\nNo refunds.")
	u.shownHas("Choose a plan")
	rows := planRowsOf(u.shown(), "plan:")
	if len(rows) != 2 {
		t.Fatalf("the group's plans: %v", rows)
	}
	checkKeyboardLimits(t, "a group", u.shown())
	if !hasButton(u.shown(), "buy") {
		t.Fatalf("no way back to the groups: %v", callbacks(u.shown()))
	}

	// Picking a plan opens the payment menu; its Back button returns to the group, not to the list of everything.
	u.pressLabel("Weekly 20GB")
	u.shownHas("How would you like to pay?")
	if !slices.Contains(callbacks(u.shown()), "pg:"+g.ID) {
		t.Fatalf("Back from a plan does not return to its group: %v", callbacks(u.shown()))
	}

	// The plans of no group.
	u.tap("buy")
	u.tap("pg:-")
	u.shownHas("Other plans")
	if rows := planRowsOf(u.shown(), "plan:"); len(rows) != 1 || rows[0][0] != "Quarterly 200GB" {
		t.Fatalf("other plans: %v", rows)
	}

	// A group that is gone (an old button) shows the list again; an unknown id is the same.
	u.tap("pg:00000000-0000-7000-8000-000000000000")
	u.shownHas("Choose a category")

	// In Persian: the group's Persian name and text.
	fa := w.person(2302, "")
	fa.text("/start")
	fa.press("lang:fa")
	fa.tap("buy")
	fa.lastButton("📂 شروع")
	fa.lastButton("📦 سایر پلن\u200cها")
	fa.tap("pg:" + g.ID)
	fa.shownHas("برای مصرف سبک")

	// A group with only a Persian text is shown to an English reader in that language rather than empty.
	if _, err := w.stack.Domain.UpsertPlanGroup(ctx, actor, &corestore.PlanGroup{ID: g.ID,
		NameI18n: map[string]string{"fa": "شروع"}, DescriptionI18n: map[string]string{"fa": "فقط فارسی"}, Sort: 1}); err != nil {
		t.Fatal(err)
	}
	u.tap("pg:" + g.ID)
	u.shownHas("شروع")
	u.shownHas("فقط فارسی")

	// Deleting the group puts its plans back among the rest: flat again, all still on sale.
	if err := w.stack.Domain.DeletePlanGroup(ctx, actor, g.ID); err != nil {
		t.Fatal(err)
	}
	if err := w.stack.Domain.DeletePlanGroup(ctx, actor, empty.ID); err != nil {
		t.Fatal(err)
	}
	u.tap("buy")
	if rows := planRowsOf(u.shown(), "plan:"); len(rows) != 3 {
		t.Fatalf("after deleting the groups: %v", u.shown().labels())
	}
}

// The bot's own plan editor never sends a group: saving a plan from there keeps the group the dashboard gave it.
func TestEditingAPlanFromTheBotKeepsItsGroup(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	ctx := context.Background()
	actor := w.storeUser(ownerID)
	g, err := w.stack.Domain.UpsertPlanGroup(ctx, actor, &corestore.PlanGroup{NameI18n: map[string]string{"en": "Starter"}})
	if err != nil {
		t.Fatal(err)
	}
	all, _ := w.stack.Store.ListPlans(ctx, w.stack.Store.Conn(), true)
	var p corestore.Plan
	for _, c := range all {
		if c.NameI18n["en"] == "Monthly 50GB" {
			p = c
		}
	}
	if p.ID == "" {
		t.Fatalf("no Monthly plan among %d", len(all))
	}
	p.GroupID = &g.ID
	if _, err := w.stack.Domain.UpsertPlan(ctx, actor, &p); err != nil {
		t.Fatal(err)
	}
	// What the bot's admin plan editor sends: a plan with no group id (turning it off and on is such a save).
	again, err := w.stack.Store.GetPlan(ctx, w.stack.Store.Conn(), p.ID)
	if err != nil || again.GroupID == nil || *again.GroupID != g.ID {
		t.Fatalf("group %v %v", again, err)
	}
	keep := *again
	keep.GroupID = nil
	if _, err := w.stack.Domain.UpsertPlan(ctx, actor, &keep); err != nil {
		t.Fatal(err)
	}
	after, _ := w.stack.Store.GetPlan(ctx, w.stack.Store.Conn(), p.ID)
	if after.GroupID == nil || *after.GroupID != g.ID {
		t.Fatalf("an edit without a group dropped it: %v", after.GroupID)
	}
	// An empty id takes it out.
	none := ""
	keep.GroupID = &none
	if _, err := w.stack.Domain.UpsertPlan(ctx, actor, &keep); err != nil {
		t.Fatal(err)
	}
	if after, _ = w.stack.Store.GetPlan(ctx, w.stack.Store.Conn(), p.ID); after.GroupID != nil {
		t.Fatalf("an empty group id did not clear it: %v", *after.GroupID)
	}
}
