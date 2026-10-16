// Package provisionerclient adapts the provisioner gRPC client to the core
// domain's Provisioner port.
package provisionerclient

import (
	"context"
	"encoding/base64"
	"fmt"

	provisionerv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/provisioner/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// Client wraps the generated stub.
type Client struct {
	rpc provisionerv1.ProvisionerServiceClient
}

// New adapts a connected stub.
func New(rpc provisionerv1.ProvisionerServiceClient) *Client { return &Client{rpc: rpc} }

// CreateClient implements domain.Provisioner.
func (c *Client) CreateClient(ctx context.Context, subscriptionID, email string, durationDays, trafficBytes int64, candidates []string, inbounds []store.PlanInbound) (domain.ProvisionedClient, error) {
	cl, err := c.rpc.CreateClient(ctx, &provisionerv1.CreateClientRequest{
		SubscriptionId: subscriptionID, Email: email, DurationDays: durationDays, TrafficBytes: trafficBytes,
		CandidateServerIds: candidates, Inbounds: inboundSelections(inbounds),
	})
	if err != nil {
		return domain.ProvisionedClient{}, fmt.Errorf("provisioner.CreateClient: %w", err)
	}
	return domain.ProvisionedClient{ServerID: cl.GetServerId(), XUISubID: cl.GetXuiSubId(), ExpiresAt: cl.GetExpiresAt()}, nil
}

// inboundSelections maps a plan's inbound choice to the request's.
func inboundSelections(in []store.PlanInbound) []*provisionerv1.InboundSelection {
	var out []*provisionerv1.InboundSelection
	for _, sel := range in {
		if len(sel.InboundIDs) == 0 {
			continue
		}
		out = append(out, &provisionerv1.InboundSelection{ServerId: sel.ServerID, InboundIds: append([]int32(nil), sel.InboundIDs...)})
	}
	return out
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

// SetEnabled implements domain.Provisioner.
func (c *Client) SetEnabled(ctx context.Context, subscriptionID string, enabled bool) error {
	if _, err := c.rpc.SetClientEnabled(ctx, &provisionerv1.SetClientEnabledRequest{
		SubscriptionId: subscriptionID, Enabled: enabled,
	}); err != nil {
		return fmt.Errorf("provisioner.SetClientEnabled: %w", err)
	}
	return nil
}

// ResetTraffic implements domain.Provisioner.
func (c *Client) ResetTraffic(ctx context.Context, subscriptionID string) error {
	if _, err := c.rpc.ResetTraffic(ctx, &provisionerv1.ResetTrafficRequest{SubscriptionId: subscriptionID}); err != nil {
		return fmt.Errorf("provisioner.ResetTraffic: %w", err)
	}
	return nil
}

// Delete implements domain.Provisioner.
func (c *Client) Delete(ctx context.Context, subscriptionID string) error {
	if _, err := c.rpc.DeleteClient(ctx, &provisionerv1.DeleteClientRequest{SubscriptionId: subscriptionID}); err != nil {
		return fmt.Errorf("provisioner.DeleteClient: %w", err)
	}
	return nil
}

// The server registry (domain.ServerAdmin). Tokens only ever go in.

func serverOf(p *provisionerv1.XUIServer) *domain.Server {
	return &domain.Server{
		ID: p.GetId(), Kind: p.GetKind(), Name: p.GetName(), BaseURL: p.GetBaseUrl(), SubBaseURL: p.GetSubBaseUrl(),
		Enabled: p.GetEnabled(), AllowPrivate: p.GetAllowPrivate(), Weight: p.GetWeight(), Managed: p.GetManaged(),
		HasToken: p.GetHasToken(), Clients: p.GetClients(), PanelVersion: p.GetPanelVersion(),
		VersionUntested: p.GetVersionUntested(), Healthy: p.GetHealthy(), HealthError: p.GetHealthError(),
		CheckedAt: p.GetCheckedAt(),
	}
}

// ListServers implements domain.ServerAdmin.
func (c *Client) ListServers(ctx context.Context) (*domain.ServerList, error) {
	resp, err := c.rpc.ListServers(ctx, &provisionerv1.ListServersRequest{})
	if err != nil {
		return nil, fmt.Errorf("provisioner.ListServers: %w", err)
	}
	out := &domain.ServerList{TestedMin: resp.GetTestedMinVersion(), TestedMax: resp.GetTestedMaxVersion(),
		Servers: make([]domain.Server, 0, len(resp.GetServers()))}
	for _, s := range resp.GetServers() {
		out.Servers = append(out.Servers, *serverOf(s))
	}
	return out, nil
}

// AddServer implements domain.ServerAdmin. The server is saved switched on
// or off as asked, in one step.
func (c *Client) AddServer(ctx context.Context, in domain.ServerInput) (*domain.Server, error) {
	s, err := c.rpc.AddServer(ctx, &provisionerv1.AddServerRequest{
		Name: in.Name, BaseUrl: in.BaseURL, ApiToken: in.Token, SubBaseUrl: in.SubBaseURL,
		AllowPrivate: in.AllowPrivate, Weight: in.Weight, SkipCheck: in.SkipCheck, Enabled: &in.Enabled, Kind: in.Kind,
	})
	if err != nil {
		return nil, fmt.Errorf("provisioner.AddServer: %w", err)
	}
	return serverOf(s), nil
}

// UpdateServer implements domain.ServerAdmin.
func (c *Client) UpdateServer(ctx context.Context, id string, in domain.ServerInput) (*domain.Server, error) {
	s, err := c.rpc.UpdateServer(ctx, &provisionerv1.UpdateServerRequest{
		Id: id, Name: in.Name, Enabled: in.Enabled, Weight: in.Weight, SubBaseUrl: in.SubBaseURL,
		AllowPrivate: in.AllowPrivate, BaseUrl: in.BaseURL, ApiToken: in.Token, SkipCheck: in.SkipCheck,
	})
	if err != nil {
		return nil, fmt.Errorf("provisioner.UpdateServer: %w", err)
	}
	return serverOf(s), nil
}

// DeleteServer implements domain.ServerAdmin.
func (c *Client) DeleteServer(ctx context.Context, id string) error {
	if _, err := c.rpc.DeleteServer(ctx, &provisionerv1.DeleteServerRequest{Id: id}); err != nil {
		return fmt.Errorf("provisioner.DeleteServer: %w", err)
	}
	return nil
}

// TestServer implements domain.ServerAdmin.
func (c *Client) TestServer(ctx context.Context, p domain.ServerProbe) (*domain.ServerTest, error) {
	r, err := c.rpc.TestServer(ctx, &provisionerv1.TestServerRequest{
		Id: p.ID, BaseUrl: p.BaseURL, ApiToken: p.Token, AllowPrivate: p.AllowPrivate, Kind: p.Kind,
	})
	if err != nil {
		return nil, fmt.Errorf("provisioner.TestServer: %w", err)
	}
	return &domain.ServerTest{OK: r.GetOk(), PanelVersion: r.GetPanelVersion(), Inbounds: r.GetInbounds(), Detail: r.GetDetail()}, nil
}

// ListInbounds implements domain.ServerAdmin.
func (c *Client) ListInbounds(ctx context.Context, serverID string) ([]domain.Inbound, error) {
	resp, err := c.rpc.ListInbounds(ctx, &provisionerv1.ListInboundsRequest{ServerId: serverID})
	if err != nil {
		return nil, fmt.Errorf("provisioner.ListInbounds: %w", err)
	}
	out := make([]domain.Inbound, 0, len(resp.GetInbounds()))
	for _, in := range resp.GetInbounds() {
		out = append(out, domain.Inbound{ID: in.GetId(), Tag: in.GetTag(), Protocol: in.GetProtocol(), Port: in.GetPort(),
			Enabled: in.GetEnabled(), Attach: in.GetAttach()})
	}
	return out, nil
}

// SetInboundAttach implements domain.ServerAdmin.
func (c *Client) SetInboundAttach(ctx context.Context, serverID string, inboundID int32, attach bool) error {
	if _, err := c.rpc.SetInboundAttach(ctx, &provisionerv1.SetInboundAttachRequest{
		ServerId: serverID, InboundId: inboundID, Attach: attach,
	}); err != nil {
		return fmt.Errorf("provisioner.SetInboundAttach: %w", err)
	}
	return nil
}
