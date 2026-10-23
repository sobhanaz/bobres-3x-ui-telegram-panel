package handler

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/state"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	coredomain "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	corestore "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/helpcontent"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testdb"
	"google.golang.org/grpc"
)

// --- what the owner does in the dashboard, through core's domain ---

// helpQ is a question in both languages (Persian text starts with a mark so
// that it can be told from the English one).
func helpQ(q, a string) coredomain.HelpInput {
	return coredomain.HelpInput{Kind: helpcontent.KindFAQ, Enabled: true,
		Title: map[string]string{"en": q, "fa": "س\u200c " + q}, Body: map[string]string{"en": a, "fa": "پ\u200c " + a}}
}

// helpT is a tutorial in both languages with URL buttons.
func helpT(title, body string, buttons ...corestore.HelpButton) coredomain.HelpInput {
	return coredomain.HelpInput{Kind: helpcontent.KindTutorial, Enabled: true, Buttons: buttons,
		Title: map[string]string{"en": title, "fa": "آ\u200c " + title}, Body: map[string]string{"en": body, "fa": "م\u200c " + body}}
}

func helpLink(url, en, fa string) corestore.HelpButton {
	return corestore.HelpButton{URL: url, Label: map[string]string{"en": en, "fa": fa}}
}

// addHelp saves an entry as the owner would and has the bot learn of it (the
// dashboard asks the bot to reload after each change).
func (w *world) addHelp(in coredomain.HelpInput) *corestore.HelpEntry {
	w.t.Helper()
	e, err := w.dom().CreateHelp(context.Background(), w.storeUser(ownerID), in)
	if err != nil {
		w.t.Fatal(err)
	}
	w.reloadSettings()
	return e
}

// helpSQL writes a row the way nothing in core would: by hand.
func (w *world) helpSQL(q string, args ...any) {
	w.t.Helper()
	if _, err := w.stack.Store.DB().Exec(context.Background(), q, args...); err != nil {
		w.t.Fatal(err)
	}
}

func (w *world) reloadSettings() {
	w.t.Helper()
	if err := w.h.RefreshSettings(context.Background()); err != nil {
		w.t.Fatal(err)
	}
}

// helpWorld is a store whose owner is registered and that has no help entries
// (the stack is truncated by every test).
func helpWorld(t *testing.T) (*world, *person) {
	t.Helper()
	w := newWorld(t)
	owner := setupStore(t, w)
	if err := testdb.Truncate(context.Background(), w.stack.Store.DB(), `core.help_entries`); err != nil {
		t.Fatal(err)
	}
	w.reloadSettings()
	return w, owner
}

// persian registers a customer who picked Persian.
func (w *world) persian(id int64, name string) *person {
	p := w.person(id, name)
	p.text("/start")
	p.press("lang:fa")
	return p
}

// actionsOf are the actions of an inline keyboard's buttons, by name: the
// callback's verb, "store" for the button that opens the Mini App.
func actionsOf(m *msg) []string {
	var out []string
	for _, b := range buttons(m) {
		switch {
		case b.WebApp != nil:
			out = append(out, "store")
		case b.CallbackData != "":
			verb, _, _ := strings.Cut(b.CallbackData, ":")
			out = append(out, verb)
		}
	}
	slices.Sort(out)
	return out
}

// menuActionsOf are the actions of a bottom menu's items, as a tap on them runs.
func (p *person) menuActionsOf(m *tg.Menu) []string {
	p.w.t.Helper()
	var out []string
	for _, row := range m.Keyboard {
		for _, b := range row {
			data, ok := p.w.h.menuAction(b.Text)
			if !ok {
				p.w.t.Fatalf("user %d: %q on the bottom menu is no tap the bot knows", p.id, b.Text)
			}
			out = append(out, data)
		}
	}
	slices.Sort(out)
	return out
}

// menuLabels are the texts of the bottom menu's items.
func menuLabels(m *tg.Menu) []string {
	var out []string
	for _, row := range m.Keyboard {
		for _, b := range row {
			out = append(out, b.Text)
		}
	}
	return out
}

// linkButtons are the URL buttons of a message: their texts and addresses.
func linkButtons(m *msg) [][2]string {
	var out [][2]string
	for _, b := range buttons(m) {
		if b.URL != "" {
			out = append(out, [2]string{b.Text, b.URL})
		}
	}
	return out
}

// everyCallbackFits checks Telegram's limit on callback data (64 bytes) for a message.
func everyCallbackFits(t *testing.T, m *msg) {
	t.Helper()
	for _, d := range callbacks(m) {
		if len(d) > 64 {
			t.Errorf("callback data of %d bytes: %q", len(d), d)
		}
	}
}

// --- the buttons ---

// The Tutorials button and the FAQ button show only while there is something
// enabled to show behind them; switching the last entry off takes them away,
// and the bottom menu follows by itself.
func TestHelpButtonsShowOnlyWithContent(t *testing.T) {
	w, _ := helpWorld(t)
	u := w.customer(7001, "help_buttons")

	home := func() *msg {
		t.Helper()
		u.text("/menu")
		return u.shown()
	}
	if hasCallback(home(), "tut") || slices.Contains(menuLabels(u.lastMenu()), w.h.cat.T("en", "btn.tutorials")) {
		t.Fatalf("a Tutorials button with no tutorial: %v / %v", callbacks(u.shown()), menuLabels(u.lastMenu()))
	}
	u.press("support")
	if hasCallback(u.shown(), "faq") {
		t.Fatalf("a FAQ button with no question: %v", callbacks(u.shown()))
	}

	// A tutorial: the home screen and the bottom menu get the button, as one row
	// of its own after Support.
	tut := w.addHelp(helpT("Install the app", "Steps"))
	if !hasCallback(home(), "tut") {
		t.Fatalf("no Tutorials button on the home screen: %v", callbacks(u.shown()))
	}
	label := w.h.cat.T("en", "btn.tutorials")
	menu := u.lastMenu()
	if !slices.Contains(menuLabels(menu), label) {
		t.Fatalf("no Tutorials item on the bottom menu: %v", menuLabels(menu))
	}
	var rows [][]string
	for _, row := range menu.Keyboard {
		var r []string
		for _, b := range row {
			r = append(r, b.Text)
		}
		rows = append(rows, r)
	}
	at := slices.IndexFunc(rows, func(r []string) bool { return slices.Contains(r, label) })
	if len(rows[at]) != 1 || !slices.Contains(rows[at-1], w.h.cat.T("en", "btn.support")) {
		t.Fatalf("the Tutorials item is a row of its own after Support: %v", rows)
	}
	if style := menu.Keyboard[at][0].Style; style != tg.StylePrimary {
		t.Fatalf("the Tutorials item is %q, want blue", style)
	}
	if style := styleOf(t, u.shown(), "tut"); style != tg.StylePrimary {
		t.Fatalf("the Tutorials button is %q, want blue", style)
	}
	// Still no FAQ button: it needs a question.
	u.press("support")
	if hasCallback(u.shown(), "faq") {
		t.Fatalf("a FAQ button with only a tutorial: %v", callbacks(u.shown()))
	}

	// A question: the FAQ button is the first thing under Support, and stays there
	// once a ticket is open, beside the past tickets.
	q := w.addHelp(helpQ("How?", "Like this."))
	u.tapMenu("en", "btn.support")
	first := u.shown().kb.InlineKeyboard[0]
	if len(first) != 1 || first[0].CallbackData != "faq" || first[0].Text != w.h.cat.T("en", "btn.faq") || first[0].Style != "" {
		t.Fatalf("the first row under Support: %+v", first)
	}
	u.text("my service does not work")
	u.tapMenu("en", "btn.support") // the open ticket
	if !hasCallback(u.shown(), "tk:r:") || !hasCallback(u.shown(), "faq") || !hasCallback(u.shown(), "tk:l") {
		t.Fatalf("the open ticket's screen: %v", callbacks(u.shown()))
	}
	// A topic picked keeps it (the prompt is shown again in place).
	if w.hasOpenTicket(7001) {
		if _, err := w.dom().SetTicketStatus(context.Background(), w.storeUser(ownerID), w.openTicket(7001).ID, "closed", ""); err != nil {
			t.Fatal(err)
		}
	}
	u.tapMenu("en", "btn.support")
	u.press("tk:n:payment")
	if !hasCallback(u.shown(), "faq") {
		t.Fatalf("the prompt with a topic picked: %v", callbacks(u.shown()))
	}

	// Switched off: the buttons go, and the bottom menu is sent again without the item.
	if _, err := w.dom().SetHelpEnabled(context.Background(), w.storeUser(ownerID), tut.ID, false, tut.Version); err != nil {
		t.Fatal(err)
	}
	if _, err := w.dom().SetHelpEnabled(context.Background(), w.storeUser(ownerID), q.ID, false, q.Version); err != nil {
		t.Fatal(err)
	}
	w.reloadSettings()
	menus := len(u.menus())
	u.tap("home") // any update: the menu changed, so it comes ahead of the answer
	if len(u.menus()) != menus+1 || slices.Contains(menuLabels(u.lastMenu()), label) {
		t.Fatalf("the bottom menu after the last tutorial went: %d menus, %v", len(u.menus())-menus, menuLabels(u.lastMenu()))
	}
	if hasCallback(u.shown(), "tut") {
		t.Fatalf("the home screen still has Tutorials: %v", callbacks(u.shown()))
	}
	u.tapMenu("en", "btn.support")
	if hasCallback(u.shown(), "faq") {
		t.Fatalf("Support still has the FAQ: %v", callbacks(u.shown()))
	}
}

// The home screen and the bottom menu offer the same things, in every case:
// each role, the switches that add an item (invites, the Mini App, tutorials),
// both languages. An item on one that the other lacks would be a tap that does
// something else than the button next to it.
func TestHelpHomeAndBottomMenuAgree(t *testing.T) {
	w, owner := helpWorld(t)
	owner.text("/set referral.reward_percent 10")
	owner.sees("referral.reward_percent")
	const app = "https://shop.example.com/app/"

	customer := w.customer(7101, "agree_cust")
	farsi := w.persian(7102, "agree_fa")
	reseller := w.customer(7103, "agree_rs")
	w.makeReseller(7103, 0, 10, 0)
	support := w.customer(7104, "agree_sup")
	w.role(7104, "support")
	people := []struct {
		name string
		p    *person
	}{{"customer", customer}, {"persian customer", farsi}, {"reseller", reseller}, {"support", support}, {"owner", owner}}

	check := func(state string) {
		t.Helper()
		for _, c := range people {
			c.p.text("/menu")
			// Bulk purchase is the one home button that has no bottom-menu item on purpose (docs/03-bot-ux.md).
			home := slices.DeleteFunc(actionsOf(c.p.shown()), func(a string) bool { return a == "bk" })
			menu := c.p.menuActionsOf(c.p.lastMenu())
			if !slices.Equal(home, menu) {
				t.Errorf("%s, %s:\n home %v\n menu %v", c.name, state, home, menu)
			}
			// Every action of the menu does what the same button does: the labels route.
			for _, row := range c.p.lastMenu().Keyboard {
				for _, b := range row {
					if data, ok := w.h.menuAction(b.Text); !ok || data == "" {
						t.Errorf("%s, %s: %q routes nowhere", c.name, state, b.Text)
					}
				}
			}
		}
	}
	check("nothing extra")
	// The growth items (Top users beside invites, Become a reseller while the
	// license has resellers) are on both, so they are part of what is compared.
	if !slices.Contains(actionsOf(customer.shown()), "top") || !slices.Contains(actionsOf(customer.shown()), "ap") {
		t.Fatalf("the customer's home has no growth buttons to compare: %v", actionsOf(customer.shown()))
	}
	owner.text("/set growth.leaderboard.enabled false")
	owner.sees("Saved")
	check("with the leaderboard off")
	owner.text("/set growth.leaderboard.enabled true")
	owner.sees("Saved")

	w.addHelp(helpT("T", "text"))
	check("with tutorials")
	for _, c := range people {
		if !slices.Contains(actionsOf(c.p.shown()), "tut") {
			t.Errorf("%s: no Tutorials button on the home screen", c.name)
		}
	}
	w.h.miniApp.Store(&[]string{app}[0])
	check("with tutorials and the Mini App")
	w.h.miniApp.Store(&[]string{""}[0])
	owner.text("/set referral.reward_percent 0")
	owner.sees("Saved")
	check("with tutorials and no invites")
	// A tap on the item answers what the button does.
	customer.tapMenu("en", "btn.tutorials")
	if !hasCallback(customer.last(), "tut:v:") {
		t.Fatalf("the Tutorials item answered %q %v", customer.last().text, callbacks(customer.last()))
	}
	farsi.tapMenu("fa", "btn.tutorials")
	farsi.lastHas(w.h.cat.T("fa", "help.tutorials_title")[:12])
}

// --- the tutorials ---

func TestHelpTutorialsListAndOne(t *testing.T) {
	w, _ := helpWorld(t)
	u := w.customer(7201, "tut_one")

	// Nothing yet (a button on an old keyboard): a plain answer with the way home.
	u.tap("tut")
	u.lastHas(w.h.cat.T("en", "help.tutorials_empty"))
	if got := callbacks(u.shown()); len(got) != 1 || got[0] != "home" {
		t.Fatalf("the empty list: %v", got)
	}

	app := w.addHelp(helpT("Install <the> app & go", "<b>Step 1:</b> open the store &amp; search.\nStep 2: install.",
		helpLink("https://example.com/get", "Download", "دانلود"), helpLink("https://example.com/web", "Website", "وب\u200cسایت")))
	w.addHelp(helpT("Second", "Another text"))
	u.press("home")
	u.press("tut")
	list := u.shown()
	if !strings.Contains(list.text, "Tutorials") || strings.Contains(list.text, "Page 1") {
		t.Fatalf("the list: %q", list.text)
	}
	if labels := list.labels(); len(labels) != 3 || labels[0] != "Install <the> app & go" || labels[1] != "Second" || labels[2] != w.h.cat.T("en", "btn.home") {
		t.Fatalf("a button for each tutorial, as written (a button is plain text), then Home: %q", labels)
	}
	if hasCallback(list, "tut:p:") {
		t.Fatalf("a pager on a list of two: %v", callbacks(list))
	}
	everyCallbackFits(t, list)

	// One tutorial: its text with the markup it was written with, the title escaped,
	// a URL button for each link, and the way back to the page of the list.
	before := len(w.tg.all(u.id))
	u.press("tut:v:1:" + app.ID)
	if len(w.tg.all(u.id)) != before {
		t.Fatal("opening a tutorial sent a new message instead of editing the list")
	}
	one := u.shown()
	if one.text != "<b>Install &lt;the&gt; app &amp; go</b>\n\n<b>Step 1:</b> open the store &amp; search.\nStep 2: install." {
		t.Fatalf("the tutorial: %q", one.text)
	}
	links := linkButtons(one)
	if len(links) != 2 || links[0] != [2]string{"Download", "https://example.com/get"} || links[1] != [2]string{"Website", "https://example.com/web"} {
		t.Fatalf("link buttons: %v", links)
	}
	last := one.kb.InlineKeyboard[len(one.kb.InlineKeyboard)-1]
	if len(last) != 2 || last[0].CallbackData != "tut:p:1" || last[0].Text != w.h.cat.T("en", "btn.back") || last[1].CallbackData != "home" {
		t.Fatalf("Back and Home: %+v", last)
	}
	for _, b := range buttons(one) {
		if b.URL != "" && b.Style != "" {
			t.Errorf("a link to read is colored: %+v", b)
		}
	}
	everyCallbackFits(t, one)
	u.press("tut:p:1") // Back: the list again, in place
	if got := u.shown().labels(); len(got) != 3 || got[1] != "Second" {
		t.Fatalf("back to the list: %q", got)
	}

	// A tutorial that was deleted after the list was shown: told the menu is old,
	// and the list comes fresh.
	gone := w.addHelp(helpT("Short-lived", "x"))
	u.press("tut")
	if err := w.dom().DeleteHelp(context.Background(), w.storeUser(ownerID), gone.ID); err != nil {
		t.Fatal(err)
	}
	u.tap("tut:v:1:" + gone.ID)
	u.toast(w.h.cat.T("en", "error.expired"))
	if labels := u.shown().labels(); slices.Contains(labels, "Short-lived") || len(labels) != 3 {
		t.Fatalf("the list after it: %q", labels)
	}
	// An id that is nothing, and a switched-off tutorial: the same.
	u.tap("tut:v:1:not-an-id")
	u.tap("tut:v:1:00000000-0000-7000-8000-00000000dead")
	if _, err := w.dom().SetHelpEnabled(context.Background(), w.storeUser(ownerID), app.ID, false, app.Version); err != nil {
		t.Fatal(err)
	}
	u.tap("tut:v:1:" + app.ID)
	if labels := u.shown().labels(); len(labels) != 2 || labels[0] != "Second" {
		t.Fatalf("a switched-off tutorial is listed: %q", labels)
	}
}

// Lists of tutorials page by eight: the edges of the pages, a page number that is
// nothing, and the way back from a tutorial to the page it was opened from.
func TestHelpTutorialsPage(t *testing.T) {
	w, _ := helpWorld(t)
	u := w.customer(7301, "tut_pages")
	var ids []string
	add := func(n int) {
		for i := 0; i < n; i++ {
			ids = append(ids, w.addHelp(helpT(fmt.Sprintf("Tutorial %02d", len(ids)+1), "text")).ID)
		}
	}
	titles := func() []string {
		var out []string
		for _, b := range buttons(u.shown()) {
			if strings.HasPrefix(b.CallbackData, "tut:v:") {
				out = append(out, b.Text)
			}
		}
		return out
	}
	open := func(data string) *msg {
		t.Helper()
		u.tap(data)
		m := u.shown()
		everyCallbackFits(t, m)
		return m
	}

	add(8) // exactly a page: no pager, no page line
	m := open("tut")
	if got := titles(); len(got) != 8 || got[7] != "Tutorial 08" || hasCallback(m, "tut:p:") || strings.Contains(m.text, "Page") {
		t.Fatalf("eight: %v %v %q", got, callbacks(m), m.text)
	}
	add(1) // one more: a second page
	m = open("tut")
	if got := titles(); len(got) != 8 || got[7] != "Tutorial 08" || !hasCallback(m, "tut:p:2") || hasCallback(m, "tut:p:0") ||
		!strings.Contains(m.text, "Page 1 of 2") {
		t.Fatalf("nine, page 1: %v %v %q", got, callbacks(m), m.text)
	}
	m = open("tut:p:2")
	if got := titles(); len(got) != 1 || got[0] != "Tutorial 09" || !hasCallback(m, "tut:p:1") || hasCallback(m, "tut:p:3") ||
		!strings.Contains(m.text, "Page 2 of 2") {
		t.Fatalf("nine, page 2: %v %v %q", got, callbacks(m), m.text)
	}
	add(11) // twenty: three pages, 8 + 8 + 4
	m = open("tut:p:2")
	if got := titles(); len(got) != 8 || got[0] != "Tutorial 09" || !hasCallback(m, "tut:p:1") || !hasCallback(m, "tut:p:3") ||
		!strings.Contains(m.text, "Page 2 of 3") {
		t.Fatalf("twenty, page 2: %v %v %q", got, callbacks(m), m.text)
	}
	m = open("tut:p:3")
	if got := titles(); len(got) != 4 || got[3] != "Tutorial 20" || !hasCallback(m, "tut:p:2") || hasCallback(m, "tut:p:4") {
		t.Fatalf("twenty, page 3: %v %v", got, callbacks(m))
	}
	// A page past the end is the last; nothing, zero and a bad number are the first.
	for data, want := range map[string]string{"tut:p:9": "Page 3 of 3", "tut:p:0": "Page 1 of 3", "tut:p:x": "Page 1 of 3", "tut:p:-4": "Page 1 of 3",
		"tut:p:": "Page 1 of 3", "tut:p:999999999": "Page 1 of 3"} {
		if m := open(data); !strings.Contains(m.text, want) {
			t.Errorf("%s: %q, want %q", data, m.text, want)
		}
	}
	// Opened from page 3, a tutorial's Back goes to page 3; from the bottom menu to page 1.
	open("tut:p:3")
	m = open("tut:v:3:" + ids[17])
	if !hasCallback(m, "tut:p:3") || !strings.Contains(m.text, "Tutorial 18") {
		t.Fatalf("tutorial 18: %q %v", m.text, callbacks(m))
	}
	u.tapMenu("en", "btn.tutorials")
	if m := u.last(); !strings.Contains(m.text, "Page 1 of 3") {
		t.Fatalf("from the bottom menu: %q", m.text)
	}
	// A page shown stays when a tutorial goes meanwhile: the last page is held to what there is.
	for _, id := range ids[8:] {
		if err := w.dom().DeleteHelp(context.Background(), w.storeUser(ownerID), id); err != nil {
			t.Fatal(err)
		}
	}
	if m := open("tut:p:3"); !strings.Contains(m.text, "Tutorials") || strings.Contains(m.text, "Page") || len(titles()) != 8 {
		t.Fatalf("a page that is gone: %q %v", m.text, titles())
	}
}

// Persian customers get the Persian text and buttons, Persian digits, and the
// other language where a translation is missing (per entry, so a list can mix).
func TestHelpTutorialsInPersianAndWithFallback(t *testing.T) {
	w, _ := helpWorld(t)
	fa := w.persian(7401, "tut_fa")
	en := w.customer(7402, "tut_en")
	both := w.addHelp(helpT("Both", "Text", helpLink("https://example.com/a", "Get it", "دریافت")))
	onlyEN := w.addHelp(coredomain.HelpInput{Kind: "tutorial", Enabled: true, Title: map[string]string{"en": "English only"}, Body: map[string]string{"en": "English text"},
		Buttons: []corestore.HelpButton{{URL: "https://example.com/b", Label: map[string]string{"en": "Only English label"}}}})
	onlyFA := w.addHelp(coredomain.HelpInput{Kind: "tutorial", Enabled: true, Title: map[string]string{"fa": "فقط فارسی"}, Body: map[string]string{"fa": "متن فارسی"},
		Buttons: []corestore.HelpButton{{URL: "https://example.com/c", Label: map[string]string{"fa": "برچسب فارسی"}}}})

	fa.tapMenu("fa", "btn.tutorials")
	labels := fa.last().labels()
	if len(labels) != 4 || labels[0] != "آ\u200c Both" || labels[1] != "English only" || labels[2] != "فقط فارسی" || labels[3] != w.h.cat.T("fa", "btn.home") {
		t.Fatalf("Persian list: %q", labels)
	}
	if !strings.Contains(fa.last().text, "آموزش استفاده") {
		t.Fatalf("Persian heading: %q", fa.last().text)
	}
	fa.press("tut:v:1:" + both.ID)
	if m := fa.shown(); !strings.Contains(m.text, "م\u200c Text") || linkButtons(m)[0] != [2]string{"دریافت", "https://example.com/a"} || m.labels()[len(m.labels())-2] != w.h.cat.T("fa", "btn.back") {
		t.Fatalf("a Persian tutorial: %q %v", m.text, m.labels())
	}
	// The English-only tutorial, for a Persian customer: English, whole, with its English label.
	fa.press("tut:p:1")
	fa.press("tut:v:1:" + onlyEN.ID)
	if m := fa.shown(); !strings.Contains(m.text, "English text") || linkButtons(m)[0][0] != "Only English label" {
		t.Fatalf("a fallback tutorial: %q %v", m.text, linkButtons(m))
	}
	// And the Persian-only one, for an English customer.
	en.tapMenu("en", "btn.tutorials")
	if labels := en.last().labels(); labels[2] != "فقط فارسی" {
		t.Fatalf("English list: %q", labels)
	}
	en.press("tut:v:1:" + onlyFA.ID)
	if m := en.shown(); !strings.Contains(m.text, "متن فارسی") || linkButtons(m)[0][0] != "برچسب فارسی" {
		t.Fatalf("a Persian-only tutorial for an English customer: %q %v", m.text, linkButtons(m))
	}
	// Persian digits in the page line.
	for i := 0; i < 9; i++ {
		w.addHelp(helpT(fmt.Sprintf("More %d", i), "x"))
	}
	fa.tapMenu("fa", "btn.tutorials")
	if !strings.Contains(fa.last().text, "صفحهٔ ۱ از ۲") {
		t.Fatalf("Persian page line: %q", fa.last().text)
	}
}

// What cannot be sent as written is not sent in a way Telegram would refuse: an
// address that is not https is left off the buttons, and text too long for a
// message (core's checks keep it from being stored, but a row can be written by
// hand) comes shortened and plain instead of not at all.
func TestHelpTutorialsAreSentWhateverIsStored(t *testing.T) {
	w, _ := helpWorld(t)
	u := w.customer(7501, "tut_odd")
	long := strings.Repeat("long text ", 700) // far past a message
	w.helpSQL(`INSERT INTO core.help_entries (id, kind, title, body, buttons, sort) VALUES
		('00000000-0000-7000-8000-000000007501', 'tutorial', '{"en":"Odd"}', jsonb_build_object('en', $1::text),
		 '[{"url":"http://insecure.example.com","label":{"en":"Insecure"}},{"url":"https://ok.example.com/x","label":{"en":"Fine"}},{"url":"not a url","label":{"en":"Broken"}}]', 1)`, "<b>"+long+"</b>")
	w.reloadSettings()
	u.tap("tut")
	u.tap("tut:v:1:00000000-0000-7000-8000-000000007501")
	m := u.shown()
	if links := linkButtons(m); len(links) != 1 || links[0] != [2]string{"Fine", "https://ok.example.com/x"} {
		t.Fatalf("buttons: %v", links)
	}
	if !strings.Contains(m.text, "long text") || strings.Contains(m.text, "<b>") || i18n.TextLength(m.text) > i18n.MaxMessage {
		t.Fatalf("a text too long for a message: %d units, %q…", i18n.TextLength(m.text), m.text[:40])
	}
}

// --- the FAQ ---

func TestHelpFAQOneMessageWhenItFits(t *testing.T) {
	w, _ := helpWorld(t)
	u := w.customer(7601, "faq_one")

	// Nothing: an old button gets a plain answer, with the way on to support.
	u.tap("faq")
	u.lastHas(w.h.cat.T("en", "help.faq_empty"))
	if got := callbacks(u.shown()); !slices.Equal(got, []string{"support", "support", "home"}) {
		t.Fatalf("the empty FAQ: %v", got)
	}

	w.addHelp(helpQ("Will I get my own IP?", "No. <b>Many</b> customers share addresses &amp; servers."))
	w.addHelp(helpQ("How do I renew?", "Tap <i>Renew</i>."))
	u.press("support")
	u.press("faq")
	m := u.shown()
	want := w.h.cat.T("en", "help.faq_title") +
		"\n\n<b>1. Will I get my own IP?</b>\nNo. <b>Many</b> customers share addresses &amp; servers." +
		"\n\n<b>2. How do I renew?</b>\nTap <i>Renew</i>." +
		"\n\n" + w.h.cat.T("en", "help.faq_footer")
	if m.text != want {
		t.Fatalf("the FAQ:\n got %q\nwant %q", m.text, want)
	}
	// Support first (blue), then Back (to Support) and Home.
	rows := m.kb.InlineKeyboard
	if len(rows) != 2 || len(rows[0]) != 1 || rows[0][0].CallbackData != "support" || rows[0][0].Text != w.h.cat.T("en", "btn.faq_support") ||
		rows[0][0].Style != tg.StylePrimary || len(rows[1]) != 2 || rows[1][0].CallbackData != "support" || rows[1][0].Text != w.h.cat.T("en", "btn.back") ||
		rows[1][1].CallbackData != "home" {
		t.Fatalf("the buttons: %+v", rows)
	}
	// It replaced the Support screen in place, and the question mark and the
	// numbers are the customer's own: Persian digits for a Persian customer.
	fa := w.persian(7602, "faq_fa")
	fa.press("support")
	fa.press("faq")
	if m := fa.shown(); !strings.Contains(m.text, "<b>۱. س\u200c Will I get my own IP?</b>\nپ\u200c No.") || !strings.Contains(m.text, "<b>۲. ") ||
		!strings.Contains(m.text, w.h.cat.T("fa", "help.faq_title")) || strings.Contains(m.text, "<b>1.") {
		t.Fatalf("Persian: %q", m.text)
	}

	// From the FAQ, support is one tap away, and typing then files a ticket.
	u.press("support")
	if !hasCallback(u.shown(), "tk:n:") {
		t.Fatalf("Send a message to support did not open the prompt: %v", callbacks(u.shown()))
	}
	u.text("I could not find my answer")
	u.sees("Your message was sent")
	if !w.hasOpenTicket(7601) {
		t.Fatal("no ticket")
	}
	// With the ticket open the FAQ is still reachable, and its support button leads back to the ticket.
	u.tapMenu("en", "btn.support")
	u.press("faq")
	u.press("support")
	if !hasCallback(u.shown(), "tk:r:") || !strings.Contains(u.shown().text, "I could not find my answer") {
		t.Fatalf("the thread: %q %v", u.shown().text, callbacks(u.shown()))
	}
}

// The titles are plain text escaped where they go into the message, the answers
// are the markup they were written with, the numbers follow the order the owner
// chose, and a switched-off question is not there.
func TestHelpFAQOrderEscapingAndSwitchedOff(t *testing.T) {
	w, _ := helpWorld(t)
	u := w.customer(7701, "faq_order")
	a := w.addHelp(helpQ("Billing & <refunds>?", "Answer A"))
	b := w.addHelp(helpQ("Second", "Answer B"))
	c := w.addHelp(helpQ("Third", "Answer C"))
	if _, err := w.dom().SetHelpEnabled(context.Background(), w.storeUser(ownerID), b.ID, false, b.Version); err != nil {
		t.Fatal(err)
	}
	if err := w.dom().ReorderHelp(context.Background(), w.storeUser(ownerID), "faq", []string{c.ID, b.ID, a.ID}); err != nil {
		t.Fatal(err)
	}
	w.reloadSettings()
	u.tap("faq")
	m := u.shown()
	if !strings.Contains(m.text, "<b>1. Third</b>\nAnswer C") || !strings.Contains(m.text, "<b>2. Billing &amp; &lt;refunds&gt;?</b>\nAnswer A") ||
		strings.Contains(m.text, "Second") || strings.Contains(m.text, "Answer B") {
		t.Fatalf("the FAQ: %q", m.text)
	}
}

// The edges of "one message": twelve questions fit when their text is short, a
// thirteenth does not; three thousand units fit and one more does not.
func TestHelpFAQLayoutEdges(t *testing.T) {
	w, _ := helpWorld(t)
	u := w.customer(7801, "faq_edges")
	isMessage := func() bool {
		t.Helper()
		u.tap("faq")
		m := u.shown()
		everyCallbackFits(t, m)
		return strings.Contains(m.text, "<b>1. ") && !hasCallback(m, "faq:q:")
	}
	pad := func(n int) string { return strings.Repeat("x", n) }
	ids := []string{}
	add := func(title string, bodyUnits int) {
		ids = append(ids, w.addHelp(helpQ(title, pad(bodyUnits))).ID)
	}
	// Twelve short ones: one message.
	for i := 1; i <= helpcontent.OneMessageMaxEntries; i++ {
		add(fmt.Sprintf("q%02d", i), 20)
	}
	if !isMessage() {
		t.Fatalf("twelve short questions are not one message: %q", u.shown().text)
	}
	add("q13", 20)
	if isMessage() {
		t.Fatalf("thirteen are one message: %q", u.shown().text)
	}
	if !hasCallback(u.shown(), "faq:q:1:") {
		t.Fatalf("no list: %v", callbacks(u.shown()))
	}
	// The length: three entries of exactly 3000 units in all, then one over.
	for _, id := range ids {
		if err := w.dom().DeleteHelp(context.Background(), w.storeUser(ownerID), id); err != nil {
			t.Fatal(err)
		}
	}
	w.reloadSettings()
	three := []*corestore.HelpEntry{}
	for i, title := range []string{"a1", "a2", "a3"} { // titles of 2 units each: 6, so 2994 left for the answers
		in := helpQ(title, pad(998))
		in.Title["fa"], in.Body["fa"] = in.Title["en"], in.Body["en"] // the same in both languages
		three = append(three, w.addHelp(in))
		_ = i
	}
	if !isMessage() {
		t.Fatalf("exactly %d units are not one message: %q", helpcontent.OneMessageMaxUnits, u.shown().text[:60])
	}
	if i18n.TextLength(u.shown().text) > i18n.MaxMessage {
		t.Fatalf("the message is %d units: Telegram takes %d", i18n.TextLength(u.shown().text), i18n.MaxMessage)
	}
	longer := helpQ("a3", pad(999))
	longer.Title["fa"], longer.Body["fa"] = longer.Title["en"], longer.Body["en"]
	if _, err := w.dom().UpdateHelp(context.Background(), w.storeUser(ownerID), three[2].ID, longer, three[2].Version); err != nil {
		t.Fatal(err)
	}
	w.reloadSettings()
	if isMessage() {
		t.Fatalf("%d units are one message", helpcontent.OneMessageMaxUnits+1)
	}
}

// Past what one message takes, the questions are a numbered list of ten to a
// page; each number opens its answer, which has Back to the page it was opened
// from, Send a message to support, and Home.
func TestHelpFAQListPagesAndAnswers(t *testing.T) {
	w, _ := helpWorld(t)
	u := w.customer(7901, "faq_list")
	var ids []string
	for i := 1; i <= 23; i++ {
		ids = append(ids, w.addHelp(helpQ(fmt.Sprintf("Question number %d?", i), fmt.Sprintf("Answer number %d.", i))).ID)
	}
	u.press("support")
	u.press("faq")
	m := u.shown()
	if !strings.Contains(m.text, w.h.cat.T("en", "help.faq_title")) || !strings.Contains(m.text, w.h.cat.T("en", "help.faq_hint")) ||
		!strings.Contains(m.text, "<b>1.</b> Question number 1?") || !strings.Contains(m.text, "<b>10.</b> Question number 10?") ||
		strings.Contains(m.text, "Question number 11?") || strings.Contains(m.text, "Answer number") ||
		!strings.Contains(m.text, "Page 1 of 3") || !strings.Contains(m.text, w.h.cat.T("en", "help.faq_footer")) {
		t.Fatalf("page 1: %q", m.text)
	}
	// Numbers in rows of five, then Next, then support, then Back and Home.
	var shape []string
	for _, row := range m.kb.InlineKeyboard {
		verb, _, _ := strings.Cut(row[0].CallbackData, ":")
		shape = append(shape, fmt.Sprintf("%d:%s", len(row), verb))
	}
	if want := []string{"5:faq", "5:faq", "1:faq", "1:support", "2:support"}; !slices.Equal(shape, want) {
		t.Fatalf("shape %v, want %v", shape, want)
	}
	if m.kb.InlineKeyboard[0][0].Text != "1" || m.kb.InlineKeyboard[1][4].Text != "10" || m.kb.InlineKeyboard[0][0].CallbackData != "faq:q:1:"+ids[0] {
		t.Fatalf("number buttons: %+v", m.kb.InlineKeyboard[:2])
	}
	everyCallbackFits(t, m)

	// Page 2: Previous and Next, numbers 11 to 20; page 3: 21 to 23, Previous only.
	u.press("faq:p:2")
	m = u.shown()
	if !strings.Contains(m.text, "<b>11.</b> Question number 11?") || !strings.Contains(m.text, "Page 2 of 3") ||
		m.kb.InlineKeyboard[0][0].Text != "11" || !hasCallback(m, "faq:p:1") || !hasCallback(m, "faq:p:3") {
		t.Fatalf("page 2: %q %v", m.text, callbacks(m))
	}
	u.press("faq:p:3")
	m = u.shown()
	if !strings.Contains(m.text, "<b>23.</b> Question number 23?") || strings.Contains(m.text, "Question number 24") || !hasCallback(m, "faq:p:2") || hasCallback(m, "faq:p:4") ||
		len(m.kb.InlineKeyboard[0]) != 3 {
		t.Fatalf("page 3: %q %v", m.text, callbacks(m))
	}
	for data, want := range map[string]string{"faq:p:9": "Page 3 of 3", "faq:p:x": "Page 1 of 3", "faq:p:0": "Page 1 of 3", "faq:p:": "Page 1 of 3"} {
		u.tap(data)
		if got := u.shown().text; !strings.Contains(got, want) {
			t.Errorf("%s: %q, want %q", data, got, want)
		}
	}

	// An answer: opened from page 2, Back goes to page 2.
	u.press("faq:p:2")
	before := len(w.tg.all(u.id))
	u.press("faq:q:2:" + ids[12])
	if len(w.tg.all(u.id)) != before {
		t.Fatal("an answer was sent as a new message")
	}
	m = u.shown()
	if m.text != "❓ <b>Question number 13?</b>\n\nAnswer number 13." {
		t.Fatalf("the answer: %q", m.text)
	}
	rows := m.kb.InlineKeyboard
	if len(rows) != 2 || rows[0][0].CallbackData != "support" || rows[0][0].Text != w.h.cat.T("en", "btn.faq_support") ||
		rows[1][0].CallbackData != "faq:p:2" || rows[1][0].Text != w.h.cat.T("en", "btn.back") || rows[1][1].CallbackData != "home" {
		t.Fatalf("the buttons of an answer: %+v", rows)
	}
	u.press("faq:p:2") // Back
	if !strings.Contains(u.shown().text, "Page 2 of 3") {
		t.Fatalf("Back: %q", u.shown().text)
	}
	// An answer that went meanwhile, and ids that are nothing: told the menu is old, and the list.
	if err := w.dom().DeleteHelp(context.Background(), w.storeUser(ownerID), ids[12]); err != nil {
		t.Fatal(err)
	}
	u.tap("faq:q:2:" + ids[12])
	u.toast(w.h.cat.T("en", "error.expired"))
	if !hasCallback(u.shown(), "faq:q:") || !strings.Contains(u.shown().text, "Page 2 of 3") {
		t.Fatalf("after a gone answer: %q", u.shown().text)
	}
	u.tap("faq:q:1:not-an-id")
	u.tap("faq:q:zz")
	if !hasCallback(u.shown(), "faq:q:") {
		t.Fatalf("after crafted answers: %q", u.shown().text)
	}
	// The list of a Persian customer: Persian digits on the numbers and the buttons.
	fa := w.persian(7902, "faq_list_fa")
	fa.press("support")
	fa.press("faq")
	m = fa.shown()
	if !strings.Contains(m.text, "<b>۱.</b> س\u200c Question number 1?") || !strings.Contains(m.text, "صفحهٔ ۱ از ۳") || m.kb.InlineKeyboard[1][4].Text != "۱۰" {
		t.Fatalf("Persian list: %q %+v", m.text, m.kb.InlineKeyboard[1])
	}
}

// A long answer is shown whole on its screen even when the list or the single
// message could not hold it; the longest question and answer together fit.
func TestHelpFAQLongAnswers(t *testing.T) {
	w, _ := helpWorld(t)
	u := w.customer(8001, "faq_long")
	question := strings.Repeat("q", helpcontent.MaxQuestion)
	answer := "<b>" + strings.Repeat("a", helpcontent.MaxBody-7) + "</b>\n" + strings.Repeat("ب", 4)
	long := helpQ(question, answer)
	long.Title["fa"], long.Body["fa"] = long.Title["en"], long.Body["en"] // the same in both languages: the longest each may be
	e := w.addHelp(long)
	w.addHelp(helpQ("Another", "short"))
	u.tap("faq") // 3000+ units over two entries: the list
	if !hasCallback(u.shown(), "faq:q:") {
		t.Fatalf("a long FAQ is not a list: %q", u.shown().text[:80])
	}
	if text := u.shown().text; strings.Count(text, "q") < helpcontent.MaxQuestion || i18n.TextLength(text) > i18n.MaxMessage {
		t.Fatalf("the list: %d units", i18n.TextLength(text))
	}
	u.press("faq:q:1:" + e.ID)
	m := u.shown()
	if !strings.Contains(m.text, strings.Repeat("a", 100)) || !strings.HasSuffix(m.text, "</b>\nbbbb") && !strings.Contains(m.text, "</b>\n"+strings.Repeat("ب", 4)) {
		t.Fatalf("the long answer was cut: %d units", i18n.TextLength(m.text))
	}
	if n := i18n.TextLength(m.text); n > i18n.MaxMessage || n < helpcontent.MaxBody {
		t.Fatalf("the answer is %d units", n)
	}
	// Written by hand past what core allows: sent, shortened, plain.
	w.helpSQL(`INSERT INTO core.help_entries (id, kind, title, body, sort) VALUES
		('00000000-0000-7000-8000-000000008001', 'faq', '{"en":"Huge"}', jsonb_build_object('en', $1::text), 9)`, "<b>"+strings.Repeat("h", 6000)+"</b>")
	w.reloadSettings()
	u.tap("faq:q:1:00000000-0000-7000-8000-000000008001")
	if m := u.shown(); i18n.TextLength(m.text) > i18n.MaxMessage || strings.Contains(m.text, "<b>") || !strings.Contains(m.text, "hhhh") {
		t.Fatalf("a huge answer: %d units %q", i18n.TextLength(m.text), m.text[:30])
	}
}

// The default FAQ is the screenshot's: one message, in both languages, with Persian
// numerals in Persian, each answer in the customer's language.
func TestHelpFAQDefaultContentIsOneMessage(t *testing.T) {
	w, _ := helpWorld(t)
	if _, err := w.dom().AddDefaultHelp(context.Background(), w.storeUser(ownerID), "faq"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.dom().AddDefaultHelp(context.Background(), w.storeUser(ownerID), "tutorial"); err != nil {
		t.Fatal(err)
	}
	w.reloadSettings()
	for lang, who := range map[string]*person{"en": w.customer(8101, "defaults_en"), "fa": w.persian(8102, "defaults_fa")} {
		who.press("support")
		who.press("faq")
		m := who.shown()
		if hasCallback(m, "faq:q:") || i18n.TextLength(m.text) > i18n.MaxMessage || !strings.Contains(m.text, w.h.cat.T(lang, "help.faq_footer")) {
			t.Errorf("%s: %d units, %v", lang, i18n.TextLength(m.text), callbacks(m))
		}
		first := "<b>1. "
		if lang == "fa" {
			first = "<b>۱. "
		}
		if !strings.Contains(m.text, first) || !strings.Contains(m.text, "<b>6. ") && !strings.Contains(m.text, "<b>۶. ") {
			t.Errorf("%s: %q", lang, m.text)
		}
		// Every tutorial of the defaults opens, with its buttons.
		who.press("home")
		who.press("tut")
		list := who.shown()
		if n := len(buttons(list)); n != len(coredomain.DefaultHelp("tutorial"))+1 { // every default tutorial and Home
			t.Errorf("%s: %d buttons in the tutorials list", lang, n)
		}
		for _, d := range callbacks(list) {
			if !strings.HasPrefix(d, "tut:v:") {
				continue
			}
			who.tap(d)
			if got := who.shown(); len(got.text) < 50 || len(linkButtons(got)) > 3 {
				t.Errorf("%s %s: %q %v", lang, d, got.text, linkButtons(got))
			}
			who.tap("tut:p:1")
		}
	}
}

// --- gates ---

// Both screens go through every gate as any other: maintenance, the required
// channel, a ban and the language, whether reached by a button or by a tap on the
// bottom menu.
func TestHelpScreensGoThroughTheGates(t *testing.T) {
	w, owner := helpWorld(t)
	tut := w.addHelp(helpT("Tutorial", "text"))
	q := w.addHelp(helpQ("Question", "answer"))
	u := w.customer(8201, "gates_a")
	staff := w.customer(8202, "gates_staff")
	w.role(8202, "support")
	banned := w.customer(8203, "gates_banned")
	ctx := context.Background()
	reach := func(p *person) {
		t.Helper()
		p.tap("tut")
		p.tap("tut:v:1:" + tut.ID)
		p.tap("faq")
		p.tap("faq:q:1:" + q.ID)
		p.tapMenu("en", "btn.tutorials")
	}

	// Maintenance: customers get the popup (or the message at most once a minute), staff pass.
	owner.text("/set maintenance.enabled true")
	owner.sees("Saved")
	for _, data := range []string{"tut", "tut:p:2", "tut:v:1:" + tut.ID, "faq", "faq:p:1", "faq:q:1:" + q.ID} {
		u.tap(data)
		u.toast("under maintenance")
	}
	if strings.Contains(u.shown().text, "Tutorial") || strings.Contains(u.shown().text, "answer") {
		t.Fatalf("a customer saw help content during maintenance: %q", u.shown().text)
	}
	u.tapMenu("en", "btn.tutorials")
	u.lastHas("under maintenance")
	staff.tap("tut")
	if !hasCallback(staff.shown(), "tut:v:") {
		t.Fatalf("staff during maintenance: %q", staff.shown().text)
	}
	owner.text("/set maintenance.enabled false")
	owner.sees("Saved")
	u.tapMenu("en", "btn.tutorials")
	if !hasCallback(u.last(), "tut:v:") {
		t.Fatalf("after maintenance: %q", u.last().text)
	}

	// The required channel.
	owner.text("/set join.channel @testchan")
	owner.sees("Saved")
	for _, data := range []string{"tut", "tut:v:1:" + tut.ID, "faq", "faq:q:1:" + q.ID} {
		u.tap(data)
		u.lastHas("Join our channel")
		if !hasCallback(u.last(), "jn") {
			t.Fatalf("%s: no I've-joined button: %v", data, callbacks(u.last()))
		}
	}
	u.tapMenu("en", "btn.tutorials")
	u.lastHas("Join our channel")
	staff.tap("faq")
	if !hasCallback(staff.shown(), "support") {
		t.Fatalf("staff with a required channel: %q", staff.shown().text)
	}
	w.tg.setMember(8201, "member")
	u.tap("tut")
	if !hasCallback(u.shown(), "tut:v:") {
		t.Fatalf("a member: %q", u.shown().text)
	}
	owner.text("/set join.channel")
	owner.sees("Saved")

	// A banned customer is refused as everywhere else, and a typed label too.
	if err := w.stack.Store.SetUserStatus(ctx, w.stack.Store.Conn(), w.storeUser(8203).ID, "banned"); err != nil {
		t.Fatal(err)
	}
	for _, data := range []string{"tut", "tut:v:1:" + tut.ID, "faq", "faq:q:1:" + q.ID} {
		banned.tap(data)
		banned.toast("restricted")
	}
	banned.tapMenu("en", "btn.tutorials")
	banned.lastHas("restricted")
	if hasCallback(banned.last(), "tut:v:") {
		t.Fatal("a banned customer was shown the tutorials")
	}
	_ = reach

	// The language: the screens, the labels and the bottom menu follow the customer's.
	u.press("lang")
	u.press("lang:fa")
	u.hasMenuLabel("fa", "btn.tutorials")
	u.tapMenu("fa", "btn.tutorials")
	u.lastHas("آموزش استفاده")
	if labels := u.last().labels(); labels[0] != "آ\u200c Tutorial" {
		t.Fatalf("Persian: %q", labels)
	}
}

// hasMenuLabel checks that the user's bottom menu has the item with this text key.
func (p *person) hasMenuLabel(lang, key string) {
	p.w.t.Helper()
	if want := p.w.h.cat.T(lang, key); !slices.Contains(menuLabels(p.lastMenu()), want) {
		p.w.t.Fatalf("user %d: the bottom menu %v has no %q", p.id, menuLabels(p.lastMenu()), want)
	}
}

// A newcomer or a customer holding a payment under way is not let through the
// gate by the new screens: a tap is a tap.
func TestHelpTapsDoNotRideOnAPaymentUnderWay(t *testing.T) {
	w, owner := helpWorld(t)
	w.addHelp(helpT("Tutorial", "text"))
	paying := w.customer(8301, "gate_paying")
	paying.press("buy")
	paying.press("plan:")
	paying.press("pay:c:") // a receipt is awaited
	owner.text("/set maintenance.enabled true")
	owner.sees("Saved")
	paying.tapMenu("en", "btn.tutorials")
	paying.lastHas("under maintenance")
	paying.photo("receipt-help", "REF-1") // the payment itself still goes through
	paying.lastHas("Received")
}

// --- a core that does not know the calls yet ---

// olderHelpCore answers the help calls as a core without them does: gRPC says
// they are unimplemented.
type olderHelpCore struct{ corev1.CoreServiceClient }

func (olderHelpCore) ListHelpEntries(context.Context, *corev1.ListHelpEntriesRequest, ...grpc.CallOption) (*corev1.ListHelpEntriesResponse, error) {
	return nil, errOlder
}

func (olderHelpCore) GetHelpEntry(context.Context, *corev1.GetHelpEntryRequest, ...grpc.CallOption) (*corev1.HelpEntry, error) {
	return nil, errOlder
}

// A bot updated before its core shows nothing new: the buttons need the core's
// word that there is something to show, and a label typed from an old keyboard gets
// the empty answer, not an error.
func TestHelpWithACoreOlderThanTheBot(t *testing.T) {
	w, _ := helpWorld(t)
	w.addHelp(helpT("Tutorial", "text"))
	w.addHelp(helpQ("Question", "answer"))
	u := w.customer(8401, "older")
	w.h = New(olderHelpCore{w.stack.Core}, w.tg, state.NewMemory(), i18n.MustLoad(), nil,
		Config{AdminTelegramIDs: []int64{ownerID}, BotName: "TestVPN", BotUsername: "test_vpn_bot"}, nil)
	// This one asks the real core for its settings (which have the flags), so the
	// buttons show; the calls behind them are the old core's.
	w.reloadSettings()
	u.text("/menu")
	u.tap("tut")
	u.lastHas(w.h.cat.T("en", "help.tutorials_empty"))
	u.tap("faq")
	u.lastHas(w.h.cat.T("en", "help.faq_empty"))
	u.tap("tut:v:1:00000000-0000-7000-8000-00000000dead")
	u.toast(w.h.cat.T("en", "error.expired"))
}
