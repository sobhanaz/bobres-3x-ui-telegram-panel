// Package provisionerclient adapts the provisioner gRPC client to the core
// domain's Provisioner port.
package provisionerclient

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	provisionerv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/provisioner/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Client wraps the generated stub.
type Client struct {
	rpc provisionerv1.ProvisionerServiceClient
}

// New adapts a connected stub.
func New(rpc provisionerv1.ProvisionerServiceClient) *Client { return &Client{rpc: rpc} }

// CreateClient implements domain.Provisioner.
func (c *Client) CreateClient(ctx context.Context, subscriptionID, email string, durationDays, trafficBytes int64, candidates []string, inbounds []store.PlanInbound, group string) (domain.ProvisionedClient, error) {
	cl, err := c.rpc.CreateClient(ctx, &provisionerv1.CreateClientRequest{
		SubscriptionId: subscriptionID, Email: email, DurationDays: durationDays, TrafficBytes: trafficBytes,
		CandidateServerIds: candidates, Inbounds: inboundSelections(inbounds), Group: group,
	})
	if nameRefused(err) {
		return domain.ProvisionedClient{}, fmt.Errorf("provisioner.CreateClient: %w: %w", domain.ErrClientNameTaken, err)
	}
	if err != nil {
		return domain.ProvisionedClient{}, fmt.Errorf("provisioner.CreateClient: %w", err)
	}
	return domain.ProvisionedClient{ServerID: cl.GetServerId(), XUISubID: cl.GetXuiSubId(), ExpiresAt: cl.GetExpiresAt()}, nil
}

// nameRefused reports the provisioner's refusal of a client name another client has (on the server, or on the panel):
// AlreadyExists saying "already uses this email". Its other AlreadyExists (the subscription was made under another
// name) is not a name taken by someone else.
func nameRefused(err error) bool {
	st, ok := status.FromError(err)
	return ok && st.Code() == codes.AlreadyExists && strings.Contains(st.Message(), "already uses this email")
}

// ReleaseClient implements domain.Provisioner.
func (c *Client) ReleaseClient(ctx context.Context, subscriptionID string) (domain.Release, error) {
	resp, err := c.rpc.ReleaseClient(ctx, &provisionerv1.ReleaseClientRequest{SubscriptionId: subscriptionID})
	if err != nil {
		return domain.Release{}, fmt.Errorf("provisioner.ReleaseClient: %w", err)
	}
	return domain.Release{Released: resp.GetReleased(), Leftover: resp.GetLeftover(), ServerID: resp.GetServerId()}, nil
}

// SetClientGroups implements domain.Provisioner.
func (c *Client) SetClientGroups(ctx context.Context, items []domain.ClientGroup) (int, error) {
	req := &provisionerv1.SetClientGroupsRequest{Clients: make([]*provisionerv1.ClientGroup, 0, len(items))}
	for _, it := range items {
		req.Clients = append(req.Clients, &provisionerv1.ClientGroup{SubscriptionId: it.SubscriptionID, Group: it.Group})
	}
	resp, err := c.rpc.SetClientGroups(ctx, req)
	if err != nil {
		return 0, fmt.Errorf("provisioner.SetClientGroups: %w", err)
	}
	return int(resp.GetUpdated()), nil
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
		ExpiresAt: u.GetExpiresAt(), Enabled: u.GetEnabled(), Missing: u.GetClientMissing()}, nil
}

// RecreateClient implements domain.Provisioner.
func (c *Client) RecreateClient(ctx context.Context, subscriptionID string, expiresAt, trafficBytes int64) error {
	if _, err := c.rpc.RecreateClient(ctx, &provisionerv1.RecreateClientRequest{
		SubscriptionId: subscriptionID, ExpiresAt: expiresAt, TrafficTotalBytes: trafficBytes,
	}); err != nil {
		return fmt.Errorf("provisioner.RecreateClient: %w", err)
	}
	return nil
}

// CheckClientName implements domain.NameChecker.
func (c *Client) CheckClientName(ctx context.Context, name string) (domain.NameCheck, error) {
	res, err := c.rpc.CheckClientName(ctx, &provisionerv1.CheckClientNameRequest{Name: name})
	if err != nil {
		return domain.NameCheck{}, fmt.Errorf("provisioner.CheckClientName: %w", err)
	}
	return domain.NameCheck{Available: res.GetAvailable(), Reason: res.GetReason()}, nil
}

// RenameClient implements domain.Provisioner.
func (c *Client) RenameClient(ctx context.Context, subscriptionID, name string) error {
	_, err := c.rpc.RenameClient(ctx, &provisionerv1.RenameClientRequest{SubscriptionId: subscriptionID, NewName: name})
	switch status.Code(err) {
	case codes.OK:
		return nil
	case codes.AlreadyExists:
		return fmt.Errorf("provisioner.RenameClient: %w: %w", domain.ErrClientNameTaken, err)
	case codes.FailedPrecondition:
		return fmt.Errorf("provisioner.RenameClient: %w: %w", domain.ErrNotRenamable, err)
	}
	return fmt.Errorf("provisioner.RenameClient: %w", err)
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
		Enabled: p.GetEnabled(), AllowPrivate: p.GetAllowPrivate(), Weight: p.GetWeight(), MaxClients: p.GetMaxClients(), Managed: p.GetManaged(),
		HasToken: p.GetHasToken(), Clients: p.GetClients(), PanelVersion: p.GetPanelVersion(),
		VersionUntested: p.GetVersionUntested(), Healthy: p.GetHealthy(), HealthError: p.GetHealthError(),
		CheckedAt: p.GetCheckedAt(), RenamesClients: p.GetRenamesClients(),
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
	var limit int32
	if in.MaxClients != nil {
		limit = *in.MaxClients
	}
	s, err := c.rpc.AddServer(ctx, &provisionerv1.AddServerRequest{
		Name: in.Name, BaseUrl: in.BaseURL, ApiToken: in.Token, SubBaseUrl: in.SubBaseURL,
		AllowPrivate: in.AllowPrivate, Weight: in.Weight, SkipCheck: in.SkipCheck, Enabled: &in.Enabled, Kind: in.Kind, MaxClients: limit,
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
		AllowPrivate: in.AllowPrivate, BaseUrl: in.BaseURL, ApiToken: in.Token, SkipCheck: in.SkipCheck, MaxClients: in.MaxClients,
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
