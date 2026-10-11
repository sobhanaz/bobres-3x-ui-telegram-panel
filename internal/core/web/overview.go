package web

import (
	"net/http"
	"sort"
)

type money struct {
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
}

func moneyList(m map[string]int64) []money {
	out := make([]money, 0, len(m))
	for c, a := range m {
		out = append(out, money{Amount: a, Currency: c})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Currency < out[j].Currency })
	return out
}

// overview is the dashboard's first page: users, services, money, health.
func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	o, err := s.cfg.Domain.DashboardOverview(r.Context(), s.cfg.Payments, s.cfg.Provisioner)
	if err != nil {
		s.internal(w, "overview", err)
		return
	}
	recent := make([]map[string]any, 0, len(o.Recent))
	for _, ro := range o.Recent {
		recent = append(recent, map[string]any{
			"id": ro.ID, "type": ro.Type, "status": ro.Status, "amount": money{ro.Amount, ro.Currency},
			"created_at": ro.CreatedAt.Unix(), "telegram_id": ro.TelegramID, "username": ro.Username, "plan": ro.PlanName,
		})
	}
	tickets := map[string]any{"waiting": int64(0), "oldest_waiting_at": nil}
	if c, err := s.cfg.Store.CountTickets(r.Context(), s.cfg.Store.Conn(), ""); err == nil {
		tickets = map[string]any{"waiting": c.Waiting, "oldest_waiting_at": unix(c.OldestWaiting)}
	} else {
		s.cfg.Log.Warn("overview tickets", "err", err)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"tickets":          tickets,
		"users":            map[string]int64{"total": o.UsersTotal, "new_24h": o.UsersNew24h, "new_7d": o.UsersNew7d},
		"services":         map[string]int64{"active": o.ServicesActive, "expiring": o.ServicesExpiring, "ended": o.ServicesEnded},
		"orders":           map[string]int64{"paid_24h": o.OrdersPaid24h, "provision_failed": o.ProvisionFailed},
		"revenue":          map[string]any{"day": moneyList(o.Revenue24h), "month": moneyList(o.Revenue30d)},
		"pending_payments": o.PendingPayments,
		"panel":            map[string]any{"healthy": o.PanelHealthy, "detail": o.PanelDetail},
		"recent_orders":    recent,
	})
}
