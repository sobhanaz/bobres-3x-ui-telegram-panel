package handler

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	coredomain "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	corestore "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/syshealth"
)

// pmWorld is a store whose customers each have a delivered trial, and the owner (English) at hand.
type pmWorld struct {
	*world
	owner    *person
	subs     []corestore.Subscription // oldest missing first, once gone
	planName string                   // the trial's plan, in English
}

func newPMWorld(t *testing.T, customers int) *pmWorld {
	t.Helper()
	w := newWorld(t)
	owner := setupStore(t, w)
	m := &pmWorld{world: w, owner: owner}
	for i := range customers {
		id := int64(9301 + i)
		c := w.customer(id, fmt.Sprintf("cust%d", i+1))
		c.press("trial")
		c.eventuallySees("Your service is ready")
		m.subs = append(m.subs, w.onlySub(id))
	}
	if customers > 0 {
		var names map[string]string
		if err := w.stack.Store.DB().QueryRow(context.Background(), `SELECT p.name_i18n FROM core.orders o JOIN core.plans p ON p.id = o.plan_id
			WHERE o.id = $1`, m.subs[0].OrderID).Scan(&names); err != nil {
			t.Fatal(err)
		}
		m.planName = names["en"]
	}
	return m
}

// gone deletes the clients of the services in the panel (as an operator does in its UI) and marks them as
// core does when it reads them: the first one missing the longest.
func (m *pmWorld) gone(subs ...corestore.Subscription) {
	m.t.Helper()
	for i, s := range subs {
		m.stack.Panel.Remove(s.ClientEmail)
		if _, err := m.stack.Store.DB().Exec(context.Background(),
			`UPDATE core.subscriptions SET panel_missing_since = now() - make_interval(hours => $2) WHERE id = $1`, s.ID, 100-i); err != nil {
			m.t.Fatal(err)
		}
	}
}

func (m *pmWorld) sub(id string) *corestore.Subscription {
	m.t.Helper()
	s, err := m.stack.Store.GetSubscription(context.Background(), m.stack.Store.Conn(), id)
	if err != nil {
		m.t.Fatal(err)
	}
	return s
}

// audit lists the reasons of the audit entries of an action, oldest first.
func (m *pmWorld) audit(action string) []string {
	m.t.Helper()
	rows, err := m.stack.Store.DB().Query(context.Background(),
		`SELECT entity_id::text || ' ' || COALESCE(reason, '') FROM core.audit_log WHERE action = $1 ORDER BY created_at, id`, action)
	if err != nil {
		m.t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			m.t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

// lastData lists the callback data of the user's newest message, in order.
func lastData(p *person) []string { return callbacks(p.last()) }

// The daily notice carries the button for owners and admins; it opens the list, five to a page, oldest
// missing first, with each service's email, customer, plan, end and since when; the admin menu offers it
// while there are any.
func TestPanelMissingAlertOpensTheList(t *testing.T) {
	m := newPMWorld(t, 7)
	adm := m.person(9401, "ali")
	adm.text("/start")
	adm.press("lang:fa")
	m.role(9401, "admin")
	m.owner.text("/set notify.recipients staff")
	m.gone(m.subs...)

	dom := coredomain.New(m.stack.Store, coredomain.Config{OwnerTelegramIDs: []int64{ownerID}})
	watch := dom.NewWatchdog(coredomain.WatchdogSources{Health: &stubHealth{status: syshealth.StatusOK}}, nil)
	for range 2 {
		if err := watch.Step(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	m.owner.eventuallySees("Services whose client is gone from the panel: 7")
	adm.eventuallySees("کلاینتشان از پنل پاک شده است: ۷")
	alert := m.owner.last()
	if !slices.Equal(lastData(m.owner), []string{"adm:pm"}) || !strings.Contains(strings.Join(alert.labels(), ""), "Manage these services") {
		t.Fatalf("the alert's buttons: %v %v", lastData(m.owner), alert.labels())
	}
	if !slices.Equal(lastData(adm), []string{"adm:pm"}) || !hasLabel(adm, "مدیریت این سرویس") {
		t.Fatalf("the admin's alert: %v %v", lastData(adm), adm.last().labels())
	}

	// The list comes as a new message: the alert stays as it was.
	m.owner.press("adm:pm")
	list := m.owner.last()
	if list == alert || !strings.Contains(alert.text, "gone from the panel") {
		t.Fatal("the list replaced the alert")
	}
	m.owner.lastHas("Not on the panel: 7</b> · page 1 of 2")
	for i, s := range m.subs[:5] {
		m.owner.lastHas(fmt.Sprintf("%d. <code>%s</code>\n👤 @cust%d · %s\n📅 Ends ", i+1, s.ClientEmail, i+1, m.planName))
	}
	m.owner.lastHas("gone from the panel since ")
	if strings.Contains(m.owner.last().text, m.subs[5].ClientEmail) {
		t.Fatal("the sixth is on the first page")
	}
	want := []string{}
	for _, s := range m.subs[:5] {
		want = append(want, "adm:pmr:0:"+s.ID, "adm:pmd:0:"+s.ID)
	}
	want = append(want, "adm:pm:1", "adm:pma:r", "adm:pma:d", "adm")
	if got := lastData(m.owner); !slices.Equal(got, want) {
		t.Fatalf("buttons:\n%v\nwant\n%v", got, want)
	}
	for _, d := range callbacksOf(m.owner) {
		if len(d) > 64 {
			t.Fatalf("callback over 64 bytes: %q", d)
		}
	}
	if !hasLabel(m.owner, "🔁 1. Make again") || !hasLabel(m.owner, "🗑 5. Delete from the bot") {
		t.Fatalf("labels: %v", m.owner.last().labels())
	}

	// The next page, in place.
	m.owner.press("adm:pm:1")
	if m.owner.last() != list {
		t.Fatal("the next page was not shown in place")
	}
	m.owner.lastHas("page 2 of 2")
	m.owner.lastHas("6. <code>" + m.subs[5].ClientEmail)
	m.owner.lastHas("7. <code>" + m.subs[6].ClientEmail)
	if got := lastData(m.owner); !slices.Contains(got, "adm:pm:0") || slices.Contains(got, "adm:pm:2") {
		t.Fatalf("paging on the last page: %v", got)
	}
	// A page past the end shows the last one.
	m.owner.tap("adm:pm:99")
	m.owner.lastHas("page 2 of 2")

	// The admin reads it in Persian.
	adm.press("adm:pm")
	adm.lastHas("روی پنل نیست: ۷")
	adm.lastHas(" · صفحه ۱ از ۲") // the pending list's page line
	if !hasLabel(adm, "ساخت دوباره") || !hasLabel(adm, "حذف از ربات") || !hasLabel(adm, "ساخت دوبارهٔ همه") {
		t.Fatalf("Persian labels: %v", adm.last().labels())
	}

	// The admin menu offers it while there are any.
	m.owner.text("/admin")
	if !hasLabel(m.owner, "Not on the panel (7)") {
		t.Fatalf("admin menu: %v", m.owner.last().labels())
	}
	m.owner.press("adm:pm:0")
	m.owner.lastHas("Not on the panel: 7")
	adm.text("/admin")
	if !hasLabel(adm, "روی پنل نیست (۷)") {
		t.Fatalf("admin menu in Persian: %v", adm.last().labels())
	}
}

// One at a time: each asks first; No changes nothing; Yes makes the client again (the customer's link works:
// the panel has the client) or deletes the service, audited with the fixed reason naming who; the outcome is
// said above the list, in place. A service that changed meanwhile is skipped and said so; a stale or forged
// button does nothing.
func TestPanelMissingOneAtATime(t *testing.T) {
	m := newPMWorld(t, 3)
	m.gone(m.subs...)
	a, b, c := m.subs[0], m.subs[1], m.subs[2]
	m.owner.text("/admin")
	m.owner.press("adm:pm:0")
	list := m.owner.last()

	// Ask, then No: nothing happened.
	m.owner.press("adm:pmr:0:" + a.ID)
	m.owner.lastHas("Make the client again?</b>\n\n<code>" + a.ClientEmail + "</code>\n👤 @cust1 · " + m.planName + " · ends ")
	if got := lastData(m.owner); !slices.Equal(got, []string{"adm:pmy:r:0:" + a.ID, "adm:pm:0"}) {
		t.Fatalf("confirm buttons: %v", got)
	}
	m.owner.press("adm:pm:0")
	m.owner.lastHas("Not on the panel: 3")
	if m.stack.Panel.Has(a.ClientEmail) || m.sub(a.ID).PanelMissingSince == nil {
		t.Fatal("No made the client again")
	}

	// Yes: made again, in place, and the list without it.
	m.owner.press("adm:pmr:0:" + a.ID)
	m.owner.press("adm:pmy:r:0:" + a.ID)
	if m.owner.last() != list {
		t.Fatal("the result was not shown in place")
	}
	m.owner.lastHas("✅ The client of <code>" + a.ClientEmail + "</code> was made again: the customer can connect.\n\n⚠️ <b>Not on the panel: 2</b>")
	m.owner.toast("Done")
	if strings.Contains(m.owner.last().text, "1. <code>"+a.ClientEmail) {
		t.Fatal("the service made again is still listed")
	}
	if !m.stack.Panel.Has(a.ClientEmail) || m.sub(a.ID).PanelMissingSince != nil {
		t.Fatalf("not made again: on the panel %v, %+v", m.stack.Panel.Has(a.ClientEmail), m.sub(a.ID))
	}

	// Delete: asked with what it means, then deleted.
	m.owner.press("adm:pmd:0:" + b.ID)
	m.owner.lastHas("Delete this service from the bot too?</b>\n\n<code>" + b.ClientEmail)
	m.owner.lastHas("no money goes back to the customer")
	m.owner.press("adm:pmy:d:0:" + b.ID)
	m.owner.lastHas("🗑 <code>" + b.ClientEmail + "</code> was deleted from the bot.\n\n⚠️ <b>Not on the panel: 1</b>")
	if got := m.sub(b.ID); got.Status != "deleted" {
		t.Fatalf("not deleted: %+v", got)
	}

	// The reasons the audit log keeps: what, from the bot, by whom.
	if got := m.audit("subscription.recreate_client"); !slices.Equal(got, []string{a.ID + " not on the panel: made again from the bot by @boss"}) {
		t.Fatalf("recreate audit: %q", got)
	}
	if got := m.audit("subscription.delete"); !slices.Equal(got, []string{b.ID + " not on the panel: deleted from the bot by @boss"}) {
		t.Fatalf("delete audit: %q", got)
	}

	// Changed meanwhile: an old Yes for one made again does not delete it, nor one deleted twice.
	m.owner.tap("adm:pmy:d:0:" + a.ID)
	m.owner.lastHas("ℹ️ <code>" + a.ClientEmail + "</code> is on the panel again: nothing was done.")
	if m.sub(a.ID).Status == "deleted" || !m.stack.Panel.Has(a.ClientEmail) {
		t.Fatal("a service back on the panel was deleted")
	}
	m.owner.tap("adm:pmy:r:0:" + b.ID)
	m.owner.lastHas("ℹ️ That service was deleted already: nothing was done.")
	// Asking about one no longer listed shows the list, saying so.
	m.owner.tap("adm:pmr:0:" + a.ID)
	m.owner.lastHas("That service is no longer on the list")
	m.owner.lastHas("Not on the panel: 1")

	// Forged or broken: nothing happens, the list is shown.
	for _, data := range []string{"adm:pmy:x:0:" + c.ID, "adm:pmy:d:0:not-an-id", "adm:pmd:0:" + strings.ToUpper(c.ID), "adm:pmz:d:abc", "adm:pma:q", "adm:pmy:d"} {
		m.owner.tap(data)
		m.owner.lastHas("That button is out of date")
	}
	m.owner.tap("adm:pmy:d:0:00000000-0000-4000-8000-000000000000")
	m.owner.lastHas("deleted already: nothing was done")
	if got := m.sub(c.ID); got.Status == "deleted" || got.PanelMissingSince == nil {
		t.Fatalf("a forged button changed the service: %+v", got)
	}
	if n := len(m.audit("subscription.delete")); n != 1 {
		t.Fatalf("deletes audited: %d", n)
	}

	// The panel failing is said, with the service still listed.
	m.stack.Panel.SetFailNext(20)
	m.owner.tap("adm:pmy:r:0:" + c.ID)
	m.owner.lastHas("⚠️ The client of <code>" + c.ClientEmail + "</code> could not be made again: ")
	m.owner.lastHas("Not on the panel: 1")
	m.stack.Panel.SetFailNext(0)

	// The last one gone, the list says there is nothing left, and the admin menu no longer offers it.
	m.owner.tap("adm:pmy:r:0:" + c.ID)
	m.owner.lastHas("Every service is on the panel")
	m.owner.text("/admin")
	if hasLabel(m.owner, "Not on the panel") {
		t.Fatal("the admin menu offers an empty list")
	}
}

// Who may: a customer, support and a reseller are refused whatever they tap, and nothing changes; an admin
// may, as the owner.
func TestPanelMissingNeedsServicesWrite(t *testing.T) {
	m := newPMWorld(t, 1)
	m.gone(m.subs...)
	s := m.subs[0]
	cust := m.customer(9501, "eve")
	sup := m.customer(9502, "sup")
	m.role(9502, "support")
	res := m.customer(9503, "res")
	m.role(9503, "reseller")
	for _, p := range []*person{cust, sup, res} {
		for _, data := range []string{"adm:pm", "adm:pm:0", "adm:pmd:0:" + s.ID, "adm:pmy:d:0:" + s.ID, "adm:pma:d", "adm:pmz:d:1"} {
			p.tap(data)
		}
		p.toast("admins only")
		if p.count("Not on the panel") != 0 {
			t.Fatalf("user %d saw the list", p.id)
		}
	}
	if got := m.sub(s.ID); got.Status == "deleted" || got.PanelMissingSince == nil {
		t.Fatalf("someone without the permission changed it: %+v", got)
	}

	adm := m.customer(9504, "adm")
	m.role(9504, "admin")
	adm.tap("adm:pm")
	adm.lastHas("Not on the panel: 1")
	adm.press("adm:pmd:0:" + s.ID)
	adm.press("adm:pmy:d:0:" + s.ID)
	adm.lastHas("was deleted from the bot")
	if got := m.audit("subscription.delete"); !slices.Equal(got, []string{s.ID + " not on the panel: deleted from the bot by @adm"}) {
		t.Fatalf("audit: %q", got)
	}
}

// All at once: the question names the count, a list that changed meanwhile is asked about again, and the
// outcome counts what was done, skipped and failed, naming the failures.
func TestPanelMissingAll(t *testing.T) {
	m := newPMWorld(t, 4)
	m.gone(m.subs...)
	m.owner.text("/admin")
	m.owner.press("adm:pm:0")

	// Make all again, asked with the count; No changes nothing.
	m.owner.press("adm:pma:r")
	m.owner.lastHas("Make the client again for all 4 services?")
	if got := lastData(m.owner); !slices.Equal(got, []string{"adm:pmz:r:4", "adm:pm:0"}) {
		t.Fatalf("buttons: %v", got)
	}
	m.owner.press("adm:pm:0")
	m.owner.lastHas("Not on the panel: 4")
	m.owner.press("adm:pma:r")

	// One comes back meanwhile: the question is asked again with the new count, nothing is done.
	if err := m.stack.Store.ClearPanelMissing(context.Background(), m.stack.Store.Conn(), m.subs[3].ID); err != nil {
		t.Fatal(err)
	}
	m.owner.press("adm:pmz:r:4")
	m.owner.lastHas("The list changed meanwhile: it now holds 3.")
	m.owner.lastHas("for all 3 services?")
	if n := len(m.audit("subscription.recreate_client")); n != 0 {
		t.Fatalf("made again before the new question: %d", n)
	}
	m.owner.press("adm:pmz:r:3")
	m.owner.lastHas("🔁 <b>Made again: 3</b> · skipped: 0 · failed: 0")
	m.owner.lastHas("Every service is on the panel")
	for _, s := range m.subs[:3] {
		if !m.stack.Panel.Has(s.ClientEmail) || m.sub(s.ID).PanelMissingSince != nil {
			t.Fatalf("%s not made again", s.ClientEmail)
		}
	}
	if n := len(m.audit("subscription.recreate_client")); n != 3 {
		t.Fatalf("audited: %d", n)
	}
	// An old Yes of the done list asks again (none left now).
	m.owner.tap("adm:pmz:r:3")
	m.owner.lastHas("Every service is on the panel")

	// Delete all with the panel failing: done and failed are counted, the failure named.
	m.gone(m.subs[:3]...)
	m.owner.tap("adm:pma:d")
	m.owner.lastHas("Delete all 3 services from the bot too?")
	m.owner.lastHas("This cannot be undone")
	m.stack.Panel.SetFailNext(100)
	m.owner.press("adm:pmz:d:3")
	m.owner.lastHas("🗑 <b>Deleted: 0</b> · skipped: 0 · failed: 3")
	m.owner.lastHas("⚠️ <code>" + m.subs[0].ClientEmail + "</code>: ")
	m.owner.lastHas("Not on the panel: 3")
	m.stack.Panel.SetFailNext(0)
	m.owner.tap("adm:pma:d")
	m.owner.press("adm:pmz:d:3")
	m.owner.lastHas("🗑 <b>Deleted: 3</b> · skipped: 0 · failed: 0")
	for _, s := range m.subs[:3] {
		if m.sub(s.ID).Status != "deleted" {
			t.Fatalf("%s not deleted", s.ClientEmail)
		}
	}
	if m.sub(m.subs[3].ID).Status == "deleted" {
		t.Fatal("the service on the panel was deleted")
	}
}
