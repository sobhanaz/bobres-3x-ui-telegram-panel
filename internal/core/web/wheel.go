package web

import (
	"net/http"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// The daily wheel of fortune in the dashboard (docs/04-dashboard.md, Settings, "Daily wheel"): its settings are the Settings page's, and a small report
// of what the wheel did, for the owner and admins who read reports. Customers spin in the bot and the Mini App.

type wheelDayJSON struct {
	Day     string `json:"day"`
	Spins   int64  `json:"spins"`   // customers who spun
	Winners int64  `json:"winners"` // of them, those who won a prize
	Paid    money  `json:"paid"`    // Toman paid out
}

func wheelDayOf(d store.WheelDay) wheelDayJSON {
	return wheelDayJSON{Day: d.Day, Spins: d.Spins, Winners: d.Winners, Paid: money{d.Paid, domain.WalletCurrency}}
}

// wheelReport: GET /api/v1/wheel/report, today against the daily budget and the days of the last month that had a spin (newest first).
func (s *Server) wheelReport(w http.ResponseWriter, r *http.Request) {
	rep, err := s.cfg.Domain.WheelReport(r.Context())
	if err != nil {
		s.internal(w, "wheel report", err)
		return
	}
	days := make([]wheelDayJSON, 0, len(rep.Days))
	for _, d := range rep.Days {
		days = append(days, wheelDayOf(d))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"on": rep.On, "day": rep.Day, "budget": money{rep.Budget, rep.Currency}, "remaining": money{rep.Remaining, rep.Currency},
		"today": wheelDayOf(rep.Today), "days": days, "total": wheelDayOf(rep.Total), "window_days": domain.WheelReportDays,
	})
}
