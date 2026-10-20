package handler

import (
	"slices"
	"strings"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/settings"
)

// /new is the Buy button, /status the My services button, and /renew the list
// of the services that can be renewed, each opening that service's plans.
func TestNewRenewStatusCommands(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	cat := w.h.cat
	u := w.customer(7300000001, "cmds")

	// With no service: My services is empty and there is nothing to renew.
	u.text("/status")
	u.lastHas(cat.T("en", "subs.empty"))
	u.text("/renew")
	u.lastHas(cat.T("en", "renew.none"))
	if cb := callbacks(u.last()); !slices.Equal(cb, []string{"buy", "home"}) {
		t.Fatalf("nothing to renew: %v", cb)
	}

	// /new answers as the Buy button does.
	u.text("/new")
	byCommand := u.last()
	u.press("home")
	u.press("buy")
	byButton := u.shown()
	if byCommand.text != byButton.text || !slices.Equal(callbacks(byCommand), callbacks(byButton)) || !hasCallback(byCommand, "plan:") {
		t.Fatalf("/new: %q %v\nBuy: %q %v", byCommand.text, callbacks(byCommand), byButton.text, callbacks(byButton))
	}
	// An upper-case command, and one addressed to this bot, are the same.
	u.text("/NEW")
	u.lastHas(cat.T("en", "plans.title"))
	u.text("/status@test_vpn_bot")
	u.lastHas(cat.T("en", "subs.empty"))

	// With services: My services lists them all; /renew only the ones that can
	// be renewed (a paused one cannot), each a button that opens its plans.
	running := w.seedService(7300000001, "")
	paused := w.seedService(7300000001, "")
	w.setStatus(paused, "disabled")
	u.text("/status")
	if got := callbacks(u.last()); !slices.Contains(got, "sub:"+running) || !slices.Contains(got, "sub:"+paused) {
		t.Fatalf("/status: %v", got)
	}
	u.text("/renew")
	list := u.last()
	if !strings.HasPrefix(list.text, "♻️ <b>Renew a service</b>") || !slices.Equal(callbacks(list), []string{"rnw:" + running, "home"}) {
		t.Fatalf("/renew: %q %v", list.text, callbacks(list))
	}
	u.press("rnw:" + running)
	if !strings.Contains(u.shown().text, "Renew service #") || !hasCallback(u.shown(), "rp:") {
		t.Fatalf("a service picked to renew: %q %v", u.shown().text, callbacks(u.shown()))
	}
}

// A reseller's /renew and /status are their services, as the button's.
func TestResellersRenewAndStatusCommands(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	rs := w.customer(7300000002, "reseller_cmds")
	w.makeReseller(7300000002, 0, 10, 0)
	id := w.seedService(7300000002, "Ali")
	rs.text("/menu")
	rs.press("subs")
	byButton := rs.shown()
	rs.text("/status")
	byStatus := rs.last()
	rs.text("/renew")
	byRenew := rs.last()
	for _, got := range []*msg{byStatus, byRenew} {
		if got.text != byButton.text || !slices.Equal(callbacks(got), callbacks(byButton)) || !hasCallback(got, "sub:"+id) {
			t.Fatalf("a reseller's list: %q %v\nthe button: %q %v", got.text, callbacks(got), byButton.text, callbacks(byButton))
		}
	}
}

// The commands are in the bot's command menu with a Persian and an English
// description, which the owner can edit like any text.
func TestCommandMenuListsNewRenewAndStatus(t *testing.T) {
	cat := i18n.MustLoad()
	set := settings.New()
	p, on := wantedProfile(cat, set, "", true, "")
	if !on {
		t.Fatal("the profile is on by default")
	}
	for lang, want := range map[string]map[string]string{
		"en": {"new": "Buy a new service", "renew": "Renew a service", "status": "My services"},
		"fa": {"new": "خرید سرویس جدید", "renew": "تمدید سرویس", "status": "سرویس\u200cهای من"},
	} {
		got := map[string]string{}
		for _, c := range p.Langs[lang].Commands {
			got[c.Command] = c.Description
		}
		for cmd, desc := range want {
			if got[cmd] != desc {
				t.Errorf("%s /%s: %q, want %q", lang, cmd, got[cmd], desc)
			}
		}
	}
	if names(p.Langs[""].Commands) != "start menu new renew status paysupport" {
		t.Fatalf("the default menu: %q", names(p.Langs[""].Commands))
	}
	// Every staff chat has them too, ahead of its own commands.
	set.SetStaff([]settings.Contact{{TelegramID: 1, Language: "en", Role: "owner"}})
	p, _ = wantedProfile(cat, set, "", true, "")
	if got := names(p.Staff[1]); got != "start menu new renew status paysupport admin dashboard" {
		t.Fatalf("an owner's menu: %q", got)
	}
	// An edited text is the description.
	if dropped := cat.SetOverrides(map[string]string{"texts.en.profile.cmd.new": "Get a VPN"}); len(dropped) > 0 {
		t.Fatalf("dropped: %v", dropped)
	}
	p, _ = wantedProfile(cat, set, "", true, "")
	for _, c := range p.Langs["en"].Commands {
		if c.Command == "new" && c.Description != "Get a VPN" {
			t.Fatalf("an edited description: %q", c.Description)
		}
	}
	for _, c := range p.Langs["fa"].Commands {
		if c.Command == "new" && c.Description != "خرید سرویس جدید" {
			t.Fatalf("an English edit changed the Persian description: %q", c.Description)
		}
	}
}

// The commands pass the gates as the buttons do. Maintenance and a required
// channel answer them, staff pass, someone who has not chosen a language
// chooses it first, a banned customer gets what the buttons give, and a
// command held back by a gate leaves a payment under way alone.
func TestCommandsPassTheGatesLikeTheButtons(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	cat := w.h.cat
	commands := []string{"/new", "/renew", "/status"}
	shown := []string{cat.T("en", "plans.title"), cat.T("en", "renew.none"), cat.T("en", "subs.empty")}
	var held, outside []*person
	for i := range commands { // one customer a command: maintenance answers a message at most once a minute
		held = append(held, w.customer(7300000010+int64(i), "held"))
		outside = append(outside, w.customer(7300000020+int64(i), "outside"))
	}
	sup := w.customer(7300000030, "support_guy")
	w.role(7300000030, "support")
	paying := w.customer(7300000031, "paying")
	paying.press("buy")
	paying.press("plan:")
	paying.press("pay:c:") // a receipt is awaited
	newcomer := w.person(7300000032, "newbie")

	// Maintenance.
	owner.text("/set maintenance.enabled true")
	owner.sees("Saved")
	for i, cmd := range commands {
		held[i].text(cmd)
		held[i].lastHas("under maintenance")
		if strings.Contains(held[i].allText(), shown[i]) {
			t.Fatalf("%s answered in maintenance: %s", cmd, held[i].last().text)
		}
	}
	owner.text("/status") // staff pass
	owner.lastHas(cat.T("en", "subs.empty"))
	sup.text("/new")
	sup.lastHas(cat.T("en", "plans.title"))
	newcomer.text("/new") // not registered yet: the gate answers first, in the language their Telegram has (Persian here)
	newcomer.lastHas(cat.T("fa", "maintenance"))
	// The command was held back, and the payment under way is untouched: its
	// receipt still goes through the gate.
	paying.text("/status")
	paying.lastHas("under maintenance")
	paying.photo("receipt-held", "REF-77")
	paying.lastHas("Received")
	owner.sees("New payment to review from @paying")
	owner.text("/set maintenance.enabled false")
	owner.sees("Saved")
	for i, cmd := range commands {
		held[i].text(cmd)
		held[i].lastHas(shown[i])
	}

	// A required channel.
	owner.text("/set join.channel @testchan")
	owner.sees("Saved")
	for i, cmd := range commands {
		outside[i].text(cmd)
		outside[i].lastHas("Join our channel")
		if strings.Contains(outside[i].allText(), shown[i]) {
			t.Fatalf("%s answered to someone outside the channel", cmd)
		}
	}
	owner.text("/new")
	owner.lastHas(cat.T("en", "plans.title"))
	newcomer.text("/new")
	newcomer.lastHas(cat.T("fa", "join.prompt"))
	for i, cmd := range commands {
		w.tg.setMember(7300000020+int64(i), "member")
		outside[i].press("jn")
		outside[i].text(cmd)
		outside[i].lastHas(shown[i])
	}
	w.tg.setMember(7300000032, "member")
	newcomer.press("jn")
	newcomer.lastHas("Choose your language") // let in, and a language comes first
	owner.text("/set join.channel")
	owner.sees("Saved")

	// A language not chosen yet: the picker, whatever the command; chosen, the
	// command works.
	for _, cmd := range commands {
		newcomer.text(cmd)
		newcomer.lastHas("Choose your language")
	}
	newcomer.press("lang:en")
	newcomer.text("/renew")
	newcomer.lastHas(cat.T("en", "renew.none"))
}

// A banned customer gets from a command what the same button gives them.
func TestCommandsForABannedCustomerAreTheButtons(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	u := w.customer(7300000041, "banned_cmds")
	w.seedService(7300000041, "")
	w.setStatusOf(7300000041, "banned")
	u.text("/menu")
	for _, c := range []struct{ cmd, button string }{{"/new", "buy"}, {"/status", "subs"}} {
		u.text(c.cmd)
		byCommand := u.last()
		u.press("home")
		u.press(c.button)
		byButton := u.shown()
		if byCommand.text != byButton.text || !slices.Equal(callbacks(byCommand), callbacks(byButton)) {
			t.Fatalf("%s: %q %v\nbutton %s: %q %v", c.cmd, byCommand.text, callbacks(byCommand), c.button, byButton.text, callbacks(byButton))
		}
	}
}

// A command is never the answer to an open question: it closes the question
// and does what it says, as a tap on the bottom menu does.
func TestCommandsLeaveAConversationInProgress(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	cat := w.h.cat

	// A message to support.
	ticket := w.customer(7300000051, "ticketing")
	ticket.press("support")
	ticket.text("/status")
	ticket.lastHas(cat.T("en", "subs.empty"))
	ticket.text("hello support")
	ticket.lastHas("Main menu")
	if w.hasOpenTicket(7300000051) {
		t.Fatal("the text after the command was filed as a ticket")
	}

	// A discount code.
	code := w.customer(7300000052, "coding")
	code.press("buy")
	code.press("plan:")
	code.press("dc:")
	code.text("/new")
	code.lastHas(cat.T("en", "plans.title"))
	code.text("SPRING20")
	code.lastHas("Main menu")

	// A receipt: the command closes the payment's question, and a picture after
	// it is not a receipt.
	pay := w.customer(7300000053, "receipting")
	pay.press("buy")
	pay.press("plan:")
	pay.press("pay:c:")
	pay.text("/renew")
	pay.lastHas(cat.T("en", "renew.none"))
	pay.photo("receipt-after-command", "REF-1")
	pay.lastHas("Main menu")
	if owner.count("New payment to review") != 0 {
		t.Fatal("a picture sent after a command was taken for a receipt")
	}

	// A reseller application.
	apply := w.customer(7300000054, "applying")
	apply.press("ap")
	apply.text("/new")
	apply.lastHas(cat.T("en", "plans.title"))
	apply.text("I would like to resell")
	apply.lastHas("Main menu")
	if w.hasOpenTicket(7300000054) {
		t.Fatal("the text after the command was filed as an application")
	}
}
