// Package server exposes the payments domain over gRPC (PaymentsService).
package server

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"

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
// it also creates the payment there (Zarinpal) or fixes the Stars.
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
	case "zarinpal":
		out, err = s.svc.StartGateway(ctx, in, req.GetDescription(), s.publicURL+"/webhooks/"+req.GetProvider())
	case domain.USDTAuto:
		out, err = s.svc.StartChain(ctx, in)
	default:
		out, err = s.svc.CreateIntent(ctx, in)
	}
	if err != nil {
		return nil, fail(err)
	}
	return s.publicIntent(out), nil
}

// publicIntent is intentToProto for the customer: a Zarinpal payment must start
// from our own page (Zarinpal checks the Referer), so that page is the pay URL.
func (s *Server) publicIntent(in *store.Intent) *paymentsv1.PaymentIntent {
	p := intentToProto(in)
	if in.Provider == "zarinpal" && p.PayUrl != "" {
		p.PayUrl = s.publicURL + "/pay/" + in.ID
	}
	return p
}

// CheckIntent asks the gateway about an open intent now.
func (s *Server) CheckIntent(ctx context.Context, req *paymentsv1.CheckIntentRequest) (*paymentsv1.PaymentIntent, error) {
	in, err := s.svc.CheckIntent(ctx, req.GetUserId(), req.GetIntentId(), "check")
	if err != nil {
		return nil, fail(err)
	}
	return s.publicIntent(in), nil
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

// ListGatewayConfigs lists the automated gateways' settings, without secrets.
func (s *Server) ListGatewayConfigs(context.Context, *paymentsv1.ListGatewayConfigsRequest) (*paymentsv1.ListGatewayConfigsResponse, error) {
	out := &paymentsv1.ListGatewayConfigsResponse{}
	for _, c := range s.svc.GatewayConfigs() {
		out.Configs = append(out.Configs, gatewayConfigToProto(c))
	}
	return out, nil
}

// SetGatewayConfig stores and applies a gateway's new settings.
func (s *Server) SetGatewayConfig(ctx context.Context, req *paymentsv1.SetGatewayConfigRequest) (*paymentsv1.GatewayConfig, error) {
	c, err := s.svc.SetGatewayConfig(ctx, domain.GatewayUpdate{
		Provider: req.GetProvider(), Enabled: req.GetEnabled(), Sandbox: req.GetSandbox(),
		MerchantID: req.GetMerchantId(), Proxy: req.GetProxy(), ClearProxy: req.GetClearProxy(), ActorID: req.GetActorId(),
		Address: req.GetAddress(), APIKey: req.GetApiKey(), ClearAPIKey: req.GetClearApiKey(),
	})
	if err != nil {
		return nil, fail(err)
	}
	return gatewayConfigToProto(c), nil
}

// TestGateway checks a gateway's stored or candidate settings, read-only.
func (s *Server) TestGateway(ctx context.Context, req *paymentsv1.TestGatewayRequest) (*paymentsv1.TestGatewayResponse, error) {
	var cand *domain.GatewayCandidate
	if c := req.GetCandidate(); c != nil {
		cand = &domain.GatewayCandidate{Sandbox: c.GetSandbox(), MerchantID: c.GetMerchantId(), Proxy: c.GetProxy(), ClearProxy: c.GetClearProxy(),
			Address: c.GetAddress(), APIKey: c.GetApiKey(), ClearAPIKey: c.GetClearApiKey()}
	}
	r, err := s.svc.TestGateway(ctx, req.GetProvider(), cand)
	if err != nil {
		return nil, fail(err)
	}
	return &paymentsv1.TestGatewayResponse{
		Ok: r.OK, Detail: r.Detail, Unverified: int32(min(r.Unverified, math.MaxInt32)), //nolint:gosec // bounded
		LatencyMs: r.Latency.Milliseconds(),
	}, nil
}

// GatewayReport summarises one payment method over a period.
func (s *Server) GatewayReport(ctx context.Context, req *paymentsv1.GatewayReportRequest) (*paymentsv1.GatewayReportResponse, error) {
	var from, to time.Time
	if req.GetFrom() < 0 || req.GetTo() < 0 {
		return nil, status.Error(codes.InvalidArgument, "the report period must be unix seconds")
	}
	if req.GetFrom() > 0 {
		from = time.Unix(req.GetFrom(), 0)
	}
	if req.GetTo() > 0 {
		to = time.Unix(req.GetTo(), 0)
	}
	r, err := s.svc.GatewayReport(ctx, req.GetProvider(), from, to)
	if err != nil {
		return nil, fail(err)
	}
	out := &paymentsv1.GatewayReportResponse{
		Provider: r.Provider, From: r.From.Unix(), To: r.To.Unix(), OpenCount: r.OpenCount,
		LastError: gatewayEventToProto(r.LastError), LastPaid: gatewayEventToProto(r.LastPaid),
	}
	if r.OldestOpen != nil {
		out.OldestOpenAt = r.OldestOpen.Unix()
	}
	for _, t := range r.Totals {
		pt := &paymentsv1.GatewayStatusTotal{Status: t.Status, Count: t.Count}
		for _, m := range t.Amounts {
			pt.Amounts = append(pt.Amounts, &commonv1.Money{Amount: m.Amount, Currency: m.Currency})
		}
		for _, m := range t.GatewayAmounts {
			pt.GatewayAmounts = append(pt.GatewayAmounts, &commonv1.Money{Amount: m.Amount, Currency: m.Currency})
		}
		out.Totals = append(out.Totals, pt)
	}
	for _, f := range r.Failures {
		out.Failures = append(out.Failures, &paymentsv1.GatewayReasonCount{Reason: f.Reason, Count: f.Count})
	}
	out.CheckErrorCount = r.CheckErrorCount
	for i := range r.CheckErrors {
		out.CheckErrors = append(out.CheckErrors, gatewayEventToProto(&r.CheckErrors[i]))
	}
	if c := r.Chain; c != nil {
		out.UnmatchedCount = c.UnmatchedCount
		for i := range c.Unmatched {
			out.Unmatched = append(out.Unmatched, chainTransferToProto(&c.Unmatched[i]))
		}
		if cur := c.Cursor; cur != nil {
			if cur.ScannedAt != nil {
				out.LastScanAt = cur.ScannedAt.Unix()
			}
			if cur.OKAt != nil {
				out.LastScanOkAt = cur.OKAt.Unix()
			}
			if cur.LastError != nil {
				out.ScanError = *cur.LastError
			}
		}
	}
	return out, nil
}

// ListChainTransfers is the automatic USDT transfer log, newest first.
func (s *Server) ListChainTransfers(ctx context.Context, req *paymentsv1.ListChainTransfersRequest) (*paymentsv1.ListChainTransfersResponse, error) {
	page, size := int(req.GetPagination().GetPage()), int(req.GetPagination().GetPageSize())
	if size <= 0 || size > 200 {
		size = 25
	}
	if page <= 0 {
		page = 1
	}
	list, total, err := s.svc.ListChainTransfers(ctx, store.ChainTransferFilter{
		Outcome: req.GetOutcome(), TxID: req.GetTxid(), IntentID: req.GetIntentId(), Value: req.GetValue(),
	}, size, (page-1)*size)
	if err != nil {
		return nil, fail(err)
	}
	out := &paymentsv1.ListChainTransfersResponse{
		PageInfo: &commonv1.PageInfo{Page: int32(page), PageSize: int32(size), Total: total}, //nolint:gosec // bounded above
	}
	for i := range list {
		out.Transfers = append(out.Transfers, chainTransferToProto(&list[i]))
	}
	return out, nil
}

// ResolveChainTransfer marks an unmatched deposit handled by staff.
func (s *Server) ResolveChainTransfer(ctx context.Context, req *paymentsv1.ResolveChainTransferRequest) (*paymentsv1.ChainTransfer, error) {
	t, err := s.svc.ResolveChainTransfer(ctx, req.GetActorId(), req.GetTxid(), int(req.GetEventIndex()), req.GetNote())
	if err != nil {
		return nil, fail(err)
	}
	return chainTransferToProto(t), nil
}

func chainTransferToProto(t *store.ChainTransfer) *paymentsv1.ChainTransfer {
	p := &paymentsv1.ChainTransfer{
		Txid: t.TxID, EventIndex: int32(min(t.EventIndex, math.MaxInt32)), Network: t.Network, //nolint:gosec // bounded
		FromAddress: t.From, ToAddress: t.To, Amount: &commonv1.Money{Amount: t.Value, Currency: "USDT"},
		BlockTime: t.BlockTS.Unix(), Outcome: t.Outcome, RecordedAt: t.CreatedAt.Unix(),
	}
	for dst, src := range map[*string]*string{&p.Reason: t.Reason, &p.IntentId: t.IntentID, &p.ResolvedBy: t.ResolvedBy, &p.Note: t.Note} {
		if src != nil {
			*dst = *src
		}
	}
	if t.ResolvedAt != nil {
		p.ResolvedAt = t.ResolvedAt.Unix()
	}
	if t.AnnounceAt != nil && t.Outcome == store.TransferUnmatched {
		p.HeldUntil = t.AnnounceAt.Unix()
	}
	return p
}

func gatewayConfigToProto(c domain.GatewayConfig) *paymentsv1.GatewayConfig {
	p := &paymentsv1.GatewayConfig{
		Provider: c.Provider, Enabled: c.Enabled, Configured: c.Configured, Sandbox: c.Sandbox,
		MerchantHint: c.MerchantHint, ProxySet: c.ProxySet, KeyMissing: c.KeyMissing, Offered: c.Offered,
		Problem: c.Problem, FromEnv: c.FromEnv, UpdatedBy: c.UpdatedBy, Address: c.Address, ApiKeySet: c.APIKeySet,
	}
	if !c.UpdatedAt.IsZero() {
		p.UpdatedAt = c.UpdatedAt.Unix()
	}
	return p
}

func gatewayEventToProto(e *store.GatewayEvent) *paymentsv1.GatewayEvent {
	if e == nil {
		return nil
	}
	return &paymentsv1.GatewayEvent{At: e.At.Unix(), Kind: e.Kind, IntentId: e.IntentID, Detail: e.Detail}
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

// ListIntents is the payment history for staff, newest first.
func (s *Server) ListIntents(ctx context.Context, req *paymentsv1.ListIntentsRequest) (*paymentsv1.ListIntentsResponse, error) {
	page, size := int(req.GetPagination().GetPage()), int(req.GetPagination().GetPageSize())
	if size <= 0 || size > 200 {
		size = 25
	}
	if page <= 0 {
		page = 1
	}
	recs, total, err := s.st.ListIntents(ctx, store.IntentFilter{
		UserID: req.GetUserId(), OrderID: req.GetOrderId(), Status: req.GetStatus(), Provider: req.GetProvider(),
		IntentID: req.GetIntentId(),
	}, size, (page-1)*size)
	if err != nil {
		return nil, fail(err)
	}
	out := &paymentsv1.ListIntentsResponse{
		PageInfo: &commonv1.PageInfo{Page: int32(page), PageSize: int32(size), Total: total}, //nolint:gosec // bounded above
	}
	for i := range recs {
		rec := &paymentsv1.IntentRecord{Intent: intentToProto(&recs[i].Intent)}
		if r := recs[i].Receipt; r != nil {
			rec.Receipt = receiptToProto(r)
		}
		out.Records = append(out.Records, rec)
	}
	return out, nil
}

// receiptToProto converts a receipt and its review.
func receiptToProto(r *store.Receipt) *paymentsv1.ManualReceipt {
	p := &paymentsv1.ManualReceipt{SubmittedAt: r.SubmittedAt.Unix()}
	for dst, src := range map[*string]*string{
		&p.ReceiptFile: r.ReceiptFile, &p.ReferenceNumber: r.ReferenceNumber, &p.Network: r.Network, &p.Txid: r.TXID,
		&p.Decision: r.Decision, &p.ReviewReason: r.Reason, &p.ReviewedBy: r.ReviewedBy,
	} {
		if src != nil {
			*dst = *src
		}
	}
	if r.ReviewedAt != nil {
		p.ReviewedAt = r.ReviewedAt.Unix()
	}
	return p
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
	if in.PayAddress != nil {
		p.PayAddress = *in.PayAddress
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
	case errors.Is(err, domain.ErrTXIDUsed):
		return status.Error(codes.AlreadyExists, "this transaction already paid another payment")
	case errors.Is(err, store.ErrIdempotencyConflict):
		return status.Error(codes.AlreadyExists, "idempotency key reused for a different payment")
	case errors.Is(err, domain.ErrAlreadyPaid):
		return status.Error(codes.AlreadyExists, "this payment was already settled")
	case errors.Is(err, domain.ErrUnavailable):
		return status.Error(codes.Unavailable, "the payment gateway is unavailable, try again shortly")
	case errors.Is(err, domain.ErrKeyMissing):
		return status.Error(codes.FailedPrecondition, "the payments master key is not set: add it (re-run bobres install) to change gateway settings")
	case errors.Is(err, domain.ErrPrecondition):
		return status.Error(codes.FailedPrecondition, strings.TrimPrefix(err.Error(), domain.ErrPrecondition.Error()+": "))
	default:
		return status.Error(codes.Internal, "internal error")
	}
}
