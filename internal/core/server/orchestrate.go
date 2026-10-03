package server

import (
	"context"

	commonv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/common/v1"
	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// SetPayments injects the payments client for orchestration RPCs.
func (s *Server) SetPayments(p domain.PaymentsClient) { s.pay = p }

// SetProvisioner injects the provisioner client (links, panel health).
func (s *Server) SetProvisioner(p domain.Provisioner) { s.prov = p }

func (s *Server) needPayments() error {
	if s.pay == nil {
		return status.Error(codes.FailedPrecondition, "payments client not configured")
	}
	return nil
}

func (s *Server) needProvisioner() error {
	if s.prov == nil {
		return status.Error(codes.FailedPrecondition, "provisioner client not configured")
	}
	return nil
}

// PayOrderWithWallet debits the wallet and marks the order paid.
func (s *Server) PayOrderWithWallet(ctx context.Context, req *corev1.PayOrderWithWalletRequest) (*corev1.Order, error) {
	o, err := s.dom.PayOrderWithWallet(ctx, req.GetOrderId(), req.GetUserId())
	if err != nil {
		return nil, fail(err)
	}
	return orderToProto(o), nil
}

// CreatePaymentIntent creates a payments intent (order payment or wallet top-up)
// and returns rendered instructions.
func (s *Server) CreatePaymentIntent(ctx context.Context, req *corev1.CreatePaymentIntentRequest) (*corev1.PaymentIntentRef, error) {
	if err := s.needPayments(); err != nil {
		return nil, err
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
		Amount:       &commonv1.Money{Amount: res.Amount, Currency: res.Currency},
		Details:      res.Details,
	}, nil
}

// SubmitPaymentProof forwards a receipt or TXID to payments.
func (s *Server) SubmitPaymentProof(ctx context.Context, req *corev1.SubmitPaymentProofRequest) (*corev1.PaymentIntentRef, error) {
	if err := s.needPayments(); err != nil {
		return nil, err
	}
	st, err := s.dom.SubmitPaymentProof(ctx, s.pay, domain.ProofParams{
		UserID: req.GetUserId(), IntentID: req.GetIntentId(),
		ReceiptFile: req.GetReceiptFile(), ReferenceNumber: req.GetReferenceNumber(),
		Network: req.GetNetwork(), TXID: req.GetTxid(),
	})
	if err != nil {
		return nil, fail(err)
	}
	return &corev1.PaymentIntentRef{Id: req.GetIntentId(), Status: st}, nil
}

// GetSubscriptionLinks returns the user's subscription link and QR code.
func (s *Server) GetSubscriptionLinks(ctx context.Context, req *corev1.GetSubscriptionLinksRequest) (*corev1.SubscriptionLinks, error) {
	if err := s.needProvisioner(); err != nil {
		return nil, err
	}
	l, err := s.dom.SubscriptionLinks(ctx, s.prov, req.GetUserId(), req.GetSubscriptionId())
	if err != nil {
		return nil, fail(err)
	}
	return &corev1.SubscriptionLinks{SubscriptionLink: l.SubscriptionLink, QrPng: l.QRPNG, ConfigLinks: l.ConfigLinks}, nil
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
