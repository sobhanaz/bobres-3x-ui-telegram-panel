package tests

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tgauth"
)

// The store's required channel in the Mini App, end to end: core's gate, the
// client that asks the bot, the bot's endpoint, its Telegram client and a fake
// Telegram that knows who is in which channel. Only a real Telegram is missing
// (docs/17-mini-app.md lists what a phone has to prove).

// signIn tries to sign a customer in as Telegram's init data would, and says how it went.
func (w *miniWorld) signIn(telegramID int64, username string) (int, map[string]any) {
	w.t.Helper()
	initData := tgauth.Sign(miniBotToken, tgauth.User{ID: telegramID, Username: username, FirstName: username, LanguageCode: "en"}, time.Now(), nil)
	return (&miniCustomer{w: w}).do(http.MethodPost, "/api/mini/v1/auth", map[string]any{"init_data": initData})
}

func TestMiniAppHoldsTheRequiredChannelThroughTheBotAndTelegram(t *testing.T) {
	w := newMiniWorld(t)
	w.fake.AddChannel("@mynews")
	w.set("join.channel", "@mynews")

	// Outside the channel: Telegram says "left", the bot says no, core sends the customer to the channel.
	code, out := w.signIn(5001, "sam")
	if code != http.StatusForbidden || out["error"] != "join_required" || out["join_url"] != "https://t.me/mynews" || out["token"] != nil {
		t.Fatalf("a customer outside the channel: %d %v", code, out)
	}

	// Joined: the very next sign-in works (a non-member was not remembered), and so do the calls.
	w.fake.SetMember("@mynews", 5001, "member")
	sam := w.open(5001, "sam")
	sam.must(http.MethodGet, "/api/mini/v1/plans", nil)

	// Staff are not asked: the owner is not in the channel.
	w.open(ownerTG, "owner")

	// A private channel is asked by its id and sent by its invite link, never by its id.
	w.fake.AddChannel("-1001234567890")
	w.set("join.link", "https://t.me/+AbCdEfGhIjKlMnOp")
	w.set("join.channel", "-1001234567890")
	code, out = w.signIn(5002, "kim")
	if code != http.StatusForbidden || out["error"] != "join_required" || out["join_url"] != "https://t.me/+AbCdEfGhIjKlMnOp" || strings.Contains(fmt.Sprint(out), "1234567890") {
		t.Fatalf("a private channel: %d %v", code, out)
	}
	w.fake.SetMember("-1001234567890", 5002, "administrator")
	w.open(5002, "kim")

	// A channel Telegram will not show the bot (it is not its administrator, or there is no such channel): the
	// customer is let in, as the bot's own gate lets them in.
	w.set("join.channel", "@hiddenchannel")
	w.set("join.link", "")
	w.open(5003, "lee")
}

// Several required chats, end to end: core asks the bot about each one, the bot asks the fake Telegram,
// and the customer is told only the chats they still lack.
func TestMiniAppHoldsSeveralRequiredChatsThroughTheBotAndTelegram(t *testing.T) {
	w := newMiniWorld(t)
	for _, c := range []string{"@mynews", "-1001234567890", "@team_group"} {
		w.fake.AddChannel(c)
	}
	owner, err := w.stack.Store.GetUserByTelegramID(context.Background(), w.stack.Store.Conn(), ownerTG)
	if err != nil {
		owner = nil // the audit entry then has no actor; the gate is what is tested
	}
	if _, err := w.stack.Domain.SetJoinChats(context.Background(), owner, []domain.JoinChat{
		{Chat: "@mynews", Label: "News"},
		{Chat: "-1001234567890", Link: "https://t.me/+AbCdEfGhIjKlMnOp", Label: "VIP"},
		{Chat: "@team_group"},
	}); err != nil {
		t.Fatal(err)
	}

	code, out := w.signIn(5101, "sam")
	chats, _ := out["join_chats"].([]any)
	if code != http.StatusForbidden || out["error"] != "join_required" || len(chats) != 3 || strings.Contains(fmt.Sprint(out), "1234567890") {
		t.Fatalf("a customer outside all three: %d %v", code, out)
	}
	w.fake.SetMember("@mynews", 5101, "member")
	w.fake.SetMember("@team_group", 5101, "member")
	code, out = w.signIn(5101, "sam")
	chats, _ = out["join_chats"].([]any)
	if code != http.StatusForbidden || len(chats) != 1 || out["join_url"] != "https://t.me/+AbCdEfGhIjKlMnOp" {
		t.Fatalf("a customer lacking the private chat: %d %v", code, out)
	}
	w.fake.SetMember("-1001234567890", 5101, "administrator")
	w.open(5101, "sam").must(http.MethodGet, "/api/mini/v1/plans", nil)
	w.open(ownerTG, "owner") // staff are not asked
}
