package handler

import (
	"context"
	"strings"
	"testing"
)

// waitForFirstUse puts a service in the state a purchase made with days from the first connection on leaves it in: no
// end date yet, and the days it runs once it connects.
func (w *world) waitForFirstUse(id string, days int) {
	w.t.Helper()
	if _, err := w.stack.Store.DB().Exec(context.Background(),
		`UPDATE core.subscriptions SET expires_at = NULL, first_use_days = $2 WHERE id = $1`, id, days); err != nil {
		w.t.Fatal(err)
	}
}

// Days from the first connection, as a customer meets them: the owner finds the switches under their own heading and
// turns the customers' one on; a service bought then waits for its first connection, and every place that shows its
// end says so instead of "never" (the delivery, My services, the service's page); the renewal screens say the days
// are added to the wait, the renewal message shows the longer wait, and a traffic package keeps it. Someone who finds
// the service by its link sees the same, and a Persian customer reads it in Persian digits.
func TestServiceWaitingForItsFirstConnection(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	owner.text("/topup_add 30000 IRT 10 +10 GB | ۱۰ گیگ اضافه")
	owner.sees("Plan saved: +10 GB")

	// The switches, by the bot's own names.
	owner.text("/admin")
	owner.press("adm:set")
	owner.lastButton("Days from the first connection")
	owner.press("adm:sg:first_use")
	owner.lastButton("⛔ Customers' days from first connection")
	owner.lastButton("⛔ Resellers' days from first connection")
	owner.press("adm:st:first_use.customers")
	owner.lastButton("✅ Customers' days from first connection")
	owner.lastButton("⛔ Resellers' days from first connection")

	const wait = "30 days from the first connection"
	u := w.customer(761, "waiter")
	w.fund(761, 1_000_000)
	u.press("buy")
	u.press("plan:")
	u.press("pay:w:")
	u.eventuallySees("Your service is ready")
	if ready := u.lastWith("Your service is ready"); !strings.Contains(ready.text, "\n\nExpires: "+wait+"\nTraffic: 50 GB") {
		t.Fatalf("the delivery: %q", ready.text)
	}
	sub := w.onlySub(761)
	if sub.ExpiresAt != nil || sub.FirstUseDays == nil || *sub.FirstUseDays != 30 {
		t.Fatalf("the service does not wait: expires %v, days %v", sub.ExpiresAt, sub.FirstUseDays)
	}

	// My services and the service's page.
	u.press("home")
	u.press("subs")
	if got := serviceLabels(u.shown()); len(got) != 1 || !strings.HasSuffix(got[0], " · "+wait) {
		t.Fatalf("My services: %v", got)
	}
	u.press("sub:" + sub.ID)
	u.shownHas("\nExpires: " + wait + "\n")

	// Renewing it: the screens say the days are added to the wait, not started again from today.
	u.press("rnw:")
	u.shownHas("This service has not connected yet")
	if strings.Contains(u.shown().text, "the days left are dropped") {
		t.Fatalf("the renewal plans: %q", u.shown().text)
	}
	u.press("rp:")
	u.shownHas("This service has not connected yet. This plan's days are added")
	u.press("pay:w:")
	u.sees("Paid from your wallet")
	u.eventuallySees("renewed")
	if msg := u.lastWith("renewed"); !strings.Contains(msg.text, "Expires: 60 days from the first connection") {
		t.Fatalf("the renewal message: %q", msg.text)
	}
	if got := w.onlySub(761); got.ExpiresAt != nil || got.FirstUseDays == nil || *got.FirstUseDays != 60 {
		t.Fatalf("after the renewal: expires %v, days %v", got.ExpiresAt, got.FirstUseDays)
	}

	// A traffic package keeps the wait.
	u.press("sub:") // View service, under the renewal message
	u.shownHas("\nExpires: 60 days from the first connection\n")
	u.press("tup:")
	u.press("rp:")
	u.press("pay:w:")
	u.eventuallySees("Traffic added to service")
	if msg := u.lastWith("Traffic added to service"); !strings.Contains(msg.text, "Expires: 60 days from the first connection") {
		t.Fatalf("the traffic package message: %q", msg.text)
	}

	// Found by its link: the same state, and the renewal says the days are added to the wait.
	holder := w.customer(762, "holder")
	w.fund(762, 1_000_000)
	holder.press("subs")
	holder.press("fl")
	holder.text(w.onlySub(761).SubLink)
	holder.shownHas("Service found")
	holder.shownHas("\nExpires: 60 days from the first connection\n")
	holder.press("fr:")
	holder.shownHas("This service has not connected yet")
	holder.pressLabel("Monthly 50GB")
	holder.shownHas("This service has not connected yet. This plan's days are added")

	// In Persian.
	fa := w.persian(763, "farsi")
	w.fund(763, 1_000_000)
	fa.press("buy")
	fa.press("plan:")
	fa.press("pay:w:")
	fa.eventuallySees("سرویس شما آماده است")
	if ready := fa.lastWith("سرویس شما آماده است"); !strings.Contains(ready.text, "تاریخ انقضا: ۳۰ روز از اولین اتصال") {
		t.Fatalf("the delivery in Persian: %q", ready.text)
	}
	fa.press("home")
	fa.press("subs")
	if got := serviceLabels(fa.shown()); len(got) != 1 || !strings.HasSuffix(got[0], " · ۳۰ روز از اولین اتصال") {
		t.Fatalf("My services in Persian: %v", got)
	}
}

// A reseller's lists and pages show a service waiting for its first connection the same way, beside one that does
// not wait (its date), in the reseller's language.
func TestResellerSeesAServiceWaitingForItsFirstConnection(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	rs := w.persian(2301, "rsl")
	w.makeReseller(2301, 0, 0, 0)
	waits := w.seedService(2301, "Ali")
	runs := w.seedService(2301, "Reza")
	w.waitForFirstUse(waits, 45)

	rs.text("/menu")
	rs.press("subs")
	var ali, reza string
	for _, l := range serviceLabels(rs.shown()) {
		switch {
		case strings.HasPrefix(l, "Ali · "):
			ali = l
		case strings.HasPrefix(l, "Reza · "):
			reza = l
		}
	}
	if !strings.HasSuffix(ali, " · ۴۵ روز از اولین اتصال") || reza == "" || strings.Contains(reza, "اتصال") || strings.Contains(reza, "نامحدود") {
		t.Fatalf("the reseller's services: %v", serviceLabels(rs.shown()))
	}
	rs.press("sub:" + waits)
	rs.shownHas("\nتاریخ انقضا: ۴۵ روز از اولین اتصال\n")
	rs.press("rnw:")
	rs.shownHas("این سرویس هنوز وصل نشده است")
	rs.text("/menu")
	rs.press("subs")
	rs.press("sub:" + runs)
	if strings.Contains(rs.shown().text, "اولین اتصال") {
		t.Fatalf("a service that does not wait: %q", rs.shown().text)
	}
	rs.press("rnw:")
	rs.shownHas("روزهای باقی\u200cمانده صفر می\u200cشود")
}
