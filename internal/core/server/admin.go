package server

import (
	"context"
	"time"

	commonv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/common/v1"
	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// AdminGetStats returns the admin panel figures.
func (s *Server) AdminGetStats(ctx context.Context, req *corev1.AdminGetStatsRequest) (*corev1.AdminStats, error) {
	if err := s.needPayments(); err != nil {
		return nil, err
	}
	if err := s.needProvisioner(); err != nil {
		return nil, err
	}
	st, err := s.dom.AdminStats(ctx, req.GetActorTelegramId(), s.pay, s.prov)
	if err != nil {
		return nil, fail(err)
	}
	return &corev1.AdminStats{
		UsersTotal: st.UsersTotal, UsersToday: st.UsersSince, ActiveSubscriptions: st.ActiveSubscriptions,
		PendingPayments: st.PendingPayments, ProvisionFailedOrders: st.ProvisionFailedOrders,
		PanelHealthy: st.PanelHealthy, PanelDetail: st.PanelDetail,
	}, nil
}

// AdminListPendingPayments returns the manual payment review queue.
func (s *Server) AdminListPendingPayments(ctx context.Context, req *corev1.AdminListPendingPaymentsRequest) (*corev1.AdminListPendingPaymentsResponse, error) {
	if err := s.needPayments(); err != nil {
		return nil, err
	}
	views, err := s.dom.AdminListPendingPayments(ctx, req.GetActorTelegramId(), s.pay, int(req.GetLimit()))
	if err != nil {
		return nil, fail(err)
	}
	out := &corev1.AdminListPendingPaymentsResponse{}
	for _, v := range views {
		p := &corev1.PendingPayment{
			IntentId: v.IntentID, OrderId: v.OrderID, Provider: v.Provider,
			Amount:      &commonv1.Money{Amount: v.Amount, Currency: v.Currency},
			ReceiptFile: v.ReceiptFile, ReferenceNumber: v.ReferenceNumber, Network: v.Network, Txid: v.TXID,
			SubmittedAt: v.SubmittedAt.Unix(), PossibleDuplicate: v.PossibleDuplicate,
		}
		if v.User != nil {
			p.User = userToProto(v.User)
		}
		out.Payments = append(out.Payments, p)
	}
	return out, nil
}

// ReviewManualPayment approves or rejects a manual payment as the actor.
func (s *Server) ReviewManualPayment(ctx context.Context, req *corev1.ReviewManualPaymentRequest) (*corev1.PaymentIntentRef, error) {
	ctx = fromBot(ctx)
	if err := s.needPayments(); err != nil {
		return nil, err
	}
	st, err := s.dom.AdminReviewPayment(ctx, req.GetActorTelegramId(), s.pay, req.GetIntentId(), req.GetDecision(), req.GetReason())
	if err != nil {
		return nil, fail(err)
	}
	return &corev1.PaymentIntentRef{Id: req.GetIntentId(), Status: st}, nil
}

// AdminFindUser looks a user up by Telegram id or @username.
func (s *Server) AdminFindUser(ctx context.Context, req *corev1.AdminFindUserRequest) (*corev1.AdminUserView, error) {
	v, err := s.dom.AdminFindUser(ctx, req.GetActorTelegramId(), req.GetQuery())
	if err != nil {
		return nil, fail(err)
	}
	out := &corev1.AdminUserView{User: userToProto(v.User), Subscriptions: v.Subscriptions, Orders: v.Orders}
	for _, w := range v.Wallets {
		out.Wallets = append(out.Wallets, &corev1.Wallet{UserId: w.UserID, Currency: w.Currency, Balance: w.Balance, UpdatedAt: w.UpdatedAt.Unix()})
	}
	return out, nil
}

// AdminAdjustBalance credits or debits a wallet by hand.
func (s *Server) AdminAdjustBalance(ctx context.Context, req *corev1.AdminAdjustBalanceRequest) (*corev1.Wallet, error) {
	ctx = fromBot(ctx)
	w, err := s.dom.AdminAdjustBalance(ctx, req.GetActorTelegramId(), req.GetUserId(),
		req.GetDelta().GetAmount(), req.GetDelta().GetCurrency(), req.GetReason(), req.GetIdempotencyKey())
	if err != nil {
		return nil, fail(err)
	}
	return &corev1.Wallet{UserId: w.UserID, Currency: w.Currency, Balance: w.Balance, UpdatedAt: w.UpdatedAt.Unix()}, nil
}

// AdminSetUserStatus bans or unbans a user.
func (s *Server) AdminSetUserStatus(ctx context.Context, req *corev1.AdminSetUserStatusRequest) (*corev1.User, error) {
	ctx = fromBot(ctx)
	u, err := s.dom.AdminSetUserStatus(ctx, req.GetActorTelegramId(), req.GetUserId(), req.GetStatus(), req.GetReason())
	if err != nil {
		return nil, fail(err)
	}
	return userToProto(u), nil
}

// AdminUpsertPlan creates or updates a plan.
func (s *Server) AdminUpsertPlan(ctx context.Context, req *corev1.AdminUpsertPlanRequest) (*corev1.Plan, error) {
	ctx = fromBot(ctx)
	in := req.GetPlan()
	p := &store.Plan{
		ID: in.GetId(), NameI18n: in.GetNameI18N(), Kind: in.GetKind(),
		Price: in.GetPrice().GetAmount(), Currency: in.GetPrice().GetCurrency(),
		Enabled: in.GetEnabled(), IsTrial: in.GetIsTrial(), Sort: in.GetSort(), IsTopup: in.GetIsTopup(),
	}
	if d := in.GetDurationDays(); d > 0 {
		p.DurationDays = &d
	}
	if b := in.GetTrafficBytes(); b > 0 {
		p.TrafficBytes = &b
	}
	got, err := s.dom.AdminUpsertPlan(ctx, req.GetActorTelegramId(), p)
	if err != nil {
		return nil, fail(err)
	}
	return planToProto(got), nil
}

// AdminSetSetting changes one allowlisted setting.
func (s *Server) AdminSetSetting(ctx context.Context, req *corev1.AdminSetSettingRequest) (*corev1.Settings, error) {
	ctx = fromBot(ctx)
	if err := s.dom.AdminSetSetting(ctx, req.GetActorTelegramId(), req.GetKey(), req.GetValue()); err != nil {
		return nil, fail(err)
	}
	return s.GetSettings(ctx, &corev1.GetSettingsRequest{})
}

// AdminUpsertDiscount creates or changes a discount code.
func (s *Server) AdminUpsertDiscount(ctx context.Context, req *corev1.AdminUpsertDiscountRequest) (*corev1.Discount, error) {
	ctx = fromBot(ctx)
	in := req.GetDiscount()
	d := &store.Discount{Code: in.GetCode(), Enabled: in.GetEnabled()}
	if p := in.GetPercent(); p != 0 {
		d.Percent = &p
	}
	if a := in.GetAmount(); a.GetAmount() != 0 {
		amount, cur := a.GetAmount(), a.GetCurrency()
		d.Amount, d.Currency = &amount, &cur
	}
	if m := in.GetMaxUses(); m != 0 {
		d.MaxUses = &m
	}
	if e := in.GetExpiresAt(); e != 0 {
		t := time.Unix(e, 0).UTC()
		d.ExpiresAt = &t
	}
	got, err := s.dom.AdminUpsertDiscount(ctx, req.GetActorTelegramId(), d)
	if err != nil {
		return nil, fail(err)
	}
	return discountToProto(got), nil
}

// AdminListDiscounts lists the discount codes.
func (s *Server) AdminListDiscounts(ctx context.Context, req *corev1.AdminListDiscountsRequest) (*corev1.AdminListDiscountsResponse, error) {
	list, err := s.dom.AdminListDiscounts(ctx, req.GetActorTelegramId())
	if err != nil {
		return nil, fail(err)
	}
	out := &corev1.AdminListDiscountsResponse{}
	for i := range list {
		out.Discounts = append(out.Discounts, discountToProto(&list[i]))
	}
	return out, nil
}

func discountToProto(d *store.Discount) *corev1.Discount {
	out := &corev1.Discount{Code: d.Code, Used: d.Used, Enabled: d.Enabled}
	if d.Percent != nil {
		out.Percent = *d.Percent
	}
	if d.Amount != nil && d.Currency != nil {
		out.Amount = &commonv1.Money{Amount: *d.Amount, Currency: *d.Currency}
	}
	if d.MaxUses != nil {
		out.MaxUses = *d.MaxUses
	}
	if d.ExpiresAt != nil {
		out.ExpiresAt = d.ExpiresAt.Unix()
	}
	return out
}
