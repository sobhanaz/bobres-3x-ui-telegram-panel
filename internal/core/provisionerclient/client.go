// Package provisionerclient adapts the provisioner gRPC client to the core
// domain's Provisioner port.
package provisionerclient

import (
	"context"
	"encoding/base64"
	"fmt"

	provisionerv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/provisioner/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
)

// Client wraps the generated stub.
type Client struct {
	rpc provisionerv1.ProvisionerServiceClient
}

// New adapts a connected stub.
func New(rpc provisionerv1.ProvisionerServiceClient) *Client { return &Client{rpc: rpc} }

// CreateClient implements domain.Provisioner.
func (c *Client) CreateClient(ctx context.Context, subscriptionID, email string, durationDays, trafficBytes int64) (domain.ProvisionedClient, error) {
	cl, err := c.rpc.CreateClient(ctx, &provisionerv1.CreateClientRequest{
		SubscriptionId: subscriptionID, Email: email, DurationDays: durationDays, TrafficBytes: trafficBytes,
	})
	if err != nil {
		return domain.ProvisionedClient{}, fmt.Errorf("provisioner.CreateClient: %w", err)
	}
	return domain.ProvisionedClient{ServerID: cl.GetServerId(), XUISubID: cl.GetXuiSubId(), ExpiresAt: cl.GetExpiresAt()}, nil
}

// GetLinks implements domain.Provisioner.
func (c *Client) GetLinks(ctx context.Context, subscriptionID string) (domain.Links, error) {
	l, err := c.rpc.GetLinks(ctx, &provisionerv1.GetLinksRequest{SubscriptionId: subscriptionID})
	if err != nil {
		return domain.Links{}, fmt.Errorf("provisioner.GetLinks: %w", err)
	}
	png, err := base64.StdEncoding.DecodeString(l.GetQrPngBase64())
	if err != nil {
		return domain.Links{}, fmt.Errorf("provisioner.GetLinks: bad QR code: %w", err)
	}
	return domain.Links{SubscriptionLink: l.GetSubscriptionLink(), QRPNG: png, ConfigLinks: l.GetConfigLinks()}, nil
}

// Health implements domain.Provisioner.
func (c *Client) Health(ctx context.Context) (bool, string, error) {
	h, err := c.rpc.HealthCheck(ctx, &provisionerv1.HealthCheckRequest{})
	if err != nil {
		return false, "", fmt.Errorf("provisioner.HealthCheck: %w", err)
	}
	return h.GetHealthy(), h.GetDetail(), nil
}

// SetLimits implements domain.Provisioner.
func (c *Client) SetLimits(ctx context.Context, subscriptionID string, expiresAt, trafficBytes int64) error {
	if _, err := c.rpc.SetClientLimits(ctx, &provisionerv1.SetClientLimitsRequest{
		SubscriptionId: subscriptionID, ExpiresAt: expiresAt, TrafficTotalBytes: trafficBytes,
	}); err != nil {
		return fmt.Errorf("provisioner.SetClientLimits: %w", err)
	}
	return nil
}

// Usage implements domain.Provisioner.
func (c *Client) Usage(ctx context.Context, subscriptionID string) (domain.Usage, error) {
	u, err := c.rpc.GetUsage(ctx, &provisionerv1.GetUsageRequest{SubscriptionId: subscriptionID})
	if err != nil {
		return domain.Usage{}, fmt.Errorf("provisioner.GetUsage: %w", err)
	}
	return domain.Usage{UsedBytes: u.GetTrafficUsedBytes(), TotalBytes: u.GetTrafficTotalBytes(),
		ExpiresAt: u.GetExpiresAt(), Enabled: u.GetEnabled()}, nil
}
