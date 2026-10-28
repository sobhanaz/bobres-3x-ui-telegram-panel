// Package server exposes the core domain over gRPC (CoreService).
package server

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strconv"
	"strings"

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
	prov Provisioner
	// publicURL is the dashboard's origin now, for login links and ticket
	// links (nil: none).
	publicURL func(context.Context) string
}

// fromBot marks the changes a request makes as made in the bot (the only
// peer that calls core), for the audit log.
func fromBot(ctx context.Context) context.Context { return store.WithSource(ctx, store.SourceBot) }

// ListStaffContacts lists the active owners and admins, for staff alerts.
func (s *Server) ListStaffContacts(ctx context.Context, req *corev1.ListStaffContactsRequest) (*corev1.ListStaffContactsResponse, error) {
	users, err := s.st.ListStaffContacts(ctx, s.st.Conn(), req.GetIncludeSupport())
	if err != nil {
		return nil, fail(err)
	}
	out := &corev1.ListStaffContactsResponse{}
	for _, u := range users {
		out.Items = append(out.Items, &corev1.StaffContact{TelegramId: u.TelegramID, Language: u.Language, Role: u.Role})
	}
	return out, nil
}

// SetPublicURL sets a fixed dashboard origin for the links.
func (s *Server) SetPublicURL(u string) {
	s.publicURL = func(context.Context) string { return u }
}

// SetPublicURLFunc sets where the dashboard origin comes from when a link is
// made: the HTTPS manager (domain.TLS.PublicURL), whose answer follows the
// HTTPS mode.
func (s *Server) SetPublicURLFunc(f func(context.Context) string) { s.publicURL = f }

// dashboardURL is the dashboard's origin now ("" when there is none).
func (s *Server) dashboardURL(ctx context.Context) string {
	if s.publicURL == nil {
		return ""
	}
	return s.publicURL(ctx)
}

// CreateDashboardLink makes a one-time dashboard login link for a staff member.
func (s *Server) CreateDashboardLink(ctx context.Context, req *corev1.CreateDashboardLinkRequest) (*corev1.DashboardLink, error) {
	ctx = fromBot(ctx)
	origin := s.dashboardURL(ctx)
	if origin == "" {
		return &corev1.DashboardLink{}, nil
	}
	token, exp, err := s.dom.CreateLoginLink(ctx, req.GetActorTelegramId())
	if err != nil {
		return nil, fail(err)
	}
	return &corev1.DashboardLink{Url: DashboardLoginURL(origin, token), ExpiresAt: exp.Unix()}, nil
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
	case domain.IsPurchaseRefusal(err):
		// A new purchase refused by a license gate (the active-services
		// limit, or a reseller after resellers left the entitlements): the
		// bot's customer is told only that purchases are unavailable, never
		// why.
		return status.Error(codes.FailedPrecondition, domain.PurchasesPaused)
	case errors.Is(err, domain.ErrTrialAlreadyUsed),
		errors.Is(err, domain.ErrOrderNotPayable),
		errors.Is(err, domain.ErrPlanUnavailable),
		errors.Is(err, domain.ErrNotExtendable),
		errors.Is(err, domain.ErrDiscount),
		errors.Is(err, store.ErrInsufficientFunds):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, store.ErrIdempotencyConflict):
		return status.Error(codes.AlreadyExists, err.Error())
	case errors.Is(err, domain.ErrNotReady), errors.Is(err, domain.ErrState):
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
		BotBlocked: u.BotBlockedAt != nil,
	}
	if u.ReferredBy != nil {
		out.ReferredBy = *u.ReferredBy
	}
	return out
}

// UpsertUser creates or updates a user by telegram id.
func (s *Server) UpsertUser(ctx context.Context, req *corev1.UpsertUserRequest) (*corev1.User, error) {
	ctx = fromBot(ctx)
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

// ListPlans returns the plan catalog. With a user, each plan's price is what
// that user pays (a reseller's tier price) and list_price the store price.
func (s *Server) ListPlans(ctx context.Context, req *corev1.ListPlansRequest) (*corev1.ListPlansResponse, error) {
	plans, err := s.st.ListPlans(ctx, s.st.Conn(), req.GetIncludeDisabled())
	if err != nil {
		return nil, fail(err)
	}
	var prices []int64
	if req.GetUserId() != "" {
		if prices, err = s.dom.PlanPrices(ctx, req.GetUserId(), plans); err != nil {
			return nil, fail(err)
		}
	}
	groups, err := s.st.ListPlanGroups(ctx, s.st.Conn())
	if err != nil {
		return nil, fail(err)
	}
	out := &corev1.ListPlansResponse{}
	for i := range plans {
		pp := planToProto(&plans[i])
		if prices != nil {
			pp.Price = &commonv1.Money{Amount: prices[i], Currency: plans[i].Currency}
		}
		out.Plans = append(out.Plans, pp)
	}
	for i := range groups {
		out.Groups = append(out.Groups, &corev1.PlanGroup{Id: groups[i].ID, NameI18N: groups[i].NameI18n,
			DescriptionI18N: groups[i].DescriptionI18n, Sort: groups[i].Sort})
	}
	return out, nil
}

func planToProto(p *store.Plan) *corev1.Plan {
	pp := &corev1.Plan{
		Id:        p.ID,
		NameI18N:  p.NameI18n,
		Kind:      p.Kind,
		Price:     &commonv1.Money{Amount: p.Price, Currency: p.Currency},
		ListPrice: &commonv1.Money{Amount: p.Price, Currency: p.Currency},
		Enabled:   p.Enabled,
		IsTrial:   p.IsTrial,
		Sort:      p.Sort,
		IsTopup:   p.IsTopup,
	}
	if p.DurationDays != nil {
		pp.DurationDays = *p.DurationDays
	}
	if p.TrafficBytes != nil {
		pp.TrafficBytes = *p.TrafficBytes
	}
	for _, sel := range p.Inbounds {
		pp.Inbounds = append(pp.Inbounds, &corev1.InboundSelection{ServerId: sel.ServerID, InboundIds: sel.InboundIDs})
	}
	if p.GroupID != nil {
		pp.GroupId = *p.GroupID
	}
	if p.RewardPercent != nil {
		pp.RewardPercent = *p.RewardPercent
	}
	return pp
}

// FindServiceByLink finds a service by its link (see domain.FindServiceByLink).
func (s *Server) FindServiceByLink(ctx context.Context, req *corev1.FindServiceByLinkRequest) (*corev1.FoundService, error) {
	sub, secret, err := s.dom.FindServiceByLink(ctx, req.GetUserId(), req.GetLink())
	if err != nil {
		return nil, fail(err)
	}
	mine := sub.UserID == req.GetUserId()
	pb := subscriptionToProto(sub, s.pageLinks(ctx))
	if !mine { // only what the link already shows
		pb.UserId, pb.OrderId, pb.ServerId, pb.ClientEmail, pb.Label, pb.Note = "", "", "", "", "", ""
	}
	return &corev1.FoundService{Service: pb, Mine: mine, Secret: secret}, nil
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
		Label:          req.GetLabel(),
		LinkSecret:     req.GetLinkSecret(),
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
	if o.Label != nil {
		out.Label = *o.Label
	}
	return out
}

// QuoteOrder prices an order before it is created (discount codes).
func (s *Server) QuoteOrder(ctx context.Context, req *corev1.QuoteOrderRequest) (*corev1.Quote, error) {
	q, err := s.dom.QuoteOrder(ctx, domain.QuoteParams{
		UserID: req.GetUserId(), PlanID: req.GetPlanId(), Type: req.GetType(),
		SubscriptionID: req.GetSubscriptionId(), DiscountCode: req.GetDiscountCode(), LinkSecret: req.GetLinkSecret(),
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
	out.Earned = moneyList(r.Earned)
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
	m := walletToProto(w)
	if w.Currency == domain.WalletCurrency {
		// One wallet, in Toman: its equivalent in USDT and the rate it is worked out at, always beside it.
		m.UsdtEquivalent, m.UsdtRate = s.dom.UsdtEquivalent(ctx, w.Balance), s.dom.UsdtBaseRate(ctx)
	}
	return m, nil
}

func walletToProto(w *store.Wallet) *corev1.Wallet {
	return &corev1.Wallet{UserId: w.UserID, Currency: w.Currency, Balance: w.Balance, UpdatedAt: w.UpdatedAt.Unix(),
		CreditLimit: w.CreditLimit}
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

// maxSubscriptionPage is the most services one page of ListSubscriptions holds.
const maxSubscriptionPage = 50

// ListSubscriptions returns the user's subscriptions: the newest 100, or one
// page of them (with the total) when the request pages or searches.
func (s *Server) ListSubscriptions(ctx context.Context, req *corev1.ListSubscriptionsRequest) (*corev1.ListSubscriptionsResponse, error) {
	out := &corev1.ListSubscriptionsResponse{}
	var subs []store.Subscription
	if req.GetPagination() == nil && req.GetQuery() == "" {
		var err error
		if subs, err = s.st.ListSubscriptions(ctx, s.st.Conn(), req.GetUserId(), 100); err != nil {
			return nil, fail(err)
		}
	} else {
		page, size := req.GetPagination().GetPage(), req.GetPagination().GetPageSize()
		if size <= 0 {
			size = 10
		}
		size = min(size, maxSubscriptionPage)
		if page < 1 {
			page = 1
		}
		var (
			total int64
			err   error
		)
		subs, total, err = s.st.ListUserSubscriptions(ctx, s.st.Conn(), req.GetUserId(), req.GetQuery(),
			store.Page{Limit: int(size), Offset: int(page-1) * int(size)})
		if err != nil {
			return nil, fail(err)
		}
		out.PageInfo = &commonv1.PageInfo{Page: page, PageSize: size, Total: total}
	}
	page := s.pageLinks(ctx)
	for i := range subs {
		out.Subscriptions = append(out.Subscriptions, subscriptionToProto(&subs[i], page))
	}
	return out, nil
}

// pageLinks makes the page URL of a service: empty while the page is
// switched off or the store has no public URL (the settings are read once for
// all the services of a reply).
func (s *Server) pageLinks(ctx context.Context) func(token string) string {
	none := func(string) string { return "" }
	base := s.dashboardURL(ctx)
	if base == "" {
		return none
	}
	set, err := s.dom.SubPage(ctx)
	if err != nil || !set.Enabled {
		return none
	}
	return func(token string) string { return domain.SubPageURL(base, token) }
}

func subscriptionToProto(sc *store.Subscription, page func(token string) string) *corev1.Subscription {
	sp := &corev1.Subscription{
		Id:               sc.ID,
		UserId:           sc.UserID,
		OrderId:          sc.OrderID,
		ServerId:         sc.ServerID,
		ClientEmail:      sc.ClientEmail,
		Status:           sc.Status,
		TrafficUsedBytes: sc.TrafficUsed,
		SubscriptionLink: sc.SubLink,
	}
	if page != nil && sc.Status != "pending" && sc.Status != "deleted" {
		sp.PageUrl = page(sc.PageToken)
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
	if sc.LastSyncedAt != nil {
		sp.LastSyncedAt = sc.LastSyncedAt.Unix()
	}
	if sc.Label != nil {
		sp.Label = *sc.Label
	}
	if sc.Note != nil {
		sp.Note = *sc.Note
	}
	return sp
}

// SetSubscriptionLabel names one of a reseller's services.
func (s *Server) SetSubscriptionLabel(ctx context.Context, req *corev1.SetSubscriptionLabelRequest) (*corev1.Subscription, error) {
	sub, err := s.dom.SetSubscriptionLabel(ctx, req.GetUserId(), req.GetSubscriptionId(), req.GetLabel())
	if err != nil {
		return nil, fail(err)
	}
	return subscriptionToProto(sub, s.pageLinks(ctx)), nil
}

// GetResellerInfo is a reseller's panel: terms, balances and earnings.
func (s *Server) GetResellerInfo(ctx context.Context, req *corev1.GetResellerInfoRequest) (*corev1.ResellerInfo, error) {
	ri, err := s.dom.ResellerInfo(ctx, req.GetUserId())
	if err != nil {
		return nil, fail(err)
	}
	naming, err := s.dom.ResellerNaming(ctx, req.GetUserId())
	if err != nil {
		return nil, fail(err)
	}
	out := &corev1.ResellerInfo{TierName: ri.Terms.TierName, DiscountPercent: ri.Terms.DiscountPercent,
		CommissionPercent: ri.Terms.CommissionPercent, Customers: ri.Customers, RefCode: ri.RefCode,
		ConfigPrefix: naming.ConfigPrefix, PanelGroup: naming.PanelGroup}
	for _, w := range ri.Wallets {
		out.Balances = append(out.Balances, &corev1.ResellerBalance{Currency: w.Currency, Balance: w.Balance,
			CreditLimit: w.CreditLimit, Available: w.Balance + w.CreditLimit})
	}
	out.CommissionEarned = moneyList(ri.Earned)
	out.Targets = targetsToProto(ri.Targets)
	return out, nil
}

// targetsToProto is the monthly target for the bot (nil while the owner has targets off).
func targetsToProto(p *domain.TargetProgress) *corev1.ResellerTargets {
	if p == nil || !p.On {
		return nil
	}
	out := &corev1.ResellerTargets{Period: p.Period, Calendar: p.Calendar, From: p.From, To: p.To, EndsAt: p.EndsAt.Unix(), Sold: p.Sold,
		Remaining: p.Remaining, BonusPaid: p.BonusPaid}
	for _, t := range p.Tiers {
		out.Tiers = append(out.Tiers, &corev1.ResellerTargetTier{Threshold: t.Threshold, Bonus: t.Bonus, Reached: t.Reached, Paid: t.Paid})
	}
	return out
}

// SetResellerConfigPrefix sets the reseller's own prefix for the names of their configs in the 3x-ui panel (once:
// after that only the owner changes it, from the dashboard).
func (s *Server) SetResellerConfigPrefix(ctx context.Context, req *corev1.SetResellerConfigPrefixRequest) (*corev1.ResellerInfo, error) {
	u, err := s.st.GetUser(ctx, s.st.Conn(), req.GetUserId())
	if err != nil {
		return nil, fail(err)
	}
	if err := s.dom.SetResellerConfigPrefix(ctx, u, u.ID, req.GetPrefix()); err != nil {
		return nil, fail(err)
	}
	return s.GetResellerInfo(ctx, &corev1.GetResellerInfoRequest{UserId: u.ID})
}

// moneyList is amounts per currency, sorted by currency.
func moneyList(m map[string]int64) []*commonv1.Money {
	currencies := make([]string, 0, len(m))
	for c := range m {
		currencies = append(currencies, c)
	}
	sort.Strings(currencies)
	out := make([]*commonv1.Money, 0, len(m))
	for _, c := range currencies {
		out = append(out, &commonv1.Money{Amount: m[c], Currency: c})
	}
	return out
}

// GetSettings returns flattened settings (key -> raw JSON string) and the
// bot photo's version.
func (s *Server) GetSettings(ctx context.Context, _ *corev1.GetSettingsRequest) (*corev1.Settings, error) {
	m, err := s.st.GetSettings(ctx, s.st.Conn())
	if err != nil {
		return nil, fail(err)
	}
	out := &corev1.Settings{Values: map[string]string{}, BotPhotoVersion: s.botPhotoVersion(ctx), WhiteLabel: s.whiteLabel(ctx),
		ResellersEnabled: s.resellersIncluded(ctx)}
	for k, v := range m {
		if strings.HasPrefix(k, "legal.") { // the pages' texts are core's to show; the bot needs none of them
			continue
		}
		out.Values[k] = string(v)
	}
	// The USDT rate the bot shows and the store charges: the live price (plus the owner's margin) while it is fresh, else the rate the owner typed.
	if raw := s.dom.UsdtSettingRaw(ctx); raw != "" {
		out.Values["payments.usdt_rate"] = raw
	}
	// The vendor's own features are switched off for a store whose license does not grant them, whatever the switches say.
	if !s.dom.PrivateFeatures(ctx) {
		out.Values[domain.SettingVendorEnabled] = "false"
		out.Values[domain.SettingSupplierEnabled] = "false"
	}
	// The store's own Terms and Privacy pages answer where the owner has set no address of their own, so the bot's buttons and /terms work
	// from the first day (needs the store's public HTTPS address).
	if origin := strings.TrimRight(s.dashboardURL(ctx), "/"); strings.HasPrefix(origin, "https://") {
		for key, path := range map[string]string{"branding.terms_url": "/terms", "branding.privacy_url": "/privacy"} {
			if v := strings.TrimSpace(out.Values[key]); v == "" || v == `""` || v == "null" {
				b, _ := json.Marshal(origin + path)
				out.Values[key] = string(b)
			}
		}
	}
	// Where the Mini App opens from: only while it is on and the store has a
	// public HTTPS address Telegram can reach.
	if plain, err := s.dom.Settings(ctx); err == nil && domain.MiniAppOn(plain) {
		out.MiniappUrl = domain.MiniAppURL(s.dashboardURL(ctx))
	}
	// The extra required chats the license allows. When they cannot be read the
	// bot holds customers to the first chat alone (the floor of every license),
	// as it does for an older core.
	if extras, err := s.dom.EnforcedJoinExtras(ctx); err == nil {
		for _, c := range extras {
			out.JoinChats = append(out.JoinChats, &corev1.JoinChat{Chat: c.Chat, Link: c.Link, Label: c.Label})
		}
	}
	// Whether the FAQ and the tutorials have anything to show: the bot's buttons
	// follow. A read that fails fails the refresh, so the bot keeps what it had
	// instead of hiding the buttons for a minute.
	faq, tutorials, err := s.dom.HelpAvailable(ctx)
	if err != nil {
		return nil, fail(err)
	}
	out.HelpFaq, out.HelpTutorials = faq, tutorials
	return out, nil
}

// whiteLabel: the license includes white label. When the license cannot be
// read it is false, so an error never hides the vendor credit.
func (s *Server) whiteLabel(ctx context.Context) bool {
	eff, err := s.dom.Effective(ctx)
	return err == nil && eff.WhiteLabel()
}

// resellersIncluded: the license in effect includes resellers, so the bot
// offers customers the Become a reseller button. When the license cannot be
// read it is false: no button, rather than an application core would refuse.
func (s *Server) resellersIncluded(ctx context.Context) bool {
	eff, err := s.dom.Effective(ctx)
	return err == nil && eff.Entitlements.Resellers
}

// botPhotoVersion is the bot photo's version (without reading the photo): ""
// when there is none, "?" when it cannot be read now, so the bot neither
// sets nor removes its photo on a passing failure.
func (s *Server) botPhotoVersion(ctx context.Context) string {
	a, err := s.st.AssetInfo(ctx, s.st.Conn(), store.AssetBotPhoto)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return ""
	case err != nil:
		return "?"
	}
	return photoVersion(a)
}

// photoVersion tells uploads apart: each one changes updated_at.
func photoVersion(a *store.Asset) string { return strconv.FormatInt(a.UpdatedAt.UnixNano(), 10) }

// GetBotPhoto returns the bot photo, a square JPEG, with its version;
// NotFound when the store has none.
func (s *Server) GetBotPhoto(ctx context.Context, _ *corev1.GetBotPhotoRequest) (*corev1.BotPhoto, error) {
	a, err := s.st.GetAsset(ctx, s.st.Conn(), store.AssetBotPhoto)
	if err != nil {
		return nil, fail(err)
	}
	return &corev1.BotPhoto{Version: photoVersion(a), Jpeg: a.Data}, nil
}
