package handler

import (
	"encoding/hex"
	"regexp"
	"strings"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Product news (docs/03-bot-ux.md, "Product news"). While the store has announcements on (announce.enabled), the bot asks every customer
// once "Do you want news of new products?": a newcomer right after their first /start, everyone else at their next interaction (never in a
// message of its own to all). No answer is no. The answer can be changed later on the language screen (and in the Mini App). Core sends the
// announcements themselves (a broadcast for the private messages, a channel post through runner.AnnounceChannel); here are the buttons:
//
//   - "an:y" and "an:n": the answer to the question; "an:t:y" and "an:t:n": the switch on the language screen;
//   - "an:b:<kind>:<product>": the Buy button under an announcement, which opens that product as a new message (the card stays as it is):
//     "an:b:p:<plan id without dashes>", "an:b:s:<supplier short id>", "an:b:l:<tier>:<months>". The product is checked when it is tapped.
//
// A channel post's Buy button is a link, t.me/<bot>?start=<payload> with the payload p_<plan>, s_<product> or l_<tier>_<months>, which
// onStart turns into the same "an:b:…" (a newcomer picks a language first, then gets the product).

// announceOn is the store's switch (announce.enabled, off unless it is true).
func (h *Handler) announceOn() bool { return h.setting("announce.enabled") == "true" }

func (h *Handler) onAnnounce(r *req, rest string) {
	switch {
	case rest == "y" || rest == "n":
		h.setAnnounceOptIn(r, rest == "y", false)
	case rest == "t:y" || rest == "t:n": // the switch on the language screen
		h.setAnnounceOptIn(r, rest == "t:y", true)
	case strings.HasPrefix(rest, "b:"):
		r.keep = true // the card under which Buy was tapped stays as it is
		h.openAnnounced(r, strings.TrimPrefix(rest, "b:"))
	default:
		r.toast = r.t("error.expired")
		h.showHome(r)
	}
}

// setAnnounceOptIn stores the customer's answer and says what it means. Under the question the question goes; on the language screen the
// screen is shown again with the switch turned.
func (h *Handler) setAnnounceOptIn(r *req, yes, onLanguageScreen bool) {
	u, err := h.core.SetAnnounceOptIn(r.ctx, &corev1.SetAnnounceOptInRequest{TelegramId: r.from.ID, OptIn: yes})
	if err != nil {
		r.fail(err)
		return
	}
	r.user = u
	key := "an.no_done"
	if yes {
		key = "an.yes_done"
	}
	if onLanguageScreen {
		r.toast = r.t("an.off")
		if yes {
			r.toast = r.t("an.on")
		}
		h.askLanguage(r)
		return
	}
	r.show(r.t(key), homeKeyboard(r))
}

// maybeAskAnnounce asks the customer about product news once, after their update was served: while announcements are on, when they have not
// been asked, and not while they are in the middle of something (a payment's proof, a ticket, a number they are typing) or held by a gate.
// Core records the question, so a second update at the same time does not ask again.
func (h *Handler) maybeAskAnnounce(r *req) {
	if r.user == nil || r.user.GetStatus() != "active" || r.user.GetAnnounceAsked() || r.held || !h.announceOn() {
		return
	}
	if r.cb != nil && strings.HasPrefix(r.cb.Data, "an:") {
		return
	}
	if st, err := h.state.Get(r.ctx, r.from.ID); err != nil || st.Scene != "" {
		return
	}
	resp, err := h.core.AskAnnounce(r.ctx, &corev1.AskAnnounceRequest{TelegramId: r.from.ID})
	if err != nil {
		h.log.Debug("announce question not claimed", "err", err)
		return
	}
	if !resp.GetAsk() {
		return
	}
	kb := (&tg.Keyboard{}).Row(r.button("btn.an_yes", "an:y"), r.button("btn.an_no", "an:n"))
	r.send(r.t("an.ask"), kb)
}

// announceToggle is the switch of product news on the language screen (nil while announcements are off or for a newcomer).
func (h *Handler) announceToggle(r *req) *tg.Button {
	if r.user == nil || !h.announceOn() {
		return nil
	}
	b := r.button("btn.an_news_off", "an:t:y")
	if r.user.GetAnnounceOptin() {
		b = r.button("btn.an_news_on", "an:t:n")
	}
	return &b
}

var (
	announcedRe  = regexp.MustCompile(`^(p:[0-9a-f]{32}|s:[a-z2-7]{10}|l:(standard|pro):(1|12))$`)
	startAnnRe   = regexp.MustCompile(`^(p_[0-9a-f]{32}|s_[a-z2-7]{10}|l_(standard|pro)_(1|12))$`)
	startToCBRep = strings.NewReplacer("_", ":")
)

// announcedStart is the Buy callback of a channel post's start payload ("" when it is none).
func announcedStart(param string) string {
	if !startAnnRe.MatchString(param) {
		return ""
	}
	return "an:b:" + startToCBRep.Replace(param)
}

// planIDOf writes a plan id without dashes back with them.
func planIDOf(h32 string) string {
	if len(h32) != 32 {
		return ""
	}
	if _, err := hex.DecodeString(h32); err != nil {
		return ""
	}
	return h32[0:8] + "-" + h32[8:12] + "-" + h32[12:16] + "-" + h32[16:20] + "-" + h32[20:]
}

// openAnnounced opens the product of a Buy button (kind:product), checked now: one that cannot be bought any more says so.
func (h *Handler) openAnnounced(r *req, what string) {
	if !announcedRe.MatchString(what) {
		r.toast, r.alert = r.t("an.unavailable"), true
		return
	}
	kind, id, _ := strings.Cut(what, ":")
	gone := func() {
		r.toast, r.alert = r.t("an.unavailable"), true
		if r.cb == nil { // a start link: there is no button to answer
			r.send(r.t("an.unavailable"), homeKeyboard(r))
		}
	}
	switch kind {
	case "p":
		planID := planIDOf(id)
		if _, err := h.findPlan(r, planID, false); err != nil {
			if status.Code(err) == codes.NotFound {
				gone()
				return
			}
			r.fail(err)
			return
		}
		h.showPlan(r, planID)
	case "s":
		if !h.suppliersOn() {
			gone()
			return
		}
		resp, err := h.core.ListSupplierOffers(r.ctx, &corev1.ListSupplierOffersRequest{UserId: r.user.GetId(), Language: r.lang})
		if err != nil {
			r.fail(err)
			return
		}
		if !resp.GetOn() || h.offerBySID(resp, id) == nil {
			gone()
			return
		}
		h.showSupplierProduct(r, id) // a product sold out since says so there
	case "l":
		tier, months, _ := strings.Cut(id, ":")
		if !h.licensesOn() {
			gone()
			return
		}
		resp, err := h.core.ListVendorOffers(r.ctx, &corev1.ListVendorOffersRequest{UserId: r.user.GetId()})
		if err != nil {
			r.fail(err)
			return
		}
		m := int32(1)
		if months == "12" {
			m = 12
		}
		if !resp.GetOn() || offerFor(resp.GetOffers(), tier, m) == nil {
			gone()
			return
		}
		h.confirmLicense(r, tier, "", months)
	}
}
