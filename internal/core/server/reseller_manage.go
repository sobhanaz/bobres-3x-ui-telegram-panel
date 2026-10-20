package server

import (
	"context"

	commonv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/common/v1"
	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// Reseller management over gRPC (docs/03-bot-ux.md): a reseller's own
// services and the customers who joined through their link. Every rule lives
// in the domain (domain.ResellerAccess, the ownership checks); the bot is the
// only caller, and what it changes is audited as made in the bot.

// resellerPage is the page a request asks for, with the bot's defaults.
func resellerPage(p *commonv1.Pagination) (page, size int32) {
	page, size = p.GetPage(), p.GetPageSize()
	if size <= 0 {
		size = 10
	}
	size = min(size, maxSubscriptionPage)
	if page < 1 {
		page = 1
	}
	return page, size
}

func storePage(page, size int32) store.Page {
	return store.Page{Limit: int(size), Offset: int(page-1) * int(size)}
}

// ListResellerServices pages a reseller's own services, filtered by status and
// searched by name or number, with the counts per status.
func (s *Server) ListResellerServices(ctx context.Context, req *corev1.ListResellerServicesRequest) (*corev1.ListResellerServicesResponse, error) {
	ctx = fromBot(ctx)
	page, size := resellerPage(req.GetPagination())
	got, err := s.dom.ListResellerServices(ctx, req.GetUserId(),
		domain.ResellerServiceFilter{Status: req.GetStatus(), Query: req.GetQuery()}, storePage(page, size))
	if err != nil {
		return nil, fail(err)
	}
	out := &corev1.ListResellerServicesResponse{
		PageInfo: &commonv1.PageInfo{Page: page, PageSize: size, Total: got.Total},
		Counts: &corev1.ServiceCounts{Total: got.Counts.Total, Active: got.Counts.Active, ExpiringSoon: got.Counts.ExpiringSoon,
			Expired: got.Counts.Expired, Paused: got.Counts.Paused, Pending: got.Counts.Pending},
	}
	links := s.pageLinks(ctx)
	for i := range got.Items {
		out.Subscriptions = append(out.Subscriptions, subscriptionToProto(&got.Items[i], links))
	}
	return out, nil
}

// SetResellerServiceEnabled pauses or resumes one of the reseller's services.
func (s *Server) SetResellerServiceEnabled(ctx context.Context, req *corev1.SetResellerServiceEnabledRequest) (*corev1.Subscription, error) {
	if err := s.needProvisioner(); err != nil {
		return nil, err
	}
	ctx = fromBot(ctx)
	sub, err := s.dom.ResellerSetServiceEnabled(ctx, s.prov, req.GetUserId(), req.GetSubscriptionId(), req.GetEnabled())
	if err != nil {
		return nil, fail(err)
	}
	return subscriptionToProto(sub, s.pageLinks(ctx)), nil
}

// DeleteResellerService deletes one of the reseller's services.
func (s *Server) DeleteResellerService(ctx context.Context, req *corev1.DeleteResellerServiceRequest) (*corev1.DeleteResellerServiceResponse, error) {
	if err := s.needProvisioner(); err != nil {
		return nil, err
	}
	ctx = fromBot(ctx)
	if err := s.dom.ResellerDeleteService(ctx, s.prov, req.GetUserId(), req.GetSubscriptionId()); err != nil {
		return nil, fail(err)
	}
	return &corev1.DeleteResellerServiceResponse{}, nil
}

// SetSubscriptionNote sets the private note on one of the reseller's services.
func (s *Server) SetSubscriptionNote(ctx context.Context, req *corev1.SetSubscriptionNoteRequest) (*corev1.Subscription, error) {
	ctx = fromBot(ctx)
	sub, err := s.dom.ResellerSetNote(ctx, req.GetUserId(), req.GetSubscriptionId(), req.GetNote())
	if err != nil {
		return nil, fail(err)
	}
	return subscriptionToProto(sub, s.pageLinks(ctx)), nil
}

// ListInvitedCustomers pages the users who joined through the reseller's link.
func (s *Server) ListInvitedCustomers(ctx context.Context, req *corev1.ListInvitedCustomersRequest) (*corev1.ListInvitedCustomersResponse, error) {
	ctx = fromBot(ctx)
	page, size := resellerPage(req.GetPagination())
	got, err := s.dom.ListInvitedCustomers(ctx, req.GetUserId(), storePage(page, size))
	if err != nil {
		return nil, fail(err)
	}
	out := &corev1.ListInvitedCustomersResponse{PageInfo: &commonv1.PageInfo{Page: page, PageSize: size, Total: got.Total}}
	for _, c := range got.Items {
		out.Customers = append(out.Customers, &corev1.InvitedCustomer{
			TelegramId: c.TelegramID, Username: c.Username, JoinedAt: c.JoinedAt.Unix(), PaidOrders: c.PaidOrders,
			Spent: moneyList(c.Spent), Commission: moneyList(c.Commission),
		})
	}
	return out, nil
}
