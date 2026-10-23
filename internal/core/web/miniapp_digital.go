package web

import (
	"context"
	"errors"
	"net/http"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	bmoney "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/money"
)

// Digital accounts in the Mini App: the same shelf the bot sells (the products of the supplier the owner switched on and priced), the same
// wallet, the same purchase and the same idempotency, so a purchase made here or in the bot is one kind of order. The section is only for a store
// that sells them (the owner's own, with private_features); everywhere else the flag is false and the routes answer 404.

func init() {
	registerMiniFeature(miniFeature{
		routes: func(s *Server, api *http.ServeMux) {
			api.HandleFunc("GET /api/mini/v1/digital", s.miniSession(false, s.miniDigitalShelf))
			api.HandleFunc("POST /api/mini/v1/digital/buy", s.miniSession(false, s.miniDigitalBuy))
			api.HandleFunc("GET /api/mini/v1/digital/orders", s.miniSession(false, s.miniDigitalOrders))
			api.HandleFunc("GET /api/mini/v1/digital/orders/{id}", s.miniSession(false, s.miniDigitalOrder))
		},
		me: func(s *Server, ctx context.Context, _ *store.User, features map[string]any) error {
			offers, err := s.cfg.Domain.SupplierShelf(ctx)
			features["digital"] = err == nil && len(offers) > 0
			return err
		},
	})
}

// digitalNames is a product's name in both languages (the app picks).
func digitalNames(p *store.SupplierProduct) map[string]string {
	return map[string]string{"fa": domain.SupplierName(p, "fa"), "en": domain.SupplierName(p, "en")}
}

func digitalMoney(amount int64) map[string]any {
	return map[string]any{"amount": amount, "currency": domain.SupplierCurrency, "scale": bmoney.Scale(domain.SupplierCurrency)}
}

// digitalOrderBody is an order as the app shows it; the delivery (the account) is only in the answer for one order and for a purchase just made.
func (s *Server) digitalOrderBody(ctx context.Context, o *store.SupplierOrder) map[string]any {
	names := map[string]string{"fa": o.ProductID, "en": o.ProductID}
	if p, err := s.cfg.Store.GetSupplierProduct(ctx, s.cfg.Store.Conn(), o.ProductID, false); err == nil {
		names = digitalNames(p)
	}
	return map[string]any{"id": o.ID, "names": names, "quantity": o.Quantity, "total": digitalMoney(o.Amount), "status": o.Status,
		"at": o.CreatedAt.Unix()}
}

// miniDigitalShelf: GET /api/mini/v1/digital, what is on sale and the customer's wallet.
func (s *Server) miniDigitalShelf(w http.ResponseWriter, r *http.Request) {
	ctx, c := r.Context(), miniFrom(r.Context())
	offers, err := s.cfg.Domain.SupplierShelf(ctx)
	if err != nil {
		s.miniFail(w, "mini app digital shelf", err)
		return
	}
	if len(offers) == 0 {
		writeError(w, http.StatusNotFound, "not_found", "no such item")
		return
	}
	wallets, err := s.miniWallets(ctx, c.user.ID)
	if err != nil {
		s.miniFail(w, "mini app digital wallet", err)
		return
	}
	var wallet map[string]any
	for _, m := range wallets {
		if m["currency"] == domain.SupplierCurrency {
			wallet = m
		}
	}
	items := make([]map[string]any, 0, len(offers))
	for i := range offers {
		p := &offers[i]
		stock := int32(0)
		if p.Stock != nil {
			stock = *p.Stock
		}
		items = append(items, map[string]any{"sid": domain.SupplierSID(p.ID), "names": digitalNames(p),
			"descriptions": map[string]string{"fa": domain.SupplierDescription(p, "fa"), "en": domain.SupplierDescription(p, "en")},
			"category":     domain.SupplierCategory(p), "price": *p.SellPrice, "sold_out": !p.Available(), "stock": stock, "stock_known": p.Stock != nil})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "wallet": wallet, "currency": domain.SupplierCurrency,
		"scale": bmoney.Scale(domain.SupplierCurrency), "max_quantity": domain.SupplierMaxQuantity})
}

type digitalBuyIn struct {
	SID           string `json:"sid"`
	Quantity      int    `json:"quantity"`
	ExpectedTotal int64  `json:"expected_total"`
	Key           string `json:"key"`
}

// miniDigitalBuy: POST /api/mini/v1/digital/buy. The wallet pays; a retry with the same key returns the first result and charges nothing again. The
// answer is the order, with the account in it once the supplier has delivered; until then its status is "paid" and the app asks for the order again.
func (s *Server) miniDigitalBuy(w http.ResponseWriter, r *http.Request) {
	ctx, c := r.Context(), miniFrom(r.Context())
	var in digitalBuyIn
	if !readJSON(w, r, &in) {
		return
	}
	if !miniKeyRe.MatchString(in.Key) {
		s.miniFail(w, "mini app digital buy", errInvalid("a request key of 8 to 64 letters, digits, - or _ is required"))
		return
	}
	prod, err := s.cfg.Domain.SupplierProductBySID(ctx, in.SID)
	if err != nil {
		s.miniFail(w, "mini app digital buy", err)
		return
	}
	p, err := s.cfg.Domain.BuySupplierProduct(ctx, domain.SupplierBuyParams{UserID: c.user.ID, ProductID: prod.ID, Quantity: in.Quantity,
		IdempotencyKey: "mini:" + c.user.ID + ":" + in.Key, ExpectedAmount: in.ExpectedTotal})
	if err != nil {
		s.miniFail(w, "mini app digital buy", err)
		return
	}
	body := s.digitalOrderBody(ctx, &p.Order)
	body["delivery"] = p.Delivery
	body["replayed"] = p.Replayed
	body["failure"] = p.Order.LastError != "" && p.Order.Status != "done"
	writeJSON(w, http.StatusOK, body)
}

// miniDigitalOrders: GET /api/mini/v1/digital/orders, the customer's purchases, newest first (never what was delivered).
func (s *Server) miniDigitalOrders(w http.ResponseWriter, r *http.Request) {
	ctx, c := r.Context(), miniFrom(r.Context())
	orders, err := s.cfg.Domain.MySupplierOrders(ctx, c.user.ID)
	if err != nil {
		s.miniFail(w, "mini app digital orders", err)
		return
	}
	out := make([]map[string]any, 0, len(orders))
	for i := range orders {
		out = append(out, s.digitalOrderBody(ctx, &orders[i]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"orders": out})
}

// miniDigitalOrder: GET /api/mini/v1/digital/orders/{id}, one order of the customer with what it delivered. Another customer's order does not exist.
func (s *Server) miniDigitalOrder(w http.ResponseWriter, r *http.Request) {
	ctx, c := r.Context(), miniFrom(r.Context())
	if !miniUUIDRe.MatchString(r.PathValue("id")) {
		s.miniFail(w, "mini app digital order", store.ErrNotFound)
		return
	}
	p, err := s.cfg.Domain.SupplierDelivery(ctx, c.user.ID, r.PathValue("id"))
	if errors.Is(err, domain.ErrForbidden) {
		err = store.ErrNotFound
	}
	if err != nil {
		s.miniFail(w, "mini app digital order", err)
		return
	}
	body := s.digitalOrderBody(ctx, &p.Order)
	body["delivery"] = p.Delivery
	writeJSON(w, http.StatusOK, body)
}
