package handler

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	corestore "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// A plan priced in USDT is paid from the Toman wallet at a quote: the customer is shown the USDT price, the rate and the Toman that will be taken, and
// confirms (walletquote.go). The tests drive the real core with a clock they move.

type quoteClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *quoteClock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *quoteClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// usdtPlan adds a plan priced in USDT (4 USDT), as the owner does in the dashboard.
func (w *world) usdtPlan(price int64) *corestore.Plan {
	w.t.Helper()
	days, traffic := int32(30), int64(50<<30)
	p := &corestore.Plan{NameI18n: map[string]string{"en": "Gold USDT", "fa": "طلایی تتری"}, Kind: "both", DurationDays: &days, TrafficBytes: &traffic,
		Price: price, Currency: "USDT", Enabled: true, Sort: -1}
	if err := w.stack.Store.UpsertPlan(context.Background(), w.stack.Store.Conn(), p); err != nil {
		w.t.Fatal(err)
	}
	return p
}

// usdtRate sets the store's own USDT rate (the live price off) and lets the bot learn it.
func (w *world) usdtRate(rate string) {
	w.t.Helper()
	w.setInCore("payments.usdt_rate_auto", "false")
	w.setInCore("payments.usdt_rate", rate)
	w.reloadSettings()
}

type usdtShop struct {
	w     *world
	owner *person
	plan  *corestore.Plan
	clock *quoteClock
}

// newUsdtShop is a store with a plan at 4 USDT and a rate of 300,000 Toman for a USDT: 1,200,000 Toman.
func newUsdtShop(t *testing.T) *usdtShop {
	t.Helper()
	w := newWorld(t)
	owner := setupStore(t, w)
	s := &usdtShop{w: w, owner: owner, plan: w.usdtPlan(4_000_000), clock: &quoteClock{t: time.Now()}}
	w.stack.Domain.SetClock(s.clock.now)
	w.usdtRate("300000")
	return s
}

func (s *usdtShop) customer(id int64, name string, balance int64) *person {
	u := s.w.customer(id, name)
	if balance > 0 {
		s.w.fund(id, balance)
	}
	return u
}

// order is the customer's only order of the USDT plan.
func (s *usdtShop) order(t *testing.T, tg int64) corestore.Order {
	t.Helper()
	var o corestore.Order
	st := s.w.stack.Store
	u := s.w.storeUser(tg)
	row := st.DB().QueryRow(context.Background(), `SELECT id FROM core.orders WHERE user_id = $1 AND plan_id = $2 ORDER BY created_at DESC LIMIT 1`, u.ID, s.plan.ID)
	var id string
	if err := row.Scan(&id); err != nil {
		t.Fatalf("the customer has no order of the plan: %v", err)
	}
	got, err := st.GetOrder(context.Background(), st.Conn(), id)
	if err != nil {
		t.Fatal(err)
	}
	o = *got
	return o
}

// openCheckout goes (from the home screen, or from any screen that has Home) to the payment menu of the USDT plan.
func (s *usdtShop) openCheckout(u *person) {
	if kb := u.last().kb; kb != nil {
		for _, row := range kb.InlineKeyboard {
			for _, b := range row {
				if b.CallbackData == "home" {
					u.press("home")
					break
				}
			}
		}
	}
	u.press("buy")
	u.press("plan:" + s.plan.ID)
}

// From the payment menu to the delivered service: the menu says about what the wallet would be charged, the wallet button shows the quote and charges
// nothing, the confirming button charges exactly the quote whatever the rate did meanwhile, a second tap charges nothing, the order keeps what it was
// listed at, and the service arrives.
func TestBotPaysAUsdtPlanFromTheWalletAtAQuote(t *testing.T) {
	s := newUsdtShop(t)
	u := s.customer(701, "ali", 2_000_000)

	s.openCheckout(u)
	u.sees("4 USDT")
	u.sees("≈ 1,200,000 Toman from your wallet at today's rate (300,000 Toman per USDT)")
	u.sees("Wallet (2,000,000 Toman)")
	if strings.Contains(strings.Join(u.last().labels(), "|"), "Card to card") {
		t.Fatalf("card to card for a price in USDT: %v", u.last().labels())
	}

	u.press("pay:w:")
	for _, sub := range []string{"Pay from your wallet", "Gold USDT", "Price: <b>4 USDT</b>", "Rate: 1 USDT = 300,000 Toman", "You pay: <b>1,200,000 Toman</b>",
		"Your wallet: 2,000,000 Toman", "held for 2 minutes", "Confirm and pay 1,200,000 Toman"} {
		u.sees(sub)
	}
	if bal := s.w.balanceOf(701); bal != 2_000_000 {
		t.Fatalf("the quote charged: the wallet holds %d", bal)
	}
	if o := s.order(t, 701); o.Status != "awaiting_payment" || o.Currency != "USDT" || o.Amount != 4_000_000 || o.ListAmount != nil {
		t.Fatalf("the order before the confirmation: %+v", o)
	}

	// The rate rises before the customer confirms: the price they saw is the price they pay.
	s.w.usdtRate("330000")
	confirm := callbackWithPrefix(u.last(), "pq:")
	u.tap(confirm)
	u.sees("<b>1,200,000 Toman</b> was taken from your wallet (4 USDT at 300,000 Toman per USDT)")
	if bal := s.w.balanceOf(701); bal != 800_000 {
		t.Fatalf("the wallet after the payment: %d", bal)
	}
	o := s.order(t, 701)
	if o.Currency != "IRT" || o.Amount != 1_200_000 || o.ListAmount == nil || *o.ListAmount != 4_000_000 || *o.ListCurrency != "USDT" || *o.Rate != 300_000 {
		t.Fatalf("the order after: %+v", o)
	}

	// A second tap on the same button (it reached the bot before the screen changed) charges nothing and says the same.
	u.tap(confirm)
	u.sees("<b>1,200,000 Toman</b> was taken")
	if bal := s.w.balanceOf(701); bal != 800_000 {
		t.Fatalf("a double tap charged again: the wallet holds %d", bal)
	}
	var purchases int
	if err := s.w.stack.Store.DB().QueryRow(context.Background(), `SELECT count(*) FROM core.ledger_entries WHERE kind = 'purchase' AND ref_id = $1`, o.ID).Scan(&purchases); err != nil || purchases != 1 {
		t.Fatalf("purchase entries: %d %v", purchases, err)
	}
	u.eventuallySees("Your service is ready")
	u.press("home")
	u.press("wallet")
	u.sees("800,000 Toman")
}

// A quote that ran out is not charged: the customer is shown a new quote at the new rate, says so, and confirms again.
func TestBotQuoteRunsOutAndIsShownAgain(t *testing.T) {
	s := newUsdtShop(t)
	u := s.customer(702, "sara", 2_000_000)
	s.openCheckout(u)
	u.press("pay:w:")
	u.sees("You pay: <b>1,200,000 Toman</b>")
	first := u.last().labels()

	s.w.usdtRate("330000")
	s.clock.advance(2*time.Minute + time.Second)
	u.press("pq:")
	for _, sub := range []string{"The price was updated", "Rate: 1 USDT = 330,000 Toman", "You pay: <b>1,320,000 Toman</b>", "Confirm and pay 1,320,000 Toman"} {
		if !strings.Contains(u.last().text, sub) && !strings.Contains(strings.Join(u.last().labels(), "\n"), sub) {
			t.Fatalf("the new quote lacks %q: %q %v", sub, u.last().text, u.last().labels())
		}
	}
	if strings.Join(u.last().labels(), "|") == strings.Join(first, "|") {
		t.Fatal("the confirming button still names the old price")
	}
	if bal := s.w.balanceOf(702); bal != 2_000_000 {
		t.Fatalf("the expired quote charged: %d", bal)
	}
	// Confirming the new one pays it.
	u.press("pq:")
	u.sees("<b>1,320,000 Toman</b> was taken from your wallet")
	if bal := s.w.balanceOf(702); bal != 680_000 {
		t.Fatalf("the wallet after the new quote: %d", bal)
	}
	if o := s.order(t, 702); o.Amount != 1_320_000 || *o.Rate != 330_000 {
		t.Fatalf("the order: %+v", o)
	}
}

// A wallet that cannot cover the quote is told what is missing and offered a top-up and the other ways; a wallet that was spent after the price was shown
// shows the shortfall instead of charging.
func TestBotUsdtPlanWithAShortWallet(t *testing.T) {
	s := newUsdtShop(t)
	chain := enableUSDT(t, s.w)
	u := s.customer(703, "reza", 1_000_000)
	s.openCheckout(u)
	u.press("pay:w:")
	u.sees("You pay: <b>1,200,000 Toman</b>")
	u.sees("Your wallet is short by <b>200,000 Toman</b>")
	labels := strings.Join(u.last().labels(), "|")
	if !strings.Contains(labels, "Top up wallet") || !strings.Contains(labels, "USDT TRC20 (automatic)") || strings.Contains(labels, "Confirm and pay") {
		t.Fatalf("buttons of a short wallet: %v", u.last().labels())
	}
	if bal := s.w.balanceOf(703); bal != 1_000_000 {
		t.Fatalf("the wallet: %d", bal)
	}

	// The customer tops up and comes back; the price is quoted again and paid.
	s.w.fund(703, 500_000)
	s.openCheckout(u)
	u.press("pay:w:")
	u.sees("Confirm and pay 1,200,000 Toman")
	// ...but spends the money elsewhere before confirming: the confirmation shows the shortfall and charges nothing.
	s.w.spend(703, 1_000_000)
	u.press("pq:")
	u.sees("Your wallet is short by <b>700,000 Toman</b>")
	if bal := s.w.balanceOf(703); bal != 500_000 {
		t.Fatalf("a payment the wallet could not cover charged: %d", bal)
	}
	if o := s.order(t, 703); o.Status != "awaiting_payment" || o.Currency != "USDT" || o.ListAmount != nil {
		t.Fatalf("the order: %+v", o)
	}
	// The other way out is the direct one: the same menu pays the plan in USDT on TRON, as before.
	u.press("pay:u:")
	amount, _ := u.usdtScreen()
	if amount < 4_000_000 || amount > 4_009_999 {
		t.Fatalf("exact amount %d", amount)
	}
	chain.Pay(usdtStore, amount)
	s.w.scan()
	u.eventuallySees("Payment confirmed")
	u.eventuallySees("Your service is ready")
	paid := s.order(t, 703)
	if paid.Status == "awaiting_payment" || paid.Currency != "USDT" || paid.Amount != 4_000_000 || paid.ListAmount != nil {
		t.Fatalf("the order paid by crypto: %+v", paid)
	}
	if bal := s.w.balanceOf(703); bal != 500_000 {
		t.Fatalf("the Toman wallet moved for a crypto payment: %d", bal)
	}
}

// With no USDT rate (the live price off and none typed) nothing is quoted or charged: the customer is told so, and the direct crypto payment stays.
func TestBotUsdtPlanWithoutARate(t *testing.T) {
	s := newUsdtShop(t)
	enableUSDT(t, s.w)
	s.w.usdtRate("")
	u := s.customer(704, "mina", 2_000_000)
	s.openCheckout(u)
	if strings.Contains(u.last().text, "from your wallet at today's rate") {
		t.Fatalf("an estimate without a rate: %q", u.last().text)
	}
	u.press("pay:w:")
	u.sees("The USDT price is not available right now")
	if strings.Contains(strings.Join(u.last().labels(), "|"), "Confirm and pay") {
		t.Fatal("a payment offered without a price")
	}
	if bal := s.w.balanceOf(704); bal != 2_000_000 {
		t.Fatalf("the wallet moved: %d", bal)
	}
	u.press("pay:u:")
	amount, _ := u.usdtScreen()
	if amount < 4_000_000 || amount > 4_009_999 {
		t.Fatalf("exact amount %d", amount)
	}
}

// The same flow in Persian: the texts, the digits and the Toman.
func TestBotQuoteInPersian(t *testing.T) {
	s := newUsdtShop(t)
	u := s.w.persian(705, "پریسا")
	s.w.fund(705, 2_000_000)
	u.press("buy")
	u.press("plan:" + s.plan.ID)
	u.sees("حدود ۱٬۲۰۰٬۰۰۰ تومان از کیف پول شما")
	u.press("pay:w:")
	for _, sub := range []string{"پرداخت از کیف پول", "قیمت: <b>۴ تتر</b>", "مبلغ کسر از کیف پول: <b>۱٬۲۰۰٬۰۰۰ تومان</b>", "۲ دقیقه", "تأیید و پرداخت ۱٬۲۰۰٬۰۰۰ تومان"} {
		u.sees(sub)
	}
	u.press("pq:")
	u.sees("مبلغ <b>۱٬۲۰۰٬۰۰۰ تومان</b> از کیف پول شما کسر شد")
	if bal := s.w.balanceOf(705); bal != 800_000 {
		t.Fatalf("the wallet: %d", bal)
	}
}

// A reseller pays on credit, down to the limit; the screen says what they can spend.
func TestBotUsdtPlanOnAResellersCredit(t *testing.T) {
	s := newUsdtShop(t)
	r := s.w.customer(706, "shop")
	s.w.makeReseller(706, 0, 10, 1_500_000)
	s.openCheckout(r)
	r.sees("available)") // the wallet button names what they can spend
	r.press("pay:w:")
	r.sees("Confirm and pay 1,200,000 Toman")
	r.sees("Available to spend: 1,500,000 Toman")
	r.press("pq:")
	r.sees("was taken from your wallet")
	if bal := s.w.balance(706); bal != -1_200_000 {
		t.Fatalf("on credit: %d", bal)
	}
	// 300,000 of credit is left: the next one is short, and the screen says how much.
	s.openCheckout(r)
	r.press("pay:w:")
	r.sees("Your wallet is short by <b>900,000 Toman</b>")
	if strings.Contains(strings.Join(r.last().labels(), "|"), "Confirm and pay") {
		t.Fatal("a payment past the credit limit offered")
	}
}

// A Toman plan is paid exactly as it was: no quote, no confirmation.
func TestBotTomanPlanIsUnchanged(t *testing.T) {
	s := newUsdtShop(t)
	u := s.customer(707, "omid", 500_000)
	u.press("buy")
	var tomanPlan string
	for _, row := range u.last().kb.InlineKeyboard {
		for _, b := range row {
			if id, ok := strings.CutPrefix(b.CallbackData, "plan:"); ok && id != s.plan.ID {
				tomanPlan = id
			}
		}
	}
	if tomanPlan == "" {
		t.Fatalf("no Toman plan in the list: %v", u.last().labels())
	}
	u.press("plan:" + tomanPlan)
	if strings.Contains(u.last().text, "from your wallet at today's rate") {
		t.Fatalf("a Toman plan with a USDT estimate: %q", u.last().text)
	}
	u.press("pay:w:")
	u.sees("Paid from your wallet")
	if strings.Contains(u.last().text, "was taken from your wallet") || s.w.balanceOf(707) != 350_000 {
		t.Fatalf("a Toman plan: %q, wallet %d", u.last().text, s.w.balanceOf(707))
	}
}
