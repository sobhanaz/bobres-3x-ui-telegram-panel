package server

import (
	"context"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// Config names (domain/config_names.go): the bot checks a name before an order or a rename, and renames a customer's or a
// reseller's own service.

// servers is the provisioner's server registry (nil without a provisioner).
func (s *Server) servers() domain.ServerAdmin {
	if s.prov == nil {
		return nil
	}
	return s.prov
}

// names is the provisioner as the name check asks it (nil without a provisioner: every name is then "unchecked").
func (s *Server) names() domain.NameChecker {
	if s.prov == nil {
		return nil
	}
	return s.prov
}

// renamable says, for the services of one user, whether they may rename them now (Subscription.renamable).
func (s *Server) renamable(ctx context.Context, userID string) func(*store.Subscription) bool {
	u, err := s.st.GetUser(ctx, s.st.Conn(), userID)
	if err != nil {
		return func(*store.Subscription) bool { return false }
	}
	return s.dom.RenamableFor(ctx, s.servers(), u)
}

// CheckConfigName says whether a config name can be used for a new service or a rename.
func (s *Server) CheckConfigName(ctx context.Context, req *corev1.CheckConfigNameRequest) (*corev1.ConfigNameCheck, error) {
	res, err := s.dom.CheckConfigName(ctx, s.names(), req.GetUserId(), req.GetName(), req.GetSubscriptionId())
	if err != nil {
		return nil, fail(err)
	}
	return &corev1.ConfigNameCheck{Ok: res.OK, Reason: res.Reason, Name: res.Name}, nil
}

// RenameService renames one of the user's own services, keeping its subscription link.
func (s *Server) RenameService(ctx context.Context, req *corev1.RenameServiceRequest) (*corev1.RenameServiceResponse, error) {
	if err := s.needProvisioner(); err != nil {
		return nil, err
	}
	ctx = fromBot(ctx)
	res, err := s.dom.RenameOwnService(ctx, s.prov, s.servers(), req.GetUserId(), req.GetSubscriptionId(), req.GetName())
	if err != nil {
		return nil, fail(err)
	}
	out := &corev1.RenameServiceResponse{Ok: res.OK, Reason: res.Reason, Name: res.Name}
	if res.Service != nil {
		out.Service = subscriptionToProto(res.Service, s.pageLinks(ctx))
		out.Service.Renamable = s.renamable(ctx, req.GetUserId())(res.Service)
	}
	return out, nil
}
