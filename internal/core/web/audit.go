package web

import (
	"encoding/csv"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// maxAuditCSVRows caps one audit export (a filter narrows a bigger one).
const maxAuditCSVRows = 100_000

type auditJSON struct {
	ID        string          `json:"id"`
	Actor     *userBrief      `json:"actor"` // nil: the system (or a deleted user)
	ActorRole string          `json:"actor_role"`
	Action    string          `json:"action"`
	Entity    string          `json:"entity"`
	EntityID  string          `json:"entity_id"`
	Before    json.RawMessage `json:"before"`
	After     json.RawMessage `json:"after"`
	Reason    string          `json:"reason"`
	IP        string          `json:"ip"`
	CreatedAt int64           `json:"created_at"`
}

func auditOf(a *store.AuditRow) auditJSON {
	out := auditJSON{ID: a.ID, ActorRole: a.ActorRole, Action: a.Action, Entity: a.Entity, Before: a.Before,
		After: a.After, Reason: a.Reason, IP: a.IP, CreatedAt: a.CreatedAt.Unix()}
	if a.ActorID != nil && a.ActorTelegramID != 0 {
		out.Actor = briefOf(*a.ActorID, a.ActorTelegramID, a.ActorUsername)
	}
	if a.EntityID != nil {
		out.EntityID = *a.EntityID
	}
	if len(out.Before) == 0 {
		out.Before = nil
	}
	if len(out.After) == 0 {
		out.After = nil
	}
	return out
}

// auditFilter reads ?actor=&action=&entity=&entity_id=&from=&to= (from/to:
// unix seconds, [from, to)).
func auditFilter(r *http.Request) (store.AuditFilter, bool) {
	q := r.URL.Query()
	actor, ok1 := queryID(r, "actor")
	entityID, ok2 := queryID(r, "entity_id")
	if !ok1 || !ok2 {
		return store.AuditFilter{}, false
	}
	f := store.AuditFilter{ActorID: actor, Action: q.Get("action"), Entity: q.Get("entity"), EntityID: entityID}
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

// listAudit: GET /api/v1/audit?actor=&action=&entity=&entity_id=&from=&to=&page=&size=
func (s *Server) listAudit(w http.ResponseWriter, r *http.Request) {
	p, page, size := pageOf(r)
	f, ok := auditFilter(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid", "bad filter")
		return
	}
	rows, total, err := s.cfg.Store.ListAudit(r.Context(), s.cfg.Store.Conn(), f, p)
	if err != nil {
		s.fail(w, "list audit", err)
		return
	}
	items := make([]auditJSON, 0, len(rows))
	for i := range rows {
		items = append(items, auditOf(&rows[i]))
	}
	writeJSON(w, http.StatusOK, list{items, total, page, size})
}

// auditActions: GET /api/v1/audit/actions, the actions recorded so far.
func (s *Server) auditActions(w http.ResponseWriter, r *http.Request) {
	counts, err := s.cfg.Store.AuditActions(r.Context(), s.cfg.Store.Conn())
	if err != nil {
		s.fail(w, "audit actions", err)
		return
	}
	type item struct {
		Action string `json:"action"`
		Count  int64  `json:"count"`
	}
	items := make([]item, 0, len(counts))
	for a, n := range counts {
		items = append(items, item{a, n})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Action < items[j].Action })
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// exportAudit: GET /api/v1/audit.csv with the same filters, newest first.
func (s *Server) exportAudit(w http.ResponseWriter, r *http.Request) {
	f, ok := auditFilter(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid", "bad filter")
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/csv; charset=utf-8")
	h.Set("Content-Disposition", `attachment; filename="audit-`+s.now().UTC().Format("20060102-1504")+`.csv"`)
	_, _ = w.Write([]byte{0xEF, 0xBB, 0xBF})
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"time_utc", "actor_telegram_id", "actor_username", "actor_role", "action", "entity", "entity_id",
		"reason", "ip", "before", "after", "entry_id"})
	more, err := s.cfg.Store.EachAudit(r.Context(), s.cfg.Store.Conn(), f, maxAuditCSVRows, func(a store.AuditRow) error {
		entity := ""
		if a.EntityID != nil {
			entity = *a.EntityID
		}
		tg := ""
		if a.ActorTelegramID != 0 {
			tg = strconv.FormatInt(a.ActorTelegramID, 10)
		}
		return cw.Write([]string{
			a.CreatedAt.UTC().Format(time.RFC3339), tg, csvText(a.ActorUsername), a.ActorRole, a.Action, a.Entity, entity,
			csvText(a.Reason), a.IP, csvText(string(a.Before)), csvText(string(a.After)), a.ID,
		})
	})
	if err != nil {
		s.cfg.Log.Error("dashboard api: export audit", "err", err)
	}
	if more {
		_ = cw.Write([]string{"# more entries match: narrow the filter (at most " + strconv.Itoa(maxAuditCSVRows) + " rows per file)"})
	}
	cw.Flush()
}
