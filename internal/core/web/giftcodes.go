package web

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// Gift codes in the dashboard (docs/04-dashboard.md, "Gift codes"): staff with
// discounts.write, as for the discount codes, make, list, switch off and read
// the redemptions of codes. Customers apply them in the bot (the RedeemGiftCode
// RPC). A code is in exactly one answer, the one that made it.

// maxGiftCSVRows caps one gift code export (a filter narrows a bigger one).
const maxGiftCSVRows = 100_000

type giftCodeJSON struct {
	ID               string     `json:"id"`
	Tail             string     `json:"tail"` // the last four characters of the code: all that is kept of it
	Generated        bool       `json:"generated"`
	Amount           money      `json:"amount"`
	MaxRedemptions   int        `json:"max_redemptions"`
	PerCustomerLimit int        `json:"per_customer_limit"`
	Redeemed         int        `json:"redeemed"`
	ExpiresAt        *int64     `json:"expires_at"`
	Enabled          bool       `json:"enabled"`
	Status           string     `json:"status"` // active | disabled | expired | used_up
	Note             string     `json:"note"`
	CreatedAt        int64      `json:"created_at"`
	CreatedBy        *userBrief `json:"created_by"`
	LastRedeemedAt   *int64     `json:"last_redeemed_at"`
}

func giftCodeOf(g *store.GiftCode, now time.Time, users map[string]*userBrief) giftCodeJSON {
	out := giftCodeJSON{ID: g.ID, Tail: g.Tail, Generated: g.Generated, Amount: money{g.Amount, g.Currency},
		MaxRedemptions: g.MaxRedemptions, PerCustomerLimit: g.PerCustomerLimit, Redeemed: g.Redeemed,
		ExpiresAt: unix(g.ExpiresAt), Enabled: g.Enabled, Status: domain.GiftCodeStatus(g, now), Note: g.Note,
		CreatedAt: g.CreatedAt.Unix(), LastRedeemedAt: unix(g.LastRedeemedAt)}
	if g.CreatedBy != nil {
		out.CreatedBy = users[*g.CreatedBy]
	}
	return out
}

// giftCodeItems reads codes for the list, with who made each.
func (s *Server) giftCodeItems(ctx context.Context, codes []store.GiftCode) []giftCodeJSON {
	ids := make([]string, 0, len(codes))
	for i := range codes {
		if codes[i].CreatedBy != nil {
			ids = append(ids, *codes[i].CreatedBy)
		}
	}
	users := s.usersByID(ctx, ids)
	now := s.now()
	items := make([]giftCodeJSON, 0, len(codes))
	for i := range codes {
		items = append(items, giftCodeOf(&codes[i], now, users))
	}
	return items
}

func giftFilter(r *http.Request) store.GiftCodeFilter {
	q := r.URL.Query()
	return store.GiftCodeFilter{Status: q.Get("status"), Query: strings.TrimSpace(q.Get("q"))}
}

// listGiftCodes: GET /api/v1/gift-codes?status=&q=&page=&size= (newest first),
// with whether customers can use gift codes now (the owner's switch) and
// whether core can make them at all (it needs its master key).
func (s *Server) listGiftCodes(w http.ResponseWriter, r *http.Request) {
	p, page, size := pageOf(r)
	codes, total, err := s.cfg.Domain.ListGiftCodes(r.Context(), giftFilter(r), p)
	if err != nil {
		s.fail(w, "list gift codes", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": s.giftCodeItems(r.Context(), codes), "total": total, "page": page, "size": size,
		"enabled": s.cfg.Domain.GiftCodesOn(r.Context()), "available": s.cfg.Domain.GiftCodesAvailable(),
	})
}

// createGiftCodes: POST /api/v1/gift-codes makes one code of the owner's own words
// ({code}) or a batch of generated ones ({count, prefix}) with the same terms:
// {amount, currency, max_redemptions, per_customer_limit, expires_at, note}. The
// answer holds the codes, "codes" in the order of "items": the only copy there is.
func (s *Server) createGiftCodes(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Amount           string `json:"amount"`
		Currency         string `json:"currency"`
		MaxRedemptions   int    `json:"max_redemptions"`
		PerCustomerLimit int    `json:"per_customer_limit"`
		ExpiresAt        *int64 `json:"expires_at"`
		Note             string `json:"note"`
		Code             string `json:"code"`
		Prefix           string `json:"prefix"`
		Count            int    `json:"count"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	cur := strings.ToUpper(strings.TrimSpace(req.Currency))
	amount, ok := parseAmount(req.Amount, cur, false)
	if !ok || amount <= 0 {
		writeFieldError(w, "amount", "a positive amount in a supported currency")
		return
	}
	spec := domain.GiftCodeSpec{Amount: amount, Currency: cur, MaxRedemptions: req.MaxRedemptions, PerCustomerLimit: req.PerCustomerLimit,
		Note: req.Note, Code: req.Code, Prefix: req.Prefix, Count: req.Count}
	if req.ExpiresAt != nil {
		t := time.Unix(*req.ExpiresAt, 0)
		spec.ExpiresAt = &t
	}
	created, err := s.cfg.Domain.CreateGiftCodes(r.Context(), actor(r), spec)
	if err != nil {
		s.fail(w, "create gift codes", err)
		return
	}
	rows := make([]store.GiftCode, 0, len(created))
	codes := make([]string, 0, len(created))
	for i := range created {
		rows = append(rows, created[i].GiftCode)
		codes = append(codes, created[i].Code)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": s.giftCodeItems(r.Context(), rows), "codes": codes})
}

// setGiftCodeEnabled: PUT /api/v1/gift-codes/{id}/enabled {enabled}. Switching
// a code off takes nothing back: what it paid stays paid.
func (s *Server) setGiftCodeEnabled(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	g, err := s.cfg.Domain.SetGiftCodeEnabled(r.Context(), actor(r), id, req.Enabled)
	if err != nil {
		s.fail(w, "set gift code enabled", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"item": s.giftCodeItems(r.Context(), []store.GiftCode{*g})[0]})
}

type giftRedemptionJSON struct {
	ID           string     `json:"id"`
	User         *userBrief `json:"user"`
	Seq          int        `json:"seq"` // the customer's n-th redemption of this code
	Amount       money      `json:"amount"`
	BalanceAfter money      `json:"balance_after"`
	CreatedAt    int64      `json:"created_at"`
}

// listGiftRedemptions: GET /api/v1/gift-codes/{id}/redemptions?page=&size=, who
// redeemed one code and when, newest first.
func (s *Server) listGiftRedemptions(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	p, page, size := pageOf(r)
	g, rows, total, err := s.cfg.Domain.GiftCodeRedemptions(r.Context(), id, p)
	if err != nil {
		s.fail(w, "list gift redemptions", err)
		return
	}
	items := make([]giftRedemptionJSON, 0, len(rows))
	for i := range rows {
		x := &rows[i]
		items = append(items, giftRedemptionJSON{ID: x.ID, User: briefOf(x.UserID, x.TelegramID, x.Username), Seq: x.Seq,
			Amount: money{x.Amount, x.Currency}, BalanceAfter: money{x.BalanceAfter, x.Currency}, CreatedAt: x.CreatedAt.Unix()})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"code": s.giftCodeItems(r.Context(), []store.GiftCode{*g})[0], "items": items, "total": total, "page": page, "size": size,
	})
}

// giftCodeColumns are the columns of gift-codes.csv in the order they were added
// (English names, new columns at the end), the contract of the files in
// docs/14-reports.md: created_utc is the instant, created_local the same instant on the wall
// clocks of the zone the tz column names, and a Jalali file (?calendar=jalali)
// has one more column at the very end, created_jalali.
var giftCodeColumns = []string{"created_utc", "code_last4", "kind", "amount", "currency", "max_redemptions", "per_customer_limit", "redeemed",
	"status", "expires_utc", "last_redeemed_utc", "note", "created_by_telegram_id", "code_id", "created_local", "tz"}

// giftRedemptionColumns are the columns of a code's redemptions file, in the same way.
var giftRedemptionColumns = []string{"time_utc", "telegram_id", "username", "number", "amount", "currency", "balance_after", "redemption_id",
	"time_local", "tz"}

// utcOrEmpty is an optional instant in UTC (the clock's rule), or nothing.
func utcOrEmpty(c clock, t *time.Time) string {
	if t == nil {
		return ""
	}
	return c.utc(*t)
}

// exportGiftCodes: GET /api/v1/gift-codes.csv?status=&q=&tz=&calendar= is the list as
// a file, newest first, at most maxGiftCSVRows rows: an export like the ledger's
// (streamCSV: UTF-8 with a byte-order mark, a place among the running exports, a
// deadline, an "export incomplete" note, an audit entry with how it ended) that
// follows the rules of docs/14-reports.md for its cells and dates. It never holds
// a code, only the last four characters; text cells go through textCell, so a
// note that is a spreadsheet formula is quoted.
func (s *Server) exportGiftCodes(w http.ResponseWriter, r *http.Request) {
	f := giftFilter(r)
	switch f.Status {
	case "", "active", "disabled", "expired", "used_up":
	default:
		writeError(w, http.StatusBadRequest, "invalid", "status must be active, disabled, expired or used_up")
		return
	}
	clk, ok := s.exportClock(w, r)
	if !ok {
		return
	}
	header := append([]string{}, giftCodeColumns...)
	if clk.jalali {
		header = append(header, "created_jalali")
	}
	now := s.now()
	s.streamCSV(w, r, csvExport{
		name:   exportFileName("gift-codes", clk, nil, nil, now),
		action: "export.gift_codes",
		query:  r.URL.Query(),
		header: header,
		each: func(ctx context.Context, row func(cells ...string) error) (bool, error) {
			return s.cfg.Store.EachGiftCode(ctx, f, maxGiftCSVRows, func(g store.GiftCodeRow) error {
				kind := "custom"
				if g.Generated {
					kind = "generated"
				}
				by := ""
				if g.CreatedByTelegramID != 0 {
					by = strconv.FormatInt(g.CreatedByTelegramID, 10)
				}
				cells := []string{clk.utc(g.CreatedAt), textCell(g.Tail), kind, decimal(g.Amount, g.Currency), textCell(g.Currency),
					strconv.Itoa(g.MaxRedemptions), strconv.Itoa(g.PerCustomerLimit), strconv.Itoa(g.Redeemed),
					domain.GiftCodeStatus(&g.GiftCode, now), utcOrEmpty(clk, g.ExpiresAt), utcOrEmpty(clk, g.LastRedeemedAt), textCell(g.Note), by, g.ID,
					clk.local(g.CreatedAt), clk.zone}
				if clk.jalali {
					cells = append(cells, clk.jalaliTime(g.CreatedAt))
				}
				return row(cells...)
			})
		},
		moreNote: moreEntriesNote(maxGiftCSVRows),
	})
}

// exportGiftRedemptions: GET /api/v1/gift-codes/{id}/redemptions.csv, who redeemed one
// code, newest first, as exportGiftCodes writes its file (time_local, tz and, in a
// Jalali file, time_jalali after the machine columns).
func (s *Server) exportGiftRedemptions(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	g, err := s.cfg.Store.GetGiftCode(r.Context(), s.cfg.Store.Conn(), id)
	if err != nil {
		s.fail(w, "export gift redemptions", err)
		return
	}
	clk, ok := s.exportClock(w, r)
	if !ok {
		return
	}
	header := append([]string{}, giftRedemptionColumns...)
	if clk.jalali {
		header = append(header, timeJalali)
	}
	q := r.URL.Query()
	q.Set("code_id", g.ID)
	s.streamCSV(w, r, csvExport{
		name:   exportFileName("gift-code-"+g.Tail+"-redemptions", clk, nil, nil, s.now()),
		action: "export.gift_redemptions",
		query:  q,
		header: header,
		each: func(ctx context.Context, row func(cells ...string) error) (bool, error) {
			return s.cfg.Store.EachGiftRedemption(ctx, g.ID, maxGiftCSVRows, func(x store.GiftRedemptionRow) error {
				cells := []string{clk.utc(x.CreatedAt), strconv.FormatInt(x.TelegramID, 10), textCell(x.Username), strconv.Itoa(x.Seq),
					decimal(x.Amount, x.Currency), textCell(x.Currency), decimal(x.BalanceAfter, x.Currency), x.ID,
					clk.local(x.CreatedAt), clk.zone}
				if clk.jalali {
					cells = append(cells, clk.jalaliTime(x.CreatedAt))
				}
				return row(cells...)
			})
		},
		moreNote: moreEntriesNote(maxGiftCSVRows),
	})
}
