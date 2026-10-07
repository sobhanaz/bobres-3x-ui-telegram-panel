// Package server exposes the core domain over gRPC (CoreService).
package server

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"sort"

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
	st   *store.Store
	dom  *domain.Service
	pay  domain.PaymentsClient
	prov domain.Provisioner
	// publicURL is the dashboard's origin, for login links.
	publicURL string
}

// SetPublicURL sets the dashboard origin login links point to.
func (s *Server) SetPublicURL(u string) { s.publicURL = u }

// CreateDashboardLink makes a one-time dashboard login link for a staff member.
func (s *Server) CreateDashboardLink(ctx context.Context, req *corev1.CreateDashboardLinkRequest) (*corev1.DashboardLink, error) {
	if s.publicURL == "" {
		return &corev1.DashboardLink{}, nil
	}
	token, exp, err := s.dom.CreateLoginLink(ctx, req.GetActorTelegramId())
	if err != nil {
		return nil, fail(err)
	}
	return &corev1.DashboardLink{Url: DashboardLoginURL(s.publicURL, token), ExpiresAt: exp.Unix()}, nil
}

// DashboardLoginURL is the link a login token is sent as. The token is in the
// fragment, so it never reaches server logs or a Referer header.
func DashboardLoginURL(publicURL, token string) string {
	return publicURL + "/admin/login#t=" + token
}

// New builds the handler set.
func New(st *store.Store, dom *domain.Service) *Server {
	return &Server{st: st, dom: dom}
}

// Register attaches the service to a grpc.Server.
func (s *Server) Register(g *grpc.Server) { corev1.RegisterCoreServiceServer(g, s) }

// fail maps domain errors to gRPC codes the bot can act on. Unknown errors
// become Internal with a generic message (details stay in logs).
func fail(err error) error {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, domain.ErrInvalid):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, domain.ErrForbidden), errors.Is(err, domain.ErrUserBanned):
		return status.Error(codes.PermissionDenied, err.Error())
	case errors.Is(err, domain.ErrTrialAlreadyUsed),
		errors.Is(err, domain.ErrOrderNotPayable),
		errors.Is(err, domain.ErrPlanUnavailable),
		errors.Is(err, domain.ErrNotExtendable),
		errors.Is(err, domain.ErrDiscount),
		errors.Is(err, store.ErrInsufficientFunds):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, store.ErrIdempotencyConflict):
		return status.Error(codes.AlreadyExists, err.Error())
	case errors.Is(err, domain.ErrNotReady):
		return status.Error(codes.FailedPrecondition, err.Error())
	}
	// Errors from a peer service (payments, provisioner) keep their code and
	// original message when they are meaningful to the caller.
	var peer interface{ GRPCStatus() *status.Status }
	if errors.As(err, &peer) && peer.GRPCStatus() != nil {
		st := peer.GRPCStatus()
		switch st.Code() {
		case codes.InvalidArgument, codes.NotFound, codes.AlreadyExists,
			codes.FailedPrecondition, codes.PermissionDenied:
			return status.Error(st.Code(), st.Message())
		case codes.Unavailable, codes.DeadlineExceeded:
			return status.Error(codes.Unavailable, "a backend service is unavailable, try again")
		}
	}
	return status.Error(codes.Internal, "internal error")
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
	u, err := s.dom.UpsertUser(ctx, domain.UpsertUserParams{
		TelegramID: req.GetTelegramId(), Username: req.GetUsername(),
		Language: req.GetLanguage(), ReferredBy: req.GetReferredBy(), ReferralCode: req.GetReferralCode(),
	})
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
	for i := range plans {
		out.Plans = append(out.Plans, planToProto(&plans[i]))
	}
	return out, nil
}

func planToProto(p *store.Plan) *corev1.Plan {
	pp := &corev1.Plan{
		Id:       p.ID,
		NameI18N: p.NameI18n,
		Kind:     p.Kind,
		Price:    &commonv1.Money{Amount: p.Price, Currency: p.Currency},
		Enabled:  p.Enabled,
		IsTrial:  p.IsTrial,
		Sort:     p.Sort,
		IsTopup:  p.IsTopup,
	}
	if p.DurationDays != nil {
		pp.DurationDays = *p.DurationDays
	}
	if p.TrafficBytes != nil {
		pp.TrafficBytes = *p.TrafficBytes
	}
	return pp
}

// CreateOrder inserts an order (idempotent).
func (s *Server) CreateOrder(ctx context.Context, req *corev1.CreateOrderRequest) (*corev1.Order, error) {
	o, err := s.dom.CreateOrder(ctx, domain.CreateOrderParams{
		UserID:         req.GetUserId(),
		PlanID:         req.GetPlanId(),
		Type:           req.GetType(),
		IdempotencyKey: req.GetIdempotencyKey(),
		SubscriptionID: req.GetSubscriptionId(),
		DiscountCode:   req.GetDiscountCode(),
	})
	if err != nil {
		return nil, fail(err)
	}
	return orderToProto(o), nil
}

func orderToProto(o *store.Order) *corev1.Order {
	out := &corev1.Order{
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
	if o.SubscriptionID != nil {
		out.SubscriptionId = *o.SubscriptionID
	}
	if o.DiscountCode != nil {
		out.DiscountCode = *o.DiscountCode
		out.Discount = &commonv1.Money{Amount: o.DiscountAmount, Currency: o.Currency}
	}
	return out
}

// QuoteOrder prices an order before it is created (discount codes).
func (s *Server) QuoteOrder(ctx context.Context, req *corev1.QuoteOrderRequest) (*corev1.Quote, error) {
	q, err := s.dom.QuoteOrder(ctx, domain.QuoteParams{
		UserID: req.GetUserId(), PlanID: req.GetPlanId(), Type: req.GetType(),
		SubscriptionID: req.GetSubscriptionId(), DiscountCode: req.GetDiscountCode(),
	})
	if err != nil {
		return nil, fail(err)
	}
	return &corev1.Quote{
		ListPrice:    &commonv1.Money{Amount: q.ListPrice, Currency: q.Currency},
		Discount:     &commonv1.Money{Amount: q.Discount, Currency: q.Currency},
		Total:        &commonv1.Money{Amount: q.Total, Currency: q.Currency},
		DiscountCode: q.DiscountCode,
	}, nil
}

// GetReferralInfo returns the user's invite code and what it earned.
func (s *Server) GetReferralInfo(ctx context.Context, req *corev1.GetReferralInfoRequest) (*corev1.ReferralInfo, error) {
	r, err := s.dom.Referral(ctx, req.GetUserId())
	if err != nil {
		return nil, fail(err)
	}
	out := &corev1.ReferralInfo{Code: r.Code, Invited: int32(min(r.Invited, math.MaxInt32)), //nolint:gosec // bounded
		Rewarded: int32(min(r.Rewarded, math.MaxInt32)), RewardPercent: int32(r.RewardPercent)} //nolint:gosec // a percentage
	currencies := make([]string, 0, len(r.Earned))
	for c := range r.Earned {
		currencies = append(currencies, c)
	}
	sort.Strings(currencies)
	for _, c := range currencies {
		out.Earned = append(out.Earned, &commonv1.Money{Amount: r.Earned[c], Currency: c})
	}
	return out, nil
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

// ListLedgerEntries returns a user's ledger, newest first.
func (s *Server) ListLedgerEntries(ctx context.Context, req *corev1.ListLedgerEntriesRequest) (*corev1.ListLedgerEntriesResponse, error) {
	page, size := req.GetPagination().GetPage(), req.GetPagination().GetPageSize()
	if size <= 0 || size > 100 {
		size = 20
	}
	if page < 1 {
		page = 1
	}
	entries, err := s.st.ListLedger(ctx, s.st.Conn(), req.GetUserId(), int(size), int((page-1)*size))
	if err != nil {
		return nil, fail(err)
	}
	out := &corev1.ListLedgerEntriesResponse{PageInfo: &commonv1.PageInfo{Page: page, PageSize: size}}
	for _, e := range entries {
		le := &corev1.LedgerEntry{
			Id: e.ID, UserId: e.UserID, Currency: e.Currency, Amount: e.Amount, Kind: e.Kind,
			IdempotencyKey: e.IdempotencyKey, BalanceAfter: e.BalanceAfter, CreatedAt: e.CreatedAt.Unix(),
		}
		if e.RefType != nil {
			le.RefType = *e.RefType
		}
		if e.RefID != nil {
			le.RefId = *e.RefID
		}
		out.Entries = append(out.Entries, le)
	}
	return out, nil
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
		sp.SubscriptionLink = sc.SubLink
		if sc.LastSyncedAt != nil {
			sp.LastSyncedAt = sc.LastSyncedAt.Unix()
		}
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
