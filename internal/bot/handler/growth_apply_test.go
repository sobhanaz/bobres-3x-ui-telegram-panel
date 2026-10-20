package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/notify"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	coredomain "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/eventbus"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
	"google.golang.org/grpc"
)

// scene is what the bot waits for from the user now ("" for nothing).
func (w *world) scene(telegramID int64) string {
	w.t.Helper()
	st, err := w.h.state.Get(context.Background(), telegramID)
	if err != nil {
		w.t.Fatal(err)
	}
	return st.Scene
}

// A customer taps Become a reseller, sends one message about themselves and gets
// a ticket in the reseller category; the staff are alerted with the applicant
// and a button that opens the ticket, and the customer is told to wait.
func TestResellerApplication(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	cat := w.h.cat
	u := w.customer(7400000001, "applicant")

	// The button is on the home screen and in the bottom menu, for a customer.
	u.text("/menu")
	if !hasCallback(u.shown(), "ap") || styleOf(t, u.shown(), "ap") != "primary" {
		t.Fatalf("the home screen: %v", callbacks(u.shown()))
	}
	if !menuHas(u.lastMenu().Keyboard, cat.T("en", "btn.reseller_apply")) {
		t.Fatalf("the bottom menu: %+v", u.lastMenu())
	}

	u.press("ap")
	if got := u.last().text; got != cat.T("en", "rs.apply_ask") {
		t.Fatalf("the prompt: %q", got)
	}
	if cb := callbacks(u.last()); !slices.Equal(cb, []string{"cancel"}) {
		t.Fatalf("the prompt's buttons: %v", cb)
	}
	if w.scene(7400000001) != sceneResellerApply {
		t.Fatalf("the question is not open: %q", w.scene(7400000001))
	}
	application := "I run a shop in Tehran and have 40 customers who need a VPN."
	u.text(application)
	tk := w.openTicket(7400000001)
	if tk.Category != "reseller" || tk.Subject != application || tk.Status != "open" {
		t.Fatalf("the ticket: %+v", tk)
	}
	if ms := w.ticketMessages(tk.ID); len(ms) != 1 || ms[0].Kind != "customer" || ms[0].Body != application {
		t.Fatalf("the application's message: %+v", ms)
	}
	u.lastHas(fmt.Sprintf("✅ Your application was sent (ticket <b>#%d</b>). An admin will contact you here.", tk.Number))
	if cb := callbacks(u.last()); !slices.Equal(cb, []string{"home"}) {
		t.Fatalf("the confirmation's buttons: %v", cb)
	}
	if w.scene(7400000001) != "" {
		t.Fatalf("the question is still open: %q", w.scene(7400000001))
	}

	// The owner is alerted: the applicant and what they wrote, in the words of an
	// application, not of a support ticket.
	owner.eventuallySees(fmt.Sprintf("🤝 New reseller application <b>#%d</b> from @applicant:\n\n%s", tk.Number, application))
	w.drain()
	if owner.count("New ticket") != 0 {
		t.Fatal("the application was alerted as a support ticket")
	}

	// With a public address the alert opens the ticket in the dashboard.
	core := &publicCore{CoreServiceClient: w.stack.Core, base: "https://panel.example.com"}
	n := notify.New(core, w.tg, cat, w.h.Settings(), []int64{ownerID}, nil)
	payload, _ := json.Marshal(events.TicketOpenedEvent{TicketID: tk.ID, Number: tk.Number,
		MessageID: w.ticketMessages(tk.ID)[0].ID, TelegramID: 7400000001, Category: "reseller"})
	if err := n.Handle(context.Background(), eventbus.Message{Topic: events.TicketOpened, Payload: payload}); err != nil {
		t.Fatal(err)
	}
	if l := owner.link("Open in dashboard"); l != "https://panel.example.com/admin/support/"+tk.ID {
		t.Fatalf("the alert's link: %q", l)
	}

	// The application is the customer's open conversation: the Support screen
	// shows it, and nothing they write next starts another ticket.
	u.text("one more thing")
	u.lastHas("Main menu")
	if ms := w.ticketMessages(tk.ID); len(ms) != 1 {
		t.Fatalf("a message outside the question was added: %d", len(ms))
	}
	u.press("home")
	u.press("support")
	u.lastHas(application)
}

// menuHas reports whether a bottom menu has a button with this label.
func menuHas(rows [][]tg.MenuButton, text string) bool {
	for _, row := range rows {
		for _, b := range row {
			if b.Text == text {
				return true
			}
		}
	}
	return false
}

// The application in Persian, and the staff alert in the language of the
// staff member who gets it.
func TestResellerApplicationInPersian(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	owner.text("/set notify.recipients staff")
	adm := w.customerNamed(7400000011, "admin_fa", "fa")
	w.role(7400000011, "admin")
	fa := w.customerNamed(7400000012, "", "fa")
	fa.text("/menu")
	fa.press("ap")
	if got := fa.last().text; got != w.h.cat.T("fa", "rs.apply_ask") || !strings.Contains(got, "درخواست نمایندگی") {
		t.Fatalf("the Persian prompt: %q", got)
	}
	fa.text("فروشگاه موبایل دارم و می\u200cخواهم نماینده شوم")
	tk := w.openTicket(7400000012)
	fa.lastHas(fmt.Sprintf("✅ درخواست نمایندگی شما ثبت شد (تیکت <b>#%d</b>)", tk.Number))
	adm.eventuallySees(fmt.Sprintf("🤝 درخواست نمایندگی جدید <b>#%d</b> از id 7400000012:\n\nفروشگاه موبایل دارم", tk.Number))
	owner.eventuallySees(fmt.Sprintf("🤝 New reseller application <b>#%d</b> from id 7400000012:", tk.Number))
}

// One open application a customer: another tap says so (with the ticket) and
// asks for nothing; so does another open conversation with support; a closed
// application can be made again.
func TestResellerApplicationIsOnePerCustomer(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	cat := w.h.cat
	u := w.customer(7400000021, "once")
	u.press("ap")
	u.text("first application")
	first := w.openTicket(7400000021)

	u.press("home")
	u.press("ap")
	u.lastHas(fmt.Sprintf("Your application (ticket <b>#%d</b>) is waiting for an admin.", first.Number))
	if cb := callbacks(u.last()); !slices.Equal(cb, []string{"support", "home"}) {
		t.Fatalf("an application waiting: %v", cb)
	}
	if w.scene(7400000021) != "" {
		t.Fatal("a question was opened beside the application")
	}
	u.text("let me apply again")
	u.lastHas("Main menu")
	if ms := w.ticketMessages(first.ID); len(ms) != 1 {
		t.Fatalf("the application got %d messages", len(ms))
	}
	u.press("support") // its thread, to write more in
	u.lastHas("first application")

	// Staff answer and close it: the customer can apply again.
	w.staffReply(7400000021, "Please send the address of your shop", true)
	u.eventuallySees("Please send the address of your shop")
	u.press("home")
	u.press("ap")
	if got := u.shown().text; got != cat.T("en", "rs.apply_ask") { // an older message, edited
		t.Fatalf("after the first was closed: %q", got)
	}
	u.text("second application")
	second := w.openTicket(7400000021)
	if second.ID == first.ID || second.Number <= first.Number || second.Category != "reseller" {
		t.Fatalf("the second application: %+v after %+v", second, first)
	}

	// Another conversation with support is open: an application neither joins it
	// nor starts one beside it, and says so.
	busy := w.customer(7400000022, "busy")
	busy.press("support")
	busy.press("tk:n:payment")
	busy.text("my payment is missing")
	help := w.openTicket(7400000022)
	busy.press("home")
	busy.press("ap")
	busy.lastHas(fmt.Sprintf("You have an open conversation with support (ticket <b>#%d</b>).", help.Number))
	if cb := callbacks(busy.last()); !slices.Equal(cb, []string{"support", "home"}) {
		t.Fatalf("an open conversation: %v", cb)
	}
	if w.scene(7400000022) != "" || w.openTicket(7400000022).Category != "payment" {
		t.Fatal("an application was started beside an open conversation")
	}

	// A conversation opened after the question was asked (on another device, in
	// the Mini App): core refuses the application and the bot says which ticket
	// is open.
	racing := w.customer(7400000023, "racing")
	racing.press("ap")
	if _, err := w.dom().AddCustomerMessage(context.Background(), coredomain.CustomerMessage{UserID: w.storeUser(7400000023).ID,
		Category: "service", Text: "no connection", TGMessageID: 1}); err != nil {
		t.Fatal(err)
	}
	racing.text("I want to be a reseller")
	racing.lastHas("You have an open conversation with support")
	if w.scene(7400000023) != "" || w.openTicket(7400000023).Category != "service" || len(w.ticketMessages(w.openTicket(7400000023).ID)) != 1 {
		t.Fatal("the application joined or replaced the open conversation")
	}
}

// The question can be cancelled; what is not a text message is asked for again;
// a gate that holds the customer back keeps the question and the application
// is made once they are let in; a text after the question closed is no
// application.
func TestResellerApplicationQuestion(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	cat := w.h.cat
	u := w.customer(7400000031, "asking")
	u.press("ap")
	u.press("cancel")
	u.toast(cat.T("en", "cancelled"))
	if w.scene(7400000031) != "" {
		t.Fatal("the question is open after Cancel")
	}
	u.text("an application nobody asked for")
	u.lastHas("Main menu")
	if w.hasOpenTicket(7400000031) {
		t.Fatal("a ticket was filed after Cancel")
	}

	// A picture is not an application; the question stays.
	u.press("ap")
	u.photo("shop.jpg", "")
	u.lastHas(cat.T("en", "rs.apply_need_text"))
	if cb := callbacks(u.last()); !slices.Equal(cb, []string{"cancel"}) || w.scene(7400000031) != sceneResellerApply || w.hasOpenTicket(7400000031) {
		t.Fatalf("after a picture: %v %q", cb, w.scene(7400000031))
	}

	// Maintenance holds the customer back, the question stays, and the same
	// message is the application once they are let in.
	owner.text("/set maintenance.enabled true")
	owner.sees("Saved")
	u.text("I sell phones")
	u.lastHas("under maintenance")
	if w.hasOpenTicket(7400000031) {
		t.Fatal("an application was filed in maintenance")
	}
	owner.text("/set maintenance.enabled false")
	owner.sees("Saved")
	u.text("I sell phones")
	if !w.hasOpenTicket(7400000031) || w.openTicket(7400000031).Subject != "I sell phones" {
		t.Fatal("no application after maintenance")
	}

	// The tap itself is held back by a required channel.
	late := w.customer(7400000032, "outside")
	late.text("/menu")
	owner.text("/set join.channel @testchan")
	owner.sees("Saved")
	late.press("ap")
	late.lastHas("Join our channel")
	if w.scene(7400000032) != "" {
		t.Fatal("the question was opened for someone outside the channel")
	}
}

// Who is offered the button, and what core says to the rest: a reseller, staff
// and a banned customer never get it; a license without resellers takes it
// away, the question that was open ends with a plain "not available" and
// nothing is filed.
func TestResellerApplicationIsOnlyForCustomers(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	cat := w.h.cat
	hasApply := func(p *person) bool {
		p.text("/menu")
		inHome := hasCallback(p.shown(), "ap")
		inMenu := menuHas(p.lastMenu().Keyboard, cat.T("en", "btn.reseller_apply"))
		if inHome != inMenu {
			t.Fatalf("user %d: the home screen has the button: %v, the bottom menu: %v", p.id, inHome, inMenu)
		}
		return inHome
	}

	customer := w.customer(7400000041, "plain")
	if !hasApply(customer) {
		t.Fatal("a customer is not offered the button")
	}
	rs := w.customer(7400000042, "already_rs")
	w.makeReseller(7400000042, 0, 10, 0)
	if hasApply(rs) {
		t.Fatal("a reseller is offered the button")
	}
	rs.tap("ap") // an old button
	rs.toast(cat.T("en", "rs.apply_already"))
	rs.lastHas("Main menu")
	if w.hasOpenTicket(7400000042) {
		t.Fatal("a reseller applied")
	}
	for i, role := range []string{"owner", "admin", "support"} {
		id := int64(7400000050 + i)
		p := w.customer(id, role+"_user")
		w.role(id, role)
		if hasApply(p) {
			t.Fatalf("a %s is offered the button", role)
		}
		p.tap("ap")
		p.toast(cat.T("en", "rs.unavailable"))
		if w.hasOpenTicket(id) || w.scene(id) != "" {
			t.Fatalf("a %s applied", role)
		}
	}
	banned := w.customer(7400000060, "banned_one")
	w.setStatusOf(7400000060, "banned")
	if hasApply(banned) {
		t.Fatal("a banned customer is offered the button")
	}
	banned.tap("ap")
	banned.toast(cat.T("en", "rs.unavailable"))

	// The license loses resellers. Core tells the bot at its next refresh;
	// before that a customer can still tap, and core refuses the application
	// without a word about the license.
	asking := w.customer(7400000061, "asking_late")
	asking.press("ap")
	w.resellersLeaveTheLicense()
	asking.text("I want to resell")
	asking.lastHas(cat.T("en", "rs.unavailable"))
	if w.hasOpenTicket(7400000061) || w.scene(7400000061) != "" {
		t.Fatal("an application was filed without resellers in the license")
	}
	if strings.Contains(strings.ToLower(asking.allText()), "license") {
		t.Fatalf("the customer was told about the license: %s", asking.allText())
	}
	if err := w.h.RefreshSettings(context.Background()); err != nil {
		t.Fatal(err)
	}
	if hasApply(customer) {
		t.Fatal("the button stays after resellers left the license")
	}
	customer.tap("ap")
	customer.toast(cat.T("en", "rs.unavailable"))
	customer.lastHas("Main menu")

	// A crafted call to core is refused the same way, for anyone.
	if _, err := w.stack.Core.AddTicketMessage(context.Background(), &corev1.AddTicketMessageRequest{UserId: w.storeUser(7400000041).ID,
		Category: "reseller", Text: "direct", TgMessageId: 99}); err == nil {
		t.Fatal("core took an application without resellers in the license")
	}
}

// The two growth buttons are on the home screen and in the bottom menu together,
// for every role, as the switches turn them on and off: the referral program
// and the owner's switch for Top users, the license for Become a reseller.
func TestGrowthButtonsStayInStepOnHomeAndBottomMenu(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	cat := w.h.cat
	who := map[string]*person{"customer": w.customer(7500000001, "g_customer"), "reseller": w.customer(7500000002, "g_reseller"),
		"support": w.customer(7500000003, "g_support"), "admin": w.customer(7500000004, "g_admin"),
		"banned": w.customer(7500000005, "g_banned"), "owner": owner}
	w.makeReseller(7500000002, 0, 10, 0)
	w.role(7500000003, "support")
	w.role(7500000004, "admin")
	w.setStatusOf(7500000005, "banned")

	check := func(phase string, want map[string][]string) {
		t.Helper()
		for role, p := range who {
			p.text("/menu")
			var home, menu []string
			for _, b := range []struct{ key, data string }{{"btn.reseller_apply", "ap"}, {"btn.top_users", "top"}} {
				if hasCallback(p.shown(), b.data) {
					home = append(home, b.key)
				}
				if menuHas(p.lastMenu().Keyboard, cat.T("en", b.key)) {
					menu = append(menu, b.key)
				}
			}
			if !slices.Equal(home, menu) || !slices.Equal(home, want[role]) {
				t.Errorf("%s, %s: the home screen has %v, the bottom menu %v, want %v", phase, role, home, menu, want[role])
			}
		}
	}
	apply, top := "btn.reseller_apply", "btn.top_users"

	check("no referral program", map[string][]string{"customer": {apply}})
	owner.text("/set referral.reward_percent 10")
	owner.sees("referral.reward_percent")
	check("the referral program on", map[string][]string{"customer": {apply, top}, "reseller": {top}, "support": {top},
		"admin": {top}, "banned": {top}, "owner": {top}})
	owner.text("/set growth.leaderboard.enabled false")
	owner.sees("Saved")
	check("the board switched off", map[string][]string{"customer": {apply}})
	owner.text("/set growth.leaderboard.enabled true")
	owner.sees("Saved")
	w.resellersLeaveTheLicense()
	if err := w.h.RefreshSettings(context.Background()); err != nil {
		t.Fatal(err)
	}
	check("resellers out of the license", map[string][]string{"customer": {top}, "reseller": {top}, "support": {top},
		"admin": {top}, "banned": {top}, "owner": {top}})
}

// settingsOlderCore is a core that does not say whether resellers are in the
// license (an older one): the bot offers no button.
type settingsOlderCore struct{ corev1.CoreServiceClient }

func (c settingsOlderCore) GetSettings(ctx context.Context, in *corev1.GetSettingsRequest, opts ...grpc.CallOption) (*corev1.Settings, error) {
	s, err := c.CoreServiceClient.GetSettings(ctx, in, opts...)
	if s != nil {
		s.ResellersEnabled = false
	}
	return s, err
}

func TestResellerApplicationWithACoreOlderThanTheBot(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	u := w.customer(7400000071, "old_core")
	w.h.core = settingsOlderCore{w.stack.Core}
	if err := w.h.RefreshSettings(context.Background()); err != nil {
		t.Fatal(err)
	}
	u.text("/menu")
	if hasCallback(u.shown(), "ap") {
		t.Fatalf("an older core's customer was offered the button: %v", callbacks(u.shown()))
	}
}

// Alerts follow the staff's switch for new tickets (notify.new_ticket), as any
// new ticket's do; the ticket is made all the same.
func TestResellerApplicationAlertFollowsTheTicketSwitch(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	owner.text("/set notify.new_ticket false")
	owner.sees("Saved")
	u := w.customer(7400000081, "quiet")
	u.press("ap")
	u.text("I would like to resell")
	tk := w.openTicket(7400000081)
	if tk.Category != "reseller" {
		t.Fatalf("the ticket: %+v", tk)
	}
	w.drain()
	if owner.count("reseller application") != 0 {
		t.Fatal("an alert came with notify.new_ticket off")
	}
}
