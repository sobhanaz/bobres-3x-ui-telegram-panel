package handler

import (
	"fmt"
	"hash/fnv"
	"slices"
	"strconv"
	"strings"
	"time"

	commonv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/common/v1"
	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/state"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Digital accounts resold from a supplier, in the owner's own bot (docs/supplier.md). The bot holds no rule: core lists
// what the owner sells, takes the money from the wallet (Toman), asks the supplier for the account and hands it over. A
// buyer short of money tops the wallet up with the bot's ordinary top-up and comes back. The menu button exists only while
// the owner has `supplier.enabled` on, and core refuses everything while it is off.
//
// The callbacks are "sup" (home: the supplier's categories, or the products when it has at most one), "sup:c:<key>" (the products of a
// category), "sup:p:<sid>" (a product: its description, price and stock), "sup:q:<sid>" (how many), "sup:n:<sid>" (type how many),
// "sup:s:<sid>:<qty>" (the order summary), "sup:y:<sid>:<qty>:<nonce>" (pay; "sup:y:<sid>:<nonce>" is one), "sup:my" (my purchases) and
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
	case parts[0] == "c" && len(parts) == 2:
		h.showSupplierCategory(r, parts[1])
	case parts[0] == "p" && len(parts) == 2:
		h.showSupplierProduct(r, parts[1])
	case parts[0] == "q" && len(parts) == 2:
		h.showSupplierQuantity(r, parts[1])
	case parts[0] == "n" && len(parts) == 2:
		h.askSupplierQuantity(r, parts[1])
	case parts[0] == "s" && len(parts) == 3:
		h.showSupplierSummary(r, parts[1], parts[2])
	case parts[0] == "y" && len(parts) == 3: // from before quantities: one
		h.buySupplier(r, parts[1], "1", parts[2])
	case parts[0] == "y" && len(parts) == 4:
		h.buySupplier(r, parts[1], parts[2], parts[3])
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

// supplierCategory is the offers of one supplier category, in the order the shelf lists them.
type supplierCategory struct {
	key, name string
	offers    []*corev1.SupplierOffer
}

// categoryKey is the short, stable id of a category in a button's data ("o" for the offers the supplier gave no category).
func categoryKey(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return "o"
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(name))
	return fmt.Sprintf("%08x", h.Sum32())
}

// supplierCategories groups the offers by the supplier's category, in the order the categories first appear; the offers with none come last,
// as "Other".
func supplierCategories(offers []*corev1.SupplierOffer) []*supplierCategory {
	var out []*supplierCategory
	byKey := map[string]*supplierCategory{}
	for _, o := range offers {
		key := categoryKey(o.GetCategory())
		c := byKey[key]
		if c == nil {
			c = &supplierCategory{key: key, name: strings.TrimSpace(o.GetCategory())}
			byKey[key] = c
			out = append(out, c)
		}
		c.offers = append(c.offers, o)
	}
	if c := byKey["o"]; c != nil && len(out) > 1 && out[len(out)-1] != c {
		out = slices.DeleteFunc(out, func(x *supplierCategory) bool { return x == c })
		out = append(out, c)
	}
	return out
}

func (c *supplierCategory) allSoldOut() bool {
	for _, o := range c.offers {
		if !o.GetSoldOut() {
			return false
		}
	}
	return true
}

// categoryEmojis give a category an icon by a word in its name; the first match wins, and a category that matches none gets a box.
var categoryEmojis = []struct{ word, emoji string }{
	{"telegram", "✈️"}, {"chatgpt", "🤖"}, {"openai", "🤖"}, {"claude", "🧠"}, {"gemini", "✨"}, {"grok", "⚡"}, {"perplexity", "🔎"},
	{"midjourney", "🖼"}, {"leonardo", "🖼"}, {"canva", "🎨"}, {"figma", "🎨"}, {"adobe", "🅰️"}, {"capcut", "🎬"}, {"notion", "📝"},
	{"github", "🐙"}, {"loveable", "💖"}, {"lovable", "💖"}, {"email", "📧"}, {"mail", "📧"}, {"youtube", "▶️"}, {"spotify", "🎧"},
	{"netflix", "🎞"}, {"vpn", "🛡"}, {"eleven", "🎙"}, {"duolingo", "🦉"}, {"doulingo", "🦉"}, {"muse", "🎭"}, {"microsoft", "🪟"},
	{"office", "🪟"}, {"google", "🔍"}, {"linkedin", "💼"}, {"vip", "⭐"},
}

func categoryEmoji(name string) string {
	n := strings.ToLower(name)
	for _, e := range categoryEmojis {
		if strings.Contains(n, e.word) {
			return e.emoji
		}
	}
	return "📦"
}

func (c *supplierCategory) title(r *req) string {
	if c.name == "" {
		return r.t("sup.cat_other")
	}
	return c.name
}

// offerButton is a product's button: blue with its price, red and "out of stock" when it cannot be bought now.
func (h *Handler) offerButton(r *req, o *corev1.SupplierOffer) tg.Button {
	label := r.t("sup.offer_btn", "name", truncRunes(o.GetName(), 40), "price", r.money(o.GetPrice().GetAmount(), o.GetPrice().GetCurrency()))
	if o.GetSoldOut() { // shown, with its description, but not for sale now
		return tg.CB(r.t("sup.offer_btn_out", "name", truncRunes(o.GetName(), 40)), "sup:p:"+o.GetSid()).Danger()
	}
	return tg.CB(label, "sup:p:"+o.GetSid()).Success()
}

// showSupplier is the digital accounts home: the supplier's categories, two to a row (red when everything in one is sold out), or, when
// the supplier has at most one category, the products themselves.
func (h *Handler) showSupplier(r *req) {
	resp, ok := h.supplierOffers(r)
	if !ok {
		return
	}
	bal := resp.GetBalance()
	cats := supplierCategories(resp.GetOffers())
	kb := &tg.Keyboard{}
	text := ""
	if len(cats) <= 1 {
		for _, o := range resp.GetOffers() {
			kb.Row(h.offerButton(r, o))
		}
		text = r.t("sup.title", "balance", r.balance(bal.GetAmount(), bal.GetCurrency()))
	} else {
		var row []tg.Button
		for _, c := range cats {
			b := tg.CB(categoryEmoji(c.name)+" "+truncRunes(c.title(r), 28), "sup:c:"+c.key).Primary()
			if c.allSoldOut() {
				b = b.Danger()
			}
			if row = append(row, b); len(row) == 2 {
				kb.Row(row...)
				row = nil
			}
		}
		kb.Row(row...)
		text = r.t("sup.title_cats", "balance", r.balance(bal.GetAmount(), bal.GetCurrency()))
	}
	kb.Row(r.button("btn.sup_my", "sup:my"), r.button("btn.home", "home"))
	if len(resp.GetOffers()) == 0 {
		text += "\n\n" + r.t("sup.empty")
	}
	r.show(text, kb)
}

// showSupplierCategory lists the products of one category, with a red Back to the categories.
func (h *Handler) showSupplierCategory(r *req, key string) {
	resp, ok := h.supplierOffers(r)
	if !ok {
		return
	}
	var cat *supplierCategory
	for _, c := range supplierCategories(resp.GetOffers()) {
		if c.key == key {
			cat = c
		}
	}
	if cat == nil {
		h.showSupplier(r)
		return
	}
	bal := resp.GetBalance()
	kb := &tg.Keyboard{}
	for _, o := range cat.offers {
		kb.Row(h.offerButton(r, o))
	}
	kb.Row(r.button("btn.back", "sup").Danger(), r.button("btn.home", "home"))
	r.show(r.t("sup.category", "category", esc(cat.title(r)), "balance", r.balance(bal.GetAmount(), bal.GetCurrency())), kb)
}

func (h *Handler) offerBySID(resp *corev1.ListSupplierOffersResponse, sid string) *corev1.SupplierOffer {
	for _, o := range resp.GetOffers() {
		if o.GetSid() == sid {
			return o
		}
	}
	return nil
}

// supplierBack is where Back and Cancel lead from a product: its category when the shop has categories, else the home.
func (h *Handler) supplierBack(resp *corev1.ListSupplierOffersResponse, o *corev1.SupplierOffer) string {
	if cats := supplierCategories(resp.GetOffers()); len(cats) > 1 {
		return "sup:c:" + categoryKey(o.GetCategory())
	}
	return "sup"
}

// supplierMaxQuantity is the most one purchase may take: 25, or what the supplier has when it says.
func supplierMaxQuantity(o *corev1.SupplierOffer) int64 {
	maxQ := int64(25)
	if o.GetStockKnown() && int64(o.GetStock()) < maxQ {
		maxQ = int64(o.GetStock())
	}
	return maxQ
}

// supplierDescriptionMax keeps a product's page inside one message.
const supplierDescriptionMax = 3000

// showSupplierProduct is a product's page: its description, the unit price and the stock, with Buy now (green) and Back (red).
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
	stock := ""
	if o.GetStockKnown() {
		stock = r.t("sup.stock_line", "n", i18n.Number(r.lang, int64(o.GetStock())))
	}
	text := r.t("sup.product", "name", esc(o.GetName()), "price", r.money(price, cur), "stock", stock)
	if d := strings.TrimSpace(o.GetDescription()); d != "" {
		text = r.t("sup.product_desc", "name", esc(o.GetName()), "description", esc(truncRunes(d, supplierDescriptionMax)), "price", r.money(price, cur), "stock", stock)
	}
	kb := &tg.Keyboard{}
	if o.GetSoldOut() {
		text += "\n\n" + r.t("sup.sold_out")
	} else {
		kb.Row(r.button("btn.sup_buy", "sup:q:"+sid))
	}
	kb.Row(r.button("btn.back", h.supplierBack(resp, o)).Danger(), r.button("btn.home", "home"))
	r.show(text, kb)
}

// showSupplierQuantity asks how many: 1, 2, 3, 5, 10, 15, 20, 25 (no more than the supplier has) or a number the customer types. A product
// with one in stock goes straight to the summary.
func (h *Handler) showSupplierQuantity(r *req, sid string) {
	resp, ok := h.supplierOffers(r)
	if !ok {
		return
	}
	o := h.offerBySID(resp, sid)
	if o == nil || o.GetSoldOut() {
		h.showSupplierProduct(r, sid)
		return
	}
	maxQ := supplierMaxQuantity(o)
	if maxQ <= 1 {
		h.showSupplierSummary(r, sid, "1")
		return
	}
	kb := &tg.Keyboard{}
	var row []tg.Button
	for _, n := range []int64{1, 2, 3, 5, 10, 15, 20, 25} {
		if n > maxQ {
			break
		}
		row = append(row, tg.CB(i18n.Number(r.lang, n), fmt.Sprintf("sup:s:%s:%d", sid, n)).Primary())
		if len(row) == 4 {
			kb.Row(row...)
			row = nil
		}
	}
	kb.Row(row...)
	kb.Row(tg.CB(r.t("btn.sup_qty_custom"), "sup:n:"+sid).Primary())
	kb.Row(r.button("btn.sup_back_product", "sup:p:"+sid).Danger(), r.button("btn.cancel", h.supplierBack(resp, o)).Danger())
	r.show(r.t("sup.qty", "name", esc(o.GetName()), "price", r.money(o.GetPrice().GetAmount(), o.GetPrice().GetCurrency())), kb)
}

// askSupplierQuantity waits for a number the customer types.
func (h *Handler) askSupplierQuantity(r *req, sid string) {
	resp, ok := h.supplierOffers(r)
	if !ok {
		return
	}
	o := h.offerBySID(resp, sid)
	if o == nil || o.GetSoldOut() {
		h.showSupplierProduct(r, sid)
		return
	}
	r.setState(sceneSupplierQuantity, map[string]string{"sid": sid})
	kb := (&tg.Keyboard{}).Row(r.button("btn.sup_back_product", "sup:q:"+sid).Danger())
	r.show(r.t("sup.qty_ask", "max", i18n.Number(r.lang, supplierMaxQuantity(o))), kb)
}

// onSupplierQuantity is the number the customer typed: the summary for it, or why it is not one (the question stays open).
func (h *Handler) onSupplierQuantity(r *req, st state.State) {
	sid := st.Get("sid")
	resp, ok := h.supplierOffers(r)
	if !ok {
		r.clearState()
		return
	}
	o := h.offerBySID(resp, sid)
	if o == nil || o.GetSoldOut() {
		r.clearState()
		h.showSupplierProduct(r, sid)
		return
	}
	maxQ := supplierMaxQuantity(o)
	q, good := i18n.ParseNumber(strings.TrimSpace(r.msg.Text))
	if !good || q < 1 || q > maxQ {
		kb := (&tg.Keyboard{}).Row(r.button("btn.sup_back_product", "sup:q:"+sid).Danger())
		r.send(r.t("sup.qty_bad", "max", i18n.Number(r.lang, maxQ)), kb)
		return
	}
	r.clearState()
	h.showSupplierSummary(r, sid, strconv.FormatInt(q, 10))
}

// showSupplierSummary is the order before it is paid: what, how many, the price, the total, the wallet and what is left of it, with Pay
// with wallet (green) when the wallet covers it, and a top-up when it does not.
func (h *Handler) showSupplierSummary(r *req, sid, qty string) {
	resp, ok := h.supplierOffers(r)
	if !ok {
		return
	}
	o := h.offerBySID(resp, sid)
	q, err := strconv.ParseInt(qty, 10, 32)
	if o == nil || o.GetSoldOut() || err != nil || q < 1 || q > supplierMaxQuantity(o) {
		h.showSupplierProduct(r, sid)
		return
	}
	price, cur := o.GetPrice().GetAmount(), o.GetPrice().GetCurrency()
	total := price * q
	bal := resp.GetBalance().GetAmount()
	kb := &tg.Keyboard{}
	verdict := r.t("sup.ready")
	after := r.balance(bal-total, cur)
	if bal >= total {
		kb.Row(r.button("btn.sup_pay", fmt.Sprintf("sup:y:%s:%d:%s", sid, q, nonce())))
	} else {
		verdict = r.t("sup.short", "short", r.money(total-bal, cur))
		after = "—"
		kb.Row(r.button("btn.sup_topup", "wtop"))
	}
	kb.Row(r.button("btn.back", "sup:q:"+sid).Danger(), r.button("btn.sup_cancel_order", h.supplierBack(resp, o)).Danger())
	r.show(r.t("sup.summary", "name", esc(o.GetName()), "qty", i18n.Number(r.lang, q), "price", r.money(price, cur), "total", r.money(total, cur),
		"balance", r.balance(bal, cur), "after", after, "verdict", verdict), kb)
}

func (h *Handler) buySupplier(r *req, sid, qty, n string) {
	q, err := strconv.ParseInt(qty, 10, 32)
	if n == "" || err != nil || q < 1 {
		h.showSupplier(r)
		return
	}
	var expected *commonv1.Money
	if resp, ok := h.supplierOffers(r); ok {
		if o := h.offerBySID(resp, sid); o != nil && !o.GetSoldOut() {
			expected = &commonv1.Money{Amount: o.GetPrice().GetAmount() * q, Currency: o.GetPrice().GetCurrency()}
		}
	} else {
		return
	}
	resp, err := h.core.BuySupplierProduct(r.ctx, &corev1.BuySupplierProductRequest{UserId: r.user.GetId(), Sid: sid, Quantity: int32(q), //nolint:gosec // 1 to 25
		IdempotencyKey: fmt.Sprintf("sup:%d:%s:%s", r.from.ID, sid, n), ExpectedTotal: expected})
	if err != nil {
		st, _ := status.FromError(err)
		switch {
		case st.Code() == codes.FailedPrecondition && strings.Contains(st.Message(), "insufficient"):
			h.showSupplierSummary(r, sid, qty)
		case st.Code() == codes.FailedPrecondition || st.Code() == codes.NotFound:
			h.showSupplierProduct(r, sid)
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
