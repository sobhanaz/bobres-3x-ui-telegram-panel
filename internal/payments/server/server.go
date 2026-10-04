// Package server exposes the payments domain over gRPC (PaymentsService).
package server

import (
	"context"
	"errors"
	"strings"

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
	svc       *domain.Service
	st        *store.Store
	publicURL string // https://<domain>, for gateway callbacks
}

// New builds the server.
func New(svc *domain.Service, st *store.Store) *Server { return &Server{svc: svc, st: st} }

// SetPublicURL sets the install's public base URL (https://<domain>); gateways
// that return the customer's browser (Zarinpal) are told to come back there.
func (s *Server) SetPublicURL(u string) { s.publicURL = strings.TrimRight(u, "/") }

// Register attaches the service.
func (s *Server) Register(g *grpc.Server) { paymentsv1.RegisterPaymentsServiceServer(g, s) }

// CreateIntent validates and stores a pending intent; for an automated gateway
// it also creates the payment there (Zarinpal, Crypto Pay) or fixes the Stars.
func (s *Server) CreateIntent(ctx context.Context, req *paymentsv1.CreateIntentRequest) (*paymentsv1.PaymentIntent, error) {
	in := &store.Intent{
		OrderID:        optstr(req.GetOrderId()),
		UserID:         req.GetUserId(),
		Provider:       req.GetProvider(),
		Amount:         req.GetAmount().GetAmount(),
		Currency:       req.GetAmount().GetCurrency(),
		IdempotencyKey: req.GetIdempotencyKey(),
	}
	if ga := req.GetGatewayAmount(); ga != nil {
		amount, cur := ga.GetAmount(), ga.GetCurrency()
		in.GatewayAmount, in.GatewayCurrency = &amount, &cur
	}
	var (
		out *store.Intent
		err error
	)
	switch req.GetProvider() {
	case domain.Stars:
		out, err = s.svc.StartStars(ctx, in)
	case "zarinpal", "cryptopay":
		out, err = s.svc.StartGateway(ctx, in, req.GetDescription(), s.publicURL+"/webhooks/"+req.GetProvider())
	default:
		out, err = s.svc.CreateIntent(ctx, in)
	}
	if err != nil {
		return nil, fail(err)
	}
	return intentToProto(out), nil
}

// CheckIntent asks the gateway about an open intent now.
func (s *Server) CheckIntent(ctx context.Context, req *paymentsv1.CheckIntentRequest) (*paymentsv1.PaymentIntent, error) {
	in, err := s.svc.CheckIntent(ctx, req.GetUserId(), req.GetIntentId(), "check")
	if err != nil {
		return nil, fail(err)
	}
	return intentToProto(in), nil
}

// PrecheckStarsPayment validates a Stars pre-checkout (nothing is charged yet).
func (s *Server) PrecheckStarsPayment(ctx context.Context, req *paymentsv1.PrecheckStarsPaymentRequest) (*paymentsv1.PaymentIntent, error) {
	in, err := s.svc.PrecheckStars(ctx, req.GetUserId(), req.GetIntentId(), req.GetTotalAmount(), req.GetCurrency())
	if err != nil {
		return nil, fail(err)
	}
	return intentToProto(in), nil
}

// ConfirmStarsPayment settles a Stars payment Telegram reported to the bot.
func (s *Server) ConfirmStarsPayment(ctx context.Context, req *paymentsv1.ConfirmStarsPaymentRequest) (*paymentsv1.PaymentIntent, error) {
	in, err := s.svc.ConfirmStars(ctx, req.GetUserId(), req.GetIntentId(), req.GetTotalAmount(), req.GetCurrency(), req.GetTelegramPaymentChargeId())
	if err != nil {
		return nil, fail(err)
	}
	return intentToProto(in), nil
}

// ListGateways lists the automated providers usable on this install.
func (s *Server) ListGateways(context.Context, *paymentsv1.ListGatewaysRequest) (*paymentsv1.ListGatewaysResponse, error) {
	return &paymentsv1.ListGatewaysResponse{Providers: s.svc.Gateways()}, nil
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

// ListPendingReceipts lists receipts awaiting admin review, with their proof.
func (s *Server) ListPendingReceipts(ctx context.Context, req *paymentsv1.ListPendingReceiptsRequest) (*paymentsv1.ListPendingReceiptsResponse, error) {
	size := int(req.GetPagination().GetPageSize())
	pending, err := s.st.ListPendingReceipts(ctx, size)
	if err != nil {
		return nil, fail(err)
	}
	out := &paymentsv1.ListPendingReceiptsResponse{}
	for i := range pending {
		p := &pending[i]
		ip := intentToProto(&p.Intent)
		out.Intents = append(out.Intents, ip)
		r := &paymentsv1.ManualReceipt{SubmittedAt: p.Receipt.SubmittedAt.Unix()}
		if p.Receipt.ReceiptFile != nil {
			r.ReceiptFile = *p.Receipt.ReceiptFile
		}
		if p.Receipt.ReferenceNumber != nil {
			r.ReferenceNumber = *p.Receipt.ReferenceNumber
		}
		if p.Receipt.Network != nil {
			r.Network = *p.Receipt.Network
		}
		if p.Receipt.TXID != nil {
			r.Txid = *p.Receipt.TXID
		}
		out.Receipts = append(out.Receipts, &paymentsv1.PendingReceipt{Intent: ip, Receipt: r, PossibleDuplicate: p.PossibleDuplicate})
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
	if in.GatewayAmount != nil && in.GatewayCurrency != nil {
		p.GatewayAmount = &commonv1.Money{Amount: *in.GatewayAmount, Currency: *in.GatewayCurrency}
	}
	if in.PayURL != nil {
		p.PayUrl = *in.PayURL
	}
	if in.ExternalID != nil {
		p.ExternalId = *in.ExternalID
	}
	if in.ProviderRef != nil {
		p.ProviderRef = *in.ProviderRef
	}
	if in.FailureReason != nil {
		p.FailureReason = *in.FailureReason
	}
	return p
}

// fail maps domain errors to gRPC codes; anything unexpected is Internal with
// a generic message, so database details never leave the service.
func fail(err error) error {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return status.Error(codes.NotFound, "payment not found")
	case errors.Is(err, domain.ErrInvalid):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, domain.ErrForbidden):
		return status.Error(codes.PermissionDenied, "this payment belongs to another user")
	case errors.Is(err, store.ErrInvalidTransition):
		return status.Error(codes.FailedPrecondition, "this payment cannot change state now")
	case errors.Is(err, store.ErrDuplicateProof):
		return status.Error(codes.AlreadyExists, "this transaction was already submitted")
	case errors.Is(err, store.ErrIdempotencyConflict):
		return status.Error(codes.AlreadyExists, "idempotency key reused for a different payment")
	case errors.Is(err, domain.ErrAlreadyPaid):
		return status.Error(codes.AlreadyExists, "this payment was already settled")
	case errors.Is(err, domain.ErrUnavailable):
		return status.Error(codes.Unavailable, "the payment gateway is unavailable, try again shortly")
	default:
		return status.Error(codes.Internal, "internal error")
	}
}
