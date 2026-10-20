package handler

import (
	"context"
	"regexp"
	"slices"
	"strings"
	"testing"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// The wallet screen starts with the customer's account: their numeric Telegram
// id, how many services they have (deleted ones do not count) and the day they
// joined the bot, above the balance; the buttons are the wallet's own.
func TestWalletShowsTheAccount(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	u := w.customer(7200000001, "acct")
	joined := w.storeUser(7200000001).CreatedAt
	block := func(services string) string {
		return "🧑‍💻 <b>Your account</b>\n🆔 Numeric ID: <code>7200000001</code>\n🛍 Services: " + services +
			"\n📅 Joined the bot: " + i18n.Date("en", joined)
	}

	u.press("wallet")
	if got, want := u.shown().text, block("0")+"\n\n👛 <b>Wallet</b>\nBalance: <b>0 Toman</b>"; got != want {
		t.Fatalf("the wallet screen:\n%s\nwant:\n%s", got, want)
	}
	if cb := callbacks(u.shown()); !slices.Equal(cb, []string{"wtop", "whist", "home"}) {
		t.Fatalf("the wallet's buttons: %v", cb)
	}

	w.seedService(7200000001, "")
	u.press("home")
	u.press("wallet")
	if got := u.shown().text; !strings.HasPrefix(got, block("1")+"\n\n") {
		t.Fatalf("with a service: %s", got)
	}
	w.seedService(7200000001, "")
	gone := w.seedService(7200000001, "")
	w.setStatus(gone, "deleted") // a deleted service is not one of theirs any more
	u.press("home")
	u.press("wallet")
	if got := u.shown().text; !strings.HasPrefix(got, block("2")+"\n\n") {
		t.Fatalf("two services and a deleted one: %s", got)
	}
}

// In Persian the account lines are Persian, the id stays in Latin digits
// (it is copied), and the date is the Jalali one.
func TestWalletShowsTheAccountInPersian(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	fa := w.customerNamed(7200000002, "", "fa")
	joined := w.storeUser(7200000002).CreatedAt
	fa.press("wallet")
	want := "🧑‍💻 <b>اطلاعات حساب کاربری شما:</b>\n🆔 آیدی عددی: <code>7200000002</code>\n🛍 تعداد سرویس\u200cها: ۰\n📅 تاریخ ورود به ربات: " +
		i18n.Date("fa", joined) + "\n\n👛 <b>کیف پول</b>\nموجودی: <b>۰ تومان</b>"
	if got := fa.shown().text; got != want {
		t.Fatalf("the Persian wallet screen:\n%s\nwant:\n%s", got, want)
	}
	// A Jalali date: a Persian month name and a year of the 14th century of
	// the solar Hijri calendar, not the Gregorian 20xx.
	if !regexp.MustCompile(`📅 تاریخ ورود به ربات: [۰-۹]+ \S+ ۱۴[۰-۹]{2}\n`).MatchString(fa.shown().text) {
		t.Fatalf("not a Jalali date: %s", fa.shown().text)
	}
	w.seedService(7200000002, "")
	w.seedService(7200000002, "")
	fa.press("home")
	fa.press("wallet")
	if !strings.Contains(fa.shown().text, "🛍 تعداد سرویس\u200cها: ۲\n") {
		t.Fatalf("two services in Persian digits: %s", fa.shown().text)
	}
}

// A reseller's wallet has the account lines too, and their credit under the
// balance.
func TestResellersWalletShowsTheAccount(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	rs := w.customer(7200000003, "shopkeeper")
	w.makeReseller(7200000003, 0, 10, 200_000)
	rs.text("/menu")
	rs.press("wallet")
	text := rs.shown().text
	if !strings.HasPrefix(text, "🧑‍💻 <b>Your account</b>\n🆔 Numeric ID: <code>7200000003</code>\n") ||
		!strings.Contains(text, "👛 <b>Wallet</b>\nBalance: <b>0 Toman</b>\n") || !strings.Contains(text, "200,000 Toman") {
		t.Fatalf("a reseller's wallet: %s", text)
	}
}

// subsFailCore cannot count services (a call of the wallet's account lines that
// fails must cost the customer nothing else).
type subsFailCore struct{ corev1.CoreServiceClient }

func (subsFailCore) ListSubscriptions(context.Context, *corev1.ListSubscriptionsRequest, ...grpc.CallOption) (*corev1.ListSubscriptionsResponse, error) {
	return nil, status.Error(codes.Unavailable, "down")
}

// A figure that cannot be read shows a dash; the balance and the buttons are
// still there.
func TestWalletShowsTheBalanceWhenTheServicesCannotBeCounted(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	u := w.customer(7200000004, "flaky")
	w.h.core = subsFailCore{w.stack.Core}
	u.press("wallet")
	text := u.shown().text
	if !strings.Contains(text, "🛍 Services: —\n") || !strings.Contains(text, "Balance: <b>0 Toman</b>") || !hasCallback(u.shown(), "wtop") {
		t.Fatalf("the wallet with services that cannot be counted: %s", text)
	}
}

// --- the invite promo card ---

var inviteLinkRe = regexp.MustCompile(`https://t\.me/test_vpn_bot\?start=r_([a-z0-9]+)`)

// "Invite friends" sends, besides its screen, a card to forward: the store's
// promo text ending in the customer's own invite link as a text link, as a
// message of its own with no buttons, then a line that says to forward it.
// The screen keeps its Share button.
func TestInviteSendsAForwardableCard(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	cat := w.h.cat
	owner.text("/set referral.reward_percent 10")
	owner.sees("referral.reward_percent")
	u := w.customer(7200000011, "ina")
	u.text("/menu")
	before := len(w.tg.all(u.id))
	u.press("ref")

	got := w.tg.all(u.id)[before:]
	if len(got) != 2 { // the screen is the home menu edited in place
		t.Fatalf("%d new messages, want the card and the line about forwarding it: %v", len(got), texts(got))
	}
	screen := u.lastWith("Invite friends")
	m := inviteLinkRe.FindStringSubmatch(screen.text)
	if m == nil {
		t.Fatalf("no invite link on the screen: %q", screen.text)
	}
	link := m[0]
	if !offers(screen, "https://t.me/share/url") || !hasCallback(screen, "home") {
		t.Fatalf("the screen's buttons: %v", buttons(screen))
	}
	card, hint := got[0], got[1]
	wantCard := "✨ Get safe, fast services without interruption, through the <b>TestVPN</b> bot\n\n" +
		`<a href="` + link + `">🚀 Buy a service</a>`
	if card.text != wantCard {
		t.Fatalf("the card:\n%s\nwant:\n%s", card.text, wantCard)
	}
	if card.kb != nil || hint.kb != nil {
		t.Fatal("the card to forward or the line after it has buttons")
	}
	if hint.text != cat.T("en", "ref.promo_hint") || !strings.Contains(hint.text, "forward the message above") {
		t.Fatalf("the line about forwarding: %q", hint.text)
	}
	// The link in the card is the customer's own: a friend who joins with it is
	// theirs.
	friend := w.person(7200000012, "fred")
	friend.text("/start r_" + m[1])
	friend.press("lang:en")
	if w.storeUser(7200000012).ReferredBy == nil || *w.storeUser(7200000012).ReferredBy != w.storeUser(7200000011).ID {
		t.Fatal("the link in the card is not the customer's")
	}

	// Each time the screen is opened the card comes again.
	u.press("home")
	u.press("ref")
	if n := u.count("Get safe, fast services"); n != 2 {
		t.Fatalf("%d cards after two openings", n)
	}
}

// The card is in the customer's language, with the store's own name; the owner
// edits it like any text, and a text that would break it is refused.
func TestInvitePromoCardTextsAreTheOwners(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	owner.text("/set referral.reward_percent 10")
	owner.text("/set branding.name Tom & Jerry VPN")
	owner.sees("Saved")
	fa := w.customerNamed(7200000021, "", "fa")
	fa.text("/menu")
	fa.press("ref")
	link := inviteLinkRe.FindString(fa.lastWith("دعوت از دوستان").text)
	wantFA := "✨ دریافت سرویس\u200cهای ایمن و پرسرعت، بدون معطلی، از طریق ربات <b>Tom &amp; Jerry VPN</b>\n\n" +
		`<a href="` + link + `">🚀 خرید سرویس</a>`
	if got := fa.lastWith("دریافت سرویس\u200cهای ایمن").text; got != wantFA {
		t.Fatalf("the Persian card:\n%s\nwant:\n%s", got, wantFA)
	}
	if got := fa.last().text; got != w.h.cat.T("fa", "ref.promo_hint") {
		t.Fatalf("the Persian line about forwarding: %q", got)
	}

	// The owner's own words, with the brand and the link where they say.
	owner.text("/set texts.en.ref.promo 🔥 Join <b>{brand}</b> now: {link}")
	owner.sees("Saved")
	owner.text("/set texts.en.ref.promo_link Open the bot")
	owner.sees("Saved")
	u := w.customer(7200000022, "ina2")
	u.text("/menu")
	u.press("ref")
	mine := inviteLinkRe.FindString(u.lastWith("Invite friends").text)
	if got, want := u.lastWith("Join <b>").text, `🔥 Join <b>Tom &amp; Jerry VPN</b> now: <a href="`+mine+`">Open the bot</a>`; got != want {
		t.Fatalf("the owner's card:\n%s\nwant:\n%s", got, want)
	}
	// A text without the link, with it where markup is not allowed, or with a
	// placeholder the card does not have, is not accepted: the card stays.
	for _, bad := range []struct{ command, problem string }{
		{"/set texts.en.ref.promo Join {brand} today", "placeholder_missing (link)"},
		{"/set texts.en.ref.promo <code>{link}</code> {brand}", "tag_in_code"},
		{"/set texts.en.ref.promo {brand} {link} {more}", "placeholder_unknown (more)"},
	} {
		owner.text(bad.command)
		if got := owner.last().text; !strings.Contains(got, bad.problem) || strings.Contains(got, "Saved") {
			t.Fatalf("%q: %q, want the problem %q", bad.command, got, bad.problem)
		}
	}
	if got := w.h.cat.T("en", "ref.promo", "brand", "B", "link", "L"); got != "🔥 Join <b>B</b> now: L" {
		t.Fatalf("a refused text changed the card: %q", got)
	}
}

// With the referral program off there is no invite and no card; a reseller's
// own link screen sends one too, with their link, whatever the store's
// referral reward is.
func TestInviteCardOnlyWithALink(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	u := w.customer(7200000031, "nolink")
	u.text("/menu")
	u.tap("ref") // an old button: the program is off
	u.lastHas("The referral program is not active right now.")
	if u.count("Get safe, fast services") != 0 {
		t.Fatal("a card without a program")
	}

	rs := w.customer(7200000032, "reseller_link")
	w.makeReseller(7200000032, 0, 10, 0)
	rs.text("/menu")
	rs.press("rs")
	before := len(w.tg.all(rs.id))
	rs.press("rs:link")
	got := w.tg.all(rs.id)[before:]
	if len(got) != 2 {
		t.Fatalf("a reseller's link screen sent %d new messages: %v", len(got), texts(got))
	}
	screen := rs.lastWith("Your invite link")
	mine := inviteLinkRe.FindString(screen.text)
	if mine == "" || !strings.Contains(got[0].text, `<a href="`+mine+`">`) || got[0].kb != nil {
		t.Fatalf("a reseller's card: %q (their link %q)", got[0].text, mine)
	}
	owner.text("/set referral.reward_percent 10")
	owner.sees("referral.reward_percent")
	rs.text("/menu")
	rs.tap("ref") // an old Invite button: their own link again
	rs.lastWith("Your invite link")
}
