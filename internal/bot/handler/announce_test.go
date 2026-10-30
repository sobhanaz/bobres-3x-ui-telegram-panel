package handler

import (
	"context"
	"strings"
	"testing"
)

// The question about product news: asked once, only while announcements are on: a newcomer right after their first /start, a customer who
// was there before at their next interaction. The answer can be changed on the language screen.
func TestProductNewsQuestion(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	before := w.customer(7301, "before")
	if before.count("News of new products") != 0 || owner.count("News of new products") != 0 {
		t.Fatal("asked while announcements are off")
	}
	before.press("lang")
	if hasCallback(before.shown(), "an:") {
		t.Fatalf("the switch while announcements are off: %v", callbacks(before.shown()))
	}

	owner.text("/set announce.enabled true")
	owner.sees("Saved")

	// A newcomer: the language, the welcome and the home screen, then the question.
	fresh := w.person(7302, "fresh")
	fresh.text("/start")
	fresh.press("lang:en")
	fresh.sees("Main menu")
	fresh.lastHas("News of new products")
	fresh.press("an:y")
	fresh.sees("we will message you when a new product arrives")
	fresh.text("/menu")
	fresh.press("buy")
	if n := fresh.count("News of new products") + fresh.count("we will message you"); n != 1 { // the question, answered in place
		t.Fatalf("the newcomer was asked %d times", n)
	}
	fresh.press("lang")
	fresh.lastButton("Product news: on")
	fresh.press("an:t:n")
	fresh.toast("Product news off")
	fresh.lastButton("Product news: off")

	// A customer from before: asked at their next interaction, once; no answer is no.
	before.text("/menu")
	before.lastHas("News of new products")
	before.press("buy")
	before.text("/menu")
	if n := before.count("News of new products"); n != 1 {
		t.Fatalf("the customer from before was asked %d times", n)
	}
	before.press("lang")
	before.lastButton("Product news: off")
	before.press("an:t:y")
	before.toast("Product news on")
	u, err := w.stack.Store.GetUserByTelegramID(context.Background(), w.stack.Store.Conn(), 7301)
	if err != nil || u.AnnounceOptIn == nil || !*u.AnnounceOptIn {
		t.Fatalf("the switch is stored: %+v %v", u, err)
	}
}

// The Buy button under an announcement opens that plan as a new message (the card stays); a channel post's start link does the same, for a
// newcomer once they have picked a language; a plan switched off since says it is not available.
func TestAnnouncementBuyButton(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	buyer := w.customer(7311, "buyer")
	buyer.press("buy")
	planID := strings.TrimPrefix(callbackWithPrefix(buyer.shown(), "plan:"), "plan:")
	hexID := strings.ReplaceAll(planID, "-", "")

	before := len(w.tg.all(buyer.id))
	buyer.tap("an:b:p:" + hexID)
	if len(w.tg.all(buyer.id)) != before+1 {
		t.Fatal("the plan did not come as a new message")
	}
	buyer.lastHas("Monthly 50GB")
	if !hasCallback(buyer.last(), "pay:") {
		t.Fatalf("no payment buttons: %v", callbacks(buyer.last()))
	}

	// The channel's start link: an existing customer, and a newcomer after the language.
	buyer.text("/start p_" + hexID)
	buyer.lastHas("Monthly 50GB")
	newcomer := w.person(7312, "")
	newcomer.text("/start p_" + hexID)
	newcomer.sees("Choose your language")
	newcomer.press("lang:en")
	newcomer.sees("Main menu")
	newcomer.sees("Monthly 50GB")

	// Switched off since: said so, nothing else changes.
	if _, err := w.stack.Store.DB().Exec(context.Background(), `UPDATE core.plans SET enabled = false WHERE id = $1`, planID); err != nil {
		t.Fatal(err)
	}
	buyer.tap("an:b:p:" + hexID)
	buyer.toast("not available any more")
	buyer.tap("an:b:s:abcdefghij") // digital accounts are not sold here
	buyer.toast("not available any more")
	buyer.tap("an:b:l:pro:12")
	buyer.toast("not available any more")
	buyer.tap("an:b:p:nothex")
	buyer.toast("not available any more")
}
