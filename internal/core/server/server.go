// Package server exposes the core domain over gRPC (CoreService).
package server

import (
	"context"
	"encoding/json"
	"errors"

	commonv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/common/v1"
	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Server implements corev1.CoreServiceServer on top of domain+store.
type Server struct {
	corev1.UnimplementedCoreServiceServer
	st  *store.Store
	dom *domain.Service
	pay domain.PaymentsClient
}

// New builds the handler set.
func New(st *store.Store, dom *domain.Service) *Server {
	return &Server{st: st, dom: dom}
}

// Register attaches the service to a grpc.Server.
func (s *Server) Register(g *grpc.Server) { corev1.RegisterCoreServiceServer(g, s) }

func fail(err error) error {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, domain.ErrTrialAlreadyUsed),
		errors.Is(err, domain.ErrOrderNotPayable),
		errors.Is(err, store.ErrInsufficientFunds):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, store.ErrDuplicateIdempotency):
		return status.Error(codes.AlreadyExists, err.Error())
	default:
		return status.Error(codes.Internal, "internal error")
	}
}

func userToProto(u *store.User) *corev1.User {
	out := &corev1.User{
		Id:         u.ID,
		TelegramId: u.TelegramID,
		Username:   u.Username,
		Language:   u.Language,
		Role:       u.Role,
		Status:     u.Status,
		CreatedAt:  u.CreatedAt.Unix(),
		UpdatedAt:  u.UpdatedAt.Unix(),
	}
	if u.ReferredBy != nil {
		out.ReferredBy = *u.ReferredBy
	}
	return out
}

// UpsertUser creates or updates a user by telegram id.
func (s *Server) UpsertUser(ctx context.Context, req *corev1.UpsertUserRequest) (*corev1.User, error) {
	u, err := s.st.UpsertUser(ctx, s.st.Conn(), req.GetTelegramId(), req.GetUsername(), req.GetLanguage(), req.GetReferredBy())
	if err != nil {
		return nil, fail(err)
	}
	return userToProto(u), nil
}

// GetUser looks up by id or telegram id.
func (s *Server) GetUser(ctx context.Context, req *corev1.GetUserRequest) (*corev1.User, error) {
	var (
		u   *store.User
		err error
	)
	switch l := req.GetLookup().(type) {
	case *corev1.GetUserRequest_Id:
		u, err = s.st.GetUser(ctx, s.st.Conn(), l.Id)
	case *corev1.GetUserRequest_TelegramId:
		u, err = s.st.GetUserByTelegramID(ctx, s.st.Conn(), l.TelegramId)
	default:
		return nil, status.Error(codes.InvalidArgument, "lookup required")
	}
	if err != nil {
		return nil, fail(err)
	}
	return userToProto(u), nil
}

// ListPlans returns the plan catalog.
func (s *Server) ListPlans(ctx context.Context, req *corev1.ListPlansRequest) (*corev1.ListPlansResponse, error) {
	plans, err := s.st.ListPlans(ctx, s.st.Conn(), req.GetIncludeDisabled())
	if err != nil {
		return nil, fail(err)
	}
	out := &corev1.ListPlansResponse{}
	for _, p := range plans {
		pp := &corev1.Plan{
			Id:       p.ID,
			NameI18N: p.NameI18n,
			Kind:     p.Kind,
			Price:    &commonv1.Money{Amount: p.Price, Currency: p.Currency},
			Enabled:  p.Enabled,
			IsTrial:  p.IsTrial,
			Sort:     p.Sort,
		}
		if p.DurationDays != nil {
			pp.DurationDays = *p.DurationDays
		}
		if p.TrafficBytes != nil {
			pp.TrafficBytes = *p.TrafficBytes
		}
		out.Plans = append(out.Plans, pp)
	}
	return out, nil
}

// CreateOrder inserts an order (idempotent).
func (s *Server) CreateOrder(ctx context.Context, req *corev1.CreateOrderRequest) (*corev1.Order, error) {
	o, err := s.dom.CreateOrder(ctx, domain.CreateOrderParams{
		UserID:         req.GetUserId(),
		PlanID:         req.GetPlanId(),
		Type:           req.GetType(),
		IdempotencyKey: req.GetIdempotencyKey(),
	})
	if err != nil {
		return nil, fail(err)
	}
	return orderToProto(o), nil
}

func orderToProto(o *store.Order) *corev1.Order {
	return &corev1.Order{
		Id:             o.ID,
		UserId:         o.UserID,
		PlanId:         o.PlanID,
		Type:           o.Type,
		Status:         o.Status,
		Amount:         &commonv1.Money{Amount: o.Amount, Currency: o.Currency},
		IdempotencyKey: o.IdempotencyKey,
		CreatedAt:      o.CreatedAt.Unix(),
		UpdatedAt:      o.UpdatedAt.Unix(),
	}
}

// GetOrder returns one order.
func (s *Server) GetOrder(ctx context.Context, req *corev1.GetOrderRequest) (*corev1.Order, error) {
	o, err := s.st.GetOrder(ctx, s.st.Conn(), req.GetId())
	if err != nil {
		return nil, fail(err)
	}
	return orderToProto(o), nil
}

// GetWallet returns the user's balance for a currency.
func (s *Server) GetWallet(ctx context.Context, req *corev1.GetWalletRequest) (*corev1.Wallet, error) {
	w, err := s.st.GetWallet(ctx, s.st.Conn(), req.GetUserId(), req.GetCurrency())
	if err != nil {
		return nil, fail(err)
	}
	return &corev1.Wallet{
		UserId:    w.UserID,
		Currency:  w.Currency,
		Balance:   w.Balance,
		UpdatedAt: w.UpdatedAt.Unix(),
	}, nil
}

// ListSubscriptions returns the user's subscriptions.
func (s *Server) ListSubscriptions(ctx context.Context, req *corev1.ListSubscriptionsRequest) (*corev1.ListSubscriptionsResponse, error) {
	subs, err := s.st.ListSubscriptions(ctx, s.st.Conn(), req.GetUserId(), 100)
	if err != nil {
		return nil, fail(err)
	}
	out := &corev1.ListSubscriptionsResponse{}
	for i := range subs {
		sc := subs[i]
		sp := &corev1.Subscription{
			Id:          sc.ID,
			UserId:      sc.UserID,
			OrderId:     sc.OrderID,
			ServerId:    sc.ServerID,
			ClientEmail: sc.ClientEmail,
			Status:      sc.Status,
		}
		if sc.SubID != nil {
			sp.SubId = *sc.SubID
		}
		if sc.ExpiresAt != nil {
			sp.ExpiresAt = sc.ExpiresAt.Unix()
		}
		if sc.TrafficTotal != nil {
			sp.TrafficTotalBytes = *sc.TrafficTotal
		}
		sp.TrafficUsedBytes = sc.TrafficUsed
		out.Subscriptions = append(out.Subscriptions, sp)
	}
	return out, nil
}

// CreateSupportTicket stores a minimal support ticket.
func (s *Server) CreateSupportTicket(ctx context.Context, req *corev1.CreateSupportTicketRequest) (*corev1.Ticket, error) {
	t, err := s.st.CreateTicket(ctx, s.st.Conn(), &store.Ticket{
		UserID:   req.GetUserId(),
		Category: req.GetCategory(),
		Text:     req.GetText(),
	})
	if err != nil {
		return nil, fail(err)
	}
	return &corev1.Ticket{
		Id:        t.ID,
		UserId:    t.UserID,
		Category:  t.Category,
		Text:      t.Text,
		Status:    t.Status,
		CreatedAt: t.CreatedAt.Unix(),
	}, nil
}

// GetSettings returns flattened settings (key -> raw JSON string).
func (s *Server) GetSettings(ctx context.Context, _ *corev1.GetSettingsRequest) (*corev1.Settings, error) {
	m, err := s.st.GetSettings(ctx, s.st.Conn())
	if err != nil {
		return nil, fail(err)
	}
	out := &corev1.Settings{Values: map[string]string{}}
	for k, v := range m {
		out.Values[k] = string(v)
	}
	return out, nil
}

// UpdateBranding upserts branding settings with an audit row.
func (s *Server) UpdateBranding(ctx context.Context, req *corev1.UpdateBrandingRequest) (*corev1.Settings, error) {
	for k, v := range req.GetValues() {
		raw := json.RawMessage(v)
		if !json.Valid(raw) {
			raw, _ = json.Marshal(v) // treat as plain string
		}
		if err := s.st.SetSetting(ctx, s.st.Conn(), "branding."+k, raw); err != nil {
			return nil, fail(err)
		}
	}
	actor := req.GetActorId()
	reason := req.GetReason()
	payload, _ := json.Marshal(req.GetValues())
	if err := s.st.WriteAudit(ctx, s.st.Conn(), &store.Audit{
		ActorID: optstr(actor),
		Action:  "branding.update",
		Entity:  "settings",
		After:   payload,
		Reason:  optstr(reason),
	}); err != nil {
		return nil, fail(err)
	}
	return s.GetSettings(ctx, &corev1.GetSettingsRequest{})
}

func optstr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
