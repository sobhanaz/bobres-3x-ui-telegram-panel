// Package paymentsclient adapts the payments gRPC client to the domain's
// PaymentsClient port.
package paymentsclient

import (
	"context"
	"fmt"
	"math"
	"time"

	commonv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/common/v1"
	paymentsv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/payments/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
)

// Client wraps the generated stub.
type Client struct {
	rpc paymentsv1.PaymentsServiceClient
}

// New adapts a connected stub.
func New(rpc paymentsv1.PaymentsServiceClient) *Client { return &Client{rpc: rpc} }

// Errors keep their gRPC status (wrapped with %w), so core can pass
// meaningful codes such as AlreadyExists on to the bot.

// CreateIntent implements domain.PaymentsClient.
func (c *Client) CreateIntent(ctx context.Context, orderID, userID, provider string, amount int64, currency, idemKey string) (string, string, error) {
	in, err := c.rpc.CreateIntent(ctx, &paymentsv1.CreateIntentRequest{
		OrderId: orderID, UserId: userID, Provider: provider,
		Amount: &commonv1.Money{Amount: amount, Currency: currency}, IdempotencyKey: idemKey,
	})
	if err != nil {
		return "", "", fmt.Errorf("payments.CreateIntent: %w", err)
	}
	return in.GetId(), in.GetStatus(), nil
}

// SubmitReceipt implements domain.PaymentsClient.
func (c *Client) SubmitReceipt(ctx context.Context, userID, intentID, fileID, reference string) (string, error) {
	in, err := c.rpc.SubmitReceipt(ctx, &paymentsv1.SubmitReceiptRequest{
		UserId: userID, IntentId: intentID, ReceiptFile: fileID, ReferenceNumber: reference,
	})
	if err != nil {
		return "", fmt.Errorf("payments.SubmitReceipt: %w", err)
	}
	return in.GetStatus(), nil
}

// SubmitTXID implements domain.PaymentsClient.
func (c *Client) SubmitTXID(ctx context.Context, userID, intentID, network, txid string) (string, error) {
	in, err := c.rpc.SubmitTXID(ctx, &paymentsv1.SubmitTXIDRequest{
		UserId: userID, IntentId: intentID, Network: network, Txid: txid,
	})
	if err != nil {
		return "", fmt.Errorf("payments.SubmitTXID: %w", err)
	}
	return in.GetStatus(), nil
}

// ListPending implements domain.PaymentsClient.
func (c *Client) ListPending(ctx context.Context, limit int) ([]domain.PendingPayment, error) {
	resp, err := c.rpc.ListPendingReceipts(ctx, &paymentsv1.ListPendingReceiptsRequest{
		Pagination: &commonv1.Pagination{PageSize: int32(min(max(limit, 1), 200))}, //nolint:gosec // bounded
	})
	if err != nil {
		return nil, fmt.Errorf("payments.ListPendingReceipts: %w", err)
	}
	out := make([]domain.PendingPayment, 0, len(resp.GetReceipts()))
	for _, r := range resp.GetReceipts() {
		in, rc := r.GetIntent(), r.GetReceipt()
		out = append(out, domain.PendingPayment{
			IntentID: in.GetId(), OrderID: in.GetOrderId(), UserID: in.GetUserId(), Provider: in.GetProvider(),
			Amount: in.GetAmount().GetAmount(), Currency: in.GetAmount().GetCurrency(),
			ReceiptFile: rc.GetReceiptFile(), ReferenceNumber: rc.GetReferenceNumber(),
			Network: rc.GetNetwork(), TXID: rc.GetTxid(),
			SubmittedAt: time.Unix(rc.GetSubmittedAt(), 0), PossibleDuplicate: r.GetPossibleDuplicate(),
		})
	}
	return out, nil
}

// Review implements domain.PaymentsClient.
func (c *Client) Review(ctx context.Context, reviewerID, intentID, decision, reason string) (string, error) {
	in, err := c.rpc.ReviewManualPayment(ctx, &paymentsv1.ReviewManualPaymentRequest{
		ReviewerId: reviewerID, IntentId: intentID, Decision: decision, Reason: reason,
	})
	if err != nil {
		return "", fmt.Errorf("payments.ReviewManualPayment: %w", err)
	}
	return in.GetStatus(), nil
}

func gatewayIntent(in *paymentsv1.PaymentIntent) *domain.GatewayIntent {
	return &domain.GatewayIntent{
		ID: in.GetId(), Status: in.GetStatus(), OrderID: in.GetOrderId(), Provider: in.GetProvider(),
		Amount: in.GetAmount().GetAmount(), Currency: in.GetAmount().GetCurrency(),
		GatewayAmount: in.GetGatewayAmount().GetAmount(), GatewayCurrency: in.GetGatewayAmount().GetCurrency(),
		PayURL: in.GetPayUrl(), FailureReason: in.GetFailureReason(),
		PayAddress: in.GetPayAddress(), ExpiresAt: in.GetExpiresAt(), ExternalID: in.GetExternalId(),
	}
}

// StartGateway starts an automated payment (Zarinpal, automatic USDT) or
// fixes the Stars of a Telegram Stars invoice.
func (c *Client) StartGateway(ctx context.Context, p domain.GatewayStart) (*domain.GatewayIntent, error) {
	in, err := c.rpc.CreateIntent(ctx, &paymentsv1.CreateIntentRequest{
		OrderId: p.OrderID, UserId: p.UserID, Provider: p.Provider,
		Amount:         &commonv1.Money{Amount: p.Amount, Currency: p.Currency},
		IdempotencyKey: p.IdempotencyKey,
		GatewayAmount:  &commonv1.Money{Amount: p.GatewayAmount, Currency: p.GatewayCurrency},
		Description:    p.Description,
	})
	if err != nil {
		return nil, fmt.Errorf("payments.CreateIntent: %w", err)
	}
	return gatewayIntent(in), nil
}

// CheckIntent asks payments to check an automated payment now.
func (c *Client) CheckIntent(ctx context.Context, userID, intentID string) (*domain.GatewayIntent, error) {
	in, err := c.rpc.CheckIntent(ctx, &paymentsv1.CheckIntentRequest{UserId: userID, IntentId: intentID})
	if err != nil {
		return nil, fmt.Errorf("payments.CheckIntent: %w", err)
	}
	return gatewayIntent(in), nil
}

// PrecheckStars validates a Telegram Stars pre-checkout.
func (c *Client) PrecheckStars(ctx context.Context, userID, intentID string, total int64, currency string) (*domain.GatewayIntent, error) {
	in, err := c.rpc.PrecheckStarsPayment(ctx, &paymentsv1.PrecheckStarsPaymentRequest{
		UserId: userID, IntentId: intentID, TotalAmount: total, Currency: currency,
	})
	if err != nil {
		return nil, fmt.Errorf("payments.PrecheckStarsPayment: %w", err)
	}
	return gatewayIntent(in), nil
}

// ConfirmStars settles a Telegram Stars payment.
func (c *Client) ConfirmStars(ctx context.Context, userID, intentID string, total int64, currency, chargeID string) (*domain.GatewayIntent, error) {
	in, err := c.rpc.ConfirmStarsPayment(ctx, &paymentsv1.ConfirmStarsPaymentRequest{
		UserId: userID, IntentId: intentID, TotalAmount: total, Currency: currency, TelegramPaymentChargeId: chargeID,
	})
	if err != nil {
		return nil, fmt.Errorf("payments.ConfirmStarsPayment: %w", err)
	}
	return gatewayIntent(in), nil
}

// ListGateways lists the automated providers enabled in payments.
func (c *Client) ListGateways(ctx context.Context) ([]string, error) {
	resp, err := c.rpc.ListGateways(ctx, &paymentsv1.ListGatewaysRequest{})
	if err != nil {
		return nil, fmt.Errorf("payments.ListGateways: %w", err)
	}
	return resp.GetProviders(), nil
}

// ListPayments implements domain.PaymentsClient.
func (c *Client) ListPayments(ctx context.Context, f domain.PaymentFilter, page, size int) ([]domain.PaymentRecord, int64, error) {
	resp, err := c.rpc.ListIntents(ctx, &paymentsv1.ListIntentsRequest{
		UserId: f.UserID, OrderId: f.OrderID, Status: f.Status, Provider: f.Provider, IntentId: f.IntentID,
		Pagination: &commonv1.Pagination{Page: int32(page), PageSize: int32(size)}, //nolint:gosec // small page numbers
	})
	if err != nil {
		return nil, 0, fmt.Errorf("payments.ListIntents: %w", err)
	}
	out := make([]domain.PaymentRecord, 0, len(resp.GetRecords()))
	for _, r := range resp.GetRecords() {
		in := r.GetIntent()
		rec := domain.PaymentRecord{
			IntentID: in.GetId(), OrderID: in.GetOrderId(), UserID: in.GetUserId(), Provider: in.GetProvider(),
			Status: in.GetStatus(), Amount: in.GetAmount().GetAmount(), Currency: in.GetAmount().GetCurrency(),
			GatewayAmount: in.GetGatewayAmount().GetAmount(), GatewayCurrency: in.GetGatewayAmount().GetCurrency(),
			ProviderRef: in.GetProviderRef(), FailureReason: in.GetFailureReason(), CreatedAt: time.Unix(in.GetCreatedAt(), 0),
		}
		if p := r.GetReceipt(); p != nil {
			proof := &domain.PaymentProof{
				ReceiptFile: p.GetReceiptFile(), ReferenceNumber: p.GetReferenceNumber(), Network: p.GetNetwork(),
				TXID: p.GetTxid(), SubmittedAt: time.Unix(p.GetSubmittedAt(), 0), Decision: p.GetDecision(),
				Reason: p.GetReviewReason(), ReviewedBy: p.GetReviewedBy(),
			}
			if p.GetReviewedAt() > 0 {
				t := time.Unix(p.GetReviewedAt(), 0)
				proof.ReviewedAt = &t
			}
			rec.Proof = proof
		}
		out = append(out, rec)
	}
	return out, resp.GetPageInfo().GetTotal(), nil
}

// Gateway settings (domain.GatewayAdmin). Secrets only ever go in.

func gatewayConfigOf(p *paymentsv1.GatewayConfig) *domain.GatewayConfig {
	return &domain.GatewayConfig{
		Provider: p.GetProvider(), Enabled: p.GetEnabled(), Configured: p.GetConfigured(), Offered: p.GetOffered(),
		Sandbox: p.GetSandbox(), MerchantHint: p.GetMerchantHint(), ProxySet: p.GetProxySet(),
		KeyMissing: p.GetKeyMissing(), FromEnv: p.GetFromEnv(), Problem: p.GetProblem(),
		UpdatedAt: p.GetUpdatedAt(), UpdatedBy: p.GetUpdatedBy(), Address: p.GetAddress(), APIKeySet: p.GetApiKeySet(),
	}
}

// GatewayConfigs implements domain.GatewayAdmin.
func (c *Client) GatewayConfigs(ctx context.Context) ([]domain.GatewayConfig, error) {
	resp, err := c.rpc.ListGatewayConfigs(ctx, &paymentsv1.ListGatewayConfigsRequest{})
	if err != nil {
		return nil, fmt.Errorf("payments.ListGatewayConfigs: %w", err)
	}
	out := make([]domain.GatewayConfig, 0, len(resp.GetConfigs()))
	for _, g := range resp.GetConfigs() {
		out = append(out, *gatewayConfigOf(g))
	}
	return out, nil
}

// SetGatewayConfig implements domain.GatewayAdmin.
func (c *Client) SetGatewayConfig(ctx context.Context, u domain.GatewayUpdate, actorID string) (*domain.GatewayConfig, error) {
	g, err := c.rpc.SetGatewayConfig(ctx, &paymentsv1.SetGatewayConfigRequest{
		Provider: u.Provider, Enabled: u.Enabled, Sandbox: u.Sandbox, MerchantId: u.MerchantID, Proxy: u.Proxy,
		ClearProxy: u.ClearProxy, ActorId: actorID, Address: u.Address, ApiKey: u.APIKey, ClearApiKey: u.ClearAPIKey,
	})
	if err != nil {
		return nil, fmt.Errorf("payments.SetGatewayConfig: %w", err)
	}
	return gatewayConfigOf(g), nil
}

// TestGateway implements domain.GatewayAdmin.
func (c *Client) TestGateway(ctx context.Context, provider string, cand *domain.GatewayCandidate) (*domain.GatewayTest, error) {
	req := &paymentsv1.TestGatewayRequest{Provider: provider}
	if cand != nil {
		req.Candidate = &paymentsv1.GatewayCandidate{Sandbox: cand.Sandbox, MerchantId: cand.MerchantID, Proxy: cand.Proxy,
			ClearProxy: cand.ClearProxy, Address: cand.Address, ApiKey: cand.APIKey, ClearApiKey: cand.ClearAPIKey}
	}
	r, err := c.rpc.TestGateway(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("payments.TestGateway: %w", err)
	}
	return &domain.GatewayTest{OK: r.GetOk(), Detail: r.GetDetail(), Unverified: r.GetUnverified(), LatencyMS: r.GetLatencyMs()}, nil
}

func gatewayEventOf(e *paymentsv1.GatewayEvent) *domain.GatewayEvent {
	if e == nil {
		return nil
	}
	return &domain.GatewayEvent{At: e.GetAt(), Kind: e.GetKind(), IntentID: e.GetIntentId(), Detail: e.GetDetail()}
}

// GatewayReport implements domain.GatewayAdmin.
func (c *Client) GatewayReport(ctx context.Context, provider string, from, to int64) (*domain.GatewayReport, error) {
	r, err := c.rpc.GatewayReport(ctx, &paymentsv1.GatewayReportRequest{Provider: provider, From: from, To: to})
	if err != nil {
		return nil, fmt.Errorf("payments.GatewayReport: %w", err)
	}
	out := &domain.GatewayReport{Provider: r.GetProvider(), From: r.GetFrom(), To: r.GetTo(), OpenCount: r.GetOpenCount(),
		OldestOpenAt: r.GetOldestOpenAt(), LastError: gatewayEventOf(r.GetLastError()), LastPaid: gatewayEventOf(r.GetLastPaid()),
		UnmatchedCount: r.GetUnmatchedCount(), LastScanAt: r.GetLastScanAt(), LastScanOKAt: r.GetLastScanOkAt(),
		ScanError: r.GetScanError(), CheckErrorCount: r.GetCheckErrorCount()}
	for _, e := range r.GetCheckErrors() {
		if ev := gatewayEventOf(e); ev != nil {
			out.CheckErrors = append(out.CheckErrors, *ev)
		}
	}
	for _, t := range r.GetUnmatched() {
		out.Unmatched = append(out.Unmatched, chainTransferOf(t))
	}
	for _, t := range r.GetTotals() {
		st := domain.GatewayStatusTotal{Status: t.GetStatus(), Count: t.GetCount(), Amounts: map[string]int64{}}
		for _, m := range t.GetAmounts() {
			st.Amounts[m.GetCurrency()] += m.GetAmount()
		}
		out.Totals = append(out.Totals, st)
	}
	return out, nil
}

func chainTransferOf(t *paymentsv1.ChainTransfer) domain.ChainTransfer {
	return domain.ChainTransfer{
		TxID: t.GetTxid(), EventIndex: int(t.GetEventIndex()), Network: t.GetNetwork(), From: t.GetFromAddress(),
		To: t.GetToAddress(), Amount: t.GetAmount().GetAmount(), Currency: t.GetAmount().GetCurrency(),
		BlockTime: t.GetBlockTime(), Outcome: t.GetOutcome(), Reason: t.GetReason(), IntentID: t.GetIntentId(),
		RecordedAt: t.GetRecordedAt(), ResolvedBy: t.GetResolvedBy(), ResolvedAt: t.GetResolvedAt(), Note: t.GetNote(),
	}
}

// ChainTransfers implements domain.GatewayAdmin.
func (c *Client) ChainTransfers(ctx context.Context, f domain.ChainTransferFilter, page, size int) ([]domain.ChainTransfer, int64, error) {
	resp, err := c.rpc.ListChainTransfers(ctx, &paymentsv1.ListChainTransfersRequest{
		Outcome: f.Outcome, Txid: f.TxID, IntentId: f.IntentID,
		Pagination: &commonv1.Pagination{Page: int32(min(max(page, 1), math.MaxInt32)), PageSize: int32(min(max(size, 1), 200))}, //nolint:gosec // bounded
	})
	if err != nil {
		return nil, 0, fmt.Errorf("payments.ListChainTransfers: %w", err)
	}
	out := make([]domain.ChainTransfer, 0, len(resp.GetTransfers()))
	for _, t := range resp.GetTransfers() {
		out = append(out, chainTransferOf(t))
	}
	return out, resp.GetPageInfo().GetTotal(), nil
}

// ResolveChainTransfer implements domain.GatewayAdmin.
func (c *Client) ResolveChainTransfer(ctx context.Context, txid string, index int, actorID, note string) (*domain.ChainTransfer, error) {
	t, err := c.rpc.ResolveChainTransfer(ctx, &paymentsv1.ResolveChainTransferRequest{
		Txid: txid, EventIndex: int32(min(max(index, 0), math.MaxInt32)), ActorId: actorID, Note: note, //nolint:gosec // bounded
	})
	if err != nil {
		return nil, fmt.Errorf("payments.ResolveChainTransfer: %w", err)
	}
	out := chainTransferOf(t)
	return &out, nil
}
