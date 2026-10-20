package server

import (
	"context"

	commonv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/common/v1"
	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
)

// Bulk purchase over gRPC (docs/03-bot-ux.md, "Bulk purchase"). Every rule
// lives in the domain (domain/bulk.go); the bot is the only caller, and what
// the purchase changes is audited as made in the bot.

// QuoteBulkOrder prices a bulk purchase before it is made.
func (s *Server) QuoteBulkOrder(ctx context.Context, req *corev1.QuoteBulkOrderRequest) (*corev1.BulkQuote, error) {
	q, err := s.dom.QuoteBulk(ctx, domain.BulkQuoteParams{
		UserID: req.GetUserId(), PlanID: req.GetPlanId(), Quantity: int(req.GetQuantity()),
	})
	if err != nil {
		return nil, fail(err)
	}
	return &corev1.BulkQuote{
		Quantity:    int32(q.Quantity), //nolint:gosec // checked against the store's maximum (at most 100)
		UnitPrice:   &commonv1.Money{Amount: q.UnitAmount, Currency: q.Currency},
		Total:       &commonv1.Money{Amount: q.Amount, Currency: q.Currency},
		MaxQuantity: int32(q.MaxQuantity), //nolint:gosec // at most 100
	}, nil
}

// CreateBulkOrder buys the services and pays for them from the wallet, all or
// nothing (idempotent by the request's key).
func (s *Server) CreateBulkOrder(ctx context.Context, req *corev1.CreateBulkOrderRequest) (*corev1.BulkOrder, error) {
	ctx = fromBot(ctx)
	v, err := s.dom.CreateBulk(ctx, domain.BulkParams{
		UserID: req.GetUserId(), PlanID: req.GetPlanId(), Quantity: int(req.GetQuantity()),
		IdempotencyKey: req.GetIdempotencyKey(), LabelPrefix: req.GetLabelPrefix(),
		ExpectedAmount: req.GetExpectedTotal().GetAmount(), ExpectedCurrency: req.GetExpectedTotal().GetCurrency(),
	})
	if err != nil {
		return nil, fail(err)
	}
	return bulkToProto(v), nil
}

// GetBulkOrder returns a bulk purchase with its services and their links.
func (s *Server) GetBulkOrder(ctx context.Context, req *corev1.GetBulkOrderRequest) (*corev1.BulkOrder, error) {
	v, err := s.dom.GetBulk(ctx, req.GetBulkId(), req.GetUserId())
	if err != nil {
		return nil, fail(err)
	}
	return bulkToProto(v), nil
}

func bulkToProto(v *domain.BulkView) *corev1.BulkOrder {
	out := &corev1.BulkOrder{
		Id: v.ID, UserId: v.UserID, PlanId: v.PlanID, Quantity: v.Quantity,
		UnitPrice:    &commonv1.Money{Amount: v.UnitAmount, Currency: v.Currency},
		Total:        &commonv1.Money{Amount: v.Amount, Currency: v.Currency},
		CreatedAt:    v.CreatedAt.Unix(),
		PlanNameI18N: v.PlanName,
		Replayed:     v.Replayed,
	}
	if v.LabelPrefix != nil {
		out.LabelPrefix = *v.LabelPrefix
	}
	if v.DeliveredAt != nil {
		out.DeliveredAt = v.DeliveredAt.Unix()
	}
	for _, sv := range v.Services {
		svc := &corev1.BulkService{
			OrderId: sv.ID, OrderStatus: sv.Status,
			SubscriptionId: sv.SubscriptionID, Status: sv.SubStatus, SubscriptionLink: sv.SubLink,
		}
		if sv.BulkIndex != nil {
			svc.Index = *sv.BulkIndex
		}
		switch {
		case sv.SubLabel != nil:
			svc.Label = *sv.SubLabel
		case sv.Label != nil:
			svc.Label = *sv.Label
		}
		if sv.ExpiresAt != nil {
			svc.ExpiresAt = sv.ExpiresAt.Unix()
		}
		if sv.TrafficTotal != nil {
			svc.TrafficTotalBytes = *sv.TrafficTotal
		}
		out.Services = append(out.Services, svc)
	}
	return out
}
