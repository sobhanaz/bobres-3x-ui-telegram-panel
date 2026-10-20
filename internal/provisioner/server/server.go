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
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/skip2/go-qrcode"
	provisionerv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/provisioner/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/marzban"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/provisioner/adapter"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/provisioner/store"
	buuid "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
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

	fill groupFill // the backfill of the bot group (groupfill.go)
}

// cachedAdapter is reused for every call to one panel (keep-alive connections,
// shared per-email locks) until the server row changes.
type cachedAdapter struct {
	version time.Time
	a       adapter.PanelAdapter
}

// New builds the server. xuiOpts relax panel-client hardening in tests only.
func New(st *store.Store, xuiOpts ...xui.Option) *Server {
	return &Server{st: st, xuiOpts: xuiOpts, log: slog.New(slog.DiscardHandler), adapters: map[string]cachedAdapter{}, fill: newGroupFill()}
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
	s.log.Warn("panel call failed", "op", op, "err", err)
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

// newAdapter builds the adapter of a panel kind. token is the 3x-ui API token,
// or "username:password" of a Marzban admin.
func newAdapter(kind, baseURL, token string, opts ...xui.Option) (adapter.PanelAdapter, error) {
	switch kind {
	case store.KindMarzban:
		return adapter.NewMarzban(baseURL, token, opts...)
	case store.KindXUI, "":
		return adapter.NewXUIv3(baseURL, token, opts...)
	}
	return nil, fmt.Errorf("unknown panel kind %q", kind)
}

func (s *Server) adapterFor(sv *store.Server) (adapter.PanelAdapter, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.adapters[sv.ID]; ok && c.version.Equal(sv.UpdatedAt) {
		return c.a, nil
	}
	a, err := newAdapter(sv.Kind, sv.BaseURL, sv.Token, s.clientOptions(sv.AllowPrivate)...)
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, "panel configuration is invalid")
	}
	s.adapters[sv.ID] = cachedAdapter{version: sv.UpdatedAt, a: a}
	return a, nil
}

// canonicalID is a server id in canonical form; ok is false for a malformed one.
func canonicalID(id string) (string, bool) {
	u, err := buuid.Parse(strings.TrimSpace(id))
	if err != nil {
		return "", false
	}
	return u.String(), true
}

// server resolves a panel by id, or the first enabled panel when id is empty.
// An id resolves a disabled panel too: its existing clients keep working.
func (s *Server) server(ctx context.Context, id string) (*store.Server, adapter.PanelAdapter, error) {
	var (
		sv  *store.Server
		err error
	)
	if id != "" {
		cid, ok := canonicalID(id)
		if !ok {
			return nil, nil, status.Error(codes.NotFound, "server not found")
		}
		sv, err = s.st.GetServer(ctx, cid)
	} else {
		sv, err = s.st.FirstServer(ctx)
	}
	switch {
	case errors.Is(err, store.ErrNotFound) && id != "":
		return nil, nil, status.Error(codes.NotFound, "server not found")
	case errors.Is(err, store.ErrNotFound):
		return nil, nil, s.noServer(ctx, "no enabled panel server")
	case err != nil:
		return nil, nil, status.Error(codes.Internal, "server lookup failed")
	}
	a, err := s.adapterFor(sv)
	if err != nil {
		return nil, nil, err
	}
	return sv, a, nil
}

// noServer explains why no server could be used: none is registered at all,
// or (msg) none qualifies.
func (s *Server) noServer(ctx context.Context, msg string) error {
	if n, err := s.st.CountServers(ctx); err == nil && n == 0 {
		return status.Error(codes.FailedPrecondition, "no panel server: add one in the dashboard (Servers)")
	}
	return status.Error(codes.FailedPrecondition, msg)
}

// subscription resolves a provisioned subscription and its panel. cm is nil
// (with no error) when the subscription was never provisioned, or its
// delivery has not finished (a reservation).
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
	if cm.Pending {
		return nil, nil, nil, nil
	}
	sv, a, err := s.server(ctx, cm.ServerID)
	if err != nil {
		return nil, nil, nil, err
	}
	return cm, sv, a, nil
}

// attachableInbounds lists the inbounds a new client of the server gets. By
// default: enabled in the panel and not detached in BOBRES. When the plan
// chose inbounds on this server (chosen non-empty), exactly those that are
// enabled in the panel: an explicit choice wins over the detach switch (which
// only sets the default for plans that choose nothing). A choice of which none
// is enabled any more is a FailedPrecondition naming the server, not a
// silent fallback to the default: the plan would hand out other inbounds than
// the ones its owner picked.
func (s *Server) attachableInbounds(ctx context.Context, sv *store.Server, a adapter.PanelAdapter, chosen []int32) ([]int, error) {
	ins, err := a.Inbounds(ctx)
	if err != nil {
		return nil, s.panelError("panel inbounds", err)
	}
	if len(chosen) > 0 {
		out := make([]int, 0, len(chosen))
		for _, in := range ins {
			if in.Enabled && slices.Contains(chosen, int32(in.ID)) { //nolint:gosec // panel ids are small
				out = append(out, in.ID)
			}
		}
		if len(out) == 0 {
			return nil, status.Errorf(codes.FailedPrecondition,
				"the inbound chosen for this plan no longer exists or is not enabled on server %q: edit the plan's inbounds", sv.Name)
		}
		if len(out) < len(chosen) {
			s.log.Warn("some of the inbounds chosen for a plan are gone from the panel", "server", sv.Name, "chosen", chosen, "attached", out)
		}
		return out, nil
	}
	attach, err := s.st.InboundAttach(ctx, sv.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, "inbound settings lookup failed")
	}
	out := make([]int, 0, len(ins))
	for _, in := range ins {
		if on, set := attach[in.ID]; in.Enabled && (on || !set) {
			out = append(out, in.ID)
		}
	}
	if len(out) == 0 {
		return nil, status.Error(codes.FailedPrecondition, "the panel has no enabled inbound to attach clients to")
	}
	return out, nil
}

// chosenInbounds is the request's inbound choice for a server (nil: none),
// each id once.
func chosenInbounds(sel []*provisionerv1.InboundSelection, serverID string) []int32 {
	var out []int32
	for _, in := range sel {
		if id, ok := canonicalID(in.GetServerId()); !ok || id != serverID {
			continue
		}
		for _, n := range in.GetInboundIds() {
			if !slices.Contains(out, n) {
				out = append(out, n)
			}
		}
	}
	return out
}

// noSuchClient reports the panel's answer to a lookup of a client it does
// not have. Marzban answers HTTP 404. 3x-ui: "record not found" inside a successful HTTP response, which is
// how 3x-ui reports business errors (the xui client says "client ... not
// found" for an empty record). Anything else says nothing about the client:
// unreachable, a server error, a refused token, a page that is not the
// panel's, or another refusal inside HTTP 200, such as "database is locked"
// from a busy panel.
func noSuchClient(err error) bool {
	if marzban.IsNotFound(err) {
		return true
	}
	var ae *xui.APIError
	if !errors.As(err, &ae) || ae.Status != http.StatusOK {
		return false
	}
	m := strings.ToLower(ae.Msg)
	return strings.Contains(m, "record not found") || (strings.HasPrefix(m, "client ") && strings.HasSuffix(m, " not found"))
}

// answered reports whether the panel answered a call. One that got no answer
// (our client timed out, the connection dropped) may still be running there,
// and finish after a lookup that follows it.
func answered(err error) bool {
	var ae *xui.APIError
	var me *marzban.APIError
	return errors.As(err, &ae) || errors.As(err, &me)
}

// CreateClient provisions a subscription on a panel. It is safe to retry: an
// already-mapped subscription is returned as is, the server is reserved
// before the panel is called (so a retry goes back to it), and a client left
// on the panel by an earlier attempt (whose response was lost) is adopted
// when it carries our derived subscription id.
func (s *Server) CreateClient(ctx context.Context, req *provisionerv1.CreateClientRequest) (*provisionerv1.Client, error) {
	email := req.GetEmail()
	if req.GetSubscriptionId() == "" || xui.ValidateIdentifier(email) != nil {
		return nil, status.Error(codes.InvalidArgument, "a valid email and subscription_id are required")
	}
	if req.GetDurationDays() < 0 || req.GetTrafficBytes() < 0 || req.GetDurationDays() > 36500 {
		return nil, status.Error(codes.InvalidArgument, "duration_days and traffic_bytes out of range")
	}
	for _, sel := range req.GetInbounds() {
		for _, id := range sel.GetInboundIds() {
			if id <= 0 {
				return nil, status.Error(codes.InvalidArgument, "an inbound id is a positive number")
			}
		}
	}
	fresh := false
	cm, err := s.st.GetClientMap(ctx, req.GetSubscriptionId())
	switch {
	case errors.Is(err, store.ErrNotFound):
		if cm, fresh, err = s.reserve(ctx, req); err != nil {
			return nil, err
		}
	case err != nil:
		return nil, status.Error(codes.Internal, "client map lookup failed")
	}
	if cm.Email != email {
		return nil, status.Error(codes.AlreadyExists, "subscription already provisioned with another email")
	}
	if !cm.Pending {
		return s.existingClient(ctx, cm)
	}
	return s.createOnPanel(ctx, req, cm, fresh)
}

// existingClient answers a retry after the response was lost: it reports the
// limits the panel holds (zeros would read as "never expires" in core).
func (s *Server) existingClient(ctx context.Context, cm *store.ClientMap) (*provisionerv1.Client, error) {
	_, a, err := s.server(ctx, cm.ServerID)
	if err != nil {
		return nil, err
	}
	st, err := a.Status(ctx, cm.Email)
	if err != nil {
		return nil, s.panelError("panel client", err)
	}
	return clientProto(cm, max(st.ExpiryMs, 0)/1000, st.TotalBytes), nil
}

// reserve picks the server for a new client and reserves it. fresh is false
// when another attempt reserved (or finished) first: its row is returned.
func (s *Server) reserve(ctx context.Context, req *provisionerv1.CreateClientRequest) (*store.ClientMap, bool, error) {
	sv, err := s.pick(ctx, req.GetServerId(), req.GetCandidateServerIds())
	if err != nil {
		return nil, false, err
	}
	if sv.Kind == store.KindMarzban && marzban.ValidateUsername(req.GetEmail()) != nil {
		return nil, false, status.Error(codes.InvalidArgument,
			"the email is not a valid Marzban username (3 to 32 of a-z, 0-9, - _ @ ., and at least 3 letters, digits or underscores before the first - . or @)")
	}
	subID := s.st.SubIDFor(req.GetSubscriptionId())
	cm, fresh, err := s.st.ReserveClient(ctx, &store.ClientMap{
		SubscriptionID: req.GetSubscriptionId(), ServerID: sv.ID, Email: req.GetEmail(), XUISubID: &subID,
	})
	switch {
	case errors.Is(err, store.ErrEmailTaken):
		return nil, false, status.Error(codes.AlreadyExists, "another subscription already uses this email on the server")
	case errors.Is(err, store.ErrNotFound):
		return nil, false, status.Error(codes.Aborted, "the server was removed meanwhile; retry")
	case err != nil:
		return nil, false, status.Error(codes.Internal, "client reservation failed")
	}
	return cm, fresh, nil
}

// pick chooses the server for a new client: the explicit one (refused when
// disabled), or the enabled, healthy candidate with the fewest clients per
// weight.
func (s *Server) pick(ctx context.Context, serverID string, candidates []string) (*store.Server, error) {
	if serverID != "" {
		sv, _, err := s.server(ctx, serverID)
		if err != nil {
			return nil, err
		}
		if !sv.Enabled {
			return nil, status.Error(codes.FailedPrecondition, "the server is disabled: it takes no new clients")
		}
		return sv, nil
	}
	ids := make([]string, 0, len(candidates))
	for _, c := range candidates {
		if id, ok := canonicalID(c); ok {
			c = id
		}
		ids = append(ids, c) // a malformed id stays, and matches nothing
	}
	sv, err := s.st.PickServer(ctx, ids)
	switch {
	case errors.Is(err, store.ErrNotFound) && len(ids) > 0:
		return nil, s.noServer(ctx, "none of the candidate servers is enabled and healthy")
	case errors.Is(err, store.ErrNotFound):
		return nil, s.noServer(ctx, "no enabled and healthy server")
	case err != nil:
		return nil, status.Error(codes.Internal, "server lookup failed")
	}
	return sv, nil
}

// createOnPanel creates (or adopts) the reserved client on its server and
// turns the reservation into the client's row. When it fails, the
// reservation stays unless no client of ours can be on that panel.
func (s *Server) createOnPanel(ctx context.Context, req *provisionerv1.CreateClientRequest, cm *store.ClientMap, fresh bool) (*provisionerv1.Client, error) {
	email, subID := cm.Email, s.st.SubIDFor(cm.SubscriptionID)
	// The reservation's server even when disabled since: an earlier attempt
	// may have created the client there.
	sv, a, err := s.server(ctx, cm.ServerID)
	if err != nil {
		if fresh {
			s.release(ctx, cm)
		}
		return nil, err
	}
	inboundIDs, err := s.attachableInbounds(ctx, sv, a, chosenInbounds(req.GetInbounds(), sv.ID))
	if err != nil {
		if fresh {
			s.release(ctx, cm)
		} else {
			s.releaseIfAbsent(ctx, a, cm, subID)
		}
		return nil, err
	}
	var expiryMs int64
	if d := req.GetDurationDays(); d > 0 {
		expiryMs = time.Now().Add(time.Duration(d) * 24 * time.Hour).UnixMilli()
	}
	if err := a.CreateClient(ctx, email, subID, expiryMs, req.GetTrafficBytes(), inboundIDs); err != nil {
		existing, lookupErr := a.ClientSubID(ctx, email)
		switch {
		case noSuchClient(lookupErr) && answered(err):
			s.release(ctx, cm)
			return nil, s.panelError("panel create", err)
		case noSuchClient(lookupErr):
			// The create may still land: the retry comes back here and adopts it.
			return nil, s.panelError("panel create", err)
		case lookupErr != nil:
			s.log.Warn("panel create failed", "err", err)
			return nil, s.panelError("panel create", lookupErr)
		case existing != subID:
			s.release(ctx, cm) // the email is someone else's there, so none of ours is
			return nil, status.Error(codes.AlreadyExists, "another client on the panel already uses this email")
		}
		// Ours, from an earlier attempt: adopt it with the limits it has, and in the
		// bot group if that attempt (made before BOBRES grouped its clients) left it in none.
		if st, err := a.Status(ctx, email); err == nil {
			expiryMs = max(st.ExpiryMs, 0)
		}
		s.ensureTagged(ctx, a, email)
	} else if !fresh {
		// A retry. An earlier attempt may have made the client before BOBRES grouped
		// its clients, and a 3x-ui panel takes a create of a client it has (same
		// subscription id, on the inbounds it is on) as a success that changes
		// nothing, its group included.
		s.ensureTagged(ctx, a, email)
	}
	done := &store.ClientMap{
		SubscriptionID: cm.SubscriptionID, ServerID: cm.ServerID, Email: email,
		XUISubID: &subID, InboundIDs: toInt32s(inboundIDs),
	}
	if err := s.st.PutClientMap(ctx, done); err != nil {
		return nil, status.Error(codes.Internal, "client map persist failed")
	}
	return clientProto(done, expiryMs/1000, req.GetTrafficBytes()), nil
}

// release drops a reservation so a retry picks a server again.
func (s *Server) release(ctx context.Context, cm *store.ClientMap) {
	if err := s.st.ReleaseReservation(ctx, cm.SubscriptionID); err != nil {
		s.log.Warn("releasing a client reservation failed", "err", err)
	}
}

// releaseIfAbsent drops a reservation from an earlier attempt when the panel
// says it has no client of ours under the email.
func (s *Server) releaseIfAbsent(ctx context.Context, a adapter.PanelAdapter, cm *store.ClientMap, subID string) {
	existing, err := a.ClientSubID(ctx, cm.Email)
	if noSuchClient(err) || (err == nil && existing != subID) {
		s.release(ctx, cm)
	}
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

// SetClientEnabled turns a provisioned client off or on; its limits stay.
func (s *Server) SetClientEnabled(ctx context.Context, req *provisionerv1.SetClientEnabledRequest) (*provisionerv1.Client, error) {
	cm, _, a, err := s.subscription(ctx, req.GetSubscriptionId())
	if err != nil {
		return nil, err
	}
	if cm == nil {
		return nil, status.Error(codes.NotFound, "client not provisioned")
	}
	if err := a.SetEnabled(ctx, cm.Email, req.GetEnabled()); err != nil {
		return nil, s.panelError("panel set enabled", err)
	}
	return clientProto(cm, 0, 0), nil
}

// DeleteClient removes the client from the panel and the map (idempotent).
// A reservation (a delivery that never finished) goes too, with the client
// if an earlier attempt created one. A client the panel no longer has, or
// whose email is someone else's there now, only leaves the map.
func (s *Server) DeleteClient(ctx context.Context, req *provisionerv1.DeleteClientRequest) (*emptypb.Empty, error) {
	if req.GetSubscriptionId() == "" {
		return nil, status.Error(codes.InvalidArgument, "subscription_id required")
	}
	cm, err := s.st.GetClientMap(ctx, req.GetSubscriptionId())
	if errors.Is(err, store.ErrNotFound) {
		return &emptypb.Empty{}, nil
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "client map lookup failed")
	}
	_, a, err := s.server(ctx, cm.ServerID)
	if err != nil {
		return nil, err
	}
	if err := a.Delete(ctx, cm.Email); err != nil {
		existing, lookupErr := a.ClientSubID(ctx, cm.Email)
		gone := noSuchClient(lookupErr) ||
			(lookupErr == nil && cm.XUISubID != nil && *cm.XUISubID != "" && existing != *cm.XUISubID)
		if !gone {
			return nil, s.panelError("panel delete", err)
		}
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
		TrafficTotalBytes: st.TotalBytes, ExpiresAt: st.ExpiryMs / 1000, Enabled: st.Enabled,
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
	linker, ownsSub := a.(adapter.SubscriptionLinker)
	if !ownsSub && cm.XUISubID != nil && *cm.XUISubID != "" {
		configs, _ = a.SubLinks(ctx, *cm.XUISubID)
	}
	if len(configs) == 0 {
		configs, _ = a.Links(ctx, cm.Email)
	}
	link := ""
	switch {
	case ownsSub:
		// The panel owns the subscription token: ask it, and let the
		// server's base URL replace only the prefix.
		l, lerr := linker.SubscriptionLink(ctx, cm.Email, sv.SubBaseURL)
		if lerr == nil {
			link = l
		} else {
			err = lerr
		}
	case sv.SubBaseURL != "" && cm.XUISubID != nil:
		link = strings.TrimRight(sv.SubBaseURL, "/") + "/" + *cm.XUISubID
	}
	if link == "" && len(configs) > 0 {
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

func toInt32s(in []int) []int32 {
	out := make([]int32, 0, len(in))
	for _, v := range in {
		if v >= 0 && v <= math.MaxInt32 {
			out = append(out, int32(v)) //nolint:gosec // range checked
		}
	}
	return out
}
