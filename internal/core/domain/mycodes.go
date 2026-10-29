package domain

import (
	"context"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	buuid "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
)

// My codes (docs/03-bot-ux.md): the customer's own list of the codes they hold, which the bot, the Mini App and the dashboard's
// customer page show. A code is in it only when it can be attributed to the customer:
//
//   - a reward code is a discount code made for its buyer (discount_codes.user_id), so it is the buyer's and nobody else's;
//   - a gift code has no owner (staff make it and hand it out; core keeps a keyed hash of it, never the code), so only the
//     customer's own redemption of it can be attributed: it is listed as "used by you", with the last characters of the code;
//   - a code staff made for everyone (a discount code with no user), and a gift code nobody applied, belong to no one and are
//     never listed.
const (
	// CodeReward and CodeGift are the kinds of entry.
	CodeReward = store.CodeKindReward
	CodeGift   = store.CodeKindGift

	// The states of an entry. A reward code is unused until a paid order uses it; it is used for good then (even after its date),
	// else expired once its date has passed, else off when staff switched it off. A gift code is listed only once the customer
	// applied it, so it is always used.
	CodeUnused  = "unused"
	CodeUsed    = "used"
	CodeExpired = "expired"
	CodeOff     = "off"
)

// CustomerCode is one entry of a customer's list of codes, as the screens show it.
type CustomerCode struct {
	Kind string
	// ID is unique in a list (a reward code's own text, or a redemption's id).
	ID string
	// Code is a reward code, whole. A gift code is never stored: Tail is the last characters of the one the customer applied.
	Code, Tail string
	State      string
	// Percent (1 to 90), or Amount and Currency, is what a reward code takes off; a gift entry's Amount is what it added to the
	// wallet.
	Percent  int32
	Amount   int64
	Currency string
	// ExpiresAt is a reward code's last moment (nil: it does not expire). UsedAt is when it was used, or the gift code applied.
	ExpiresAt *time.Time
	UsedAt    *time.Time
	// ReceivedAt is when the reward code was made for the customer, or they applied the gift code.
	ReceivedAt time.Time
	// PlanNames are the names of the plan whose purchase gave a reward code, and OrderID that purchase (empty: it is gone).
	PlanNames map[string]string
	OrderID   string
	// OnlyPlanNames: a reward code made before reward codes were for any plan works on this one plan only (nil: any plan).
	OnlyPlanNames map[string]string
}

// codeState is the state of a stored entry at now.
func codeState(c *store.CodeRow, now time.Time) string {
	if c.Kind == CodeGift {
		return CodeUsed
	}
	switch {
	case c.UsedAt != nil || (c.MaxUses != nil && c.Used >= *c.MaxUses):
		return CodeUsed
	case c.ExpiresAt != nil && !now.Before(*c.ExpiresAt):
		return CodeExpired
	case !c.Enabled:
		return CodeOff
	}
	return CodeUnused
}

func customerCodeOf(c *store.CodeRow, now time.Time) CustomerCode {
	out := CustomerCode{Kind: c.Kind, ID: c.ID, Code: c.Code, Tail: c.Tail, State: codeState(c, now), ExpiresAt: c.ExpiresAt, UsedAt: c.UsedAt,
		ReceivedAt: c.ReceivedAt, PlanNames: c.PlanNames, OnlyPlanNames: c.OnlyPlanNames}
	if c.Percent != nil {
		out.Percent = *c.Percent
	}
	if c.Amount != nil {
		out.Amount = *c.Amount
	}
	if c.Currency != nil {
		out.Currency = *c.Currency
	}
	if c.OrderID != nil {
		out.OrderID = *c.OrderID
	}
	return out
}

// userCodes reads a page of one customer's codes. The customer is the only filter the store applies, so the page holds nothing that
// is not theirs.
func (s *Service) userCodes(ctx context.Context, userID string, p store.Page) ([]CustomerCode, int64, error) {
	rows, total, err := s.st.ListUserCodes(ctx, s.st.Conn(), userID, p)
	if err != nil {
		return nil, 0, err
	}
	now := time.Now()
	out := make([]CustomerCode, 0, len(rows))
	for i := range rows {
		out = append(out, customerCodeOf(&rows[i], now))
	}
	return out, total, nil
}

// CustomerCodes is a page of the customer's own codes, newest first, with how many they have in all. It serves the customer
// themselves (the bot, the Mini App): a banned account is refused as everywhere else (ErrUserBanned), an unknown one is
// store.ErrNotFound.
func (s *Service) CustomerCodes(ctx context.Context, userID string, p store.Page) ([]CustomerCode, int64, error) {
	if _, err := buuid.Parse(userID); err != nil {
		return nil, 0, invalid("user_id must be a uuid")
	}
	if _, err := s.activeUser(ctx, nil, userID); err != nil {
		return nil, 0, err
	}
	return s.userCodes(ctx, userID, p)
}

// UserCodes is the same list for staff reading a customer's page in the dashboard (the permission that page needs is checked by
// the caller): a banned customer's list is readable too. An unknown customer is store.ErrNotFound.
func (s *Service) UserCodes(ctx context.Context, userID string, p store.Page) ([]CustomerCode, int64, error) {
	if _, err := buuid.Parse(userID); err != nil {
		return nil, 0, invalid("user_id must be a uuid")
	}
	if _, err := s.st.GetUser(ctx, s.st.Conn(), userID); err != nil {
		return nil, 0, err
	}
	return s.userCodes(ctx, userID, p)
}
