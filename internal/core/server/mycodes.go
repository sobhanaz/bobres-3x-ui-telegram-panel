package server

import (
	"context"

	commonv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/common/v1"
	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// My codes (proto/core/v1/codes.proto): the bot's list of the customer's own reward codes and gift codes. The customer is the one
// the bot names, as in every call of this service; core reads nothing but their own.

const (
	defaultCodesPage = 10
	maxCodesPage     = 50
)

func codeToProto(c *domain.CustomerCode) *corev1.MyCode {
	out := &corev1.MyCode{Kind: c.Kind, Code: c.Code, Tail: c.Tail, State: c.State, Percent: c.Percent, ReceivedAt: c.ReceivedAt.Unix(),
		PlanNames: c.PlanNames, OrderId: c.OrderID, OnlyPlanNames: c.OnlyPlanNames}
	if c.Amount > 0 {
		out.Amount = &commonv1.Money{Amount: c.Amount, Currency: c.Currency}
	}
	if c.ExpiresAt != nil {
		out.ExpiresAt = c.ExpiresAt.Unix()
	}
	if c.UsedAt != nil {
		out.UsedAt = c.UsedAt.Unix()
	}
	return out
}

// ListMyCodes returns a page of the customer's own codes, newest first, with how many they have in all.
func (s *Server) ListMyCodes(ctx context.Context, req *corev1.ListMyCodesRequest) (*corev1.ListMyCodesResponse, error) {
	page, size := req.GetPagination().GetPage(), req.GetPagination().GetPageSize()
	if size <= 0 || size > maxCodesPage {
		size = defaultCodesPage
	}
	if page < 1 {
		page = 1
	}
	codes, total, err := s.dom.CustomerCodes(ctx, req.GetUserId(), store.Page{Limit: int(size), Offset: int(page-1) * int(size)})
	if err != nil {
		return nil, fail(err)
	}
	out := &corev1.ListMyCodesResponse{Codes: make([]*corev1.MyCode, 0, len(codes)), PageInfo: &commonv1.PageInfo{Page: page, PageSize: size, Total: total}}
	for i := range codes {
		out.Codes = append(out.Codes, codeToProto(&codes[i]))
	}
	return out, nil
}

const (
	defaultPendingPayments = 5
	maxPendingPayments     = 10
)

// ListMyPendingPayments lists the customer's own manual payments that still wait for their proof, newest first
// (domain.PendingManualPayments, as the Mini App's list of them).
func (s *Server) ListMyPendingPayments(ctx context.Context, req *corev1.ListMyPendingPaymentsRequest) (*corev1.ListMyPendingPaymentsResponse, error) {
	if err := s.needPayments(); err != nil {
		return nil, err
	}
	limit := int(req.GetLimit())
	if limit <= 0 || limit > maxPendingPayments {
		limit = defaultPendingPayments
	}
	got, err := s.dom.PendingManualPayments(ctx, s.pay, req.GetUserId(), limit)
	if err != nil {
		return nil, fail(err)
	}
	out := &corev1.ListMyPendingPaymentsResponse{Payments: make([]*corev1.MyPendingPayment, 0, len(got))}
	for _, p := range got {
		out.Payments = append(out.Payments, &corev1.MyPendingPayment{
			IntentId: p.IntentID, Provider: p.Provider, Amount: &commonv1.Money{Amount: p.Amount, Currency: p.Currency},
			OrderId: p.OrderID, CreatedAt: p.CreatedAt.Unix(),
		})
	}
	return out, nil
}
