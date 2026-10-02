// Package paymentsclient adapts the payments gRPC client to the domain's
// PaymentsClient port.
package paymentsclient

import (
	"context"
	"fmt"

	commonv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/common/v1"
	paymentsv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/payments/v1"
)

// Client wraps the generated stub.
type Client struct {
	rpc paymentsv1.PaymentsServiceClient
}

// New adapts a connected stub.
func New(rpc paymentsv1.PaymentsServiceClient) *Client { return &Client{rpc: rpc} }

// CreateIntent implements domain.PaymentsClient.
func (c *Client) CreateIntent(ctx context.Context, orderID, userID, provider string, amount int64, currency, idemKey string) (string, string, error) {
	in, err := c.rpc.CreateIntent(ctx, &paymentsv1.CreateIntentRequest{
		OrderId:        orderID,
		UserId:         userID,
		Provider:       provider,
		Amount:         &commonv1.Money{Amount: amount, Currency: currency},
		IdempotencyKey: idemKey,
	})
	if err != nil {
		return "", "", fmt.Errorf("payments.CreateIntent: %w", err)
	}
	return in.GetId(), in.GetStatus(), nil
}
