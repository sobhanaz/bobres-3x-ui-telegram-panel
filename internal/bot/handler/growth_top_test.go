package handler

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"testing"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// board is what the Top users screen shows now: the message with the board.
func (p *person) board() *msg {
	p.w.t.Helper()
	return p.lastWith("Top inviters")
}

// The board shows the top five (a tie to whoever joined the bot first), the
// viewer's own invites on a button, and the others a page at a time; a name and
// an id are masked, and nobody who is staff, a reseller or banned is on it.
func TestTopUsersBoard(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	owner.text("/set referral.reward_percent 10")
	owner.sees("referral.reward_percent")

	// The people ranked, in the order they joined (the tie of the first two
	// goes to the first), with real-looking ids; and three who are not.
	type inv struct {
		id      int64
		name    string
		invites int
	}
	ranked := []inv{
		{7100000001, "Parisa_Ahmad", 6}, {7100000002, "naftali_474", 6}, {7100000003, "", 5},
		{7100000004, "rezahil", 4}, {7100000005, "zeinab00", 3}, {7100000006, "sixth_place", 2}, {7100000007, "seventh_one", 1},
	}
	for _, c := range ranked {
		w.customerNamed(c.id, c.name, "en")
		w.invite(c.id, c.invites)
	}
	for _, c := range []inv{{7100000008, "owner_like", 20}, {7100000009, "bad_actor", 15}, {7100000010, "shop_ali", 12}} {
		w.customerNamed(c.id, c.name, "en")
		w.invite(c.id, c.invites)
	}
	w.role(7100000008, "admin")
	w.setStatusOf(7100000009, "banned")
	w.makeReseller(7100000010, 0, 10, 0)

	viewer := w.customerNamed(7100000011, "just_a_viewer", "en")
	viewer.text("/menu")
	viewer.press("top")
	screen := viewer.board()
	for _, want := range []string{
		"🎗 <b>Top inviters</b>\n\n",
		"<b>1.</b> 👤 " + isolated("Par***mad") + " (" + isolated("710***001") + ")\n🔗 Invited: <b>6</b>",
		"<b>2.</b> 👤 " + isolated("naf***474") + " (" + isolated("710***002") + ")\n🔗 Invited: <b>6</b>",
		"<b>3.</b> 👤 " + isolated("***") + " (" + isolated("710***003") + ")\n🔗 Invited: <b>5</b>",
		"<b>4.</b> 👤 " + isolated("re***il") + " (" + isolated("710***004") + ")\n🔗 Invited: <b>4</b>",
		"<b>5.</b> 👤 " + isolated("ze***00") + " (" + isolated("710***005") + ")\n🔗 Invited: <b>3</b>",
		"invite your friends with your link and get 10% of their first purchase",
	} {
		if !strings.Contains(screen.text, want) {
			t.Fatalf("the board lacks %q:\n%s", want, screen.text)
		}
	}
	if strings.Contains(screen.text, "ranks") || strings.Contains(screen.text, "⭐") {
		t.Fatalf("page 1 names a range or the viewer: %s", screen.text)
	}
	if got := strings.Join(screen.labels(), "|"); got != "🔗 Your invites: 0|👥 Others|"+w.h.cat.T("en", "btn.home") {
		t.Fatalf("buttons: %q", got)
	}
	if cb := callbacks(screen); !slices.Equal(cb, []string{"ref", "top:2", "home"}) {
		t.Fatalf("callbacks: %v", cb)
	}
	if styleOf(t, screen, "top:2") != "" || styleOf(t, screen, "ref") != "" {
		t.Fatal("a navigation button has a color")
	}

	// The rest, a page at a time: ranks 6 and 7, with the way back.
	viewer.press("top:2")
	page2 := viewer.shown()
	if !strings.Contains(page2.text, "Top inviters</b> · ranks 6 to 7\n\n") ||
		!strings.Contains(page2.text, "<b>6.</b> 👤 "+isolated("six***ace")+" ("+isolated("710***006")+")\n🔗 Invited: <b>2</b>") ||
		!strings.Contains(page2.text, "<b>7.</b> 👤 "+isolated("sev***one")+" ("+isolated("710***007")+")\n🔗 Invited: <b>1</b>") {
		t.Fatalf("page 2: %s", page2.text)
	}
	if cb := callbacks(page2); !slices.Equal(cb, []string{"ref", "top:1", "home"}) {
		t.Fatalf("page 2 callbacks: %v", cb)
	}
	viewer.press("top:1")
	if !strings.Contains(viewer.shown().text, "<b>1.</b> 👤") || strings.Contains(viewer.shown().text, "ranks") {
		t.Fatalf("back to page 1: %s", viewer.shown().text)
	}

	// Nobody who is staff, a reseller or banned is on it, however many they
	// invited; and no username or Telegram id of anyone is shown whole, to
	// the viewer or in any of the messages.
	all := viewer.allText()
	for _, hidden := range []string{"own***ike", "bad***tor", "sh***li", "7100000008", "7100000009", "7100000010"} {
		if strings.Contains(all, hidden) {
			t.Fatalf("someone who must not be on the board is: %q", hidden)
		}
	}
	for _, c := range ranked {
		if (c.name != "" && strings.Contains(all, c.name)) || strings.Contains(all, strconv.FormatInt(c.id, 10)) {
			t.Fatalf("a whole name or id is shown: %v", c)
		}
	}

	// The viewer's own invites are on the button, and when they are ranked their
	// place is marked: two invites tie with sixth_place, who joined first.
	w.invite(7100000011, 2)
	viewer.press("home")
	viewer.press("top")
	if got := viewer.board().labels()[0]; got != "🔗 Your invites: 2" {
		t.Fatalf("the viewer's invites: %q", got)
	}
	viewer.press("top:2")
	if page := viewer.shown().text; !strings.Contains(page, "ranks 6 to 8") ||
		!strings.Contains(page, "<b>7.</b> 👤 "+isolated("jus***wer")+" ("+isolated("710***011")+") ⭐ you\n🔗 Invited: <b>2</b>") {
		t.Fatalf("the viewer on page 2: %s", page)
	}
	// The button opens their invite screen (and the card to forward).
	viewer.press("ref")
	viewer.lastWith("Invite friends")
	viewer.lastWith("forward the message above")
}

// The board in Persian: Persian digits and text, the masked name and id kept
// whole by isolates, the same places.
func TestTopUsersBoardInPersian(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	owner.text("/set referral.reward_percent 25")
	owner.sees("referral.reward_percent")
	w.customerNamed(7100000021, "Parisa_Ahmad", "en")
	w.invite(7100000021, 12)
	fa := w.customerNamed(7100000022, "", "fa")
	fa.text("/menu")
	fa.press("top")
	screen := fa.lastWith("برترین کاربران ربات در دعوت از دوستان")
	for _, want := range []string{
		"\u200f<b>۱.</b> 👤 " + isolated("Par***mad") + " (" + isolated("710***021") + ")\n🔗 تعداد دعوت: <b>۱۲</b> نفر",
		"و ۲۵٪ از اولین خرید آن\u200cها را به کیف پول خود بگیرید.",
	} {
		if !strings.Contains(screen.text, want) {
			t.Fatalf("the Persian board lacks %q:\n%s", want, screen.text)
		}
	}
	if got := strings.Join(screen.labels(), "|"); got != "🔗 تعداد دعوت\u200cهای شما: ۰|"+w.h.cat.T("fa", "btn.home") {
		t.Fatalf("buttons: %q", got)
	}
}

// The board is empty until someone invites someone, and says so.
func TestTopUsersBoardWithNoReferrals(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	owner.text("/set referral.reward_percent 10")
	owner.sees("referral.reward_percent")
	u := w.customer(7100000031, "alone")
	u.text("/menu")
	u.press("top")
	if got := u.shown().text; got != w.h.cat.T("en", "ref.top_empty") {
		t.Fatalf("an empty board: %q", got)
	}
	if cb := callbacks(u.shown()); !slices.Equal(cb, []string{"ref", "home"}) {
		t.Fatalf("an empty board's buttons: %v", cb)
	}
	if got := u.shown().labels()[0]; got != "🔗 Your invites: 0" {
		t.Fatalf("their invites: %q", got)
	}
}

// The button is there only while the referral program is on and the owner has
// not switched the board off, on the home screen and the bottom menu alike; a
// tap that comes after it went (an old menu, or a switch core knows before the
// bot does) is answered with a popup and the home screen, never with the board.
func TestTopUsersButtonFollowsTheSwitches(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	cat := w.h.cat
	u := w.customer(7100000041, "switcher")
	w.customerNamed(7100000042, "someone_here", "en")
	w.invite(7100000042, 1)
	label := cat.T("en", "btn.top_users")
	homeHas := func(want bool) {
		t.Helper()
		u.text("/menu")
		if got := hasCallback(u.shown(), "top"); got != want {
			t.Fatalf("the home screen's Top users button: %v, want %v (%v)", got, want, callbacks(u.shown()))
		}
		var inMenu bool
		for _, row := range u.lastMenu().Keyboard {
			for _, b := range row {
				inMenu = inMenu || b.Text == label
			}
		}
		if inMenu != want {
			t.Fatalf("the bottom menu's Top users item: %v, want %v", inMenu, want)
		}
	}

	homeHas(false) // the referral program is off: no reward set
	u.tap("top")   // an old button anyway
	u.toast(cat.T("en", "ref.top_off"))
	if !strings.Contains(u.shown().text, "Main menu") {
		t.Fatalf("a refused tap shows %q", u.shown().text)
	}

	owner.text("/set referral.reward_percent 10")
	owner.sees("referral.reward_percent")
	homeHas(true)
	u.press("top")
	u.board()

	// Core knows first: the owner switches the board off outside the bot's
	// settings refresh, so the bot still shows the button; the tap is refused.
	w.setInCore("growth.leaderboard.enabled", "false")
	u.text("/menu")
	if !hasCallback(u.shown(), "top") {
		t.Fatal("the bot knew the switch before its refresh")
	}
	u.press("top")
	u.toast(cat.T("en", "ref.top_off"))
	if got := u.shown().text; !strings.Contains(got, "Main menu") {
		t.Fatalf("a tap on a board that is off showed %q", got)
	}
	if err := w.h.RefreshSettings(context.Background()); err != nil {
		t.Fatal(err)
	}
	homeHas(false)

	owner.text("/set growth.leaderboard.enabled true")
	owner.sees("Saved")
	homeHas(true)
	owner.text("/set referral.reward_percent 0")
	owner.sees("Saved")
	homeHas(false)
	// The bottom menu's own label routes the same way: off, it is refused.
	u.tapMenu("en", "btn.top_users")
	u.lastHas(cat.T("en", "ref.top_off"))
}

// topOlderCore is a core that does not know the Top users call yet (the bot was
// updated first).
type topOlderCore struct{ corev1.CoreServiceClient }

func (topOlderCore) GetTopInviters(context.Context, *corev1.GetTopInvitersRequest, ...grpc.CallOption) (*corev1.TopInviters, error) {
	return nil, status.Error(codes.Unimplemented, "unknown method")
}

// A bot updated before its core answers a tap on Top users with "not available".
func TestTopUsersWithACoreOlderThanTheBot(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	owner.text("/set referral.reward_percent 10")
	owner.sees("referral.reward_percent")
	u := w.customer(7100000051, "old_core")
	u.text("/menu")
	w.h.core = topOlderCore{w.stack.Core}
	u.press("top")
	u.toast(w.h.cat.T("en", "ref.top_off"))
	u.lastHas("Main menu")
}

// A customer in maintenance, or outside the required channel, gets the gate's
// answer to the button and to the bottom menu's item, never the board.
func TestTopUsersPassesTheGates(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	owner.text("/set referral.reward_percent 10")
	owner.sees("referral.reward_percent")
	u := w.customer(7100000061, "gated")
	u.text("/menu")
	owner.text("/set maintenance.enabled true")
	owner.sees("Saved")
	u.press("top")
	u.toast("under maintenance")
	if strings.Contains(u.allText(), "Top inviters") {
		t.Fatal("the board was shown in maintenance")
	}
	owner.text("/set maintenance.enabled false")
	owner.text("/set join.channel @testchan")
	owner.sees("Saved")
	u.tapMenu("en", "btn.top_users")
	u.lastHas("Join our channel")
	if strings.Contains(u.allText(), "Top inviters") {
		t.Fatal("the board was shown to someone outside the channel")
	}
	w.tg.setMember(7100000061, "member")
	u.press("jn")
	u.tapMenu("en", "btn.top_users")
	u.board()
}
