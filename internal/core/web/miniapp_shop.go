package web

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	bmoney "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/money"
)

// The Mini App's shop calls: plans, services, orders, payments, top-up. Each
// acts on the signed-in customer only; an order or payment that is not theirs
// is "not found".

func miniUnix(t *int64) any {
	if t == nil {
		return nil
	}
	return *t
}

// miniPlans: GET /api/mini/v1/plans. Enabled plans at the customer's price
// (a reseller's tier price), free trials left out (they start in the bot).
func (s *Server) miniPlans(w http.ResponseWriter, r *http.Request) {
	ctx, c := r.Context(), miniFrom(r.Context())
	all, err := s.cfg.Store.ListPlans(ctx, s.cfg.Store.Conn(), false)
	if err != nil {
		s.miniFail(w, "mini app plans", err)
		return
	}
	plans := make([]store.Plan, 0, len(all))
	for _, p := range all {
		if p.Enabled && !p.IsTrial {
			plans = append(plans, p)
		}
	}
	prices, err := s.cfg.Domain.PlanPrices(ctx, c.user.ID, plans)
	if err != nil {
		s.miniFail(w, "mini app prices", err)
		return
	}
	out := make([]map[string]any, 0, len(plans))
	for i, p := range plans {
		var days, traffic any
		if p.DurationDays != nil {
			days = *p.DurationDays
		}
		if p.TrafficBytes != nil {
			traffic = *p.TrafficBytes
		}
		var group any // the plan group it is listed under (null: none)
		if p.GroupID != nil {
			group = *p.GroupID
		}
		out = append(out, map[string]any{
			"id": p.ID, "names": p.NameI18n, "kind": p.Kind, "days": days, "traffic_bytes": traffic,
			"price": prices[i], "list_price": p.Price, "currency": p.Currency, "scale": bmoney.Scale(p.Currency), "topup": p.IsTopup,
			"group": group,
		})
	}
	// The groups, in the order customers see them: a customer reads a group's text before choosing one of its plans.
	groups, err := s.cfg.Domain.ListPlanGroups(ctx)
	if err != nil {
		s.miniFail(w, "mini app plan groups", err)
		return
	}
	gout := make([]map[string]any, 0, len(groups))
	for _, g := range groups {
		gout = append(gout, map[string]any{"id": g.ID, "names": g.NameI18n, "texts": g.DescriptionI18n})
	}
	writeJSON(w, http.StatusOK, map[string]any{"plans": out, "groups": gout})
}

// miniServices: GET /api/mini/v1/services, the customer's services newest
// first, each with its link and what can be done to it.
func (s *Server) miniServices(w http.ResponseWriter, r *http.Request) {
	ctx, c := r.Context(), miniFrom(r.Context())
	subs, err := s.cfg.Store.ListSubscriptions(ctx, s.cfg.Store.Conn(), c.user.ID, 50)
	if err != nil {
		s.miniFail(w, "mini app services", err)
		return
	}
	out := make([]map[string]any, 0, len(subs))
	for i := range subs {
		sub := &subs[i]
		renewable := sub.SubLink != "" && (sub.Status == "active" || sub.Status == "expiring_soon" || sub.Status == "expired" || sub.Status == "depleted")
		var expires any
		if sub.ExpiresAt != nil {
			expires = sub.ExpiresAt.Unix()
		}
		label := ""
		if sub.Label != nil {
			label = *sub.Label
		}
		out = append(out, map[string]any{
			"id": sub.ID, "label": label, "status": sub.Status, "expires_at": expires,
			"traffic_total": miniUnix(sub.TrafficTotal), "traffic_used": sub.TrafficUsed,
			"link": sub.SubLink, "page_url": s.pageURL(r, sub),
			"can_renew": renewable,
			"can_topup": renewable && sub.TrafficTotal != nil && *sub.TrafficTotal > 0 && sub.Status != "expired",
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"services": out})
}

// miniWallet: GET /api/mini/v1/wallet, balances and the latest movements.
func (s *Server) miniWallet(w http.ResponseWriter, r *http.Request) {
	ctx, c := r.Context(), miniFrom(r.Context())
	wallets, err := s.miniWallets(ctx, c.user.ID)
	if err != nil {
		s.miniFail(w, "mini app wallet", err)
		return
	}
	entries, err := s.cfg.Store.ListLedger(ctx, s.cfg.Store.Conn(), c.user.ID, miniLedger, 0)
	if err != nil {
		s.miniFail(w, "mini app ledger", err)
		return
	}
	ledger := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		ledger = append(ledger, map[string]any{"kind": e.Kind, "amount": e.Amount, "currency": e.Currency, "balance_after": e.BalanceAfter,
			"at": e.CreatedAt.Unix(), "scale": bmoney.Scale(e.Currency)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"wallets": wallets, "ledger": ledger})
}

// miniPaymentMethods: GET /api/mini/v1/methods?currency=IRT&amount=N, the
// payment methods the store offers for that price right now: the online ones
// (Stars among them) and the manual ones (card to card, USDT, a Zarinpal
// link), the same the bot offers.
func (s *Server) miniPaymentMethods(w http.ResponseWriter, r *http.Request) {
	currency := r.URL.Query().Get("currency")
	if currency != "IRT" && currency != "USDT" {
		s.miniFail(w, "mini app methods", errInvalid("currency must be IRT or USDT"))
		return
	}
	amount, err := strconv.ParseInt(r.URL.Query().Get("amount"), 10, 64)
	if err != nil || amount < 0 || amount > miniMaxTopupMinor {
		s.miniFail(w, "mini app methods", errInvalid("amount out of range"))
		return
	}
	methods, err := s.miniOffered(r.Context(), currency, amount)
	if err != nil {
		s.miniFail(w, "mini app methods", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"methods": methods})
}

// miniMaxTopupMinor bounds the amount of a methods query (the largest price).
const miniMaxTopupMinor = 1_000_000_000_000

func errInvalid(msg string) error { return fmt.Errorf("%w: %s", domain.ErrInvalid, msg) }

type miniOrderIn struct {
	PlanID         string `json:"plan_id"`
	Type           string `json:"type"`
	SubscriptionID string `json:"subscription_id"`
	DiscountCode   string `json:"discount_code"`
	Key            string `json:"key"`
	// Label is a reseller's name for the new service they buy for a customer
	// (core ignores it for anyone else, and on renewals and top-ups).
	Label string `json:"label"`
}

func (in *miniOrderIn) check() error {
	switch in.Type {
	case "":
		in.Type = domain.OrderNew
	case domain.OrderNew, domain.OrderRenew, domain.OrderTrafficTopup:
	default:
		return errInvalid("unknown order type")
	}
	if !miniUUIDRe.MatchString(in.PlanID) || (in.SubscriptionID != "" && !miniUUIDRe.MatchString(in.SubscriptionID)) || len(in.DiscountCode) > 64 {
		return errInvalid("a plan is required")
	}
	if (in.Type == domain.OrderNew) != (in.SubscriptionID == "") {
		return errInvalid("a renewal or top-up names its service, a new service does not")
	}
	return nil
}

// miniQuote: POST /api/mini/v1/quote prices an order (with a discount code)
// without creating it.
func (s *Server) miniQuote(w http.ResponseWriter, r *http.Request) {
	ctx, c := r.Context(), miniFrom(r.Context())
	var in miniOrderIn
	if !readJSON(w, r, &in) {
		return
	}
	if err := in.check(); err != nil {
		s.miniFail(w, "mini app quote", err)
		return
	}
	q, err := s.cfg.Domain.QuoteOrder(ctx, domain.QuoteParams{UserID: c.user.ID, PlanID: in.PlanID, Type: in.Type,
		SubscriptionID: in.SubscriptionID, DiscountCode: in.DiscountCode})
	if err != nil {
		s.miniFail(w, "mini app quote", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"list_price": q.ListPrice, "discount": q.Discount, "total": q.Total, "currency": q.Currency,
		"scale": bmoney.Scale(q.Currency), "discount_code": q.DiscountCode})
}

func miniOrderBody(o *store.Order) map[string]any {
	sub := ""
	if o.SubscriptionID != nil {
		sub = *o.SubscriptionID
	}
	return map[string]any{"id": o.ID, "plan_id": o.PlanID, "type": o.Type, "status": o.Status, "amount": o.Amount,
		"discount_amount": o.DiscountAmount, "currency": o.Currency, "scale": bmoney.Scale(o.Currency), "subscription_id": sub}
}

// miniCreateOrder: POST /api/mini/v1/orders. The key is the app's own random
// string for this purchase: sending it again (a double tap, a retry) returns
// the same order.
func (s *Server) miniCreateOrder(w http.ResponseWriter, r *http.Request) {
	ctx, c := r.Context(), miniFrom(r.Context())
	var in miniOrderIn
	if !readJSON(w, r, &in) {
		return
	}
	if err := in.check(); err != nil {
		s.miniFail(w, "mini app order", err)
		return
	}
	if !miniKeyRe.MatchString(in.Key) {
		s.miniFail(w, "mini app order", errInvalid("a request key of 8 to 64 letters, digits, - or _ is required"))
		return
	}
	o, err := s.cfg.Domain.CreateOrder(ctx, domain.CreateOrderParams{UserID: c.user.ID, PlanID: in.PlanID, Type: in.Type,
		SubscriptionID: in.SubscriptionID, DiscountCode: in.DiscountCode, Label: in.Label, IdempotencyKey: "mini:" + c.user.ID + ":" + in.Key})
	if err != nil {
		s.miniFail(w, "mini app order", err)
		return
	}
	writeJSON(w, http.StatusOK, miniOrderBody(o))
}

// ownOrder loads an order of the signed-in customer; anything else is not found.
func (s *Server) ownOrder(r *http.Request) (*store.Order, error) {
	if !miniUUIDRe.MatchString(r.PathValue("id")) {
		return nil, store.ErrNotFound
	}
	o, err := s.cfg.Store.GetOrder(r.Context(), s.cfg.Store.Conn(), r.PathValue("id"))
	if err != nil {
		return nil, err
	}
	if o.UserID != miniFrom(r.Context()).user.ID {
		return nil, store.ErrNotFound
	}
	return o, nil
}

// miniGetOrder: GET /api/mini/v1/orders/{id}, polled while an order is paid
// and delivered.
func (s *Server) miniGetOrder(w http.ResponseWriter, r *http.Request) {
	o, err := s.ownOrder(r)
	if err != nil {
		s.miniFail(w, "mini app order", err)
		return
	}
	writeJSON(w, http.StatusOK, miniOrderBody(o))
}

// miniPayWallet: POST /api/mini/v1/orders/{id}/pay-wallet. Paying twice is
// harmless: the second call sees the order paid.
func (s *Server) miniPayWallet(w http.ResponseWriter, r *http.Request) {
	o, err := s.ownOrder(r)
	if err != nil {
		s.miniFail(w, "mini app pay", err)
		return
	}
	paid, err := s.cfg.Domain.PayOrderWithWallet(r.Context(), o.ID, o.UserID)
	if err != nil {
		s.miniFail(w, "mini app pay", err)
		return
	}
	writeJSON(w, http.StatusOK, miniOrderBody(paid))
}

// miniIntentDetails are the details of a payment the customer is shown: the
// USDT address of an automatic payment, and what a manual payment says (the
// card, the addresses, the link, the payment reference), as in the bot.
var miniIntentDetails = []string{"pay_address", "network", "expires_at", "reference",
	"card_number", "card_holder", "usdt_trc20", "usdt_erc20", "zarinpal_link"}

func miniIntentBody(res *domain.IntentResult) map[string]any {
	details := map[string]string{}
	for _, k := range miniIntentDetails {
		if v := res.Details[k]; v != "" {
			details[k] = v
		}
	}
	return map[string]any{"intent_id": res.IntentID, "status": res.Status, "provider": res.Provider, "order_id": res.OrderID,
		"amount": res.Amount, "currency": res.Currency, "scale": bmoney.Scale(res.Currency), "pay_url": res.PayURL,
		"gateway_amount": res.GatewayAmount, "gateway_currency": res.GatewayCurrency, "scale_gateway": bmoney.Scale(res.GatewayCurrency), "details": details}
}

type miniPayIn struct {
	Method string `json:"method"`
}

// miniStartPayment makes the payment of a purchase or a top-up by a method the
// store offers for its price (the amount and the currency are the order's, or
// the top-up's, never the request's) and answers what the app shows next:
// a page to open, a USDT address, a Stars invoice to open, or what to pay by
// hand and where to send the proof.
func (s *Server) miniStartPayment(w http.ResponseWriter, r *http.Request, p domain.CreatePaymentIntentParams, price miniPrice) {
	ctx := r.Context()
	if s.cfg.Payments == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "payments are not connected")
		return
	}
	if !miniMethods[p.Provider] {
		s.miniFail(w, "mini app pay", errInvalid("unknown payment method"))
		return
	}
	// What the store offers for this price now, asked again: the list the app
	// showed may be old, and a method must not be started that was never offered.
	offered, err := s.miniOffered(ctx, price.currency, price.amount)
	if err != nil {
		s.miniFail(w, "mini app payment", err)
		return
	}
	if !slices.Contains(offered, p.Provider) {
		writeError(w, http.StatusBadRequest, "method_off", "this payment method is not available for this payment")
		return
	}
	res, err := s.cfg.Domain.CreatePaymentIntent(ctx, s.cfg.Payments, p)
	if err != nil {
		s.miniFail(w, "mini app payment", err)
		return
	}
	body := miniIntentBody(res)
	switch err := s.miniPaymentExtras(ctx, miniFrom(ctx).user, res, price, body); {
	case errors.Is(err, errMethodOff):
		writeError(w, http.StatusBadRequest, "method_off", "this payment method is not available for this payment")
		return
	case err != nil:
		s.miniBotFail(w, "invoice", err)
		return
	}
	writeJSON(w, http.StatusOK, body)
}

// miniPayOrder: POST /api/mini/v1/orders/{id}/pay {method}. The intent's key
// is the bot's, so an order started in either place continues in the other.
func (s *Server) miniPayOrder(w http.ResponseWriter, r *http.Request) {
	var in miniPayIn
	if !readJSON(w, r, &in) {
		return
	}
	o, err := s.ownOrder(r)
	if err != nil {
		s.miniFail(w, "mini app pay", err)
		return
	}
	s.miniStartPayment(w, r, domain.CreatePaymentIntentParams{UserID: o.UserID, OrderID: o.ID, Provider: in.Method,
		IdempotencyKey: "int:" + o.ID + ":" + in.Method}, miniPrice{amount: o.Amount, currency: o.Currency, planID: o.PlanID})
}

// miniTopup: POST /api/mini/v1/topup {amount, method, key} in Toman.
func (s *Server) miniTopup(w http.ResponseWriter, r *http.Request) {
	c := miniFrom(r.Context())
	var in struct {
		Amount int64  `json:"amount"`
		Method string `json:"method"`
		Key    string `json:"key"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	switch {
	case in.Amount < miniMinTopup || in.Amount > miniMaxTopup:
		s.miniFail(w, "mini app topup", errInvalid("top-up amount out of range"))
		return
	case !miniKeyRe.MatchString(in.Key):
		s.miniFail(w, "mini app topup", errInvalid("a request key of 8 to 64 letters, digits, - or _ is required"))
		return
	}
	s.miniStartPayment(w, r, domain.CreatePaymentIntentParams{UserID: c.user.ID, Provider: in.Method, Amount: in.Amount, Currency: "IRT",
		IdempotencyKey: "mini:topup:" + c.user.ID + ":" + in.Key + ":" + in.Method}, miniPrice{amount: in.Amount, currency: "IRT"})
}

// miniCheckPayment: POST /api/mini/v1/payments/{id}/check asks payments to check the
// customer's own payment now ("I paid").
func (s *Server) miniCheckPayment(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Payments == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "payments are not connected")
		return
	}
	c := miniFrom(r.Context())
	// A payment id is a UUID, in either case; anything else is no payment of
	// this customer (payments answers an internal error to a malformed one).
	id := strings.ToLower(r.PathValue("id"))
	if !miniUUIDRe.MatchString(id) {
		s.miniFail(w, "mini app payment check", store.ErrNotFound)
		return
	}
	gi, err := s.cfg.Domain.CheckPayment(r.Context(), s.cfg.Payments, c.user.ID, id)
	if err != nil {
		s.miniFail(w, "mini app payment check", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"intent_id": gi.ID, "status": gi.Status, "order_id": gi.OrderID, "provider": gi.Provider,
		"failure_reason": gi.FailureReason})
}
