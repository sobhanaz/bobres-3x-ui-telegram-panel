package server

import (
	"context"

	commonv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/common/v1"
	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

func vendorLicenseMsg(l *store.VendorLicense) *corev1.VendorLicense {
	if l == nil {
		return nil
	}
	m := &corev1.VendorLicense{Id: l.ID, Tier: l.Tier, Status: l.Status}
	if l.EndsAt != nil {
		m.EndsAt = l.EndsAt.Unix()
	}
	return m
}

// ListVendorOffers says whether this store sells licenses and what it asks for each tier and term. A store that
// does not (every customer's install) answers on=false with nothing else.
func (s *Server) ListVendorOffers(ctx context.Context, req *corev1.ListVendorOffersRequest) (*corev1.ListVendorOffersResponse, error) {
	resp := &corev1.ListVendorOffersResponse{}
	if !s.dom.VendorOn(ctx) {
		return resp, nil
	}
	tiers, err := s.dom.VendorTiers()
	if err != nil {
		return nil, fail(err)
	}
	for _, t := range tiers {
		for _, m := range []int{domain.VendorMonth, domain.VendorYear} {
			list := t.Price(m)
			price, rate, err := s.dom.VendorToman(ctx, &t, list)
			if err != nil {
				continue // no USDT rate now: the offer is not shown (nothing could be priced)
			}
			resp.Rate = rate
			resp.Offers = append(resp.Offers, &corev1.VendorOffer{Tier: t.Name, Months: int32(m), //nolint:gosec // 1 or 12
				Price:     &commonv1.Money{Amount: price, Currency: domain.WalletCurrency},
				ListPrice: &commonv1.Money{Amount: list, Currency: t.Currency}})
		}
	}
	if len(resp.Offers) == 0 {
		return resp, nil
	}
	w, err := s.st.GetWallet(ctx, s.st.Conn(), req.GetUserId(), domain.WalletCurrency)
	if err != nil {
		return nil, fail(err)
	}
	resp.On = true
	resp.Balance = &commonv1.Money{Amount: w.Balance, Currency: domain.WalletCurrency}
	resp.BalanceUsdt = s.dom.UsdtEquivalent(ctx, w.Balance)
	return resp, nil
}

// BuyVendorLicense sells (or renews) a license for the buyer's wallet. A buyer who is short of money gets
// FAILED_PRECONDITION "insufficient funds" and nothing is written.
func (s *Server) BuyVendorLicense(ctx context.Context, req *corev1.BuyVendorLicenseRequest) (*corev1.BuyVendorLicenseResponse, error) {
	ctx = fromBot(ctx)
	p, err := s.dom.BuyVendorLicense(ctx, domain.VendorBuyParams{
		UserID: req.GetUserId(), Tier: req.GetTier(), Months: int(req.GetMonths()), LicenseID: req.GetLicenseId(),
		IdempotencyKey: req.GetIdempotencyKey(), ExpectedAmount: req.GetExpectedPrice().GetAmount(),
	})
	if err != nil {
		return nil, fail(err)
	}
	return &corev1.BuyVendorLicenseResponse{
		OrderStatus: p.Order.Status, License: vendorLicenseMsg(p.License), Key: p.Key, Replayed: p.Replayed,
		Price:   &commonv1.Money{Amount: p.Order.Amount, Currency: p.Order.Currency},
		Failure: p.Order.LastError,
	}, nil
}

// SetVendorIntent records what a customer meant to buy when they tapped a payment method for the shortfall; core buys it by itself once the
// payment lands and the wallet covers it.
func (s *Server) SetVendorIntent(ctx context.Context, req *corev1.SetVendorIntentRequest) (*corev1.SetVendorIntentResponse, error) {
	ctx = fromBot(ctx)
	if err := s.dom.SetVendorIntent(ctx, req.GetUserId(), req.GetTier(), int(req.GetMonths()), req.GetLicenseId()); err != nil {
		return nil, fail(err)
	}
	return &corev1.SetVendorIntentResponse{}, nil
}

// ListMyVendorLicenses lists the licenses a customer bought (never their keys).
func (s *Server) ListMyVendorLicenses(ctx context.Context, req *corev1.ListMyVendorLicensesRequest) (*corev1.ListMyVendorLicensesResponse, error) {
	ls, err := s.dom.MyVendorLicenses(ctx, req.GetUserId())
	if err != nil {
		return nil, fail(err)
	}
	resp := &corev1.ListMyVendorLicensesResponse{}
	for i := range ls {
		resp.Licenses = append(resp.Licenses, vendorLicenseMsg(&ls[i]))
	}
	return resp, nil
}

// GetVendorLicenseKey is the activation key of a license its buyer owns. The key is never logged: the interceptors
// log the method, not the message.
func (s *Server) GetVendorLicenseKey(ctx context.Context, req *corev1.GetVendorLicenseKeyRequest) (*corev1.GetVendorLicenseKeyResponse, error) {
	l, key, err := s.dom.ShowVendorKey(ctx, req.GetUserId(), req.GetLicenseId())
	if err != nil {
		return nil, fail(err)
	}
	return &corev1.GetVendorLicenseKeyResponse{License: vendorLicenseMsg(l), Key: key}, nil
}
