package handler

import (
	"strings"
	"testing"
)

// What a delivered service brings, through the whole stack (core, its provisioner, the event feed and the notifier): after the ready message the
// customer gets the owner's tutorial video (a channel post, copied) and the tutorial they picked; the free test too, until the owner switches that
// off; and nothing when the owner has switched it all off.
func TestDeliveryBringsTheVideoAndTheTutorial(t *testing.T) {
	w, _ := helpWorld(t)
	tut := w.addHelp(helpT("Connect on Android", "Open <b>v2rayNG</b>", helpLink("https://youtu.be/abc", "Watch", "تماشا")))
	w.setInCore("delivery.video_post", "https://t.me/heisen_tutorial/12")
	w.setInCore("delivery.tutorial", tut.ID)
	w.reloadSettings()

	// A free test brings them.
	carol := w.customer(301, "carol")
	carol.press("trial")
	carol.eventuallySees("Your service is ready")
	if got := w.tg.copiedTo(301); len(got) != 1 || got[0].from != "@heisen_tutorial" || got[0].id != 12 || got[0].kb != nil {
		t.Fatalf("the video of the free test: %+v", got)
	}
	carol.sees("Direct config")
	carol.sees("<b>Connect on Android</b>\n\nOpen <b>v2rayNG</b>")
	if m := w.tg.last(301); m.kb == nil || len(m.kb.InlineKeyboard) != 1 || m.kb.InlineKeyboard[0][0].URL != "https://youtu.be/abc" || m.kb.InlineKeyboard[0][0].Text != "Watch" {
		t.Errorf("the tutorial's last message: %+v", m)
	}

	// A purchase brings them.
	erin := w.customer(303, "erin")
	w.fund(303, 200_000)
	erin.press("buy")
	erin.press("plan:")
	erin.press("pay:w:")
	erin.eventuallySees("Your service is ready")
	if got := w.tg.copiedTo(303); len(got) != 1 || got[0].from != "@heisen_tutorial" || got[0].id != 12 {
		t.Fatalf("the video of a purchase: %+v", got)
	}
	erin.sees("<b>Connect on Android</b>")

	// The owner takes the free test out of it: the next free test has only its link; a purchase still brings them.
	w.setInCore("delivery.bundle_trial", "false")
	w.reloadSettings()
	dave := w.customer(302, "dave")
	dave.press("trial")
	dave.eventuallySees("Your service is ready")
	if n := len(w.tg.copiedTo(302)); n != 0 || dave.count("Connect on Android") != 0 {
		t.Errorf("the free test with its switch off: %d copies, %d tutorials", n, dave.count("Connect on Android"))
	}

	// And with the whole of it switched off, a purchase has only its link.
	w.setInCore("delivery.bundle", "false")
	w.reloadSettings()
	frank := w.customer(304, "frank")
	w.fund(304, 200_000)
	frank.press("buy")
	frank.press("plan:")
	frank.press("pay:w:")
	frank.eventuallySees("Your service is ready")
	if n := len(w.tg.copiedTo(304)); n != 0 || frank.count("Connect on Android") != 0 {
		t.Errorf("a purchase with the bundle off: %d copies, %d tutorials", n, frank.count("Connect on Android"))
	}
	for _, m := range w.tg.all(304) {
		if strings.Contains(m.text, "Direct config") {
			t.Errorf("a direct config with the bundle off: %q", m.text)
		}
	}
}
