package main

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/config"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

const metricsTokenUsage = "usage: metrics-token generate | status" //nolint:gosec // a usage line, not a credential

// metricsTokenCmd is the server's way to turn the store's /metrics on without the dashboard (docs/runbooks/monitoring.md): generate makes a
// new token through the same domain code as Settings > Monitoring (the audit says it came from the server), prints it once on stdout and
// replaces any token there was; status says whether one is set. `bobres admin metrics-token` runs it inside the core container; tools/hosted
// runs that over SSH after it installed a hosted store, so the owner's dashboard can read the store's health.
func metricsTokenCmd(args []string, out, errOut io.Writer) int {
	if len(args) != 1 || (args[0] != "generate" && args[0] != "status") {
		fmt.Fprintln(errOut, metricsTokenUsage)
		return 2
	}
	cfg, err := config.LoadCore()
	if err != nil {
		fmt.Fprintln(errOut, "load core config:", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	st, err := store.New(ctx, cfg.DatabaseURL)
	if err != nil {
		fmt.Fprintln(errOut, "connect:", err)
		return 1
	}
	defer st.Close()
	dom := domain.New(st, domain.Config{OwnerTelegramIDs: cfg.AdminTelegramIDs})
	ctx = store.WithSource(ctx, store.SourceCLI)
	if args[0] == "status" {
		on, err := dom.MetricsEnabled(ctx)
		if err != nil {
			fmt.Fprintln(errOut, "error:", err)
			return 1
		}
		if on {
			fmt.Fprintln(out, "on: /metrics answers a request that carries the token")
		} else {
			fmt.Fprintln(out, "off: /metrics answers 404 until a token is made")
		}
		return 0
	}
	token, err := dom.GenerateMetricsToken(ctx, nil)
	if err != nil {
		fmt.Fprintln(errOut, "error: the token could not be made:", err)
		return 1
	}
	fmt.Fprintln(out, token)
	return 0
}
