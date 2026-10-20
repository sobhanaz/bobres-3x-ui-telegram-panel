package handler

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	coredomain "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	corestore "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	buuid "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
)

// resellerService has a reseller (a registered customer made a reseller) buy
// a service for a customer through core, waits for the panel to deliver it
// and returns it: a real client on the fake panel, which pausing and
// deleting act on.
func (w *world) resellerService(telegramID int64, label string) corestore.Subscription {
	w.t.Helper()
	ctx := context.Background()
	d, st := w.stack.Domain, w.stack.Store
	u := w.storeUser(telegramID)
	plans, err := st.ListPlans(ctx, st.Conn(), false)
	if err != nil {
		w.t.Fatal(err)
	}
	i := slices.IndexFunc(plans, func(p corestore.Plan) bool { return !p.IsTrial && !p.IsTopup })
	if i < 0 {
		w.t.Fatal("no plan to buy")
	}
	o, err := d.CreateOrder(ctx, coredomain.CreateOrderParams{UserID: u.ID, PlanID: plans[i].ID,
		IdempotencyKey: "rsm-" + buuid.MustV7().String(), Label: label})
	if err != nil {
		w.t.Fatal(err)
	}
	if _, err := d.PayOrderWithWallet(ctx, o.ID, u.ID); err != nil {
		w.t.Fatal(err)
	}
	var sub corestore.Subscription
	w.eventually("the service to be delivered", func() bool {
		s, err := st.SubscriptionByOrder(ctx, st.Conn(), o.ID)
		if err != nil || s.Status != "active" {
			return false
		}
		sub = *s
		return true
	})
	return sub
}

// setStatus puts a service in a status directly, for the lists' sake.
func (w *world) setStatus(id, status string) {
	w.t.Helper()
	if err := w.stack.Store.SetSubscriptionStatus(context.Background(), w.stack.Store.Conn(), id, status); err != nil {
		w.t.Fatal(err)
	}
}

// audited counts the audit entries of an action on one entity, and returns
// the newest.
func (w *world) audited(action, entityID string) (int, *corestore.AuditRow) {
	w.t.Helper()
	rows, total, err := w.stack.Store.ListAudit(context.Background(), w.stack.Store.Conn(),
		corestore.AuditFilter{Action: action, EntityID: entityID}, corestore.Page{Limit: 10})
	if err != nil {
		w.t.Fatal(err)
	}
	if len(rows) == 0 {
		return int(total), nil
	}
	return int(total), &rows[0]
}

// balance is a user's Toman balance.
func (w *world) balance(telegramID int64) int64 {
	w.t.Helper()
	wal, err := w.stack.Store.GetWallet(context.Background(), w.stack.Store.Conn(), w.storeUser(telegramID).ID, "IRT")
	if err != nil {
		w.t.Fatal(err)
	}
	return wal.Balance
}

// resellersLeaveTheLicense ends the trial (no license: the free tier has no
// resellers) until the test ends.
func (w *world) resellersLeaveTheLicense() {
	w.t.Helper()
	db := w.stack.Store.DB()
	if _, err := db.Exec(context.Background(), `UPDATE core.install SET trial_started_at = now() - interval '31 days'`); err != nil {
		w.t.Fatal(err)
	}
	w.t.Cleanup(func() {
		_, _ = db.Exec(context.Background(), `UPDATE core.install SET trial_started_at = now(), limit_hit_at = NULL`)
	})
}

// noLongCallbacks fails when a button of the message carries more than the
// 64 bytes Telegram allows.
func noLongCallbacks(t *testing.T, m *msg) {
	t.Helper()
	for _, d := range callbacks(m) {
		if len(d) > 64 {
			t.Fatalf("callback data of %d bytes: %q", len(d), d)
		}
	}
}

// A reseller's services list is grouped: every group has its count on a
// button, a tap shows that group alone (a search made there stays inside it),
// the group shown is ticked, and paging keeps the group and the search.
func TestResellerServiceFiltersAndCounts(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	rs := w.customer(2001, "rsl")
	w.makeReseller(2001, 0, 10, 0)
	ids := map[string]string{}
	for i := 1; i <= 10; i++ { // 10 active
		name := fmt.Sprintf("act-%02d", i)
		ids[name] = w.seedService(2001, name)
	}
	for _, c := range []struct{ name, status string }{
		{"ending-1", "expiring_soon"}, {"ending-2", "expiring_soon"},
		{"gone-time", "expired"}, {"gone-data", "depleted"}, {"stopped-1", "disabled"}, {"new-1", "pending"},
	} {
		ids[c.name] = w.seedService(2001, c.name)
		w.setStatus(ids[c.name], c.status)
	}
	deleted := w.seedService(2001, "removed-1")
	w.setStatus(deleted, "deleted")
	other := w.customer(2002, "other")
	w.makeReseller(2002, 0, 0, 0)
	w.seedService(2002, "not-mine")

	rs.text("/menu")
	rs.press("rs")
	rs.press("rs:subs:1")
	rs.shownHas("📦 <b>Customer services</b> (16)\nPage 1 of 2.") // 10 + 2 + 2 + 1 + 1, the deleted one is not counted
	for _, want := range []string{"✔️ 📋 All (16)", "✅ Active (10)", "⚠️ Ending soon (2)", "⌛ Ended (2)", "⏸ Paused (1)"} {
		rs.lastButton(want)
	}
	noLongCallbacks(t, rs.shown())
	for _, d := range []string{"rs:subs:1", "rs:sf:a:1", "rs:sf:e:1", "rs:sf:x:1", "rs:sf:p:1"} {
		if !slices.Contains(callbacks(rs.shown()), d) {
			t.Errorf("no %s button: %v", d, callbacks(rs.shown()))
		}
	}

	// One group: its services only, ticked, with the count in the title.
	rs.press("rs:sf:e:1")
	rs.shownHas("📦 <b>Customer services</b> · ⚠️ Ending soon (2)\nPage 1 of 1.")
	if got := serviceLabels(rs.shown()); len(got) != 2 || !strings.HasPrefix(got[0], "ending-2 · ") || !strings.HasPrefix(got[1], "ending-1 · ") {
		t.Fatalf("ending soon: %v", got)
	}
	rs.lastButton("✔️ ⚠️ Ending soon (2)")
	rs.press("rs:sf:x:1")
	if got := serviceLabels(rs.shown()); len(got) != 2 || !strings.HasPrefix(got[0], "gone-data · Traffic used up") || !strings.HasPrefix(got[1], "gone-time · Expired") {
		t.Fatalf("ended (time and traffic): %v", got)
	}
	rs.press("rs:sf:p:1")
	if got := serviceLabels(rs.shown()); len(got) != 1 || !strings.HasPrefix(got[0], "stopped-1 · Paused ⏸") {
		t.Fatalf("paused is shown as paused: %v", got)
	}
	rs.press("rs:sf:a:1")
	rs.shownHas("Page 1 of 2.")
	if got := serviceLabels(rs.shown()); len(got) != 8 || !strings.HasPrefix(got[0], "act-10 · Active ✅") {
		t.Fatalf("active page 1: %v", got)
	}
	if cb := callbacks(rs.shown()); !slices.Contains(cb, "rs:sf:a:2") || slices.Contains(cb, "rs:sf:a:0") {
		t.Fatalf("active page 1 buttons: %v", cb)
	}
	rs.press("rs:sf:a:2")
	if got := serviceLabels(rs.shown()); len(got) != 2 || !strings.HasPrefix(got[1], "act-01 · ") {
		t.Fatalf("active page 2: %v", got)
	}
	rs.tap("rs:sf:a:99") // past the end: the first page of the group
	rs.shownHas("Page 1 of 2.")
	rs.tap("rs:sf:zz:1") // not a group: all services
	rs.shownHas("📦 <b>Customer services</b> (16)")

	// A search inside a group; paging and "All services" keep the group.
	rs.press("rs:sf:a:1")
	rs.press("rs:find:a")
	rs.shownHas("Send part of a service's name")
	rs.text("act-0")
	rs.shownHas("🔎 Services matching <b>act-0</b> · ✅ Active (9)\nPage 1 of 2.")
	if cb := callbacks(rs.shown()); !slices.Contains(cb, "rs:sf:a:2:act-0") {
		t.Fatalf("paging a search in a group: %v", cb)
	}
	noLongCallbacks(t, rs.shown())
	rs.press("rs:sf:a:2:act-0")
	if got := serviceLabels(rs.shown()); len(got) != 1 || !strings.HasPrefix(got[0], "act-01") {
		t.Fatalf("search in a group, page 2: %v", got)
	}
	rs.tap("rs:sf:e:1")
	rs.press("rs:find:e")
	rs.text("act-0") // nothing that is ending soon has this name
	rs.shownHas("🔎 No service in ⚠️ Ending soon matches <b>act-0</b>.")
	rs.press("rs:sf:e:1") // "All services" of the search: the group without the search
	rs.shownHas("Customer services</b> · ⚠️ Ending soon (2)")
	rs.press("rs:sf:a:1")
	rs.press("rs:find:a")
	rs.text("ending") // an empty answer in the group
	rs.shownHas("No service in ✅ Active matches <b>ending</b>.")
	rs.press("rs:sf:a:1")
	rs.shownHas("(10)")

	// Another reseller's and a deleted service never show; nothing counts them.
	for _, group := range []string{"rs:subs:1", "rs:sf:a:1", "rs:sf:e:1", "rs:sf:x:1", "rs:sf:p:1"} {
		rs.tap(group)
		for _, l := range serviceLabels(rs.shown()) {
			if strings.Contains(l, "not-mine") || strings.Contains(l, "removed-1") {
				t.Fatalf("%s: %v", group, serviceLabels(rs.shown()))
			}
		}
	}
	// A group with nothing says so and keeps the buttons.
	w.setStatus(ids["stopped-1"], "active")
	rs.tap("rs:sf:p:1")
	rs.shownHas("📦 No services here: ⏸ Paused.")
	rs.lastButton("⏸ Paused (0)")

	// The other reseller sees their own counts.
	other.text("/menu")
	other.press("rs")
	other.press("rs:subs:1")
	other.shownHas("(1)")
	other.lastButton("✅ Active (1)")
}

// The Persian list: groups with Persian digits and names.
func TestResellerServiceFiltersInPersian(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	rs := w.person(2011, "rsl")
	rs.text("/start")
	rs.press("lang:fa")
	w.makeReseller(2011, 0, 10, 0)
	a := w.seedService(2011, "علی")
	w.seedService(2011, "رضا")
	w.setStatus(a, "disabled")
	rs.text("/menu")
	rs.press("rs")
	rs.press("rs:subs:1")
	for _, want := range []string{"📋 همه (۲)", "✅ فعال (۱)", "⚠️ رو به اتمام (۰)", "⌛ تمام\u200cشده (۰)", "⏸ متوقف (۱)"} {
		rs.lastButton(want)
	}
	if got := serviceLabels(rs.shown()); len(got) != 2 || !strings.HasPrefix(got[1], "علی · متوقف ⏸") {
		t.Fatalf("services: %v", got)
	}
	rs.press("rs:sf:p:1")
	rs.shownHas("⏸ متوقف (۱)")
	rs.press("rs:sf:e:1")
	rs.shownHas("📦 سرویسی در این دسته نیست: ⚠️ رو به اتمام.")
}

// The whole of pausing, resuming, noting and deleting, on a real client of
// the fake panel: the screens, the panel, the audit log, and what a second
// tap does.
func TestResellerPausesNotesAndDeletesAService(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	rs := w.customer(2101, "rsl")
	w.makeReseller(2101, 0, 10, 5_000_000)
	sub := w.resellerService(2101, "Ali")
	email := sub.ClientEmail
	if on, ok := w.stack.Panel.Snapshot(email); !ok || !on.Enable {
		t.Fatalf("the client on the panel: %+v %v", on, ok)
	}

	rs.text("/menu")
	rs.press("subs")
	rs.press("sub:" + sub.ID)
	rs.shownHas("🏷 <b>Ali</b>\n📦 <b>Service</b>")
	for _, want := range []string{"⏸ Pause", "📝 Note", "🗑 Delete", "✏️ Rename", "📷 QR code", "🔄 Renew"} {
		rs.lastButton(want)
	}
	if strings.Contains(strings.Join(rs.shown().labels(), "|"), "Resume") {
		t.Fatalf("Resume on a running service: %v", rs.shown().labels())
	}
	noLongCallbacks(t, rs.shown())

	// Pause: the client is off on the panel, the page says so, and Resume is offered.
	rs.press("rs:pz:")
	rs.toast("Service paused")
	rs.shownHas("Status: Paused ⏸")
	rs.lastButton("▶️ Resume")
	if strings.Contains(strings.Join(rs.shown().labels(), "|"), "Pause") || strings.Contains(strings.Join(rs.shown().labels(), "|"), "Renew") {
		t.Fatalf("a paused service offers Pause or Renew: %v", rs.shown().labels())
	}
	if on, _ := w.stack.Panel.Snapshot(email); on.Enable {
		t.Fatal("the panel client is still on")
	}
	if got := w.subscription(sub.ID).Status; got != "disabled" {
		t.Fatalf("status: %s", got)
	}
	n, row := w.audited("subscription.disable", sub.ID)
	if n != 1 || row == nil || row.ActorTelegramID != 2101 || row.ActorRole != "reseller" || row.Source != "bot" {
		t.Fatalf("audit of the pause: %d %+v", n, row)
	}
	// A second tap on an old Pause button changes nothing.
	rs.tap("rs:pz:" + sub.ID)
	if n, _ := w.audited("subscription.disable", sub.ID); n != 1 {
		t.Fatalf("a second pause was audited: %d", n)
	}
	// The list shows it as paused.
	rs.tap("rs:sf:p:1")
	if got := serviceLabels(rs.shown()); len(got) != 1 || !strings.HasPrefix(got[0], "Ali · Paused ⏸") {
		t.Fatalf("paused list: %v", got)
	}

	// Resume.
	rs.tap("sub:" + sub.ID)
	rs.press("rs:rz:")
	rs.toast("Service resumed")
	rs.shownHas("Status: Active ✅")
	rs.lastButton("⏸ Pause")
	if on, _ := w.stack.Panel.Snapshot(email); !on.Enable {
		t.Fatal("the panel client is still off")
	}
	if n, row := w.audited("subscription.enable", sub.ID); n != 1 || row == nil || row.ActorTelegramID != 2101 || row.Source != "bot" {
		t.Fatalf("audit of the resume: %d %+v", n, row)
	}

	// What support paused, the reseller cannot resume.
	if _, err := w.stack.Domain.SetSubscriptionEnabled(context.Background(), w.storeUser(ownerID), w.stack.Prov, sub.ID, false, "abuse"); err != nil {
		t.Fatal(err)
	}
	rs.tap("sub:" + sub.ID)
	rs.shownHas("Status: Paused ⏸")
	rs.press("rs:rz:")
	rs.toast("Support paused this service")
	if on, _ := w.stack.Panel.Snapshot(email); on.Enable || w.subscription(sub.ID).Status != "disabled" {
		t.Fatal("the reseller undid support's pause")
	}
	if _, err := w.stack.Domain.SetSubscriptionEnabled(context.Background(), w.storeUser(ownerID), w.stack.Prov, sub.ID, true, "ok"); err != nil {
		t.Fatal(err)
	}
	_ = owner

	// Notes: asked for, saved as one line, shown on the page, refused when
	// too long, removed.
	rs.tap("sub:" + sub.ID)
	rs.press("rs:nt:")
	rs.shownHas("Send a note for service #" + shortID(sub.ID))
	if hasCallback(rs.shown(), "rs:nc:") {
		t.Fatal("Remove the note, with no note")
	}
	rs.text(strings.Repeat("x", 201))
	rs.shownHas("at most 200 characters")
	rs.text("  pays on the 5th,\n  <his brother>  ")
	rs.shownHas("📝 pays on the 5th, &lt;his brother&gt;")
	if got := w.subscription(sub.ID).Note; got == nil || *got != "pays on the 5th, <his brother>" {
		t.Fatalf("stored note: %v", got)
	}
	rs.text("not a note anymore") // the question was answered: this is no note
	if got := w.subscription(sub.ID).Note; got == nil || *got != "pays on the 5th, <his brother>" {
		t.Fatalf("a message after the note changed it: %v", got)
	}
	rs.tap("sub:" + sub.ID)
	rs.shownHas("📝 pays on the 5th, &lt;his brother&gt;")
	rs.press("rs:nt:")
	rs.shownHas("pays on the 5th")
	rs.press("rs:nc:")
	rs.toast("Note removed")
	if got := w.subscription(sub.ID).Note; got != nil {
		t.Fatalf("note not removed: %q", *got)
	}
	if strings.Contains(rs.shown().text, "📝") {
		t.Fatalf("service page after the note was removed: %q", rs.shown().text)
	}
	rs.press("rs:nt:")
	rs.press("cancel")
	rs.text("after cancel")
	if got := w.subscription(sub.ID).Note; got != nil {
		t.Fatalf("a message after Cancel became the note: %q", *got)
	}

	// Delete: a confirmation first; No keeps it; Yes removes it from the panel.
	rs.tap("sub:" + sub.ID)
	rs.press("rs:dl:")
	rs.shownHas("Delete service #" + shortID(sub.ID) + "?")
	rs.shownHas("nothing is refunded")
	rs.lastButton("🗑 Yes, delete it")
	rs.lastButton("« No, keep it")
	if w.subscription(sub.ID).Status == "deleted" {
		t.Fatal("deleted before the confirmation")
	}
	if on, ok := w.stack.Panel.Snapshot(email); !ok || !on.Enable {
		t.Fatal("the panel client changed before the confirmation")
	}
	rs.tap("sub:" + sub.ID) // No, keep it
	rs.shownHas("📦 <b>Service</b>")
	balanceBefore := w.balance(2101)
	rs.press("rs:dl:")
	rs.press("rs:dy:")
	rs.toast("Service deleted")
	rs.shownHas("You have not bought any services for customers yet")
	if w.subscription(sub.ID).Status != "deleted" {
		t.Fatalf("status after the delete: %s", w.subscription(sub.ID).Status)
	}
	if _, ok := w.stack.Panel.Snapshot(email); ok {
		t.Fatal("the client is still on the panel")
	}
	n, row = w.audited("subscription.delete", sub.ID)
	if n != 1 || row == nil || row.ActorTelegramID != 2101 || row.ActorRole != "reseller" || row.Source != "bot" {
		t.Fatalf("audit of the delete: %d %+v", n, row)
	}
	if w.balance(2101) != balanceBefore {
		t.Fatalf("the delete moved money: %d -> %d", balanceBefore, w.balance(2101))
	}
	// The confirmation tapped again: still fine, nothing happens twice.
	rs.tap("rs:dy:" + sub.ID)
	if n, _ := w.audited("subscription.delete", sub.ID); n != 1 {
		t.Fatalf("a second delete was audited: %d", n)
	}
	// And the other powers on a deleted service say it is gone.
	rs.tap("rs:pz:" + sub.ID)
	rs.toast("no longer there")
	rs.tap("rs:nt:" + sub.ID)
	rs.shownHas("You have not bought any services for customers yet") // sent back to the list
}

// Nobody can act on a service that is not theirs, whatever button they craft:
// another reseller, a customer, a banned reseller, a reseller after resellers
// left the license.
func TestResellerManagementIsOnlyForTheirOwnServices(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	mine := w.customer(2201, "mine")
	w.makeReseller(2201, 0, 10, 5_000_000)
	theirs := w.customer(2202, "theirs")
	w.makeReseller(2202, 0, 10, 5_000_000)
	cust := w.customer(2203, "plain")
	theirSub := w.resellerService(2202, "theirs")
	ownSub := w.resellerService(2201, "mine")
	custSub := w.seedService(2203, "customers")

	untouched := func(what string) {
		t.Helper()
		for _, s := range []corestore.Subscription{theirSub} {
			got := w.subscription(s.ID)
			if got.Status != "active" || got.Note != nil {
				t.Fatalf("%s: their service changed: %+v", what, got)
			}
			if on, ok := w.stack.Panel.Snapshot(s.ClientEmail); !ok || !on.Enable {
				t.Fatalf("%s: their client changed on the panel", what)
			}
		}
		if got := w.subscription(custSub); got.Status != "active" || got.Note != nil {
			t.Fatalf("%s: a customer's service changed: %+v", what, got)
		}
	}

	// Another reseller's service: not found, nothing changes.
	for _, d := range []string{"rs:pz:", "rs:rz:", "rs:dl:", "rs:dy:", "rs:nt:", "rs:nc:"} {
		mine.tap(d + theirSub.ID)
		untouched(d)
	}
	mine.tap("rs:pz:" + theirSub.ID)
	mine.toast("no longer there")
	mine.tap("rs:nt:" + custSub)
	untouched("a customer's service")
	mine.text("sneaky note") // no note question was opened
	untouched("a stray message")

	// A customer's crafted buttons: the reseller screens are not theirs.
	for _, d := range []string{"rs:pz:", "rs:dy:", "rs:nt:", "rs:cu:"} {
		cust.tap(d + custSub)
		cust.toast("This section is for resellers only.")
		untouched("customer " + d)
	}

	// Banned: refused as restricted.
	if err := w.stack.Store.SetUserStatus(context.Background(), w.stack.Store.Conn(), w.storeUser(2201).ID, "banned"); err != nil {
		t.Fatal(err)
	}
	mine.tap("rs:dy:" + ownSub.ID)
	mine.toast("restricted")
	if w.subscription(ownSub.ID).Status != "active" {
		t.Fatal("a banned reseller deleted a service")
	}
	if err := w.stack.Store.SetUserStatus(context.Background(), w.stack.Store.Conn(), w.storeUser(2201).ID, "active"); err != nil {
		t.Fatal(err)
	}

	// Resellers left the license: the powers say "not available" and change
	// nothing; the plain list of services still opens, and a service can still
	// be opened to take its link.
	w.resellersLeaveTheLicense()
	mine.text("/menu")
	mine.press("subs")
	mine.shownHas("📦 <b>Customer services</b> (1)")
	if strings.Contains(strings.Join(mine.shown().labels(), "|"), "Active (") {
		t.Fatalf("groups without the license: %v", mine.shown().labels())
	}
	mine.press("sub:" + ownSub.ID)
	mine.shownHas("Subscription link")
	for _, d := range []string{"rs:pz:", "rs:dy:"} {
		mine.tap(d + ownSub.ID)
		mine.toast("not available right now")
	}
	mine.tap("rs:nt:" + ownSub.ID) // the question is asked; the answer is refused
	mine.shownHas("Send a note for service #")
	mine.text("a note")
	mine.sees("not available right now")
	mine.tap("rs:cu:1")
	mine.toast("not available right now")
	if got := w.subscription(ownSub.ID); got.Status != "active" || got.Note != nil {
		t.Fatalf("a refused call changed the service: %+v", got)
	}
	if n, _ := w.audited("subscription.disable", ownSub.ID); n != 0 {
		t.Fatalf("audit entries: %d", n)
	}
	_ = theirs
}

// The people who joined through the reseller's link: who, when, how many paid
// orders, what they spent and what it paid the reseller; paged, newest first;
// nobody else's, nothing else about them.
func TestResellerSeesTheCustomersWhoJoinedThroughTheLink(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	rs := w.customer(2301, "rsl")
	w.makeReseller(2301, 0, 10, 0)
	rival := w.customer(2302, "rival")
	w.makeReseller(2302, 0, 10, 0)

	rs.text("/menu")
	rs.press("rs")
	rs.press("rs:cu:1")
	rs.shownHas("Nobody has joined with your link yet")
	rs.lastButton("🔗 My link")

	ctx := context.Background()
	st, d := w.stack.Store, w.stack.Domain
	joined := func(inviter *corestore.User, tg int64, username string, ago time.Duration) *corestore.User {
		t.Helper()
		u, err := st.UpsertUser(ctx, st.Conn(), tg, username, "en", inviter.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.DB().Exec(ctx, `UPDATE core.users SET created_at = $2 WHERE id = $1`, u.ID, time.Now().Add(-ago)); err != nil {
			t.Fatal(err)
		}
		return u
	}
	buy := func(u *corestore.User, key string) {
		t.Helper()
		w.fund(u.TelegramID, 300_000)
		plans, _ := st.ListPlans(ctx, st.Conn(), false)
		i := slices.IndexFunc(plans, func(p corestore.Plan) bool { return !p.IsTrial && !p.IsTopup })
		o, err := d.CreateOrder(ctx, coredomain.CreateOrderParams{UserID: u.ID, PlanID: plans[i].ID, IdempotencyKey: key})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := d.PayOrderWithWallet(ctx, o.ID, u.ID); err != nil {
			t.Fatal(err)
		}
	}
	me, other := w.storeUser(2301), w.storeUser(2302)
	// Twelve joined through mine, newest first = the highest number.
	var mineUsers []*corestore.User
	for i := 1; i <= 12; i++ {
		username := fmt.Sprintf("cust_%02d", i)
		if i == 5 {
			username = "" // no @username: shown by id
		}
		mineUsers = append(mineUsers, joined(me, int64(2400+i), username, time.Duration(24*(13-i))*time.Hour))
	}
	theirs := joined(other, 2490, "rivals_customer", time.Hour)
	joined(me, 2491, "<b>tag</b>", 30*time.Minute) // an @username is escaped in the message
	buy(mineUsers[11], "cu-1")                     // the newest of the twelve: one purchase, 150,000 Toman, 15,000 commission
	buy(mineUsers[11], "cu-2")
	buy(mineUsers[4], "cu-3")
	buy(theirs, "cu-4")

	rs.tap("rs:cu:1")
	rs.shownHas("👥 <b>Customers who joined with your link</b> (13)\nPage 1 of 2.")
	text := rs.shown().text
	// The two newest first: the escaped name, then cust_12 with two purchases.
	if i, j := strings.Index(text, "@&lt;b&gt;tag&lt;/b&gt;"), strings.Index(text, "@cust_12"); i < 0 || j < 0 || i > j {
		t.Fatalf("order or escaping: %q", text)
	}
	for _, want := range []string{
		"<b>@cust_12</b> · joined ", "🛒 Orders: 2 · Spent: 300,000 Toman\n💰 Commission: 30,000 Toman",
		"<b>@cust_11</b> · joined ", "🛒 Orders: 0 · Spent: 0 Toman\n💰 Commission: 0 Toman",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("customers page: missing %q in %q", want, text)
		}
	}
	// The customer without an @username is shown by id, with their purchase.
	if !strings.Contains(text, "<b>id 2405</b> · joined ") || !strings.Contains(text, "🛒 Orders: 1 · Spent: 150,000 Toman\n💰 Commission: 15,000 Toman") {
		t.Fatalf("a customer without a username: %q", text)
	}
	for _, leaked := range []string{"rivals_customer", "@rival", "2490"} {
		if strings.Contains(text, leaked) {
			t.Fatalf("a customer of another reseller: %q", text)
		}
	}
	if n := strings.Count(text, "· joined"); n != customersPage {
		t.Fatalf("%d customers on the page, want %d", n, customersPage)
	}
	if cb := callbacks(rs.shown()); !slices.Contains(cb, "rs:cu:2") || slices.Contains(cb, "rs:cu:0") {
		t.Fatalf("page 1 buttons: %v", cb)
	}
	rs.press("rs:cu:2")
	rs.shownHas("Page 2 of 2.")
	text = rs.shown().text
	if n := strings.Count(text, "· joined"); n != 3 || !strings.Contains(text, "@cust_01") || strings.Contains(text, "@cust_12") {
		t.Fatalf("customers on page 2, want the 3 oldest: %q", text)
	}
	if cb := callbacks(rs.shown()); !slices.Contains(cb, "rs:cu:1") || slices.Contains(cb, "rs:cu:3") {
		t.Fatalf("page 2 buttons: %v", cb)
	}
	rs.tap("rs:cu:99") // past the end: the first page
	rs.shownHas("Page 1 of 2.")
	rs.tap("rs:cu:x")
	rs.shownHas("Page 1 of 2.")
	noLongCallbacks(t, rs.shown())
	// Nothing but those fields: no wallet, no language, no status, no service.
	for _, never := range []string{"balance", "Balance", "language", "banned", "subscription", "Service"} {
		if strings.Contains(rs.shown().text, never) {
			t.Fatalf("the customers page says %q: %q", never, rs.shown().text)
		}
	}

	// The panel button, and the other reseller's own view.
	rs.press("rs")
	rs.lastButton("👥 Customers")
	rival.text("/menu")
	rival.press("rs")
	rival.press("rs:cu:1")
	rival.shownHas("(1)")
	rival.shownHas("<b>@rivals_customer</b>")
	if strings.Contains(rival.shown().text, "cust_") {
		t.Fatalf("the rival sees my customers: %q", rival.shown().text)
	}
}

// The customers page in Persian, with Persian digits.
func TestResellerCustomersInPersian(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	rs := w.person(2501, "rsl")
	rs.text("/start")
	rs.press("lang:fa")
	w.makeReseller(2501, 0, 10, 0)
	ctx := context.Background()
	if _, err := w.stack.Store.UpsertUser(ctx, w.stack.Store.Conn(), 2502, "kasra", "fa", w.storeUser(2501).ID); err != nil {
		t.Fatal(err)
	}
	rs.text("/menu")
	rs.press("rs")
	rs.lastButton("👥 مشتریان")
	rs.press("rs:cu:1")
	rs.shownHas("👥 <b>مشتریانی که با لینک شما عضو شده\u200cاند</b> (۱)")
	rs.shownHas("<b>@kasra</b> · عضویت: ")
	rs.shownHas("🛒 تعداد خرید: ۰ · مجموع خرید: ۰ تومان\n💰 پورسانت: ۰ تومان")
}
