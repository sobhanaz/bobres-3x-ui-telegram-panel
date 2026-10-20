package handler

import (
	"context"
	"strings"
)

// --- helpers for the growth tests ---

// lastWith is the newest message of the chat whose text contains sub; it fails
// the test when there is none. (The invite screen is followed by its promo card
// and the line about forwarding it, so "the last message" is no longer it.)
func (p *person) lastWith(sub string) *msg {
	p.w.t.Helper()
	ms := p.w.tg.all(p.id)
	for i := len(ms) - 1; i >= 0; i-- {
		if strings.Contains(ms[i].text, sub) {
			return ms[i]
		}
	}
	p.w.t.Fatalf("user %d: no message contains %q; the last is %q", p.id, sub, p.last().text)
	return nil
}

// lastKeyboard is the newest message with buttons under it.
func (p *person) lastKeyboard() *msg {
	p.w.t.Helper()
	ms := p.w.tg.all(p.id)
	for i := len(ms) - 1; i >= 0; i-- {
		if len(buttons(ms[i])) > 0 {
			return ms[i]
		}
	}
	p.w.t.Fatalf("user %d: no message has buttons; the last is %q", p.id, p.last().text)
	return nil
}

// allText is everything the user was shown: every message and button label.
func (p *person) allText() string {
	var b strings.Builder
	for _, m := range p.w.tg.all(p.id) {
		b.WriteString(m.text + "\n" + strings.Join(m.labels(), "\n") + "\n")
	}
	return b.String()
}

// invite makes n new users join through the link of a registered user (as the
// bot's /start r_<code> does), each with a Telegram id of their own.
func (w *world) invite(inviterTG int64, n int) {
	w.t.Helper()
	ctx := context.Background()
	st := w.stack.Store
	inviter := w.storeUser(inviterTG)
	for i := range n {
		if _, err := st.UpsertUser(ctx, st.Conn(), inviterTG*100+int64(i), "", "en", inviter.ID); err != nil {
			w.t.Fatal(err)
		}
	}
}

// customerNamed registers a user who picked a language, with a username ("" for
// none).
func (w *world) customerNamed(id int64, username, lang string) *person {
	w.t.Helper()
	p := w.person(id, username)
	p.text("/start")
	p.press("lang:" + lang)
	return p
}

// setStatusOf bans (or frees) a registered user in core.
func (w *world) setStatusOf(telegramID int64, status string) {
	w.t.Helper()
	if err := w.stack.Store.SetUserStatus(context.Background(), w.stack.Store.Conn(), w.storeUser(telegramID).ID, status); err != nil {
		w.t.Fatal(err)
	}
}
