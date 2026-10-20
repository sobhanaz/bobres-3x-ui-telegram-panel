package handler

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/notify"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/state"
	corestore "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/eventbus"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testenv"
)

// A wallet that cannot pay for all of it is told what is missing and offered the
// way to top up (the exact amount when a top-up takes it), without Pay; a screen
// that went stale because the money was spent meanwhile is refused the same way,
// and nothing is made.
func TestBulkNeedsTheWholeTotalInTheBot(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	u := w.customer(2501, "short")
	w.fund(2501, 400_000)
	u.startBulk("3")
	u.shownHas("👛 Wallet: 400,000 Toman. You need 50,000 Toman more")
	if contains(u.shown().labels(), payThree) || hasCallback(u.shown(), "bk:y:") {
		t.Fatalf("Pay is offered with a short wallet: %v", u.shown().labels())
	}
	topup := u.dataOf("➕ Top up wallet")
	if topup != "wamt:50000" {
		t.Fatalf("the top-up button carries %q, want the missing amount", topup)
	}
	u.tap(topup)
	u.shownHas("Add <b>50,000 Toman</b> with:")

	// Topped up: the same purchase can be paid.
	w.fund(2501, 100_000)
	u.startBulk("3")
	if !contains(u.shown().labels(), payThree) {
		t.Fatalf("no Pay after the top-up: %v", u.shown().labels())
	}
	stale := u.dataOf(payThree)

	// The money is spent before Pay is tapped on the screen that was shown.
	w.spend(2501, 300_000)
	u.tap(stale)
	u.shownHas("Your wallet (200,000 Toman) is not enough for 3 services (450,000 Toman)")
	u.shownHas("Top up at least 250,000 Toman more")
	if all, _ := w.orders(2501); all != 0 {
		t.Fatalf("%d orders were made", all)
	}
	if got := w.balance(2501); got != 200_000 {
		t.Fatalf("balance %d", got)
	}
	if d := u.dataOf("➕ Top up wallet"); d != "wamt:250000" {
		t.Fatalf("the top-up button: %q", d)
	}

	// A missing amount below what a top-up takes goes to the amounts screen.
	w.fund(2501, 245_000) // 445,000: 5,000 short
	u.startBulk("3")
	if d := u.dataOf("➕ Top up wallet"); d != "wtop" {
		t.Fatalf("5,000 short: the top-up button carries %q", d)
	}
}

// A price that changed since the total was shown is not charged: the customer
// sees the new total, and pays that one.
func TestBulkPriceChangedSinceTheTotalWasShown(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	u := w.customer(2511, "price")
	w.fund(2511, 1_000_000)
	u.startBulk("3")
	pay := u.dataOf(payThree)
	w.setPlanPrice(160_000)
	u.tap(pay)
	u.toast("The price changed")
	u.shownHas("Total: <b>480,000 Toman</b>")
	if all, _ := w.orders(2511); all != 0 {
		t.Fatalf("%d orders at the old price", all)
	}
	if got := w.balance(2511); got != 1_000_000 {
		t.Fatalf("balance %d", got)
	}
	u.pressLabel("👛 Pay 480,000 Toman from the wallet")
	u.shownHas("Paid 480,000 Toman")
	if got := w.balance(2511); got != 520_000 {
		t.Fatalf("balance %d after paying the new price", got)
	}
}

// The owner's limit (bulk.max_quantity) decides the quick buttons and the
// numbers taken, as the bot reads it, and core refuses what is beyond it even
// when the bot's copy of the setting is old.
func TestBulkQuantityLimitFromTheOwner(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	u := w.customer(2521, "limit")
	w.fund(2521, 5_000_000)

	owner.text("/set bulk.max_quantity 4")
	owner.sees("Saved")
	u.press("bk")
	u.pressLabel("Monthly 50GB")
	u.shownHas("from 2 to 4")
	labels := u.shown().labels()
	if !contains(labels, "2") || !contains(labels, "3") || contains(labels, "5") || contains(labels, "10") {
		t.Fatalf("quick buttons with a limit of 4: %v", labels)
	}
	u.pressLabel("✏️ Another number")
	u.text("5")
	u.lastHas("Please send a whole number from 2 to 4")
	u.text("4")
	u.shownHas("Services: <b>4</b>")

	owner.text("/set bulk.max_quantity 100")
	u.startBulk("10") // the quick button is there again
	u.shownHas("Services: <b>10</b>")
	u.text("/menu")
	u.press("bk")
	u.pressLabel("Monthly 50GB")
	u.pressLabel("✏️ Another number")
	u.text("100")
	u.shownHas("Services: <b>100</b>")
	u.shownHas("Total: <b>15,000,000 Toman</b>")

	// The owner lowers it: a quantity button of a screen open from before is
	// not followed; the bot goes back to the quantity.
	u.text("/menu")
	u.press("bk")
	u.pressLabel("Monthly 50GB")
	quick := u.dataOf("10")
	owner.text("/set bulk.max_quantity 4")
	u.tap(quick)
	u.shownHas("from 2 to 4")
	if strings.Contains(u.shown().text, "Total:") {
		t.Fatalf("a quantity over the limit reached the total: %q", u.shown().text)
	}
	// And when the bot's copy of the setting is old, core is the one that says no.
	w.setInCore("bulk.max_quantity", "3")
	u.text("/menu")
	u.press("bk")
	u.pressLabel("Monthly 50GB")
	u.pressLabel("✏️ Another number")
	u.text("4")                             // the bot still thinks 4 is allowed
	u.lastHas("Please send a whole number") // a typed answer gets its refusal as a message
	if strings.Contains(u.shown().text, "Total:") {
		t.Fatalf("core quoted past its limit: %q", u.shown().text)
	}
	if all, _ := w.orders(2521); all != 0 {
		t.Fatalf("%d orders", all)
	}
	for _, bad := range []string{"1", "101", "many"} {
		owner.text("/set bulk.max_quantity " + bad)
		owner.lastHas("a whole number from 2 to 100")
	}
}

// A customer who is held back by the store's gates cannot start a bulk purchase,
// and one who is banned cannot finish one; a store that switched bulk purchase
// off refuses it in core even when the bot was not told yet.
func TestBulkIsHeldByTheGatesAndTheBan(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	u := w.customer(2531, "gated")
	w.fund(2531, 1_000_000)
	u.startBulk("3")
	pay := u.dataOf(payThree)

	// Maintenance: the buttons answer with the maintenance text, and a question
	// left open is not answered.
	owner.text("/set maintenance.enabled true")
	owner.sees("Saved")
	u.tap("bk")
	u.toast("under maintenance")
	u.tap(pay)
	u.toast("under maintenance")
	if all, _ := w.orders(2531); all != 0 {
		t.Fatalf("an order was made during maintenance")
	}
	owner.text("/set maintenance.enabled false")

	// The required channel: not a member, no bulk purchase.
	owner.text("/set join.channel @testchan")
	owner.sees("Saved")
	u.press("bk")
	u.lastHas("Join our channel")
	u.tap(pay)
	u.lastHas("Join our channel")
	if all, _ := w.orders(2531); all != 0 {
		t.Fatalf("an order was made by a customer who has not joined the channel")
	}
	w.tg.setMember(2531, "member")
	u.press("jn")
	u.lastHas("Main menu")
	owner.text("/set join.channel")

	// Banned: the total cannot be quoted and a payment on a screen from before is refused.
	st := w.stack.Store
	user := w.storeUser(2531)
	if err := st.SetUserRole(context.Background(), st.Conn(), user.ID, "user", "banned"); err != nil {
		t.Fatal(err)
	}
	u.tap(pay)
	u.toast("Your account is restricted")
	u.text("/menu")
	u.press("bk")
	u.pressLabel("Monthly 50GB")
	u.pressLabel("3")
	u.toast("Your account is restricted")
	if all, _ := w.orders(2531); all != 0 {
		t.Fatalf("a banned customer made %d orders", all)
	}
	if err := st.SetUserRole(context.Background(), st.Conn(), user.ID, "user", "active"); err != nil {
		t.Fatal(err)
	}

	// Switched off in core while the bot has not heard: core refuses.
	u.startBulk("3")
	pay = u.dataOf(payThree)
	w.setInCore("bulk.enabled", "false")
	u.tap(pay)
	u.toast("Bulk purchase is not available right now.")
	if all, _ := w.orders(2531); all != 0 {
		t.Fatalf("an order was made in a store with bulk purchase off")
	}
	if got := w.balance(2531); got != 1_000_000 {
		t.Fatalf("balance %d", got)
	}
}

// fillServices makes n delivered services of other customers (the free tier
// allows 25 once the trial is over), so the places left are what is asked for.
func fillServices(t *testing.T, w *world, n int) {
	t.Helper()
	ctx := context.Background()
	st := w.stack.Store
	if _, err := st.InstallID(ctx, st.Conn()); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec(ctx, `UPDATE core.install SET trial_started_at = now() - interval '31 days'`); err != nil {
		t.Fatal(err)
	}
	plan := &corestore.Plan{NameI18n: map[string]string{"en": "Seed"}, Kind: "time", Price: 1000, Currency: "IRT", Enabled: true}
	if err := st.UpsertPlan(ctx, st.Conn(), plan); err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(10 * 24 * time.Hour)
	for i := range n {
		u, err := st.UpsertUser(ctx, st.Conn(), int64(82000+i), "", "", "")
		if err != nil {
			t.Fatal(err)
		}
		o, _, err := st.CreateOrder(ctx, st.Conn(), &corestore.Order{UserID: u.ID, PlanID: plan.ID, Type: "new", Status: "active",
			Amount: 1000, Currency: "IRT", IdempotencyKey: fmt.Sprintf("fill-bulk-%d", i)})
		if err != nil {
			t.Fatal(err)
		}
		sub, err := st.CreateSubscription(ctx, st.Conn(), &corestore.Subscription{UserID: u.ID, OrderID: o.ID, ClientEmail: fmt.Sprintf("fillbulk%d", i)})
		if err != nil {
			t.Fatal(err)
		}
		if err := st.ActivateSubscription(ctx, st.Conn(), sub.ID, "00000000-0000-7000-8000-000000000001", "x", "", &expires, nil); err != nil {
			t.Fatal(err)
		}
	}
}

// A bulk purchase needs places for ALL its services: three at twenty-three of
// twenty-five are refused with the same neutral words as any new purchase (never
// a word about licensing), two fit, and nothing is charged for a refused one.
func TestBulkAtTheServiceLimit(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	fillServices(t, w, 23) // the free tier's 25: two places left
	u := w.customer(2701, "limited")
	w.fund(2701, 2_000_000)

	const paused = "New purchases are temporarily unavailable. Please contact support."
	u.startBulk("3")
	u.toast(paused)
	if strings.Contains(u.shown().text, "Total:") {
		t.Fatalf("a total was shown for a purchase that cannot be made: %q", u.shown().text)
	}
	if all, _ := w.orders(2701); all != 0 {
		t.Fatalf("%d orders", all)
	}
	u.pressLabel("2")
	u.shownHas("Total: <b>300,000 Toman</b>")
	u.pressLabel("👛 Pay 300,000 Toman from the wallet")
	u.shownHas("Paid 300,000 Toman")
	if got := w.balance(2701); got != 1_700_000 {
		t.Fatalf("balance %d", got)
	}
	u.eventuallySees("Your 2 services are ready!")
	// All places are taken now: not even two more, and the single purchase is refused too.
	u.startBulk("2")
	u.toast(paused)
	if got := w.balance(2701); got != 1_700_000 {
		t.Fatalf("balance %d after a refused purchase", got)
	}
	var seen []string
	for _, m := range w.tg.all(2701) {
		seen = append(seen, m.text)
		seen = append(seen, m.labels()...)
	}
	w.tg.mu.Lock()
	seen = append(seen, w.tg.toasts[2701]...)
	w.tg.mu.Unlock()
	for _, s := range seen {
		low := strings.ToLower(s)
		for _, word := range []string{"licen", "free tier", "مجوز", "پروانه", "لایسنس"} {
			if strings.Contains(low, word) {
				t.Errorf("the customer was shown %q (%q)", s, word)
			}
		}
	}
}

// A reseller buys in bulk at their tier price on credit: the screen says how much
// they can spend, past it there is no Pay and no top-up, they name the services,
// and the debt and the delivery are as for any purchase of theirs.
func TestBulkByAReseller(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	rs := w.customer(2801, "shop")
	w.makeReseller(2801, 20, 10, 200_000)

	rs.text("/menu")
	rs.press("bk")
	rs.lastButton("120,000 Toman") // the tier's price on the plan's button
	rs.pressLabel("Monthly 50GB")
	rs.shownHas("Price for each service: <b>120,000 Toman</b>")
	rs.pressLabel("2")
	rs.shownHas("Total: <b>240,000 Toman</b>")
	rs.shownHas("Not enough credit: you can spend 200,000 Toman now, and 2 services cost 240,000 Toman.")
	for _, b := range buttons(rs.shown()) {
		if strings.HasPrefix(b.CallbackData, "bk:y:") || strings.HasPrefix(b.CallbackData, "wamt") || b.CallbackData == "wtop" {
			t.Fatalf("a reseller past their credit is offered %+v", b)
		}
	}
	if all, _ := w.orders(2801); all != 0 {
		t.Fatalf("%d orders", all)
	}

	w.setCreditLimit(2801, 500_000)
	rs.startBulk("2")
	rs.shownHas("👛 Available to spend: 500,000 Toman")
	rs.pressLabel("🏷 Name the services")
	rs.text("Ali")
	rs.shownHas("Names: <b>Ali 1</b> to <b>Ali 2</b>")
	rs.pressLabel("👛 Pay 240,000 Toman from the wallet")
	rs.shownHas("Paid 240,000 Toman")
	if !contains(rs.shown().labels(), "🤝 Reseller panel") {
		t.Fatalf("the paid screen: %v", rs.shown().labels())
	}
	if got := w.balance(2801); got != -240_000 {
		t.Fatalf("balance %d: they owe 240000", got)
	}
	rs.eventuallySees("Your 2 services are ready!")
	msg := rs.containing("services are ready!")[0]
	for _, want := range []string{"1. <b>Ali 1</b>", "2. <b>Ali 2</b>"} {
		if !strings.Contains(msg.text, want) {
			t.Errorf("the delivery lacks %q: %q", want, msg.text)
		}
	}
	rs.press("subs") // a reseller's list of services, with the names
	if joined := strings.Join(serviceLabels(rs.shown()), "|"); !strings.Contains(joined, "Ali 1") || !strings.Contains(joined, "Ali 2") {
		t.Fatalf("the reseller's services: %v", serviceLabels(rs.shown()))
	}
	// 260,000 are left to spend: three at 120,000 are refused whole, with the way out.
	rs.startBulk("3")
	rs.shownHas("Not enough credit: you can spend 260,000 Toman now, and 3 services cost 360,000 Toman.")
}

// The staff are told about a bulk sale once, for the whole quantity, and never once
// per order.
func TestBulkSaleAlertIsOne(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	owner.text("/set notify.sales true")
	owner.sees("Saved")
	u := w.customer(2901, "buyer")
	w.fund(2901, 1_000_000)
	u.startBulk("3")
	u.pressLabel(payThree)
	owner.eventuallySees("New bulk sale")
	u.eventuallySees("Your 3 services are ready!")
	w.drain()
	if n := len(owner.containing("New bulk sale")); n != 1 {
		t.Fatalf("%d bulk sale alerts", n)
	}
	m := owner.containing("New bulk sale")[0]
	if !strings.Contains(m.text, "3 × Monthly 50GB") || !strings.Contains(m.text, "450,000 Toman") || !strings.Contains(m.text, "@buyer") {
		t.Fatalf("the alert: %q", m.text)
	}
	if n := len(owner.containing("New sale")); n != 0 {
		t.Fatalf("%d single sale alerts for a bulk", n)
	}
}

// newWorldWithGates is newWorld on a stack whose provisioner, database and core
// can be cut off and put back (testenv.WithGates).
func newWorldWithGates(t *testing.T) *world {
	t.Helper()
	stack := testenv.Start(t, ownerID, testenv.WithGates())
	ftg := newFakeTG()
	cat := i18n.MustLoad()
	h := New(stack.Core, ftg, state.NewMemory(), cat, nil, Config{AdminTelegramIDs: []int64{ownerID}, BotName: "TestVPN", BotUsername: "test_vpn_bot"}, nil)
	n := notify.New(stack.Core, ftg, cat, h.Settings(), []int64{ownerID}, nil)
	feed, err := eventbus.NewConsumer(eventbus.ConsumerConfig{
		Source:     eventbus.NewGRPCSource(stack.Feed),
		Handle:     n.Handle,
		DeadLetter: n.DeadLetter,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &world{t: t, h: h, tg: ftg, stack: stack, notifier: n, feed: feed}
}

// The provisioner is down when a bulk purchase is paid: every service fails, is
// retried, and the staff are told about each (a failed delivery, per service) while
// the customer gets ONE message that names every service as being prepared (not
// one "taking longer" note per service). When the provisioner is back, each
// service is delivered on its own, once, and nothing paid for is lost.
func TestBulkWithTheProvisionerDown(t *testing.T) {
	w := newWorldWithGates(t)
	owner := setupStore(t, w)
	u := w.customer(2951, "unlucky")
	w.fund(2951, 1_000_000)
	w.stack.Gates.Provisioner.Down()

	// The retry waits (30 seconds, a minute...) are made due at once.
	ctx, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	go func() {
		tick := time.NewTicker(25 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				_, _ = w.stack.Store.DB().Exec(ctx, `UPDATE core.orders SET next_attempt_at = now() WHERE status = 'provision_failed'`)
			}
		}
	}()

	u.startBulk("3")
	u.pressLabel(payThree)
	u.shownHas("Paid 450,000 Toman")

	// One message, naming the three, none of them ready.
	u.eventuallySees("Your 3 services are not ready yet")
	msg := u.containing("are not ready yet")[0]
	if n := strings.Count(msg.text, "⏳ Being prepared"); n != 3 || strings.Contains(msg.text, "<code>") {
		t.Fatalf("the message: %q", msg.text)
	}
	// The staff were told about each service, once.
	w.eventually("three failed-delivery alerts", func() bool { return len(owner.containing("Provisioning failed 3 times")) == 3 })
	// The customer was not sent a note per service.
	w.drain()
	if n := len(u.containing("Preparing your service is taking longer than usual")); n != 0 {
		t.Fatalf("%d per-service delay notes", n)
	}
	if n := len(u.containing("are not ready yet")); n != 1 {
		t.Fatalf("%d bulk messages", n)
	}
	// Nothing was lost: the three orders are paid and wait for their delivery.
	var all, received int
	if err := w.stack.Store.DB().QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE status IN ('paid', 'provisioning', 'provision_failed', 'active'))
		FROM core.orders WHERE user_id = $1`, w.storeUser(2951).ID).Scan(&all, &received); err != nil || all != 3 || received != 3 {
		t.Fatalf("orders %d, paid %d (%v)", all, received, err)
	}

	// The provisioner is back: each service is delivered on its own.
	w.stack.Gates.Provisioner.Up()
	w.eventually("the three services delivered on their own", func() bool { return len(u.containing("Your service is ready!")) == 3 })
	w.drain()
	if n := len(u.containing("Your service is ready!")); n != 3 {
		t.Fatalf("%d single deliveries", n)
	}
	if n := len(u.containing("are not ready yet")); n != 1 {
		t.Fatalf("the bulk message was sent again: %d", n)
	}
	st := w.stack.Store
	subs, err := st.ListSubscriptions(context.Background(), st.Conn(), w.storeUser(2951).ID, 10)
	if err != nil || len(subs) != 3 {
		t.Fatalf("services: %d %v", len(subs), err)
	}
	for _, s := range subs {
		if s.Status != "active" {
			t.Fatalf("service %s is %s", s.ID, s.Status)
		}
	}
	if got := w.balance(2951); got != 550_000 {
		t.Fatalf("balance %d: charged once, 450000", got)
	}
}
