package handler

import (
	"context"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/channelcards"
)

// The owner's three channels: two public, one private (its invite link is the button's channel) with no Persian text.
const threeChannels = `[{"label":{"fa":"کانال نظرات","en":"Reviews"},"post":"https://t.me/heisen_reviews/2"},` +
	`{"label":{"fa":"آموزش","en":"Learn"},"post":"https://t.me/heisen_learn/5"},` +
	`{"label":{"en":"Private club"},"channel":"https://t.me/+AbCdEfGh12","post":"https://t.me/c/1234567890/45"}]`

var (
	reviewsID = channelcards.ID("https://t.me/heisen_reviews/2")
	learnID   = channelcards.ID("https://t.me/heisen_learn/5")
	clubID    = channelcards.ID("https://t.me/c/1234567890/45")
)

// withChannels has the owner write the list of channel buttons in core, and the bot learn of it.
func (w *world) withChannels(raw string) {
	w.t.Helper()
	w.setInCore(channelcards.Setting, raw)
	if err := w.h.RefreshSettings(context.Background()); err != nil {
		w.t.Fatal(err)
	}
}

// menuStartsWith checks the first rows of the user's bottom menu: one button each, as text and color.
func (p *person) menuStartsWith(want ...[2]string) {
	p.w.t.Helper()
	m := p.lastMenu()
	if len(m.Keyboard) < len(want) {
		p.w.t.Fatalf("user %d: the bottom menu has %d rows, want at least %d: %+v", p.id, len(m.Keyboard), len(want), m.Keyboard)
	}
	for i, w := range want {
		if row := m.Keyboard[i]; len(row) != 1 || row[0].Text != w[0] || row[0].Style != w[1] {
			p.w.t.Fatalf("user %d: row %d of the bottom menu is %+v, want %q colored %q", p.id, i, row, w[0], w[1])
		}
	}
}

// viewsChannel checks that kb is the one button that opens the channel.
func viewsChannel(t *testing.T, kb *tg.Keyboard, label, url string) {
	t.Helper()
	if kb == nil || len(kb.InlineKeyboard) != 1 || len(kb.InlineKeyboard[0]) != 1 {
		t.Fatalf("keyboard under the post: %+v, want one button", kb)
	}
	if b := kb.InlineKeyboard[0][0]; b.Text != label || b.URL != url || b.CallbackData != "" || b.Style != tg.StylePrimary {
		t.Fatalf("button under the post: %+v, want %q opening %s", b, label, url)
	}
}

// The owner's channels are buttons of the menu, each on a row of its own under Buy and Free trial, in the bottom menu and on the home screen, in
// the order the owner wrote them and in the customer's language (the other where a text is missing). A customer who was here before they were
// written gets the menu with them at the next thing they do.
func TestChannelButtonsComeUnderBuyAndTrial(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	cat := w.h.cat
	early := w.customer(2003, "cards_early")
	w.withChannels(threeChannels)

	en := w.customer(2001, "cards_en")
	en.menuStartsWith([2]string{cat.T("en", "btn.buy"), tg.StyleSuccess}, [2]string{cat.T("en", "btn.trial"), tg.StyleSuccess},
		[2]string{"Reviews", tg.StylePrimary}, [2]string{"Learn", tg.StylePrimary}, [2]string{"Private club", tg.StylePrimary})

	fa := w.person(2002, "cards_fa")
	fa.text("/start")
	fa.press("lang:fa")
	fa.menuStartsWith([2]string{cat.T("fa", "btn.buy"), tg.StyleSuccess}, [2]string{cat.T("fa", "btn.trial"), tg.StyleSuccess},
		[2]string{"کانال نظرات", tg.StylePrimary}, [2]string{"آموزش", tg.StylePrimary}, [2]string{"Private club", tg.StylePrimary})

	en.text("/menu")
	rows := en.shown().kb.InlineKeyboard
	if len(rows) < 5 {
		t.Fatalf("the home screen: %+v", rows)
	}
	for i, want := range []tg.Button{
		{Text: cat.T("en", "btn.buy"), CallbackData: "buy", Style: tg.StyleSuccess},
		{Text: cat.T("en", "btn.trial"), CallbackData: "trial", Style: tg.StyleSuccess},
		{Text: "Reviews", CallbackData: "chc:" + reviewsID, Style: tg.StylePrimary},
		{Text: "Learn", CallbackData: "chc:" + learnID, Style: tg.StylePrimary},
		{Text: "Private club", CallbackData: "chc:" + clubID, Style: tg.StylePrimary},
	} {
		if len(rows[i]) != 1 || rows[i][0] != want {
			t.Errorf("home screen row %d: %+v, want %+v", i, rows[i], want)
		}
	}

	early.text(cat.T("en", "btn.wallet"))
	early.menuStartsWith([2]string{cat.T("en", "btn.buy"), tg.StyleSuccess}, [2]string{cat.T("en", "btn.trial"), tg.StyleSuccess},
		[2]string{"Reviews", tg.StylePrimary}, [2]string{"Learn", tg.StylePrimary}, [2]string{"Private club", tg.StylePrimary})
}

// A tap, on the bottom menu or on the home screen, shows a copy of the channel's post with one button, View channel, under it; the home screen
// stays as it is. A private channel's post is copied by its id and its button opens the invite link.
func TestTappingAChannelButtonShowsThePost(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	cat := w.h.cat
	w.withChannels(threeChannels)
	en := w.customer(2001, "cards_tap")
	view := cat.T("en", "btn.view_channel")

	en.text("Reviews")
	got := w.tg.copiedTo(2001)
	if len(got) != 1 || got[0].from != "@heisen_reviews" || got[0].id != 2 {
		t.Fatalf("the bottom menu's Reviews: %+v", got)
	}
	viewsChannel(t, got[0].kb, view, "https://t.me/heisen_reviews")

	en.text("/menu")
	en.press("chc:" + learnID)
	got = w.tg.copiedTo(2001)
	if len(got) != 2 || got[1].from != "@heisen_learn" || got[1].id != 5 {
		t.Fatalf("the home screen's Learn: %+v", got)
	}
	viewsChannel(t, got[1].kb, view, "https://t.me/heisen_learn")
	if !hasCallback(en.shown(), "chc:") {
		t.Errorf("the home screen was replaced by %q", en.shown().text)
	}

	en.text("Private club")
	got = w.tg.copiedTo(2001)
	if len(got) != 3 || got[2].from != "-1001234567890" || got[2].id != 45 {
		t.Fatalf("the private channel: %+v", got)
	}
	viewsChannel(t, got[2].kb, view, "https://t.me/+AbCdEfGh12")
}

// A post that cannot be copied (the bot cannot read the channel, the post was deleted) leaves the customer the button's name and the View channel
// button, never nothing; staff are told what may be wrong, since they can mend the list. A customer who blocked the bot is told nothing.
func TestChannelButtonWhenThePostCannotBeCopied(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	cat := w.h.cat
	w.withChannels(threeChannels)
	en := w.customer(2001, "cards_down")
	w.tg.failCopies("@heisen_reviews", &tg.APIError{Code: 400, Description: "Bad Request: chat not found"})

	en.text("Reviews")
	if n := len(w.tg.copiedTo(2001)); n != 0 {
		t.Fatalf("%d copies for a channel that cannot be read", n)
	}
	last := en.last()
	if last.text != "<b>Reviews</b>" {
		t.Errorf("the customer got %q", last.text)
	}
	viewsChannel(t, last.kb, cat.T("en", "btn.view_channel"), "https://t.me/heisen_reviews")

	owner.text("/menu")
	owner.press("chc:" + reviewsID)
	if got := owner.last().text; got != "<b>Reviews</b>\n\n"+cat.T("en", "channels.copy_failed") {
		t.Errorf("staff got %q", got)
	}

	w.tg.failCopies("@heisen_learn", &tg.APIError{Code: 403, Description: "Forbidden: bot was blocked by the user"})
	en.text("Learn")
	for _, m := range w.tg.all(2001) {
		if m.text == "<b>Learn</b>" {
			t.Errorf("a customer who blocked the bot got %q", m.text)
		}
	}
}

// A button the owner has since removed answers like any old button: the popup, and the home screen. So does the label of an old bottom menu
// (the bot sends the new menu first, and the tap is read as the keyboard the customer had).
func TestChannelButtonsTheOwnerRemovedAreRefused(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	cat := w.h.cat
	w.withChannels(threeChannels)
	inline := w.customer(2001, "cards_inline")
	label := w.customer(2002, "cards_label")
	inline.text("/menu")

	w.withChannels("")
	inline.press("chc:" + reviewsID)
	inline.toast(cat.T("en", "error.expired"))
	label.text("Reviews")
	label.sees(cat.T("en", "error.expired"))
	if n := len(w.tg.copiedTo(2001)) + len(w.tg.copiedTo(2002)); n != 0 {
		t.Errorf("%d copies for buttons that are gone", n)
	}
	for _, p := range []*person{inline, label} {
		for _, row := range p.lastMenu().Keyboard {
			for _, b := range row {
				if b.Text == "Reviews" || b.Text == "Learn" || b.Text == "Private club" {
					t.Errorf("user %d still has %q in the bottom menu", p.id, b.Text)
				}
			}
		}
	}
}

// A list the bot cannot read (written some way other than the dashboard, which checks it) is no buttons, never a broken menu.
func TestUnreadableChannelButtonsAreIgnored(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	w.withChannels(threeChannels)
	if n := len(w.h.channelCards()); n != 3 {
		t.Fatalf("%d cards", n)
	}
	w.h.refreshCards(`[{"label":`)
	if n := len(w.h.channelCards()); n != 0 {
		t.Errorf("%d cards from a list that cannot be read", n)
	}
	w.h.refreshCards(`[{"label":{"en":"x"},"post":"https://example.com/p/1"}]`)
	if n := len(w.h.channelCards()); n != 0 {
		t.Errorf("%d cards from a list with a bad post", n)
	}
	w.h.refreshCards(threeChannels)
	if n := len(w.h.channelCards()); n != 3 {
		t.Errorf("%d cards once it can be read again", n)
	}
}
