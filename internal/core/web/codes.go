package web

import (
	"net/http"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	bmoney "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/money"
)

// A customer's codes (docs/04-dashboard.md, the customer page's Codes tab; the Mini App's My codes is the same list). The list holds the
// reward codes issued to the customer and the gift codes they applied, and nothing that cannot be attributed to them (see
// domain.CustomerCodes). The dashboard shows it read only, to staff who may read the customer's page.

// customerCodeJSON is one entry of the list, in the dashboard and in the Mini App.
type customerCodeJSON struct {
	// ID is unique in a list: a reward code's own text, or a redemption's id.
	ID   string `json:"id"`
	Kind string `json:"kind"` // reward | gift
	// Code is a reward code, whole. A gift code is never stored: Tail is the last characters of the one the customer applied.
	Code  string `json:"code,omitempty"`
	Tail  string `json:"tail,omitempty"`
	State string `json:"state"` // unused | used | expired | off (a gift code is always used)
	// Percent, or Amount in Currency (minor units, with their Scale), is what a reward code takes off; a gift entry's amount is what it
	// added to the wallet.
	Percent  int32  `json:"percent,omitempty"`
	Amount   int64  `json:"amount,omitempty"`
	Currency string `json:"currency,omitempty"`
	Scale    int    `json:"scale"`
	// Unix seconds: when a reward code ends (null: never), when it was used or the gift code applied (null: not yet), and when the
	// customer received it.
	ExpiresAt  *int64 `json:"expires_at"`
	UsedAt     *int64 `json:"used_at"`
	ReceivedAt int64  `json:"received_at"`
	// Plan: the names of the plan whose purchase gave a reward code, OrderID that purchase, and OnlyPlan the one plan a code made before
	// reward codes were for any plan works on.
	Plan     map[string]string `json:"plan,omitempty"`
	OrderID  string            `json:"order_id,omitempty"`
	OnlyPlan map[string]string `json:"only_plan,omitempty"`
}

func customerCodesOf(codes []domain.CustomerCode) []customerCodeJSON {
	out := make([]customerCodeJSON, 0, len(codes))
	for i := range codes {
		c := &codes[i]
		it := customerCodeJSON{ID: c.ID, Kind: c.Kind, Code: c.Code, Tail: c.Tail, State: c.State, Percent: c.Percent, Amount: c.Amount,
			Currency: c.Currency, ReceivedAt: c.ReceivedAt.Unix(), Plan: c.PlanNames, OrderID: c.OrderID, OnlyPlan: c.OnlyPlanNames}
		if it.Currency != "" {
			it.Scale = max(bmoney.Scale(it.Currency), 0)
		}
		if c.ExpiresAt != nil {
			at := c.ExpiresAt.Unix()
			it.ExpiresAt = &at
		}
		if c.UsedAt != nil {
			at := c.UsedAt.Unix()
			it.UsedAt = &at
		}
		out = append(out, it)
	}
	return out
}

// listCodes: GET /api/v1/codes?user=<customer id>&page=&size= is the codes of one customer, newest first (the customer page's Codes
// tab). The customer is required: the list has no meaning without one, and it never gives another's.
func (s *Server) listCodes(w http.ResponseWriter, r *http.Request) {
	p, page, size := pageOf(r)
	userID, ok := queryID(r, "user")
	if !ok || userID == "" {
		writeError(w, http.StatusBadRequest, "invalid", "a customer is required (?user=)")
		return
	}
	codes, total, err := s.cfg.Domain.UserCodes(r.Context(), userID, p)
	if err != nil {
		s.fail(w, "list codes", err)
		return
	}
	writeJSON(w, http.StatusOK, list{customerCodesOf(codes), total, page, size})
}
