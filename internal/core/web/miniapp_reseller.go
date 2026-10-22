package web

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"slices"
	"strconv"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	bmoney "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/money"
)

// The Mini App's reseller section (docs/17-mini-app.md): a reseller's panel,
// their customers' services (list, pause and resume, delete, name, note), the
// customers who joined through their invite link, and their statement. Buying
// for a customer, renewing and adding traffic use the shop's own order calls.
//
// Every call is the signed-in reseller's own: the user comes from the session
// and nothing the client sends names another user. Core's domain checks again
// on every call that the caller is an active reseller whose install's license
// includes resellers (domain.ResellerAccess), and that a service is theirs;
// anyone else gets 403 or 404 and nothing changes. No call takes a price or an
// amount.

func init() {
	registerMiniFeature(miniFeature{routes: (*Server).miniResellerRoutes, me: (*Server).miniResellerFlag})
}

func (s *Server) miniResellerRoutes(api *http.ServeMux) {
	api.HandleFunc("GET /api/mini/v1/reseller", s.miniSession(false, s.miniResellerPanel))
	api.HandleFunc("POST /api/mini/v1/reseller/prefix", s.miniSession(false, s.miniResellerPrefix))
	api.HandleFunc("GET /api/mini/v1/reseller/services", s.miniSession(false, s.miniResellerServices))
	api.HandleFunc("POST /api/mini/v1/reseller/services/{id}/pause", s.miniSession(false, s.miniResellerPause(true)))
	api.HandleFunc("POST /api/mini/v1/reseller/services/{id}/resume", s.miniSession(false, s.miniResellerPause(false)))
	api.HandleFunc("POST /api/mini/v1/reseller/services/{id}/delete", s.miniSession(false, s.miniResellerDelete))
	api.HandleFunc("POST /api/mini/v1/reseller/services/{id}/label", s.miniSession(false, s.miniResellerLabel))
	api.HandleFunc("POST /api/mini/v1/reseller/services/{id}/note", s.miniSession(false, s.miniResellerNote))
	api.HandleFunc("GET /api/mini/v1/reseller/customers", s.miniSession(false, s.miniResellerCustomers))
	api.HandleFunc("GET /api/mini/v1/reseller/statement", s.miniSession(false, s.miniResellerStatement))
}

// miniResellerFlag says in /me whether the section is shown: the caller is an
// active reseller and the install's license includes resellers.
func (s *Server) miniResellerFlag(ctx context.Context, u *store.User, features map[string]any) error {
	features["reseller"] = false
	if u.Role != domain.ResellerRole || u.Status != "active" {
		return nil // the license is only read for the people it could matter to
	}
	switch _, err := s.cfg.Domain.ResellerAccess(ctx, u.ID); {
	case err == nil:
		features["reseller"] = true
	case errors.Is(err, domain.ErrForbidden), errors.Is(err, domain.ErrUserBanned), errors.Is(err, store.ErrNotFound):
		// not a reseller now, or resellers are no longer in the license
	default:
		return err
	}
	return nil
}

// miniResellerFail answers an error of a reseller call. A service staff
// paused gets its own code, so the app can say to ask support.
func (s *Server) miniResellerFail(w http.ResponseWriter, what string, err error) {
	if errors.Is(err, domain.ErrPausedByStaff) {
		writeError(w, http.StatusConflict, "paused_by_staff", "support paused this service")
		return
	}
	s.miniFail(w, what, err)
}

const (
	miniResellerPage    = 10 // services, customers and statement lines a page
	miniResellerMaxPage = 50 // the most a client may ask for
	miniMaxPageNumber   = 100_000
)

// miniPaging reads ?page= and ?size= (1 and the default when absent).
func miniPaging(w http.ResponseWriter, r *http.Request) (page, size int, ok bool) {
	page, size = 1, miniResellerPage
	for name, dst := range map[string]*int{"page": &page, "size": &size} {
		v := r.URL.Query().Get(name)
		if v == "" {
			continue
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > miniMaxPageNumber || (name == "size" && n > miniResellerMaxPage) {
			writeError(w, http.StatusBadRequest, "invalid", name+" is out of range")
			return 0, 0, false
		}
		*dst = n
	}
	return page, size, true
}

func miniStorePage(page, size int) store.Page {
	return store.Page{Limit: size, Offset: (page - 1) * size}
}

// miniMoneyList is amounts per currency, sorted by currency, with their scale.
func miniMoneyList(m map[string]int64) []map[string]any {
	out := make([]map[string]any, 0, len(m))
	for _, cur := range slices.Sorted(maps.Keys(m)) {
		out = append(out, map[string]any{"currency": cur, "amount": m[cur], "scale": bmoney.Scale(cur)})
	}
	return out
}

// miniResellerPanel: GET /api/mini/v1/reseller, the reseller's tier, balances
// with their credit, commission, customers and invite link.
func (s *Server) miniResellerPanel(w http.ResponseWriter, r *http.Request) {
	ctx, c := r.Context(), miniFrom(r.Context())
	if _, err := s.cfg.Domain.ResellerAccess(ctx, c.user.ID); err != nil {
		s.miniResellerFail(w, "mini app reseller", err)
		return
	}
	info, err := s.cfg.Domain.ResellerInfo(ctx, c.user.ID)
	if err != nil {
		s.miniResellerFail(w, "mini app reseller", err)
		return
	}
	counts, err := s.cfg.Store.CountUserServices(ctx, s.cfg.Store.Conn(), c.user.ID)
	if err != nil {
		s.miniResellerFail(w, "mini app reseller", err)
		return
	}
	balances := make([]map[string]any, 0, len(info.Wallets))
	for _, wal := range info.Wallets {
		balances = append(balances, map[string]any{"currency": wal.Currency, "balance": wal.Balance, "credit_limit": wal.CreditLimit,
			"available": wal.Balance + wal.CreditLimit, "scale": bmoney.Scale(wal.Currency)})
	}
	link := "" // the bot's username is the one in Branding, else the one the bot told at a sign-in
	if settings, err := s.cfg.Domain.Settings(ctx); err == nil {
		if bot := s.miniBot(settings); bot != "" {
			link = "https://t.me/" + bot + "?start=r_" + info.RefCode
		}
	}
	naming, err := s.cfg.Domain.ResellerNaming(ctx, c.user.ID)
	if err != nil {
		s.miniResellerFail(w, "mini app reseller", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"tier": info.Terms.TierName, "discount_percent": info.Terms.DiscountPercent, "commission_percent": info.Terms.CommissionPercent,
		"balances": balances, "customers": info.Customers, "commission_earned": miniMoneyList(info.Earned),
		"ref_code": info.RefCode, "invite_link": link, "counts": miniServiceCounts(counts),
		// What their configs are called and grouped as in the 3x-ui panel ("" until chosen or given).
		"config_prefix": naming.ConfigPrefix, "panel_group": naming.PanelGroup,
	})
}

// miniResellerPrefix: POST /api/mini/v1/reseller/prefix {prefix} sets the reseller's own config prefix, once (the owner changes it
// after that); the answer is the panel again.
func (s *Server) miniResellerPrefix(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Prefix string `json:"prefix"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	ctx, c := r.Context(), miniFrom(r.Context())
	if _, err := s.cfg.Domain.ResellerAccess(ctx, c.user.ID); err != nil {
		s.miniResellerFail(w, "mini app reseller prefix", err)
		return
	}
	if err := s.cfg.Domain.SetResellerConfigPrefix(ctx, c.user, c.user.ID, in.Prefix); err != nil {
		s.miniResellerFail(w, "mini app reseller prefix", err)
		return
	}
	s.miniResellerPanel(w, r)
}

func miniServiceCounts(c store.ServiceCounts) map[string]any {
	return map[string]any{"total": c.Total, "active": c.Active, "expiring_soon": c.ExpiringSoon, "expired": c.Expired,
		"paused": c.Paused, "pending": c.Pending}
}

// miniResellerServiceBody is one of the reseller's services as the section
// shows it: usage, link, note and what can be done to it. can_renew and
// can_topup follow the shop's own rule (miniServices).
func (s *Server) miniResellerServiceBody(r *http.Request, sub *store.Subscription) map[string]any {
	running := sub.Status == "active" || sub.Status == "expiring_soon" || sub.Status == "expired" || sub.Status == "depleted"
	renewable := sub.SubLink != "" && running
	label, note := "", ""
	var expires any
	if sub.ExpiresAt != nil {
		expires = sub.ExpiresAt.Unix()
	}
	if sub.Label != nil {
		label = *sub.Label
	}
	if sub.Note != nil {
		note = *sub.Note
	}
	return map[string]any{
		"id": sub.ID, "label": label, "note": note, "status": sub.Status, "expires_at": expires,
		"traffic_total": miniUnix(sub.TrafficTotal), "traffic_used": sub.TrafficUsed, "created_at": sub.CreatedAt.Unix(),
		"link": sub.SubLink, "page_url": s.pageURL(r, sub),
		"can_renew":  renewable,
		"can_topup":  renewable && sub.TrafficTotal != nil && *sub.TrafficTotal > 0 && sub.Status != "expired",
		"can_pause":  renewable,
		"can_resume": sub.Status == "disabled",
		"can_delete": sub.Status != "pending" && sub.Status != "deleted",
	}
}

// miniResellerServices: GET /api/mini/v1/reseller/services?status=&q=&page=&size=
func (s *Server) miniResellerServices(w http.ResponseWriter, r *http.Request) {
	ctx, c := r.Context(), miniFrom(r.Context())
	page, size, ok := miniPaging(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	got, err := s.cfg.Domain.ListResellerServices(ctx, c.user.ID, domain.ResellerServiceFilter{Status: q.Get("status"), Query: q.Get("q")},
		miniStorePage(page, size))
	if err != nil {
		s.miniResellerFail(w, "mini app reseller services", err)
		return
	}
	out := make([]map[string]any, 0, len(got.Items))
	for i := range got.Items {
		out = append(out, s.miniResellerServiceBody(r, &got.Items[i]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"services": out, "total": got.Total, "page": page, "size": size,
		"counts": miniServiceCounts(got.Counts)})
}

// miniResellerService answers with the service as it is now.
func (s *Server) miniResellerService(w http.ResponseWriter, r *http.Request, sub *store.Subscription) {
	writeJSON(w, http.StatusOK, map[string]any{"service": s.miniResellerServiceBody(r, sub)})
}

// miniResellerPause: POST /api/mini/v1/reseller/services/{id}/pause and
// .../resume. Doing it twice changes nothing.
func (s *Server) miniResellerPause(pause bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in struct{}
		if !readJSON(w, r, &in) {
			return
		}
		if s.cfg.Provisioner == nil {
			writeError(w, http.StatusServiceUnavailable, "unavailable", "the store cannot reach its servers")
			return
		}
		sub, err := s.cfg.Domain.ResellerSetServiceEnabled(r.Context(), s.cfg.Provisioner, miniFrom(r.Context()).user.ID, r.PathValue("id"), !pause)
		if err != nil {
			s.miniResellerFail(w, "mini app reseller pause", err)
			return
		}
		s.miniResellerService(w, r, sub)
	}
}

// miniResellerDelete: POST /api/mini/v1/reseller/services/{id}/delete
// {"confirm": true}. Deleting is for good and refunds nothing, so the call
// refuses to run without the confirmation the app asks the reseller for.
func (s *Server) miniResellerDelete(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Confirm bool `json:"confirm"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if !in.Confirm {
		s.miniFail(w, "mini app reseller delete", errInvalid("deleting a service needs confirm: true"))
		return
	}
	if s.cfg.Provisioner == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "the store cannot reach its servers")
		return
	}
	if err := s.cfg.Domain.ResellerDeleteService(r.Context(), s.cfg.Provisioner, miniFrom(r.Context()).user.ID, r.PathValue("id")); err != nil {
		s.miniResellerFail(w, "mini app reseller delete", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// miniResellerLabel: POST /api/mini/v1/reseller/services/{id}/label {label}
// names a service (empty clears the name).
func (s *Server) miniResellerLabel(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Label string `json:"label"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	sub, err := s.cfg.Domain.ResellerSetLabel(r.Context(), miniFrom(r.Context()).user.ID, r.PathValue("id"), in.Label)
	if err != nil {
		s.miniResellerFail(w, "mini app reseller label", err)
		return
	}
	s.miniResellerService(w, r, sub)
}

// miniResellerNote: POST /api/mini/v1/reseller/services/{id}/note {note}
// sets the private note (empty clears it).
func (s *Server) miniResellerNote(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Note string `json:"note"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	sub, err := s.cfg.Domain.ResellerSetNote(r.Context(), miniFrom(r.Context()).user.ID, r.PathValue("id"), in.Note)
	if err != nil {
		s.miniResellerFail(w, "mini app reseller note", err)
		return
	}
	s.miniResellerService(w, r, sub)
}

// miniResellerCustomers: GET /api/mini/v1/reseller/customers?page=&size=, the
// people who joined through the reseller's invite link, newest first.
func (s *Server) miniResellerCustomers(w http.ResponseWriter, r *http.Request) {
	page, size, ok := miniPaging(w, r)
	if !ok {
		return
	}
	got, err := s.cfg.Domain.ListInvitedCustomers(r.Context(), miniFrom(r.Context()).user.ID, miniStorePage(page, size))
	if err != nil {
		s.miniResellerFail(w, "mini app reseller customers", err)
		return
	}
	out := make([]map[string]any, 0, len(got.Items))
	for _, cu := range got.Items {
		out = append(out, map[string]any{"telegram_id": cu.TelegramID, "username": cu.Username, "joined_at": cu.JoinedAt.Unix(),
			"paid_orders": cu.PaidOrders, "spent": miniMoneyList(cu.Spent), "commission": miniMoneyList(cu.Commission)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"customers": out, "total": got.Total, "page": page, "size": size})
}

// miniResellerStatement: GET /api/mini/v1/reseller/statement?page=&size=, the
// reseller's wallet movements, newest first (commissions and payments to the
// store included). It has no total: has_more says whether another page follows.
func (s *Server) miniResellerStatement(w http.ResponseWriter, r *http.Request) {
	ctx, c := r.Context(), miniFrom(r.Context())
	page, size, ok := miniPaging(w, r)
	if !ok {
		return
	}
	if _, err := s.cfg.Domain.ResellerAccess(ctx, c.user.ID); err != nil {
		s.miniResellerFail(w, "mini app reseller statement", err)
		return
	}
	entries, err := s.cfg.Store.ListLedger(ctx, s.cfg.Store.Conn(), c.user.ID, size+1, (page-1)*size)
	if err != nil {
		s.miniResellerFail(w, "mini app reseller statement", err)
		return
	}
	more := len(entries) > size
	if more {
		entries = entries[:size]
	}
	out := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		out = append(out, map[string]any{"kind": e.Kind, "amount": e.Amount, "currency": e.Currency, "balance_after": e.BalanceAfter,
			"at": e.CreatedAt.Unix(), "scale": bmoney.Scale(e.Currency)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": out, "page": page, "size": size, "has_more": more})
}
