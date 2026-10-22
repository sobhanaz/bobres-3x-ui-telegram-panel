package handler

import (
	"context"
	"regexp"
	"strings"
	"testing"
)

// A plan the owner marked gives its buyer a discount code for their next purchase of it: told in the chat, for them only, for that plan only, once.
func TestRewardCodeAfterAPurchase(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	owner.text("/plan_add 99000 IRT 7 20 Weekly 20GB | هفتگی")
	ctx := context.Background()
	plans, err := w.stack.Store.ListPlans(ctx, w.stack.Store.Conn(), true)
	if err != nil {
		t.Fatal(err)
	}
	pct := int32(20)
	for _, p := range plans {
		if p.NameI18n["en"] == "Monthly 50GB" {
			p := p
			p.RewardPercent = &pct
			if _, err := w.dom().UpsertPlan(ctx, w.storeUser(ownerID), &p); err != nil {
				t.Fatal(err)
			}
		}
	}

	buyer := w.person(731, "rewarded")
	buyer.text("/start")
	buyer.press("lang:en")
	w.fund(731, 500_000)
	// The plan that gives the reward is the Monthly one (the first plan button is its table row).
	buyer.tap("buy")
	buyer.pressLabel("Monthly 50GB")
	buyer.press("pay:w:")
	buyer.sees("Paid from your wallet")
	buyer.eventuallySees("Your service is ready")
	buyer.eventuallySees("Thank you for your purchase!")
	msg := buyer.lastWith("Thank you for your purchase!").text
	m := regexp.MustCompile(`<code>(THX-[A-Z0-9]{8})</code>`).FindStringSubmatch(msg)
	if m == nil {
		t.Fatalf("no code in %q", msg)
	}
	code := m[1]
	for _, want := range []string{"Monthly 50GB", "20% off", "only for you, once"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the message lacks %q: %q", want, msg)
		}
	}

	// Nobody else can use it.
	other := w.person(732, "other")
	other.text("/start")
	other.press("lang:en")
	w.fund(732, 500_000)
	other.press("buy")
	other.pressLabel("Monthly 50GB")
	other.press("dc:")
	other.text(code)
	other.sees("not valid")

	// For another plan of the buyer's it says so; for this plan it takes 20% off, once.
	buyer.tap("home")
	buyer.tap("buy")
	buyer.pressLabel("Weekly 20GB")
	buyer.press("dc:")
	buyer.text(code)
	buyer.sees("This code is for another plan.")
	buyer.tap("home")
	buyer.tap("buy")
	buyer.pressLabel("Monthly 50GB")
	buyer.press("dc:")
	buyer.text(code)
	buyer.sees("30,000 Toman off")
	buyer.press("pay:w:")
	buyer.sees("Paid from your wallet")
	buyer.eventuallySees("Your service is ready")
	buyer.tap("home")
	buyer.tap("buy")
	buyer.pressLabel("Monthly 50GB")
	buyer.press("dc:")
	buyer.text(code)
	buyer.sees("used up") // one use
}
