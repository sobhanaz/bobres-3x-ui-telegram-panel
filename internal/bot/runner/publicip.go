package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tlsconf"
)

// The server's public IP address, for the dashboard's HTTPS page (ip mode):
// core has no internet access, so it asks the bot, at GET
// /internal/public-ip (core's service token).

// IPServices are asked in order.
var IPServices = []string{"https://api.ipify.org", "https://ifconfig.me/ip"}

// maxIPAnswer bounds a service's answer.
const maxIPAnswer = 64

// IPLookup asks the services for this server's public address.
type IPLookup struct {
	// Services are asked in order (IPServices when nil).
	Services []string
	// Client asks them (nil: one that waits 5 seconds per service, follows
	// no redirect and uses no proxy: a proxy would answer with its own
	// address).
	Client *http.Client
}

// Lookup returns the first usable answer: a 200 of at most 64 bytes that is
// a public IP address (tlsconf.PublicIP, in canonical form).
func (l IPLookup) Lookup(ctx context.Context) (string, error) {
	services := l.Services
	if services == nil {
		services = IPServices
	}
	hc := l.Client
	if hc == nil {
		hc = &http.Client{Timeout: 5 * time.Second, CheckRedirect: noRedirect, Transport: &http.Transport{Proxy: nil}}
	}
	var errs []error
	for _, u := range services {
		ip, err := askIP(ctx, hc, u)
		if err == nil {
			return ip, nil
		}
		errs = append(errs, fmt.Errorf("%s: %w", u, err))
	}
	return "", errors.Join(errs...)
}

func askIP(ctx context.Context, hc *http.Client, u string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxIPAnswer+1))
	switch {
	case err != nil:
		return "", err
	case resp.StatusCode != http.StatusOK:
		return "", fmt.Errorf("answered %d", resp.StatusCode)
	case len(body) > maxIPAnswer:
		return "", errors.New("the answer is too long")
	}
	return tlsconf.PublicIP(strings.TrimSpace(string(body)))
}

// PublicIP serves GET /internal/public-ip: {"ip"}, or 502 {"error":
// "unavailable"} when no service gave a public address (the details go to
// the bot's log).
func PublicIP(lookup func(ctx context.Context) (string, error), coreToken string, log *slog.Logger) http.Handler {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !fromCore(r, coreToken) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		ip, err := lookup(r.Context())
		if err != nil {
			log.Warn("public IP address not detected", "err", err)
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "unavailable"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"ip": ip})
	})
}
