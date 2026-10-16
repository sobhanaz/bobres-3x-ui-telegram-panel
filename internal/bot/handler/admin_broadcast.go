package handler

import (
	"unicode/utf8"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
)

// The broadcast wizard: who gets it, the Persian text, the English text, a
// preview with the audience size, and a confirmation that sends it. Both
// texts are required: each customer gets the one in their language.

var broadcastSegments = [][2]string{
	{"all", "btn.adm_seg_all"}, {"active", "btn.adm_seg_active"},
	{"expired", "btn.adm_seg_expired"}, {"never_bought", "btn.adm_seg_never"},
}

func broadcastTextStep(lang string) wizStep {
	return wizStep{
		id: "t_" + lang,
		prompt: func(r *req, d map[string]string) (string, [][]tg.Button) {
			return promptText(r, "wiz.bc.text_"+lang, nil, r.t("admin.wiz_bc_limit", "max", i18n.Number(r.lang, i18n.MaxMessage))), nil
		},
		parse: func(r *req, d map[string]string, in string) (string, string) {
			v, problems := i18n.ValidateMessage(in, i18n.MaxMessage)
			if len(problems) > 0 {
				return "", r.t("admin.err_bc_text", "max", i18n.Number(r.lang, i18n.MaxMessage))
			}
			return v, ""
		},
	}
}

func broadcastRequest(h *Handler, r *req, d map[string]string, send bool) *corev1.AdminBroadcastRequest {
	return &corev1.AdminBroadcastRequest{
		ActorTelegramId: h.actor(r), Segment: d["seg"], Send: send,
		Texts: map[string]string{"fa": d["t_fa"], "en": d["t_en"]},
	}
}

// preview is a short look at a text: escaped, cut to a few hundred characters.
func preview(s string) string {
	const max = 300
	if utf8.RuneCountInString(s) <= max {
		return esc(s)
	}
	return esc(string([]rune(s)[:max])) + "…"
}

func broadcastWizard() *wizard {
	return &wizard{
		steps: []wizStep{
			choiceStep("seg", "wiz.bc.segment", broadcastSegments...),
			broadcastTextStep("fa"),
			broadcastTextStep("en"),
		},
		confirm: func(r *req, d map[string]string) (string, string) {
			resp, err := r.h.core.AdminBroadcast(r.ctx, broadcastRequest(r.h, r, d, false))
			if err != nil {
				return "", r.h.refused(r, err)
			}
			return r.t("admin.confirm_bc", "segment", r.t("btn.adm_seg_"+segKey(d["seg"])),
				"audience", i18n.Number(r.lang, resp.GetAudience()),
				"fa", i18n.Number(r.lang, resp.GetAudienceFa()), "en", i18n.Number(r.lang, resp.GetAudienceEn()),
				"text_fa", preview(resp.GetTexts()["fa"]), "text_en", preview(resp.GetTexts()["en"])), ""
		},
		save: func(r *req, d map[string]string) string {
			resp, err := r.h.core.AdminBroadcast(r.ctx, broadcastRequest(r.h, r, d, true))
			if err != nil {
				return r.h.refused(r, err)
			}
			r.show(r.t("admin.bc_sent", "n", i18n.Number(r.lang, resp.GetAudience())), (&tg.Keyboard{}).Row(r.button("btn.back", "adm")))
			return ""
		},
		home: func(map[string]string) string { return "" },
	}
}

func segKey(seg string) string {
	if seg == "never_bought" {
		return "never"
	}
	return seg
}
