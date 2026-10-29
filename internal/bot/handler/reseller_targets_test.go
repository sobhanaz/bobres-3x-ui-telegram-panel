package handler

import (
	"context"
	"strings"
	"testing"
)

// The reseller's panel shows the month against the owner's targets while they are on, a sale that reaches a tier brings the bonus and a message,
// and the panel ticks the tier; a customer's home has none of this.
func TestResellerSeesTheMonthlyTargetAndIsToldTheBonus(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	rs := w.customer(1851, "rsl")
	w.makeReseller(1851, 20, 10, 1_000_000)
	ctx := context.Background()
	set := func(values map[string]string) {
		t.Helper()
		if _, err := w.dom().SetSettings(ctx, w.storeUser(ownerID), values); err != nil {
			t.Fatal(err)
		}
	}

	// the panel, opened from the home menu as a reseller does
	panel := func() {
		t.Helper()
		rs.text("/menu")
		rs.press("rs")
	}
	// Off by default: the panel is as it was.
	panel()
	if strings.Contains(rs.shown().text, "Monthly target") {
		t.Fatalf("a target while they are off: %q", rs.shown().text)
	}

	set(map[string]string{"targets.enabled": "true", "targets.tiers": `[{"threshold":3,"bonus":25000},{"threshold":2,"bonus":10000}]`})
	panel()
	for _, want := range []string{"🎯 <b>Monthly target</b> · ", "Services sold this month: <b>0</b>", "⬜ 2 sold: bonus 10,000 Toman", "⬜ 3 sold: bonus 25,000 Toman",
		"Next bonus: <b>10,000 Toman</b> · still to sell: <b>2</b>", "⏳ Days left in this month: "} {
		rs.shownHas(want)
	}
	// the tiers are in order, whatever order the owner wrote them in
	if text := rs.shown().text; strings.Index(text, "2 sold") > strings.Index(text, "3 sold") {
		t.Fatalf("the tiers are out of order:\n%s", text)
	}

	delivered := func() int {
		n := 0
		for _, m := range w.tg.all(rs.id) {
			if strings.Contains(m.text, "Your service is ready") {
				n++
			}
		}
		return n
	}
	buy := func(want int) {
		t.Helper()
		rs.press("rs:buy")
		rs.press("rs:p:")
		rs.press("rs:sk:")
		rs.press("pay:w:")
		w.eventually("a service delivered", func() bool { return delivered() >= want })
	}
	buy(1)
	panel()
	rs.shownHas("Services sold this month: <b>1</b>")
	rs.shownHas("still to sell: <b>1</b>")
	for _, m := range w.tg.all(rs.id) {
		if strings.Contains(m.text, "Monthly target reached") {
			t.Fatal("told of a bonus before a tier was reached")
		}
	}

	buy(2) // the first tier
	rs.eventuallySees("🎯 <b>Monthly target reached!</b>\nSold this month: 2 · tier: 2\nThe bonus, <b>10,000 Toman</b>, was added to your wallet.\nBalance: debt 230,000 Toman")
	panel()
	rs.shownHas("Services sold this month: <b>2</b>")
	rs.shownHas("✅ 2 sold: bonus 10,000 Toman")
	rs.shownHas("⬜ 3 sold: bonus 25,000 Toman")
	rs.shownHas("Next bonus: <b>25,000 Toman</b> · still to sell: <b>1</b>")

	buy(3) // the last tier
	rs.eventuallySees("Sold this month: 3 · tier: 3\nThe bonus, <b>25,000 Toman</b>, was added to your wallet.")
	panel()
	rs.shownHas("✅ 3 sold: bonus 25,000 Toman")
	rs.shownHas("You have reached every tier this month. 🎉")
	rs.shownHas("Balance: <b>debt 325,000 Toman</b>") // 3 x 120,000 on credit, 35,000 of bonus

	// Switched off again: the panel loses the block, and what was paid stays.
	set(map[string]string{"targets.enabled": "false"})
	panel()
	if strings.Contains(rs.shown().text, "Monthly target") {
		t.Fatalf("a target after switching them off: %q", rs.shown().text)
	}
	rs.shownHas("Balance: <b>debt 325,000 Toman</b>")
}

// The same in Persian, with Persian digits and the dates of the month in the Persian calendar.
func TestResellerSeesTheMonthlyTargetInPersian(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	rs := w.person(1861, "rsl")
	rs.text("/start")
	rs.press("lang:fa")
	w.makeReseller(1861, 0, 10, 500_000)
	if _, err := w.dom().SetSettings(context.Background(), w.storeUser(ownerID), map[string]string{
		"targets.enabled": "true", "targets.tiers": `[{"threshold":10,"bonus":50000}]`, "targets.calendar": "jalali"}); err != nil {
		t.Fatal(err)
	}
	rs.text("/menu")
	rs.press("rs")
	for _, want := range []string{"🎯 <b>هدف ماهانه</b> · ", " تا ", "سرویس فروخته\u200cشده در این ماه: <b>۰</b>", "⬜ ۱۰ فروش: پاداش ۵۰٬۰۰۰ تومان",
		"پاداش بعدی: <b>۵۰٬۰۰۰ تومان</b> · فروش باقی\u200cمانده: <b>۱۰</b>", "⏳ روزهای باقی\u200cماندهٔ این ماه: "} {
		rs.shownHas(want)
	}
}
