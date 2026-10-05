// Package server exposes the provisioner over gRPC. It is the ONLY process
// that holds 3x-ui API tokens (encrypted at rest, decrypted in memory only).
package server

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/skip2/go-qrcode"
	provisionerv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/provisioner/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/provisioner/adapter"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/provisioner/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/xui"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

// Server implements provisionerv1.ProvisionerServiceServer.
type Server struct {
	provisionerv1.UnimplementedProvisionerServiceServer
	st      *store.Store
	xuiOpts []xui.Option
	log     *slog.Logger

	mu       sync.Mutex
	adapters map[string]cachedAdapter // by server id
}

// cachedAdapter is reused for every call to one panel (keep-alive connections,
// shared per-email locks) until the server row changes.
type cachedAdapter struct {
	version time.Time
	a       adapter.PanelAdapter
}

// New builds the server. xuiOpts relax panel-client hardening in tests only.
func New(st *store.Store, xuiOpts ...xui.Option) *Server {
	return &Server{st: st, xuiOpts: xuiOpts, log: slog.New(slog.DiscardHandler), adapters: map[string]cachedAdapter{}}
}

// SetLogger enables logging of panel failures (xui errors carry neither the
// token nor the URL path, so they are safe to log).
func (s *Server) SetLogger(l *slog.Logger) {
	if l != nil {
		s.log = l
	}
}

// panelError logs a failed panel call and maps it to a gRPC status. Refusals by
// the address policy (plain http to a public address, a private address without
// allow-private) are configuration errors the operator must see verbatim;
// everything else is reported as unavailable with the operation's name.
func (s *Server) panelError(op string, err error) error {
	s.log.Warn("3x-ui call failed", "op", op, "err", err)
	if policyRefusal(err) {
		return status.Error(codes.FailedPrecondition, err.Error())
	}
	return status.Error(codes.Unavailable, op+" failed")
}

func policyRefusal(err error) bool {
	return errors.Is(err, xui.ErrPublicPlaintext) || errors.Is(err, xui.ErrBlockedAddress) || errors.Is(err, xui.ErrInsecureURL)
}

// Register attaches the service.
func (s *Server) Register(g *grpc.Server) { provisionerv1.RegisterProvisionerServiceServer(g, s) }

func (s *Server) clientOptions(allowPrivate bool) []xui.Option {
	opts := append([]xui.Option(nil), s.xuiOpts...)
	if allowPrivate {
		opts = append(opts, xui.AllowPrivateAddresses())
	}
	return opts
}

func (s *Server) adapterFor(sv *store.Server) (adapter.PanelAdapter, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.adapters[sv.ID]; ok && c.version.Equal(sv.UpdatedAt) {
		return c.a, nil
	}
	a, err := adapter.NewXUIv3(sv.BaseURL, sv.Token, s.clientOptions(sv.AllowPrivate)...)
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, "panel configuration is invalid")
	}
	s.adapters[sv.ID] = cachedAdapter{version: sv.UpdatedAt, a: a}
	return a, nil
}

// server resolves a panel by id, or the first enabled panel when id is empty.
func (s *Server) server(ctx context.Context, id string) (*store.Server, adapter.PanelAdapter, error) {
	var (
		sv  *store.Server
		err error
	)
	if id != "" {
		sv, err = s.st.GetServer(ctx, id)
	} else {
		sv, err = s.st.FirstServer(ctx)
	}
	switch {
	case errors.Is(err, store.ErrNotFound) && id != "":
		return nil, nil, status.Error(codes.NotFound, "3x-ui server not found")
	case errors.Is(err, store.ErrNotFound):
		return nil, nil, status.Error(codes.FailedPrecondition, "no enabled 3x-ui server")
	case err != nil:
		return nil, nil, status.Error(codes.Internal, "server lookup failed")
	}
	a, err := s.adapterFor(sv)
	if err != nil {
		return nil, nil, err
	}
	return sv, a, nil
}

// subscription resolves a provisioned subscription and its panel. cm is nil
// (with no error) when the subscription was never provisioned.
func (s *Server) subscription(ctx context.Context, subscriptionID string) (*store.ClientMap, *store.Server, adapter.PanelAdapter, error) {
	if subscriptionID == "" {
		return nil, nil, nil, status.Error(codes.InvalidArgument, "subscription_id required")
	}
	cm, err := s.st.GetClientMap(ctx, subscriptionID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil, nil, nil
	}
	if err != nil {
		return nil, nil, nil, status.Error(codes.Internal, "client map lookup failed")
	}
	sv, a, err := s.server(ctx, cm.ServerID)
	if err != nil {
		return nil, nil, nil, err
	}
	return cm, sv, a, nil
}

func enabledInboundIDs(ins []adapter.Inbound) []int {
	out := make([]int, 0, len(ins))
	for _, in := range ins {
		if in.Enabled {
			out = append(out, in.ID)
		}
	}
	return out
}

// CreateClient provisions a subscription on the panel. It is safe to retry:
// an already-mapped subscription is returned as is, and a client left on the
// panel by an earlier attempt (whose response was lost) is adopted when it
// carries our derived subscription id.
func (s *Server) CreateClient(ctx context.Context, req *provisionerv1.CreateClientRequest) (*provisionerv1.Client, error) {
	email := req.GetEmail()
	if req.GetSubscriptionId() == "" || xui.ValidateIdentifier(email) != nil {
		return nil, status.Error(codes.InvalidArgument, "a valid email and subscription_id are required")
	}
	if req.GetDurationDays() < 0 || req.GetTrafficBytes() < 0 || req.GetDurationDays() > 36500 {
		return nil, status.Error(codes.InvalidArgument, "duration_days and traffic_bytes out of range")
	}
	if cm, err := s.st.GetClientMap(ctx, req.GetSubscriptionId()); err == nil {
		if cm.Email != email {
			return nil, status.Error(codes.AlreadyExists, "subscription already provisioned with another email")
		}
		// A retry after the response was lost: report the limits the panel
		// holds (zeros would read as "never expires" in core).
		_, a, err := s.server(ctx, cm.ServerID)
		if err != nil {
			return nil, err
		}
		st, err := a.Status(ctx, cm.Email)
		if err != nil {
			return nil, s.panelError("panel client", err)
		}
		return clientProto(cm, max(st.ExpiryMs, 0)/1000, st.TotalBytes), nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, status.Error(codes.Internal, "client map lookup failed")
	}

	sv, a, err := s.server(ctx, req.GetServerId())
	if err != nil {
		return nil, err
	}
	ins, err := a.Inbounds(ctx)
	if err != nil {
		return nil, s.panelError("panel inbounds", err)
	}
	inboundIDs := enabledInboundIDs(ins)
	if len(inboundIDs) == 0 {
		return nil, status.Error(codes.FailedPrecondition, "the panel has no enabled inbound to attach clients to")
	}
	var expiryMs int64
	if d := req.GetDurationDays(); d > 0 {
		expiryMs = time.Now().Add(time.Duration(d) * 24 * time.Hour).UnixMilli()
	}
	subID := s.st.SubIDFor(req.GetSubscriptionId())
	if err := a.CreateClient(ctx, email, subID, expiryMs, req.GetTrafficBytes(), inboundIDs); err != nil {
		existing, lookupErr := a.ClientSubID(ctx, email)
		switch {
		case lookupErr != nil:
			s.log.Warn("3x-ui create failed", "err", err)
			return nil, s.panelError("panel create", lookupErr)
		case existing != subID:
			return nil, status.Error(codes.AlreadyExists, "another client on the panel already uses this email")
		}
		// Ours, from an earlier attempt: adopt it with the limits it has.
		if st, err := a.Status(ctx, email); err == nil {
			expiryMs = max(st.ExpiryMs, 0)
		}
	}
	cm := &store.ClientMap{
		SubscriptionID: req.GetSubscriptionId(), ServerID: sv.ID, Email: email,
		XUISubID: &subID, InboundIDs: toInt32s(inboundIDs),
	}
	if err := s.st.PutClientMap(ctx, cm); err != nil {
		return nil, status.Error(codes.Internal, "client map persist failed")
	}
	return clientProto(cm, expiryMs/1000, req.GetTrafficBytes()), nil
}

func clientProto(cm *store.ClientMap, expiresAt, traffic int64) *provisionerv1.Client {
	c := &provisionerv1.Client{
		SubscriptionId: cm.SubscriptionID, ServerId: cm.ServerID, Email: cm.Email,
		InboundIds: cm.InboundIDs, ExpiresAt: expiresAt, TrafficTotalBytes: traffic,
	}
	if cm.XUISubID != nil {
		c.XuiSubId = *cm.XUISubID
	}
	return c
}

// RenewClient extends expiry/traffic on the mapped panel.
func (s *Server) RenewClient(ctx context.Context, req *provisionerv1.RenewClientRequest) (*provisionerv1.Client, error) {
	cm, _, a, err := s.subscription(ctx, req.GetSubscriptionId())
	if err != nil {
		return nil, err
	}
	if cm == nil {
		return nil, status.Error(codes.NotFound, "client not provisioned")
	}
	if err := a.Renew(ctx, cm.Email, req.GetAddDays(), req.GetAddBytes()); err != nil {
		return nil, s.panelError("panel renew", err)
	}
	return clientProto(cm, 0, 0), nil
}

// SetClientLimits sets the absolute expiry and quota of a provisioned client
// and enables it (renewals and traffic top-ups). Setting the same values
// again changes nothing, so a retried order cannot add twice.
func (s *Server) SetClientLimits(ctx context.Context, req *provisionerv1.SetClientLimitsRequest) (*provisionerv1.Client, error) {
	if req.GetExpiresAt() < 0 || req.GetTrafficTotalBytes() < 0 {
		return nil, status.Error(codes.InvalidArgument, "expires_at and traffic_total_bytes must be >= 0")
	}
	cm, _, a, err := s.subscription(ctx, req.GetSubscriptionId())
	if err != nil {
		return nil, err
	}
	if cm == nil {
		return nil, status.Error(codes.NotFound, "client not provisioned")
	}
	if err := a.SetLimits(ctx, cm.Email, req.GetExpiresAt()*1000, req.GetTrafficTotalBytes()); err != nil {
		return nil, s.panelError("panel set limits", err)
	}
	return clientProto(cm, req.GetExpiresAt(), req.GetTrafficTotalBytes()), nil
}

// DeleteClient removes the client from the panel and the map (idempotent).
func (s *Server) DeleteClient(ctx context.Context, req *provisionerv1.DeleteClientRequest) (*emptypb.Empty, error) {
	cm, _, a, err := s.subscription(ctx, req.GetSubscriptionId())
	if err != nil {
		return nil, err
	}
	if cm == nil {
		return &emptypb.Empty{}, nil
	}
	if err := a.Delete(ctx, cm.Email); err != nil {
		return nil, s.panelError("panel delete", err)
	}
	if err := s.st.DeleteClientMap(ctx, req.GetSubscriptionId()); err != nil {
		return nil, status.Error(codes.Internal, "client map delete failed")
	}
	return &emptypb.Empty{}, nil
}

// ResetTraffic zeroes the client's counter on the panel.
func (s *Server) ResetTraffic(ctx context.Context, req *provisionerv1.ResetTrafficRequest) (*provisionerv1.Client, error) {
	cm, _, a, err := s.subscription(ctx, req.GetSubscriptionId())
	if err != nil {
		return nil, err
	}
	if cm == nil {
		return nil, status.Error(codes.NotFound, "client not provisioned")
	}
	if err := a.ResetTraffic(ctx, cm.Email); err != nil {
		return nil, s.panelError("panel reset", err)
	}
	return clientProto(cm, 0, 0), nil
}

// GetUsage returns the subscription's used bytes and the limits the panel
// holds for it (an operator may have changed them in the panel).
func (s *Server) GetUsage(ctx context.Context, req *provisionerv1.GetUsageRequest) (*provisionerv1.Usage, error) {
	cm, _, a, err := s.subscription(ctx, req.GetSubscriptionId())
	if err != nil {
		return nil, err
	}
	if cm == nil {
		return nil, status.Error(codes.NotFound, "client not provisioned")
	}
	st, err := a.Status(ctx, cm.Email)
	if err != nil {
		return nil, s.panelError("panel usage", err)
	}
	return &provisionerv1.Usage{
		SubscriptionId: cm.SubscriptionID, TrafficUsedBytes: st.UsedBytes, LastSyncedAt: time.Now().Unix(),
		TrafficTotalBytes: st.TotalBytes, ExpiresAt: max(st.ExpiryMs, 0) / 1000, Enabled: st.Enabled,
	}, nil
}

// GetLinks returns the subscription link (and its QR code as a PNG) plus one
// share link per inbound. The subscription link is the panel's subscription
// URL when the server has a subscription base URL configured; otherwise the
// first share link.
func (s *Server) GetLinks(ctx context.Context, req *provisionerv1.GetLinksRequest) (*provisionerv1.Links, error) {
	cm, sv, a, err := s.subscription(ctx, req.GetSubscriptionId())
	if err != nil {
		return nil, err
	}
	if cm == nil {
		return nil, status.Error(codes.NotFound, "client not provisioned")
	}
	var configs []string
	if cm.XUISubID != nil && *cm.XUISubID != "" {
		configs, _ = a.SubLinks(ctx, *cm.XUISubID)
	}
	if len(configs) == 0 {
		configs, _ = a.Links(ctx, cm.Email)
	}
	link := ""
	if sv.SubBaseURL != "" && cm.XUISubID != nil {
		link = strings.TrimRight(sv.SubBaseURL, "/") + "/" + *cm.XUISubID
	} else if len(configs) > 0 {
		link = configs[0]
	}
	if link == "" {
		return nil, s.panelError("panel links", err)
	}
	png, err := qrcode.Encode(link, qrcode.Medium, 512)
	if err != nil {
		return nil, status.Error(codes.Internal, "qr encode failed")
	}
	return &provisionerv1.Links{
		SubscriptionLink: link,
		QrPngBase64:      base64.StdEncoding.EncodeToString(png),
		ConfigLinks:      configs,
	}, nil
}

// ListInbounds lists panel inbounds for a server (or the default).
func (s *Server) ListInbounds(ctx context.Context, req *provisionerv1.ListInboundsRequest) (*provisionerv1.ListInboundsResponse, error) {
	_, a, err := s.server(ctx, req.GetServerId())
	if err != nil {
		return nil, err
	}
	ins, err := a.Inbounds(ctx)
	if err != nil {
		return nil, s.panelError("panel inbounds", err)
	}
	out := &provisionerv1.ListInboundsResponse{}
	for _, in := range ins {
		if in.ID < 0 || in.ID > math.MaxInt32 {
			continue
		}
		out.Inbounds = append(out.Inbounds, &provisionerv1.Inbound{Id: int32(in.ID), Tag: in.Tag, Enabled: in.Enabled}) //nolint:gosec // range checked above
	}
	return out, nil
}

// AddServer registers a new 3x-ui panel (token encrypted at rest).
func (s *Server) AddServer(ctx context.Context, req *provisionerv1.AddServerRequest) (*provisionerv1.XUIServer, error) {
	sv, err := s.addServer(ctx, req.GetName(), req.GetBaseUrl(), req.GetApiToken(), req.GetSubBaseUrl(), req.GetAllowPrivate())
	if err != nil {
		return nil, err
	}
	return &provisionerv1.XUIServer{Id: sv.ID, Name: sv.Name, BaseUrl: sv.BaseURL, Enabled: sv.Enabled, SubBaseUrl: sv.SubBaseURL}, nil
}

func (s *Server) addServer(ctx context.Context, name, baseURL, token, subBaseURL string, allowPrivate bool) (*store.Server, error) {
	name, baseURL, subBaseURL = strings.TrimSpace(name), strings.TrimSpace(baseURL), strings.TrimSpace(subBaseURL)
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "name, base_url and api_token required")
	}
	if err := s.validateServer(baseURL, token, subBaseURL, allowPrivate); err != nil {
		return nil, err
	}
	sv, err := s.st.AddServer(ctx, &store.Server{
		Name: name, BaseURL: baseURL, Token: token, Enabled: true,
		SubBaseURL: subBaseURL, AllowPrivate: allowPrivate,
	})
	if err != nil {
		return nil, status.Error(codes.Internal, "server persist failed")
	}
	return sv, nil
}

// validateServer refuses what the panel client would refuse later (plain http
// without allow-private, credentials in the URL...) and a malformed sub URL.
func (s *Server) validateServer(baseURL, token, subBaseURL string, allowPrivate bool) error {
	if baseURL == "" || token == "" {
		return status.Error(codes.InvalidArgument, "name, base_url and api_token required")
	}
	if _, err := xui.New(baseURL, token, s.clientOptions(allowPrivate)...); err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
	}
	if subBaseURL != "" {
		u, err := url.Parse(subBaseURL)
		if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
			return status.Error(codes.InvalidArgument, "sub_base_url must be an http(s) URL")
		}
	}
	return nil
}

// EnsureDefaultServer registers the installer-provided panel as "default" on
// first start and keeps that row in line with the environment afterwards, so
// re-running `bobres install` with a corrected URL, token or option takes
// effect. Until the dashboard manages panels, the environment is the only place
// to change them. It reports whether anything was written.
func (s *Server) EnsureDefaultServer(ctx context.Context, baseURL, token, subBaseURL string, allowPrivate bool) (bool, error) {
	baseURL, subBaseURL = strings.TrimSpace(baseURL), strings.TrimSpace(subBaseURL)
	if baseURL == "" {
		return false, nil
	}
	sv, err := s.st.ServerByName(ctx, "default")
	switch {
	case errors.Is(err, store.ErrNotFound):
		n, err := s.st.CountServers(ctx)
		if err != nil {
			return false, err
		}
		if n > 0 { // panels were registered another way; leave them alone
			return false, nil
		}
		if _, err := s.addServer(ctx, "default", baseURL, token, subBaseURL, allowPrivate); err != nil {
			return false, fmt.Errorf("register 3x-ui panel from BOBRES_XUI_URL: %w", err)
		}
		return true, nil
	case err != nil:
		return false, err
	}
	if sv.BaseURL == baseURL && sv.Token == token && sv.SubBaseURL == subBaseURL && sv.AllowPrivate == allowPrivate {
		return false, nil
	}
	if err := s.validateServer(baseURL, token, subBaseURL, allowPrivate); err != nil {
		return false, fmt.Errorf("update 3x-ui panel from BOBRES_XUI_URL: %w", err)
	}
	if err := s.st.UpdateServerConnection(ctx, sv.ID, baseURL, token, subBaseURL, allowPrivate); err != nil {
		return false, err
	}
	return true, nil
}

// HealthCheck probes the panel and records its version.
func (s *Server) HealthCheck(ctx context.Context, req *provisionerv1.HealthCheckRequest) (*provisionerv1.HealthCheckResponse, error) {
	sv, a, err := s.server(ctx, req.GetServerId())
	if err != nil {
		return &provisionerv1.HealthCheckResponse{Healthy: false, Detail: status.Convert(err).Message()}, nil
	}
	ver, err := a.HealthCheck(ctx)
	if err != nil {
		s.log.Warn("3x-ui health check failed", "err", err)
		detail := "panel unreachable"
		if policyRefusal(err) {
			detail = err.Error()
		}
		return &provisionerv1.HealthCheckResponse{Healthy: false, Detail: detail}, nil
	}
	_ = s.st.TouchHealth(ctx, sv.ID, ver)
	return &provisionerv1.HealthCheckResponse{Healthy: true, PanelVersion: ver}, nil
}

func toInt32s(in []int) []int32 {
	out := make([]int32, 0, len(in))
	for _, v := range in {
		if v >= 0 && v <= math.MaxInt32 {
			out = append(out, int32(v)) //nolint:gosec // range checked
		}
	}
	return out
}
