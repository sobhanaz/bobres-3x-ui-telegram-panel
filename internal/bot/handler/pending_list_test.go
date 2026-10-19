package handler

import (
	"fmt"
	"strings"
	"testing"
)

// cardPayment makes a customer pay by card with a receipt photo, which puts a
// payment in the review queue and alerts the owners.
func cardPayment(t *testing.T, w *world, id int64, username string) *person {
	t.Helper()
	u := w.person(id, username)
	u.text("/start")
	u.press("lang:en")
	u.press("buy")
	u.press("plan:")
	u.press("pay:c:")
	u.photo("receipt-"+username, "REF-"+username)
	u.sees("Received")
	return u
}

// alertButtons are the Review buttons of the payment alerts a person got, in
// the order they arrived.
func alertButtons(p *person) []string {
	var out []string
	for _, m := range p.w.tg.all(p.id) {
		if strings.Contains(m.text, "New payment to review") {
			for _, d := range callbacks(m) {
				if strings.HasPrefix(d, "adm:pi:") {
					out = append(out, d)
				}
			}
		}
	}
	return out
}

// listItems are the payments of the newest list message a person was sent
// (the buttons that open a card), in order.
func listItems(p *person) []string {
	var items []string
	for _, d := range callbacks(p.last()) {
		if strings.HasPrefix(d, "adm:pi:") {
			items = append(items, d)
		}
	}
	return items
}

// A server with two owners: both get every payment alert, both can open the
// list of what waits, an alert opens its own payment (not the oldest), and
// when one owner decides a payment the other sees who did and that it is gone
// from their list.
func TestEveryOwnerSeesThePaymentsAndTheFirstDecisionCounts(t *testing.T) {
	const partnerID = 9001
	w := newWorldOwners(t, ownerID, partnerID)
	owner := setupStore(t, w)
	partner := w.person(partnerID, "partner")
	partner.text("/start")
	partner.press("lang:en")
	partner.sees("Main menu")

	cardPayment(t, w, 301, "first")
	cardPayment(t, w, 302, "second")

	for _, o := range []*person{owner, partner} {
		if got := alertButtons(o); len(got) != 2 || got[0] == got[1] {
			t.Fatalf("owner %d: alerts %v, want one for each of the two payments", o.id, got)
		}
	}

	// The partner's list: both payments, oldest first, with who paid and how much.
	partner.text("/admin")
	partner.sees("Review payments (2)")
	partner.press("adm:pend")
	partner.lastHas("Payments waiting for review: 2")
	list := listItems(partner)
	labels := partner.last().labels()
	if len(list) != 2 || !strings.Contains(labels[0], "@first") || !strings.Contains(labels[1], "@second") ||
		!strings.Contains(labels[0], "150,000 Toman") {
		t.Fatalf("list %v labels %v", list, labels)
	}
	if list[0] != alertButtons(partner)[0] || list[1] != alertButtons(partner)[1] {
		t.Fatalf("the list and the alerts name different payments: %v vs %v", list, alertButtons(partner))
	}

	// An alert opens its own payment, not the oldest one: the second alert
	// shows "second" with its receipt.
	owner.tap(alertButtons(owner)[1])
	if m := owner.last(); !m.photo || !strings.Contains(m.text, "@second") || !strings.Contains(m.text, "REF-second") {
		t.Fatalf("the second alert opened %+v", m)
	}

	// The partner approves "first" from the list.
	partner.tap(list[0])
	if m := partner.last(); !m.photo || !strings.Contains(m.text, "@first") {
		t.Fatalf("card: %+v", m)
	}
	partner.press("adm:ok:")
	partner.toast("Approved")
	partner.lastHas("Payments waiting for review: 1") // the list again, without the decided one

	// The main owner taps the old alert for it: it was decided already, by whom
	// and when, and offers no approve or reject button.
	owner.tap(alertButtons(owner)[0])
	card := owner.last()
	if !strings.Contains(card.text, "Already approved") || !strings.Contains(card.text, "@partner") ||
		hasCallback(card, "adm:ok:") || hasCallback(card, "adm:no:") || !hasCallback(card, "adm:pend") {
		t.Fatalf("a payment another owner approved: %q %v", card.text, callbacks(card))
	}
	// Their list now holds only the second payment, and deciding it empties it.
	owner.text("/admin")
	owner.press("adm:pend")
	if items := listItems(owner); len(items) != 1 || items[0] != alertButtons(owner)[1] {
		t.Fatalf("the main owner's list: %v", items)
	}
	owner.press("adm:pi:")
	owner.press("adm:no:")
	owner.press("adm:nr:1:") // unreadable
	owner.toast("Rejected")
	owner.sees("No payments are waiting")

	// The customers were told, each their own outcome.
	w.eventually("both customers heard back", func() bool {
		return w.person(301, "first").count("Payment confirmed") == 1 && w.person(302, "second").count("receipt is not readable") == 1
	})
	// And the partner, tapping the second alert, sees the rejection and its reason.
	partner.tap(alertButtons(partner)[1])
	card = partner.last()
	if !strings.Contains(card.text, "Already rejected") || !strings.Contains(card.text, "@boss") || !strings.Contains(card.text, "not readable") {
		t.Fatalf("a payment the main owner rejected: %q", card.text)
	}
}

// The list pages by eight, oldest first, and moves in place.
func TestPendingListPages(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	for i := 0; i < 10; i++ {
		cardPayment(t, w, int64(400+i), fmt.Sprintf("payer%02d", i))
	}
	owner.text("/admin")
	owner.sees("Review payments (10)")
	owner.press("adm:pend")
	owner.lastHas("Payments waiting for review: 10")
	owner.lastHas("page 1 of 2")
	first := owner.last()
	items := listItems(owner)
	if len(items) != 8 || !strings.Contains(first.labels()[0], "@payer00") || !strings.Contains(first.labels()[7], "@payer07") ||
		!hasCallback(first, "adm:pend:1") || hasCallback(first, "adm:pend:0") {
		t.Fatalf("page 1: %d items, labels %v, callbacks %v", len(items), first.labels(), callbacks(first))
	}
	before := len(w.tg.all(owner.id))
	owner.press("adm:pend:1")
	if len(w.tg.all(owner.id)) != before {
		t.Fatal("paging sent a new message instead of editing the list")
	}
	page := owner.shown()
	if !strings.Contains(page.text, "page 2 of 2") || len(page.labels()) < 2 || !strings.Contains(page.labels()[0], "@payer08") ||
		!strings.Contains(page.labels()[1], "@payer09") || !hasCallback(page, "adm:pend:0") || hasCallback(page, "adm:pend:2") {
		t.Fatalf("page 2: %q %v", page.text, callbacks(page))
	}
	// A page number out of range lands on the last page rather than failing.
	owner.tap("adm:pend:9")
	if !strings.Contains(owner.shown().text, "page 2 of 2") {
		t.Fatalf("out-of-range page: %q", owner.shown().text)
	}
	owner.tap("adm:pend:x")
	if !strings.Contains(owner.shown().text, "page 1 of 2") {
		t.Fatalf("junk page: %q", owner.shown().text)
	}
}

// A customer cannot open a payment's card with a button they were never
// given: core refuses, and nothing of the payment is shown.
func TestPaymentCardIsForStaffOnly(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	u := cardPayment(t, w, 501, "payer")
	id := strings.TrimPrefix(alertButtons(owner)[0], "adm:pi:")

	u.tap("adm:pi:" + id)
	u.toast("This is for admins only.")
	u.tap("adm:pend")
	if strings.Contains(u.last().text, "payer") && strings.Contains(u.last().text, "150,000") && !strings.Contains(u.last().text, "Received") {
		t.Fatalf("a customer saw a payment: %q", u.last().text)
	}
	// A made-up id is "not found" for staff, with a way back to the list.
	owner.tap("adm:pi:00000000-0000-0000-0000-000000000000")
	owner.lastHas("That payment was not found.")
	if !hasCallback(owner.last(), "adm:pend") {
		t.Fatalf("no way back: %v", callbacks(owner.last()))
	}
}
