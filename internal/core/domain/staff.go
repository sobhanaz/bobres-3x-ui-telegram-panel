package domain

import (
	"context"
	"encoding/hex"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// StaffRoles are the roles the owner can give, least powerful first.
var StaffRoles = []string{"support", "admin", "owner"}

// cleanOwnerIDs drops the ids that are not positive and the repeats, keeping
// the order (the first is the main owner).
func cleanOwnerIDs(ids []int64) []int64 {
	var out []int64
	for _, id := range ids {
		if id > 0 && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

// OwnerIDs lists the Telegram ids of the root admins, the main owner first.
func (s *Service) OwnerIDs() []int64 { return slices.Clone(s.cfg.OwnerTelegramIDs) }

// PrimaryOwnerID is the main owner's Telegram id: the first root admin (0:
// the server names none).
func (s *Service) PrimaryOwnerID() int64 {
	if len(s.cfg.OwnerTelegramIDs) == 0 {
		return 0
	}
	return s.cfg.OwnerTelegramIDs[0]
}

// IsConfiguredOwner reports whether u is one of the owners named in the
// server's configuration (BOBRES_ADMIN_TELEGRAM_ID, one id or several): the
// root of trust, changed only there (bobres admin owners, bobres menu), never
// from the dashboard or the bot.
func (s *Service) IsConfiguredOwner(u *store.User) bool {
	return u != nil && slices.Contains(s.cfg.OwnerTelegramIDs, u.TelegramID)
}

// OwnerConfigured reports whether the server names an owner.
func (s *Service) OwnerConfigured() bool { return len(s.cfg.OwnerTelegramIDs) > 0 }

// CanGrantOwner: giving or taking the owner role is a configured owner's
// decision (any owner's on an install that names none).
func (s *Service) CanGrantOwner(actor *store.User) bool {
	return actor.Role == "owner" && actor.Status == "active" && (!s.OwnerConfigured() || s.IsConfiguredOwner(actor))
}

// ListStaff lists the staff with their login state.
func (s *Service) ListStaff(ctx context.Context, idle time.Duration) ([]store.StaffRow, error) {
	return s.st.ListStaff(ctx, s.st.Conn(), idle)
}

// requireOwner checks the actor again, whatever the caller checked.
func requireOwner(actor *store.User) error {
	if actor == nil || actor.Role != "owner" || actor.Status != "active" {
		return ErrForbidden
	}
	return nil
}

func needReasonText(reason string) (string, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return "", invalid("a reason is required")
	}
	if len([]rune(reason)) > 500 {
		return "", invalid("the reason is too long (at most 500 characters)")
	}
	return reason, nil
}

// staffTarget loads the member an owner acts on: never themselves, never
// the configured owner.
func (s *Service) staffTarget(ctx context.Context, actor *store.User, id string) (*store.User, error) {
	if err := requireOwner(actor); err != nil {
		return nil, err
	}
	t, err := s.st.GetUser(ctx, s.st.Conn(), id)
	if err != nil {
		return nil, err
	}
	if t.ID == actor.ID {
		return nil, stateErr("you cannot change your own staff account here; use My account")
	}
	if s.IsConfiguredOwner(t) {
		return nil, stateErr("a store owner is set in the server settings (bobres admin owners), not here")
	}
	return t, nil
}

// AddStaff makes a customer a staff member. They must have started the bot
// (so they have an account and can get a login link) and not be banned.
func (s *Service) AddStaff(ctx context.Context, actor *store.User, userID, role, reason string) (*store.User, error) {
	reason, err := needReasonText(reason)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(StaffRoles, role) {
		return nil, invalid("role must be support, admin or owner")
	}
	t, err := s.staffTarget(ctx, actor, userID)
	if err != nil {
		return nil, err
	}
	switch {
	case IsStaffRole(t.Role):
		return nil, stateErr("this person is already staff; change their role instead")
	case t.Role != "user":
		return nil, stateErr("only customers can become staff")
	case t.Status != "active":
		return nil, stateErr("a banned customer cannot become staff; unban them first")
	case role == "owner" && !s.CanGrantOwner(actor):
		return nil, stateErr("only the store owner can add another owner")
	}
	// Every staff member but the main owner the server names (the first of
	// its owners) takes a seat, an owner too (or two seat holders promoted to
	// owner would empty them).
	if err := s.requireStaffSeat(ctx); err != nil {
		return nil, err
	}
	err = s.st.WithTx(ctx, func(tx pgx.Tx) error {
		if err := s.st.SetRole(ctx, tx, t.ID, "user", role); errors.Is(err, store.ErrNotFound) {
			return stateErr("this person changed meanwhile; reload and try again")
		} else if err != nil {
			return err
		}
		return s.auditChange(ctx, tx, actor, "staff.add", "staff", t.ID,
			map[string]string{"role": "user"}, map[string]string{"role": role}, reason)
	})
	if err != nil {
		return nil, err
	}
	t.Role = role
	return t, nil
}

// SetStaffRole changes a staff member's role. Their sessions end, so the
// dashboard reloads with what the new role may do.
func (s *Service) SetStaffRole(ctx context.Context, actor *store.User, id, role, reason string) (*store.User, error) {
	reason, err := needReasonText(reason)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(StaffRoles, role) {
		return nil, invalid("role must be support, admin or owner")
	}
	t, err := s.staffTarget(ctx, actor, id)
	if err != nil {
		return nil, err
	}
	if !IsStaffRole(t.Role) {
		return nil, stateErr("this person is not staff")
	}
	if t.Role == role {
		return nil, stateErr("they already have this role")
	}
	if (role == "owner" || t.Role == "owner") && !s.CanGrantOwner(actor) {
		return nil, stateErr("only the store owner can give or take the owner role")
	}
	// Admin, support and owner each take a seat: a change between them
	// neither takes a new one nor frees one, so there is nothing to check.
	err = s.st.WithTx(ctx, func(tx pgx.Tx) error {
		if err := s.keepAnOwner(ctx, tx, t); err != nil {
			return err
		}
		if err := s.st.SetRole(ctx, tx, t.ID, t.Role, role); errors.Is(err, store.ErrNotFound) {
			return stateErr("this person changed meanwhile; reload and try again")
		} else if err != nil {
			return err
		}
		if _, err := s.st.RevokeUserSessions(ctx, tx, t.ID, 0); err != nil {
			return err
		}
		return s.auditChange(ctx, tx, actor, "staff.role", "staff", t.ID,
			map[string]string{"role": t.Role}, map[string]string{"role": role}, reason)
	})
	if err != nil {
		return nil, err
	}
	t.Role = role
	return t, nil
}

// keepAnOwner refuses a change that would leave no active owner (with the
// owners locked, so two owners cannot both step down at the same moment).
func (s *Service) keepAnOwner(ctx context.Context, tx pgx.Tx, target *store.User) error {
	if target.Role != "owner" {
		return nil
	}
	owners, err := s.st.LockActiveOwners(ctx, tx)
	if err != nil {
		return err
	}
	if len(owners) <= 1 && slices.Contains(owners, target.ID) {
		return stateErr("the store needs at least one owner")
	}
	return nil
}

// RemoveStaff makes a staff member a customer again: their sessions end,
// their password login and unused login links are deleted.
func (s *Service) RemoveStaff(ctx context.Context, actor *store.User, id, reason string) error {
	reason, err := needReasonText(reason)
	if err != nil {
		return err
	}
	t, err := s.staffTarget(ctx, actor, id)
	if err != nil {
		return err
	}
	if !IsStaffRole(t.Role) {
		return stateErr("this person is not staff")
	}
	if t.Role == "owner" && !s.CanGrantOwner(actor) {
		return stateErr("only the store owner can remove an owner")
	}
	return s.st.WithTx(ctx, func(tx pgx.Tx) error {
		if err := s.keepAnOwner(ctx, tx, t); err != nil {
			return err
		}
		if err := s.st.SetRole(ctx, tx, t.ID, t.Role, "user"); errors.Is(err, store.ErrNotFound) {
			return stateErr("this person changed meanwhile; reload and try again")
		} else if err != nil {
			return err
		}
		if err := s.endAccess(ctx, tx, t.ID); err != nil {
			return err
		}
		return s.auditChange(ctx, tx, actor, "staff.remove", "staff", t.ID,
			map[string]string{"role": t.Role}, map[string]string{"role": "user"}, reason)
	})
}

// endAccess deletes a member's password login and unused login links and
// ends their sessions.
func (s *Service) endAccess(ctx context.Context, tx pgx.Tx, userID string) error {
	if err := s.st.DeleteCredentials(ctx, tx, userID); err != nil {
		return err
	}
	if err := s.st.ExpireLoginLinks(ctx, tx, userID); err != nil {
		return err
	}
	_, err := s.st.RevokeUserSessions(ctx, tx, userID, 0)
	return err
}

// staffMember is staffTarget for someone who must be staff.
func (s *Service) staffMember(ctx context.Context, actor *store.User, id string) (*store.User, error) {
	t, err := s.staffTarget(ctx, actor, id)
	if err != nil {
		return nil, err
	}
	if !IsStaffRole(t.Role) {
		return nil, store.ErrNotFound
	}
	return t, nil
}

// StaffSessions lists a member's live sessions.
func (s *Service) StaffSessions(ctx context.Context, actor *store.User, id string, idle time.Duration) ([]store.WebSession, error) {
	if err := requireOwner(actor); err != nil {
		return nil, err
	}
	t, err := s.st.GetUser(ctx, s.st.Conn(), id)
	if err != nil {
		return nil, err
	}
	if !IsStaffRole(t.Role) {
		return nil, store.ErrNotFound
	}
	return s.st.ListSessions(ctx, s.st.Conn(), t.ID, idle)
}

// RevokeStaffSessions logs a member out everywhere and says how many live
// sessions (used within idle) ended.
func (s *Service) RevokeStaffSessions(ctx context.Context, actor *store.User, id, reason string, idle time.Duration) (int64, error) {
	reason, err := needReasonText(reason)
	if err != nil {
		return 0, err
	}
	t, err := s.staffMember(ctx, actor, id)
	if err != nil {
		return 0, err
	}
	var n int64
	err = s.st.WithTx(ctx, func(tx pgx.Tx) error {
		if n, err = s.st.RevokeUserSessions(ctx, tx, t.ID, idle); err != nil {
			return err
		}
		return s.auditChange(ctx, tx, actor, "staff.sessions.revoke", "staff", t.ID, nil, map[string]int64{"count": n}, reason)
	})
	return n, err
}

// RevokeStaffSession ends one of a member's sessions.
func (s *Service) RevokeStaffSession(ctx context.Context, actor *store.User, id string, idHash []byte) error {
	t, err := s.staffMember(ctx, actor, id)
	if err != nil {
		return err
	}
	return s.st.WithTx(ctx, func(tx pgx.Tx) error {
		ok, err := s.st.RevokeUserSession(ctx, tx, t.ID, idHash)
		if err != nil {
			return err
		}
		if !ok {
			return store.ErrNotFound
		}
		return s.auditChange(ctx, tx, actor, "staff.session.revoke", "staff", t.ID, nil,
			map[string]string{"session": hex.EncodeToString(idHash)[:12]}, "")
	})
}

// ResetStaffPassword deletes a member's password login (they get back in
// with a login link from the bot and set a new one) and logs them out.
func (s *Service) ResetStaffPassword(ctx context.Context, actor *store.User, id, reason string) error {
	reason, err := needReasonText(reason)
	if err != nil {
		return err
	}
	t, err := s.staffMember(ctx, actor, id)
	if err != nil {
		return err
	}
	return s.st.WithTx(ctx, func(tx pgx.Tx) error {
		if _, err := s.st.CredentialsFor(ctx, tx, t.ID); errors.Is(err, store.ErrNotFound) {
			return stateErr("they have no password login")
		} else if err != nil {
			return err
		}
		if err := s.st.DeleteCredentials(ctx, tx, t.ID); err != nil {
			return err
		}
		if _, err := s.st.RevokeUserSessions(ctx, tx, t.ID, 0); err != nil {
			return err
		}
		return s.auditChange(ctx, tx, actor, "staff.password.reset", "staff", t.ID, nil, nil, reason)
	})
}

// UnlockStaff lifts the lock five wrong password logins put on a member.
func (s *Service) UnlockStaff(ctx context.Context, actor *store.User, id string) error {
	t, err := s.staffMember(ctx, actor, id)
	if err != nil {
		return err
	}
	return s.st.WithTx(ctx, func(tx pgx.Tx) error {
		ok, err := s.st.UnlockCredentials(ctx, tx, t.ID)
		if err != nil {
			return err
		}
		if !ok {
			return stateErr("their password login is not locked")
		}
		return s.auditChange(ctx, tx, actor, "staff.unlock", "staff", t.ID, nil, nil, "")
	})
}

// EnsureOwner gives every configured owner the owner role if they have an
// account without it: at start-up, so changing the owners in the server
// settings takes effect without waiting for their next visit to the bot.
func (s *Service) EnsureOwner(ctx context.Context) error {
	for _, id := range s.cfg.OwnerTelegramIDs {
		u, err := s.st.GetUserByTelegramID(ctx, s.st.Conn(), id)
		if errors.Is(err, store.ErrNotFound) {
			continue // they have not started the bot yet
		}
		if err != nil {
			return err
		}
		if err := s.promoteOwner(ctx, u); err != nil {
			return err
		}
	}
	return nil
}

// promoteOwner makes a configured owner an active owner, audited as the
// system acting on the configuration. A reseller is left as they are (their
// debt and customers would be orphaned): staff remove them as a reseller
// first, and they become the owner on their next visit.
func (s *Service) promoteOwner(ctx context.Context, u *store.User) error {
	if u.Role == "owner" && u.Status == "active" {
		return nil
	}
	if isReseller(u) {
		s.cfg.Log.Error("the configured owner is a reseller: not made owner; remove them as a reseller in the dashboard first",
			"telegram_id", u.TelegramID)
		return nil
	}
	err := s.st.WithTx(ctx, func(tx pgx.Tx) error {
		if err := s.st.SetUserRole(ctx, tx, u.ID, "owner", "active"); err != nil {
			return err
		}
		return s.auditChange(ctx, tx, nil, "staff.owner_bootstrap", "staff", u.ID,
			map[string]string{"role": u.Role, "status": u.Status},
			map[string]string{"role": "owner", "status": "active", "source": "server settings"}, "")
	})
	if err != nil {
		return err
	}
	u.Role, u.Status = "owner", "active"
	return nil
}
