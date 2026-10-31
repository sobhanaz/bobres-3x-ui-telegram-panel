package server

import (
	"context"
	"math"

	commonv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/common/v1"
	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
)

func toman(n int64) *commonv1.Money {
	return &commonv1.Money{Amount: n, Currency: domain.WalletCurrency}
}

// GetMyAffiliate is a customer's affiliate code and figures (the bot's /affiliate); they become an affiliate on their first request.
func (s *Server) GetMyAffiliate(ctx context.Context, req *corev1.GetMyAffiliateRequest) (*corev1.GetMyAffiliateResponse, error) {
	a, err := s.dom.MyAffiliate(fromBot(ctx), req.GetUserId())
	if err != nil {
		return nil, fail(err)
	}
	return &corev1.GetMyAffiliateResponse{Code: a.Code, Percent: int32(min(max(a.Percent, 0), math.MaxInt32)), Status: a.Status, //nolint:gosec // bounded
		Referred: a.Referred, Licenses: a.Licenses, Earned: toman(a.Earned), Paid: toman(a.Paid), Balance: toman(a.Balance),
		MinPayout: toman(a.MinPayout)}, nil
}

// ApplyAffiliateCode records that a customer came through an affiliate's link (start=aff_<code>).
func (s *Server) ApplyAffiliateCode(ctx context.Context, req *corev1.ApplyAffiliateCodeRequest) (*corev1.ApplyAffiliateCodeResponse, error) {
	ok, err := s.dom.ApplyAffiliateCode(fromBot(ctx), req.GetUserId(), req.GetCode())
	if err != nil {
		return nil, fail(err)
	}
	return &corev1.ApplyAffiliateCodeResponse{Applied: ok}, nil
}
