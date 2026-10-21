package handler

import (
	"fmt"
	"strings"
	"time"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Digital accounts resold from a supplier, in the owner's own bot (docs/supplier.md). The bot holds no rule: core lists
// what the owner sells, takes the money from the wallet (Toman), asks the supplier for the account and hands it over. A
// buyer short of money tops the wallet up with the bot's ordinary top-up and comes back. The menu button exists only while
// the owner has `supplier.enabled` on, and core refuses everything while it is off.
//
// The callbacks are "sup" (home), "sup:p:<sid>" (a product), "sup:y:<sid>:<nonce>" (pay), "sup:my" (my purchases) and
// "sup:o:<order id>" (one purchase, with what it delivered).

func (h *Handler) suppliersOn() bool { return h.setting("supplier.enabled") == "true" }

func (h *Handler) onSupplier(r *req, rest string) {
	if !h.suppliersOn() {
		r.toast, r.alert = r.t("sup.unavailable"), true
		h.showHome(r)
		return
	}
	parts := strings.Split(rest, ":")
	switch {
	case rest == "":
		h.showSupplier(r)
	case parts[0] == "p" && len(parts) == 2:
		h.showSupplierProduct(r, parts[1])
	case parts[0] == "y" && len(parts) == 3:
		h.buySupplier(r, parts[1], parts[2])
	case parts[0] == "my":
		h.showSupplierOrders(r)
	case parts[0] == "o" && len(parts) == 2:
		h.showSupplierOrder(r, parts[1])
	default:
		h.showSupplier(r)
	}
}

func (h *Handler) supplierOffers(r *req) (*corev1.ListSupplierOffersResponse, bool) {
	resp, err := h.core.ListSupplierOffers(r.ctx, &corev1.ListSupplierOffersRequest{UserId: r.user.GetId(), Language: r.lang})
	if err != nil {
		r.fail(err)
		return nil, false
	}
	if !resp.GetOn() {
		r.toast, r.alert = r.t("sup.unavailable"), true
		h.showHome(r)
		return nil, false
	}
	return resp, true
}

func (h *Handler) showSupplier(r *req) {
	resp, ok := h.supplierOffers(r)
	if !ok {
		return
	}
	bal := resp.GetBalance()
	kb := &tg.Keyboard{}
	for _, o := range resp.GetOffers() {
		label := r.t("sup.offer_btn", "name", truncRunes(o.GetName(), 40), "price", r.money(o.GetPrice().GetAmount(), o.GetPrice().GetCurrency()))
		kb.Row(tg.CB(label, "sup:p:"+o.GetSid()))
	}
	kb.Row(r.button("btn.sup_my", "sup:my"), r.button("btn.home", "home"))
	text := r.t("sup.title", "balance", r.balance(bal.GetAmount(), bal.GetCurrency()))
	if len(resp.GetOffers()) == 0 {
		text += "\n\n" + r.t("sup.empty")
	}
	r.show(text, kb)
}

func (h *Handler) offerBySID(resp *corev1.ListSupplierOffersResponse, sid string) *corev1.SupplierOffer {
	for _, o := range resp.GetOffers() {
		if o.GetSid() == sid {
			return o
		}
	}
	return nil
}

func (h *Handler) showSupplierProduct(r *req, sid string) {
	resp, ok := h.supplierOffers(r)
	if !ok {
		return
	}
	o := h.offerBySID(resp, sid)
	if o == nil {
		h.showSupplier(r)
		return
	}
	price, cur := o.GetPrice().GetAmount(), o.GetPrice().GetCurrency()
	bal := resp.GetBalance().GetAmount()
	text := r.t("sup.product", "name", esc(o.GetName()), "price", r.money(price, cur), "balance", r.balance(bal, cur))
	if d := strings.TrimSpace(o.GetDescription()); d != "" {
		text = r.t("sup.product_desc", "name", esc(o.GetName()), "description", esc(d), "price", r.money(price, cur), "balance", r.balance(bal, cur))
	}
	kb := &tg.Keyboard{}
	if bal >= price {
		kb.Row(tg.CB(r.t("btn.sup_pay", "price", r.money(price, cur)), fmt.Sprintf("sup:y:%s:%s", sid, nonce())))
	} else {
		text += "\n\n" + r.t("sup.insufficient")
		kb.Row(r.button("btn.sup_topup", "wtop"))
	}
	kb.Row(r.button("btn.back", "sup"), r.button("btn.home", "home"))
	r.show(text, kb)
}

func (h *Handler) buySupplier(r *req, sid, n string) {
	if n == "" {
		h.showSupplier(r)
		return
	}
	resp, err := h.core.BuySupplierProduct(r.ctx, &corev1.BuySupplierProductRequest{UserId: r.user.GetId(), Sid: sid, Quantity: 1,
		IdempotencyKey: fmt.Sprintf("sup:%d:%s:%s", r.from.ID, sid, n)})
	if err != nil {
		st, _ := status.FromError(err)
		switch {
		case st.Code() == codes.FailedPrecondition && strings.Contains(st.Message(), "insufficient"):
			h.showSupplierProduct(r, sid)
		case st.Code() == codes.FailedPrecondition || st.Code() == codes.NotFound:
			h.showSupplier(r)
		default:
			r.fail(err)
		}
		return
	}
	home := (&tg.Keyboard{}).Row(r.button("btn.sup_my", "sup:my"), r.button("btn.home", "home"))
	switch resp.GetOrderStatus() {
	case "done":
		name := h.supplierOrderName(r, resp.GetOrderId())
		r.show(r.t("sup.delivered", "name", esc(name), "delivery", esc(resp.GetDelivery())), home)
	case "paid":
		r.show(r.t("sup.pending"), home)
	default: // refunded
		r.show(r.t("sup.refunded"), home)
	}
}

// supplierOrderName is the product name of an order of this customer ("" when it cannot be read).
func (h *Handler) supplierOrderName(r *req, orderID string) string {
	d, err := h.core.GetSupplierDelivery(r.ctx, &corev1.GetSupplierDeliveryRequest{UserId: r.user.GetId(), OrderId: orderID, Language: r.lang})
	if err != nil {
		return ""
	}
	return d.GetOrder().GetProductName()
}

func supplierStatusKey(s string) string {
	switch s {
	case "done", "paid", "refunded":
		return "sup.status." + s
	}
	return "sup.status.paid"
}

func (h *Handler) showSupplierOrders(r *req) {
	resp, err := h.core.ListMySupplierOrders(r.ctx, &corev1.ListMySupplierOrdersRequest{UserId: r.user.GetId(), Language: r.lang})
	if err != nil {
		r.fail(err)
		return
	}
	kb := &tg.Keyboard{}
	if len(resp.GetOrders()) == 0 {
		kb.Row(r.button("btn.sup_home", "sup"), r.button("btn.home", "home"))
		r.show(r.t("sup.my.empty"), kb)
		return
	}
	lines := []string{r.t("sup.my.title"), ""}
	for _, o := range resp.GetOrders() {
		total := o.GetTotal()
		lines = append(lines, r.t("sup.my.line", "name", esc(o.GetProductName()), "price", r.money(total.GetAmount(), total.GetCurrency()),
			"date", i18n.Date(r.lang, time.Unix(o.GetCreatedAt(), 0)), "status", r.t(supplierStatusKey(o.GetStatus()))))
		if o.GetStatus() == "done" {
			kb.Row(tg.CB(r.t("btn.sup_view", "name", truncRunes(o.GetProductName(), 30)), "sup:o:"+o.GetId()))
		}
	}
	kb.Row(r.button("btn.sup_home", "sup"), r.button("btn.home", "home"))
	r.show(strings.Join(lines, "\n"), kb)
}

func (h *Handler) showSupplierOrder(r *req, id string) {
	d, err := h.core.GetSupplierDelivery(r.ctx, &corev1.GetSupplierDeliveryRequest{UserId: r.user.GetId(), OrderId: id, Language: r.lang})
	if err != nil {
		r.fail(err)
		return
	}
	r.show(r.t("sup.delivered", "name", esc(d.GetOrder().GetProductName()), "delivery", esc(d.GetDelivery())), backHome(r, "sup:my"))
}
