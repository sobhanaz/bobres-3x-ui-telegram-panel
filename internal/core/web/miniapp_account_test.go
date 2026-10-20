package web

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
)

// features is the "features" object of the sign-in and /me answers.
func (m *miniFix) features(tok string) map[string]any {
	m.t.Helper()
	code, out := m.call(http.MethodGet, "/api/mini/v1/me", tok, nil)
	f, _ := out["features"].(map[string]any)
	if code != http.StatusOK || f == nil {
		m.t.Fatalf("/me: %d %v", code, out)
	}
	return f
}

// trialPlan makes the one enabled trial plan: other tests leave plans behind,
// so theirs are switched off first.
func (m *miniFix) trialPlan() {
	m.t.Helper()
	m.sql(`UPDATE core.plans SET enabled = false WHERE is_trial`)
	p := m.plan("Free trial", 1, 1<<30, 0)
	m.sql(`UPDATE core.plans SET is_trial = true WHERE id = $1`, p.ID)
}

func TestMiniAppTrial(t *testing.T) {
	m := newMiniFixture(t, true)
	m.sql(`UPDATE core.plans SET enabled = false WHERE is_trial`)
	tok := m.signIn("tg:3001:tara")

	// No trial plan: nothing to offer, and starting one is refused.
	if f := m.features(tok); f["trial"] != false {
		t.Fatalf("features without a trial plan: %v", f)
	}
	code, out := m.call(http.MethodGet, "/api/mini/v1/trial", tok, nil)
	if code != http.StatusOK || out["available"] != false || out["used"] != false || out["plan"] != nil {
		t.Fatalf("trial without a plan: %d %v", code, out)
	}
	if code, out := m.call(http.MethodPost, "/api/mini/v1/trial", tok, map[string]any{}); code != http.StatusConflict || out["error"] != "plan_unavailable" {
		t.Fatalf("start without a plan: %d %v", code, out)
	}
	if code, _ := m.call(http.MethodGet, "/api/mini/v1/trial", "", nil); code != http.StatusUnauthorized {
		t.Fatalf("trial without a session: %d", code)
	}

	// With one: what it gives, then taken once.
	m.trialPlan()
	if f := m.features(tok); f["trial"] != true {
		t.Fatalf("features with a trial plan: %v", f)
	}
	code, out = m.call(http.MethodGet, "/api/mini/v1/trial", tok, nil)
	plan, _ := out["plan"].(map[string]any)
	if code != http.StatusOK || out["available"] != true || out["used"] != false || plan["days"] != float64(1) || plan["traffic_bytes"] != float64(1<<30) {
		t.Fatalf("trial: %d %v", code, out)
	}
	code, out = m.call(http.MethodPost, "/api/mini/v1/trial", tok, map[string]any{})
	if code != http.StatusOK || out["status"] != "paid" || out["amount"] != float64(0) || out["type"] != "new" || out["id"] == "" {
		t.Fatalf("start: %d %v", code, out)
	}
	orderID := out["id"]
	// The order is the customer's own, polled like a purchase's.
	if code, out := m.call(http.MethodGet, fmt.Sprintf("/api/mini/v1/orders/%v", orderID), tok, nil); code != http.StatusOK || out["id"] != orderID {
		t.Fatalf("poll the trial order: %d %v", code, out)
	}

	// Once: asking again is refused, and the app stops offering it.
	if code, out := m.call(http.MethodPost, "/api/mini/v1/trial", tok, map[string]any{}); code != http.StatusConflict || out["error"] != "trial_used" {
		t.Fatalf("second start: %d %v", code, out)
	}
	if code, out := m.call(http.MethodGet, "/api/mini/v1/trial", tok, nil); code != http.StatusOK || out["available"] != false || out["used"] != true {
		t.Fatalf("after the trial: %d %v", code, out)
	}
	if f := m.features(tok); f["trial"] != false {
		t.Fatalf("features after the trial: %v", f)
	}

	// Another customer still has theirs.
	tok2 := m.signIn("tg:3002:omid")
	if code, out := m.call(http.MethodPost, "/api/mini/v1/trial", tok2, map[string]any{}); code != http.StatusOK || out["id"] == orderID {
		t.Fatalf("another customer's trial: %d %v", code, out)
	}
}

// The trial is the bot's trial: a trial taken there is used here too (one per
// account, whichever door it came through).
func TestMiniAppTrialTakenInTheBot(t *testing.T) {
	m := newMiniFixture(t, true)
	m.trialPlan()
	tok := m.signIn("tg:3003:nika")
	u, err := m.st.GetUserByTelegramID(context.Background(), m.st.Conn(), 3003)
	if err != nil {
		t.Fatal(err)
	}
	var planID string
	if err := m.st.DB().QueryRow(context.Background(), `SELECT id FROM core.plans WHERE is_trial AND enabled LIMIT 1`).Scan(&planID); err != nil {
		t.Fatal(err)
	}
	// What the bot does: the same domain call, its own key.
	if _, err := m.dom.StartTrial(context.Background(), u.ID, planID, "tg-trial-3003"); err != nil {
		t.Fatal(err)
	}
	if code, out := m.call(http.MethodGet, "/api/mini/v1/trial", tok, nil); code != http.StatusOK || out["available"] != false || out["used"] != true {
		t.Fatalf("after the bot's trial: %d %v", code, out)
	}
	if code, out := m.call(http.MethodPost, "/api/mini/v1/trial", tok, map[string]any{}); code != http.StatusConflict || out["error"] != "trial_used" {
		t.Fatalf("start after the bot's trial: %d %v", code, out)
	}
}

func TestMiniAppInvite(t *testing.T) {
	m := newMiniFixture(t, true)
	tok := m.signIn("tg:3101:ida")

	// Off until the owner sets a reward and the bot's name is known.
	if f := m.features(tok); f["invite"] != false {
		t.Fatalf("features by default: %v", f)
	}
	if code, out := m.call(http.MethodGet, "/api/mini/v1/invite", tok, nil); code != http.StatusNotFound {
		t.Fatalf("invite while off: %d %v", code, out)
	}
	m.setting("referral.reward_percent", "10")
	if f := m.features(tok); f["invite"] != false {
		t.Fatalf("a reward but no bot name: %v", f)
	}
	m.setting("branding.bot_username", "@acme_bot")
	if f := m.features(tok); f["invite"] != true {
		t.Fatalf("features with a reward and a bot: %v", f)
	}

	code, out := m.call(http.MethodGet, "/api/mini/v1/invite", tok, nil)
	link, _ := out["link"].(string)
	if code != http.StatusOK || !strings.HasPrefix(link, "https://t.me/acme_bot?start=r_") || len(link) < len("https://t.me/acme_bot?start=r_")+4 ||
		out["percent"] != float64(10) || out["invited"] != float64(0) || out["rewarded"] != float64(0) {
		t.Fatalf("invite: %d %v", code, out)
	}
	if earned, _ := out["earned"].([]any); len(earned) != 0 {
		t.Fatalf("earned before anyone joined: %v", out["earned"])
	}
	// The code is the customer's own and stays.
	if _, again := m.call(http.MethodGet, "/api/mini/v1/invite", tok, nil); again["link"] != link {
		t.Fatalf("the link changed: %v then %v", link, again["link"])
	}
	if other := m.signIn("tg:3102:jan"); other == tok {
		t.Fatal("the same session twice")
	} else if _, o := m.call(http.MethodGet, "/api/mini/v1/invite", other, nil); o["link"] == link {
		t.Fatalf("two customers share a link: %v", o)
	}

	// A reseller has a link of their own in their panel, not this one.
	u, err := m.st.GetUserByTelegramID(context.Background(), m.st.Conn(), 3101)
	if err != nil {
		t.Fatal(err)
	}
	m.sql(`UPDATE core.users SET role = 'reseller' WHERE id = $1`, u.ID)
	if f := m.features(tok); f["invite"] != false {
		t.Fatalf("a reseller is offered the customer link: %v", f)
	}
	if code, _ := m.call(http.MethodGet, "/api/mini/v1/invite", tok, nil); code != http.StatusNotFound {
		t.Fatalf("a reseller's invite: %d", code)
	}

	// Switching the program off hides it again.
	m.setting("referral.reward_percent", "0")
	if f := m.features(m.signIn("tg:3102:jan")); f["invite"] != false {
		t.Fatalf("features with the program off: %v", f)
	}
}

// The bot's own username (told with every sign-in check) stands in for the
// Branding setting, so the invite section and the "open the bot" link work on a
// store whose owner never typed it; the setting, when set, wins.
func TestMiniAppUsesTheBotsOwnUsername(t *testing.T) {
	m := newMiniFixture(t, true)
	m.setting("referral.reward_percent", "10")
	m.api.cfg.MiniAppVerify = fakeVerifier{bot: "told_bot"}

	// Signing in is what tells core: the same answer already has it.
	code, out := m.call(http.MethodPost, "/api/mini/v1/auth", "", map[string]any{"init_data": "tg:3401:eli"})
	tok, _ := out["token"].(string)
	brand, _ := out["brand"].(map[string]any)
	features, _ := out["features"].(map[string]any)
	if code != http.StatusOK || brand["bot_url"] != "https://t.me/told_bot" || features["invite"] != true {
		t.Fatalf("sign-in with no Branding username: %d %v", code, out)
	}
	if _, inv := m.call(http.MethodGet, "/api/mini/v1/invite", tok, nil); !strings.HasPrefix(fmt.Sprint(inv["link"]), "https://t.me/told_bot?start=r_") {
		t.Fatalf("invite link: %v", inv)
	}

	// The owner's setting wins.
	m.setting("branding.bot_username", "@owner_bot")
	if _, inv := m.call(http.MethodGet, "/api/mini/v1/invite", tok, nil); !strings.HasPrefix(fmt.Sprint(inv["link"]), "https://t.me/owner_bot?start=r_") {
		t.Fatalf("invite link with the setting: %v", inv)
	}
	if _, me := m.call(http.MethodGet, "/api/mini/v1/me", tok, nil); me["brand"].(map[string]any)["bot_url"] != "https://t.me/owner_bot" {
		t.Fatalf("/me: %v", me)
	}

	// A bot that says nothing, or something that is not a username, adds nothing.
	m2 := newMiniFixture(t, true)
	m2.setting("referral.reward_percent", "10")
	for _, bot := range []string{"", "not a username", "ab"} {
		m2.api.cfg.MiniAppVerify = fakeVerifier{bot: bot}
		_, out := m2.call(http.MethodPost, "/api/mini/v1/auth", "", map[string]any{"init_data": "tg:3402:fay"})
		if out["features"].(map[string]any)["invite"] != false || out["brand"].(map[string]any)["bot_url"] != "" {
			t.Fatalf("bot %q: %v", bot, out)
		}
	}
}

func TestMiniAppSupport(t *testing.T) {
	m := newMiniFixture(t, true)
	tok := m.signIn("tg:3201:sima")
	if f := m.features(tok); f["support"] != true {
		t.Fatalf("features: %v", f)
	}

	code, out := m.call(http.MethodGet, "/api/mini/v1/support", tok, nil)
	if tickets, _ := out["tickets"].([]any); code != http.StatusOK || out["open"] != nil || len(tickets) != 0 {
		t.Fatalf("support when empty: %d %v", code, out)
	}
	if cats, _ := out["categories"].([]any); len(cats) != 3 {
		t.Fatalf("categories: %v", out["categories"])
	}
	if code, _ := m.call(http.MethodPost, "/api/mini/v1/support/messages", tok, map[string]any{"text": "  "}); code != http.StatusBadRequest {
		t.Fatalf("an empty message: %d", code)
	}

	// The first message starts a ticket, the next ones join it.
	code, out = m.call(http.MethodPost, "/api/mini/v1/support/messages", tok, map[string]any{"text": "My payment is missing", "category": "payment"})
	first, _ := out["ticket"].(map[string]any)
	if code != http.StatusOK || out["new_ticket"] != true || first["category"] != "payment" || first["status"] == "closed" || first["id"] == "" {
		t.Fatalf("first message: %d %v", code, out)
	}
	code, out = m.call(http.MethodPost, "/api/mini/v1/support/messages", tok, map[string]any{"text": "Sent it an hour ago", "category": "service"})
	second, _ := out["ticket"].(map[string]any)
	if code != http.StatusOK || out["new_ticket"] != false || second["id"] != first["id"] {
		t.Fatalf("second message: %d %v", code, out)
	}
	id := first["id"].(string)

	code, out = m.call(http.MethodGet, "/api/mini/v1/support", tok, nil)
	open, _ := out["open"].(map[string]any)
	if tickets, _ := out["tickets"].([]any); code != http.StatusOK || open["id"] != id || len(tickets) != 1 {
		t.Fatalf("support with a ticket: %d %v", code, out)
	}

	// Staff answer and leave a private note: the thread shows the answer only.
	owner, err := m.st.GetUserByTelegramID(context.Background(), m.st.Conn(), ownerTG)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.dom.ReplyTicket(context.Background(), owner, id, "Checking it now", "reply", false, "k1"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.dom.ReplyTicket(context.Background(), owner, id, "internal: looks like a duplicate", "note", false, "k2"); err != nil {
		t.Fatal(err)
	}
	code, out = m.call(http.MethodGet, "/api/mini/v1/support/tickets/"+id, tok, nil)
	msgs, _ := out["messages"].([]any)
	if code != http.StatusOK || len(msgs) != 3 || strings.Contains(fmt.Sprint(out), "internal:") {
		t.Fatalf("thread: %d %v", code, out)
	}
	var kinds []string
	for _, x := range msgs {
		kinds = append(kinds, x.(map[string]any)["kind"].(string))
	}
	if strings.Join(kinds, ",") != "customer,customer,staff" {
		t.Fatalf("thread order: %v", kinds)
	}
	if msgs[2].(map[string]any)["body"] != "Checking it now" {
		t.Fatalf("staff reply: %v", msgs[2])
	}

	// A ticket is its customer's alone, and the id is checked before any query.
	other := m.signIn("tg:3202:tina")
	if code, _ := m.call(http.MethodGet, "/api/mini/v1/support/tickets/"+id, other, nil); code != http.StatusNotFound {
		t.Fatalf("another customer reads the thread: %d", code)
	}
	if code, _ := m.call(http.MethodPost, "/api/mini/v1/support/tickets/"+id+"/close", other, map[string]any{}); code != http.StatusNotFound {
		t.Fatalf("another customer closes it: %d", code)
	}
	if code, _ := m.call(http.MethodGet, "/api/mini/v1/support/tickets/not-an-id", tok, nil); code != http.StatusNotFound {
		t.Fatalf("a malformed id: %d", code)
	}
	if code, _ := m.call(http.MethodGet, "/api/mini/v1/support/tickets/"+id, "", nil); code != http.StatusUnauthorized {
		t.Fatalf("thread without a session: %d", code)
	}
	if _, o := m.call(http.MethodGet, "/api/mini/v1/support", other, nil); o["open"] != nil {
		t.Fatalf("another customer sees a ticket: %v", o)
	}

	// Closing: it stops being the open one, a new message starts a new ticket.
	code, out = m.call(http.MethodPost, "/api/mini/v1/support/tickets/"+id+"/close", tok, map[string]any{})
	if closed, _ := out["ticket"].(map[string]any); code != http.StatusOK || closed["status"] != "closed" {
		t.Fatalf("close: %d %v", code, out)
	}
	if _, o := m.call(http.MethodGet, "/api/mini/v1/support", tok, nil); o["open"] != nil {
		t.Fatalf("a closed ticket is still open: %v", o)
	}
	code, out = m.call(http.MethodPost, "/api/mini/v1/support/messages", tok, map[string]any{"text": "One more thing"})
	again, _ := out["ticket"].(map[string]any)
	if code != http.StatusOK || out["new_ticket"] != true || again["id"] == id || again["category"] != "other" {
		t.Fatalf("a message after closing: %d %v", code, out)
	}
}

// A reseller application is made in the bot, not picked here: the app lists
// only the three topics, files a message that names the reseller category as
// "other", and shows an application the bot filed under its own category, where
// a message written in the app joins it (one open conversation).
func TestMiniAppSupportAndResellerApplications(t *testing.T) {
	m := newMiniFixture(t, true)
	ctx := context.Background()
	tok := m.signIn("tg:3211:nadia")
	_, out := m.call(http.MethodGet, "/api/mini/v1/support", tok, nil)
	if cats, _ := out["categories"].([]any); fmt.Sprint(cats) != "[payment service other]" {
		t.Fatalf("topics to pick: %v", out["categories"])
	}
	code, out := m.call(http.MethodPost, "/api/mini/v1/support/messages", tok, map[string]any{"text": "I want to be a reseller", "category": "reseller"})
	filed, _ := out["ticket"].(map[string]any)
	if code != http.StatusOK || filed["category"] != "other" {
		t.Fatalf("a reseller application filed from the app: %d %v", code, out)
	}
	if code, _ := m.call(http.MethodPost, "/api/mini/v1/support/tickets/"+filed["id"].(string)+"/close", tok, map[string]any{}); code != http.StatusOK {
		t.Fatalf("close: %d", code)
	}

	// The bot's application: its category is shown, and the app's message joins it.
	user, err := m.st.GetUserByTelegramID(ctx, m.st.Conn(), 3211)
	if err != nil {
		t.Fatal(err)
	}
	app, err := m.dom.AddCustomerMessage(ctx, domain.CustomerMessage{UserID: user.ID, Category: domain.CategoryReseller, Text: "I run a shop", TGMessageID: 5})
	if err != nil || app.Ticket.Category != "reseller" {
		t.Fatalf("the application: %+v %v", app, err)
	}
	code, out = m.call(http.MethodGet, "/api/mini/v1/support", tok, nil)
	open, _ := out["open"].(map[string]any)
	if code != http.StatusOK || open["id"] != app.Ticket.ID || open["category"] != "reseller" {
		t.Fatalf("the application in the list: %d %v", code, out)
	}
	if cats, _ := out["categories"].([]any); len(cats) != 3 {
		t.Fatalf("topics to pick beside an application: %v", out["categories"])
	}
	code, out = m.call(http.MethodGet, "/api/mini/v1/support/tickets/"+app.Ticket.ID, tok, nil)
	if tk, _ := out["ticket"].(map[string]any); code != http.StatusOK || tk["category"] != "reseller" {
		t.Fatalf("the application's thread: %d %v", code, out)
	}
	code, out = m.call(http.MethodPost, "/api/mini/v1/support/messages", tok, map[string]any{"text": "My shop is on Instagram", "category": "payment"})
	joined, _ := out["ticket"].(map[string]any)
	if code != http.StatusOK || out["new_ticket"] != false || joined["id"] != app.Ticket.ID || joined["category"] != "reseller" {
		t.Fatalf("a message beside the application: %d %v", code, out)
	}
}

// limits.tickets_per_day counts the messages the customer wrote in the last 24
// hours, whichever door they came through; 0 lifts it.
func TestMiniAppSupportDailyLimit(t *testing.T) {
	m := newMiniFixture(t, true)
	m.setting("limits.tickets_per_day", "2")
	tok := m.signIn("tg:3301:rana")
	send := func(text string) (int, map[string]any) {
		return m.call(http.MethodPost, "/api/mini/v1/support/messages", tok, map[string]any{"text": text})
	}
	for i := 1; i <= 2; i++ {
		if code, out := send(fmt.Sprintf("message %d", i)); code != http.StatusOK {
			t.Fatalf("message %d: %d %v", i, code, out)
		}
	}
	code, out := send("message 3")
	if code != http.StatusTooManyRequests || out["error"] != "daily_limit" {
		t.Fatalf("over the limit: %d %v", code, out)
	}
	// Another customer has a count of their own.
	if code, out := m.call(http.MethodPost, "/api/mini/v1/support/messages", m.signIn("tg:3302:sam"), map[string]any{"text": "hi"}); code != http.StatusOK {
		t.Fatalf("another customer: %d %v", code, out)
	}
	// Messages older than a day do not count.
	m.sql(`UPDATE core.ticket_messages SET created_at = now() - interval '25 hours'
		WHERE ticket_id IN (SELECT t.id FROM core.tickets t JOIN core.users u ON u.id = t.user_id WHERE u.telegram_id = 3301)`)
	if code, out := send("message 4"); code != http.StatusOK {
		t.Fatalf("after a day: %d %v", code, out)
	}
	// 0: no limit.
	m.setting("limits.tickets_per_day", "0")
	for i := 5; i <= 8; i++ {
		if code, out := send(fmt.Sprintf("message %d", i)); code != http.StatusOK {
			t.Fatalf("message %d without a limit: %d %v", i, code, out)
		}
	}
}
