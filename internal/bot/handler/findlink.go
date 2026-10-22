package handler

import (
	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/state"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// A customer who does not see a service in My services (it was made for them, or under another account) finds it by its link: they send the
// subscription link (or the address of its page), the bot shows the service's state and offers to renew it, paying from their own account.
// Whoever holds the link holds the service, so the link is the proof; core checks it again at the renewal ("rnw" with the link's secret).

// askFindLink handles "fl": the next message is the link.
func (h *Handler) askFindLink(r *req) {
	r.setState(sceneFindLink, nil)
	r.show(r.t("find.ask"), cancelKeyboard(r))
}

// onFindLink is the answer to the question: the service for the link, or why there is none (the question stays open).
func (h *Handler) onFindLink(r *req) {
	res, err := h.core.FindServiceByLink(r.ctx, &corev1.FindServiceByLinkRequest{UserId: r.user.GetId(), Link: r.msg.Text})
	if err != nil {
		switch status.Code(err) {
		case codes.InvalidArgument:
			r.send(r.t("find.bad"), cancelKeyboard(r))
		case codes.NotFound:
			r.send(r.t("find.none"), cancelKeyboard(r))
		default:
			r.clearState()
			r.fail(err)
		}
		return
	}
	r.clearState()
	if res.GetMine() { // it is in their own list: the usual screen
		h.showService(r, res.GetService().GetId())
		return
	}
	h.showFound(r, res.GetService(), res.GetSecret())
}

// showFound shows a service that is not in the customer's list: its state, with Renew when it can be.
func (h *Handler) showFound(r *req, s *corev1.Subscription, secret string) {
	text := r.t("find.detail", "status", statusText(r, s.GetStatus()), "expires", h.expires(r, s.GetExpiresAt()),
		"traffic", h.traffic(r, s.GetTrafficTotalBytes()))
	used, total := s.GetTrafficUsedBytes(), s.GetTrafficTotalBytes()
	switch {
	case total > 0:
		text += "\n" + r.t("sub.usage", "used", i18n.Bytes(r.lang, used), "total", i18n.Bytes(r.lang, total), "bar", usageBar(used, total))
	case used > 0:
		text += "\n" + r.t("sub.usage_unlimited", "used", i18n.Bytes(r.lang, used))
	}
	kb := &tg.Keyboard{}
	if renewable(s) {
		n, ok := h.saveCheckout(r, state.Checkout{Type: "renew", SubscriptionID: s.GetId(), LinkSecret: secret})
		if !ok {
			return
		}
		kb.Row(r.button("btn.renew", "fr:"+n))
	} else {
		text += "\n\n" + r.t("find.not_renewable")
	}
	if u := s.GetPageUrl(); u != "" {
		kb.Row(tg.Link(r.t("btn.open_page"), pageLink(u, r.lang)))
	}
	kb.Row(r.button("btn.find_link", "fl"), r.button("btn.back", "subs"))
	kb.Row(r.button("btn.home", "home"))
	r.show(text, kb)
}

// onFoundRenew handles "fr:<nonce>": the plans to renew the found service with.
func (h *Handler) onFoundRenew(r *req, n string) {
	c, ok := h.loadCheckout(r, n)
	if !ok {
		return
	}
	if c.LinkSecret == "" || c.SubscriptionID == "" {
		h.showServices(r)
		return
	}
	ps, err := h.regularPlans(r)
	if err != nil {
		r.fail(err)
		return
	}
	h.showPicker(r, c, ps, r.t("find.renew_pick", "id", shortID(c.SubscriptionID)))
}
