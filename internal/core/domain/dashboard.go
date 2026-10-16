package domain

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
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

// InitialPasswordLen is the length of a generated initial password.
const InitialPasswordLen = 16

// initialPasswordAlphabet leaves out the characters that read alike (0 O o 1
// l I), so a password copied by eye from a terminal is typed right.
const initialPasswordAlphabet = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// NewInitialPassword makes a random initial password from crypto/rand (about
// 93 bits).
func NewInitialPassword() (string, error) {
	max := big.NewInt(int64(len(initialPasswordAlphabet)))
	b := make([]byte, InitialPasswordLen)
	for i := range b {
		n, err := rand.Int(rand.Reader, max) // uniform: no modulo bias
		if err != nil {
			return "", fmt.Errorf("initial password: %w", err)
		}
		b[i] = initialPasswordAlphabet[n.Int64()]
	}
	return string(b), nil
}

// InitialLogin is a staff member's way into the dashboard issued from the
// server's command line.
type InitialLogin struct {
	Username string
	Password string // shown once, never stored in the clear
}

// IssueInitialPassword gives the configured owner a way into the dashboard
// without ever having sent /start to the bot: the account is created if it
// is missing, made the owner, and given a new random password (the owner's
// username is kept when they have a password login, else "owner"). The
// login is marked must_change: with it the owner opens only a restricted
// session, which asks for a new password and an authenticator app. Running
// it again resets the login (the password, the authenticator app, a lock)
// and ends every session of the owner. The password is returned once; the
// audit entry (dashboard.password.reset_cli, no actor) never holds it.
func (s *Service) IssueInitialPassword(ctx context.Context) (*InitialLogin, error) {
	if !s.OwnerConfigured() {
		return nil, invalid("BOBRES_ADMIN_TELEGRAM_ID is not set: no owner to give a password to")
	}
	u, err := s.UpsertUser(ctx, UpsertUserParams{TelegramID: s.cfg.OwnerTelegramID})
	if err != nil {
		return nil, err
	}
	if _, err := s.StaffUser(ctx, u.ID); err != nil {
		return nil, fmt.Errorf("the owner (Telegram id %d) is not an active staff member (a reseller must be removed as a reseller first): %w",
			s.cfg.OwnerTelegramID, err)
	}
	password, err := NewInitialPassword()
	if err != nil {
		return nil, err
	}
	hash, err := webauth.HashPassword(password)
	if err != nil {
		return nil, err
	}
	var out InitialLogin
	err = s.st.WithTx(ctx, func(tx pgx.Tx) error {
		var name string
		// "owner" may be another member's username: then owner-<n>.
		for i := 0; ; i++ {
			candidate := "owner"
			if i > 0 {
				candidate = fmt.Sprintf("owner-%d", i+1)
			}
			// A savepoint: a taken username must not poison the transaction.
			sp, err := tx.Begin(ctx)
			if err != nil {
				return err
			}
			name, err = s.st.SetInitialCredentials(ctx, sp, u.ID, candidate, hash)
			if errors.Is(err, store.ErrUsernameTaken) && i < 20 {
				_ = sp.Rollback(ctx)
				continue
			}
			if err != nil {
				_ = sp.Rollback(ctx)
				return err
			}
			if err := sp.Commit(ctx); err != nil {
				return err
			}
			break
		}
		revoked, err := s.st.RevokeUserSessions(ctx, tx, u.ID, 0)
		if err != nil {
			return err
		}
		out = InitialLogin{Username: name, Password: password}
		return s.auditChange(ctx, tx, nil, "dashboard.password.reset_cli", "dashboard", u.ID, nil,
			map[string]any{"username": name, "must_change": true, "sessions_ended": revoked}, "")
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}
