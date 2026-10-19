package handler

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/state"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
)

// --- the bottom menu, as the user's chat holds it ---

// menus are the messages that brought the user a bottom menu.
func (p *person) menus() []*msg {
	var out []*msg
	for _, m := range p.w.tg.all(p.id) {
		if m.menu != nil {
			out = append(out, m)
		}
	}
	return out
}

// lastMenu is the bottom menu the user has now: the newest one sent.
func (p *person) lastMenu() *tg.Menu {
	p.w.t.Helper()
	ms := p.menus()
	if len(ms) == 0 {
		p.w.t.Fatalf("user %d: no bottom menu", p.id)
	}
	return ms[len(ms)-1].menu
}

// tapMenu taps a button of the bottom menu: its label comes as a message.
func (p *person) tapMenu(lang, key string) { p.text(p.w.h.cat.T(lang, key)) }

// menuStyles are the colors of the bottom menu's buttons; the others have none.
var menuStyles = map[string]string{
	"btn.buy": tg.StyleSuccess, "btn.trial": tg.StyleSuccess,
	"btn.services": tg.StylePrimary, "btn.wallet": tg.StylePrimary, "btn.support": tg.StylePrimary,
	"btn.reseller": tg.StylePrimary, "btn.invite": tg.StylePrimary,
}

// hasMenu checks the user's bottom menu: rows of text keys in lang, colored
// as menuStyles says, persistent and resized, with the hint.
func (p *person) hasMenu(lang string, rows ...[]string) {
	p.w.t.Helper()
	cat := p.w.h.cat
	want := &tg.Menu{IsPersistent: true, Resize: true, Placeholder: cat.T(lang, "menu.placeholder")}
	for _, keys := range rows {
		var row []tg.MenuButton
		for _, k := range keys {
			row = append(row, tg.MenuButton{Text: cat.T(lang, k), Style: menuStyles[k]})
		}
		want.Row(row...)
	}
	if got := p.lastMenu(); !reflect.DeepEqual(got, want) {
		p.w.t.Fatalf("user %d: bottom menu\n got %+v\nwant %+v", p.id, got, want)
	}
}

// menuRows are the bottom menu's rows: the ones everybody has, then more.
func menuRows(more ...[]string) [][]string {
	return append([][]string{{"btn.buy"}, {"btn.services", "btn.wallet"}, {"btn.trial", "btn.support"}}, more...)
}

// offers reports whether a message has a button whose callback data or link
// starts with prefix.
func offers(m *msg, prefix string) bool {
	for _, b := range buttons(m) {
		if strings.HasPrefix(b.CallbackData, prefix) || b.URL != "" && strings.HasPrefix(b.URL, prefix) {
			return true
		}
	}
	return false
}

func texts(ms []*msg) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.text)
	}
	return out
}

// restart is a new bot process: the same core, Telegram and Redis (the
// state store), nothing else kept, and the settings not loaded yet (the
// notifier keeps the old ones).
func (w *world) restart() {
	w.h = New(w.stack.Core, w.tg, w.h.state, i18n.MustLoad(), w.h.limiter, w.h.cfg, nil)
}

// --- tests ---

// The bottom menu comes with the welcome, in the user's language, with the
// buttons of their role; /menu sends it again.
func TestBottomMenuPerRoleAndLanguage(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	cat := w.h.cat

	en := w.customer(2001, "menu_en")
	if ms := en.menus(); len(ms) != 1 || ms[0].text != cat.T("en", "welcome", "brand", "TestVPN") {
		t.Fatalf("the welcome brings the menu: %v", texts(ms))
	}
	en.hasMenu("en", menuRows([]string{"btn.language"})...)
	fa := w.person(2002, "menu_fa")
	fa.text("/start")
	fa.press("lang:fa")
	fa.hasMenu("fa", menuRows([]string{"btn.language"})...)

	owner.text("/set referral.reward_percent 10") // Invite friends, for all but resellers
	owner.sees("referral.reward_percent")
	en.text("/menu")
	en.hasMenu("en", menuRows([]string{"btn.invite", "btn.language"})...)
	if text := en.menus()[len(en.menus())-1].text; text != cat.T("en", "menu.keyboard") || !strings.Contains(en.last().text, "Main menu") {
		t.Fatalf("/menu: %q, then %q", text, en.last().text)
	}
	w.makeReseller(2001, 0, 10, 0)
	en.text("/menu")
	en.hasMenu("en", menuRows([]string{"btn.reseller", "btn.language"})...)
	w.role(2002, "support")
	fa.text("/menu")
	fa.hasMenu("fa", menuRows([]string{"btn.invite", "btn.language"}, []string{"btn.dashboard"})...)
	owner.text("/menu")
	owner.hasMenu("en", menuRows([]string{"btn.invite", "btn.language"}, []string{"btn.admin", "btn.dashboard"})...)
}

// Every button of the bottom menu does what the same inline button does, in
// both languages, for the roles that have it.
func TestMenuLabelsRoute(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	owner.text("/set referral.reward_percent 10")
	owner.sees("referral.reward_percent")
	fa := func(id int64, name string) *person {
		p := w.person(id, name)
		p.text("/start")
		p.press("lang:fa")
		return p
	}
	people := map[string]map[string]*person{ // language -> role -> who taps
		"en": {"customer": w.customer(2101, "route_en"), "reseller": w.customer(2102, "route_en_rs"), "staff": owner},
		"fa": {"customer": fa(2103, "route_fa"), "reseller": fa(2104, "route_fa_rs"), "staff": fa(2105, "route_fa_adm")},
	}
	w.makeReseller(2102, 0, 10, 0)
	w.makeReseller(2104, 0, 10, 0)
	w.role(2105, "admin")
	cat := w.h.cat
	for _, c := range []struct {
		key, role string
		// offers: what the answer offers (a button's callback data or a
		// link, by prefix); text: else the text key of the answer.
		offers, text string
	}{
		{"btn.buy", "customer", "plan:", ""},
		{"btn.services", "customer", "", "subs.empty"},
		{"btn.wallet", "customer", "wtop", ""},
		{"btn.trial", "customer", "", "trial.started"},
		{"btn.support", "customer", "tk:n:", ""},
		{"btn.language", "customer", "lang:fa", ""},
		{"btn.invite", "customer", "https://t.me/share/url", ""},
		{"btn.reseller", "reseller", "rs:buy", ""},
		{"btn.admin", "staff", "adm:pend", ""},
		{"btn.dashboard", "staff", "", "admin.dashboard_unavailable"},
	} {
		for _, lang := range i18n.Languages {
			p := people[lang][c.role]
			p.tapMenu(lang, c.key)
			got := p.last()
			if c.text != "" && got.text != cat.T(lang, c.text) || c.offers != "" && !offers(got, c.offers) {
				t.Errorf("%s %s: answered %q %v", lang, c.key, got.text, callbacks(got))
			}
		}
	}
}

// A keyboard from before a language switch or a text edit keeps working: the
// labels of every language, and the built-in ones, still route.
func TestOldMenuLabelsStillRoute(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	cat := w.h.cat
	u := w.customer(2111, "old")
	u.tapMenu("fa", "btn.buy") // the Persian keyboard, kept after a switch to English
	u.lastHas(cat.T("en", "plans.title"))

	owner.text("/set texts.en.btn.wallet 💰 My wallet")
	owner.sees("Saved")
	for _, label := range []string{"💰 My wallet", cat.Default("en", "btn.wallet"), cat.T("fa", "btn.wallet")} {
		u.text(label)
		if !hasCallback(u.last(), "wtop") {
			t.Fatalf("%q: answered %q", label, u.last().text)
		}
	}
	u.text(cat.T("en", "btn.home")) // an inline button's label typed: just text
	u.lastHas("Main menu")
}

// The labels are known from the start (a tap is never taken for an answer,
// even before the settings load), the current texts before the built-in
// ones, and other text is not a tap.
func TestMenuLabelsAtStartAndAfterEdits(t *testing.T) {
	cat := i18n.MustLoad()
	h := New(nil, newFakeTG(), state.NewMemory(), cat, nil, Config{}, nil)
	want := map[string]string{"btn.buy": "buy", "btn.services": "subs", "btn.wallet": "wallet", "btn.trial": "trial",
		"btn.support": "support", "btn.language": "lang", "btn.invite": "ref", "btn.reseller": "rs",
		"btn.admin": "adm", "btn.dashboard": "dash"}
	for _, lang := range i18n.Languages {
		for key, data := range want {
			if got, ok := h.menuAction(cat.T(lang, key)); !ok || got != data {
				t.Errorf("%s %s: %q %v", lang, key, got, ok)
			}
		}
	}
	for _, text := range []string{"", "hello", cat.T("en", "btn.home"), cat.T("fa", "btn.cancel")} {
		if got, ok := h.menuAction(text); ok {
			t.Errorf("%q taken for %q", text, got)
		}
	}
	// Buy renamed, and its old label given to My services: that label is
	// My services now.
	cat.SetOverrides(map[string]string{"texts.en.btn.buy": "🛍 Shop", "texts.en.btn.services": cat.Default("en", "btn.buy")})
	h.loadMenuLabels()
	if got, _ := h.menuAction(cat.Default("en", "btn.buy")); got != "subs" {
		t.Errorf("a built-in label another button now has: %q", got)
	}
	if got, _ := h.menuAction("🛍 Shop"); got != "buy" {
		t.Errorf("the new label: %q", got)
	}
	if got, _ := h.menuAction(cat.Default("en", "btn.services")); got != "subs" {
		t.Errorf("the old label of My services: %q", got)
	}
}

// A label stays known while the bot runs, though its text was edited again
// or reset since (a keyboard sent with it may still be shown), unless a
// button has that text now.
func TestEditedLabelsStayKnown(t *testing.T) {
	cat := i18n.MustLoad()
	h := New(nil, newFakeTG(), state.NewMemory(), cat, nil, Config{}, nil)
	edit := func(overrides map[string]string) {
		cat.SetOverrides(overrides)
		h.loadMenuLabels()
	}
	edit(map[string]string{"texts.en.btn.wallet": "💰 My wallet"})
	edit(map[string]string{"texts.en.btn.wallet": "👛 Purse"})
	edit(nil)
	for _, label := range []string{"💰 My wallet", "👛 Purse", cat.Default("en", "btn.wallet")} {
		if got, ok := h.menuAction(label); !ok || got != "wallet" {
			t.Errorf("%q: %q %v", label, got, ok)
		}
	}
	edit(map[string]string{"texts.en.btn.services": "💰 My wallet"})
	if got, _ := h.menuAction("💰 My wallet"); got != "subs" {
		t.Errorf("an old label My services has now: %q", got)
	}
}

// A tap on the bottom menu is never taken for what a question asked: during
// a card payment Buy shows the plans and closes the question (the label is
// not the bank reference), and so it does before the settings ever loaded.
func TestMenuTapLeavesTheQuestion(t *testing.T) {
	w := newWorld(t)
	cat := w.h.cat
	early := w.customer(2201, "early") // nothing has loaded the settings yet
	early.press("wallet")
	early.press("wtop")
	early.press("wcustom")
	early.tapMenu("en", "btn.buy")
	early.lastHas(cat.T("en", "plans.empty"))
	if st, _ := w.h.state.Get(context.Background(), 2201); st.Scene != "" {
		t.Fatalf("the amount question is still open: %+v", st)
	}

	owner := setupStore(t, w)
	u := w.customer(2202, "scene")
	u.press("buy")
	u.press("plan:")
	u.press("pay:c:")
	u.photo("receipt-scene", "") // the bank reference is asked
	u.lastHas("bank tracking/reference number")
	u.tapMenu("en", "btn.buy")
	u.lastHas(cat.T("en", "plans.title"))
	if st, _ := w.h.state.Get(context.Background(), 2202); st.Scene != "" {
		t.Fatalf("the reference question is still open: %+v", st)
	}
	u.text("998877") // no question now: just text
	u.lastHas("Main menu")
	if owner.count("New payment to review") != 0 {
		t.Fatal("a label or the text after it was taken for the bank reference")
	}
}

// What a button answers with a popup comes as a message after a tap on the
// bottom menu, which has no popup; a button's popup stays a popup.
func TestMenuTapsGetPopupsAsMessages(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	cat := w.h.cat
	nu := w.person(2301, "pop")
	nu.text("/start")
	nu.press("lang:en")
	if got := w.tg.all(2301); len(got) != 3 {
		t.Fatalf("registering: %v", texts(got)) // the picker, the welcome with the menu, the home
	}
	nu.tapMenu("en", "btn.trial")
	nu.lastHas(cat.T("en", "trial.started"))
	nu.tapMenu("en", "btn.trial")
	nu.lastHas(cat.T("en", "trial.used"))
	nu.tapMenu("en", "btn.admin") // typed, or an old staff keyboard
	nu.lastHas(cat.T("en", "admin.forbidden"))
	nu.tapMenu("en", "btn.dashboard")
	nu.lastHas(cat.T("en", "admin.forbidden"))
	shown := len(w.tg.all(2301))
	nu.press("trial") // the inline button
	nu.toast(cat.T("en", "trial.used"))
	if n := len(w.tg.all(2301)); n != shown {
		t.Fatalf("a button's popup also came as a message: %d messages, want %d", n, shown)
	}

	owner.text("/trial off")
	other := w.customer(2302, "pop2")
	other.tapMenu("en", "btn.trial")
	other.lastHas(cat.T("en", "trial.unavailable"))
}

// A payment under way passes the gates, a tap on the bottom menu never rides
// on it; going on with a ticket lets Support through, nothing else.
func TestMenuTapsDoNotPassTheGates(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	ctx := context.Background()
	paying := w.customer(2401, "gate_m")
	ticket := w.customer(2402, "gate_t")
	ref := w.customer(2403, "gate_j")
	paying.press("buy")
	paying.press("plan:")
	paying.press("pay:c:") // a receipt is awaited
	ticket.text("/menu")
	ticket.press("support")
	ticket.text("help me please")
	ref.press("buy")
	ref.press("plan:")
	ref.press("pay:c:")
	ref.photo("receipt-gate", "") // its bank reference is awaited

	owner.text("/set maintenance.enabled true")
	owner.sees("Saved")
	paying.tapMenu("en", "btn.trial")
	paying.lastHas("under maintenance")
	if subs, err := w.stack.Store.ListSubscriptions(ctx, w.stack.Store.Conn(), w.storeUser(2401).ID, 10); err != nil || len(subs) != 0 {
		t.Fatalf("a trial started during maintenance: %d %v", len(subs), err)
	}
	paying.photo("receipt-gate-m", "REF-1") // the payment itself still goes through
	paying.lastHas("Received")
	ticket.tapMenu("en", "btn.support")
	ticket.lastHas("help me please") // the ticket, not the maintenance answer
	ticket.tapMenu("en", "btn.buy")
	ticket.lastHas("under maintenance")

	owner.text("/set maintenance.enabled false")
	owner.text("/set join.channel @testchan")
	owner.sees("Saved")
	alerts := owner.count("New payment to review")
	ref.tapMenu("en", "btn.buy")
	ref.lastHas("Join our channel")
	if owner.count("New payment to review") != alerts {
		t.Fatal("the label was taken for the bank reference")
	}
	ref.text("554433")
	ref.lastHas("Received")
}

// The bottom menu reaches every user, the ones from before it existed too,
// ahead of the answer to their next update; again when it changes (a role,
// an edited label, the language); never with every update, and not while a
// gate holds the user back.
func TestBottomMenuReachesEveryone(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	ctx := context.Background()
	cat := w.h.cat
	u := w.customer(2501, "sig")
	u.press("buy")
	u.text("hello")
	if n := len(u.menus()); n != 1 {
		t.Fatalf("%d menus: sent again with nothing changed", n)
	}
	forget := func() { // as for a user from before the bottom menu
		if err := w.h.state.SaveMenu(ctx, u.id, state.SentMenu{}); err != nil {
			t.Fatal(err)
		}
	}

	forget()
	before := len(w.tg.all(u.id))
	u.text("hello")
	got := w.tg.all(u.id)[before:]
	if len(got) != 2 || got[0].menu == nil || got[0].text != cat.T("en", "menu.keyboard") || !strings.Contains(got[1].text, "Main menu") {
		t.Fatalf("a message: %v", texts(got))
	}
	// A tap: the answer comes below the menu as a new message; the tapped
	// menu stays as it was.
	forget()
	home := u.last()
	before = len(w.tg.all(u.id))
	u.press("wallet")
	got = w.tg.all(u.id)[before:]
	if len(got) != 2 || got[0].menu == nil || !hasCallback(got[1], "wtop") || !strings.Contains(home.text, "Main menu") {
		t.Fatalf("a tap: %v; the tapped menu now %q", texts(got), home.text)
	}

	w.makeReseller(u.id, 0, 10, 0)
	u.text("hello")
	u.hasMenu("en", menuRows([]string{"btn.reseller", "btn.language"})...)
	owner.text("/set texts.en.btn.wallet 💰 My wallet")
	owner.sees("Saved")
	n := len(u.menus())
	u.press("subs")
	if len(u.menus()) != n+1 || u.lastMenu().Keyboard[1][1].Text != "💰 My wallet" {
		t.Fatalf("after the edit: %d menus, %+v", len(u.menus()), u.lastMenu())
	}
	// /menu and a language change send one menu, even when none is known.
	forget()
	n = len(u.menus())
	u.text("/menu")
	if len(u.menus()) != n+1 {
		t.Fatalf("/menu sent %d menus", len(u.menus())-n)
	}
	u.tapMenu("en", "btn.language")
	forget()
	u.press("lang:fa")
	u.toast(cat.T("fa", "lang.changed"))
	u.hasMenu("fa", menuRows([]string{"btn.reseller", "btn.language"})...)
	if len(u.menus()) != n+2 {
		t.Fatalf("the language change sent %d menus", len(u.menus())-n-1)
	}
	n = len(u.menus())
	u.text("سلام")
	if len(u.menus()) != n {
		t.Fatal("the menu was sent again after the language change")
	}

	forget()
	owner.text("/set maintenance.enabled true")
	owner.sees("Saved")
	u.text("/paysupport") // let through, the user still held back
	if len(u.menus()) != n {
		t.Fatal("a user held back by maintenance got the menu")
	}
	owner.text("/set maintenance.enabled false")
	u.text("سلام")
	if len(u.menus()) != n+1 {
		t.Fatal("no menu once let in")
	}
}

// A label on the user's keyboard is a tap, never the answer to a question,
// however often its text was edited since: while the bot runs it knows every
// label it had, and after a restart the menu each user was sent says what
// its labels do, at the gates too.
func TestOldLabelOnTheKeyboardIsATap(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	owner.text("/set texts.en.btn.wallet 💰 My wallet")
	owner.sees("Saved")
	asked := func(id int64, name string) *person { // the edited label on the keyboard, a bank reference asked
		p := w.customer(id, name)
		p.press("buy")
		p.press("plan:")
		p.press("pay:c:")
		p.photo("receipt-"+name, "")
		p.lastHas("bank tracking/reference number")
		return p
	}
	u, v := asked(2701, "label_run"), asked(2702, "label_restart")
	isTap := func(p *person) {
		t.Helper()
		if !hasCallback(p.last(), "wtop") || owner.count("New payment to review") != 0 {
			t.Fatalf("user %d: answered %q, %d payments to review", p.id, p.last().text, owner.count("New payment to review"))
		}
		if st, _ := w.h.state.Get(context.Background(), p.id); st.Scene != "" {
			t.Fatalf("user %d: the question is still open: %+v", p.id, st)
		}
	}

	owner.text("/set texts.en.btn.wallet 👛 Purse")
	owner.sees("Saved")
	u.text("💰 My wallet")
	isTap(u)

	owner.text("/set texts.en.btn.wallet") // back to the built-in label
	owner.sees("Saved")
	w.restart()
	if err := w.h.RefreshSettings(context.Background()); err != nil {
		t.Fatal(err)
	}
	owner.text("/set maintenance.enabled true")
	owner.sees("Saved")
	v.text("💰 My wallet") // not the payment under way
	v.lastHas("under maintenance")
	owner.text("/set maintenance.enabled false")
	v.text("💰 My wallet")
	isTap(v)
}

// During maintenance Support goes on with the open ticket; with none open
// (support closed it, though the scene still names it) the tap gets the
// maintenance answer, not the prompt to start a ticket.
func TestGatedSupportTapNeedsAnOpenTicket(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	u := w.customer(2711, "gated_support")
	u.text("/menu")
	u.press("support")
	u.text("my first message")
	owner.text("/set maintenance.enabled true")
	owner.sees("Saved")
	u.tapMenu("en", "btn.support")
	u.lastHas("my first message")
	if _, err := w.dom().SetTicketStatus(context.Background(), w.storeUser(ownerID), w.openTicket(2711).ID, "closed", ""); err != nil {
		t.Fatal(err)
	}
	u.tapMenu("en", "btn.support")
	u.lastHas("under maintenance")
}

// Support pressed when the bottom menu is sent ahead of the answer answers
// below it, as the other buttons do: the menu pressed stays as it was.
func TestSupportAnswersBelowAFreshMenu(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	u := w.customer(2721, "support_keep")
	home := u.last()
	if err := w.h.state.SaveMenu(context.Background(), u.id, state.SentMenu{}); err != nil { // as for a user from before the bottom menu
		t.Fatal(err)
	}
	before := len(w.tg.all(u.id))
	u.press("support")
	got := w.tg.all(u.id)[before:]
	if len(got) != 2 || got[0].menu == nil || !offers(got[1], "tk:n:") || !strings.Contains(home.text, "Main menu") {
		t.Fatalf("Support: %v; the menu pressed now %q", texts(got), home.text)
	}
}

// A restarted bot sends no bottom menu ahead of answers before its settings
// load: built without the edited labels and the switches, it would not be
// the user's, and the right one would follow once they load.
func TestNoMenuRefreshBeforeTheSettingsLoad(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	owner.text("/set referral.reward_percent 10")
	owner.text("/set texts.en.btn.wallet 💰 My wallet")
	owner.sees("Saved")
	u := w.customer(2731, "restarted")
	n := len(u.menus())
	w.restart() // and core cannot be reached for its first refresh
	u.text("hello")
	u.lastHas("Main menu")
	if err := w.h.RefreshSettings(context.Background()); err != nil {
		t.Fatal(err)
	}
	u.text("hello")
	if len(u.menus()) != n {
		t.Fatalf("%d menus sent around the restart", len(u.menus())-n)
	}
	owner.text("/set texts.en.btn.wallet 👛 Purse") // a change still reaches the user
	owner.sees("Saved")
	u.text("hello")
	if len(u.menus()) != n+1 || u.lastMenu().Keyboard[1][1].Text != "👛 Purse" {
		t.Fatalf("after the edit: %d menus, %+v", len(u.menus())-n, u.lastMenu())
	}
}

// A newcomer the gates hold back still gets the welcome (the language picker
// becomes it), without the bottom menu, which comes once they are let in.
func TestHeldNewcomerGetsTheWelcome(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	welcome := w.h.cat.T("en", "welcome", "brand", "TestVPN")
	register := func(id int64, name string) *person {
		t.Helper()
		p := w.person(id, name)
		p.text("/start")
		picker := p.last()
		p.press("lang:en")
		if picker.text != welcome || len(p.menus()) != 0 {
			t.Fatalf("user %d: the picker became %q; %d menus", id, picker.text, len(p.menus()))
		}
		return p
	}
	owner.text("/set maintenance.enabled true")
	owner.sees("Saved")
	m := register(2741, "held_mnt")
	m.lastHas("under maintenance")
	owner.text("/set maintenance.enabled false")
	m.text("hello")
	if len(m.menus()) != 1 {
		t.Fatalf("%d menus once let in", len(m.menus()))
	}

	owner.text("/set join.channel @testchan")
	owner.sees("Saved")
	j := register(2742, "held_join")
	j.lastHas("Join our channel")
	w.tg.setMember(2742, "member")
	j.press("jn")
	j.text("hello")
	if len(j.menus()) != 1 {
		t.Fatalf("%d menus once a member", len(j.menus()))
	}
}

// styleOf is the color of a message's button whose callback data (or link)
// is data, else starts with it.
func styleOf(t *testing.T, m *msg, data string) string {
	t.Helper()
	for _, exact := range []bool{true, false} {
		for _, b := range buttons(m) {
			id := b.CallbackData
			if id == "" {
				id = b.URL
			}
			if exact && id == data || !exact && strings.HasPrefix(id, data) {
				return b.Style
			}
		}
	}
	t.Fatalf("no button %q: %v", data, callbacks(m))
	return ""
}

// Buttons are colored by what they do: green pays or confirms, blue opens a
// main action, red cancels or refuses; navigation and lists keep the app's
// look.
func TestButtonColors(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	owner.text("/set payments.usdt_trc20 " + trc20)
	owner.text("/set payments.usdt_rate 60000")
	u := w.customer(2601, "colors")
	colors := func(what string, m *msg, want map[string]string) {
		t.Helper()
		for data, style := range want {
			if got := styleOf(t, m, data); got != style {
				t.Errorf("%s, %s: %q, want %q", what, data, got, style)
			}
		}
	}
	colors("home", u.last(), map[string]string{"buy": tg.StyleSuccess, "subs": tg.StylePrimary, "wallet": tg.StylePrimary,
		"trial": tg.StyleSuccess, "support": tg.StylePrimary, "lang": ""})
	u.press("wallet")
	colors("wallet", u.shown(), map[string]string{"wtop": tg.StylePrimary, "whist": "", "home": ""})
	u.press("home")
	u.press("buy")
	colors("plans", u.shown(), map[string]string{"plan:": "", "home": ""})
	u.press("plan:")
	colors("checkout", u.shown(), map[string]string{"pay:w:": tg.StyleSuccess, "pay:z:": tg.StyleSuccess,
		"pay:c:": tg.StyleSuccess, "pay:x:": tg.StyleSuccess, "dc:": "", "buy": "", "home": ""})
	u.press("pay:c:")
	colors("card details", u.shown(), map[string]string{"cancel": tg.StyleDanger})
	u.photo("receipt-colors", "REF-5")

	owner.press("adm:pi:")
	item := owner.last()
	colors("review", item, map[string]string{"adm:ok:": tg.StyleSuccess, "adm:no:": tg.StyleDanger, "adm:pend": ""})
	owner.press("adm:no:")
	colors("reasons", item, map[string]string{"adm:nr:0:": tg.StyleDanger, "adm:nr:1:": tg.StyleDanger,
		"adm:nr:2:": tg.StyleDanger, "adm:nx:": tg.StyleDanger, "adm:pi:": ""})
}
