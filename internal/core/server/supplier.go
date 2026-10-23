package server

import (
	"context"

	commonv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/common/v1"
	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

func langOf(l string) string {
	if l == "en" {
		return "en"
	}
	return "fa"
}

func supplierOrderMsg(o *store.SupplierOrder, name string) *corev1.SupplierOrderSummary {
	return &corev1.SupplierOrderSummary{Id: o.ID, ProductName: name, Quantity: int32(o.Quantity), //nolint:gosec // 1..10
		Total: &commonv1.Money{Amount: o.Amount, Currency: o.Currency}, Status: o.Status, CreatedAt: o.CreatedAt.Unix()}
}

// ListSupplierOffers lists what the owner sells from the supplier; a store that does not answers on=false.
func (s *Server) ListSupplierOffers(ctx context.Context, req *corev1.ListSupplierOffersRequest) (*corev1.ListSupplierOffersResponse, error) {
	resp := &corev1.ListSupplierOffersResponse{}
	if !s.dom.SupplierOn(ctx) {
		return resp, nil
	}
	offers, err := s.dom.SupplierShelf(ctx)
	if err != nil {
		return nil, fail(err)
	}
	lang := langOf(req.GetLanguage())
	for i := range offers {
		p := &offers[i]
		resp.Offers = append(resp.Offers, &corev1.SupplierOffer{Sid: domain.SupplierSID(p.ID), Name: domain.SupplierName(p, lang),
			Description: domain.SupplierDescription(p, lang), Price: &commonv1.Money{Amount: *p.SellPrice, Currency: domain.SupplierCurrency},
			SoldOut: !p.Available(), Category: domain.SupplierCategory(p), Stock: stockOf(p), StockKnown: p.Stock != nil})
	}
	w, err := s.st.GetWallet(ctx, s.st.Conn(), req.GetUserId(), domain.SupplierCurrency)
	if err != nil {
		return nil, fail(err)
	}
	resp.On = true
	resp.Balance = &commonv1.Money{Amount: w.Balance, Currency: domain.SupplierCurrency}
	return resp, nil
}

// BuySupplierProduct sells a supplier product for the buyer's wallet.
func (s *Server) BuySupplierProduct(ctx context.Context, req *corev1.BuySupplierProductRequest) (*corev1.BuySupplierProductResponse, error) {
	ctx = fromBot(ctx)
	prod, err := s.dom.SupplierProductBySID(ctx, req.GetSid())
	if err != nil {
		return nil, fail(err)
	}
	p, err := s.dom.BuySupplierProduct(ctx, domain.SupplierBuyParams{UserID: req.GetUserId(), ProductID: prod.ID, Quantity: int(req.GetQuantity()),
		IdempotencyKey: req.GetIdempotencyKey(), ExpectedAmount: req.GetExpectedTotal().GetAmount()})
	if err != nil {
		return nil, fail(err)
	}
	return &corev1.BuySupplierProductResponse{OrderStatus: p.Order.Status, OrderId: p.Order.ID, Delivery: p.Delivery, Replayed: p.Replayed,
		Total: &commonv1.Money{Amount: p.Order.Amount, Currency: p.Order.Currency}, Failure: p.Order.LastError}, nil
}

// ListMySupplierOrders lists a customer's purchases (never what was delivered).
func (s *Server) ListMySupplierOrders(ctx context.Context, req *corev1.ListMySupplierOrdersRequest) (*corev1.ListMySupplierOrdersResponse, error) {
	orders, err := s.dom.MySupplierOrders(ctx, req.GetUserId())
	if err != nil {
		return nil, fail(err)
	}
	lang := langOf(req.GetLanguage())
	resp := &corev1.ListMySupplierOrdersResponse{}
	for i := range orders {
		name := orders[i].ProductID
		if p, err := s.st.GetSupplierProduct(ctx, s.st.Conn(), orders[i].ProductID, false); err == nil {
			name = domain.SupplierName(p, lang)
		}
		resp.Orders = append(resp.Orders, supplierOrderMsg(&orders[i], name))
	}
	return resp, nil
}

// GetSupplierDelivery shows what a done order delivered, to its buyer only. It is never logged: the interceptors log the
// method, not the message.
func (s *Server) GetSupplierDelivery(ctx context.Context, req *corev1.GetSupplierDeliveryRequest) (*corev1.GetSupplierDeliveryResponse, error) {
	p, err := s.dom.SupplierDelivery(ctx, req.GetUserId(), req.GetOrderId())
	if err != nil {
		return nil, fail(err)
	}
	name := p.Order.ProductID
	if prod, err := s.st.GetSupplierProduct(ctx, s.st.Conn(), p.Order.ProductID, false); err == nil {
		name = domain.SupplierName(prod, langOf(req.GetLanguage()))
	}
	return &corev1.GetSupplierDeliveryResponse{Order: supplierOrderMsg(&p.Order, name), Delivery: p.Delivery}, nil
}

// stockOf is how many a supplier product has (0 when the supplier does not say).
func stockOf(p *store.SupplierProduct) int32 {
	if p.Stock == nil {
		return 0
	}
	return *p.Stock
}
