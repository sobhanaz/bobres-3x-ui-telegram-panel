package web

import (
	"encoding/csv"
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

// exportLedger: GET /api/v1/ledger.csv with the same filters, newest first,
// at most maxCSVRows rows. UTF-8 with a byte-order mark, so spreadsheet
// programs show Persian names.
func (s *Server) exportLedger(w http.ResponseWriter, r *http.Request) {
	f, ok := ledgerFilter(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid", "bad filter")
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/csv; charset=utf-8")
	h.Set("Content-Disposition", `attachment; filename="ledger-`+s.now().UTC().Format("20060102-1504")+`.csv"`)
	_, _ = w.Write([]byte{0xEF, 0xBB, 0xBF})
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"time_utc", "telegram_id", "username", "kind", "amount", "currency", "balance_after", "ref_type", "ref_id", "entry_id"})
	more, err := s.cfg.Store.EachLedger(r.Context(), s.cfg.Store.Conn(), f, maxCSVRows, func(e store.LedgerRow) error {
		ref := ""
		if e.RefID != nil {
			ref = *e.RefID
		}
		refType := ""
		if e.RefType != nil {
			refType = *e.RefType
		}
		return cw.Write([]string{
			e.CreatedAt.UTC().Format(time.RFC3339), strconv.FormatInt(e.TelegramID, 10), csvText(e.Username),
			e.Kind, decimal(e.Amount, e.Currency), e.Currency, decimal(e.BalanceAfter, e.Currency), csvText(refType), ref, e.ID,
		})
	})
	if err != nil {
		// The header is out: the file ends short, and the log says why.
		s.cfg.Log.Error("dashboard api: export ledger", "err", err)
	}
	if more {
		_ = cw.Write([]string{"# more entries match: narrow the filter (at most " + strconv.Itoa(maxCSVRows) + " rows per file)"})
	}
	cw.Flush()
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

// csvText keeps user-chosen text from running as a spreadsheet formula.
func csvText(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}
