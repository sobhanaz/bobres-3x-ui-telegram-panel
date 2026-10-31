package handler

import (
	"net/url"
	"strings"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// The affiliate program of the owner's own store (docs/18-hosted-and-affiliate-plans.md, section 2; docs/03-bot-ux.md): /affiliate (or the
// button on the licenses screen) gives a customer their link t.me/<bot>?start=aff_<code> and what it earned; a customer who opens such a link
// is recorded by core as brought by that affiliate (before their first license only, never by their own link). Core decides everything;
// the bot only shows it, and an affiliate link that does not count is silent: the customer just reaches the store.

// affiliatePrefix starts the start parameter of an affiliate link.
const affiliatePrefix = "aff_"

// affiliatesOn is the owner's switch (affiliate.enabled) on a store that sells licenses.
func (h *Handler) affiliatesOn() bool {
	return h.licensesOn() && h.setting("affiliate.enabled") == "true"
}

// affiliateCode reads an affiliate link's code from a /start parameter ("" when it is none).
func affiliateCode(param string) string {
	code, ok := strings.CutPrefix(strings.TrimSpace(param), affiliatePrefix)
	if !ok || code == "" || len(code) > 32 {
		return ""
	}
	return code
}

// applyAffiliate passes an affiliate link's code to core, which records it when it counts. Nothing is shown either way.
func (h *Handler) applyAffiliate(r *req, code string) {
	if code == "" || r.user == nil {
		return
	}
	if _, err := h.core.ApplyAffiliateCode(r.ctx, &corev1.ApplyAffiliateCodeRequest{UserId: r.user.GetId(), Code: code}); err != nil {
		h.log.Warn("affiliate link not recorded", "err", err)
	}
}

// showAffiliate answers /affiliate and the "aff" button: the customer's link and figures.
func (h *Handler) showAffiliate(r *req) {
	if !h.affiliatesOn() || h.cfg.BotUsername == "" {
		r.show(r.t("aff.off"), homeKeyboard(r))
		return
	}
	info, err := h.core.GetMyAffiliate(r.ctx, &corev1.GetMyAffiliateRequest{UserId: r.user.GetId()})
	if err != nil {
		if status.Code(err) == codes.FailedPrecondition { // switched off between the setting the bot holds and core's
			r.show(r.t("aff.off"), homeKeyboard(r))
			return
		}
		r.fail(err)
		return
	}
	link := "https://t.me/" + h.cfg.BotUsername + "?start=" + affiliatePrefix + info.GetCode()
	payout := r.t("aff.payout_any")
	if m := info.GetMinPayout(); m.GetAmount() > 0 {
		payout = r.t("aff.payout_min", "min", r.money(m.GetAmount(), m.GetCurrency()))
	}
	if info.GetStatus() == "disabled" {
		payout = r.t("aff.disabled") + "\n" + payout
	}
	money := func(m interface {
		GetAmount() int64
		GetCurrency() string
	}) string {
		return r.money(m.GetAmount(), m.GetCurrency())
	}
	share := "https://t.me/share/url?url=" + url.QueryEscape(link) + "&text=" + url.QueryEscape(r.t("aff.share_text"))
	kb := (&tg.Keyboard{}).Row(r.linkButton("btn.share", share)).Row(r.button("btn.home", "home"))
	r.show(r.t("aff.screen", "percent", i18n.Number(r.lang, int64(info.GetPercent())), "link", esc(link),
		"referred", i18n.Number(r.lang, info.GetReferred()), "licenses", i18n.Number(r.lang, info.GetLicenses()),
		"earned", money(info.GetEarned()), "paid", money(info.GetPaid()), "balance", money(info.GetBalance()), "payout", payout), kb)
}
