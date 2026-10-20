package handler

import (
	"net/url"
	"strings"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/state"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Retention (Phase 3): renewals, traffic packages, discount codes, referrals.

// findSubscription returns one of the user's subscriptions.
func (h *Handler) findSubscription(r *req, id string) (*corev1.Subscription, bool) {
	s, err := h.subscription(r, id)
	if err != nil {
		r.fail(err)
		return nil, false
	}
	if s == nil {
		h.showServices(r)
		return nil, false
	}
	return s, true
}

// renewable: delivered and not disabled by an admin.
func renewable(s *corev1.Subscription) bool {
	switch s.GetStatus() {
	case "active", "expiring_soon", "expired", "depleted":
		return s.GetSubscriptionLink() != ""
	}
	return false
}

// toppable: a running subscription with a traffic limit.
func toppable(s *corev1.Subscription) bool {
	return renewable(s) && s.GetTrafficTotalBytes() > 0 && s.GetStatus() != "expired"
}

func (h *Handler) topupPlans(r *req) []*corev1.Plan {
	ps, err := h.plans(r, false)
	if err != nil {
		return nil
	}
	var out []*corev1.Plan
	for _, p := range ps {
		if p.GetIsTopup() {
			out = append(out, p)
		}
	}
	return out
}

// showRenewPlans handles "rnw:<subscription id>": pick the plan to renew with.
func (h *Handler) showRenewPlans(r *req, subID string) {
	s, ok := h.findSubscription(r, subID)
	if !ok {
		return
	}
	if !renewable(s) {
		r.show(r.t("sub.not_extendable"), backHome(r, "sub:"+subID))
		return
	}
	ps, err := h.regularPlans(r)
	if err != nil {
		r.fail(err)
		return
	}
	h.showPicker(r, state.Checkout{Type: "renew", SubscriptionID: subID}, ps,
		r.labelled(s.GetLabel(), r.t("renew.pick", "id", shortID(subID))))
}

// showTopups handles "tup:<subscription id>": pick a traffic package.
func (h *Handler) showTopups(r *req, subID string) {
	s, ok := h.findSubscription(r, subID)
	if !ok {
		return
	}
	if !toppable(s) {
		r.show(r.t("sub.not_extendable"), backHome(r, "sub:"+subID))
		return
	}
	h.showPicker(r, state.Checkout{Type: "traffic_topup", SubscriptionID: subID}, h.topupPlans(r),
		r.labelled(s.GetLabel(), r.t("topup.pick", "id", shortID(subID))))
}

// showPicker lists plans for a saved renewal or top-up: "rp:<nonce>:<plan id>".
func (h *Handler) showPicker(r *req, c state.Checkout, ps []*corev1.Plan, title string) {
	if len(ps) == 0 {
		r.show(r.t("plans.empty"), backHome(r, "sub:"+c.SubscriptionID))
		return
	}
	n, ok := h.saveCheckout(r, c)
	if !ok {
		return
	}
	kb := &tg.Keyboard{}
	h.addPlans(r, kb, ps, func(p *corev1.Plan) string { return "rp:" + n + ":" + p.GetId() })
	kb.Row(r.button("btn.back", "sub:"+c.SubscriptionID), r.button("btn.home", "home"))
	r.show(title, kb)
}

// onPickForSub handles "rp:<nonce>:<plan id>".
func (h *Handler) onPickForSub(r *req, rest string) {
	n, planID, _ := strings.Cut(rest, ":")
	c, ok := h.loadCheckout(r, n)
	if !ok {
		return
	}
	c.PlanID = planID
	h.showCheckout(r, c)
}

// loadCheckout reads a saved menu, or tells the user it expired.
func (h *Handler) loadCheckout(r *req, n string) (state.Checkout, bool) {
	c, ok, err := h.state.LoadCheckout(r.ctx, r.from.ID, n)
	if err != nil {
		r.fail(err)
		return c, false
	}
	if !ok {
		r.show(r.t("error.menu_expired"), (&tg.Keyboard{}).Row(r.button("btn.buy", "buy"), r.button("btn.home", "home")))
	}
	return c, ok
}

// askDiscount handles "dc:<nonce>": the next message is a discount code.
// A reseller (a menu from before they became one) pays their tier's price.
func (h *Handler) askDiscount(r *req, n string) {
	c, ok := h.loadCheckout(r, n)
	if !ok {
		return
	}
	if r.isReseller() {
		r.toast, r.alert = r.t("discount.bad.reseller"), true
		h.showCheckout(r, c)
		return
	}
	r.setState(sceneDiscount, map[string]string{"co": n})
	r.show(r.t("discount.ask"), cancelKeyboard(r))
}

// onDiscountCode checks the code the user sent and shows the menu again
// with the discount; a code that does not work leaves the question open.
func (h *Handler) onDiscountCode(r *req, st state.State) {
	c, ok := h.loadCheckout(r, st.Get("co"))
	if !ok {
		r.clearState()
		return
	}
	if r.isReseller() { // became one since the question was asked
		r.clearState()
		r.send(r.t("discount.bad.reseller"), nil)
		h.showCheckout(r, c)
		return
	}
	q, err := h.core.QuoteOrder(r.ctx, &corev1.QuoteOrderRequest{UserId: r.user.GetId(), PlanId: c.PlanID,
		Type: c.Type, SubscriptionId: c.SubscriptionID, DiscountCode: strings.TrimSpace(r.msg.Text)})
	if err != nil {
		if key := discountKey(err); key != "error.generic" {
			r.send(r.t(key), cancelKeyboard(r))
			return
		}
		r.clearState()
		r.fail(err)
		return
	}
	r.clearState()
	c.DiscountCode = q.GetDiscountCode()
	h.showCheckout(r, c)
}

// discountKey is the message for a discount code core refused.
func discountKey(err error) string {
	st, _ := status.FromError(err)
	reason, found := strings.CutPrefix(st.Message(), "discount: ")
	if st.Code() != codes.FailedPrecondition || !found {
		return "error.generic"
	}
	switch reason {
	case "expired", "used_up", "already_used", "currency", "reseller":
		return "discount.bad." + reason
	}
	return "discount.bad.unknown"
}

// referralsOn: the owner set a reward, so the program is advertised.
func (h *Handler) referralsOn() bool {
	n, ok := i18n.ParseNumber(h.setting("referral.reward_percent"))
	return ok && n > 0
}

// showReferral handles "ref": the user's invite link and what it earned. A
// reseller's link earns their commission instead: their own link screen.
func (h *Handler) showReferral(r *req) {
	if r.isReseller() {
		h.showResellerLink(r)
		return
	}
	info, err := h.core.GetReferralInfo(r.ctx, &corev1.GetReferralInfoRequest{UserId: r.user.GetId()})
	if err != nil {
		r.fail(err)
		return
	}
	if info.GetRewardPercent() <= 0 || h.cfg.BotUsername == "" {
		r.show(r.t("ref.off"), homeKeyboard(r))
		return
	}
	link := "https://t.me/" + h.cfg.BotUsername + "?start=r_" + info.GetCode()
	share := "https://t.me/share/url?url=" + url.QueryEscape(link) + "&text=" + url.QueryEscape(r.t("ref.share_text", "brand", h.brand()))
	kb := (&tg.Keyboard{}).Row(r.linkButton("btn.share", share)).Row(r.button("btn.home", "home"))
	r.show(r.t("ref.screen", "percent", i18n.Number(r.lang, int64(info.GetRewardPercent())), "link", esc(link),
		"invited", i18n.Number(r.lang, int64(info.GetInvited())), "earned", earned(r, info.GetEarned())), kb)
	h.sendPromoCard(r, link) // a card to forward, apart from this screen
}

// usageBar draws used/total as ten blocks.
func usageBar(used, total int64) string {
	if total <= 0 {
		return ""
	}
	filled := int(min(max(used*10/total, 0), 10))
	return strings.Repeat("▰", filled) + strings.Repeat("▱", 10-filled)
}
