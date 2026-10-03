package handler

import (
	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
)

// onStart registers returning users silently (refreshing their @username);
// new users choose a language first.
func (h *Handler) onStart(r *req) {
	r.clearState()
	if r.user == nil {
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
	r.send(r.t("welcome", "brand", esc(h.brand())), nil)
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
	u, err := h.core.UpsertUser(r.ctx, &corev1.UpsertUserRequest{
		TelegramId: r.from.ID, Username: r.from.Username, Language: lang,
	})
	if err != nil {
		r.fail(err)
		return
	}
	isNew := r.user == nil
	r.user, r.lang = u, u.GetLanguage()
	if isNew {
		r.show(r.t("welcome", "brand", esc(h.brand())), nil)
		r.cb = nil // the welcome stays; the menu comes as a new message
		h.showHome(r)
		return
	}
	r.toast = r.t("lang.changed")
	h.showHome(r)
}

func (h *Handler) showHome(r *req) {
	if r.user == nil {
		h.askLanguage(r)
		return
	}
	kb := (&tg.Keyboard{}).
		Row(tg.CB(r.t("btn.buy"), "buy"), tg.CB(r.t("btn.services"), "subs")).
		Row(tg.CB(r.t("btn.wallet"), "wallet"), tg.CB(r.t("btn.trial"), "trial")).
		Row(tg.CB(r.t("btn.support"), "support"), tg.CB(r.t("btn.language"), "lang"))
	if r.isStaff() {
		kb.Row(tg.CB(r.t("btn.admin"), "adm"))
	}
	r.show(r.t("menu.title", "brand", esc(h.brand())), kb)
}
