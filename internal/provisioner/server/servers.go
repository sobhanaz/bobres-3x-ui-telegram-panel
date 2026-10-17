package server

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	provisionerv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/provisioner/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/marzban"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/provisioner/adapter"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/provisioner/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/xui"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

// HealthInterval is how often RunHealth checks the enabled servers.
const HealthInterval = 60 * time.Second

// probeTimeout bounds one connection test or health check of a panel.
const probeTimeout = 20 * time.Second

const maxNameLen = 64

func serverProto(in *store.ServerInfo) *provisionerv1.XUIServer {
	p := &provisionerv1.XUIServer{
		Id: in.ID, Name: in.Name, BaseUrl: in.BaseURL, PanelVersion: in.PanelVersion, Enabled: in.Enabled,
		SubBaseUrl: in.SubBaseURL, AllowPrivate: in.AllowPrivate, Weight: in.Weight, Managed: in.Managed,
		HasToken: in.HasToken, Clients: in.Clients, Kind: in.Kind,
		VersionUntested: in.Kind != store.KindMarzban && adapter.Untested(in.PanelVersion),
	}
	if in.CheckedAt != nil {
		p.CheckedAt = in.CheckedAt.Unix()
		p.Healthy = in.HealthOK != nil && *in.HealthOK
		p.HealthError = in.HealthError
	}
	return p
}

// serverInfo answers a write with the server as listed.
func (s *Server) serverInfo(ctx context.Context, id string) (*provisionerv1.XUIServer, error) {
	in, err := s.st.ServerInfo(ctx, id)
	if err != nil {
		return nil, status.Error(codes.Internal, "server lookup failed")
	}
	return serverProto(in), nil
}

// ListServers lists every registered panel with its health and client count.
// Tokens are never part of the answer.
func (s *Server) ListServers(ctx context.Context, _ *provisionerv1.ListServersRequest) (*provisionerv1.ListServersResponse, error) {
	infos, err := s.st.ListServers(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "server list failed")
	}
	out := &provisionerv1.ListServersResponse{
		TestedMinVersion: adapter.TestedMinVersion, TestedMaxVersion: adapter.TestedMaxVersion,
	}
	for _, in := range infos {
		out.Servers = append(out.Servers, serverProto(in))
	}
	return out, nil
}

// AddServer registers a new panel, 3x-ui or Marzban (token encrypted at rest). The
// connection is tested first unless skip_check. It is saved switched on
// unless the request says enabled=false (unset is on, for older callers).
func (s *Server) AddServer(ctx context.Context, req *provisionerv1.AddServerRequest) (*provisionerv1.XUIServer, error) {
	weight := req.GetWeight()
	if weight == 0 {
		weight = store.DefaultWeight
	}
	enabled := req.Enabled == nil || req.GetEnabled()
	sv, err := s.addServer(ctx, &store.Server{
		Name: req.GetName(), BaseURL: req.GetBaseUrl(), Token: req.GetApiToken(), Enabled: enabled,
		SubBaseURL: req.GetSubBaseUrl(), AllowPrivate: req.GetAllowPrivate(), Weight: weight, Managed: store.ManagedDashboard,
		Kind: req.GetKind(),
	}, !req.GetSkipCheck())
	if err != nil {
		return nil, err
	}
	return s.serverInfo(ctx, sv.ID)
}

func (s *Server) addServer(ctx context.Context, sv *store.Server, test bool) (*store.Server, error) {
	if sv.Kind == "" {
		sv.Kind = store.KindXUI
	}
	sv.Name, sv.BaseURL, sv.SubBaseURL = strings.TrimSpace(sv.Name), strings.TrimSpace(sv.BaseURL), strings.TrimSpace(sv.SubBaseURL)
	if err := validateSettings(sv.Name, sv.Weight); err != nil {
		return nil, err
	}
	if err := s.validateServer(sv.Kind, sv.BaseURL, sv.Token, sv.SubBaseURL, sv.AllowPrivate); err != nil {
		return nil, err
	}
	var version string
	if test {
		v, err := s.testConnection(ctx, sv.Kind, sv.BaseURL, sv.Token, sv.AllowPrivate)
		if err != nil {
			return nil, err
		}
		version = v
	}
	created, err := s.st.AddServer(ctx, sv)
	switch {
	case errors.Is(err, store.ErrNameTaken):
		return nil, status.Error(codes.AlreadyExists, "another server already has this name")
	case err != nil:
		return nil, status.Error(codes.Internal, "server persist failed")
	}
	if test {
		s.recordHealth(ctx, created.ID, created.UpdatedAt, true, version, "")
	}
	return created, nil
}

// validateSettings checks what an operator types besides the connection.
func validateSettings(name string, weight int32) error {
	if name == "" || utf8.RuneCountInString(name) > maxNameLen {
		return status.Error(codes.InvalidArgument, fmt.Sprintf("name required (at most %d characters)", maxNameLen))
	}
	if weight < 1 || weight > 1000 {
		return status.Error(codes.InvalidArgument, "weight must be 1 to 1000")
	}
	return nil
}

// validateServer refuses what the panel client would refuse later (plain http
// without allow-private, credentials in the URL...) and a malformed sub URL.
func (s *Server) validateServer(kind, baseURL, token, subBaseURL string, allowPrivate bool) error {
	if !store.ValidKind(kind) {
		return status.Error(codes.InvalidArgument, "unknown panel kind")
	}
	if baseURL == "" || token == "" {
		return status.Error(codes.InvalidArgument, "name, base_url and api_token required")
	}
	if _, err := newAdapter(kind, baseURL, token, s.clientOptions(allowPrivate)...); err != nil {
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

// sameHost reports whether two panel URLs point at the same host and port,
// so a stored token may be used with the second one.
func sameHost(a, b string) bool {
	ua, errA := url.Parse(a)
	ub, errB := url.Parse(b)
	return errA == nil && errB == nil && ua.Host != "" && strings.EqualFold(ua.Host, ub.Host)
}

// storedToken returns the token of server id for use with baseURL, which
// must be the address the token was entered for: an operator cannot send it
// to a host of their choosing.
func (s *Server) storedToken(ctx context.Context, cur *store.ServerInfo, baseURL string) (string, error) {
	if !sameHost(cur.BaseURL, baseURL) {
		return "", status.Error(codes.InvalidArgument, "enter the API token again: the panel's address changed")
	}
	sv, err := s.st.GetServer(ctx, cur.ID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return "", status.Error(codes.NotFound, "server not found")
	case errors.Is(err, store.ErrTokenUnreadable):
		return "", status.Error(codes.FailedPrecondition, "the stored API token cannot be read: enter it again")
	case err != nil:
		return "", status.Error(codes.Internal, "server lookup failed")
	}
	return sv.Token, nil
}

// lookupInfo resolves a server id for a write (no token decrypted).
func (s *Server) lookupInfo(ctx context.Context, id string) (*store.ServerInfo, error) {
	cid, ok := canonicalID(id)
	if !ok {
		return nil, status.Error(codes.NotFound, "server not found")
	}
	in, err := s.st.ServerInfo(ctx, cid)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return nil, status.Error(codes.NotFound, "server not found")
	case err != nil:
		return nil, status.Error(codes.Internal, "server lookup failed")
	}
	return in, nil
}

// UpdateServer replaces a server's settings. An empty token keeps the stored
// one (on the same host only). A changed connection is tested first unless
// skip_check; renaming, weighting or disabling never contacts the panel.
func (s *Server) UpdateServer(ctx context.Context, req *provisionerv1.UpdateServerRequest) (*provisionerv1.XUIServer, error) {
	cur, err := s.lookupInfo(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	sv := &store.Server{
		ID: cur.ID, Name: strings.TrimSpace(req.GetName()), BaseURL: strings.TrimSpace(req.GetBaseUrl()),
		Token: req.GetApiToken(), Enabled: req.GetEnabled(), SubBaseURL: strings.TrimSpace(req.GetSubBaseUrl()),
		AllowPrivate: req.GetAllowPrivate(), Weight: req.GetWeight(), Kind: cur.Kind,
	}
	if err := validateSettings(sv.Name, sv.Weight); err != nil {
		return nil, err
	}
	if sv.BaseURL == "" {
		return nil, status.Error(codes.InvalidArgument, "base_url required")
	}
	newToken := sv.Token != ""
	if !newToken {
		if sv.Token, err = s.storedToken(ctx, cur, sv.BaseURL); err != nil {
			return nil, err
		}
	}
	if err := s.validateServer(sv.Kind, sv.BaseURL, sv.Token, sv.SubBaseURL, sv.AllowPrivate); err != nil {
		return nil, err
	}
	connChanged := newToken || sv.BaseURL != cur.BaseURL || sv.AllowPrivate != cur.AllowPrivate
	tested := connChanged && !req.GetSkipCheck()
	var version string
	if tested {
		if version, err = s.testConnection(ctx, sv.Kind, sv.BaseURL, sv.Token, sv.AllowPrivate); err != nil {
			return nil, err
		}
	}
	switch err := s.st.UpdateServer(ctx, sv, connChanged); {
	case errors.Is(err, store.ErrNotFound):
		return nil, status.Error(codes.NotFound, "server not found")
	case errors.Is(err, store.ErrNameTaken):
		return nil, status.Error(codes.AlreadyExists, "another server already has this name")
	case err != nil:
		return nil, status.Error(codes.Internal, "server update failed")
	}
	s.forget(sv.ID)
	if tested {
		s.recordHealth(ctx, sv.ID, sv.UpdatedAt, true, version, "")
	}
	return s.serverInfo(ctx, sv.ID)
}

// forget drops a server's cached panel client.
func (s *Server) forget(id string) {
	s.mu.Lock()
	delete(s.adapters, id)
	s.mu.Unlock()
}

// DeleteServer removes a server no client maps to.
func (s *Server) DeleteServer(ctx context.Context, req *provisionerv1.DeleteServerRequest) (*emptypb.Empty, error) {
	id, ok := canonicalID(req.GetId())
	if !ok {
		return nil, status.Error(codes.NotFound, "server not found")
	}
	n, err := s.st.DeleteServer(ctx, id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return nil, status.Error(codes.NotFound, "server not found")
	case errors.Is(err, store.ErrServerInUse) && n > 0:
		return nil, status.Error(codes.FailedPrecondition, fmt.Sprintf("the server still has %d clients: delete or move them first", n))
	case errors.Is(err, store.ErrServerInUse):
		return nil, status.Error(codes.FailedPrecondition, "the server still has clients: delete or move them first")
	case err != nil:
		return nil, status.Error(codes.Internal, "server delete failed")
	}
	s.forget(id)
	return &emptypb.Empty{}, nil
}

// TestServer connects with the given settings, or a server's stored ones,
// and lists the inbounds. It saves nothing; a failed connection is ok=false
// with the reason, not an error.
func (s *Server) TestServer(ctx context.Context, req *provisionerv1.TestServerRequest) (*provisionerv1.TestServerResponse, error) {
	baseURL, token, allowPrivate := strings.TrimSpace(req.GetBaseUrl()), req.GetApiToken(), req.GetAllowPrivate()
	kind := req.GetKind()
	if kind == "" {
		kind = store.KindXUI
	}
	if req.GetId() != "" {
		cur, err := s.lookupInfo(ctx, req.GetId())
		if err != nil {
			return nil, err
		}
		kind = cur.Kind // a saved server's kind does not change
		if baseURL == "" {
			baseURL, allowPrivate = cur.BaseURL, cur.AllowPrivate
		}
		if token == "" {
			if token, err = s.storedToken(ctx, cur, baseURL); err != nil {
				return nil, err
			}
		}
	}
	if err := s.validateServer(kind, baseURL, token, "", allowPrivate); err != nil {
		return nil, err
	}
	n, version, err := s.probe(ctx, kind, baseURL, token, allowPrivate)
	if err != nil {
		return &provisionerv1.TestServerResponse{Detail: panelDetail(err)}, nil
	}
	return &provisionerv1.TestServerResponse{Ok: true, PanelVersion: version, Inbounds: int32(min(n, math.MaxInt32))}, nil //nolint:gosec // bounded above
}

// testConnection is the check before a server is saved: FailedPrecondition
// with the reason when the panel cannot be used.
func (s *Server) testConnection(ctx context.Context, kind, baseURL, token string, allowPrivate bool) (string, error) {
	_, version, err := s.probe(ctx, kind, baseURL, token, allowPrivate)
	if err != nil {
		return "", status.Error(codes.FailedPrecondition, "connection test failed: "+panelDetail(err))
	}
	return version, nil
}

// probe connects with these settings (a client of its own, not the cached
// one) and reports the inbound count and the panel version.
func (s *Server) probe(ctx context.Context, kind, baseURL, token string, allowPrivate bool) (int, string, error) {
	a, err := newAdapter(kind, baseURL, token, s.clientOptions(allowPrivate)...)
	if err != nil {
		return 0, "", err
	}
	return checkPanel(ctx, a)
}

// checkPanel is one health check: the inbound listing provisioning depends
// on, then the version.
func checkPanel(ctx context.Context, a adapter.PanelAdapter) (int, string, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	ins, err := a.Inbounds(ctx)
	if err != nil {
		return 0, "", err
	}
	return len(ins), a.PanelVersion(ctx), nil
}

const maxDetailLen = 300

// panelDetail says why a panel check failed, for the operator. xui errors
// carry neither the token nor the URL path.
func panelDetail(err error) string {
	var (
		ae     *xui.APIError
		me     *marzban.APIError
		detail string
	)
	switch {
	case policyRefusal(err):
		detail = err.Error()
	case xui.IsUnauthorized(err):
		detail = "the panel refused the API token"
	case marzban.IsUnauthorized(err):
		detail = "the panel refused the admin username or password"
	case errors.As(err, &me):
		detail = me.Error()
	case errors.Is(err, context.DeadlineExceeded):
		detail = "the panel did not answer in time"
	case errors.As(err, &ae):
		detail = ae.Error()
	default:
		detail = "panel unreachable: " + err.Error()
	}
	if len(detail) > maxDetailLen {
		detail = detail[:maxDetailLen] + "..."
	}
	return detail
}

// recordHealth stores a check result; a failure to store is only logged.
func (s *Server) recordHealth(ctx context.Context, id string, version time.Time, ok bool, panelVersion, detail string) {
	if err := s.st.RecordHealth(ctx, id, version, ok, panelVersion, detail); err != nil && ctx.Err() == nil {
		s.log.Warn("recording a panel health check failed", "err", err)
	}
}

// ListInbounds lists panel inbounds for a server (or the default), with
// whether new clients are attached to each.
func (s *Server) ListInbounds(ctx context.Context, req *provisionerv1.ListInboundsRequest) (*provisionerv1.ListInboundsResponse, error) {
	sv, a, err := s.server(ctx, req.GetServerId())
	if err != nil {
		return nil, err
	}
	ins, err := a.Inbounds(ctx)
	if err != nil {
		return nil, s.panelError("panel inbounds", err)
	}
	attach, err := s.st.InboundAttach(ctx, sv.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, "inbound settings lookup failed")
	}
	out := &provisionerv1.ListInboundsResponse{}
	for _, in := range ins {
		if in.ID < 0 || in.ID > math.MaxInt32 {
			continue
		}
		on, set := attach[in.ID]
		out.Inbounds = append(out.Inbounds, &provisionerv1.Inbound{
			Id: int32(in.ID), Tag: in.Tag, Enabled: in.Enabled, Attach: on || !set, //nolint:gosec // range checked above
			Protocol: in.Protocol, Port: int32(min(max(in.Port, 0), math.MaxInt32)), //nolint:gosec // clamped
		})
	}
	return out, nil
}

// SetInboundAttach sets whether new clients of a server are attached to an
// inbound. Existing clients keep their inbounds.
func (s *Server) SetInboundAttach(ctx context.Context, req *provisionerv1.SetInboundAttachRequest) (*emptypb.Empty, error) {
	id, ok := canonicalID(req.GetServerId())
	if !ok {
		return nil, status.Error(codes.NotFound, "server not found")
	}
	if req.GetInboundId() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "inbound_id required")
	}
	switch err := s.st.SetInboundAttach(ctx, id, int(req.GetInboundId()), req.GetAttach()); {
	case errors.Is(err, store.ErrNotFound):
		return nil, status.Error(codes.NotFound, "server not found")
	case err != nil:
		return nil, status.Error(codes.Internal, "inbound setting failed")
	}
	return &emptypb.Empty{}, nil
}

// defaultServerName names the first panel when its URL gives no usable host.
const defaultServerName = "default"

// seedName is the name the installer-provided panel gets: the host of its
// URL, lowercased, without port, path or IPv6 brackets ("panel.example.com",
// "203.0.113.7", "2001:db8::1"). A URL without a usable host, or a host too
// long for a server name, gives "default" (the name installs before this one
// kept).
func seedName(baseURL string) string {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return defaultServerName
	}
	h := strings.ToLower(u.Hostname())
	if h == "" || utf8.RuneCountInString(h) > maxNameLen {
		return defaultServerName
	}
	return h
}

// EnsureDefaultServer registers the installer-provided panel when no panel is
// registered yet, named after its host (seedName). It never touches an
// existing row: after the first start, panels are managed in the dashboard
// (an install seeded before keeps its name "default"). It reports whether it
// added one; the panel is not contacted (it may still be starting).
func (s *Server) EnsureDefaultServer(ctx context.Context, baseURL, token, subBaseURL string, allowPrivate bool) (bool, error) {
	if strings.TrimSpace(baseURL) == "" {
		return false, nil
	}
	n, err := s.st.CountServers(ctx)
	if err != nil {
		return false, err
	}
	if n > 0 {
		return false, nil
	}
	_, err = s.addServer(ctx, &store.Server{
		Name: seedName(baseURL), BaseURL: baseURL, Token: token, Enabled: true, SubBaseURL: subBaseURL,
		AllowPrivate: allowPrivate, Weight: store.DefaultWeight, Managed: store.ManagedEnv,
	}, false)
	switch {
	case status.Code(err) == codes.AlreadyExists: // another instance seeded it first
		return false, nil
	case err != nil:
		return false, fmt.Errorf("register 3x-ui panel from BOBRES_XUI_URL: %w", err)
	}
	return true, nil
}

// HealthCheck probes the panel and records the result.
func (s *Server) HealthCheck(ctx context.Context, req *provisionerv1.HealthCheckRequest) (*provisionerv1.HealthCheckResponse, error) {
	sv, a, err := s.server(ctx, req.GetServerId())
	if err != nil {
		return &provisionerv1.HealthCheckResponse{Healthy: false, Detail: status.Convert(err).Message()}, nil
	}
	ver, err := a.HealthCheck(ctx)
	if err != nil {
		s.log.Warn("panel health check failed", "err", err)
		s.recordHealth(ctx, sv.ID, sv.UpdatedAt, false, "", panelDetail(err))
		detail := "panel unreachable"
		if policyRefusal(err) {
			detail = err.Error()
		}
		return &provisionerv1.HealthCheckResponse{Healthy: false, Detail: detail}, nil
	}
	s.recordHealth(ctx, sv.ID, sv.UpdatedAt, true, ver, "")
	return &provisionerv1.HealthCheckResponse{Healthy: true, PanelVersion: ver}, nil
}

// RunHealth checks every enabled server now and then every interval until
// ctx ends. A failing server gets no new clients until it passes again.
func (s *Server) RunHealth(ctx context.Context, interval time.Duration) error {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		s.CheckHealth(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// CheckHealth checks every enabled server once, in parallel, and stores
// each result (ok, error, version, time). Changes of state are logged.
func (s *Server) CheckHealth(ctx context.Context) {
	infos, err := s.st.ListServers(ctx)
	if err != nil {
		if ctx.Err() == nil {
			s.log.Warn("3x-ui health: listing servers failed", "err", err)
		}
		return
	}
	var wg sync.WaitGroup
	for _, in := range infos {
		if in.Enabled {
			wg.Go(func() { s.checkServer(ctx, in) })
		}
	}
	wg.Wait()
}

func (s *Server) checkServer(ctx context.Context, in *store.ServerInfo) {
	var (
		ok      bool
		ver     string
		detail  string
		version = in.UpdatedAt
	)
	sv, err := s.st.GetServer(ctx, in.ID)
	switch {
	case errors.Is(err, store.ErrNotFound): // deleted meanwhile
		return
	case errors.Is(err, store.ErrTokenUnreadable):
		detail = "the stored API token cannot be decrypted (was the master key changed?)"
	case err != nil:
		if ctx.Err() == nil {
			s.log.Warn("3x-ui health: server lookup failed", "err", err)
		}
		return
	default:
		version = sv.UpdatedAt
		a, err := s.adapterFor(sv)
		if err != nil {
			detail = "panel configuration is invalid"
			break
		}
		if _, ver, err = checkPanel(ctx, a); err != nil {
			detail = panelDetail(err)
		} else {
			ok = true
		}
	}
	if ctx.Err() != nil { // shutting down: not the panel's fault
		return
	}
	s.recordHealth(ctx, in.ID, version, ok, ver, detail)
	switch was := in.HealthOK; {
	case !ok && (was == nil || *was):
		s.log.Warn("3x-ui server failed its health check", "server", in.Name, "detail", detail)
	case ok && was != nil && !*was:
		s.log.Info("3x-ui server is healthy again", "server", in.Name)
	}
}
