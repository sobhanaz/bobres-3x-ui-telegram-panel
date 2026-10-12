// Command backup is the BOBRES backup service. Every day (and on demand) it
// dumps the database with pg_dump as the read-only svc_backup role, encrypts
// the dump with BOBRES_BACKUP_KEY together with the services' master keys,
// keeps the newest seven in /backups, and hands each one to the bot, which
// sends it to the owner's Telegram. Core lists and starts backups over HTTP.
//
// `backup run-now` (what `bobres backup` runs in the container) asks the
// running service for a backup and waits for it.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // BOBRES_TIMEZONE works whatever zoneinfo the image has

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/app"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/backupsvc"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/config"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "run-now" {
		os.Exit(runNow())
	}
	os.Exit(app.Run("backup", os.Args[1:], setup))
}

func setup(rt *app.Runtime) error {
	cfg, err := config.LoadBackup()
	if err != nil {
		return fmt.Errorf("load backup config: %w", err)
	}
	for _, k := range cfg.MissingKeys {
		rt.Log.Warn("master key not set: backups do not carry it, so a restore needs it from elsewhere", "key", k)
	}
	if cfg.BotURL == "" || cfg.ServiceToken == "" {
		rt.Log.Warn("BOBRES_BOT_URL or BOBRES_SERVICE_TOKEN is not set: backups are kept on this server and not sent to Telegram")
	}
	r, err := backupsvc.New(backupsvc.Options{
		Dir: cfg.Dir, Key: []byte(cfg.Key), Keys: cfg.MasterKeys,
		Dump:   backupsvc.PgDump{URL: cfg.DumpURL},
		BotURL: cfg.BotURL, Token: cfg.ServiceToken,
		Hour: cfg.Hour, Minute: cfg.Minute, Location: cfg.Location,
		Log: rt.Log,
	})
	if err != nil {
		return err
	}
	// Not fatal at startup: the System page shows it, and the next run
	// records it, instead of the container restarting forever.
	rt.Health.AddCheck("backup-dir", r.CheckDir)
	rt.Go("schedule", r.Loop)
	if cfg.CoreToken == "" {
		rt.Log.Warn("BOBRES_PEER_CORE_TOKEN is not set: the dashboard cannot list or start backups")
		return nil
	}
	rt.Mux.Handle("GET /internal/backups", backupsvc.List(r, cfg.CoreToken))
	rt.Mux.Handle("POST /internal/backups/run", backupsvc.Start(r, cfg.CoreToken))
	return nil
}

// runNow asks the service running in this container for a backup, with the
// token core uses for it (BOBRES_PEER_CORE_TOKEN), which the container has.
func runNow() int {
	token, err := config.Secret("BOBRES_PEER_CORE_TOKEN")
	if err != nil || token == "" {
		fmt.Fprintln(os.Stderr, "run-now: BOBRES_PEER_CORE_TOKEN is not set")
		return 2
	}
	addr := config.HTTPAddrFromEnv()
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := backupsvc.RunNow(ctx, &http.Client{Timeout: 30 * time.Second}, "http://"+addr, token, os.Stdout, 2*time.Second); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	return 0
}
