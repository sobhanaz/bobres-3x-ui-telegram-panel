// Package server exposes the provisioner over gRPC. It is the ONLY process
// that holds 3x-ui API tokens (encrypted at rest, decrypted in memory only).
package server

import (
	"context"
	"encoding/base64"
	"errors"
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
}

// New builds the server. xuiOpts relax panel-client hardening in tests only.
func New(st *store.Store, xuiOpts ...xui.Option) *Server { return &Server{st: st, xuiOpts: xuiOpts} }

// Register attaches the service.
func (s *Server) Register(g *grpc.Server) { provisionerv1.RegisterProvisionerServiceServer(g, s) }

// adapterFor resolves the panel client for a subscription's server, or the
// first enabled server when subscriptionID is empty / unmapped.
func (s *Server) adapterFor(ctx context.Context, subscriptionID string) (adapter.PanelAdapter, *store.ClientMap, *store.Server, error) {
	var (
		cm  *store.ClientMap
		sv  *store.Server
		err error
	)
	if subscriptionID != "" {
		if m, e := s.st.GetClientMap(ctx, subscriptionID); e == nil {
			cm = m
		}
	}
	if cm != nil {
		sv, err = s.st.GetServer(ctx, cm.ServerID)
	} else {
		sv, err = s.st.FirstServer(ctx)
	}
	if err != nil {
		return nil, nil, nil, status.Error(codes.FailedPrecondition, "no enabled 3x-ui server")
	}
	a, err := adapter.NewXUIv3(sv.BaseURL, sv.Token, s.xuiOpts...)
	if err != nil {
		return nil, nil, nil, status.Error(codes.Internal, "adapter init failed")
	}
	return a, cm, sv, nil
}

func enabledInboundIDs(ins []adapter.Inbound) []int {
	out := make([]int, 0, len(ins))
	for _, in := range ins {
		if in.Enabled {
			out = append(out, in.ID)
		}
	}
	if len(out) == 0 {
		out = append(out, 1) // defensive: 3x-ui rejects empty inbound lists
	}
	return out
}

// CreateClient provisions a subscription on the panel.
func (s *Server) CreateClient(ctx context.Context, req *provisionerv1.CreateClientRequest) (*provisionerv1.Client, error) {
	if req.GetEmail() == "" || req.GetSubscriptionId() == "" {
		return nil, status.Error(codes.InvalidArgument, "email and subscription_id required")
	}
	if req.GetDurationDays() < 0 || req.GetTrafficBytes() < 0 {
		return nil, status.Error(codes.InvalidArgument, "duration_days and traffic_bytes must be >= 0")
	}
	a, _, sv, err := s.adapterFor(ctx, "")
	if err != nil {
		return nil, err
	}
	ins, err := a.Inbounds(ctx)
	if err != nil {
		return nil, status.Error(codes.Unavailable, "panel unreachable")
	}
	var expiryMs int64
	if req.GetDurationDays() > 0 {
		expiryMs = time.Now().Add(time.Duration(req.GetDurationDays()) * 24 * time.Hour).UnixMilli()
	}
	inboundIDs := enabledInboundIDs(ins)
	subID := shortID(req.GetSubscriptionId())
	if err := a.CreateClient(ctx, req.GetEmail(), subID, expiryMs, req.GetTrafficBytes(), inboundIDs); err != nil {
		return nil, status.Error(codes.Unavailable, "panel create failed")
	}
	cm := &store.ClientMap{
		SubscriptionID: req.GetSubscriptionId(),
		ServerID:       sv.ID,
		Email:          req.GetEmail(),
		XUISubID:       &subID,
		InboundIDs:     intTo32(inboundIDs),
	}
	if err := s.st.PutClientMap(ctx, cm); err != nil {
		return nil, status.Error(codes.Internal, "client map persist failed")
	}
	return &provisionerv1.Client{
		SubscriptionId:    req.GetSubscriptionId(),
		ServerId:          sv.ID,
		Email:             req.GetEmail(),
		XuiSubId:          subID,
		InboundIds:        cm.InboundIDs,
		ExpiresAt:         expiryMs / 1000,
		TrafficTotalBytes: req.GetTrafficBytes(),
	}, nil
}

// RenewClient extends expiry/traffic on the mapped panel.
func (s *Server) RenewClient(ctx context.Context, req *provisionerv1.RenewClientRequest) (*provisionerv1.Client, error) {
	if req.GetSubscriptionId() == "" {
		return nil, status.Error(codes.InvalidArgument, "subscription_id required")
	}
	a, cm, sv, err := s.adapterFor(ctx, req.GetSubscriptionId())
	if err != nil {
		return nil, err
	}
	if cm == nil {
		return nil, status.Error(codes.NotFound, "client not provisioned")
	}
	if err := a.Renew(ctx, cm.Email, req.GetAddDays(), req.GetAddBytes()); err != nil {
		return nil, status.Error(codes.Unavailable, "panel renew failed")
	}
	return &provisionerv1.Client{
		SubscriptionId: cm.SubscriptionID,
		ServerId:       sv.ID,
		Email:          cm.Email,
		InboundIds:     cm.InboundIDs,
	}, nil
}

// DeleteClient removes the client from the panel and the map.
func (s *Server) DeleteClient(ctx context.Context, req *provisionerv1.DeleteClientRequest) (*emptypb.Empty, error) {
	a, cm, _, err := s.adapterFor(ctx, req.GetSubscriptionId())
	if err != nil {
		return nil, err
	}
	if cm == nil {
		return &emptypb.Empty{}, nil // idempotent: nothing to delete
	}
	if err := a.Delete(ctx, cm.Email); err != nil {
		return nil, status.Error(codes.Unavailable, "panel delete failed")
	}
	if err := s.st.DeleteClientMap(ctx, req.GetSubscriptionId()); err != nil {
		return nil, status.Error(codes.Internal, "client map delete failed")
	}
	return &emptypb.Empty{}, nil
}

// ResetTraffic zeroes the client's counter on the panel.
func (s *Server) ResetTraffic(ctx context.Context, req *provisionerv1.ResetTrafficRequest) (*provisionerv1.Client, error) {
	a, cm, sv, err := s.adapterFor(ctx, req.GetSubscriptionId())
	if err != nil {
		return nil, err
	}
	if cm == nil {
		return nil, status.Error(codes.NotFound, "client not provisioned")
	}
	if err := a.ResetTraffic(ctx, cm.Email); err != nil {
		return nil, status.Error(codes.Unavailable, "panel reset failed")
	}
	return &provisionerv1.Client{SubscriptionId: cm.SubscriptionID, ServerId: sv.ID, Email: cm.Email}, nil
}

// GetUsage returns used bytes for the subscription.
func (s *Server) GetUsage(ctx context.Context, req *provisionerv1.GetUsageRequest) (*provisionerv1.Usage, error) {
	a, cm, _, err := s.adapterFor(ctx, req.GetSubscriptionId())
	if err != nil {
		return nil, err
	}
	if cm == nil {
		return nil, status.Error(codes.NotFound, "client not provisioned")
	}
	used, err := a.Usage(ctx, cm.Email)
	if err != nil {
		return nil, status.Error(codes.Unavailable, "panel usage failed")
	}
	return &provisionerv1.Usage{SubscriptionId: cm.SubscriptionID, TrafficUsedBytes: used, LastSyncedAt: time.Now().Unix()}, nil
}

// GetLinks returns the subscription URL and a PNG QR in base64.
func (s *Server) GetLinks(ctx context.Context, req *provisionerv1.GetLinksRequest) (*provisionerv1.Links, error) {
	a, cm, _, err := s.adapterFor(ctx, req.GetSubscriptionId())
	if err != nil {
		return nil, err
	}
	if cm == nil {
		return nil, status.Error(codes.NotFound, "client not provisioned")
	}
	var links []string
	if cm.XUISubID != nil && *cm.XUISubID != "" {
		links, err = subLinks(ctx, a, *cm.XUISubID)
	}
	if len(links) == 0 {
		links, err = a.Links(ctx, cm.Email)
	}
	if err != nil || len(links) == 0 {
		return nil, status.Error(codes.Unavailable, "panel links failed")
	}
	png, err := qrcode.Encode(links[0], qrcode.Medium, 256)
	if err != nil {
		return nil, status.Error(codes.Internal, "qr encode failed")
	}
	return &provisionerv1.Links{
		SubscriptionLink: links[0],
		QrPngBase64:      base64.StdEncoding.EncodeToString(png),
	}, nil
}

// subLinks uses the panel's subId endpoint when available.
func subLinks(ctx context.Context, a adapter.PanelAdapter, xuiSubID string) ([]string, error) {
	if sa, ok := a.(interface {
		SubLinks(context.Context, string) ([]string, error)
	}); ok {
		return sa.SubLinks(ctx, xuiSubID)
	}
	return nil, errors.New("sub links unsupported")
}

// ListInbounds lists panel inbounds for a server (or the default).
func (s *Server) ListInbounds(ctx context.Context, req *provisionerv1.ListInboundsRequest) (*provisionerv1.ListInboundsResponse, error) {
	a, _, _, err := s.adapterFor(ctx, req.GetServerId())
	if err != nil {
		return nil, err
	}
	ins, err := a.Inbounds(ctx)
	if err != nil {
		return nil, status.Error(codes.Unavailable, "panel unreachable")
	}
	out := &provisionerv1.ListInboundsResponse{}
	for _, in := range ins {
		out.Inbounds = append(out.Inbounds, &provisionerv1.Inbound{Id: int32(in.ID), Tag: in.Tag, Enabled: in.Enabled})
	}
	return out, nil
}

// AddServer registers a new 3x-ui panel (token encrypted at rest).
func (s *Server) AddServer(ctx context.Context, req *provisionerv1.AddServerRequest) (*provisionerv1.XUIServer, error) {
	if req.GetBaseUrl() == "" || req.GetApiToken() == "" || req.GetName() == "" {
		return nil, status.Error(codes.InvalidArgument, "name, base_url and api_token required")
	}
	sv, err := s.st.AddServer(ctx, &store.Server{
		Name: req.GetName(), BaseURL: req.GetBaseUrl(), Token: req.GetApiToken(), Enabled: true,
	})
	if err != nil {
		return nil, status.Error(codes.Internal, "server persist failed")
	}
	return &provisionerv1.XUIServer{
		Id: sv.ID, Name: sv.Name, BaseUrl: sv.BaseURL, Enabled: sv.Enabled,
	}, nil
}

// HealthCheck probes the panel and records its version.
func (s *Server) HealthCheck(ctx context.Context, req *provisionerv1.HealthCheckRequest) (*provisionerv1.HealthCheckResponse, error) {
	a, _, sv, err := s.adapterFor(ctx, req.GetServerId())
	if err != nil {
		return &provisionerv1.HealthCheckResponse{Healthy: false, Detail: err.Error()}, nil
	}
	ver, err := a.HealthCheck(ctx)
	if err != nil {
		return &provisionerv1.HealthCheckResponse{Healthy: false, Detail: "panel unreachable"}, nil
	}
	_ = s.st.TouchHealth(ctx, sv.ID, ver)
	return &provisionerv1.HealthCheckResponse{Healthy: true, PanelVersion: ver}, nil
}

func intTo32(in []int) []int32 {
	out := make([]int32, len(in))
	for i, v := range in {
		out[i] = int32(v)
	}
	return out
}

// shortID derives a compact subId from a UUID subscription id.
func shortID(sub string) string {
	if len(sub) >= 8 {
		return sub[:8]
	}
	return sub
}
