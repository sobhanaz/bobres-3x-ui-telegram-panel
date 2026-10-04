// Package paymentsclient adapts the payments gRPC client to the domain's
// PaymentsClient port.
package paymentsclient

import (
	"context"
	"fmt"
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
	}
}

// StartGateway starts an automated payment (Zarinpal) or fixes the
// Stars of a Telegram Stars invoice.
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
