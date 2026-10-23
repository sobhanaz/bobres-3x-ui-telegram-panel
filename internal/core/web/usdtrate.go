package web

import (
	"context"
	"net/http"
	"time"
)

// The live USDT price (docs/04-dashboard.md, Gateways): what the store charges for one USDT now and where it comes from.

func (s *Server) usdtRateBody(r *http.Request) map[string]any {
	info := s.cfg.Domain.UsdtRateInfo(r.Context())
	out := map[string]any{"auto": info.Auto, "margin": info.Margin, "manual": info.Manual, "rate": info.Rate, "source": info.Source, "live": nil}
	if info.HasLive {
		out["live"] = map[string]any{"rate": info.Live.Rate, "at": info.Live.At, "sources": info.Live.Sources, "fresh": info.LiveFresh}
	}
	return out
}

// getUsdtRate: GET /api/v1/usdt-rate.
func (s *Server) getUsdtRate(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.usdtRateBody(r))
}

// refreshUsdtRate: POST /api/v1/usdt-rate/refresh reads the live price now (through the bot) and answers as GET does.
func (s *Server) refreshUsdtRate(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if _, err := s.cfg.Domain.RefreshUsdtRate(ctx); err != nil {
		writeError(w, http.StatusBadGateway, "unreachable", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.usdtRateBody(r))
}
