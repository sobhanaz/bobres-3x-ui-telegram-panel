package handler

import (
	"context"
	"slices"
	"strings"
	"testing"
)

func TestWheelChanceIsWrittenInTheLanguage(t *testing.T) {
	for _, c := range []struct {
		lang          string
		weight, total int64
		want          string
	}{
		{"en", 3, 8, "37.5%"}, {"en", 1, 8, "12.5%"}, {"en", 4, 8, "50%"}, {"en", 1, 3, "33.33%"}, {"en", 2, 3, "66.67%"}, {"en", 1, 1, "100%"},
		{"en", 1, 10000, "0.01%"}, {"en", 1, 100000, "0%"}, {"en", 0, 8, "0%"}, {"en", 1, 0, "0%"},
		{"fa", 3, 8, "۳۷٫۵٪"}, {"fa", 4, 8, "۵۰٪"}, {"fa", 1, 3, "۳۳٫۳۳٪"},
	} {
		if got := wheelChance(c.lang, c.weight, c.total); got != c.want {
			t.Errorf("wheelChance(%s, %d, %d) = %q, want %q", c.lang, c.weight, c.total, got, c.want)
		}
	}
}

// wheelWorld is a store whose owner is registered and that has the wheel switched on: one prize of 2,500 Toman that every spin wins, and a budget
// of 5,000 a day (two winners).
func wheelWorld(t *testing.T) *world {
	t.Helper()
	w := newWorld(t)
	setupStore(t, w)
	return w
}

func (w *world) wheelSettings(values map[string]string) {
	w.t.Helper()
	if _, err := w.dom().SetSettings(context.Background(), w.storeUser(ownerID), values); err != nil {
		w.t.Fatal(err)
	}
	w.reloadSettings()
}

// hasAction reports whether the newest message with buttons offers the action.
func (p *person) hasAction(action string) bool {
	return slices.Contains(actionsOf(p.lastKeyboard()), action)
}

// The wheel in the bot: no button until the owner switches it on with prizes and a budget; then a customer is told what they need, sees what they can
// win, spins once, gets the same answer for a double tap, and the day's budget runs out for the next.
func TestWheelInTheBot(t *testing.T) {
	w := wheelWorld(t)
	c := w.customer(2901, "spinner")
	c.text("/menu")
	if c.hasAction("wh") {
		t.Fatal("a wheel button while the wheel is off")
	}
	if strings.Contains(c.allText(), "Daily wheel") {
		t.Fatal("the wheel is named while it is off")
	}

	// On, with prizes and a budget: the button is on the home screen and in the bottom menu.
	w.wheelSettings(map[string]string{
		"wheel.enabled": "true", "wheel.prizes": `[{"amount":2500,"weight":1}]`, "wheel.no_prize_weight": "0", "wheel.daily_budget": "5000",
	})
	c.text("/menu")
	if !c.hasAction("wh") {
		t.Fatalf("no wheel button on the home screen: %v", actionsOf(c.lastKeyboard()))
	}
	c.hasMenuLabel("en", "btn.wheel")
	c.tapMenu("en", "btn.wheel") // the bottom menu's item opens the same screen
	c.shownHas("Paid orders needed to spin: 1. Yours so far: 0.")

	// Without a paid order: what it takes, and nothing written.
	c.press("wh")
	c.shownHas("🎡 <b>Daily wheel</b>")
	c.shownHas("Paid orders needed to spin: 1. Yours so far: 0.")
	if c.hasAction("wh") {
		t.Fatal("a Spin button for a customer who has no paid order")
	}
	c.tap("wh:spin") // a crafted button: core says the same
	c.shownHas("Paid orders needed to spin: 1. Yours so far: 0.")
	if w.balanceOf(2901) != 0 {
		t.Fatal("a prize without an order")
	}

	// With one: the wheel and its chances, then the spin.
	w.seedService(2901, "")
	c.text("/menu")
	c.press("wh")
	c.shownHas("One free spin a day. What you can win:")
	c.shownHas("• 2,500 Toman: 100%")
	c.lastButton("Spin the wheel")
	c.press("wh:spin")
	c.shownHas("🎉 <b>You won 2,500 Toman!</b>")
	c.shownHas("Balance: 2,500 Toman")
	if w.balanceOf(2901) != 2500 {
		t.Fatalf("balance %d", w.balanceOf(2901))
	}
	// A double tap, and the wheel opened again: the same spin, paid once.
	c.tap("wh:spin")
	c.shownHas("You have spun today. You won <b>2,500 Toman</b>.")
	c.text("/menu")
	c.press("wh")
	c.shownHas("You have spun today. You won <b>2,500 Toman</b>.")
	if w.balanceOf(2901) != 2500 {
		t.Fatalf("balance after asking again: %d", w.balanceOf(2901))
	}

	// The budget (5,000) is for two winners; the third is sent away without using the spin.
	b := w.customer(2902, "second")
	w.seedService(2902, "")
	b.text("/menu")
	b.press("wh")
	b.press("wh:spin")
	b.shownHas("You won 2,500 Toman!")
	d := w.customer(2903, "third")
	w.seedService(2903, "")
	d.text("/menu")
	d.press("wh")
	d.shownHas("Today's prizes have all been given out. Your spin was not used: come back tomorrow.")
	if d.hasAction("wh") || w.balanceOf(2903) != 0 {
		t.Fatal("the third customer got a spin or a prize")
	}
	d.tap("wh:spin")
	d.shownHas("Your spin was not used")
	var spins, prizes int
	if err := w.stack.Store.DB().QueryRow(context.Background(), `SELECT (SELECT count(*) FROM core.wheel_spins), (SELECT count(*) FROM core.ledger_entries WHERE kind = 'wheel_prize')`).Scan(&spins, &prizes); err != nil {
		t.Fatal(err)
	}
	if spins != 2 || prizes != 2 {
		t.Fatalf("%d spins, %d prizes", spins, prizes)
	}

	// Switched off again: a stale button gets a popup and the home screen without the wheel; what was won stays.
	w.wheelSettings(map[string]string{"wheel.enabled": "false"})
	c.tap("wh")
	c.toast("The wheel is not available right now.")
	if c.hasAction("wh") {
		t.Fatal("the home screen still offers the wheel")
	}
	if w.balanceOf(2901) != 2500 {
		t.Fatalf("balance after switching it off: %d", w.balanceOf(2901))
	}
}

// Staff, resellers and banned customers are not offered the wheel, and a tap they make anyway is refused by core.
func TestWheelIsForCustomers(t *testing.T) {
	w := wheelWorld(t)
	w.wheelSettings(map[string]string{
		"wheel.enabled": "true", "wheel.prizes": `[{"amount":2500,"weight":1}]`, "wheel.no_prize_weight": "0", "wheel.daily_budget": "50000",
	})
	owner := w.person(ownerID, "owner")
	owner.text("/menu")
	if owner.hasAction("wh") {
		t.Fatal("the owner is offered the wheel")
	}
	owner.tap("wh")
	owner.toast("The wheel is not available right now.")

	rs := w.customer(2911, "shop")
	w.makeReseller(2911, 20, 10, 1_000_000)
	w.seedService(2911, "")
	rs.text("/menu")
	if rs.hasAction("wh") {
		t.Fatal("a reseller is offered the wheel")
	}
	rs.tap("wh:spin")
	rs.toast("The wheel is not available right now.")

	// A customer who was banned after the settings were read: core refuses, the bot says nothing about why.
	b := w.customer(2912, "banned")
	w.seedService(2912, "")
	b.text("/menu")
	w.setUserStatus(2912, "banned")
	b.tap("wh:spin")
	if w.balanceOf(2912) != 0 {
		t.Fatal("a banned customer won a prize")
	}
	var spins int
	if err := w.stack.Store.DB().QueryRow(context.Background(), `SELECT count(*) FROM core.wheel_spins`).Scan(&spins); err != nil || spins != 0 {
		t.Fatalf("spins: %d %v", spins, err)
	}
}

// The same in Persian: the texts, the digits and the decimal mark of the chances.
func TestWheelInPersian(t *testing.T) {
	w := wheelWorld(t)
	w.wheelSettings(map[string]string{
		"wheel.enabled": "true", "wheel.prizes": `[{"amount":1000,"weight":3},{"amount":5000,"weight":1}]`, "wheel.no_prize_weight": "4", "wheel.daily_budget": "50000",
	})
	p := w.persian(2921, "farsi")
	w.seedService(2921, "")
	p.text("/menu")
	if !p.hasAction("wh") {
		t.Fatalf("no wheel button: %v", actionsOf(p.lastKeyboard()))
	}
	p.hasMenuLabel("fa", "btn.wheel")
	p.press("wh")
	p.shownHas("🎡 <b>گردونه شانس روزانه</b>")
	p.shownHas("روزی یک چرخش رایگان. جایزه\u200cهای ممکن:")
	p.shownHas("• ۱٬۰۰۰ تومان: ۳۷٫۵٪")
	p.shownHas("• ۵٬۰۰۰ تومان: ۱۲٫۵٪")
	p.shownHas("• بدون جایزه: ۵۰٪")
	p.lastButton("بچرخان")
	p.press("wh:spin")
	// Which segment it stops on is the draw's; whatever it is, it is said in Persian and the day's spin is used.
	if text := p.shown().text; !strings.Contains(text, "برنده شدید") && !strings.Contains(text, "جایزه\u200cای نیامد") {
		t.Fatalf("the result: %q", text)
	}
	p.text("/menu")
	p.press("wh")
	p.shownHas("امروز چرخانده\u200cاید.")
}
