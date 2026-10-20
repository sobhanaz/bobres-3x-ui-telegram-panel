package web

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	bmoney "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/money"
)

// maxCSVRows caps one export (a filter narrows a bigger one).
const maxCSVRows = 100_000

type ledgerJSON struct {
	ID           string     `json:"id"`
	User         *userBrief `json:"user"`
	Amount       money      `json:"amount"` // signed: debits are negative
	BalanceAfter money      `json:"balance_after"`
	Kind         string     `json:"kind"`
	RefType      string     `json:"ref_type"`
	RefID        string     `json:"ref_id"`
	CreatedAt    int64      `json:"created_at"`
}

func ledgerOf(e *store.LedgerRow) ledgerJSON {
	out := ledgerJSON{ID: e.ID, User: briefOf(e.UserID, e.TelegramID, e.Username),
		Amount: money{e.Amount, e.Currency}, BalanceAfter: money{e.BalanceAfter, e.Currency}, Kind: e.Kind,
		CreatedAt: e.CreatedAt.Unix()}
	if e.RefType != nil {
		out.RefType = *e.RefType
	}
	if e.RefID != nil {
		out.RefID = *e.RefID
	}
	return out
}

// ledgerFilter reads ?user=&kind=&currency=&from=&to= (from/to: unix
// seconds, [from, to)).
func ledgerFilter(r *http.Request) (store.LedgerFilter, bool) {
	q := r.URL.Query()
	userID, ok := queryID(r, "user")
	if !ok {
		return store.LedgerFilter{}, false
	}
	f := store.LedgerFilter{UserID: userID, Kind: q.Get("kind"), Currency: strings.ToUpper(q.Get("currency"))}
	for name, dst := range map[string]**time.Time{"from": &f.From, "to": &f.To} {
		if v := q.Get(name); v != "" {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || n < 0 {
				return f, false
			}
			t := time.Unix(n, 0)
			*dst = &t
		}
	}
	return f, true
}

// listLedger: GET /api/v1/ledger?user=&kind=&currency=&from=&to=&page=&size=
func (s *Server) listLedger(w http.ResponseWriter, r *http.Request) {
	p, page, size := pageOf(r)
	f, ok := ledgerFilter(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid", "bad filter")
		return
	}
	rows, total, err := s.cfg.Store.ListLedgerAll(r.Context(), s.cfg.Store.Conn(), f, p)
	if err != nil {
		s.fail(w, "list ledger", err)
		return
	}
	items := make([]ledgerJSON, 0, len(rows))
	for i := range rows {
		items = append(items, ledgerOf(&rows[i]))
	}
	writeJSON(w, http.StatusOK, list{items, total, page, size})
}

// ledgerColumns are the columns of ledger.csv in the order they were added
// (English names, new columns at the end). time_utc is the instant; time_local
// the same instant on the wall clocks of the zone the tz column names. A Jalali
// file (?calendar=jalali) has one more column, time_jalali: the local date in
// the Jalali calendar, with Latin digits, and the time.
var ledgerColumns = []string{"time_utc", "telegram_id", "username", "kind", "amount", "currency", "balance_after", "ref_type", "ref_id",
	"entry_id", "time_local", "tz"}

const timeJalali = "time_jalali"

// exportLedger: GET /api/v1/ledger.csv with the same filters, newest first,
// at most maxCSVRows rows, and ?tz= and ?calendar= for the dates. UTF-8 with a
// byte-order mark, so spreadsheet programs show Persian names. It is an export
// like the reports' (streamCSV): it shares their places (two at a time), their
// deadline and their rules for a failure and for the cells (csvcells.go).
func (s *Server) exportLedger(w http.ResponseWriter, r *http.Request) {
	f, ok := ledgerFilter(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid", "bad filter")
		return
	}
	clk, ok := s.exportClock(w, r)
	if !ok {
		return
	}
	header := append([]string{}, ledgerColumns...)
	if clk.jalali {
		header = append(header, timeJalali)
	}
	s.streamCSV(w, r, csvExport{
		name:   exportFileName("ledger", clk, f.From, f.To, s.now()),
		action: "export.ledger",
		query:  r.URL.Query(),
		header: header,
		each: func(ctx context.Context, row func(cells ...string) error) (bool, error) {
			return s.cfg.Store.EachLedger(ctx, f, maxCSVRows, func(e store.LedgerRow) error {
				ref, refType := "", ""
				if e.RefID != nil {
					ref = *e.RefID
				}
				if e.RefType != nil {
					refType = *e.RefType
				}
				cells := []string{
					clk.utc(e.CreatedAt), strconv.FormatInt(e.TelegramID, 10), textCell(e.Username),
					textCell(e.Kind), decimal(e.Amount, e.Currency), textCell(e.Currency), decimal(e.BalanceAfter, e.Currency),
					textCell(refType), textCell(ref), e.ID, clk.local(e.CreatedAt), clk.zone}
				if clk.jalali {
					cells = append(cells, clk.jalaliTime(e.CreatedAt))
				}
				return row(cells...)
			})
		},
		moreNote: moreEntriesNote(maxCSVRows),
	})
}

// decimal writes minor units as a plain decimal ("-12.500000" USDT).
func decimal(amount int64, currency string) string {
	scale := bmoney.Scale(currency)
	if scale <= 0 {
		return strconv.FormatInt(amount, 10)
	}
	sign := ""
	if amount < 0 {
		sign, amount = "-", -amount
	}
	s := strconv.FormatInt(amount, 10)
	if len(s) <= scale {
		s = strings.Repeat("0", scale-len(s)+1) + s
	}
	return sign + s[:len(s)-scale] + "." + s[len(s)-scale:]
}
