package handler

import (
	"context"
	"regexp"
	"testing"
)

var affLinkRe = regexp.MustCompile(`start=aff_([a-z0-9]{6,16})`)

// /affiliate gives a customer their link; a newcomer who opens it and buys their first license earns the affiliate a commission; the
// affiliate's own link counts for nothing.
func TestAffiliateFlow(t *testing.T) {
	w, _, owner := licenseWorld(t)
	aff := w.customer(7201, "affiliate")

	// Off: /affiliate says so.
	aff.text("/affiliate")
	aff.shownHas("not active")
	owner.text("/set vendor.enabled true")
	owner.text("/set affiliate.enabled true")
	owner.sees("Saved")

	aff.text("/affiliate")
	aff.shownHas("BOBRES affiliate program")
	aff.shownHas("20%")
	m := affLinkRe.FindStringSubmatch(aff.shown().text)
	if m == nil {
		t.Fatalf("no affiliate link: %q", aff.shown().text)
	}
	code := m[1]
	// The licenses screen offers the program too.
	aff.text("/menu")
	aff.tap("lic")
	if !hasButton(aff.shown(), "aff") {
		t.Fatalf("no affiliate button on the licenses screen: %v", aff.shown().labels())
	}

	// The affiliate's own link records nothing.
	aff.text("/start aff_" + code)
	ctx := context.Background()
	if got, err := w.stack.Store.AffiliateOfBuyer(ctx, w.stack.Store.Conn(), w.storeUser(7201).ID, false); err != nil || got != "" {
		t.Fatalf("self-referral recorded: %q %v", got, err)
	}

	// A newcomer opens the link, picks a language, and buys.
	buyer := w.person(7202, "newbie")
	buyer.text("/start aff_" + code)
	buyer.press("lang:en")
	if got, err := w.stack.Store.AffiliateOfBuyer(ctx, w.stack.Store.Conn(), w.storeUser(7202).ID, false); err != nil || got == "" {
		t.Fatalf("the newcomer was not recorded: %q %v", got, err)
	}
	w.fundUSDT(7202, 20*usdtUnit)
	buyer.text("/menu")
	buyer.tap("lic")
	buyer.tap("lic:b:standard:1")
	buyer.tap(callbackWithPrefix(buyer.shown(), "lic:y:standard:1:"))
	buyer.shownHas("BOBRES-BOT-KEY")

	// 20% of 4 USDT is 0.8 USDT in the affiliate's wallet, and they are told.
	if got := w.usdtBalance(7201); got != 800_000 {
		t.Fatalf("affiliate wallet %d, want %d", got, 800_000)
	}
	aff.text("/affiliate")
	aff.shownHas("Licenses that earned: 1")
}
