package handler

import (
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/channelcards"
)

// Channel buttons (the setting bot.channel_cards, internal/channelcards): the owner's channels as buttons of the menu, each on a row of its own
// under Buy and Free trial, in the bottom menu and on the home screen. A tap shows a copy of the channel's first message (the post whose link
// the owner gave), and under it one button, View channel, that opens the channel. The list is written in the dashboard, where core checks it;
// the bot takes it from the settings at every refresh.
//
//	chc:<id>   a card's button: <id> is channelcards.Card.ID, eight hex digits made from the post's link

// refreshCards reads the cards from a settings refresh (under refreshMu). A list that cannot be read is no cards, said once in the log: core
// checks it when it is saved, so this is a setting written some other way.
func (h *Handler) refreshCards(raw string) {
	cards, err := channelcards.Parse(raw)
	switch {
	case err != nil:
		if raw != h.cardsBad {
			h.log.Warn("channel buttons ignored: the setting cannot be read", "err", err)
			h.cardsBad = raw
		}
		cards = nil
	default:
		h.cardsBad = ""
	}
	h.cards.Store(&cards)
}

// channelCards are the cards as of the last refresh.
func (h *Handler) channelCards() []channelcards.Card {
	if p := h.cards.Load(); p != nil {
		return *p
	}
	return nil
}

// cardData is the callback data of a card's button, and the action of its label in the bottom menu.
func cardData(id string) string { return "chc:" + id }

// cardRows adds a row to kb for each card, in the customer's language.
func (h *Handler) cardRows(r *req, kb *tg.Keyboard) {
	for _, c := range h.channelCards() {
		kb.Row(tg.CB(c.Text(r.lang), cardData(c.ID)).Primary())
	}
}

// onChannelCard answers a tap on a card's button (callback data or bottom-menu label) with the copy of the channel's post and the View channel
// button under it, as a new message: the screen the tap came from stays. When the post cannot be copied (the channel is private and the bot is
// not in it, the post was deleted, Telegram is down) the customer still gets the card's name and the button, so a tap never ends in nothing; staff
// are told why it may be, since they are the ones who can mend the list. A card the owner has since removed or changed answers like any old button.
func (h *Handler) onChannelCard(r *req, id string) {
	card, ok := channelcards.Find(h.channelCards(), id)
	if !ok {
		r.toast = r.t("error.expired")
		h.showHome(r)
		return
	}
	kb := (&tg.Keyboard{}).Row(tg.Link(r.t("btn.view_channel"), card.Channel).Primary())
	err := h.tg.CopyMessage(r.ctx, r.chatID, card.Post.Chat, card.Post.ID, kb)
	if err == nil || tg.IsForbidden(err) { // not copied for a customer who blocked the bot: nothing to tell them
		return
	}
	h.log.Warn("channel post not copied", "card", card.ID, "post", card.Post.URL(), "err", err)
	text := "<b>" + esc(card.Text(r.lang)) + "</b>"
	if r.isTeam() {
		text += "\n\n" + r.t("channels.copy_failed")
	}
	r.send(text, kb)
}
