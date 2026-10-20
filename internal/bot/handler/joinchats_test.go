package handler

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
)

// Several required channels and groups (joinchats.go): the first is the setting
// join.channel, the others come with the settings, and a customer must be in all
// of them.

// require saves the required chats as the owner would in the dashboard and
// makes the bot read them.
func (w *world) require(list ...domain.JoinChat) {
	w.t.Helper()
	ctx := context.Background()
	st := w.stack.Store
	owner, err := st.GetUserByTelegramID(ctx, st.Conn(), ownerID)
	if err != nil {
		w.t.Fatal(err)
	}
	if _, err := w.stack.Domain.SetJoinChats(ctx, owner, list); err != nil {
		w.t.Fatalf("require: %v", err)
	}
	if err := w.h.RefreshSettings(ctx); err != nil {
		w.t.Fatal(err)
	}
}

// endTrial makes the free tier apply (one required chat).
func (w *world) endTrial() {
	w.t.Helper()
	if _, err := w.stack.Store.InstallID(context.Background(), w.stack.Store.Conn()); err != nil {
		w.t.Fatal(err)
	}
	if _, err := w.stack.Store.DB().Exec(context.Background(), `UPDATE core.install SET trial_started_at = now() - interval '31 days'`); err != nil {
		w.t.Fatal(err)
	}
	if err := w.h.RefreshSettings(context.Background()); err != nil {
		w.t.Fatal(err)
	}
}

// links lists the URL buttons of the last message the user was shown, by label.
func (p *person) links() map[string]string {
	p.w.t.Helper()
	out := map[string]string{}
	if m := p.last(); m.kb != nil {
		for _, row := range m.kb.InlineKeyboard {
			for _, b := range row {
				if b.URL != "" {
					out[b.Text] = b.URL
				}
			}
		}
	}
	return out
}

func sortedLabels(m map[string]string) string {
	var l []string
	for k := range m {
		l = append(l, k)
	}
	slices.Sort(l)
	return strings.Join(l, " | ")
}

// toastsOf lists the popups a user was shown.
func (f *fakeTG) toastsOf(id int64) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.toasts[id])
}

func threeChats() []domain.JoinChat {
	return []domain.JoinChat{
		{Chat: "@testchan", Label: "News"},
		{Chat: "-1001234567890", Link: "https://t.me/+AbCdEf123", Label: "VIP"},
		{Chat: "@team_group"},
	}
}

func TestRequiredChatsPromptListsEveryMissingChat(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	sup := w.customer(901, "sue")
	w.role(901, "support")
	u := w.customer(902, "jack")
	w.require(threeChats()...)

	// Staff are never asked.
	checks := w.tg.checks()
	sup.text("/menu")
	sup.lastHas("Main menu")
	if w.tg.checks() != checks {
		t.Fatal("staff were looked up in the chats")
	}

	// Outside all three: one message, a Join button for each with its own link, one "I've joined".
	u.tap("buy")
	u.lastHas("Join our channels")
	got := u.links()
	if len(got) != 3 || got["📢 Join News"] != "https://t.me/testchan" || got["📢 Join VIP"] != "https://t.me/+AbCdEf123" || got["📢 Join @team_group"] != "https://t.me/team_group" {
		t.Fatalf("the Join buttons: %s %v", sortedLabels(got), got)
	}
	if labels := u.last().labels(); labels[len(labels)-1] != "✅ I've joined" || len(labels) != 4 {
		t.Fatalf("buttons: %v", labels)
	}
	for _, m := range w.tg.all(902) {
		if strings.Contains(m.text+strings.Join(m.labels(), " "), "1234567890") {
			t.Fatalf("a private chat's id was shown: %q", m.text)
		}
	}

	// Joined the first and the third only: the check says which one is missing and the prompt keeps its button.
	w.tg.setChatMember("@testchan", 902, "member")
	w.tg.setChatMember("@team_group", 902, "member")
	u.tap("jn")
	u.toast("haven't joined yet: VIP")
	if strings.Contains(strings.Join(w.tg.toastsOf(902), " "), "News") || strings.Contains(strings.Join(w.tg.toastsOf(902), " "), "@team_group") {
		t.Fatalf("the popup named chats the user is in: %v", w.tg.toastsOf(902))
	}
	if got := u.links(); len(got) != 1 || got["📢 Join VIP"] == "" {
		t.Fatalf("the prompt after the check: %s", sortedLabels(got))
	}

	// Anything else in the bot is still held, and asks only about the missing chat.
	before := w.tg.askedOf("@testchan")
	u.tap("buy")
	u.lastHas("Join our channels")
	if w.tg.askedOf("@testchan") != before {
		t.Fatal("a chat the user is a member of was asked about again")
	}

	w.tg.setChatMember("-1001234567890", 902, "creator")
	u.tap("jn")
	u.lastHas("Main menu")
	u.tap("buy")
	u.lastHas("Choose a plan")
}

func TestRequiredChatWithoutAnyNameIsNumberedAndNeverShowsItsId(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	u := w.customer(911, "ivy")
	w.require(domain.JoinChat{Chat: "@testchan"}, domain.JoinChat{Chat: "-1001234567890", Link: "https://t.me/+Zz9"})
	u.tap("buy")
	if got := u.links(); got["📢 Join @testchan"] == "" || got["📢 Join channel 2"] != "https://t.me/+Zz9" {
		t.Fatalf("buttons: %s", sortedLabels(got))
	}
	u.tap("jn")
	u.toast("haven't joined yet: @testchan, channel 2")
}

// A chat the bot cannot check lets the user in for that chat only; the others still hold.
func TestRequiredChatTelegramCannotTellAbout(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	w.require(domain.JoinChat{Chat: "@testchan"}, domain.JoinChat{Chat: "@broken_chan"}, domain.JoinChat{Chat: "@other_chan"})
	w.tg.mu.Lock()
	w.tg.chatErr["@broken_chan"] = &tg.APIError{Code: 400, Description: "Bad Request: member list is inaccessible"}
	w.tg.mu.Unlock()

	u := w.customer(921, "mia")
	u.tap("buy")
	u.lastHas("Join our channels")
	if got := u.links(); len(got) != 2 || got["📢 Join @broken_chan"] != "" {
		t.Fatalf("the chat that cannot be checked was asked of the user: %s", sortedLabels(got))
	}
	w.tg.setChatMember("@testchan", 921, "member")
	w.tg.setChatMember("@other_chan", 921, "member")
	u.tap("jn")
	u.lastHas("Main menu")
	// Not remembered as a full membership: the broken chat is looked up again next time, the others are cached.
	before, brokenBefore := w.tg.askedOf("@testchan"), w.tg.askedOf("@broken_chan")
	u.tap("buy")
	u.lastHas("Choose a plan")
	if w.tg.askedOf("@testchan") != before || w.tg.askedOf("@broken_chan") != brokenBefore+1 {
		t.Fatalf("asked %d/%d times more", w.tg.askedOf("@testchan")-before, w.tg.askedOf("@broken_chan")-brokenBefore)
	}
}

// With the free tier only the first chat is enforced; the others stay saved and count again with a license.
func TestRequiredChatsFollowTheLicense(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	w.require(threeChats()...)
	w.tg.setChatMember("@testchan", 931, "member")
	u := w.customer(931, "tom")
	u.tap("buy")
	u.lastHas("Join our channels") // the trial holds all three

	past := w.tg.askedOf("-1001234567890") + w.tg.askedOf("@team_group")
	w.endTrial()
	u.tap("buy")
	u.lastHas("Choose a plan") // the free tier holds the first only
	if n := w.tg.askedOf("-1001234567890") + w.tg.askedOf("@team_group"); n != past || past == 0 {
		t.Fatalf("chats past the limit were asked about %d times after the trial (%d before)", n-past, past)
	}
	// Saved, not deleted.
	list, err := w.stack.Domain.JoinChats(context.Background())
	if err != nil || len(list.Items) != 3 || list.Items[1].Active {
		t.Fatalf("saved chats: %+v %v", list, err)
	}
}

// A single required chat is what it always was, down to the words.
func TestOneRequiredChatKeepsTheOldPrompt(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	owner.text("/set join.channel @testchan")
	owner.sees("Saved")
	u := w.customer(941, "una")
	u.tap("buy")
	u.lastHas("Join our channel")
	if strings.Contains(u.last().text, "channels") {
		t.Fatalf("one chat, but the text is for several: %q", u.last().text)
	}
	if got := u.links(); len(got) != 1 || got["📢 Join the channel"] != "https://t.me/testchan" {
		t.Fatalf("buttons: %s", sortedLabels(got))
	}
	u.tap("jn")
	u.toast("haven't joined the channel yet")
	owner.text("/set join.label Our news")
	owner.sees("Saved")
	u.tap("buy")
	if got := u.links(); got["📢 Join the channel"] == "" {
		t.Fatalf("a name on a single chat changed its button: %s", sortedLabels(got))
	}
}

// Maintenance is answered before any chat is looked up, and the prompt comes back after it.
func TestRequiredChatsComeAfterMaintenance(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	u := w.customer(951, "max")
	w.require(threeChats()...)
	owner.text("/set maintenance.enabled true")
	owner.sees("Saved")
	checks := w.tg.checks()
	u.tap("buy")
	u.toast("under maintenance")
	u.text("/menu")
	u.lastHas("under maintenance")
	u.tap("jn")
	if w.tg.checks() != checks {
		t.Fatalf("%d lookups in maintenance", w.tg.checks()-checks)
	}
	nu := w.person(952, "newcomer")
	nu.text("/start")
	nu.tap("lang:en")
	nu.lastHas("under maintenance")
	owner.text("/set maintenance.enabled false")
	u.tap("buy")
	u.lastHas("Join our channels")
	nu2 := w.person(953, "newcomer two")
	nu2.text("/start")
	nu2.tap("lang:en")
	nu2.lastHas("Join our channels")
	if got := nu2.links(); len(got) != 3 {
		t.Fatalf("a newcomer's prompt: %s", sortedLabels(got))
	}
}

// A customer with many chats does not cost an API storm: the lookups of one update run a few at a
// time, each chat once; members are remembered; a chat not joined is not asked about again by an
// ordinary update for a moment, only by the "I've joined" button.
func TestRequiredChatsAreNotAskedInAStorm(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	var list []domain.JoinChat
	for i := range 12 {
		list = append(list, domain.JoinChat{Chat: fmt.Sprintf("@chan_%02dxyz", i)})
	}
	w.require(list...)
	u := w.customer(961, "sam")
	w.tg.mu.Lock()
	w.tg.memberDelay = 15 * time.Millisecond
	w.tg.peak = 0
	w.tg.mu.Unlock()
	w.h.joinMissTTL = 5 * time.Second

	before := w.tg.checks()
	u.tap("buy")
	u.lastHas("Join our channels")
	if n := w.tg.checks() - before; n != 12 {
		t.Fatalf("%d lookups for 12 chats", n)
	}
	w.tg.mu.Lock()
	peak := w.tg.peak
	w.tg.mu.Unlock()
	if peak > joinParallelUser {
		t.Fatalf("%d lookups at once, at most %d", peak, joinParallelUser)
	}
	if len(u.links()) != 12 {
		t.Fatalf("%d Join buttons", len(u.links()))
	}

	// Another tap right away asks nothing new...
	u.tap("buy")
	u.text("hello")
	if n := w.tg.checks() - before; n != 12 {
		t.Fatalf("an ordinary update asked again: %d lookups", n)
	}
	// ...but "I've joined" always asks (the user may have joined a second ago).
	for i := range 11 {
		w.tg.setChatMember(list[i].Chat, 961, "member")
	}
	u.tap("jn")
	u.toast("haven't joined yet: @chan_11xyz")
	if n := w.tg.checks() - before; n != 12+12 {
		t.Fatalf("the check asked %d times", n-12)
	}
	if got := u.links(); len(got) != 1 {
		t.Fatalf("the prompt after the check: %s", sortedLabels(got))
	}
	// Once the last chat is joined, the user is a member of the whole list: one cache lookup per update after that.
	w.tg.setChatMember(list[11].Chat, 961, "member")
	u.tap("jn")
	u.lastHas("Main menu")
	done := w.tg.checks()
	for range 5 {
		u.tap("buy")
		u.tap("home")
	}
	if w.tg.checks() != done {
		t.Fatalf("a member of every chat was asked about again: %d lookups", w.tg.checks()-done)
	}
}

// A chat added later is asked of a member of the old list only.
func TestAddingAChatAsksOnlyAboutTheNewOne(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	w.require(domain.JoinChat{Chat: "@testchan"}, domain.JoinChat{Chat: "@second_chan"})
	w.tg.setMember(971, "member")
	u := w.customer(971, "lee")
	u.tap("buy")
	u.lastHas("Choose a plan")
	first, second := w.tg.askedOf("@testchan"), w.tg.askedOf("@second_chan")

	w.require(domain.JoinChat{Chat: "@testchan"}, domain.JoinChat{Chat: "@second_chan"}, domain.JoinChat{Chat: "@third_chan"})
	w.tg.setChatMember("@third_chan", 971, "left")
	u.tap("buy")
	u.lastHas("Join our channels")
	if w.tg.askedOf("@testchan") != first || w.tg.askedOf("@second_chan") != second || w.tg.askedOf("@third_chan") == 0 {
		t.Fatal("the chats the user was known to be in were asked about again, or the new one was not")
	}
	if got := u.links(); len(got) != 1 || got["📢 Join @third_chan"] == "" {
		t.Fatalf("the prompt: %s", sortedLabels(got))
	}
}
