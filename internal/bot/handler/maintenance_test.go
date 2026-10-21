package handler

import (
	"cmp"
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/runner"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/state"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testenv"
)

// Maintenance mode, entry point by entry point (gates.go). While
// maintenance.enabled is on, every update of a customer is answered with the
// maintenance text, from whatever state the customer was in when it began, and
// only the allowlist (maintenancePasses) goes on. The tables below send each
// kind of update a customer has, and the last test reads the source and fails
// when a command, a button or a scene exists that the tables miss.

// --- a fake Telegram that also keeps whether a popup was an alert ---

type cbAnswer struct {
	user  int64
	text  string
	alert bool
}

// answerSpy records every callback answer with its alert flag (the fake
// Telegram keeps the text only).
type answerSpy struct {
	*fakeTG
	mu      sync.Mutex
	answers []cbAnswer
}

func (s *answerSpy) AnswerCallback(ctx context.Context, id, text string, alert bool) error {
	s.fakeTG.mu.Lock()
	user := s.cbFrom[id]
	s.fakeTG.mu.Unlock()
	s.mu.Lock()
	s.answers = append(s.answers, cbAnswer{user: user, text: text, alert: alert})
	s.mu.Unlock()
	return s.fakeTG.AnswerCallback(ctx, id, text, alert)
}

// of is the answers to one user's buttons, oldest first.
func (s *answerSpy) of(user int64) []cbAnswer {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []cbAnswer
	for _, a := range s.answers {
		if a.user == user {
			out = append(out, a)
		}
	}
	return out
}

// maintWorld is a store with a plan, a card, the crypto and Stars methods set
// up, its owner, and a Telegram that tells alerts from toasts.
type maintWorld struct {
	*world
	owner *person
	spy   *answerSpy
}

func newMaintWorld(t *testing.T) *maintWorld {
	t.Helper()
	return newMaintWorldOwners(t, ownerID)
}

func newMaintWorldOwners(t *testing.T, owners ...int64) *maintWorld {
	t.Helper()
	w := newWorldOwners(t, owners...)
	owner := setupStore(t, w)
	owner.text("/set payments.usdt_trc20 " + trc20)
	owner.text("/set payments.usdt_rate 60000")
	owner.text("/set payments.stars_rate 1500")
	owner.lastHas("Saved")
	spy := &answerSpy{fakeTG: w.tg}
	w.h.tg = spy
	return &maintWorld{world: w, owner: owner, spy: spy}
}

// maintenance switches maintenance mode on or off the way the owner does.
func (m *maintWorld) maintenance(on bool) {
	m.t.Helper()
	m.owner.text("/set maintenance.enabled " + strconv.FormatBool(on))
	m.owner.lastHas("Saved")
}

// register makes a customer who picked lang.
func (m *maintWorld) register(id int64, name, lang string) *person {
	m.t.Helper()
	p := m.person(id, name)
	p.text("/start")
	p.press("lang:" + lang)
	return p
}

// setScene puts a user in the middle of a scene, as a conversation that began
// before maintenance would have.
func (m *maintWorld) setScene(p *person, scene string, data map[string]string) {
	m.t.Helper()
	if err := m.h.state.Set(context.Background(), p.id, state.State{Scene: scene, Data: data}); err != nil {
		m.t.Fatal(err)
	}
}

func (m *maintWorld) clearScene(p *person) {
	m.t.Helper()
	if err := m.h.state.Clear(context.Background(), p.id); err != nil {
		m.t.Fatal(err)
	}
}

// startPayment takes a customer to a payment method's screen for the plan.
func (m *maintWorld) startPayment(p *person, method string) {
	m.t.Helper()
	p.press("buy")
	p.press("plan:")
	p.press(method)
}

// chatLine is one message of a chat, as the user sees it.
type chatLine struct {
	text    string
	buttons string
	menu    bool
}

func (m *maintWorld) chat(id int64) []chatLine {
	var out []chatLine
	for _, ms := range m.tg.all(id) {
		out = append(out, chatLine{text: ms.text, buttons: strings.Join(callbacks(ms), ","), menu: ms.menu != nil})
	}
	return out
}

// footprint is what a customer's update could have changed: the scene the
// conversation is in, orders, services, tickets, the wallet and the account.
func (m *maintWorld) footprint(p *person) string {
	m.t.Helper()
	ctx := context.Background()
	st, err := m.h.state.Get(ctx, p.id)
	if err != nil {
		m.t.Fatal(err)
	}
	var data []string
	for _, k := range slices.Sorted(maps.Keys(st.Data)) {
		data = append(data, k+"="+st.Data[k])
	}
	u := m.storeUser(p.id)
	orders, paid := m.orders(p.id)
	subs, err := m.stack.Store.ListSubscriptions(ctx, m.stack.Store.Conn(), u.ID, 100)
	if err != nil {
		m.t.Fatal(err)
	}
	var tickets, messages int
	if err := m.stack.Store.DB().QueryRow(ctx, `SELECT
		(SELECT count(*) FROM core.tickets WHERE user_id = $1),
		(SELECT count(*) FROM core.ticket_messages tm JOIN core.tickets t ON t.id = tm.ticket_id WHERE t.user_id = $1)`,
		u.ID).Scan(&tickets, &messages); err != nil {
		m.t.Fatal(err)
	}
	return fmt.Sprintf("scene %q %v; %d orders (%d paid), %d services, %d tickets with %d messages, wallet %d, language %s, role %s, status %s",
		st.Scene, data, orders, paid, len(subs), tickets, messages, m.balance(p.id), u.Language, u.Role, u.Status)
}

// --- the customer's updates ---

// gatedCase is one update a customer sends during maintenance, and the state
// they were in before it began.
type gatedCase struct {
	name string
	lang string // the customer's language (default en)
	role string // "reseller": a reseller (default a customer)
	prep func(m *maintWorld, p *person)
	send func(m *maintWorld, p *person)
	tap  bool // a button: answered with a popup, not a message
	// What the case covers, for the check that no entry point is missing.
	verb, cmd, scene string
}

func cmdCase(text string) gatedCase {
	cmd, _, _ := command(text)
	return gatedCase{name: fmt.Sprintf("command %q", text), cmd: cmd, send: func(_ *maintWorld, p *person) { p.text(text) }}
}

func buttonCase(data string) gatedCase {
	verb, _, _ := strings.Cut(data, ":")
	return gatedCase{name: "button " + data, tap: true, verb: verb, send: func(_ *maintWorld, p *person) { p.tap(data) }}
}

// menuCase is a customer who picked customerLang tapping the bottom menu's
// key as labelled in labelLang.
func menuCase(customerLang, labelLang, key string) gatedCase {
	return gatedCase{name: fmt.Sprintf("bottom menu %s labelled in %s for a customer in %s", key, labelLang, customerLang), lang: customerLang,
		send: func(_ *maintWorld, p *person) { p.tapMenu(labelLang, key) }}
}

// sceneCase is a message sent in the middle of a scene that a conversation
// left open from before maintenance.
func sceneCase(scene string, data map[string]string, role, what string, send func(p *person)) gatedCase {
	return gatedCase{name: what + " in the " + scene + " scene", scene: scene, role: role,
		prep: func(m *maintWorld, p *person) { m.setScene(p, scene, data) },
		send: func(_ *maintWorld, p *person) { send(p) }}
}

func textIn(s string) func(p *person) { return func(p *person) { p.text(s) } }

// openTicket has the customer write to support before maintenance.
func openTicket(m *maintWorld, p *person) {
	p.text("/menu")
	p.press("support")
	p.text("my first message")
}

// answeredTicket is a ticket staff answered: the customer holds the answer,
// with its Reply and Close buttons.
func answeredTicket(m *maintWorld, p *person) {
	openTicket(m, p)
	m.staffReply(p.id, "the staff answer", false)
	p.eventuallySees("the staff answer")
}

// answeredOutsideTheScene is an answered ticket whose conversation scene is
// gone (the customer did something else, or the bot forgot it): a Telegram
// reply to the answer is what continues it.
func answeredOutsideTheScene(m *maintWorld, p *person) {
	answeredTicket(m, p)
	m.clearScene(p)
}

// ticketID is the id of the customer's open ticket ("gone" when they have
// none: a button for it is then as stale as a crafted one).
func ticketID(m *maintWorld, p *person) string {
	t, err := m.stack.Store.OpenTicketOf(context.Background(), m.stack.Store.Conn(), m.storeUser(p.id).ID)
	if err != nil {
		return "gone"
	}
	return t.ID
}

// staffAnswer is the newest message the staff answered with.
func staffAnswer(p *person) *msg {
	ms := p.w.tg.all(p.id)
	for i := len(ms) - 1; i >= 0; i-- {
		if strings.Contains(ms[i].text, "the staff answer") {
			return ms[i]
		}
	}
	p.w.t.Fatalf("user %d was sent no staff answer", p.id)
	return nil
}

// A payment under way: where the card, the crypto address or the bank
// reference question is on screen.
func receiptAsked(m *maintWorld, p *person)   { m.startPayment(p, "pay:c:") }
func referenceAsked(m *maintWorld, p *person) { receiptAsked(m, p); p.photo("receipt-held", "") }
func txidAsked(m *maintWorld, p *person)      { m.startPayment(p, "pay:x:") }

func gatedCases() []gatedCase {
	var cases []gatedCase
	add := func(more ...gatedCase) { cases = append(cases, more...) }

	// Slash commands, with and without arguments; the staff ones too (a
	// customer cannot use them, but must hear about maintenance, not "for
	// admins only"); and ones the bot does not know.
	for _, text := range []string{
		"/start", "/start r_abc123", "/start@test_vpn_bot", "/START", " /menu", "/menu", "/buy", "/wallet", "/support", "/trial",
		"/new", "/renew", "/status",
		"/cancel", "/terms", "/dashboard", "/admin", "/help", "/nonsense",
		"/set maintenance.enabled false", "/plan_add 1 IRT 1 1 Free", "/topup_add 1 IRT 1 +1GB", "/trial 1 1",
		"/discount_add FREE 100% 1 30", "/discount_off FREE", "/discounts",
	} {
		add(cmdCase(text))
	}

	// Every item of the bottom menu: a customer taps the label in their
	// language, and a keyboard sent before a language change has the other's.
	for _, it := range menuItems {
		add(menuCase("en", "en", it.key), menuCase("fa", "fa", it.key), menuCase("en", "fa", it.key))
	}

	// Every verb of the buttons, and the ones a stale or crafted button has.
	for _, data := range []string{
		"lang", "lang:fa", "lang:en", "home", "cancel",
		"buy", "pg:g1", "pg:-", "plan:p1", "pay:c:n1", "pay:w:p1:n1", "subs", "sub:s1", "rnw:s1", "tup:s1", "rp:n1:p1", "dc:n1", "ref", "qr:s1",
		"wallet", "wtop", "wamt:100000", "wpay:c:100000:n1", "wcustom", "whist", "gift", "trial", "store",
		"top", "top:2", "ap", "renew",
		"faq", "faq:p:2", "faq:q:1:f1", "tut", "tut:p:2", "tut:v:1:t1", // the FAQ under Support and the tutorials (help.go)
		"support", "support:m", "tk:n:payment", "tk:n:other", "tk:r:t1", "tk:c:t1", "tk:l", "tk:v:t1",
		"dash", "adm", "adm:pend", "adm:find", "rs", "rs:buy", "rs:subs:1", "rs:st:1", "rs:link", "rs:p:p1",
		"jn", "nope", "x",
	} {
		add(buttonCase(data))
	}
	// The plan table's header, and every step of a bulk purchase: its plans, a
	// plan, a quantity, another number, the total, the name, no name, Pay.
	for _, data := range []string{"nop", "bk", "bk:p:p1", "bk:q:n1:3", "bk:o:n1", "bk:c:n1", "bk:n:n1", "bk:nc:n1", "bk:y:n1"} {
		add(buttonCase(data))
	}
	// The owner's own bot sells BOBRES licenses: the home, an offer, Pay, my licenses, a key, a renewal, and the USDT
	// top-up for a shortfall.
	for _, data := range []string{"sup", "sup:p:abcdefghij", "sup:y:abcdefghij:n1", "sup:my", "sup:o:00000000-0000-0000-0000-000000000000"} {
		add(buttonCase(data))
	}
	for _, data := range []string{"lic", "lic:b:standard:1", "lic:y:standard:1:n1", "lic:my", "lic:k:l1", "lic:r:l1:1", "lic:ry:l1:1:n1", "lpay:u:4000000:n1", "lpay:x:4000000:n1"} {
		add(buttonCase(data))
	}
	// Resellers are held like customers, in their panel too.
	for _, data := range []string{"rs", "rs:buy", "rs:subs:1", "rs:cu:1", "rs:st:1", "rs:link", "rs:pz:s1", "rs:dy:s1"} {
		c := buttonCase(data)
		c.name, c.role = "a reseller's "+c.name, "reseller"
		add(c)
	}
	rsTap := menuCase("en", "en", "btn.reseller")
	rsTap.name, rsTap.role = "a reseller's "+rsTap.name, "reseller"
	add(rsTap)
	// And a Persian customer gets the Persian text.
	faButton, faCommand := buttonCase("buy"), cmdCase("/menu")
	faButton.name, faButton.lang = "a persian customer's "+faButton.name, "fa"
	faCommand.name, faCommand.lang = "a persian customer's "+faCommand.name, "fa"
	add(faButton, faCommand)

	// Free text and files, with nothing open.
	plain := func(name string, send func(m *maintWorld, p *person)) gatedCase {
		return gatedCase{name: name, send: send}
	}
	add(
		plain("text", func(_ *maintWorld, p *person) { p.text("hello") }),
		plain("persian text", func(_ *maintWorld, p *person) { p.text("سلام") }),
		plain("a number", func(_ *maintWorld, p *person) { p.text("50000") }),
		plain("only spaces", func(_ *maintWorld, p *person) { p.text("   ") }),
		plain("a photo", func(_ *maintWorld, p *person) { p.photo("shot-1", "") }),
		plain("a photo with a caption", func(_ *maintWorld, p *person) { p.photo("shot-2", "look at this") }),
		plain("an image file", func(_ *maintWorld, p *person) { p.document("doc-1", "image/png", "") }),
		plain("a pdf", func(_ *maintWorld, p *person) { p.document("doc-2", "application/pdf", "invoice") }),
		plain("another file", func(_ *maintWorld, p *person) { p.file("doc-3", "application/zip", 100) }),
		plain("a voice message", func(_ *maintWorld, p *person) { p.voice() }),
		plain("a sticker", func(_ *maintWorld, p *person) { p.send(&tg.Message{Sticker: &tg.Media{FileID: "sticker-1"}}) }),
		plain("a video", func(_ *maintWorld, p *person) { p.send(&tg.Message{Video: &tg.Media{FileID: "video-1"}}) }),
		plain("a message of a kind the bot does not read", func(_ *maintWorld, p *person) { p.send(&tg.Message{}) }),
		plain("a Telegram reply to the bot's menu", func(_ *maintWorld, p *person) { p.replyTo("hello?", p.last().id) }),
		plain("an album of three photos, answered once", func(m *maintWorld, p *person) {
			p.album(fmt.Sprintf("album-%d", m.updateID()), "a-1", "a-2", "a-3")
		}),
	)

	// A question left open from before: the next message would answer it,
	// had maintenance not come in between.
	add(
		sceneCase(sceneTopupAmount, nil, "", "an amount", textIn("50000")),
		sceneCase(sceneGiftCode, nil, "", "a gift code", textIn("SPRING-AAAA-BBBB-CCCC")),
		sceneCase(sceneDiscount, map[string]string{"co": "n1"}, "", "a discount code", textIn("SPRING20")),
		sceneCase(sceneTicket, nil, "", "a first message to support", textIn("my vpn is slow")),
		sceneCase(sceneTicket, nil, "", "a photo for support", func(p *person) { p.photo("shot-3", "see") }),
		sceneCase(sceneResellerApply, nil, "", "a reseller application", textIn("I run a shop and want to resell")),
		sceneCase(sceneAdminFind, nil, "", "a search", textIn("@mallory")),
		sceneCase(sceneAdminAdjust, nil, "", "an adjustment", textIn("1000 gift")),
		sceneCase(sceneAdminReason, nil, "", "a reason", textIn("bad receipt")),
		sceneCase(sceneAdminInput, map[string]string{"_w": "plan", "_i": "0"}, "", "a wizard's answer", textIn("150000")),
		sceneCase(sceneResellerLabel, map[string]string{"co": "n1"}, "reseller", "a name", textIn("Ali")),
		sceneCase(sceneResellerFind, nil, "reseller", "a search", textIn("Ali")),
		sceneCase(sceneResellerRename, map[string]string{"id": "s1"}, "reseller", "a new name", textIn("Ali")),
		sceneCase(sceneResellerNote, map[string]string{"id": "s1"}, "reseller", "a note", textIn("pays late")),
	)
	// A bulk purchase's open questions: how many, and what to call them.
	add(
		sceneCase(sceneBulkQuantity, map[string]string{"co": "n1"}, "", "a number of services", textIn("5")),
		sceneCase(sceneBulkName, map[string]string{"co": "n1"}, "", "a name for the services", textIn("Family")),
		sceneCase(sceneBulkQuantity, map[string]string{"co": "n1"}, "reseller", "a reseller's number of services", textIn("5")),
	)

	// A payment's proof is what goes on (TestMaintenanceAllowlist); whatever
	// else is said in the middle of the payment is held.
	for _, ask := range []struct {
		scene string
		prep  func(m *maintWorld, p *person)
	}{{sceneReceipt, receiptAsked}, {sceneReference, referenceAsked}, {sceneTXID, txidAsked}} {
		for _, other := range []gatedCase{
			{name: "a tap on the bottom menu", send: func(_ *maintWorld, p *person) { p.tapMenu("en", "btn.buy") }},
			{name: "the trial in the bottom menu", send: func(_ *maintWorld, p *person) { p.tapMenu("en", "btn.trial") }},
			{name: "the command /menu", send: func(_ *maintWorld, p *person) { p.text("/menu") }},
			{name: "the command /cancel", send: func(_ *maintWorld, p *person) { p.text("/cancel") }},
			{name: "the Cancel button", tap: true, send: func(_ *maintWorld, p *person) { p.tap("cancel") }},
			{name: "the Home button", tap: true, send: func(_ *maintWorld, p *person) { p.tap("home") }},
		} {
			c := other
			c.name, c.scene, c.prep = c.name+" in the "+ask.scene+" question", ask.scene, ask.prep
			add(c)
		}
	}

	// Support. What once passed maintenance as "going on with a ticket" is
	// held like the rest: the bottom menu's Support (the report), its button,
	// the messages and files of the conversation, the ticket's buttons and a
	// Telegram reply to a staff answer.
	conversation := func(name string, send func(m *maintWorld, p *person)) gatedCase {
		return gatedCase{name: name + " in a support conversation", prep: openTicket, send: send}
	}
	conversationButton := func(name string, send func(m *maintWorld, p *person)) gatedCase {
		c := conversation(name, send)
		c.tap = true
		return c
	}
	answered := func(name string, tap bool, send func(m *maintWorld, p *person)) gatedCase {
		return gatedCase{name: name + " under a staff answer", prep: answeredTicket, tap: tap, send: send}
	}
	outside := func(name string, send func(m *maintWorld, p *person)) gatedCase {
		return gatedCase{name: name + " to a staff answer, out of the conversation", prep: answeredOutsideTheScene, send: send}
	}
	staffTarget := func(p *person) *tg.Message {
		return &tg.Message{MessageID: staffAnswer(p).id, From: &tg.User{ID: 1000, IsBot: true}, Chat: tg.Chat{ID: p.id, Type: "private"}}
	}
	add(
		gatedCase{name: "Support in the bottom menu with an open ticket", prep: openTicket,
			send: func(_ *maintWorld, p *person) { p.tapMenu("en", "btn.support") }},
		gatedCase{name: "Support in the bottom menu, in persian, with an open ticket", lang: "fa", prep: openTicket,
			send: func(_ *maintWorld, p *person) { p.tapMenu("fa", "btn.support") }},
		gatedCase{name: "Support in the bottom menu with no ticket",
			send: func(_ *maintWorld, p *person) { p.tapMenu("en", "btn.support") }},
		gatedCase{name: "the Support button with an open ticket", prep: openTicket, tap: true,
			send: func(_ *maintWorld, p *person) { p.tap("support") }},
		gatedCase{name: "the Support button under a notification", prep: openTicket, tap: true,
			send: func(_ *maintWorld, p *person) { p.tap("support:m") }},
		conversation("a message", func(_ *maintWorld, p *person) { p.text("any news?") }),
		conversation("a photo", func(_ *maintWorld, p *person) { p.photo("shot-4", "the error") }),
		conversation("an image file", func(_ *maintWorld, p *person) { p.document("doc-4", "image/jpeg", "") }),
		conversation("a pdf", func(_ *maintWorld, p *person) { p.document("doc-5", "application/pdf", "the bill") }),
		conversation("a voice message", func(_ *maintWorld, p *person) { p.voice() }),
		conversation("an album of three photos, answered once", func(m *maintWorld, p *person) {
			p.album(fmt.Sprintf("album-%d", m.updateID()), "t-1", "t-2", "t-3")
		}),
		conversationButton("the Write more button", func(m *maintWorld, p *person) { p.tap("tk:r:" + ticketID(m, p)) }),
		conversationButton("the Close button", func(m *maintWorld, p *person) { p.tap("tk:c:" + ticketID(m, p)) }),
		conversationButton("the history button", func(_ *maintWorld, p *person) { p.tap("tk:l") }),
		conversationButton("a past ticket's button", func(m *maintWorld, p *person) { p.tap("tk:v:" + ticketID(m, p)) }),
		answered("the Reply button", true, func(m *maintWorld, p *person) { p.tap("tk:r:" + ticketID(m, p)) }),
		answered("the Close button", true, func(m *maintWorld, p *person) { p.tap("tk:c:" + ticketID(m, p)) }),
		answered("a message", false, func(_ *maintWorld, p *person) { p.text("thanks") }),
		outside("a Telegram reply", func(_ *maintWorld, p *person) { p.replyTo("thanks, that works", staffAnswer(p).id) }),
		outside("a Telegram reply with a photo", func(_ *maintWorld, p *person) {
			p.send(&tg.Message{Photo: []tg.PhotoSize{{FileID: "reply-shot", Width: 800, Height: 600, FileSize: 50_000}}, ReplyToMessage: staffTarget(p)})
		}),
		outside("a Telegram reply with a file", func(_ *maintWorld, p *person) {
			p.send(&tg.Message{Document: &tg.Document{FileID: "reply-doc", MimeType: "application/pdf", FileSize: 100}, ReplyToMessage: staffTarget(p)})
		}),
	)
	return cases
}

// The report: in maintenance mode some of the bot's options (Support among
// them) did not show the maintenance message. Whatever a customer sends, from
// whatever they were doing, gets the message (a popup for a button, a message
// for the rest) every time, and nothing else happens.
func TestMaintenanceAnswersEveryCustomerUpdate(t *testing.T) {
	m := newMaintWorld(t)
	cases := gatedCases()
	people := make([]*person, len(cases))
	for i, c := range cases {
		p := m.register(int64(6000+i), fmt.Sprintf("held%d", i), cmp.Or(c.lang, "en"))
		if c.role == "reseller" {
			m.makeReseller(p.id, 0, 10, 100_000)
		}
		if c.prep != nil {
			c.prep(m, p)
		}
		people[i] = p
	}
	before := make([]string, len(cases))
	for i := range cases {
		before[i] = m.footprint(people[i])
	}
	m.maintenance(true)

	for i, c := range cases {
		p, want := people[i], m.h.cat.T(cmp.Or(c.lang, "en"), "maintenance")
		// Twice, at once: the second must not be passed over because the
		// first was answered a moment ago.
		for round := 1; round <= 2; round++ {
			chatBefore, answersBefore := m.chat(p.id), len(m.spy.of(p.id))
			c.send(m, p)
			chatAfter, answers := m.chat(p.id), m.spy.of(p.id)[answersBefore:]
			if len(chatAfter) < len(chatBefore) || !slices.Equal(chatAfter[:len(chatBefore)], chatBefore) {
				t.Errorf("%s (send %d): an earlier message changed", c.name, round)
				continue
			}
			sent := chatAfter[len(chatBefore):]
			if c.tap {
				if len(sent) != 0 {
					t.Errorf("%s (send %d): the button also sent %d messages, the first %q", c.name, round, len(sent), sent[0].text)
				}
				if len(answers) != 1 || answers[0].text != want || !answers[0].alert {
					t.Errorf("%s (send %d): answered %+v, want one alert %q", c.name, round, answers, want)
				}
				continue
			}
			if len(sent) != 1 || sent[0] != (chatLine{text: want}) {
				t.Errorf("%s (send %d): got %d messages %+v, want just %q", c.name, round, len(sent), sent, want)
			}
		}
		if after := m.footprint(p); after != before[i] {
			t.Errorf("%s: the updates changed something\n before: %s\n  after: %s", c.name, before[i], after)
		}
	}
	if !m.h.set.Bool("maintenance.enabled", false) {
		t.Error("a customer's command turned maintenance off")
	}
}

// The report, as the owner met it: in maintenance mode a customer taps the
// bottom menu's options one after the other, and Support (like the rest, when
// it came a moment after another tap) shows no maintenance message; if they
// had written to support before, it shows their ticket instead.
func TestMaintenanceSupportShowsTheMessage(t *testing.T) {
	m := newMaintWorld(t)
	fresh, talked := m.customer(6101, "fresh"), m.customer(6102, "talked")
	talked.text("/menu")
	talked.press("support")
	talked.text("my vpn is slow")
	browsing := m.customer(6103, "browsing")
	browsing.press("buy") // the plan list stays open on the screen
	m.maintenance(true)

	maintenance := m.h.cat.T("en", "maintenance")
	for _, p := range []*person{fresh, talked} {
		for _, key := range []string{"btn.buy", "btn.support", "btn.wallet", "btn.support", "btn.trial", "btn.support"} {
			before := len(m.tg.all(p.id))
			p.tapMenu("en", key)
			if got := texts(m.tg.all(p.id)[before:]); len(got) != 1 || got[0] != maintenance {
				t.Fatalf("user %d tapped %s in maintenance and got %q, want the maintenance text", p.id, key, got)
			}
		}
		popups := len(m.spy.of(p.id))
		p.tap("support") // the inline button too
		if got := m.spy.of(p.id)[popups:]; len(got) != 1 || got[0].text != maintenance || !got[0].alert {
			t.Fatalf("user %d pressed Support in maintenance and got %+v, want an alert with the maintenance text", p.id, got)
		}
	}
	if n := len(m.ticketMessages(m.openTicket(talked.id).ID)); n != 1 {
		t.Errorf("the ticket has %d messages", n)
	}

	// A menu that was already open: its buttons answer too, and it stays as it was.
	plans := browsing.last()
	text, buttons := plans.text, len(callbacks(plans))
	popups := len(m.spy.of(browsing.id))
	browsing.press("plan:")
	if got := m.spy.of(browsing.id)[popups:]; len(got) != 1 || got[0].text != maintenance || !got[0].alert {
		t.Errorf("a plan button of a menu open from before: %+v", got)
	}
	if plans.text != text || len(callbacks(plans)) != buttons {
		t.Errorf("the open menu was edited: %q", plans.text)
	}
}

// --- what goes on in maintenance ---

// Staff of every role, and an owner the server names who has no account
// yet, use the bot as ever.
func TestMaintenanceDoesNotHoldStaff(t *testing.T) {
	const second = 9100
	m := newMaintWorldOwners(t, ownerID, second)
	admin, support := m.register(9101, "adm", "en"), m.register(9102, "sup", "en")
	m.role(admin.id, "admin")
	m.role(support.id, "support")
	maintenance := m.h.cat.T("en", "maintenance")
	m.maintenance(true)

	for _, p := range []*person{m.owner, admin, support} {
		p.text("/menu")
		p.lastHas("Main menu")
		p.press("buy")
		p.lastHas("Choose a plan")
		p.tapMenu("en", "btn.wallet")
		p.lastHas("Wallet")
		p.press("wallet")
		p.sees("Wallet")
		p.text("hello")
		p.lastHas("Main menu")
		if p.count(maintenance) != 0 {
			t.Errorf("user %d was told about maintenance", p.id)
		}
		if answers := m.spy.of(p.id); len(answers) == 0 || slices.ContainsFunc(answers, func(a cbAnswer) bool { return a.text == maintenance }) {
			t.Errorf("user %d got the answers %+v to their buttons", p.id, answers)
		}
	}
	admin.press("adm")
	admin.lastHas("Admin panel")

	// The configured owner who never opened the bot: the language picker,
	// then the welcome and the menu, not the maintenance message.
	late := m.person(second, "late")
	late.text("hello")
	late.lastHas("Choose your language")
	late.text("/start")
	late.press("lang:en")
	late.lastHas("Main menu")
	late.press("buy")
	late.lastHas("Choose a plan")
	if late.count(maintenance) != 0 {
		t.Error("a configured owner without an account was told about maintenance")
	}
	if n := len(late.menus()); n != 1 {
		t.Errorf("a configured owner got %d bottom menus", n)
	}
}

// The updates that go on in maintenance: /paysupport, a payment under way (the
// proof, its check, a Stars invoice already sent) and a newcomer's first
// steps (below). A customer let through this way stays held back: no menu
// comes with it, and the next thing they tap is answered with the
// maintenance text.
func TestMaintenanceAllowlist(t *testing.T) {
	m := newMaintWorld(t)
	maintenance := m.h.cat.T("en", "maintenance")
	pay := func(id int64, name, method string) *person {
		p := m.customer(id, name)
		m.startPayment(p, method)
		return p
	}
	card, crypto, shot := pay(6501, "card", "pay:c:"), pay(6502, "crypto", "pay:x:"), pay(6503, "shot", "pay:x:")
	zarinpal, stars := pay(6504, "zarinpal", "pay:z:"), pay(6505, "stars", "pay:s:")
	top := m.customer(6506, "topup")
	top.press("wallet")
	top.press("wtop")
	top.press("wamt:100000")
	top.press("wpay:c:")
	idle := m.customer(6507, "idle")
	inv := m.tg.lastInvoice(stars.id)
	menus := len(card.menus())

	m.maintenance(true)

	// The receipt of a card payment, with its reference as the caption.
	card.photo("receipt-allowed", "REF-1")
	card.lastHas("Received")
	// A top-up's receipt, then the bank reference asked for after the photo.
	top.photo("receipt-topup", "")
	top.lastHas("bank tracking/reference number")
	top.text("554433")
	top.lastHas("Received")
	// A TXID, or a screenshot of the transfer.
	crypto.text(strings.Repeat("ab", 32))
	crypto.lastHas("Received")
	shot.document("usdt-shot", "image/png", "")
	shot.lastHas("Received")
	// The check of a gateway payment, the pre-checkout of a Stars invoice and its payment.
	zarinpal.press("chk:")
	zarinpal.toast("Not confirmed yet")
	if a := stars.preCheckout(inv, inv.Stars); !a.ok {
		t.Fatalf("pre-checkout of a Stars payment under way: %+v", a)
	}
	stars.paid(inv, inv.Stars, "maintenance-charge")
	stars.sees("Payment received")
	// /paysupport answers anyone, with an account or without.
	idle.text("/paysupport")
	idle.lastHas("Payment support")
	newcomer := stranger{m, 6508, "en"}
	newcomer.text("/paysupport")
	if got := m.tg.last(newcomer.id).text; !strings.Contains(got, "Payment support") {
		t.Errorf("/paysupport for someone with no account: %q", got)
	}

	for _, p := range []*person{card, top, crypto, shot, zarinpal, stars, idle} {
		if n := len(p.menus()); n != menus {
			t.Errorf("user %d got a bottom menu while held back (%d menus)", p.id, n)
		}
		if p.count(maintenance) != 0 {
			t.Errorf("user %d was told about maintenance for something that goes on", p.id)
		}
	}
	// Each of them is held for anything else, in the same breath.
	for _, p := range []*person{card, top, crypto, shot} {
		p.text("what now?")
		p.lastHas(maintenance)
		p.tapMenu("en", "btn.wallet")
		p.lastHas(maintenance)
	}
	zarinpal.press("home")
	zarinpal.toast(maintenance)
	idle.press("buy")
	idle.toast(maintenance)

	// The staff review goes on, and its result reaches the customer:
	// notifications are not held.
	m.owner.press("adm:pi:")
	m.owner.press("adm:ok:")
	shot.eventuallySees("Payment confirmed")
}

// stranger is a Telegram user the bot has no account for, as Telegram tells
// their language.
type stranger struct {
	m    *maintWorld
	id   int64
	lang string
}

func (s stranger) user() *tg.User {
	return &tg.User{ID: s.id, Username: "stranger" + strconv.FormatInt(s.id, 10), FirstName: "S", LanguageCode: s.lang}
}

func (s stranger) text(text string) {
	s.m.h.Handle(context.Background(), tg.Update{UpdateID: s.m.updateID(), Message: &tg.Message{
		MessageID: int(s.m.updateID()), From: s.user(), Chat: tg.Chat{ID: s.id, Type: "private"}, Text: text}})
}

func (s stranger) tap(data string) {
	id := strconv.FormatInt(s.m.updateID(), 10)
	s.m.tg.mu.Lock()
	s.m.tg.cbFrom[id] = s.id
	s.m.tg.mu.Unlock()
	s.m.h.Handle(context.Background(), tg.Update{UpdateID: s.m.updateID(), CallbackQuery: &tg.CallbackQuery{
		ID: id, From: *s.user(), Data: data, Message: &tg.Message{MessageID: s.m.tg.last(s.id).id, Chat: tg.Chat{ID: s.id, Type: "private"}}}})
}

// A newcomer can start and pick a language (an invite code survives it), then
// waits; anything else they say is answered with the maintenance text, in the
// language Telegram says they use.
func TestMaintenanceNewcomers(t *testing.T) {
	m := newMaintWorld(t)
	m.owner.text("/set referral.reward_percent 10")
	m.owner.lastHas("Saved")
	inviter := m.customer(6601, "inviter")
	inviter.press("ref")
	screen := inviter.lastWith("Invite friends").text // its promo card and the line about forwarding it come after it
	link := regexp.MustCompile(`start=r_([a-z0-9]+)`).FindStringSubmatch(screen)
	if link == nil {
		t.Fatalf("no invite link: %q", screen)
	}
	m.maintenance(true)
	en, fa := m.h.cat.T("en", "maintenance"), m.h.cat.T("fa", "maintenance")

	// Anything but /start, the language choice and /paysupport is held, in
	// their language (Persian for one Telegram does not name).
	for i, c := range []struct{ lang, text, want string }{
		{"en", "hello", en}, {"fa", "hello", fa}, {"en-GB", "/menu", en}, {"de", "/buy", fa}, {"", "/terms", fa},
		{"en", m.h.cat.T("en", "btn.buy"), en},
	} {
		s := stranger{m, int64(6610 + i), c.lang}
		for range 2 {
			before := len(m.tg.all(s.id))
			s.text(c.text)
			if got := texts(m.tg.all(s.id)[before:]); len(got) != 1 || got[0] != c.want {
				t.Errorf("a newcomer (%q) saying %q got %q, want the maintenance text once", c.lang, c.text, got)
			}
		}
	}
	// Their buttons are popups.
	s := stranger{m, 6620, "en"}
	s.tap("buy")
	s.tap("home")
	if a := m.spy.of(s.id); len(a) != 2 || a[0] != (cbAnswer{s.id, en, true}) || a[1] != (cbAnswer{s.id, en, true}) {
		t.Errorf("a newcomer's buttons: %+v", a)
	}

	// /start (with an invite code) and the language choice go on; the
	// welcome and the maintenance text come after the choice.
	s = stranger{m, 6621, "en"}
	s.text("/start r_" + link[1])
	if got := m.tg.last(s.id).text; !strings.Contains(got, "Choose your language") {
		t.Fatalf("/start in maintenance: %q", got)
	}
	s.tap("lang:en")
	got := m.tg.all(s.id) // the picker, now the welcome, and the maintenance text
	if len(got) != 2 || !strings.Contains(got[0].text, "Welcome") || got[1].text != en {
		t.Fatalf("after the choice: %q", texts(got))
	}
	for _, line := range got {
		if line.menu != nil {
			t.Error("a newcomer was sent the bottom menu during maintenance")
		}
	}
	if ref := m.storeUser(s.id).ReferredBy; ref == nil || *ref != m.storeUser(inviter.id).ID {
		t.Errorf("the invite code did not survive: referred by %v", ref)
	}
	// Now an account like any other.
	s.text("hello")
	if got := m.tg.last(s.id).text; got != en {
		t.Errorf("a customer who just registered: %q", got)
	}
	s.text(m.h.cat.T("en", "btn.wallet"))
	if got := m.tg.last(s.id).text; got != en {
		t.Errorf("a customer who just registered, tapping the menu: %q", got)
	}
}

// ownersPersianText is "we are back at five" in Persian.
const ownersPersianText = "تا ساعت پنج برمیگردیم"

// The owner's own text for the message is the one customers get, as a popup
// and as a message, in each language; reset, the built-in one is back.
func TestMaintenanceTextIsTheOwners(t *testing.T) {
	m := newMaintWorld(t)
	en, fa := m.customer(6701, "en_custom"), m.register(6702, "fa_custom", "fa")
	m.owner.text("/set texts.en.maintenance Back at five. Sorry for the wait!")
	m.owner.lastHas("Saved")
	m.owner.text("/set texts.fa.maintenance " + ownersPersianText)
	m.owner.lastHas("Saved")
	m.maintenance(true)

	en.text("hello")
	en.lastHas("Back at five. Sorry for the wait!")
	en.tapMenu("en", "btn.support")
	en.lastHas("Back at five. Sorry for the wait!")
	enPopups := len(m.spy.of(en.id))
	en.tap("buy")
	if a := m.spy.of(en.id)[enPopups:]; len(a) != 1 || a[0].text != "Back at five. Sorry for the wait!" || !a[0].alert {
		t.Errorf("popup: %+v", a)
	}
	fa.text("سلام")
	fa.lastHas(ownersPersianText)
	faPopups := len(m.spy.of(fa.id))
	fa.tap("wallet")
	if a := m.spy.of(fa.id)[faPopups:]; len(a) != 1 || a[0].text != ownersPersianText || !a[0].alert {
		t.Errorf("popup in persian: %+v", a)
	}

	m.owner.text("/set texts.en.maintenance")
	m.owner.lastHas("Saved")
	en.text("hello")
	en.lastHas("The bot is under maintenance")
}

// Turning maintenance on and off takes hold at once, with the bot running,
// by each of the owner's ways: /set, the settings button and the dashboard
// (core changes the setting, then asks the bot to reload; the bot also does
// it by itself every minute).
func TestMaintenanceFollowsTheSetting(t *testing.T) {
	m := newMaintWorld(t)
	u := m.customer(6801, "follower")
	held := func(how string) {
		t.Helper()
		messages, answers := len(m.tg.all(u.id)), len(m.spy.of(u.id))
		u.text("/menu")
		u.tap("buy")
		got := m.spy.of(u.id)[answers:]
		if n := len(m.tg.all(u.id)) - messages; n != 1 || len(got) != 1 || !got[0].alert || !strings.Contains(got[0].text, "under maintenance") {
			t.Fatalf("%s: %d messages and the answers %+v, want the maintenance text once as a message and once as an alert", how, n, got)
		}
		u.lastHas("under maintenance")
	}
	open := func(how string) {
		t.Helper()
		u.text("/menu")
		if !strings.Contains(u.last().text, "Main menu") {
			t.Fatalf("%s: /menu answered %q", how, u.last().text)
		}
		u.press("buy")
		if !strings.Contains(u.last().text, "Choose a plan") {
			t.Fatalf("%s: Buy answered %q", how, u.last().text)
		}
	}
	open("before")

	core := httptest.NewServer(runner.Refresh(m.h.RefreshSettings, testenv.CoreToken, nil))
	defer core.Close()
	askToReload := func() {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, core.URL, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+testenv.CoreToken)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("reload: %d", resp.StatusCode)
		}
	}
	for range 2 {
		m.maintenance(true)
		held("/set on")
		m.maintenance(false)
		open("/set off")

		m.owner.tap("adm:st:maintenance.enabled")
		held("button on")
		m.owner.tap("adm:st:maintenance.enabled")
		open("button off")

		m.setInCore("maintenance.enabled", "true")
		askToReload()
		held("dashboard on")
		m.setInCore("maintenance.enabled", "false")
		askToReload()
		open("dashboard off")
	}
}

// Maintenance comes before the required channel: while it is on nobody is
// asked to join (or looked up in the channel), and when it ends the channel
// is asked of whoever is outside it.
func TestMaintenanceComesBeforeTheRequiredChannel(t *testing.T) {
	m := newMaintWorld(t)
	member, outsider := m.customer(6901, "member"), m.customer(6902, "outsider")
	m.tg.setMember(member.id, "member")
	m.owner.text("/set join.channel @testchan")
	m.owner.lastHas("Saved")
	m.maintenance(true)
	checks := m.tg.checks()

	for _, p := range []*person{member, outsider} {
		popups := len(m.spy.of(p.id))
		p.text("/menu")
		p.lastHas("under maintenance")
		p.tap("buy")
		p.tap("jn")
		if a := m.spy.of(p.id)[popups:]; len(a) != 2 || !strings.Contains(a[0].text, "under maintenance") || !strings.Contains(a[1].text, "under maintenance") {
			t.Errorf("user %d got the popups %+v, want the maintenance text twice", p.id, a)
		}
		p.tapMenu("en", "btn.buy")
		p.lastHas("under maintenance")
		if p.count("Join our channel") != 0 {
			t.Errorf("user %d was asked to join during maintenance", p.id)
		}
	}
	newcomer := m.person(6903, "late")
	newcomer.text("/start")
	newcomer.press("lang:en")
	newcomer.lastHas("under maintenance")
	if newcomer.count("Join our channel") != 0 || m.tg.checks() != checks {
		t.Errorf("channel membership was looked at during maintenance (%d checks)", m.tg.checks()-checks)
	}

	m.maintenance(false)
	member.tapMenu("en", "btn.buy")
	member.lastHas("Choose a plan")
	outsider.tapMenu("en", "btn.buy")
	outsider.lastHas("Join our channel")
}

// The labels of a bottom menu that a customer holds from before an edit of
// its texts, or before a restart of the bot, are held like the rest.
func TestMaintenanceHoldsOldBottomMenus(t *testing.T) {
	m := newMaintWorld(t)
	u := m.customer(7001, "old_menu")
	m.owner.text("/set texts.en.btn.wallet 💰 My wallet")
	m.owner.lastHas("Saved")
	m.maintenance(true)
	for _, label := range []string{"💰 My wallet", m.h.cat.Default("en", "btn.wallet"), m.h.cat.Default("fa", "btn.wallet")} {
		before := len(m.tg.all(u.id))
		u.text(label)
		if got := texts(m.tg.all(u.id)[before:]); len(got) != 1 || !strings.Contains(got[0], "under maintenance") {
			t.Errorf("label %q: %q", label, got)
		}
	}
	m.restart() // a bot that has just started, and loaded its settings
	m.h.tg = m.spy
	if err := m.h.RefreshSettings(context.Background()); err != nil {
		t.Fatal(err)
	}
	u.text("💰 My wallet")
	u.lastHas("under maintenance")
}

// --- the tables miss nothing ---

// caseStrings are the string literals of the case clauses in function fn of
// one of the handler's source files.
func caseStrings(t *testing.T, file, fn string) []string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Name.Name != fn {
			continue
		}
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			if cc, ok := n.(*ast.CaseClause); ok {
				for _, e := range cc.List {
					if lit, ok := e.(*ast.BasicLit); ok && lit.Kind == token.STRING {
						s, err := strconv.Unquote(lit.Value)
						if err != nil {
							t.Fatal(err)
						}
						out = append(out, s)
					}
				}
			}
			return true
		})
	}
	if len(out) == 0 {
		t.Fatalf("no case clauses found in %s of %s", fn, file)
	}
	return out
}

// sceneNames are the values of the handler's scene constants.
func sceneNames(t *testing.T) []string {
	t.Helper()
	files, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range files {
		if !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), e.Name(), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, spec := range gd.Specs {
				vs := spec.(*ast.ValueSpec)
				for i, name := range vs.Names {
					if i >= len(vs.Values) || !strings.HasPrefix(name.Name, "scene") {
						continue
					}
					if lit, ok := vs.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
						s, _ := strconv.Unquote(lit.Value)
						out = append(out, s)
					}
				}
			}
		}
	}
	return out
}

// addACase is what to do when the tables miss an entry point.
const addACase = ": maintenance must answer it too. Add a case for it to gatedCases (maintenance_test.go) and see that it is " +
	"held; if a payment under way needs it, name it in this test and test it in TestMaintenanceAllowlist"

// Every command, button verb, bottom-menu item and scene the handler has is
// in the tables above: a new one fails here until a case for it is added (or,
// when it belongs in the allowlist, until it is named below and has a test in
// TestMaintenanceAllowlist).
func TestMaintenanceTablesCoverEveryEntryPoint(t *testing.T) {
	cases := gatedCases()
	verbs, cmds, scenes, taps := map[string]bool{"jn": true}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, c := range cases {
		verbs[c.verb], cmds[c.cmd], scenes[c.scene] = true, true, true
		if rest, ok := strings.CutPrefix(c.name, "bottom menu "); ok {
			taps[strings.Fields(rest)[0]] = true
		}
	}
	// What goes on in maintenance, and is tested as such.
	allowedVerbs, allowedCommands := []string{"chk"}, []string{"/paysupport"}
	for _, verb := range caseStrings(t, "handler.go", "dispatch") {
		if !verbs[verb] && !slices.Contains(allowedVerbs, verb) {
			t.Errorf("no case presses a button with the verb %q"+addACase, verb)
		}
	}
	for _, cmd := range caseStrings(t, "handler.go", "onCommand") {
		if !cmds[cmd] && !slices.Contains(allowedCommands, cmd) {
			t.Errorf("no case sends the command %s"+addACase, cmd)
		}
	}
	for _, scene := range sceneNames(t) {
		if !scenes[scene] {
			t.Errorf("no case sends a message in the %q scene"+addACase, scene)
		}
	}
	for _, it := range menuItems {
		if !taps[it.key] {
			t.Errorf("no case taps the bottom menu's %s"+addACase, it.key)
		}
	}
	if len(i18n.Languages) != 2 {
		t.Errorf("the tables tap the bottom menu in two languages; there are %d", len(i18n.Languages))
	}
}
