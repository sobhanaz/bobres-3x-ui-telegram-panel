package handler

import (
	"strings"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/state"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
)

// onStart registers returning users silently (refreshing their @username);
// new users choose a language first.
// onStart handles /start, with an optional start parameter: "r_<code>" from
// an invite link (kept until a new user picks a language and is created).
func (h *Handler) onStart(r *req, param string) {
	r.clearState()
	if r.user == nil {
		if code, ok := strings.CutPrefix(strings.TrimSpace(param), "r_"); ok && code != "" && len(code) <= 32 {
			r.setState("", map[string]string{"ref": code})
		}
		h.askLanguage(r)
		return
	}
	if r.user.GetUsername() != r.from.Username {
		if u, err := h.core.UpsertUser(r.ctx, &corev1.UpsertUserRequest{
			TelegramId: r.from.ID, Username: r.from.Username, Language: r.user.GetLanguage(),
		}); err == nil {
			r.user = u
		}
	}
	h.sendMenu(r, r.t("welcome", "brand", esc(h.brand())))
	h.showHome(r)
}

func (h *Handler) askLanguage(r *req) {
	kb := (&tg.Keyboard{}).Row(tg.CB("فارسی 🇮🇷", "lang:fa"), tg.CB("English 🇬🇧", "lang:en"))
	r.show(h.cat.T("en", "lang.prompt"), kb)
}

// onLanguage registers a new user, or switches an existing user's language.
func (h *Handler) onLanguage(r *req, lang string) {
	if lang == "" { // the picker itself
		h.askLanguage(r)
		return
	}
	lang = i18n.Normalize(lang)
	isNew := r.user == nil
	in := &corev1.UpsertUserRequest{TelegramId: r.from.ID, Username: r.from.Username, Language: lang}
	if isNew { // an invite link's code, kept since /start
		if st, err := h.state.Get(r.ctx, r.from.ID); err == nil && st.Scene == "" {
			in.ReferralCode = st.Get("ref")
		}
	}
	u, err := h.core.UpsertUser(r.ctx, in)
	if err != nil {
		r.fail(err)
		return
	}
	if in.ReferralCode != "" {
		r.clearState()
	}
	r.user, r.lang = u, u.GetLanguage()
	if isNew {
		if h.gateNewUser(r) {
			return // the bottom menu follows once the gates let them in (refreshMenu)
		}
		// The picker says the language is set; the welcome comes with the
		// bottom menu, which only a new message can carry.
		r.show(r.t("lang.changed"), nil)
		r.cb = nil // the rest comes as new messages
		h.sendMenu(r, r.t("welcome", "brand", esc(h.brand())))
		h.showHome(r)
		return
	}
	r.toast = r.t("lang.changed")
	h.showHome(r)
	h.sendMenu(r, r.t("menu.keyboard")) // the bottom menu in the new language
}

func (h *Handler) showHome(r *req) {
	if r.user == nil {
		h.askLanguage(r)
		return
	}
	kb := &tg.Keyboard{}
	if url := h.miniAppURL(); url != "" {
		kb.Row(r.webAppButton("btn.store", url)) // opens the app at once: an inline button gives it the sign-in data
	}
	kb.Row(r.button("btn.buy", "buy"), r.button("btn.services", "subs"))
	if h.bulkOn() { // a second green button under Buy
		kb.Row(r.button("btn.bulk", "bk"))
	}
	kb.Row(r.button("btn.wallet", "wallet"), r.button("btn.trial", "trial")).
		Row(r.button("btn.support", "support"), r.button("btn.language", "lang"))
	switch {
	case r.isReseller(): // their invite link is in the panel, with their commission
		kb.Row(r.button("btn.reseller", "rs"))
	case h.referralsOn():
		kb.Row(r.button("btn.invite", "ref"))
	}
	kb.Row(h.growthButtons(r)...) // Become a reseller and Top users, each while it is on
	switch {
	case r.isStaff():
		kb.Row(r.button("btn.admin", "adm"), r.button("btn.dashboard", "dash"))
	case r.isTeam(): // support: the dashboard only
		kb.Row(r.button("btn.dashboard", "dash"))
	}
	r.show(r.t("menu.title", "brand", esc(h.brand())), kb)
}

// menuItems are the bottom menu's buttons: each text key and the action a
// tap runs, the callback data of the same inline button (dispatch).
var menuItems = []struct{ key, data string }{
	{"btn.store", "store"}, {"btn.buy", "buy"}, {"btn.services", "subs"}, {"btn.wallet", "wallet"}, {"btn.trial", "trial"},
	{"btn.support", "support"}, {"btn.reseller", "rs"}, {"btn.invite", "ref"}, {"btn.language", "lang"},
	{"btn.reseller_apply", "ap"}, {"btn.top_users", "top"},
	{"btn.admin", "adm"}, {"btn.dashboard", "dash"},
}

// onStore answers a tap on the bottom menu's Store item with the button that
// opens the Mini App. (The menu cannot hold that button itself: a Mini App opened
// from a reply keyboard button gets no sign-in data from Telegram.) A menu sent
// before the app was switched off, or lost its public address, still has the
// item: it then leads to the plans, as the shop in the chat.
func (h *Handler) onStore(r *req) {
	url := h.miniAppURL()
	if url == "" {
		h.showPlans(r)
		return
	}
	kb := (&tg.Keyboard{}).Row(r.webAppButton("btn.store", url)).Row(r.button("btn.home", "home"))
	r.show(r.t("store.open"), kb)
}

// menuData is the action of the bottom-menu button labelled with key.
func menuData(key string) string {
	for _, it := range menuItems {
		if it.key == key {
			return it.data
		}
	}
	return ""
}

// mainMenu is the bottom menu for the user's language and role: the main
// buttons of the home menu, with the same texts (overrides apply) and the
// same colors. labels are the actions of its labels (the first button's
// when two have the same text).
func (h *Handler) mainMenu(r *req) (m *tg.Menu, labels map[string]string) {
	labels = map[string]string{}
	item := func(key string) tg.MenuButton {
		text := r.t(key)
		if _, taken := labels[text]; !taken {
			labels[text] = menuData(key)
		}
		return tg.MenuButton{Text: text, Style: i18n.ButtonStyle(key)}
	}
	m = &tg.Menu{IsPersistent: true, Resize: true, Placeholder: r.t("menu.placeholder")}
	if h.miniAppURL() != "" {
		// A tap sends the label as text and onStore answers with the button that
		// opens the app: a reply keyboard button could not give the app its sign-in data.
		m.Row(item("btn.store"))
	}
	m.Row(item("btn.buy")).
		Row(item("btn.services"), item("btn.wallet")).
		Row(item("btn.trial"), item("btn.support"))
	var more []tg.MenuButton
	switch {
	case r.isReseller():
		more = append(more, item("btn.reseller"))
	case h.referralsOn():
		more = append(more, item("btn.invite"))
	}
	m.Row(append(more, item("btn.language"))...)
	var growth []tg.MenuButton // the home screen's row: Become a reseller and Top users, each while it is on
	if h.resellerApplyOn(r) {
		growth = append(growth, item("btn.reseller_apply"))
	}
	if h.topUsersOn() {
		growth = append(growth, item("btn.top_users"))
	}
	m.Row(growth...)
	switch {
	case r.isStaff():
		m.Row(item("btn.admin"), item("btn.dashboard"))
	case r.isTeam(): // support: the dashboard only
		m.Row(item("btn.dashboard"))
	}
	return m, labels
}

// sendMenu sends text with the bottom menu (always a new message) and
// remembers that the user has this menu, and what its labels do; false when
// it was not sent.
func (h *Handler) sendMenu(r *req, text string) bool {
	m, labels := h.mainMenu(r)
	if _, err := h.tg.SendMenu(r.ctx, r.chatID, text, m); err != nil {
		h.log.Warn("send failed", "chat", r.chatID, "err", err)
		return false
	}
	if err := h.state.SaveMenu(r.ctx, r.from.ID, state.SentMenu{Sig: fingerprint(m), Labels: labels}); err != nil {
		h.log.Warn("bottom menu not remembered", "err", err)
	}
	return true
}

// refreshMenu sends the bottom menu ahead of the answer when the user's is
// not the current one: users from before it existed, and after a change of
// language, role or label. The answer then comes below it, so a tapped menu
// is not edited above it (keep). Not before the settings first load: a menu
// without the edited labels and the switches would be followed by another
// once they do.
func (h *Handler) refreshMenu(r *req) {
	if !h.settingsLoaded.Load() {
		return
	}
	sent, err := r.sentMenu()
	if err != nil {
		return // unreadable: no menu, rather than one with every update
	}
	if m, _ := h.mainMenu(r); sent.Sig == fingerprint(m) {
		return
	}
	if h.sendMenu(r, r.t("menu.keyboard")) {
		r.keep = true
	}
}

// sendsMenu reports an update whose answer brings the bottom menu anyway:
// /start, /menu and a language change.
func sendsMenu(r *req) bool {
	if r.cb != nil {
		verb, lang, _ := strings.Cut(r.cb.Data, ":")
		return verb == "lang" && lang != ""
	}
	cmd, _, _ := command(r.msg.Text)
	return cmd == "/start" || cmd == "/menu"
}

// menuTap is the action of the bottom-menu label a message is (dispatch
// data); false for any other message. The labels of the menu the user was
// sent come first, as their keyboard shows them (after a restart too), then
// every label menuAction knows.
func (h *Handler) menuTap(r *req) (string, bool) {
	if r.cb != nil || r.msg.Text == "" {
		return "", false
	}
	if sent, err := r.sentMenu(); err == nil {
		if data, ok := sent.Labels[r.msg.Text]; ok {
			return data, true
		}
	}
	return h.menuAction(r.msg.Text)
}

// menuAction is the action of a bottom-menu label (dispatch data); false for
// any other text. Every language's label counts, as the texts are now and as
// built in, and every label they had since the bot started, so a keyboard
// sent before a language switch or a text edit keeps working.
func (h *Handler) menuAction(text string) (string, bool) {
	labels := h.menuLabels.Load()
	if labels == nil {
		return "", false
	}
	data, ok := (*labels)[text]
	return data, ok
}

// loadMenuLabels rebuilds the labels menuAction knows (at start, then under
// refreshMu). The texts as they are come first: a built-in label that an
// edit gave to another button now means that button. Then the built-in
// texts, then the labels known before: a text edited again or reset may
// still be on a keyboard.
func (h *Handler) loadMenuLabels() {
	labels := map[string]string{}
	add := func(text, data string) {
		if _, taken := labels[text]; !taken && text != "" {
			labels[text] = data
		}
	}
	for _, lang := range i18n.Languages {
		for _, it := range menuItems {
			add(h.cat.T(lang, it.key), it.data)
		}
	}
	for _, lang := range i18n.Languages {
		for _, it := range menuItems {
			add(h.cat.Default(lang, it.key), it.data)
		}
	}
	if known := h.menuLabels.Load(); known != nil {
		for text, data := range *known {
			add(text, data)
		}
	}
	h.menuLabels.Store(&labels)
}
