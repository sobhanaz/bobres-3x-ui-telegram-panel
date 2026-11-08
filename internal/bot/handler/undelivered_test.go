package handler

import (
	"context"
	"slices"
	"strings"
	"testing"
)

// udWorld is a store (a plan of 150,000 Toman) whose panel can be made to stop answering, and the owner (English).
type udWorld struct {
	*world
	owner *person
}

func newUDWorld(t *testing.T) *udWorld {
	t.Helper()
	w := newWorld(t)
	return &udWorld{world: w, owner: setupStore(t, w)}
}

// panelDown makes every call to the panel fail (a server that does not answer); panelUp puts it back.
func (u *udWorld) panelDown() { u.stack.Panel.SetFailNext(1 << 20) }
func (u *udWorld) panelUp()   { u.stack.Panel.SetFailNext(0) }

// buy pays the plan from the customer's wallet, funded for it first.
func (u *udWorld) buy(p *person) {
	u.t.Helper()
	u.fund(p.id, 150_000)
	p.text("/menu")
	p.press("buy")
	p.press("plan:")
	p.press("pay:w:")
	p.shownHas("Paid from your wallet")
}

// resellerBuys has a reseller buy the plan for a customer, named label, on credit.
func (u *udWorld) resellerBuys(rs *person, label string) {
	u.t.Helper()
	rs.text("/menu")
	rs.press("rs")
	rs.press("rs:buy")
	rs.press("rs:p:")
	rs.text(label)
	rs.press("pay:w:")
	rs.shownHas("Paid from your wallet")
}

// broughtBy registers a customer (English) who joined with a reseller's invite link.
func (u *udWorld) broughtBy(reseller, id int64, username string) *person {
	u.t.Helper()
	info, err := u.dom().ResellerInfo(context.Background(), u.storeUser(reseller).ID)
	if err != nil {
		u.t.Fatal(err)
	}
	p := u.person(id, username)
	p.text("/start r_" + info.RefCode)
	p.press("lang:en")
	return p
}

// waiting waits until n orders wait for a retry after a failed delivery and returns them, the longest waiting
// first. Their next try is put an hour off, so the worker leaves them alone while the test looks at them.
func (u *udWorld) waiting(n int) []string {
	u.t.Helper()
	ctx := context.Background()
	var ids []string
	u.eventually("undelivered orders", func() bool {
		rows, err := u.stack.Store.DB().Query(ctx, `SELECT id::text FROM core.orders WHERE status = 'provision_failed' ORDER BY created_at, id`)
		if err != nil {
			u.t.Fatal(err)
		}
		defer rows.Close()
		ids = ids[:0]
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				u.t.Fatal(err)
			}
			ids = append(ids, id)
		}
		return len(ids) == n
	})
	if _, err := u.stack.Store.DB().Exec(ctx, `UPDATE core.orders SET next_attempt_at = now() + interval '1 hour' WHERE status = 'provision_failed'`); err != nil {
		u.t.Fatal(err)
	}
	return ids
}

// order is an order's status, how often it was tried and whether it was refunded.
func (u *udWorld) order(id string) (status string, attempts int, refunded bool) {
	u.t.Helper()
	if err := u.stack.Store.DB().QueryRow(context.Background(), `SELECT status, provision_attempts, refunded_at IS NOT NULL FROM core.orders WHERE id = $1`, id).
		Scan(&status, &attempts, &refunded); err != nil {
		u.t.Fatal(err)
	}
	return status, attempts, refunded
}

// audit lists the reasons of the audit entries of an action, oldest first.
func (u *udWorld) audit(action string) []string {
	u.t.Helper()
	rows, err := u.stack.Store.DB().Query(context.Background(),
		`SELECT COALESCE(entity_id::text, '') || ' ' || COALESCE(reason, '') FROM core.audit_log WHERE action = $1 ORDER BY created_at, id`, action)
	if err != nil {
		u.t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			u.t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

// The admin menu offers the undelivered orders while there are any. The list is five to a page, the longest
// waiting first: each with the plan, the amount, who bought it (a reseller for a customer of theirs, or a customer
// a reseller brought), how long it has waited, how often it was tried and why it failed, and Retry / Refund; Retry
// all and Back under it. It reads in the staff member's language.
func TestUndeliveredOrdersList(t *testing.T) {
	u := newUDWorld(t)
	rs := u.customer(9701, "rsl")
	u.makeReseller(9701, 0, 10, 500_000)
	u.panelDown()
	plain := u.customer(9702, "plain")
	u.buy(plain)
	brought := u.broughtBy(9701, 9703, "brought")
	u.buy(brought)
	u.resellerBuys(rs, "Ali <home>")
	for range 3 {
		u.buy(plain)
	}
	ids := u.waiting(6)

	u.owner.text("/admin")
	u.owner.lastHas("⚠️ Failed provisioning: 6")
	if !hasLabel(u.owner, "📦 Undelivered orders (6)") {
		t.Fatalf("admin menu: %v", u.owner.last().labels())
	}
	u.owner.press("adm:ud:0")
	list := u.owner.last()
	u.owner.lastHas("📦 <b>Undelivered orders: 6</b> · page 1 of 2\n\nThese orders are paid, but their service could not be made yet")
	u.owner.lastHas("1. <b>Monthly 50GB</b> · 150,000 Toman\n👤 @plain\n⏳ Waiting ")
	u.owner.lastHas("2. <b>Monthly 50GB</b> · 150,000 Toman\n👤 @brought · via reseller @rsl\n⏳ Waiting ")
	u.owner.lastHas("3. <b>Monthly 50GB</b> · 150,000 Toman\n👤 reseller @rsl · 🏷 Ali &lt;home&gt;\n⏳ Waiting ")
	u.owner.lastHas(" · attempts: 1\n⚠️ <code>")
	if strings.Contains(list.text, "6. <b>") {
		t.Fatal("the sixth is on the first page")
	}
	want := []string{}
	for _, id := range ids[:5] {
		want = append(want, "adm:udr:0:"+id, "adm:udf:0:"+id)
	}
	want = append(want, "adm:ud:1", "adm:uda:0", "adm")
	if got := lastData(u.owner); !slices.Equal(got, want) {
		t.Fatalf("buttons:\n%v\nwant\n%v", got, want)
	}
	for _, d := range callbacksOf(u.owner) {
		if len(d) > 64 {
			t.Fatalf("callback over 64 bytes: %q", d)
		}
	}
	if !hasLabel(u.owner, "🔁 1. Retry") || !hasLabel(u.owner, "💸 5. Refund") || !hasLabel(u.owner, "🔁 Retry all") {
		t.Fatalf("labels: %v", list.labels())
	}

	// The next page, in place; a page past the end shows the last one.
	u.owner.press("adm:ud:1")
	if u.owner.last() != list {
		t.Fatal("the next page was not shown in place")
	}
	u.owner.lastHas("page 2 of 2")
	u.owner.lastHas("6. <b>Monthly 50GB</b> · 150,000 Toman\n👤 @plain")
	if got := lastData(u.owner); !slices.Equal(got, []string{"adm:udr:1:" + ids[5], "adm:udf:1:" + ids[5], "adm:ud:0", "adm:uda:1", "adm"}) {
		t.Fatalf("the last page's buttons: %v", got)
	}
	u.owner.tap("adm:ud:99")
	u.owner.lastHas("page 2 of 2")
	u.owner.tap("adm:ud:9223372036854775807") // a forged page: no overflow, the last page
	u.owner.lastHas("page 2 of 2")
	u.owner.tap("adm:ud")
	u.owner.lastHas("page 1 of 2")

	// An admin reads it in Persian.
	adm := u.person(9704, "ali")
	adm.text("/start")
	adm.press("lang:fa")
	u.role(9704, "admin")
	adm.text("/admin")
	if !hasLabel(adm, "📦 سفارش\u200cهای تحویل\u200cنشده (۶)") {
		t.Fatalf("admin menu in Persian: %v", adm.last().labels())
	}
	adm.press("adm:ud:0")
	adm.lastHas("📦 <b>سفارش\u200cهای تحویل\u200cنشده: ۶</b> · صفحه ۱ از ۲")
	adm.lastHas("۲. <b>ماهانه ۵۰ گیگ</b> · ۱۵۰٬۰۰۰ تومان\n👤 @brought · با لینک نماینده @rsl\n⏳ ")
	adm.lastHas("👤 نماینده @rsl · 🏷 Ali &lt;home&gt;")
	adm.lastHas(" در انتظار · ۱ بار تلاش")
	if !hasLabel(adm, "🔁 ۱. تلاش دوباره") || !hasLabel(adm, "💸 ۱. برگرداندن پول") || !hasLabel(adm, "تلاش دوباره برای همه") {
		t.Fatalf("Persian labels: %v", adm.last().labels())
	}
}

// Retry tries one order now: delivered once the server answers (with an apology to a customer told it was late),
// and a button of an order that is not waiting any more says so and does nothing. Retry all tries every one and
// says how many; once none is left the list says so and the admin menu no longer offers it.
func TestUndeliveredRetry(t *testing.T) {
	u := newUDWorld(t)
	u.panelDown()
	a := u.customer(9711, "anna")
	u.buy(a)
	b := u.customer(9712, "bob")
	u.buy(b)
	c := u.customer(9713, "cara")
	u.buy(c)
	ids := u.waiting(3)
	// Anna was told her service is late (core does so at the third failure).
	if _, err := u.stack.Store.DB().Exec(context.Background(), `UPDATE core.orders SET provision_attempts = 3 WHERE id = $1`, ids[0]); err != nil {
		t.Fatal(err)
	}
	u.owner.text("/admin")
	u.owner.press("adm:ud:0")
	list := u.owner.last()
	u.owner.lastHas("Undelivered orders: 3")

	// The server answers again: Retry delivers Anna's at once, with the apology; the others still wait.
	u.panelUp()
	u.owner.press("adm:udr:0:" + ids[0])
	u.owner.toast("Tried again now.")
	if u.owner.last() != list {
		t.Fatal("the list was not shown again in place")
	}
	a.eventuallySees("Your service is ready")
	ready := a.containing("Your service is ready")
	if len(ready) != 1 || !strings.HasSuffix(ready[0].text, "\n\n🙏 Sorry this took longer than usual: one of our servers was not answering.") {
		t.Fatalf("the late delivery: %q", ready[0].text)
	}
	if st, _, _ := u.order(ids[1]); st != "provision_failed" {
		t.Fatalf("Bob's order was tried too: %s", st)
	}
	u.owner.tap("adm:ud:0")
	u.owner.lastHas("Undelivered orders: 2")
	u.owner.lastHas("1. <b>Monthly 50GB</b> · 150,000 Toman\n👤 @bob")

	// An old Retry of the delivered order: said so, nothing done.
	u.owner.tap("adm:udr:0:" + ids[0])
	u.owner.toast("This order is not waiting any more.")
	u.owner.lastHas("Undelivered orders: 2")
	u.drain()
	if n := len(a.containing("Your service is ready")); n != 1 {
		t.Fatalf("delivered %d times", n)
	}

	// Forged or broken: nothing is done, the list is shown.
	for _, data := range []string{"adm:udr:0:not-an-id", "adm:udr:0:" + strings.ToUpper(ids[1]), "adm:udr:x", "adm:udr"} {
		u.owner.tap(data)
		u.owner.lastHas("That button is out of date")
	}
	if _, attempts, _ := u.order(ids[1]); attempts != 1 {
		t.Fatalf("a forged button tried Bob's order: %d attempts", attempts)
	}

	// Retry all: both tried now, said with the count; delivered, without an apology (they were never said late).
	u.owner.press("adm:uda:0")
	u.owner.toast("Orders tried again now: 2")
	b.eventuallySees("Your service is ready")
	c.eventuallySees("Your service is ready")
	if strings.Contains(b.containing("Your service is ready")[0].text, "Sorry") {
		t.Fatal("an apology for a delivery on time")
	}
	u.owner.tap("adm:ud:0")
	u.owner.lastHas("✅ No undelivered orders: every paid order has its service.")
	if got := lastData(u.owner); !slices.Equal(got, []string{"adm"}) {
		t.Fatalf("the empty list's buttons: %v", got)
	}
	u.owner.text("/admin")
	if hasLabel(u.owner, "Undelivered orders") {
		t.Fatal("the admin menu offers an empty list")
	}
	if got := u.audit("order.retry_all"); len(got) != 1 {
		t.Fatalf("retry all audited: %q", got)
	}
}

// Refund asks first, naming the order and what it does; Back changes nothing. The refund puts the money back in the
// buyer's wallet (they are told), cancels the order, takes back the commission of the reseller who brought the
// buyer (who is told, and sees it in their statement) and is audited with the bot's reason. An order refunded
// already is not refunded again; customers, support and resellers may do none of it.
func TestUndeliveredRefund(t *testing.T) {
	u := newUDWorld(t)
	rs := u.customer(9721, "rsl")
	u.makeReseller(9721, 0, 10, 500_000)
	u.panelDown()
	cust := u.broughtBy(9721, 9722, "custo")
	u.buy(cust)
	rs.eventuallySees("🤝 Commission from a customer's purchase: <b>15,000 Toman</b>")
	other := u.customer(9723, "other")
	u.buy(other)
	ids := u.waiting(2)

	// Who may: a customer (one of whose orders waits), support and a reseller are refused whatever they tap.
	sup := u.customer(9724, "sup")
	u.role(9724, "support")
	for _, p := range []*person{other, sup, rs} {
		for _, data := range []string{"adm:ud", "adm:ud:0", "adm:udr:0:" + ids[0], "adm:uda:0", "adm:udf:0:" + ids[0], "adm:udy:0:" + ids[0]} {
			p.tap(data)
		}
		p.toast("admins only")
		if p.count("Undelivered orders") != 0 || p.count("Refund this order") != 0 {
			t.Fatalf("user %d saw the list", p.id)
		}
	}
	for _, id := range ids {
		if st, attempts, refunded := u.order(id); st != "provision_failed" || attempts != 1 || refunded {
			t.Fatalf("someone without the role changed %s: %s %d %v", id, st, attempts, refunded)
		}
	}

	// Ask, then Back: nothing happened.
	u.owner.text("/admin")
	u.owner.press("adm:ud:0")
	list := u.owner.last()
	u.owner.press("adm:udf:0:" + ids[0])
	if u.owner.last() != list {
		t.Fatal("the question was not shown in place")
	}
	u.owner.lastHas("💸 <b>Refund this order?</b>\n\n<b>Monthly 50GB</b> · 150,000 Toman\n👤 @custo · via reseller @rsl\n⏳ Waiting ")
	u.owner.lastHas("The money goes back to the buyer's wallet and the order is cancelled")
	u.owner.lastHas("A reseller's commission on it is taken back.")
	if got := lastData(u.owner); !slices.Equal(got, []string{"adm:udy:0:" + ids[0], "adm:ud:0"}) {
		t.Fatalf("the question's buttons: %v", got)
	}
	if b := buttons(u.owner.last()); b[0].Text != "✅ Refund" || b[0].Style != "success" {
		t.Fatalf("the Refund button: %+v", b[0])
	}
	u.owner.press("adm:ud:0")
	u.owner.lastHas("Undelivered orders: 2")
	if _, _, refunded := u.order(ids[0]); refunded {
		t.Fatal("Back refunded the order")
	}

	// Refund: done, said above the list in place, with the commission taken back.
	u.owner.press("adm:udf:0:" + ids[0])
	u.owner.press("adm:udy:0:" + ids[0])
	if u.owner.last() != list {
		t.Fatal("the outcome was not shown in place")
	}
	u.owner.lastHas("💸 <b>Refunded:</b> 150,000 Toman went back to the buyer's wallet, and they were told.\n" +
		"🤝 The reseller's commission on it was taken back: 15,000 Toman.\n\n📦 <b>Undelivered orders: 1</b>")
	u.owner.toast("Refunded")
	if st, _, refunded := u.order(ids[0]); st != "cancelled" || !refunded {
		t.Fatalf("the order after the refund: %s %v", st, refunded)
	}
	if got := u.audit("order.refund"); !slices.Equal(got, []string{ids[0] + " Refunded from the bot (undelivered order)"}) {
		t.Fatalf("audit: %q", got)
	}
	cust.eventuallySees("👛 An order was refunded: 150,000 Toman was added to your wallet.\nBalance: <b>150,000 Toman</b>")
	if strings.Contains(cust.containing("An order was refunded")[0].text, "Note from the store") {
		t.Fatal("a note the staff did not write")
	}
	rs.eventuallySees("↩️ An order of one of your customers was refunded before its service could be delivered, " +
		"so the commission you earned on it was taken back: <b>15,000 Toman</b>.\nBalance: 0 Toman")
	for _, m := range u.tg.all(rs.id) {
		if strings.Contains(m.text, "@custo") || strings.Contains(m.text, "9722") {
			t.Fatalf("the reseller was shown the customer: %q", m.text)
		}
	}
	rs.text("/menu")
	rs.press("wallet")
	rs.press("whist")
	rs.shownHas("Commission taken back (refund)")

	// Stale: the refunded order is not refunded again, nor asked about, nor tried; forged buttons do nothing.
	u.owner.tap("adm:udy:0:" + ids[0])
	u.owner.lastHas("ℹ️ That order is not waiting any more (delivered, refunded or being tried right now): nothing was done.")
	u.owner.tap("adm:udf:0:" + ids[0])
	u.owner.lastHas("That order is not waiting any more")
	u.owner.tap("adm:udr:0:" + ids[0])
	u.owner.toast("This order is not waiting any more.")
	for _, data := range []string{"adm:udy:0:not-an-id", "adm:udf:0:" + strings.ToUpper(ids[1]), "adm:udy", "adm:udf:3"} {
		u.owner.tap(data)
		u.owner.lastHas("That button is out of date")
	}
	u.owner.tap("adm:udy:0:00000000-0000-4000-8000-000000000000")
	u.owner.lastHas("That order is not waiting any more")
	u.drain()
	if n := cust.count("An order was refunded"); n != 1 {
		t.Fatalf("the buyer was refunded %d times", n)
	}
	if n := rs.count("commission you earned on it was taken back"); n != 1 {
		t.Fatalf("the reseller was told %d times", n)
	}
	if st, _, refunded := u.order(ids[1]); st != "provision_failed" || refunded {
		t.Fatalf("a forged button changed the other order: %s %v", st, refunded)
	}

	// An order of the store's own: no commission to take back.
	u.owner.tap("adm:udf:0:" + ids[1])
	u.owner.press("adm:udy:0:" + ids[1])
	u.owner.lastHas("💸 <b>Refunded:</b> 150,000 Toman went back to the buyer's wallet, and they were told.\n\n✅ No undelivered orders")
	other.eventuallySees("👛 An order was refunded: 150,000 Toman was added to your wallet.")
}
