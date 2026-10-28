package web

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
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
	prov := s.cfg.Provisioner
	if s.cfg.Servers != nil {
		prov = nil // the server list below says it without calling a panel
	}
	o, err := s.cfg.Domain.DashboardOverview(r.Context(), s.cfg.Payments, prov)
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
	out := map[string]any{
		"tickets":          tickets,
		"users":            map[string]int64{"total": o.UsersTotal, "new_24h": o.UsersNew24h, "new_7d": o.UsersNew7d},
		"services":         map[string]int64{"active": o.ServicesActive, "expiring": o.ServicesExpiring, "ended": o.ServicesEnded, "panel_missing": o.ServicesPanelMissing},
		"orders":           map[string]int64{"paid_24h": o.OrdersPaid24h, "provision_failed": o.ProvisionFailed},
		"pending_payments": o.PendingPayments,
		"panel":            map[string]any{"healthy": o.PanelHealthy, "detail": o.PanelDetail},
		"recent_orders":    recent,
	}
	// Revenue is for those who may read the reports (owner and admin): support
	// sees the rest of the overview, not what the store earns.
	if Allowed(actor(r).Role, PermReportsRead) {
		out["revenue"] = map[string]any{"day": moneyList(o.Revenue24h), "month": moneyList(o.Revenue30d)}
	}
	if s.cfg.Servers != nil {
		servers, panel := s.overviewServers(r.Context(), Allowed(actor(r).Role, PermServersWrite))
		out["panel"] = panel
		if servers != nil {
			out["servers"] = servers
		}
	}
	if gws := s.overviewGateways(r.Context()); len(gws) > 0 {
		out["gateways"] = gws
	}
	// What resellers owe the store, for those who manage them.
	if Allowed(actor(r).Role, PermResellersWrite) {
		if n, debt, err := s.cfg.Store.ResellerSummary(r.Context(), s.cfg.Store.Conn()); err == nil {
			out["resellers"] = map[string]any{"count": n, "debt": moneyList(debt)}
		} else {
			s.cfg.Log.Warn("overview resellers", "err", err)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// overviewTimeout bounds what the overview asks the other services.
const overviewTimeout = 5 * time.Second

// overviewServers lists the 3x-ui servers with their last health check (nil
// when the provisioner did not answer), and the panel summary: healthy when
// every server taking new services answers. Why a panel failed names its
// address, so only those who manage servers (full) read it; others are told
// it does not answer.
func (s *Server) overviewServers(ctx context.Context, full bool) ([]map[string]any, map[string]any) {
	ctx, cancel := context.WithTimeout(ctx, overviewTimeout)
	defer cancel()
	list, err := s.cfg.Servers.ListServers(ctx)
	if err != nil {
		s.cfg.Log.Warn("overview servers", "err", err)
		return nil, map[string]any{"healthy": false, "detail": "provisioner unreachable"}
	}
	out := make([]map[string]any, 0, len(list.Servers))
	live, down := 0, 0
	for _, sv := range list.Servers {
		healthy, detail := sv.Healthy, ""
		switch {
		case sv.CheckedAt == 0:
			healthy, detail = true, "not checked yet"
		case !healthy && full && sv.HealthError != "":
			detail = sv.HealthError
		case !healthy:
			detail = "does not answer"
		case !sv.Enabled:
			detail = "takes no new services"
		}
		if sv.Enabled {
			live++
			if !healthy {
				down++
			}
		}
		out = append(out, map[string]any{"id": sv.ID, "name": sv.Name, "enabled": sv.Enabled, "healthy": healthy, "detail": detail,
			"clients": sv.Clients, "max_clients": sv.MaxClients})
	}
	panel := map[string]any{"healthy": live > 0 && down == 0, "detail": ""}
	switch {
	case len(list.Servers) == 0:
		panel["detail"] = "no 3x-ui server yet"
	case live == 0:
		panel["detail"] = "no server takes new services"
	case down > 0:
		panel["detail"] = fmt.Sprintf("%d of %d servers do not answer", down, live)
	}
	return out, panel
}

// overviewGateways says for each payment method whether customers are
// offered it and whether it works: Zarinpal from payments (its report),
// the others from their settings (on, with the details they need).
func (s *Server) overviewGateways(ctx context.Context) []map[string]any {
	var out []map[string]any
	if s.cfg.Gateways != nil {
		ctx, cancel := context.WithTimeout(ctx, overviewTimeout)
		configs, err := s.cfg.Gateways.GatewayConfigs(ctx)
		if err != nil {
			s.cfg.Log.Warn("overview gateways", "err", err)
		}
		for i := range configs {
			c := &configs[i]
			healthy := c.Offered
			if h := s.gatewayHealth(ctx, s.cfg.Gateways, c.Provider); healthy && h != nil {
				healthy = h.Healthy
			}
			out = append(out, map[string]any{"provider": c.Provider, "enabled": c.Enabled, "healthy": healthy})
		}
		cancel()
	}
	values, err := s.cfg.Domain.Settings(ctx)
	if err != nil {
		s.cfg.Log.Warn("overview payment settings", "err", err)
		return out
	}
	for _, m := range []struct {
		provider string
		needs    []string // one of these must be set
	}{
		{"manual_card", []string{"payments.card_number"}},
		{"manual_crypto", []string{"payments.usdt_trc20", "payments.usdt_erc20"}},
		{"manual_zarinpal", []string{"payments.zarinpal_link"}},
		{domain.Stars, []string{"payments.stars_rate"}},
	} {
		ready := false
		for _, k := range m.needs {
			ready = ready || values[k] != ""
		}
		out = append(out, map[string]any{"provider": m.provider,
			"enabled": values[domain.MethodToggle(m.provider)] != "false", "healthy": ready})
	}
	return out
}
