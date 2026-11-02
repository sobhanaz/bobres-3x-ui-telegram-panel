package server

import (
	"context"
	"errors"
	"sync"
	"time"

	provisionerv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/provisioner/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/provisioner/adapter"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/provisioner/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/xui"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Config names chosen by customers (docs/03-bot-ux.md, "Config names"): core
// checks a name with CheckClientName before it takes an order or renames, and
// renames with RenameClient. The panel's own refusal of a name it has stays the
// last safety net at creation (CreateClient answers AlreadyExists).

// The reasons of CheckClientNameResponse.
const (
	NameTaken     = "taken"
	NameUnchecked = "unchecked"
)

// maxClientName bounds a name asked about: core's names are far shorter.
const maxClientName = 64

// nameCheckTimeout bounds one server's answer to a name check: a panel that
// does not answer makes the check "unchecked", and the customer is not kept
// waiting for long.
const nameCheckTimeout = 8 * time.Second

// renamesClients reports whether the clients of a kind of panel can be renamed
// and keep their subscription link: the kinds whose adapter is an
// adapter.ClientRenamer (a test keeps the two in line).
func renamesClients(kind string) bool {
	return kind == store.KindXUI || kind == "" || kind == store.KindHiddify
}

// validClientName is the shape every panel kind takes as a client name.
func validClientName(name string) bool {
	return name != "" && len(name) <= maxClientName && xui.ValidateIdentifier(name) == nil
}

// CheckClientName says whether a name is free: no client the provisioner holds
// has it (a reservation included), and every enabled server, healthy or not,
// answered that it has no client of that name. A server that does not answer
// (or whose token does not decrypt) makes the answer "unchecked": a name is
// never reported free without every server's word.
func (s *Server) CheckClientName(ctx context.Context, req *provisionerv1.CheckClientNameRequest) (*provisionerv1.CheckClientNameResponse, error) {
	name := req.GetName()
	if !validClientName(name) {
		return nil, status.Error(codes.InvalidArgument, "name is not a valid client name")
	}
	used, err := s.st.EmailInUse(ctx, name, "")
	if err != nil {
		return nil, status.Error(codes.Internal, "client map lookup failed")
	}
	if used {
		return &provisionerv1.CheckClientNameResponse{Reason: NameTaken}, nil
	}
	infos, err := s.st.ListServers(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "server lookup failed")
	}
	// The servers are read one by one (each read takes a connection), then
	// asked at the same time.
	var panels []adapter.PanelAdapter
	unchecked := false
	for _, in := range infos {
		if !in.Enabled {
			continue
		}
		sv, err := s.st.GetServer(ctx, in.ID)
		if errors.Is(err, store.ErrNotFound) {
			continue // removed meanwhile
		}
		if err != nil {
			s.log.Warn("a server could not be read for a name check", "server", in.Name, "err", err)
			unchecked = true
			continue
		}
		a, err := s.adapterFor(sv)
		if err != nil {
			unchecked = true
			continue
		}
		panels = append(panels, a)
	}
	var (
		mu    sync.Mutex
		taken bool
		wg    sync.WaitGroup
	)
	for _, a := range panels {
		wg.Add(1)
		go func(a adapter.PanelAdapter) {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, nameCheckTimeout)
			defer cancel()
			_, err := a.ClientSubID(cctx, name)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				taken = true
			case !noSuchClient(err):
				s.log.Warn("a panel did not answer a name check", "err", err)
				unchecked = true
			}
		}(a)
	}
	wg.Wait()
	switch {
	case taken:
		return &provisionerv1.CheckClientNameResponse{Reason: NameTaken}, nil
	case unchecked:
		return &provisionerv1.CheckClientNameResponse{Reason: NameUnchecked}, nil
	}
	return &provisionerv1.CheckClientNameResponse{Available: true}, nil
}

// RenameClient gives a provisioned client a new name on its panel and in the
// client map. The subscription link stays (3x-ui keys it by the subscription
// id, Hiddify by the user's UUID); a server whose panel cannot keep it is
// refused. A retry after a lost answer finds the panel renamed already and only
// records it.
func (s *Server) RenameClient(ctx context.Context, req *provisionerv1.RenameClientRequest) (*provisionerv1.Client, error) {
	name := req.GetNewName()
	if !validClientName(name) {
		return nil, status.Error(codes.InvalidArgument, "new_name is not a valid client name")
	}
	cm, sv, a, err := s.subscription(ctx, req.GetSubscriptionId())
	if err != nil {
		return nil, err
	}
	if cm == nil {
		return nil, status.Error(codes.NotFound, "client not provisioned")
	}
	ren, ok := a.(adapter.ClientRenamer)
	if !ok || !renamesClients(sv.Kind) {
		return nil, status.Error(codes.FailedPrecondition, "this server's panel cannot rename a client and keep its subscription link")
	}
	if cm.Email == name {
		return clientProto(cm, 0, 0), nil
	}
	used, err := s.st.EmailInUse(ctx, name, cm.SubscriptionID)
	if err != nil {
		return nil, status.Error(codes.Internal, "client map lookup failed")
	}
	if used {
		return nil, status.Error(codes.AlreadyExists, "another client already uses this name")
	}
	subID := s.st.SubIDFor(cm.SubscriptionID)
	if cm.XUISubID != nil && *cm.XUISubID != "" {
		subID = *cm.XUISubID
	}
	existing, lookupErr := a.ClientSubID(ctx, name)
	switch {
	case lookupErr == nil && existing == subID:
		// An earlier attempt renamed it there and its answer was lost: record it.
	case lookupErr == nil:
		return nil, status.Error(codes.AlreadyExists, "another client on the panel already uses this name")
	case !noSuchClient(lookupErr):
		return nil, s.panelError("panel lookup", lookupErr)
	default:
		if err := ren.RenameClient(ctx, cm.Email, name); err != nil {
			if _, again := a.ClientSubID(ctx, name); again == nil && answered(err) {
				return nil, status.Error(codes.AlreadyExists, "another client on the panel already uses this name")
			}
			return nil, s.panelError("panel rename", err)
		}
	}
	if err := s.st.RenameClientMap(ctx, cm.SubscriptionID, name); err != nil {
		// The panel has the new name and the map the old one: put the panel back, so
		// the two agree and the caller may try again.
		if back := ren.RenameClient(ctx, name, cm.Email); back != nil {
			s.log.Warn("a client renamed on its panel could not be recorded nor renamed back", "subscription_id", cm.SubscriptionID, "err", back)
		}
		if errors.Is(err, store.ErrEmailTaken) {
			return nil, status.Error(codes.AlreadyExists, "another client already uses this name")
		}
		return nil, status.Error(codes.Internal, "client map persist failed")
	}
	cm.Email = name
	return clientProto(cm, 0, 0), nil
}
