package domain

import (
	"context"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// This file holds what the bot's button-driven admin menu needs beyond the
// calls it shared with the commands: today's numbers, the settings the caller
// may change, the servers' switches, the staff and broadcasts. Each call takes
// the actor's Telegram id, checks the role again (the bot only hides the
// buttons) and goes through the same functions as the dashboard, so the audit
// log looks the same whichever door was used.

// DayLine is one currency of a day; amounts are minor units and never added
// across currencies.
type DayLine struct {
	Currency string
	Revenue  int64
	Orders   int64
	Refunds  int64
}

// DayFigures is one local day of the sales report.
type DayFigures struct {
	Date         string
	Lines        []DayLine // sorted by currency
	NewCustomers int64
}

// TodayStats are today's and yesterday's numbers, read in the store's zone
// with the same code as the Reports page.
type TodayStats struct {
	TZ               string
	Today, Yesterday DayFigures
}

// AdminToday reads today's and yesterday's sales for the admin menu.
func (s *Service) AdminToday(ctx context.Context, actorTelegramID int64, now time.Time) (*TodayStats, error) {
	if _, err := s.requireStaff(ctx, actorTelegramID); err != nil {
		return nil, err
	}
	first, err := s.ReportRange(ctx, ReportRequest{Range: RangeToday}, now)
	if err != nil {
		return nil, err
	}
	today, err := time.Parse(dateLayout, first.To)
	if err != nil {
		return nil, err
	}
	yesterday := today.AddDate(0, 0, -1).Format(dateLayout)
	rng, err := s.ReportRange(ctx, ReportRequest{From: yesterday, To: first.To, TZ: first.TZ}, now)
	if err != nil {
		return nil, err
	}
	rep, err := s.SalesReport(ctx, rng)
	if err != nil {
		return nil, err
	}
	out := &TodayStats{TZ: first.TZ, Today: DayFigures{Date: first.To}, Yesterday: DayFigures{Date: yesterday}}
	for i := range rep.Buckets {
		b := &rep.Buckets[i]
		day := &out.Today
		if b.Date == yesterday {
			day = &out.Yesterday
		} else if b.Date != first.To {
			continue
		}
		day.NewCustomers = b.NewCustomers
		for _, cur := range rep.Currencies { // sorted
			c := b.Currencies[cur]
			if c == nil || (c.Revenue == 0 && c.Orders == 0 && c.Refunds.Amount == 0) {
				continue
			}
			day.Lines = append(day.Lines, DayLine{Currency: cur, Revenue: c.Revenue, Orders: c.Orders, Refunds: c.Refunds.Amount})
		}
	}
	return out, nil
}

// AdminSettingView is a setting as an actor may see it, with its value.
type AdminSettingView struct {
	SettingSpec
	Value string
}

// settingChangeable reports whether a role may change a setting: payment
// details and the server's own operation (its update channel) are the owner's,
// like in the dashboard (web.Allowed).
func settingChangeable(role string, sp SettingSpec) bool {
	if sp.Perm == permSecurity {
		return false // how staff sign in is changed in the dashboard, never in the bot
	}
	switch role {
	case "owner":
		return true
	case "admin":
		return !ownerOnlyPerm(sp.Perm)
	}
	return false
}

// AdminSettingSpecs lists the settings the actor may change, in the order the
// dashboard shows them, with their values. The ones they may not change (the
// owner's payment details for an admin) are left out, values included.
func (s *Service) AdminSettingSpecs(ctx context.Context, actorTelegramID int64) ([]AdminSettingView, error) {
	actor, err := s.requireStaff(ctx, actorTelegramID)
	if err != nil {
		return nil, err
	}
	values, err := s.Settings(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]AdminSettingView, 0, len(SettingSpecs))
	private := s.PrivateFeatures(ctx) // the affiliate program is the vendor's own: no other store lists its settings
	for _, sp := range SettingSpecs {
		if sp.Group == "affiliate" && !private {
			continue
		}
		// A list of rows (the tiers of the reseller targets, the wheel's prizes) is edited in the dashboard, not by typing JSON in a chat;
		// so are the customers' languages (a list with a default and beta ones, which the dashboard explains).
		if settingChangeable(actor.Role, sp) && sp.Kind != KindList && sp.Kind != KindLangs {
			out = append(out, AdminSettingView{SettingSpec: sp, Value: values[sp.Key]})
		}
	}
	return out, nil
}

// AdminListServers lists the 3x-ui servers with their last health check.
// Owner and admin, as on the dashboard (servers.write).
func (s *Service) AdminListServers(ctx context.Context, actorTelegramID int64, sa ServerAdmin) ([]Server, error) {
	if _, err := s.requireStaff(ctx, actorTelegramID); err != nil {
		return nil, err
	}
	list, err := sa.ListServers(ctx)
	if err != nil {
		return nil, err
	}
	return list.Servers, nil
}

// AdminSetServer switches a server on or off and sets its weight (0 keeps
// it), from what the registry holds now. The address, token and the private
// network switch are sent back as they are and the connection is not tried,
// so the panel is never contacted and no secret passes through the bot.
func (s *Service) AdminSetServer(ctx context.Context, actorTelegramID int64, sa ServerAdmin, id string, enabled bool, weight int32) (*Server, error) {
	actor, err := s.requireStaff(ctx, actorTelegramID)
	if err != nil {
		return nil, err
	}
	if weight < 0 {
		return nil, invalid("weight is a number from 1 up, or 0 to keep it")
	}
	list, err := sa.ListServers(ctx)
	if err != nil {
		return nil, err
	}
	cur, err := list.Find(id)
	if err != nil {
		return nil, err
	}
	in := ServerInput{
		Name: cur.Name, BaseURL: cur.BaseURL, SubBaseURL: cur.SubBaseURL,
		Weight: cur.Weight, Enabled: enabled, AllowPrivate: cur.AllowPrivate, SkipCheck: true,
	}
	if weight > 0 {
		in.Weight = weight
	}
	return s.UpdateServer(ctx, actor, sa, cur.ID, in)
}

// requireOwnerTG loads the actor and checks they are an active owner.
func (s *Service) requireOwnerTG(ctx context.Context, actorTelegramID int64) (*store.User, error) {
	actor, err := s.requireStaff(ctx, actorTelegramID)
	if err != nil {
		return nil, err
	}
	if err := requireOwner(actor); err != nil {
		return nil, err
	}
	return actor, nil
}

// StaffEntry is a staff member as the bot lists them.
type StaffEntry struct {
	UserID          string
	TelegramID      int64
	Username        string
	Role            string
	Status          string
	ConfiguredOwner bool
}

func (s *Service) staffEntry(u *store.User) StaffEntry {
	return StaffEntry{UserID: u.ID, TelegramID: u.TelegramID, Username: u.Username, Role: u.Role, Status: u.Status,
		ConfiguredOwner: s.IsConfiguredOwner(u)}
}

// AdminListStaff lists the staff. Owner only.
func (s *Service) AdminListStaff(ctx context.Context, actorTelegramID int64) ([]StaffEntry, error) {
	if _, err := s.requireOwnerTG(ctx, actorTelegramID); err != nil {
		return nil, err
	}
	rows, err := s.ListStaff(ctx, 30*time.Minute)
	if err != nil {
		return nil, err
	}
	out := make([]StaffEntry, 0, len(rows))
	for i := range rows {
		out = append(out, s.staffEntry(&rows[i].User))
	}
	return out, nil
}

// AdminAddStaff gives a role to the person with this Telegram id (who must
// have started the bot), or changes the role of someone who already has one.
// Owner only; the reason is kept in the audit log.
func (s *Service) AdminAddStaff(ctx context.Context, actorTelegramID, targetTelegramID int64, role, reason string) (*StaffEntry, error) {
	actor, err := s.requireOwnerTG(ctx, actorTelegramID)
	if err != nil {
		return nil, err
	}
	t, err := s.userByTelegram(ctx, targetTelegramID)
	if err != nil {
		return nil, err
	}
	if IsStaffRole(t.Role) {
		t, err = s.SetStaffRole(ctx, actor, t.ID, role, reason)
	} else {
		t, err = s.AddStaff(ctx, actor, t.ID, role, reason)
	}
	if err != nil {
		return nil, err
	}
	e := s.staffEntry(t)
	return &e, nil
}

// AdminRemoveStaff makes a staff member a customer again. Owner only.
func (s *Service) AdminRemoveStaff(ctx context.Context, actorTelegramID, targetTelegramID int64, reason string) error {
	actor, err := s.requireOwnerTG(ctx, actorTelegramID)
	if err != nil {
		return err
	}
	t, err := s.userByTelegram(ctx, targetTelegramID)
	if err != nil {
		return err
	}
	return s.RemoveStaff(ctx, actor, t.ID, reason)
}

// userByTelegram finds the account of a Telegram id; ErrNotFound when that
// person never started the bot.
func (s *Service) userByTelegram(ctx context.Context, telegramID int64) (*store.User, error) {
	if telegramID <= 0 {
		return nil, invalid("a Telegram id is a positive number")
	}
	return s.st.GetUserByTelegramID(ctx, s.st.Conn(), telegramID)
}

// BroadcastPreview is what a broadcast would be: who it reaches and the texts
// after checking.
type BroadcastPreview struct {
	Total, FA, EN int64
	Texts         map[string]string
	ButtonLabels  map[string]string
	BroadcastID   string // set when it was sent
}

// AdminBroadcast checks a broadcast and counts its audience; with send it
// also saves it and sends it now (the dashboard's two steps, one after the
// other). Nothing is saved when the texts or the button do not pass.
func (s *Service) AdminBroadcast(ctx context.Context, actorTelegramID int64, in BroadcastInput, send bool) (*BroadcastPreview, error) {
	actor, err := s.requireStaff(ctx, actorTelegramID)
	if err != nil {
		return nil, err
	}
	b, err := normalizeBroadcast(in)
	if err != nil {
		return nil, err
	}
	total, fa, en, err := s.BroadcastAudience(ctx, in.Segment, in.Language)
	if err != nil {
		return nil, err
	}
	out := &BroadcastPreview{Total: total, FA: fa, EN: en, Texts: b.Texts, ButtonLabels: b.ButtonLabels}
	if !send {
		return out, nil
	}
	if total == 0 {
		return nil, stateErr("nobody is in this audience")
	}
	made, err := s.CreateBroadcast(ctx, actor, in)
	if err != nil {
		return nil, err
	}
	sent, err := s.SendBroadcast(ctx, actor, made.ID, nil, made.Version)
	if err != nil {
		return nil, err
	}
	out.BroadcastID = sent.ID
	return out, nil
}
