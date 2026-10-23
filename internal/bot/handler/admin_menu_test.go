package handler

import (
	"strings"
	"testing"
)

// adminWorld is a world with the owner in English, at the admin menu.
func adminWorld(t *testing.T) (*world, *person) {
	t.Helper()
	w := newWorld(t)
	owner := w.person(ownerID, "boss")
	owner.text("/start")
	owner.press("lang:en")
	owner.text("/admin")
	return w, owner
}

// callbacksOf lists every callback of every keyboard this user was shown.
func callbacksOf(p *person) []string {
	var out []string
	for _, m := range p.w.tg.all(p.id) {
		if m.kb == nil {
			continue
		}
		for _, row := range m.kb.InlineKeyboard {
			for _, b := range row {
				if b.CallbackData != "" {
					out = append(out, b.CallbackData)
				}
			}
		}
	}
	return out
}

func hasLabel(p *person, sub string) bool {
	for _, l := range p.last().labels() {
		if strings.Contains(l, sub) {
			return true
		}
	}
	return false
}

// Every button of the admin menu is reachable from /admin, and every
// callback is within Telegram's 64 bytes (a longer one is refused, and the
// button would be dead).
func TestAdminMenuReachesEveryScreenWithShortCallbacks(t *testing.T) {
	w, owner := adminWorld(t)
	owner.text("/plan_add 150000 IRT 30 50 Monthly | ماهانه")
	owner.text("/discount_add SPRING 20% 5 7")
	for _, screen := range []string{"adm:today", "adm:plans", "adm:disc", "adm:pays",
		"adm:notif", "adm:set", "adm:srv", "adm:stf", "adm"} {
		owner.press("adm")
		owner.text("/admin")
		owner.press(screen)
		if owner.last().kb == nil {
			t.Fatalf("%s showed no buttons: %q", screen, owner.last().text)
		}
	}
	owner.text("/admin")
	owner.press("adm:plans")
	owner.press("adm:tup")
	owner.text("/admin")
	owner.press("adm:plans")
	owner.press("adm:trial")
	// Every settings group and every setting.
	owner.text("/admin")
	owner.press("adm:set")
	var groups []string
	for _, c := range callbacksOf(owner) {
		if strings.HasPrefix(c, "adm:sg:") {
			groups = append(groups, c)
		}
	}
	if len(groups) < 6 {
		t.Fatalf("settings groups: %v", groups)
	}
	for _, g := range groups {
		owner.press("adm:set")
		owner.press(g)
	}
	// A plan card and a discount card.
	owner.text("/admin")
	owner.press("adm:plans")
	owner.press("adm:pl:")
	owner.text("/admin")
	owner.press("adm:disc")

	for _, c := range callbacksOf(owner) {
		if len(c) > 64 {
			t.Errorf("callback over 64 bytes (%d): %s", len(c), c)
		}
	}
	// The same check for the keys of every setting (the longest callbacks).
	owner.text("/admin")
	for _, key := range settingKeys {
		for _, verb := range []string{"adm:sk:", "adm:sv:", "adm:st:", "adm:se:"} {
			if n := len(verb + key + ":staff"); n > 64 {
				t.Errorf("%s%s: %d bytes", verb, key, n)
			}
		}
	}
	_ = w
}

// A plan, step by step: every step explains itself, a wrong answer is
// refused with the reason and asked again, Back goes one step back, the
// confirmation shows the whole plan, and saving puts it on sale.
func TestPlanWizardHappyPathBackAndRefusals(t *testing.T) {
	_, owner := adminWorld(t)
	owner.press("adm:plans")
	owner.press("adm:pn")
	owner.sees("Step 1 of")
	owner.sees("English name")
	owner.sees("Example")
	owner.text("Gold 30 days")
	owner.sees("Persian name")
	owner.text("طلایی ۳۰ روزه")
	owner.sees("Currency")
	owner.press("adm:w:cur:IRT")
	owner.sees("Price in")
	owner.text("cheap")
	owner.sees("not a valid price")
	owner.sees("Price in")
	owner.text("150000")
	owner.sees("Duration in days")
	owner.press("adm:wb")
	owner.sees("Price in")
	owner.text("200000")
	owner.text("0")
	owner.sees("Traffic in GB")
	owner.text("0") // neither a duration nor a traffic limit
	owner.sees("days and GB cannot both be 0")
	owner.text("50")
	owner.sees("Check before saving")
	owner.sees("Gold 30 days")
	owner.press("adm:wok")
	owner.sees("Plan saved")

	owner.text("/admin")
	owner.press("adm:plans")
	owner.sees("Gold 30 days")
}

func TestWizardCancelLeavesNothingBehind(t *testing.T) {
	_, owner := adminWorld(t)
	owner.press("adm:plans")
	owner.press("adm:pn")
	owner.text("Never saved")
	owner.press("adm:wx")
	owner.toast("Cancelled")
	if strings.Contains(owner.last().text, "Never saved") {
		t.Fatalf("after Cancel: %q", owner.last().text)
	}
	// Typing now is not an answer to the old wizard.
	owner.text("/admin")
	owner.press("adm:plans")
	if strings.Contains(owner.last().text, "Never saved") {
		t.Fatal("a cancelled plan was saved")
	}
}

func TestExistingPlanIsEditedByButtons(t *testing.T) {
	_, owner := adminWorld(t)
	owner.text("/plan_add 150000 IRT 30 50 Monthly | ماهانه")
	owner.press("adm:plans")
	owner.press("adm:pl:")
	owner.sees("Monthly")
	owner.press("adm:pe:price:")
	owner.sees("Now:")
	owner.text("175000")
	owner.sees("Saved")
	owner.sees("175")
	// Turn it off and on again.
	owner.press("adm:pt:")
	owner.press("adm:pt:")
}

func TestTopupWizardAndTrialByButtons(t *testing.T) {
	_, owner := adminWorld(t)
	owner.press("adm:plans")
	owner.press("adm:tup")
	owner.press("adm:tn")
	owner.text("+10 GB")
	owner.text("-") // no Persian name
	owner.press("adm:w:cur:IRT")
	owner.text("30000")
	owner.text("10")
	owner.sees("Check before saving")
	owner.press("adm:wok")
	owner.text("/admin")
	owner.press("adm:plans")
	owner.press("adm:tup")
	owner.sees("+10 GB")

	owner.text("/admin")
	owner.press("adm:plans")
	owner.press("adm:trial")
	owner.sees("No trial is set up")
	owner.press("adm:tw")
	owner.text("3")
	owner.text("2")
	owner.sees("Check before saving")
	owner.press("adm:wok")
	owner.press("adm:trial")
	owner.sees("On:")
	owner.press("adm:toff")
	owner.sees("Stopped")
}

func TestDiscountWizardAndSwitch(t *testing.T) {
	_, owner := adminWorld(t)
	owner.press("adm:disc")
	owner.press("adm:dn")
	owner.text("a b")
	owner.sees("A code is 3 to 32 characters")
	owner.text("spring20")
	owner.press("adm:w:kind:percent")
	owner.text("150")
	owner.sees("out of range")
	owner.text("20")
	owner.text("100")
	owner.text("7")
	owner.sees("SPRING20")
	owner.press("adm:wok")
	owner.sees("SPRING20")
	owner.press("adm:disc")
	owner.press("adm:dt:")
	owner.sees("SPRING20")
}

// The settings editor: a switch flips, an option is chosen, a number is
// checked against its range, a text is taken and can be cleared; each
// screen explains the setting and shows what it is now.
func TestSettingsEditorByKind(t *testing.T) {
	_, owner := adminWorld(t)
	owner.press("adm:set")
	owner.press("adm:sg:notify")
	owner.sees("Staff notifications")
	owner.press("adm:st:notify.sales")
	owner.press("adm:sk:notify.recipients")
	owner.sees("Every owner and admin")
	owner.press("adm:sv:notify.recipients:staff")
	owner.sees("Every owner and admin")

	owner.press("adm:set")
	owner.press("adm:sg:limits")
	owner.press("adm:sk:limits.bot_per_10s")
	owner.press("adm:se:limits.bot_per_10s")
	owner.sees("Allowed:")
	owner.sees("Default:")
	owner.text("many")
	owner.sees("whole number")
	owner.text("1000")
	owner.sees("out of range")
	owner.text("40")
	owner.sees("40")

	owner.text("/admin")
	owner.press("adm:pays")
	owner.press("adm:sk:payments.card_holder")
	owner.press("adm:se:payments.card_holder")
	owner.text("Ali Rezaei")
	owner.sees("Ali Rezaei")
	owner.press("adm:se:payments.card_holder")
	owner.text("-")
	if strings.Contains(owner.last().text, "Ali Rezaei") {
		t.Fatalf("the cleared setting still shows: %q", owner.last().text)
	}
	// A value core refuses is shown with its reason, nothing is saved.
	owner.text("/admin")
	owner.press("adm:pays")
	owner.press("adm:sk:payments.usdt_trc20")
	owner.press("adm:se:payments.usdt_trc20")
	owner.text("TXYZ")
	owner.sees("Not accepted")
}

// The old commands keep working next to the buttons.
func TestOldCommandsStillWork(t *testing.T) {
	_, owner := adminWorld(t)
	owner.text("/plan_add 150000 IRT 30 50 Monthly | ماهانه")
	owner.sees("Plan saved")
	owner.text("/set notify.sales true")
	owner.sees("Saved")
	owner.text("/trial 1 1")
	owner.sees("Plan saved")
	owner.text("/discount_add OLD 10%")
	owner.text("/discounts")
	owner.sees("OLD")
	owner.text("/discount_off OLD")
	owner.text("/set") // no arguments: the settings buttons
	owner.sees("Settings")
}

func TestServersByButtons(t *testing.T) {
	_, owner := adminWorld(t)
	owner.press("adm:srv")
	owner.sees("3x-ui servers")
	owner.press("adm:sx:")
	owner.sees("Weight")
	owner.press("adm:sy:")
	owner.sees("off")
	owner.press("adm:sy:")
	owner.press("adm:sm:")
	owner.sees("Server weight")
	owner.text("0")
	owner.sees("out of range")
	owner.text("25")
	owner.press("adm:srv")
	owner.sees("Weight 25")
}

func TestStaffByButtonsAndOwnerOnly(t *testing.T) {
	w, owner := adminWorld(t)
	sam := w.person(861, "sam")
	sam.text("/start")
	sam.press("lang:en")

	owner.press("adm:stf")
	owner.sees("Staff")
	owner.press("adm:sa")
	owner.text("861")
	owner.press("adm:w:role:support")
	owner.text("new support agent")
	owner.sees("Check before saving")
	owner.press("adm:wok")
	owner.sees("support")
	if !hasLabel(owner, "861") && !strings.Contains(owner.last().text, "861") && !strings.Contains(strings.Join(owner.last().labels(), "|"), "sam") {
		t.Fatalf("the staff list: %q %v", owner.last().text, owner.last().labels())
	}
	owner.press("adm:sd:")
	owner.text("left")
	owner.press("adm:wok")

	// An admin does not see the staff screen and is told politely.
	w.role(861, "admin")
	adm := w.person(861, "sam")
	adm.text("/admin")
	if hasLabel(adm, "Staff") || hasLabel(adm, "Payment") {
		t.Fatalf("an admin was offered owner screens: %v", adm.last().labels())
	}
	adm.tap("adm:stf")
	adm.toast("owner")
	adm.tap("adm:sa")
	adm.toast("owner")
}

// A role without the right sees only what it may change, a customer and a
// support agent get nothing of the admin menu.
func TestAdminMenuRoles(t *testing.T) {
	w, owner := adminWorld(t)
	sam := w.person(861, "sam")
	sam.text("/start")
	sam.press("lang:en")
	w.role(861, "admin")
	adm := w.person(861, "sam")
	adm.text("/admin")
	adm.press("adm:set")
	for _, c := range callbacksOf(adm) {
		if c == "adm:sg:payments" {
			t.Fatal("an admin was offered the payment details")
		}
	}
	adm.tap("adm:pays") // typed by hand: core refuses, the bot answers politely
	if strings.Contains(adm.last().text, "Card number") {
		t.Fatalf("an admin saw the payment settings: %q", adm.last().text)
	}

	cust := w.person(862, "carl")
	cust.text("/start")
	cust.press("lang:en")
	cust.tap("adm:set") // crafted: refused, nothing shown
	cust.text("/admin")
	for _, c := range callbacksOf(cust) {
		if strings.HasPrefix(c, "adm") {
			t.Fatalf("a customer was shown %s", c)
		}
	}
	_ = owner
}

func TestBroadcastWizardAndPreview(t *testing.T) {
	_, owner := adminWorld(t)
	owner.text("/admin")
	owner.press("adm:bc")
	owner.sees("Who gets the broadcast")
	owner.press("adm:w:seg:all")
	owner.sees("in Persian")
	owner.text("<b>سلام</b>")
	owner.sees("in English")
	owner.text("<script>")
	owner.sees("not accepted")
	owner.text("<b>Hello</b>")
	owner.sees("Check before sending")
	owner.sees("cannot be taken back")
	owner.press("adm:wok")
	owner.sees("Broadcast started")
}

// The buttons of the main menu name the new screens.
func TestAdminFrontDoorOffersTheButtons(t *testing.T) {
	_, owner := adminWorld(t)
	labels := strings.Join(owner.last().labels(), "|")
	for _, want := range []string{"Review payments", "Today", "Plans", "Discounts", "Payment methods", "Notifications", "Broadcast", "Settings", "Servers", "Staff"} {
		if !strings.Contains(labels, want) {
			t.Errorf("the admin menu lacks %q: %s", want, labels)
		}
	}
}

// Undo: the last change made with the menu's buttons (a setting, a plan or a code switched on or off) is kept and an Undo button puts it back; a
// new change replaces it, and a used one is gone.
func TestAdminUndoTheLastChange(t *testing.T) {
	w, owner := adminWorld(t)
	state := func(key string) string { return w.h.setting(key) }

	// A switch: flip it, see the Undo button, press it, it is as before; the button is gone.
	owner.press("adm:set")
	owner.press("adm:sg:notify")
	before := state("notify.sales")
	owner.press("adm:st:notify.sales")
	if state("notify.sales") == before {
		t.Fatal("the switch did not flip")
	}
	owner.sees("Undo: ")
	owner.press("adm:undo")
	owner.toast("Undone")
	if got := state("notify.sales"); got != before {
		t.Fatalf("after Undo %q, want %q", got, before)
	}
	if hasButton(owner.shown(), "adm:undo") {
		t.Fatalf("Undo is still offered after it was used: %v", owner.shown().labels())
	}
	owner.tap("adm:undo") // an old button: nothing to undo, nothing breaks
	owner.toast("nothing to undo")

	// A typed value, and the one before it comes back (an unset value goes back to unset).
	owner.text("/admin")
	owner.press("adm:pays")
	owner.press("adm:sk:payments.card_holder")
	owner.press("adm:se:payments.card_holder")
	owner.text("Ali Rezaei")
	owner.sees("Undo: ")
	owner.press("adm:undo")
	if got := state("payments.card_holder"); got != "" {
		t.Fatalf("a typed value was not undone: %q", got)
	}
	// /set offers it too.
	owner.text("/set payments.card_holder Sara Ahmadi")
	owner.sees("Undo: ")
	owner.press("adm:undo")
	if got := state("payments.card_holder"); got != "" {
		t.Fatalf("/set was not undone: %q", got)
	}

	// A plan switched off comes back on; a discount code the same.
	owner.text("/plan_add 150000 IRT 30 50 Monthly | ماهانه")
	owner.press("adm:plans")
	owner.press("adm:pl:")
	owner.press("adm:pt:")
	owner.sees("Undo: Monthly")
	owner.press("adm:undo")
	owner.toast("Undone")
	owner.text("/discount_add SPRING20 20%")
	owner.press("adm:disc")
	owner.press("adm:dt:SPRING20")
	owner.sees("Undo: SPRING20")
	owner.press("adm:undo")
	owner.toast("Undone")

	// A new change replaces the last: only the newest is undone.
	owner.press("adm:set")
	owner.press("adm:sg:notify")
	owner.press("adm:st:notify.sales")
	ticketBefore := state("notify.new_ticket")
	salesNow := state("notify.sales")
	owner.press("adm:st:notify.new_ticket")
	owner.press("adm:undo")
	if got := state("notify.new_ticket"); got != ticketBefore {
		t.Fatalf("the newest change was not undone: %q, want %q", got, ticketBefore)
	}
	if got := state("notify.sales"); got != salesNow {
		t.Fatalf("the older change was undone too: %q, want %q", got, salesNow)
	}
}
