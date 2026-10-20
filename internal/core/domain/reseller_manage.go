package domain

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	buuid "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
)

// A reseller manages the services they bought for their customers: pause and
// resume them, delete them, keep a private note on each, list and filter
// them, and see the customers who joined through their invite link. They act
// on their OWN services only, in the bot and in the Mini App. They cannot reset
// a service's traffic or add free days or free traffic: those stay with staff.
//
// Every function here checks, on the server, that the caller is an active
// reseller (with terms) and that the install's effective entitlements still
// include resellers (ResellerAccess), and answers store.ErrNotFound for a
// service that is not theirs. The changes go through the staff functions of
// manage.go (the same panel calls, audit entries and checks), with the
// reseller as the actor and the bot as the source.

// MaxNote is the longest private note on a service, in characters.
const MaxNote = 200

// maxServiceQuery is the longest search of a reseller's services, in characters.
const maxServiceQuery = 100

// Reasons the audit log records for what a reseller does.
const (
	reasonResellerPause  = "paused by the reseller"
	reasonResellerResume = "resumed by the reseller"
	reasonResellerDelete = "deleted by the reseller"
)

// ErrPausedByStaff: a member of the staff paused the service, so its
// reseller cannot resume it (they ask support). A service the reseller paused
// themselves can be resumed by them.
var ErrPausedByStaff = fmt.Errorf("%w: the service was paused by support", ErrState)

// resellerContext marks the changes made with ctx as the bot's: resellers use
// the bot and the Mini App only, so their audit entries say so whatever the
// caller put in ctx.
func resellerContext(ctx context.Context) context.Context {
	return store.WithSource(ctx, store.SourceBot)
}

// ResellerAccess loads the caller of a reseller call: an active user with the
// reseller role and terms, on an install whose effective entitlements include
// resellers. An unknown user is store.ErrNotFound; a banned one
// ErrUserBanned; anyone else (a customer, a former reseller, a reseller
// after resellers left the license) ErrForbidden, without a word about why.
func (s *Service) ResellerAccess(ctx context.Context, userID string) (*store.User, error) {
	if _, err := buuid.Parse(userID); err != nil {
		return nil, store.ErrNotFound
	}
	u, err := s.activeUser(ctx, nil, userID)
	if err != nil {
		return nil, err
	}
	if !isReseller(u) {
		return nil, ErrForbidden
	}
	if _, err := s.st.ResellerTerms(ctx, s.st.Conn(), u.ID); errors.Is(err, store.ErrNotFound) {
		return nil, ErrForbidden
	} else if err != nil {
		return nil, err
	}
	eff, err := s.Effective(ctx)
	if err != nil {
		return nil, err
	}
	if !eff.Entitlements.Resellers {
		return nil, ErrForbidden
	}
	return u, nil
}

// ownService loads one of the reseller's services that still exists; anything
// else (another user's, a deleted one, a malformed id) is store.ErrNotFound.
func (s *Service) ownService(ctx context.Context, u *store.User, id string) (*store.Subscription, error) {
	if _, err := buuid.Parse(id); err != nil {
		return nil, store.ErrNotFound
	}
	sub, err := s.st.GetSubscription(ctx, s.st.Conn(), id)
	if err != nil {
		return nil, err
	}
	if sub.UserID != u.ID || sub.Status == "deleted" {
		return nil, store.ErrNotFound
	}
	return sub, nil
}

// ResellerSetServiceEnabled pauses (enabled false) or resumes (true) one of
// the reseller's services: the panel client is turned off or on, as staff's
// SetSubscriptionEnabled does, and the audit log says who did it. Doing it
// twice changes nothing. A service staff paused cannot be resumed by its
// reseller (ErrPausedByStaff).
func (s *Service) ResellerSetServiceEnabled(ctx context.Context, prov Provisioner, resellerID, subID string, enabled bool) (*store.Subscription, error) {
	ctx = resellerContext(ctx)
	u, err := s.ResellerAccess(ctx, resellerID)
	if err != nil {
		return nil, err
	}
	sub, err := s.ownService(ctx, u, subID)
	if err != nil {
		return nil, err
	}
	reason := reasonResellerPause
	resuming := enabled && sub.Status == "disabled"
	if enabled {
		reason = reasonResellerResume
	}
	if resuming {
		if err := s.resumable(ctx, u, sub.ID); err != nil {
			return nil, err
		}
	}
	if _, err := s.SetSubscriptionEnabled(ctx, u, prov, sub.ID, enabled, reason); err != nil {
		return nil, err
	}
	if resuming { // the right status (expired, used up...) comes from the panel
		if err := s.SyncSubscription(ctx, prov, sub.ID); err != nil {
			s.cfg.Log.Warn("reseller resumed a service; reading its usage failed, the next sync will", "subscription_id", sub.ID, "err", err)
		}
	}
	return s.st.GetSubscription(ctx, s.st.Conn(), sub.ID)
}

// resumable: the reseller may turn the paused service back on only when they
// were the one who paused it. The audit log is the record: its latest pause
// of the service must be theirs. A pause nobody recorded (older than the log,
// or made by the system) is left to staff.
func (s *Service) resumable(ctx context.Context, u *store.User, subID string) error {
	last, err := s.st.LastSubscriptionToggle(ctx, s.st.Conn(), subID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return ErrPausedByStaff
	case err != nil:
		return err
	case last.Action != "subscription.disable" || last.ActorID == nil || *last.ActorID != u.ID:
		return ErrPausedByStaff
	}
	return nil
}

// ResellerDeleteService deletes one of the reseller's services: the client is
// removed from the panel and the service is marked deleted, as staff's
// DeleteSubscription does (the row stays: orders point at it). Nothing is
// refunded. Deleting a service already deleted is not an error. A service
// still being delivered, or with a renewal being applied, is refused
// (ErrState) until it settles.
func (s *Service) ResellerDeleteService(ctx context.Context, prov Provisioner, resellerID, subID string) error {
	ctx = resellerContext(ctx)
	u, err := s.ResellerAccess(ctx, resellerID)
	if err != nil {
		return err
	}
	if _, err := buuid.Parse(subID); err != nil {
		return store.ErrNotFound
	}
	sub, err := s.st.GetSubscription(ctx, s.st.Conn(), subID)
	if err != nil {
		return err
	}
	if sub.UserID != u.ID {
		return store.ErrNotFound
	}
	return s.DeleteSubscription(ctx, u, prov, sub.ID, reasonResellerDelete) // nil when it is deleted already
}

// normalizeNote checks a service's note: one line of plain text, at most
// MaxNote characters. Empty clears it (nil).
func normalizeNote(note string) (*string, error) {
	note = strings.TrimSpace(note)
	switch {
	case note == "":
		return nil, nil
	case !utf8.ValidString(note) || strings.ContainsFunc(note, unicode.IsControl):
		return nil, invalid("a note is one line of plain text")
	case utf8.RuneCountInString(note) > MaxNote:
		return nil, invalid("a note is at most %d characters", MaxNote)
	}
	return &note, nil
}

// ResellerSetNote sets (or, empty, clears) the private note on one of the
// reseller's services.
func (s *Service) ResellerSetNote(ctx context.Context, resellerID, subID, note string) (*store.Subscription, error) {
	u, err := s.ResellerAccess(ctx, resellerID)
	if err != nil {
		return nil, err
	}
	n, err := normalizeNote(note)
	if err != nil {
		return nil, err
	}
	sub, err := s.ownService(ctx, u, subID)
	if err != nil {
		return nil, err
	}
	if err := s.st.SetSubscriptionNote(ctx, s.st.Conn(), sub.ID, n); err != nil {
		return nil, err
	}
	sub.Note = n
	return sub, nil
}

// ResellerSetLabel names one of the reseller's services ("" clears the name),
// like SetSubscriptionLabel, for callers that must also be refused once
// resellers left the license (the Mini App).
func (s *Service) ResellerSetLabel(ctx context.Context, resellerID, subID, label string) (*store.Subscription, error) {
	if _, err := s.ResellerAccess(ctx, resellerID); err != nil {
		return nil, err
	}
	return s.SetSubscriptionLabel(ctx, resellerID, subID, label)
}

// ResellerServiceFilter narrows the reseller's list of services. Status is ""
// (all) or one of store.ServiceBuckets; Query matches part of a name or the
// end of a service number (#a1b2c3).
type ResellerServiceFilter struct {
	Status, Query string
}

// ResellerServices is a page of the reseller's services with how many match,
// and how many services they have in each status whatever the filter.
type ResellerServices struct {
	Items  []store.Subscription
	Total  int64
	Counts store.ServiceCounts
}

// ListResellerServices lists a page of the reseller's own services (the
// deleted ones left out), newest first, with the counts per status. Each
// service carries its usage (traffic used and total, expiry).
func (s *Service) ListResellerServices(ctx context.Context, resellerID string, f ResellerServiceFilter, p store.Page) (*ResellerServices, error) {
	u, err := s.ResellerAccess(ctx, resellerID)
	if err != nil {
		return nil, err
	}
	if f.Status != "" {
		if _, ok := store.ServiceBuckets[f.Status]; !ok {
			return nil, invalid("unknown status %q", f.Status)
		}
	}
	f.Query = strings.TrimSpace(f.Query)
	if utf8.RuneCountInString(f.Query) > maxServiceQuery {
		return nil, invalid("a search is at most %d characters", maxServiceQuery)
	}
	items, total, err := s.st.ListUserServices(ctx, s.st.Conn(), u.ID, f.Query, f.Status, p)
	if err != nil {
		return nil, err
	}
	counts, err := s.st.CountUserServices(ctx, s.st.Conn(), u.ID)
	if err != nil {
		return nil, err
	}
	return &ResellerServices{Items: items, Total: total, Counts: counts}, nil
}

// InvitedCustomers is a page of the people who joined through the reseller's
// invite link, with how many there are.
type InvitedCustomers struct {
	Items []store.InvitedCustomer
	Total int64
}

// ListInvitedCustomers lists, newest first, the users who joined through THIS
// reseller's link: who they are on Telegram, when they joined, how many paid
// orders they made, what they spent per currency, and the commission it paid
// the reseller. Nobody else is listed and nothing else about them is shown.
func (s *Service) ListInvitedCustomers(ctx context.Context, resellerID string, p store.Page) (*InvitedCustomers, error) {
	u, err := s.ResellerAccess(ctx, resellerID)
	if err != nil {
		return nil, err
	}
	items, total, err := s.st.ListInvitedCustomers(ctx, s.st.Conn(), u.ID, p)
	if err != nil {
		return nil, err
	}
	return &InvitedCustomers{Items: items, Total: total}, nil
}
