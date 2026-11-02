package handler

import (
	"strings"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testenv"
)

// hasButton reports whether any message the user was shown has a button whose data starts with prefix.
func (p *person) hasButton(prefix string) bool {
	for _, m := range p.w.tg.all(p.id) {
		if m.kb == nil {
			continue
		}
		for _, row := range m.kb.InlineKeyboard {
			for _, b := range row {
				if strings.HasPrefix(b.CallbackData, prefix) {
					return true
				}
			}
		}
	}
	return false
}

// A reseller buying for a customer chooses the config name too (with names on for resellers), and their prefix goes in
// front of it.
func TestResellerChoosesAConfigNameForACustomer(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	rs := w.customer(4201, "shop")
	w.makeReseller(4201, 0, 10, 500_000)
	rs.text("/menu")
	rs.press("rs")
	rs.press("rs:pfx")
	rs.text("sob")
	rs.press("rs:pfy:sob")

	rs.text("/menu")
	rs.press("rs")
	rs.press("rs:buy")
	rs.press("rs:p:")
	rs.press("rs:sk:") // no label
	if hasCallback(rs.shown(), "cn:") {
		t.Fatal("a name step while names are off for resellers")
	}
	owner.text("/set config_names.resellers true")
	rs.text("/menu")
	rs.press("rs")
	rs.press("rs:buy")
	rs.press("rs:p:")
	rs.press("rs:sk:")
	rs.press("cn:c:")
	rs.text("ali1234")
	rs.shownHas("Config name: <code>sob-ali1234</code>")
	rs.press("pay:w:")
	rs.shownHas("Paid from your wallet")
	rs.eventuallySees("Your service is ready")
	w.eventually("the client on the panel", func() bool { return w.stack.Panel.Has("sob-ali1234") })
	if sub := w.onlySub(4201); sub.ClientEmail != "sob-ali1234" {
		t.Fatalf("service name = %q", sub.ClientEmail)
	}
}

// Config names in the bot: off by default (no step); on, the payment menu of a new service offers a name, each refusal is
// explained in the buyer's language and they type again, the service is delivered under the name, and My services
// renames it with the link kept.
func TestConfigNamesInTheBot(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	u := w.person(4101, "sara")
	u.text("/start")
	u.press("lang:en")
	u.press("buy")
	u.press("plan:")
	if u.hasButton("cn:") || strings.Contains(u.last().text, "Config name") {
		t.Fatalf("a name step while config names are off: %q", u.last().text)
	}

	owner.text("/set config_names.customers true")
	owner.sees("Saved")
	w.stack.Panel.Seed("taken123", "", []int{1}) // a client an operator made in the panel

	u.press("home")
	u.press("buy")
	u.press("plan:")
	u.sees("Config name: automatic")
	u.press("cn:c:")
	u.sees("4 to 20 characters")
	u.text("ab")
	u.sees("must be 4 to 20 characters")
	u.text("sob han1")
	u.sees("only English letters")
	u.text("sobhanaz")
	u.sees("at least one letter and at least one digit")
	u.text("u12abc")
	u.sees("may not start with u and a digit")
	u.text("taken123")
	u.sees("This name is taken")
	u.text(" Sobhan1234 ")
	u.sees("Config name: <code>sobhan1234</code>")
	// Back to the automatic name, and to the chosen one again.
	u.press("cn:a:")
	u.sees("Config name: automatic")
	u.press("cn:c:")
	u.text("sobhan1234")
	u.sees("Config name: <code>sobhan1234</code>")

	// Pay from the wallet: the service is delivered under the name.
	owner.press("adm")
	owner.press("adm:find")
	owner.text("4101")
	owner.press("adm:adj:")
	owner.text("200000 gift")
	owner.sees("New balance")
	u.press("pay:w:")
	u.sees("Paid from your wallet")
	u.eventuallySees("Your service is ready")
	u.sees("Config name: <code>sobhan1234</code>")
	w.eventually("the client on the panel", func() bool { return w.stack.Panel.Has("sobhan1234") })

	var before string // the link the customer was given (the message is edited into the service when its button is pressed)
	for _, m := range w.tg.all(4101) {
		if strings.Contains(m.text, "Your service is ready") {
			before = m.text
		}
	}

	// My services: the name, and Change config name; the link stays.
	u.press("sub:")
	u.sees("Config name: <code>sobhan1234</code>")
	u.press("cn:r:")
	u.sees("This config is called <code>sobhan1234</code>")
	u.text("taken123")
	u.sees("This name is taken")
	u.text("sara5678")
	u.sees("Done: the config is now called <code>sara5678</code>")
	if !w.stack.Panel.Has("sara5678") || w.stack.Panel.Has("sobhan1234") {
		t.Fatal("the panel was not renamed")
	}
	link := func(text string) string {
		_, after, _ := strings.Cut(text, "<code>"+testenv.SubBase)
		l, _, _ := strings.Cut(after, "</code>")
		return l
	}
	if after := u.last().text; link(after) == "" || link(after) != link(before) {
		t.Fatalf("the link changed:\n%s\n---\n%s", before, after)
	}

	// Switched off again: no Rename button on the service.
	owner.text("/set config_names.customers false")
	u.press("subs")
	u.press("sub:")
	if strings.Contains(u.last().text, "Config name") {
		t.Fatalf("the config name is shown with names off: %q", u.last().text)
	}
	for _, row := range u.last().kb.InlineKeyboard {
		for _, b := range row {
			if strings.HasPrefix(b.CallbackData, "cn:r:") {
				t.Fatal("a Rename button while config names are off")
			}
		}
	}
}
