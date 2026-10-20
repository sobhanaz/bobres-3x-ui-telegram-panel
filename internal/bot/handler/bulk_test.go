package handler

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	corestore "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testenv"
)

// spend takes money out of a customer's wallet, as a purchase made meanwhile
// would.
func (w *world) spend(telegramID, amount int64) {
	w.t.Helper()
	ctx := context.Background()
	st := w.stack.Store
	u := w.storeUser(telegramID)
	err := st.WithTx(ctx, func(tx pgx.Tx) error {
		_, err := st.Debit(ctx, tx, u.ID, "IRT", amount, &corestore.LedgerEntry{Kind: "adjust",
			IdempotencyKey: fmt.Sprintf("test-spend-%d-%d", telegramID, time.Now().UnixNano())})
		return err
	})
	if err != nil {
		w.t.Fatal(err)
	}
}

// setPlanPrice changes the price of the store's regular plan, as the owner does.
func (w *world) setPlanPrice(price int64) {
	w.t.Helper()
	st := w.stack.Store
	ps := regularPlans(w.t, st)
	if len(ps) == 0 {
		w.t.Fatal("no plan")
	}
	p := ps[0]
	p.Price = price
	if err := st.UpsertPlan(context.Background(), st.Conn(), &p); err != nil {
		w.t.Fatal(err)
	}
}

// bulkOrders counts the orders that belong to a bulk purchase and the bulks.
func (w *world) bulkOrders() (orders, bulks int) {
	w.t.Helper()
	if err := w.stack.Store.DB().QueryRow(context.Background(),
		`SELECT (SELECT count(*) FROM core.orders WHERE bulk_id IS NOT NULL), (SELECT count(*) FROM core.bulk_orders)`).Scan(&orders, &bulks); err != nil {
		w.t.Fatal(err)
	}
	return orders, bulks
}

// startBulk takes a customer from the home screen to the total of a bulk
// purchase of quantity services of the store's plan, by buttons.
func (p *person) startBulk(quantity string) {
	p.w.t.Helper()
	p.text("/menu")
	p.press("bk")
	p.pressLabel("Monthly 50GB")
	p.pressLabel(quantity)
}

// containing are the messages shown to a user whose text contains sub.
func (p *person) containing(sub string) []*msg {
	var out []*msg
	for _, m := range p.w.tg.all(p.id) {
		if strings.Contains(m.text, sub) {
			out = append(out, m)
		}
	}
	return out
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func findButton(m *msg, label string) tg.Button {
	for _, b := range buttons(m) {
		if b.Text == label {
			return b
		}
	}
	return tg.Button{}
}

// The Pay button of a total of three services of the store's plan.
const payThree = "👛 Pay 450,000 Toman from the wallet"

// The home menu has a Bulk purchase button under Buy (green, its own row), for a
// customer, a reseller and the staff alike, until the owner switches bulk
// purchase off; the bottom menu is the one it was.
func TestHomeHasABulkButtonUnderBuy(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	u := w.customer(2001, "home")
	rows := rowsOf(u.shown())
	if len(rows) < 3 || len(rows[0]) != 2 || rows[0][0].CallbackData != "buy" || rows[0][0].Style != "success" {
		t.Fatalf("the home menu: %v", u.shown().labels())
	}
	if len(rows[1]) != 1 || rows[1][0].Text != "🧺 Bulk purchase" || rows[1][0].CallbackData != "bk" || rows[1][0].Style != tg.StyleSuccess {
		t.Fatalf("the second row: %+v", rows[1])
	}
	fa := w.person(2002, "")
	fa.text("/start")
	fa.press("lang:fa")
	if row := rowsOf(fa.shown())[1]; len(row) != 1 || row[0].Text != "🧺 خرید عمده" || row[0].CallbackData != "bk" {
		t.Fatalf("the Persian home menu: %+v", row)
	}
	// A reseller and an owner see it too.
	rs := w.customer(2003, "shop")
	w.makeReseller(2003, 20, 10, 100_000)
	rs.text("/menu")
	owner.text("/menu")
	for name, p := range map[string]*person{"a reseller": rs, "the owner": owner} {
		if !hasCallback(p.shown(), "bk") {
			t.Errorf("%s has no Bulk purchase button: %v", name, p.shown().labels())
		}
	}
	// The bottom menu did not change: no item of its own for it.
	for _, m := range w.tg.all(2001) {
		if m.menu == nil {
			continue
		}
		for _, row := range m.menu.Keyboard {
			for _, b := range row {
				if strings.Contains(b.Text, "Bulk") {
					t.Errorf("the bottom menu has %q", b.Text)
				}
			}
		}
	}

	// Switched off, it is gone from every home menu, and a tap on an old one is told so.
	owner.text("/set bulk.enabled false")
	owner.sees("Saved")
	u.text("/menu")
	if hasCallback(u.shown(), "bk") {
		t.Fatalf("a switched off store shows the button: %v", u.shown().labels())
	}
	u.tap("bk")
	u.toast("Bulk purchase is not available right now.")
	owner.text("/set bulk.enabled true")
	u.text("/menu")
	if !hasCallback(u.shown(), "bk") {
		t.Fatal("the button did not come back")
	}
	// The same switch is in the admin menu, in its own group, one tap each way.
	owner.text("/admin")
	owner.press("adm:set")
	owner.press("adm:sg:shop")
	owner.shownHas("Plans and bulk purchase")
	owner.press("adm:st:bulk.enabled")
	u.text("/menu")
	if hasCallback(u.shown(), "bk") {
		t.Fatal("switched off from the admin menu, the button stayed")
	}
	owner.press("adm:st:bulk.enabled")
	u.text("/menu")
	if !hasCallback(u.shown(), "bk") {
		t.Fatal("switched on from the admin menu, the button did not come back")
	}
}

// The whole purchase, by buttons: plan, quantity, the total with the wallet
// beside it, Pay, and then ONE message with every link (and not one per service),
// with the services delivered, paid for once per order, and listed in My services.
func TestBulkPurchaseFromPlanToLinks(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	u := w.customer(2201, "bulky")
	w.fund(2201, 1_000_000)

	u.press("bk")
	u.shownHas("Bulk purchase")
	if rows := planRowsOf(u.shown(), "bk:p:"); len(rows) != 1 || rows[0][0] != "Monthly 50GB" || rows[0][1] != "150,000 Toman" {
		t.Fatalf("the plans: %v (%v)", rows, u.shown().labels())
	}
	checkKeyboardLimits(t, "the plans", u.shown())
	u.pressLabel("Monthly 50GB")
	u.shownHas("Price for each service: <b>150,000 Toman</b>")
	u.shownHas("from 2 to 20")
	for _, want := range []string{"2", "3", "5", "10", "✏️ Another number", "🏠 Main menu"} {
		if !contains(u.shown().labels(), want) {
			t.Fatalf("the quantity screen lacks %q: %v", want, u.shown().labels())
		}
	}
	checkKeyboardLimits(t, "the quantity screen", u.shown())
	u.pressLabel("3")
	for _, want := range []string{"Services: <b>3</b>", "Price for each: 150,000 Toman", "Total: <b>450,000 Toman</b>", "👛 Wallet: 1,000,000 Toman", "paid from your wallet", "⏳ 30 days"} {
		u.shownHas(want)
	}
	pay := u.shown()
	checkKeyboardLimits(t, "the total", pay)
	for _, want := range []string{payThree, "🏷 Name the services", "🔢 Change the number", "« Back", "🏠 Main menu"} {
		if !contains(pay.labels(), want) {
			t.Fatalf("the total lacks %q: %v", want, pay.labels())
		}
	}
	if b := findButton(pay, payThree); b.Style != tg.StyleSuccess || !strings.HasPrefix(b.CallbackData, "bk:y:") {
		t.Fatalf("the Pay button: %+v", b)
	}
	if all, _ := w.orders(2201); all != 0 {
		t.Fatalf("an order was made before Pay: %d", all)
	}

	u.pressLabel(payThree)
	u.shownHas("Paid 450,000 Toman from your wallet for 3 services")
	if all, paid := w.orders(2201); all != 3 || paid != 3 {
		t.Fatalf("orders: %d, paid %d", all, paid)
	}
	if orders, bulks := w.bulkOrders(); orders != 3 || bulks != 1 {
		t.Fatalf("%d bulk orders in %d bulks", orders, bulks)
	}
	if got := w.balance(2201); got != 550_000 {
		t.Fatalf("balance %d", got)
	}
	if !contains(u.shown().labels(), "📦 My services") {
		t.Fatalf("the paid screen: %v", u.shown().labels())
	}

	// One message, with every link; nothing per service.
	u.eventuallySees("Your 3 services are ready!")
	w.drain()
	msgs := u.containing("services are ready!")
	if len(msgs) != 1 {
		t.Fatalf("%d bulk messages", len(msgs))
	}
	m := msgs[0]
	if n := strings.Count(m.text, "<code>"+testenv.SubBase); n != 3 {
		t.Fatalf("%d links in %q", n, m.text)
	}
	for _, want := range []string{"1. <b>#", "2. <b>#", "3. <b>#", "Tap a link to copy it"} {
		if !strings.Contains(m.text, want) {
			t.Errorf("the message lacks %q: %q", want, m.text)
		}
	}
	if !contains(m.labels(), "📦 My services") || len(buttons(m)) != 1 {
		t.Fatalf("the buttons: %v", m.labels())
	}
	if n := len(u.containing("Your service is ready")); n != 0 {
		t.Fatalf("%d messages for single services", n)
	}
	if n := len(u.containing("taking longer")); n != 0 {
		t.Fatalf("a delay note for a delivery that was not delayed")
	}
	// The services exist, are the buyer's and active, and My services lists them.
	st := w.stack.Store
	subs, err := st.ListSubscriptions(context.Background(), st.Conn(), w.storeUser(2201).ID, 10)
	if err != nil || len(subs) != 3 {
		t.Fatalf("services: %d %v", len(subs), err)
	}
	for _, s := range subs {
		if s.Status != "active" || !strings.Contains(m.text, s.SubLink) {
			t.Fatalf("service %s: %s, its link is not in the message", s.ID, s.Status)
		}
	}
	u.press("subs")
	if n := len(serviceLabels(u.shown())); n != 3 {
		t.Fatalf("My services lists %d", n)
	}
}

// A number can be typed, in any digits, within the owner's limits; a number that
// will not do is explained and asked for again, and Cancel leaves nothing open.
func TestBulkTypedQuantity(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	u := w.customer(2301, "typer")
	w.fund(2301, 2_000_000)
	u.press("bk")
	u.pressLabel("Monthly 50GB")
	u.pressLabel("✏️ Another number")
	u.shownHas("Send a number from 2 to 20")
	for _, bad := range []string{"1", "0", "21", "abc", "2.5", "-3", "100"} {
		u.text(bad)
		u.lastHas("Please send a whole number from 2 to 20")
	}
	if all, _ := w.orders(2301); all != 0 {
		t.Fatal("an order was made")
	}
	u.text("۴") // Persian digits
	u.shownHas("Services: <b>4</b>")
	u.shownHas("Total: <b>600,000 Toman</b>")
	// The question is closed: a number typed now is not an answer to it.
	u.text("7")
	if strings.Contains(u.shown().text, "Services: <b>7</b>") {
		t.Fatalf("a number typed after the question closed was taken: %q", u.shown().text)
	}

	// Cancel in the middle of the question.
	u.text("/menu")
	u.press("bk")
	u.pressLabel("Monthly 50GB")
	u.pressLabel("✏️ Another number")
	u.press("cancel")
	u.toast("Cancelled")
	u.text("6")
	if strings.Contains(u.shown().text, "Services: <b>6</b>") {
		t.Fatal("a cancelled question took an answer")
	}
	// A tap on the bottom menu is not an answer either.
	u.text("/menu")
	u.press("bk")
	u.pressLabel("Monthly 50GB")
	u.pressLabel("✏️ Another number")
	u.tapMenu("en", "btn.wallet")
	u.shownHas("Wallet")
	if strings.Contains(u.shown().text, "Total: <b>") {
		t.Fatalf("a menu tap was taken as the number: %q", u.shown().text)
	}
}

// The services can be named "<name> <n>"; the name can be changed or taken off
// before paying; a name that will not do is explained; the names are the
// services' names in the delivery and in My services.
func TestBulkNamesTheServices(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	u := w.customer(2311, "namer")
	w.fund(2311, 2_000_000)
	u.startBulk("3")
	u.pressLabel("🏷 Name the services")
	u.shownHas("Send a name for these services")
	u.shownHas("at most 62 characters") // "<name> 3" is a service name of 64 characters at most
	for _, bad := range []string{strings.Repeat("x", 63), "bad" + string(rune(0)) + "name", strings.Repeat("ب", 70)} {
		u.text(bad)
		u.lastHas("Please send the name as one line of text, at most 62 characters.")
	}
	if !contains(u.shown().labels(), "No name") {
		t.Fatalf("the name question has no way out: %v", u.shown().labels())
	}
	u.text("  Family   room ")
	u.shownHas("Names: <b>Family room 1</b> to <b>Family room 3</b>")
	for _, want := range []string{"🏷 Change the name", "No name", payThree} {
		if !contains(u.shown().labels(), want) {
			t.Fatalf("the total lacks %q: %v", want, u.shown().labels())
		}
	}
	u.pressLabel("No name")
	if strings.Contains(u.shown().text, "Names:") || !contains(u.shown().labels(), "🏷 Name the services") {
		t.Fatalf("the name was not taken off: %q", u.shown().text)
	}
	u.pressLabel("🏷 Name the services")
	u.text("Family")
	u.pressLabel(payThree)
	u.shownHas("Paid 450,000 Toman")
	u.eventuallySees("Your 3 services are ready!")
	msg := u.containing("services are ready!")[0]
	for i := 1; i <= 3; i++ {
		if want := fmt.Sprintf("%d. <b>Family %d</b>", i, i); !strings.Contains(msg.text, want) {
			t.Errorf("the delivery lacks %q: %q", want, msg.text)
		}
	}
	u.press("subs")
	joined := strings.Join(serviceLabels(u.shown()), "|")
	for _, name := range []string{"Family 1", "Family 2", "Family 3"} {
		if !strings.Contains(joined, name) {
			t.Errorf("My services lacks %q: %s", name, joined)
		}
	}
}

// A Persian customer sees Persian words, digits and prices on every screen of it.
func TestBulkPurchaseInPersian(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	fa := w.person(2321, "")
	fa.text("/start")
	fa.press("lang:fa")
	w.fund(2321, 1_000_000)
	fa.press("bk")
	fa.lastButton("🛍 محصول")
	fa.lastButton("💲 مبلغ")
	fa.shownHas("خرید عمده")
	fa.pressLabel("ماهانه ۵۰ گیگ")
	fa.pressLabel("۲")
	fa.shownHas("تعداد سرویس: <b>۲</b>")
	fa.shownHas("جمع کل: <b>۳۰۰٬۰۰۰ تومان</b>")
	fa.pressLabel("👛 پرداخت ۳۰۰٬۰۰۰ تومان از کیف پول")
	fa.shownHas("مبلغ ۳۰۰٬۰۰۰ تومان برای ۲ سرویس از کیف پول پرداخت شد")
	fa.eventuallySees("۲ سرویس شما آماده است!")
}

// A double tap on Pay, even from several taps at once, is one purchase: the same
// request key reaches core, which answers it once.
func TestBulkDoubleTapPaysOnce(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	u := w.customer(2401, "dbl")
	w.fund(2401, 1_000_000)
	u.startBulk("3")
	pay := u.dataOf(payThree)
	u.tap(pay)
	u.shownHas("Paid 450,000 Toman")
	u.tap(pay) // the same button of the same screen, again
	u.shownHas("Paid 450,000 Toman")
	if all, paid := w.orders(2401); all != 3 || paid != 3 {
		t.Fatalf("after a double tap: %d orders, %d paid", all, paid)
	}
	if got := w.balance(2401); got != 550_000 {
		t.Fatalf("balance %d: it was charged twice", got)
	}

	// Four taps at the same moment on another purchase.
	w.fund(2401, 350_000) // 900,000: two purchases of 450,000 in all
	u.startBulk("3")
	pay2 := u.dataOf(payThree)
	if pay2 == pay {
		t.Fatal("a new screen reuses the old request key")
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() { u.tap(pay2) })
	}
	wg.Wait()
	if all, paid := w.orders(2401); all != 6 || paid != 6 {
		t.Fatalf("after four taps at once: %d orders, %d paid, want 6", all, paid)
	}
	if got := w.balance(2401); got != 450_000 {
		t.Fatalf("balance %d, want 450000 (1,350,000 less two purchases of 450,000)", got)
	}
	var purchases int
	if err := w.stack.Store.DB().QueryRow(context.Background(), `SELECT count(*) FROM core.ledger_entries WHERE kind = 'purchase'`).Scan(&purchases); err != nil || purchases != 6 {
		t.Fatalf("%d purchases in the ledger (%v)", purchases, err)
	}
	// Two deliveries, one message each.
	w.eventually("both purchases delivered", func() bool { return len(u.containing("services are ready!")) == 2 })
	w.drain()
	if n := len(u.containing("services are ready!")); n != 2 {
		t.Fatalf("%d delivery messages for 2 purchases", n)
	}
}
