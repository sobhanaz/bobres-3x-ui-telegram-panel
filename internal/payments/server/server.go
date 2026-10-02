// Package server exposes the payments domain over gRPC (PaymentsService).
package server

import (
	"context"
	"errors"

	commonv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/common/v1"
	paymentsv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/payments/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/store"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Server implements paymentsv1.PaymentsServiceServer.
type Server struct {
	paymentsv1.UnimplementedPaymentsServiceServer
	svc *domain.Service
	st  *store.Store
}

// New builds the server.
func New(svc *domain.Service, st *store.Store) *Server { return &Server{svc: svc, st: st} }

// Register attaches the service.
func (s *Server) Register(g *grpc.Server) { paymentsv1.RegisterPaymentsServiceServer(g, s) }

// CreateIntent validates and stores a pending intent.
func (s *Server) CreateIntent(ctx context.Context, req *paymentsv1.CreateIntentRequest) (*paymentsv1.PaymentIntent, error) {
	in, err := s.svc.CreateIntent(ctx, &store.Intent{
		OrderID:        optstr(req.GetOrderId()),
		UserID:         req.GetUserId(),
		Provider:       req.GetProvider(),
		Amount:         req.GetAmount().GetAmount(),
		Currency:       req.GetAmount().GetCurrency(),
		IdempotencyKey: req.GetIdempotencyKey(),
	})
	if err != nil {
		return nil, fail(err)
	}
	return intentToProto(in), nil
}

// GetIntent returns one intent.
func (s *Server) GetIntent(ctx context.Context, req *paymentsv1.GetIntentRequest) (*paymentsv1.PaymentIntent, error) {
	in, err := s.st.GetIntent(ctx, nil, req.GetId())
	if err != nil {
		return nil, fail(err)
	}
	return intentToProto(in), nil
}

// SubmitReceipt stores a card receipt.
func (s *Server) SubmitReceipt(ctx context.Context, req *paymentsv1.SubmitReceiptRequest) (*paymentsv1.PaymentIntent, error) {
	in, err := s.svc.SubmitReceipt(ctx, req.GetUserId(), req.GetIntentId(), req.GetReceiptFile(), req.GetReferenceNumber())
	if err != nil {
		return nil, fail(err)
	}
	return intentToProto(in), nil
}

// SubmitTXID stores a crypto TXID.
func (s *Server) SubmitTXID(ctx context.Context, req *paymentsv1.SubmitTXIDRequest) (*paymentsv1.PaymentIntent, error) {
	in, err := s.svc.SubmitTXID(ctx, req.GetUserId(), req.GetIntentId(), req.GetNetwork(), req.GetTxid())
	if err != nil {
		return nil, fail(err)
	}
	return intentToProto(in), nil
}

// ReviewManualPayment applies an admin decision (approve/reject).
func (s *Server) ReviewManualPayment(ctx context.Context, req *paymentsv1.ReviewManualPaymentRequest) (*paymentsv1.PaymentIntent, error) {
	in, err := s.svc.Review(ctx, req.GetReviewerId(), req.GetIntentId(), req.GetDecision(), req.GetReason())
	if err != nil {
		return nil, fail(err)
	}
	return intentToProto(in), nil
}

// ListPendingReceipts lists receipts awaiting admin review.
func (s *Server) ListPendingReceipts(ctx context.Context, _ *paymentsv1.ListPendingReceiptsRequest) (*paymentsv1.ListPendingReceiptsResponse, error) {
	intents, err := s.st.ListPendingReceipts(ctx, 50)
	if err != nil {
		return nil, fail(err)
	}
	out := &paymentsv1.ListPendingReceiptsResponse{}
	for i := range intents {
		out.Intents = append(out.Intents, intentToProto(&intents[i]))
	}
	return out, nil
}

func optstr(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

func intentToProto(in *store.Intent) *paymentsv1.PaymentIntent {
	p := &paymentsv1.PaymentIntent{
		Id:       in.ID,
		UserId:   in.UserID,
		Provider: in.Provider,
		Amount:   &commonv1.Money{Amount: in.Amount, Currency: in.Currency},
		Status:   in.Status,
	}
	if in.OrderID != nil {
		p.OrderId = *in.OrderID
	}
	if in.ExpiresAt != nil {
		p.ExpiresAt = in.ExpiresAt.Unix()
	}
	p.CreatedAt = in.CreatedAt.Unix()
	return p
}

func fail(err error) error {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, store.ErrInvalidTransition):
		return status.Error(codes.FailedPrecondition, err.Error())
	default:
		return status.Error(codes.Internal, err.Error())
	}
}
