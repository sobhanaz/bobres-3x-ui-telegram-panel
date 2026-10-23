package handler

import (
	"context"
	"testing"
	"time"
)

// A customer who does not see a service in My services finds it by its link, sees its state and renews it, paying from their own account;
// a link that is not one, or that no service has, is told so; the owner of the service who sends their own link gets the usual screen;
// and a renewal of someone else's service made without the link (an old or crafted button) is refused.
func TestFindAServiceByItsLinkAndRenewIt(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	ctx := context.Background()

	owner := w.person(741, "owner_of_it")
	owner.text("/start")
	owner.press("lang:en")
	w.fund(741, 150_000)
	owner.press("buy")
	owner.press("plan:")
	owner.press("pay:w:")
	owner.eventuallySees("Your service is ready")
	subs, err := w.stack.Store.ListSubscriptions(ctx, w.stack.Store.Conn(), w.storeUser(741).ID, 10)
	if err != nil || len(subs) != 1 || subs[0].SubLink == "" {
		t.Fatalf("the service: %v %v", subs, err)
	}
	sub := subs[0]
	before := sub.ExpiresAt

	holder := w.person(742, "link_holder")
	holder.text("/start")
	holder.press("lang:en")
	w.fund(742, 400_000)
	holder.text("/menu")
	holder.press("subs")
	holder.lastButton("🔎 Find by link")
	holder.press("fl")
	holder.shownHas("Send the subscription link")

	// Not a link, and a link no service has: the question stays open.
	holder.text("hello there")
	holder.shownHas("does not look like a subscription link")
	holder.text("https://sub.example.com:2096/sub/doesnotexist12345678")
	holder.shownHas("No service was found")

	// The service's own link: its state, and Renew (its owner's name and account are not shown).
	holder.text(sub.SubLink)
	holder.shownHas("Service found")
	holder.shownHas("Status:")
	if hasCallback(holder.shown(), "sub:") || hasCallback(holder.shown(), "qr:") || hasCallback(holder.shown(), "tup:") {
		t.Fatalf("buttons of the owner's own screen: %v", callbacks(holder.shown()))
	}
	holder.lastButton("🔄 Renew")
	holder.press("fr:")
	holder.shownHas("Choose a plan to renew")
	holder.pressLabel("Monthly 50GB")
	holder.shownHas("Renew")
	if hasCallback(holder.shown(), "sub:"+sub.ID) {
		t.Fatalf("Back leads to a service that is not theirs: %v", callbacks(holder.shown()))
	}
	holder.press("pay:w:")
	holder.sees("Paid from your wallet")
	holder.eventuallySees("renewed")

	after, err := w.stack.Store.GetSubscription(ctx, w.stack.Store.Conn(), sub.ID)
	// The renewal adds the plan's traffic and starts its 30 days again from now (the first period began a moment ago).
	if err != nil || after.ExpiresAt == nil || before == nil || after.ExpiresAt.Before(before.Add(-time.Second)) || after.ExpiresAt.After(before.Add(2*time.Minute)) ||
		after.TrafficTotal == nil || sub.TrafficTotal == nil || *after.TrafficTotal <= *sub.TrafficTotal {
		t.Fatalf("the service was not renewed: before %v after %v (%v)", before, after.ExpiresAt, err)
	}
	if got := w.balanceOf(742); got != 250_000 {
		t.Fatalf("the holder's balance %d, want 250000", got)
	}
	if got := w.balanceOf(741); got != 0 {
		t.Fatalf("the owner's balance %d", got)
	}

	// The owner sends their own link: the usual screen of their service.
	owner.press("home")
	owner.press("subs")
	owner.press("fl")
	owner.text(sub.SubLink)
	owner.shownHas("Subscription link (tap to copy)")

	// A renewal of it by someone else without the link is refused: nothing is charged.
	stranger := w.person(743, "stranger")
	stranger.text("/start")
	stranger.press("lang:en")
	w.fund(743, 400_000)
	stranger.tap("rnw:" + sub.ID)
	if got := w.balanceOf(743); got != 400_000 {
		t.Fatalf("the stranger was charged: %d", got)
	}
	if len(w.tg.all(743)) > 0 && hasCallback(stranger.shown(), "rp:") {
		t.Fatalf("a renewal menu for someone else's service: %v", callbacks(stranger.shown()))
	}
}
