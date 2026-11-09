package server

import (
	"context"
	"math"
	"time"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
)

// The calls of the bot's button-driven admin menu. Each takes the actor's
// Telegram id and lets the domain check the role again.

func dayToProto(d domain.DayFigures) *corev1.AdminDay {
	out := &corev1.AdminDay{Date: d.Date, NewCustomers: d.NewCustomers}
	for _, l := range d.Lines {
		out.Lines = append(out.Lines, &corev1.AdminDayLine{Currency: l.Currency, Revenue: l.Revenue, Orders: l.Orders, Refunds: l.Refunds})
	}
	return out
}

// AdminTodayStats returns today's and yesterday's sales.
func (s *Server) AdminTodayStats(ctx context.Context, req *corev1.AdminTodayStatsRequest) (*corev1.AdminTodayStatsResponse, error) {
	st, err := s.dom.AdminToday(ctx, req.GetActorTelegramId(), time.Now())
	if err != nil {
		return nil, fail(err)
	}
	return &corev1.AdminTodayStatsResponse{Tz: st.TZ, Today: dayToProto(st.Today), Yesterday: dayToProto(st.Yesterday)}, nil
}

// AdminListSettingSpecs lists the settings the actor may change, with values.
func (s *Server) AdminListSettingSpecs(ctx context.Context, req *corev1.AdminListSettingSpecsRequest) (*corev1.AdminListSettingSpecsResponse, error) {
	views, err := s.dom.AdminSettingSpecs(ctx, req.GetActorTelegramId())
	if err != nil {
		return nil, fail(err)
	}
	out := &corev1.AdminListSettingSpecsResponse{}
	for _, v := range views {
		out.Specs = append(out.Specs, &corev1.AdminSettingSpec{
			Key: v.Key, Group: v.Group, Kind: v.Kind, DefaultValue: v.Default, Min: v.Min, Max: v.Max,
			Options: v.Options, MaxLen: int32(min(v.MaxLen, 1<<30)), //nolint:gosec // bounded
			Help: v.Help, Value: v.Value,
		})
	}
	return out, nil
}

func adminServerToProto(sv *domain.Server) *corev1.AdminServer {
	return &corev1.AdminServer{
		Id: sv.ID, Name: sv.Name, Enabled: sv.Enabled, Weight: sv.Weight,
		HealthKnown: sv.CheckedAt != 0, Healthy: sv.Healthy, HealthError: sv.HealthError,
		Clients: sv.Clients, Managed: sv.Managed,
	}
}

// AdminListServers lists the 3x-ui servers (owner and admin).
func (s *Server) AdminListServers(ctx context.Context, req *corev1.AdminListServersRequest) (*corev1.AdminListServersResponse, error) {
	if err := s.needProvisioner(); err != nil {
		return nil, err
	}
	list, err := s.dom.AdminListServers(ctx, req.GetActorTelegramId(), s.prov)
	if err != nil {
		return nil, fail(err)
	}
	out := &corev1.AdminListServersResponse{}
	for i := range list {
		out.Servers = append(out.Servers, adminServerToProto(&list[i]))
	}
	return out, nil
}

// AdminSetServer switches a server on or off and sets its weight (owner and admin).
func (s *Server) AdminSetServer(ctx context.Context, req *corev1.AdminSetServerRequest) (*corev1.AdminServer, error) {
	ctx = fromBot(ctx)
	if err := s.needProvisioner(); err != nil {
		return nil, err
	}
	sv, err := s.dom.AdminSetServer(ctx, req.GetActorTelegramId(), s.prov, req.GetServerId(), req.GetEnabled(), req.GetWeight())
	if err != nil {
		return nil, fail(err)
	}
	return adminServerToProto(sv), nil
}

func staffToProto(e domain.StaffEntry) *corev1.AdminStaffMember {
	return &corev1.AdminStaffMember{
		UserId: e.UserID, TelegramId: e.TelegramID, Username: e.Username, Role: e.Role, Status: e.Status,
		ConfiguredOwner: e.ConfiguredOwner,
	}
}

// AdminListStaff lists the staff (owner only).
func (s *Server) AdminListStaff(ctx context.Context, req *corev1.AdminListStaffRequest) (*corev1.AdminListStaffResponse, error) {
	list, err := s.dom.AdminListStaff(ctx, req.GetActorTelegramId())
	if err != nil {
		return nil, fail(err)
	}
	out := &corev1.AdminListStaffResponse{}
	for _, e := range list {
		out.Staff = append(out.Staff, staffToProto(e))
	}
	return out, nil
}

// AdminAddStaff gives or changes a staff role (owner only).
func (s *Server) AdminAddStaff(ctx context.Context, req *corev1.AdminAddStaffRequest) (*corev1.AdminStaffMember, error) {
	ctx = fromBot(ctx)
	e, err := s.dom.AdminAddStaff(ctx, req.GetActorTelegramId(), req.GetTargetTelegramId(), req.GetRole(), req.GetReason())
	if err != nil {
		return nil, fail(err)
	}
	return staffToProto(*e), nil
}

// AdminRemoveStaff takes a staff role away (owner only).
func (s *Server) AdminRemoveStaff(ctx context.Context, req *corev1.AdminRemoveStaffRequest) (*corev1.AdminRemoveStaffResponse, error) {
	ctx = fromBot(ctx)
	if err := s.dom.AdminRemoveStaff(ctx, req.GetActorTelegramId(), req.GetTargetTelegramId(), req.GetReason()); err != nil {
		return nil, fail(err)
	}
	return &corev1.AdminRemoveStaffResponse{}, nil
}

// AdminBroadcast checks a broadcast, or checks and sends it now.
func (s *Server) AdminBroadcast(ctx context.Context, req *corev1.AdminBroadcastRequest) (*corev1.AdminBroadcastResponse, error) {
	ctx = fromBot(ctx)
	in := domain.BroadcastInput{
		Segment: req.GetSegment(), Language: req.GetLanguage(), Texts: req.GetTexts(),
		ButtonURL: req.GetButtonUrl(), ButtonLabels: req.GetButtonLabels(),
	}
	p, err := s.dom.AdminBroadcast(ctx, req.GetActorTelegramId(), in, req.GetSend())
	if err != nil {
		return nil, fail(err)
	}
	return &corev1.AdminBroadcastResponse{
		Audience: p.Total, AudienceFa: p.FA, AudienceEn: p.EN, Texts: p.Texts, ButtonLabels: p.ButtonLabels,
		BroadcastId: p.BroadcastID,
	}, nil
}

// AdminListPanelMissing lists a page of the services whose client the panel no longer has (owner and admin).
func (s *Server) AdminListPanelMissing(ctx context.Context, req *corev1.AdminListPanelMissingRequest) (*corev1.AdminListPanelMissingResponse, error) {
	page, err := s.dom.AdminListPanelMissing(ctx, req.GetActorTelegramId(), int(req.GetOffset()), int(req.GetLimit()))
	if err != nil {
		return nil, fail(err)
	}
	out := &corev1.AdminListPanelMissingResponse{Total: page.Total}
	for i := range page.Services {
		r := &page.Services[i]
		sv := &corev1.AdminPanelMissingService{
			Id: r.ID, ClientEmail: r.ClientEmail, TelegramId: r.TelegramID, Username: r.Username,
			PlanName: r.PlanName, Status: r.Status,
		}
		if r.ExpiresAt != nil {
			sv.ExpiresAt = r.ExpiresAt.Unix()
		} else if r.FirstUseDays != nil {
			sv.FirstUseDays = *r.FirstUseDays
		}
		if r.PanelMissingSince != nil {
			sv.MissingSince = r.PanelMissingSince.Unix()
		}
		out.Services = append(out.Services, sv)
	}
	return out, nil
}

// AdminResolvePanelMissing makes the client of one such service again, or deletes the service (owner and
// admin); a service that changed since it was listed is skipped, and the outcome says so.
func (s *Server) AdminResolvePanelMissing(ctx context.Context, req *corev1.AdminResolvePanelMissingRequest) (*corev1.AdminResolvePanelMissingResponse, error) {
	ctx = fromBot(ctx)
	if err := s.needProvisioner(); err != nil {
		return nil, err
	}
	outcome, sub, err := s.dom.AdminResolvePanelMissing(ctx, req.GetActorTelegramId(), s.prov, req.GetSubscriptionId(), req.GetAction(), req.GetReason())
	if err != nil {
		return nil, fail(err)
	}
	out := &corev1.AdminResolvePanelMissingResponse{Outcome: outcome}
	if sub != nil {
		out.ClientEmail = sub.ClientEmail
	}
	return out, nil
}

// AdminListUndelivered lists a page of the paid orders whose service could not be made or applied yet, the longest
// waiting first (owner and admin).
func (s *Server) AdminListUndelivered(ctx context.Context, req *corev1.AdminListUndeliveredRequest) (*corev1.AdminListUndeliveredResponse, error) {
	page, err := s.dom.AdminListUndelivered(ctx, req.GetActorTelegramId(), int(req.GetOffset()), int(req.GetLimit()))
	if err != nil {
		return nil, fail(err)
	}
	out := &corev1.AdminListUndeliveredResponse{Total: page.Total}
	for i := range page.Orders {
		r := &page.Orders[i]
		o := &corev1.AdminUndeliveredOrder{
			Id: r.ID, TelegramId: r.TelegramID, Username: r.Username, PlanName: r.PlanName, Type: r.Type,
			Amount: r.Amount, Currency: r.Currency, Attempts: int32(min(r.Attempts, math.MaxInt32)), //nolint:gosec // bounded
			LastError: r.LastError, CreatedAt: r.CreatedAt.Unix(),
			SellerKind: r.SellerKind, SellerTelegramId: r.SellerTelegramID, SellerUsername: r.SellerUsername,
			Bulk: r.BulkID != nil,
		}
		if r.NextAttemptAt != nil {
			o.NextAttemptAt = r.NextAttemptAt.Unix()
		}
		if r.Label != nil {
			o.Label = *r.Label
		}
		out.Orders = append(out.Orders, o)
	}
	return out, nil
}

// AdminRetryUndelivered tries one undelivered order, or every one, again now (owner and admin).
func (s *Server) AdminRetryUndelivered(ctx context.Context, req *corev1.AdminRetryUndeliveredRequest) (*corev1.AdminRetryUndeliveredResponse, error) {
	ctx = fromBot(ctx)
	n, err := s.dom.AdminRetryUndelivered(ctx, req.GetActorTelegramId(), req.GetOrderId())
	if err != nil {
		return nil, fail(err)
	}
	return &corev1.AdminRetryUndeliveredResponse{Count: int32(min(n, math.MaxInt32))}, nil //nolint:gosec // bounded
}

// AdminRefundUndelivered refunds an undelivered order to the buyer's wallet (owner and admin); an order that is not
// waiting any more is the outcome gone, and nothing is done.
func (s *Server) AdminRefundUndelivered(ctx context.Context, req *corev1.AdminRefundUndeliveredRequest) (*corev1.AdminRefundUndeliveredResponse, error) {
	ctx = fromBot(ctx)
	if err := s.needProvisioner(); err != nil {
		return nil, err
	}
	outcome, res, err := s.dom.AdminRefundUndelivered(ctx, req.GetActorTelegramId(), s.prov, req.GetOrderId(), req.GetReason(), req.GetNote())
	if err != nil {
		return nil, fail(err)
	}
	out := &corev1.AdminRefundUndeliveredResponse{Outcome: outcome}
	if res != nil && res.Order != nil {
		out.Amount, out.Currency, out.CommissionTaken = res.Order.Amount, res.Order.Currency, res.CommissionTaken
	}
	return out, nil
}
