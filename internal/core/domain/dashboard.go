package domain

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/webauth"
)

// LoginLinkTTL is how long a dashboard login link works (once).
const LoginLinkTTL = 2 * time.Minute

// IsStaffRole reports the roles that may use the dashboard.
func IsStaffRole(role string) bool {
	switch role {
	case "owner", "admin", "support":
		return true
	}
	return false
}

// StaffUser returns an active staff member by user id; anyone else is
// ErrForbidden.
func (s *Service) StaffUser(ctx context.Context, userID string) (*store.User, error) {
	u, err := s.st.GetUser(ctx, s.st.Conn(), userID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrForbidden
	}
	if err != nil {
		return nil, err
	}
	if u.Status != "active" || !IsStaffRole(u.Role) {
		return nil, ErrForbidden
	}
	return u, nil
}

// CreateLoginLink makes a one-time dashboard login token for a staff member
// (by Telegram id): random, stored only as a hash, working once within
// LoginLinkTTL. The bot sends it as a link; the server CLI prints it.
func (s *Service) CreateLoginLink(ctx context.Context, telegramID int64) (string, time.Time, error) {
	u, err := s.st.GetUserByTelegramID(ctx, s.st.Conn(), telegramID)
	if errors.Is(err, store.ErrNotFound) {
		return "", time.Time{}, ErrForbidden
	}
	if err != nil {
		return "", time.Time{}, err
	}
	if u.Status != "active" || !IsStaffRole(u.Role) {
		return "", time.Time{}, ErrForbidden
	}
	token, hash, err := webauth.NewToken()
	if err != nil {
		return "", time.Time{}, err
	}
	expires := time.Now().Add(LoginLinkTTL)
	err = s.st.WithTx(ctx, func(tx pgx.Tx) error {
		if err := s.st.CreateLoginLink(ctx, tx, hash, u.ID, expires); err != nil {
			return err
		}
		// Who asked for a way in, and where (the entry's source: bot or CLI).
		return s.audit(ctx, tx, u, "dashboard.link_created", "dashboard", u.ID, nil, "")
	})
	if err != nil {
		return "", time.Time{}, err
	}
	return token, expires, nil
}

// Overview is the dashboard's first page.
type Overview struct {
	store.Overview
	PendingPayments int64 // -1 when the payments service did not answer
	PanelHealthy    bool
	PanelDetail     string
	Recent          []store.RecentOrder
}

// DashboardOverview gathers the figures; an unreachable backend degrades the
// result instead of failing it.
func (s *Service) DashboardOverview(ctx context.Context, pay PaymentsClient, prov Provisioner) (*Overview, error) {
	o, err := s.st.OverviewStats(ctx, s.st.Conn(), time.Now())
	if err != nil {
		return nil, err
	}
	recent, err := s.st.RecentOrders(ctx, s.st.Conn(), 10)
	if err != nil {
		return nil, err
	}
	out := &Overview{Overview: *o, PendingPayments: -1, Recent: recent}
	if pay != nil {
		if pending, err := pay.ListPending(ctx, 200); err == nil {
			out.PendingPayments = int64(len(pending))
		}
	}
	if prov != nil {
		if ok, detail, err := prov.Health(ctx); err != nil {
			out.PanelDetail = "provisioner unreachable"
		} else {
			out.PanelHealthy, out.PanelDetail = ok, detail
		}
	}
	return out, nil
}
