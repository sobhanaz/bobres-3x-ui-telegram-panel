package web

import (
	"net/http"
	"strings"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

type userJSON struct {
	ID         string  `json:"id"`
	TelegramID int64   `json:"telegram_id"`
	Username   string  `json:"username"`
	Language   string  `json:"language"`
	Role       string  `json:"role"`
	Status     string  `json:"status"`
	CreatedAt  int64   `json:"created_at"`
	RefCode    string  `json:"ref_code,omitempty"`
	Wallets    []money `json:"wallets"`
}

func userOf(u *store.User, wallets []store.Wallet) userJSON {
	out := userJSON{ID: u.ID, TelegramID: u.TelegramID, Username: u.Username, Language: u.Language,
		Role: u.Role, Status: u.Status, CreatedAt: u.CreatedAt.Unix(), Wallets: []money{}}
	if u.RefCode != nil {
		out.RefCode = *u.RefCode
	}
	for _, w := range wallets {
		out.Wallets = append(out.Wallets, money{w.Balance, w.Currency})
	}
	return out
}

// listUsers: GET /api/v1/users?q=&status=&role=&page=&size=
func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	p, page, size := pageOf(r)
	q := r.URL.Query()
	f := store.UserFilter{Query: q.Get("q"), Status: q.Get("status"), Role: q.Get("role")}
	rows, total, err := s.cfg.Store.ListUsers(r.Context(), s.cfg.Store.Conn(), f, p)
	if err != nil {
		s.fail(w, "list users", err)
		return
	}
	type item struct {
		userJSON
		Subscriptions int64 `json:"subscriptions"`
		Orders        int64 `json:"orders"`
	}
	items := make([]item, 0, len(rows))
	for i := range rows {
		items = append(items, item{userOf(&rows[i].User, rows[i].Wallets), rows[i].Subscriptions, rows[i].Orders})
	}
	writeJSON(w, http.StatusOK, list{items, total, page, size})
}

// getUser: GET /api/v1/users/{id}: the profile, wallets and invitations;
// the services, orders and ledger come from their lists (?user=).
func (s *Server) getUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	ctx, st := r.Context(), s.cfg.Store
	u, err := st.GetUser(ctx, st.Conn(), id)
	if err != nil {
		s.fail(w, "get user", err)
		return
	}
	wallets, err := st.ListWallets(ctx, st.Conn(), id)
	if err != nil {
		s.fail(w, "get user wallets", err)
		return
	}
	subs, orders, err := st.UserActivity(ctx, st.Conn(), id)
	if err != nil {
		s.fail(w, "get user activity", err)
		return
	}
	invited, rewarded, earned, err := st.ReferralStats(ctx, st.Conn(), id)
	if err != nil {
		s.fail(w, "get user referrals", err)
		return
	}
	var referredBy *userBrief
	if u.ReferredBy != nil {
		referredBy = s.usersByID(ctx, []string{*u.ReferredBy})[*u.ReferredBy]
	}
	me := actor(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"user":     userOf(u, wallets),
		"counts":   map[string]int64{"subscriptions": subs, "orders": orders},
		"referral": map[string]any{"referred_by": referredBy, "invited": invited, "rewarded": rewarded, "earned": moneyList(earned)},
		// Staff are managed on the Staff page, never banned from here.
		"can_ban": u.ID != me.ID && !domain.IsStaffRole(u.Role) && Allowed(me.Role, PermUsersWrite),
	})
}

// setUserStatus: POST /api/v1/users/{id}/status {status, reason}
func (s *Server) setUserStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	u, err := s.cfg.Domain.SetUserStatus(r.Context(), actor(r), id, req.Status, req.Reason)
	if err != nil {
		s.fail(w, "set user status", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": u.Status})
}

// adjustBalance: POST /api/v1/users/{id}/balance {amount, currency, reason, key}.
// amount is a signed decimal in major units ("50000", "-12.5").
func (s *Server) adjustBalance(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		Amount   string `json:"amount"`
		Currency string `json:"currency"`
		Reason   string `json:"reason"`
		Key      string `json:"key"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	cur := strings.ToUpper(strings.TrimSpace(req.Currency))
	delta, ok := parseAmount(req.Amount, cur, true)
	if !ok || delta == 0 {
		writeError(w, http.StatusBadRequest, "invalid", "enter an amount (negative to take money out) in a supported currency")
		return
	}
	if !idemKeyRe.MatchString(req.Key) {
		writeError(w, http.StatusBadRequest, "invalid", "missing request key")
		return
	}
	wallet, err := s.cfg.Domain.AdjustBalance(r.Context(), actor(r), id, delta, cur, req.Reason, req.Key)
	if err != nil {
		s.fail(w, "adjust balance", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"wallet": money{wallet.Balance, wallet.Currency}})
}
