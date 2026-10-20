package handler

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	coredomain "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	corestore "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testenv"
	buuid "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// --- what the owner does in the dashboard, through core's domain ---

// makeReseller makes a registered customer a reseller on a tier of their own
// (discount and commission in %), with a credit limit in Toman.
func (w *world) makeReseller(telegramID int64, discount, commission int32, creditLimit int64) {
	w.t.Helper()
	ctx := context.Background()
	d, owner := w.dom(), w.storeUser(ownerID)
	tier, err := d.CreateTier(ctx, owner, coredomain.TierInput{Name: fmt.Sprintf("Tier %d", telegramID),
		DiscountPercent: discount, CommissionPercent: commission}, "")
	if err != nil {
		w.t.Fatal(err)
	}
	if _, err := d.MakeReseller(ctx, owner, w.storeUser(telegramID).ID, coredomain.ResellerInput{TierID: tier.ID,
		CreditLimits: map[string]int64{"IRT": creditLimit}}, "sells to their office"); err != nil {
		w.t.Fatal(err)
	}
}

// setCreditLimit changes a reseller's Toman credit limit, as the owner does.
func (w *world) setCreditLimit(telegramID, limit int64) {
	w.t.Helper()
	ctx := context.Background()
	u := w.storeUser(telegramID)
	r, err := w.stack.Store.GetReseller(ctx, w.stack.Store.Conn(), u.ID, false)
	if err != nil {
		w.t.Fatal(err)
	}
	if _, err := w.dom().UpdateReseller(ctx, w.storeUser(ownerID), u.ID, r.Version, coredomain.ResellerInput{TierID: r.TierID,
		CreditLimits: map[string]int64{"IRT": limit}}, "more credit"); err != nil {
		w.t.Fatal(err)
	}
}

// recordPayment records money a reseller paid the store (key: a retry).
func (w *world) recordPayment(telegramID, amount int64, key string) {
	w.t.Helper()
	if _, _, err := w.dom().RecordResellerPayment(context.Background(), w.storeUser(ownerID), w.storeUser(telegramID).ID,
		amount, "IRT", "paid by card", key); err != nil {
		w.t.Fatal(err)
	}
}

// seedService gives a user a delivered service directly in the store (no
// purchase), named label ("" = none); it returns the service's id.
func (w *world) seedService(telegramID int64, label string) string {
	w.t.Helper()
	ctx := context.Background()
	st := w.stack.Store
	u := w.storeUser(telegramID)
	plans, err := st.ListPlans(ctx, st.Conn(), false)
	if err != nil {
		w.t.Fatal(err)
	}
	i := slices.IndexFunc(plans, func(p corestore.Plan) bool { return !p.IsTrial && !p.IsTopup })
	if i < 0 {
		w.t.Fatal("no plan to seed a service with")
	}
	o, _, err := st.CreateOrder(ctx, st.Conn(), &corestore.Order{UserID: u.ID, PlanID: plans[i].ID, Type: "new", Status: "active",
		Amount: plans[i].Price, Currency: plans[i].Currency, IdempotencyKey: "seed-" + buuid.MustV7().String()})
	if err != nil {
		w.t.Fatal(err)
	}
	var name *string
	if label != "" {
		name = &label
	}
	server := buuid.MustV7().String()
	sub, err := st.CreateSubscription(ctx, st.Conn(), &corestore.Subscription{UserID: u.ID, OrderID: o.ID, ServerID: server,
		ClientEmail: "seed-" + o.ID, Status: "active", Label: name})
	if err != nil {
		w.t.Fatal(err)
	}
	expires, traffic := time.Now().Add(30*24*time.Hour), int64(50<<30)
	if err := st.ActivateSubscription(ctx, st.Conn(), sub.ID, server, "", testenv.SubBase+sub.ID, &expires, &traffic); err != nil {
		w.t.Fatal(err)
	}
	return sub.ID
}

func (w *world) subscription(id string) *corestore.Subscription {
	w.t.Helper()
	s, err := w.stack.Store.GetSubscription(context.Background(), w.stack.Store.Conn(), id)
	if err != nil {
		w.t.Fatal(err)
	}
	return s
}

// orders counts a user's orders, and those whose money was received.
func (w *world) orders(telegramID int64) (all, paid int) {
	w.t.Helper()
	if err := w.stack.Store.DB().QueryRow(context.Background(), `SELECT count(*),
		count(*) FILTER (WHERE status IN ('paid', 'provisioning', 'active')) FROM core.orders WHERE user_id = $1`,
		w.storeUser(telegramID).ID).Scan(&all, &paid); err != nil {
		w.t.Fatal(err)
	}
	return all, paid
}

// shown is the message the user looks at: the one sent or edited last.
func (p *person) shown() *msg {
	p.w.tg.mu.Lock()
	defer p.w.tg.mu.Unlock()
	if m := p.w.tg.shown[p.id]; m != nil {
		return m
	}
	return &msg{}
}

func (p *person) shownHas(sub string) {
	p.w.t.Helper()
	if m := p.shown(); !strings.Contains(m.text, sub) {
		p.w.t.Fatalf("user %d: shown %q, want %q", p.id, m.text, sub)
	}
}

// lastButton checks a button label of the message shown.
func (p *person) lastButton(sub string) {
	p.w.t.Helper()
	if !strings.Contains(strings.Join(p.shown().labels(), "\n"), sub) {
		p.w.t.Fatalf("user %d: buttons %v, want %q", p.id, p.shown().labels(), sub)
	}
}

// tap sends a callback with any data, as an old or crafted button would.
func (p *person) tap(data string) {
	id := strconv.FormatInt(p.w.updateID(), 10)
	p.w.tg.mu.Lock()
	p.w.tg.cbFrom[id] = p.id
	p.w.tg.mu.Unlock()
	p.w.h.Handle(context.Background(), tg.Update{UpdateID: p.w.updateID(), CallbackQuery: &tg.CallbackQuery{
		ID: id, From: *p.from(), Data: data, Message: &tg.Message{MessageID: p.last().id, Chat: tg.Chat{ID: p.id, Type: "private"}},
	}})
}

// callbacks are the callback data of a message's buttons.
func callbacks(m *msg) []string {
	var out []string
	for _, b := range buttons(m) {
		out = append(out, b.CallbackData)
	}
	return out
}

// hasCallback reports whether one of a message's buttons starts with prefix.
func hasCallback(m *msg, prefix string) bool {
	return slices.ContainsFunc(callbacks(m), func(d string) bool { return strings.HasPrefix(d, prefix) })
}

// serviceLabels are the service buttons ("sub:") of a list, in order.
func serviceLabels(m *msg) []string {
	var out []string
	for _, b := range buttons(m) {
		if strings.HasPrefix(b.CallbackData, "sub:") {
			out = append(out, b.Text)
		}
	}
	return out
}

// --- tests ---

// A reseller sees their panel, buys for a customer at their tier's price
// with a name on credit, and is refused past the limit with what they can
// still spend; the name follows the service into its messages.
func TestResellerBuysForACustomerOnCredit(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	rs := w.customer(1501, "rsl")
	w.makeReseller(1501, 20, 10, 200_000)

	// The regular Buy button shows the tier price too, with no discount code
	// and the wallet labelled with what can be spent.
	rs.text("/menu")
	if labels := strings.Join(rs.shown().labels(), "|"); !strings.Contains(labels, "🤝 Reseller panel") || strings.Contains(labels, "Invite") {
		t.Fatalf("reseller's home: %s", labels)
	}
	rs.press("buy")
	rs.lastButton("Monthly 50GB · 120,000 Toman")
	rs.press("plan:")
	checkoutB := rs.shown()
	rs.shownHas("Price: <b>120,000 Toman</b>")
	rs.shownHas("👛 Available to spend: 200,000 Toman")
	if hasCallback(checkoutB, "dc:") || !slices.Contains(checkoutB.labels(), "👛 Wallet (200,000 Toman available)") {
		t.Fatalf("reseller's checkout buttons: %v", checkoutB.labels())
	}

	rs.text("/menu")
	rs.press("rs")
	for _, want := range []string{"🤝 <b>Reseller panel</b>", "Tier: <b>Tier 1501</b> · 20% off store prices", "Commission: 10%",
		"Balance: <b>0 Toman</b>", "Credit limit: 200,000 Toman", "Available to spend: <b>200,000 Toman</b>",
		"Customers who joined with your link: 0", "Commission earned: 0 Toman"} {
		rs.shownHas(want)
	}

	// Buy for a customer: the plans at tier prices, a name, the checkout.
	rs.press("rs:buy")
	rs.lastButton("Monthly 50GB · 120,000 Toman")
	rs.press("rs:p:")
	rs.shownHas("Type a name for this service")
	rs.text(strings.Repeat("x", 65))
	rs.shownHas("at most 64 characters")
	if !hasCallback(rs.shown(), "rs:sk:") {
		t.Fatal("Skip is gone after a name that was too long")
	}
	rs.text("  Ali's   <phone>\n2 ")
	checkoutA := rs.shown()
	rs.shownHas("🏷 <b>Ali&#39;s &lt;phone&gt; 2</b>")
	rs.shownHas("Price: <b>120,000 Toman</b>")
	if hasCallback(checkoutA, "dc:") || !hasCallback(checkoutA, "pay:w:") || !hasCallback(checkoutA, "rs:buy") {
		t.Fatalf("checkout for a customer: %v", callbacks(checkoutA))
	}

	// Paid on credit (the newest checkout first).
	rs.press("pay:w:")
	rs.shownHas("Paid from your wallet")
	if !slices.Contains(callbacks(rs.shown()), "rs") {
		t.Fatal("no way back to the panel after paying")
	}
	rs.eventuallySees("Your service is ready")
	sub := w.onlySub(1501)
	if sub.Label == nil || *sub.Label != "Ali's <phone> 2" {
		t.Fatalf("service name: %v", sub.Label)
	}
	o, err := w.stack.Store.GetOrder(context.Background(), w.stack.Store.Conn(), sub.OrderID)
	if err != nil || o.Amount != 120_000 {
		t.Fatalf("order: %+v %v", o, err)
	}
	rs.sees("🏷 <b>Ali&#39;s &lt;phone&gt; 2</b>\n🎉 <b>Your service is ready!</b>")

	// The older checkout, past the limit: what can still be spent, a way
	// back to the panel, and the other payment methods.
	rs.press("pay:w:")
	rs.shownHas("Not enough credit: you can spend 80,000 Toman now, and this costs 120,000 Toman.")
	if cb := callbacks(rs.shown()); !slices.Contains(cb, "rs") || !hasCallback(rs.shown(), "pay:c:") || hasCallback(rs.shown(), "wtop") {
		t.Fatalf("buttons over the limit: %v", cb)
	}
	if all, paid := w.orders(1501); all != 2 || paid != 1 {
		t.Fatalf("orders: %d, paid %d; want 2, 1 paid", all, paid)
	}

	rs.press("rs")
	rs.shownHas("Balance: <b>debt 120,000 Toman</b>")
	rs.shownHas("Available to spend: <b>80,000 Toman</b>")
	// A checkout the credit does not cover has no wallet button.
	rs.press("rs:buy")
	rs.press("rs:p:")
	rs.press("rs:sk:")
	rs.shownHas("👛 Available to spend: 80,000 Toman")
	if hasCallback(rs.shown(), "pay:w:") || strings.Contains(rs.shown().text, "🏷") {
		t.Fatalf("checkout past the credit: %v %q", callbacks(rs.shown()), rs.shown().text)
	}
	rs.text("/menu")
	rs.press("wallet")
	rs.shownHas("Balance: <b>debt 120,000 Toman</b>")
	rs.shownHas("Credit limit: 200,000 Toman")

	// More credit: the renewal (at the tier price) goes through, and its
	// messages carry the name.
	w.setCreditLimit(1501, 1_000_000)
	rs.text("/menu")
	rs.press("subs")
	rs.shownHas("Customer services</b> (1)")
	rs.lastButton("Ali's <phone> 2 · Active ✅ · ")
	rs.press("sub:" + sub.ID)
	rs.shownHas("🏷 <b>Ali&#39;s &lt;phone&gt; 2</b>\n📦 <b>Service</b>")
	rs.press("rnw:")
	rs.shownHas("🏷 <b>Ali&#39;s &lt;phone&gt; 2</b>\n🔄 <b>Renew service #")
	rs.lastButton("Monthly 50GB · 120,000 Toman")
	rs.press("rp:")
	rs.press("pay:w:")
	rs.shownHas("Paid from your wallet")
	rs.eventuallySees("🏷 <b>Ali&#39;s &lt;phone&gt; 2</b>\n✅ <b>Service #")

	usage := w.stack.Usage
	usage.Every = -time.Hour
	w.stack.Panel.AddUsage(sub.ClientEmail, 0, 90<<30)
	if _, err := usage.SyncDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	rs.eventuallySees("🏷 <b>Ali&#39;s &lt;phone&gt; 2</b>\n📉 Service #")
}

// A reseller's services: 8 a page, a search (paged with it) by name or
// number, and a rename; nobody can name someone else's service.
func TestResellerServicesPagingSearchAndRename(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	rs := w.customer(1601, "rsl")
	w.makeReseller(1601, 0, 10, 0)
	ids := map[string]string{}
	for i := 1; i <= 9; i++ {
		name := fmt.Sprintf("cust-%02d", i)
		ids[name] = w.seedService(1601, name)
	}
	unnamed := w.seedService(1601, "") // the newest

	rs.text("/menu")
	rs.press("rs")
	rs.press("rs:subs:1")
	rs.shownHas("📦 <b>Customer services</b> (10)\nPage 1 of 2.")
	got := serviceLabels(rs.shown())
	if len(got) != 8 || !strings.HasPrefix(got[0], "#"+shortID(unnamed)+" · Active ✅") || !strings.HasPrefix(got[1], "cust-09 · Active ✅") ||
		!strings.HasPrefix(got[7], "cust-03 · ") {
		t.Fatalf("page 1: %v", got)
	}
	if cb := callbacks(rs.shown()); !slices.Contains(cb, "rs:subs:2") || slices.Contains(cb, "rs:subs:0") {
		t.Fatalf("page 1 buttons: %v", cb)
	}
	rs.press("rs:subs:2")
	rs.shownHas("Page 2 of 2.")
	if got := serviceLabels(rs.shown()); len(got) != 2 || !strings.HasPrefix(got[0], "cust-02") || !strings.HasPrefix(got[1], "cust-01") {
		t.Fatalf("page 2: %v", got)
	}
	if cb := callbacks(rs.shown()); !slices.Contains(cb, "rs:subs:1") || slices.Contains(cb, "rs:subs:3") {
		t.Fatalf("page 2 buttons: %v", cb)
	}
	rs.tap("rs:subs:99") // past the end: the first page
	rs.shownHas("Page 1 of 2.")
	rs.press("rs:subs:2")
	rs.tap("rs:subs:x")
	rs.shownHas("Page 1 of 2.")
	// "My services" is the same list for a reseller.
	rs.text("/menu")
	rs.press("subs")
	rs.shownHas("Customer services</b> (10)")

	// A search, in any case, paged with the search.
	rs.press("rs:find")
	rs.shownHas("Send part of a service's name")
	rs.text("  CUST-0 ")
	rs.shownHas("🔎 Services matching <b>CUST-0</b> (9)\nPage 1 of 2.")
	if got := serviceLabels(rs.shown()); len(got) != 8 {
		t.Fatalf("search page 1: %v", got)
	}
	rs.press("rs:sq:2:CUST-0")
	rs.shownHas("🔎 Services matching <b>CUST-0</b> (9)\nPage 2 of 2.")
	if got := serviceLabels(rs.shown()); len(got) != 1 || !strings.HasPrefix(got[0], "cust-01") {
		t.Fatalf("search page 2: %v", got)
	}
	rs.press("rs:find")
	rs.text("#" + shortID(unnamed))
	if got := serviceLabels(rs.shown()); len(got) != 1 || !strings.HasPrefix(got[0], "#"+shortID(unnamed)) {
		t.Fatalf("search by number: %v", got)
	}
	rs.press("rs:find")
	rs.text("<nobody>")
	rs.shownHas("🔎 No service matches <b>&lt;nobody&gt;</b>.")
	rs.press("rs:subs:1") // All services
	rs.shownHas("(10)")

	// Open a service and rename it.
	rs.press("sub:" + ids["cust-05"])
	rs.shownHas("🏷 <b>cust-05</b>\n📦 <b>Service</b>")
	rs.press("rs:rn:")
	rs.shownHas("Send the new name for service #" + shortID(ids["cust-05"]))
	rs.text(strings.Repeat("y", 65))
	rs.shownHas("at most 64 characters")
	rs.text("Sara <laptop>")
	rs.shownHas("🏷 <b>Sara &lt;laptop&gt;</b>\n📦 <b>Service</b>")
	if l := w.subscription(ids["cust-05"]).Label; l == nil || *l != "Sara <laptop>" {
		t.Fatalf("renamed: %v", l)
	}
	rs.press("rs:rn:")
	rs.press("rs:rc:")
	rs.toast("Name removed.")
	if l := w.subscription(ids["cust-05"]).Label; l != nil {
		t.Fatalf("name not removed: %q", *l)
	}
	if strings.Contains(rs.shown().text, "🏷") || !strings.Contains(rs.shown().text, "📦 <b>Service</b>") {
		t.Fatalf("service after the name was removed: %q", rs.shown().text)
	}
	// Cancel keeps the name question from taking the next message.
	rs.press("rs:rn:")
	rs.press("cancel")
	rs.text("not a name")
	if l := w.subscription(ids["cust-05"]).Label; l != nil {
		t.Fatalf("a message after Cancel renamed the service: %q", *l)
	}

	// Someone else's service cannot be named, even with a crafted button.
	other := w.customer(1602, "other")
	theirs := w.seedService(1602, "")
	w.makeReseller(1602, 0, 0, 0)
	other.tap("rs:rc:" + ids["cust-06"])
	other.tap("rs:rn:" + ids["cust-06"])
	if l := w.subscription(ids["cust-06"]).Label; l == nil || *l != "cust-06" {
		t.Fatalf("another reseller changed the name: %v", l)
	}
	if _, err := w.stack.Core.SetSubscriptionLabel(context.Background(), &corev1.SetSubscriptionLabelRequest{UserId: w.storeUser(1601).ID,
		SubscriptionId: theirs, Label: "mine"}); status.Code(err) != codes.NotFound {
		t.Fatalf("naming someone else's service: %v", err)
	}

	// A long search is cut to fit the paging buttons in Telegram's 64 bytes.
	long := strings.Repeat("ج", 25)
	for i := 1; i <= 9; i++ {
		w.seedService(1601, fmt.Sprintf("%s-%02d", long, i))
	}
	rs.text("/menu")
	rs.press("subs")
	rs.press("rs:find")
	rs.text(strings.Repeat("ج", 60))
	cut := strings.Repeat("ج", maxSearch/2)
	rs.shownHas("🔎 Services matching <b>" + cut + "</b> (9)\nPage 1 of 2.")
	for _, d := range callbacks(rs.shown()) {
		if len(d) > 64 {
			t.Fatalf("callback data over 64 bytes: %q", d)
		}
	}
	rs.press("rs:sq:2:" + cut)
	rs.shownHas("Page 2 of 2.")
	if got := serviceLabels(rs.shown()); len(got) != 1 || !strings.HasPrefix(got[0], long+"-01") {
		t.Fatalf("long search page 2: %v", got)
	}
}

// A reseller's invite link works with referrals off; their customers' purchases
// pay a commission (never naming the customer) and no referral reward; the
// owner's payment records are notified; the statement pages; debts are never
// shown with a minus in Persian.
func TestResellerLinkCommissionStatementInPersian(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	rs := w.person(1701, "rsl")
	rs.text("/start")
	rs.press("lang:fa")
	w.makeReseller(1701, 0, 10, 500_000)

	rs.text("/menu")
	rs.press("rs")
	rs.press("rs:link")
	link := rs.lastWith("🔗 <b>لینک دعوت شما</b>") // the promo card and the line about forwarding it come after it
	if !strings.Contains(link.text, "۱۰٪ پورسانت") {
		t.Fatalf("the link screen: %q", link.text)
	}
	m := regexp.MustCompile(`https://t\.me/test_vpn_bot\?start=r_([a-z0-9]+)`).FindStringSubmatch(link.text)
	if m == nil {
		t.Fatalf("no invite link with referrals off: %q", link.text)
	}

	// Referral rewards on: a reseller's customer still pays them nothing.
	owner.text("/set referral.reward_percent 10")
	owner.sees("referral.reward_percent")
	rs.text("/menu")
	if cb := callbacks(rs.shown()); !slices.Contains(cb, "rs") || slices.Contains(cb, "ref") {
		t.Fatalf("reseller's home with referrals on: %v", cb)
	}
	rs.tap("ref") // an old Invite button: their own link
	rs.lastWith("🔗 <b>لینک دعوت شما</b>")
	cust := w.person(1702, "custo")
	cust.text("/start r_" + m[1])
	cust.press("lang:en")
	w.fund(1702, 150_000)
	cust.press("buy")
	cust.lastButton("Monthly 50GB · 150,000 Toman") // store prices for customers
	cust.press("plan:")
	if !hasCallback(cust.shown(), "dc:") || !slices.Contains(cust.shown().labels(), "👛 Wallet (150,000 Toman)") {
		t.Fatalf("customer's checkout: %v", cust.shown().labels())
	}
	cust.press("pay:w:")
	cust.shownHas("Paid from your wallet")
	rs.eventuallySees("🤝 پورسانت خرید یکی از مشتریان شما: <b>۱۵٬۰۰۰ تومان</b> به کیف پولتان اضافه شد.\nموجودی: ۱۵٬۰۰۰ تومان")
	cust.eventuallySees("Your service is ready")
	w.drain()
	for _, msg := range w.tg.all(1701) {
		if strings.Contains(msg.text, "custo") || strings.Contains(msg.text, "دوستانی") {
			t.Fatalf("the reseller was shown the customer or a referral reward: %q", msg.text)
		}
	}

	// The customer sees none of the reseller's screens.
	cust.text("/menu")
	if hasCallback(cust.shown(), "rs") {
		t.Fatalf("customer's home: %v", callbacks(cust.shown()))
	}
	cust.tap("rs")
	cust.toast("This section is for resellers only.")
	cust.shownHas("Main menu")
	sub := w.onlySub(1702)
	cust.tap("rs:rn:" + sub.ID)
	cust.press("subs")
	cust.press("sub:")
	if hasCallback(cust.shown(), "rs:") {
		t.Fatalf("customer's service page: %v", callbacks(cust.shown()))
	}
	if l := w.subscription(sub.ID).Label; l != nil {
		t.Fatalf("a customer's service got a name: %q", *l)
	}

	// The reseller buys on credit: a debt, shown as such in Persian.
	rs.text("/menu")
	rs.press("rs")
	rs.shownHas("👥 مشتریانی که با لینک شما عضو شده\u200cاند: ۱")
	rs.shownHas("💰 پورسانت دریافتی: ۱۵٬۰۰۰ تومان")
	rs.press("rs:buy")
	rs.press("rs:p:")
	rs.press("rs:sk:")
	rs.press("pay:w:")
	rs.shownHas("پرداخت شد")
	rs.press("rs")
	rs.shownHas("👛 موجودی: <b>بدهی ۱۳۵٬۰۰۰ تومان</b>")
	rs.shownHas("✅ قابل خرید: <b>۳۶۵٬۰۰۰ تومان</b>")
	if strings.Contains(rs.shown().text, "-") {
		t.Fatalf("a minus in the Persian panel: %q", rs.shown().text)
	}

	// The owner records payments: each is notified with the balance after
	// (still a debt); a retry with the same key changes nothing.
	for i := range 8 {
		w.recordPayment(1701, 1_000, fmt.Sprintf("settle-key-0000000%02d", i))
	}
	// Exactly one page of 10 (a commission, a purchase, 8 payments): no Next.
	rs.text("/menu")
	rs.press("rs")
	rs.press("rs:st:1")
	rs.shownHas("🧾 <b>صورت\u200cحساب</b> · صفحه ۱")
	if n := strings.Count(rs.shown().text, "\n"); n != 11 || hasCallback(rs.shown(), "rs:st:") {
		t.Fatalf("one full page: %d lines, buttons %v", n, callbacks(rs.shown()))
	}
	for i := 8; i < 11; i++ {
		w.recordPayment(1701, 1_000, fmt.Sprintf("settle-key-0000000%02d", i))
	}
	w.recordPayment(1701, 1_000, "settle-key-000000000")
	rs.eventuallySees("✅ پرداخت <b>۱٬۰۰۰ تومان</b> شما به فروشگاه ثبت شد.\nموجودی: بدهی ۱۲۴٬۰۰۰ تومان")
	w.drain()
	if n := rs.count("به فروشگاه ثبت شد"); n != 11 {
		t.Fatalf("payment notifications: %d, want 11", n)
	}

	// The statement: 13 entries (a commission, a purchase, 11 payments),
	// 10 a page.
	rs.text("/menu")
	rs.press("wallet")
	rs.shownHas("موجودی: <b>بدهی ۱۲۴٬۰۰۰ تومان</b>")
	rs.press("whist") // a reseller's history is their statement
	rs.shownHas("🧾 <b>صورت\u200cحساب</b> · صفحه ۱")
	if n := strings.Count(rs.shown().text, "پرداخت به فروشگاه"); n != 10 {
		t.Fatalf("page 1 has %d payments: %q", n, rs.shown().text)
	}
	if cb := callbacks(rs.shown()); !slices.Contains(cb, "rs:st:2") || slices.Contains(cb, "rs:st:0") {
		t.Fatalf("statement page 1 buttons: %v", cb)
	}
	rs.press("rs:st:2")
	rs.shownHas("صفحه ۲")
	for _, want := range []string{"پرداخت به فروشگاه", "خرید", "پورسانت"} {
		rs.shownHas(want)
	}
	if cb := callbacks(rs.shown()); !slices.Contains(cb, "rs:st:1") || slices.Contains(cb, "rs:st:3") {
		t.Fatalf("statement page 2 buttons: %v", cb)
	}
	rs.tap("rs:st:9") // past the end: the first page
	rs.shownHas("صفحه ۱")
}

// Resellers are gated like customers, and a banned reseller loses the panel.
func TestResellerGatesAndBan(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	rs := w.customer(1801, "rsl")
	w.makeReseller(1801, 0, 10, 100_000)
	rs.text("/menu")
	rs.press("rs")
	rs.shownHas("Reseller panel")

	owner.text("/set maintenance.enabled true")
	owner.sees("Saved")
	rs.press("rs:buy")
	rs.toast("under maintenance")
	owner.text("/set maintenance.enabled false")

	// A name question left open does not take the next message once another
	// button of the panel was pressed.
	rs.press("rs:buy")
	rs.press("rs:p:")
	rs.shownHas("Type a name")
	rs.tap("rs")
	rs.shownHas("Reseller panel")
	rs.text("Ali")
	if rs.count("🏷 <b>Ali</b>") != 0 {
		t.Fatal("a message after leaving the name question named a purchase")
	}

	rs.press("rs:buy")
	rs.press("rs:p:")
	rs.shownHas("Type a name")
	if err := w.stack.Store.SetUserStatus(context.Background(), w.stack.Store.Conn(), w.storeUser(1801).ID, "banned"); err != nil {
		t.Fatal(err)
	}
	rs.text("Ali") // the name question no longer applies
	rs.shownHas("Main menu")
	rs.sees("Your account is restricted")
	rs.tap("rs")
	rs.toast("restricted")
	if hasCallback(rs.shown(), "rs") {
		t.Fatalf("a banned reseller's home: %v", callbacks(rs.shown()))
	}
}

// A customer who becomes a reseller with menus still open gets the
// reseller's rules on them: tier prices, no discount codes.
func TestCustomerBecomesResellerWithMenusOpen(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	owner.text("/discount_add SPRING20 20% 5 30")
	owner.sees("Saved")
	u := w.customer(1901, "soon")
	u.press("buy")
	u.press("plan:")
	u.shownHas("Price: <b>150,000 Toman</b>")
	u.press("dc:")
	u.shownHas("Send your discount code")
	w.makeReseller(1901, 20, 10, 500_000)

	u.text("SPRING20") // the question was open: no code, the tier price
	u.sees("Discount codes are not for resellers")
	u.shownHas("Price: <b>120,000 Toman</b>")
	if hasCallback(u.shown(), "dc:") {
		t.Fatalf("a discount button for a reseller: %v", callbacks(u.shown()))
	}

	// An old customer's menu with its discount button.
	u.text("/menu")
	u.press("buy")
	w.role(1901, "user")
	u.press("plan:")
	u.shownHas("Price: <b>150,000 Toman</b>")
	if err := w.stack.Store.SetUserRole(context.Background(), w.stack.Store.Conn(), w.storeUser(1901).ID, "reseller", "active"); err != nil {
		t.Fatal(err)
	}
	u.press("dc:")
	u.toast("Discount codes are not for resellers")
	u.shownHas("Price: <b>120,000 Toman</b>")
	u.press("pay:w:")
	u.shownHas("Paid from your wallet")
	if all, paid := w.orders(1901); all != 1 || paid != 1 {
		t.Fatalf("orders: %d, paid %d", all, paid)
	}

	// A code applied as a customer: paying as a reseller drops it.
	w.role(1901, "user")
	w.fund(1901, 500_000)
	u.text("/menu")
	u.press("buy")
	u.press("plan:")
	u.press("dc:")
	u.text("SPRING20")
	u.shownHas("Code <b>SPRING20</b>: 30,000 Toman off")
	if err := w.stack.Store.SetUserRole(context.Background(), w.stack.Store.Conn(), w.storeUser(1901).ID, "reseller", "active"); err != nil {
		t.Fatal(err)
	}
	u.press("pay:w:")
	u.shownHas("Paid from your wallet")
	var amount int64
	var code *string
	if err := w.stack.Store.DB().QueryRow(context.Background(), `SELECT amount, discount_code FROM core.orders
		WHERE user_id = $1 ORDER BY created_at DESC LIMIT 1`, w.storeUser(1901).ID).Scan(&amount, &code); err != nil {
		t.Fatal(err)
	}
	if amount != 120_000 || code != nil {
		t.Fatalf("order paid as a reseller: %d with code %v", amount, code)
	}
}

func TestResellerInputHelpers(t *testing.T) {
	for in, want := range map[string]string{
		"  Ali's \n phone ":           "Ali's phone",
		"گوشی\u200cعلی":               "گوشی\u200cعلی", // ZWNJ stays (Persian)
		strings.Repeat("ب", maxLabel): strings.Repeat("ب", maxLabel),
	} {
		if got, ok := cleanLabel(in); !ok || got != want {
			t.Errorf("cleanLabel(%q) = %q %v", in, got, ok)
		}
	}
	for _, bad := range []string{"", "  \n ", strings.Repeat("x", maxLabel+1), "a\x00b", "a\x07b"} {
		if got, ok := cleanLabel(bad); ok {
			t.Errorf("cleanLabel(%q) accepted: %q", bad, got)
		}
	}
	if got := searchText(" a\x00b \n c "); got != "ab c" {
		t.Errorf("searchText: %q", got)
	}
	if got := searchText(strings.Repeat("ج", 30)); got != strings.Repeat("ج", maxSearch/2) || len(got) > maxSearch {
		t.Errorf("long search: %q", got)
	}
	for in, want := range map[string]int32{"3": 3, "": 1, "0": 1, "-2": 1, "x": 1, "100000": 100_000, "100001": 1, "99999999999": 1} {
		if got := pageArg(in); got != want {
			t.Errorf("pageArg(%q) = %d, want %d", in, got, want)
		}
	}
	if got := discountKey(status.Error(codes.FailedPrecondition, "discount: reseller")); got != "discount.bad.reseller" {
		t.Errorf("reseller discount refusal: %s", got)
	}
	c := i18n.MustLoad()
	for _, lang := range i18n.Languages {
		for _, k := range []string{"discount.bad.reseller", "rs.no_credit", "wallet.debt", "sub.label_line"} {
			if c.T(lang, k) == k {
				t.Errorf("%s: %s missing", lang, k)
			}
		}
	}
}

// A service still being prepared can be named already.
func TestResellerRenamesPendingService(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	rs := w.customer(1621, "rsp")
	w.makeReseller(1621, 0, 10, 0)
	ctx := context.Background()
	st := w.stack.Store
	plans, err := st.ListPlans(ctx, st.Conn(), false)
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(plans, func(p corestore.Plan) bool { return !p.IsTrial && !p.IsTopup })
	o, _, err := st.CreateOrder(ctx, st.Conn(), &corestore.Order{UserID: w.storeUser(1621).ID, PlanID: plans[i].ID, Type: "new",
		Status: "provisioning", Amount: plans[i].Price, Currency: plans[i].Currency, IdempotencyKey: "pending-" + buuid.MustV7().String()})
	if err != nil {
		t.Fatal(err)
	}
	sub, err := st.CreateSubscription(ctx, st.Conn(), &corestore.Subscription{UserID: o.UserID, OrderID: o.ID,
		ClientEmail: "pending-" + o.ID, Status: "pending"})
	if err != nil {
		t.Fatal(err)
	}

	rs.text("/menu")
	rs.press("rs")
	rs.press("rs:subs:1")
	rs.press("sub:" + sub.ID)
	rs.shownHas("being prepared")
	rs.press("rs:rn:")
	rs.text("Reza phone")
	rs.shownHas("🏷 <b>Reza phone</b>")
	if l := w.subscription(sub.ID).Label; l == nil || *l != "Reza phone" {
		t.Fatalf("renamed: %v", l)
	}
}
