package server

import (
	"context"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// SetPayments injects the payments client for orchestration RPCs.
func (s *Server) SetPayments(p domain.PaymentsClient) { s.pay = p }

// PayOrderWithWallet debits the wallet and marks the order paid.
func (s *Server) PayOrderWithWallet(ctx context.Context, req *corev1.PayOrderWithWalletRequest) (*corev1.Order, error) {
	o, err := s.dom.PayOrderWithWallet(ctx, req.GetOrderId(), req.GetIdempotencyKey())
	if err != nil {
		return nil, fail(err)
	}
	return orderToProto(o), nil
}

// CreatePaymentIntent creates a payments intent (order payment or wallet top-up)
// and returns rendered instructions.
func (s *Server) CreatePaymentIntent(ctx context.Context, req *corev1.CreatePaymentIntentRequest) (*corev1.PaymentIntentRef, error) {
	if s.pay == nil {
		return nil, status.Error(codes.FailedPrecondition, "payments client not configured")
	}
	res, err := s.dom.CreatePaymentIntent(ctx, s.pay, domain.CreatePaymentIntentParams{
		UserID:         req.GetUserId(),
		OrderID:        req.GetOrderId(),
		Provider:       req.GetProvider(),
		Amount:         req.GetAmount().GetAmount(),
		Currency:       req.GetAmount().GetCurrency(),
		IdempotencyKey: req.GetIdempotencyKey(),
	})
	if err != nil {
		return nil, fail(err)
	}
	return &corev1.PaymentIntentRef{
		Id:           res.IntentID,
		Status:       res.Status,
		OrderId:      res.OrderID,
		Provider:     res.Provider,
		Instructions: res.Instructions,
	}, nil
}

// StartTrial runs the once-per-user free trial flow.
func (s *Server) StartTrial(ctx context.Context, req *corev1.StartTrialRequest) (*corev1.Order, error) {
	o, err := s.dom.StartTrial(ctx, req.GetUserId(), req.GetPlanId(), req.GetIdempotencyKey())
	if err != nil {
		return nil, fail(err)
	}
	return orderToProto(o), nil
}

// CanStartTrial reports trial eligibility.
func (s *Server) CanStartTrial(ctx context.Context, req *corev1.CanStartTrialRequest) (*corev1.CanStartTrialResponse, error) {
	ok, err := s.dom.CanStartTrial(ctx, req.GetUserId())
	if err != nil {
		return nil, fail(err)
	}
	return &corev1.CanStartTrialResponse{Eligible: ok}, nil
}
