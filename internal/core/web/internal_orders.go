package web

import (
	"net/http"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// Internal orders (docs/14-reports.md, "Internal orders"): the Reports page's card of what owner accounts bought, and the two actions on the
// owner accounts' earlier purchases.

type internalCurrencyJSON struct {
	Currency      string `json:"currency"`
	Orders        int64  `json:"orders"` // purchases
	Amount        int64  `json:"amount"`
	Refunds       int64  `json:"refunds"`
	RefundsAmount int64  `json:"refunds_amount"`
}

func internalCurrencies(in []store.InternalCurrency) []internalCurrencyJSON {
	out := make([]internalCurrencyJSON, 0, len(in))
	for _, c := range in {
		out = append(out, internalCurrencyJSON(c))
	}
	return out
}

// reportInternal: GET /api/v1/reports/internal, the internal purchases of the range (the reports' range parameters): how many and how much
// per currency, what refunds gave back of them, and the same per owner account. enabled says whether the switch is on now.
func (s *Server) reportInternal(w http.ResponseWriter, r *http.Request) {
	rng, ok := s.reportRange(w, r)
	if !ok {
		return
	}
	rep, err := s.cfg.Domain.InternalOrders(r.Context(), rng)
	if err != nil {
		s.fail(w, "report internal orders", err)
		return
	}
	on, err := s.cfg.Domain.ExcludeOwnerOrders(r.Context())
	if err != nil {
		s.fail(w, "report internal orders", err)
		return
	}
	type account struct {
		User       *userBrief             `json:"user"`
		Orders     int64                  `json:"orders"`
		Currencies []internalCurrencyJSON `json:"currencies"`
	}
	accounts := make([]account, 0, len(rep.Accounts))
	for _, a := range rep.Accounts {
		accounts = append(accounts, account{User: briefOf(a.UserID, a.TelegramID, a.Username), Orders: a.Orders, Currencies: internalCurrencies(a.Currencies)})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"from": rng.From, "to": rng.To, "tz": rng.TZ, "enabled": on,
		"orders": rep.Orders, "currencies": internalCurrencies(rep.Currencies), "accounts": accounts,
	})
}

func internalMoveJSON(dryRun bool, m *store.InternalMove) map[string]any {
	return map[string]any{"dry_run": dryRun, "orders": m.Orders, "ledger_entries": m.Entries, "currencies": internalCurrencies(m.Currencies)}
}

// reportInternalBackfill: POST /api/v1/reports/internal/backfill {"dry_run": bool}, counts the owner accounts' earlier orders and ledger
// entries as internal. With dry_run it only says how many rows and how much money would move, and changes nothing; without it, it marks them
// (audited as reports.internal_backfill) and says what it marked. A second call marks nothing.
func (s *Server) reportInternalBackfill(w http.ResponseWriter, r *http.Request) {
	var in struct {
		DryRun bool `json:"dry_run"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	var (
		m   *store.InternalMove
		err error
	)
	if in.DryRun {
		m, err = s.cfg.Domain.InternalBackfillPreview(r.Context())
	} else {
		m, err = s.cfg.Domain.CountOwnerRowsInternal(r.Context(), actor(r))
	}
	if err != nil {
		s.fail(w, "internal backfill", err)
		return
	}
	writeJSON(w, http.StatusOK, internalMoveJSON(in.DryRun, m))
}

// reportInternalUndo: POST /api/v1/reports/internal/undo, puts the owner accounts' rows back into revenue and the reports (audited as
// reports.internal_undo). A second call changes nothing.
func (s *Server) reportInternalUndo(w http.ResponseWriter, r *http.Request) {
	m, err := s.cfg.Domain.UndoOwnerRowsInternal(r.Context(), actor(r))
	if err != nil {
		s.fail(w, "internal undo", err)
		return
	}
	writeJSON(w, http.StatusOK, internalMoveJSON(false, m))
}
