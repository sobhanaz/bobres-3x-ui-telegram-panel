package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/config"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// Lines of the output that `bobres admin password` and the installer read.
const (
	adminPasswordUserPrefix = "Username: "
	adminPasswordPassPrefix = "Password: "
)

// adminPasswordCmd gives an owner (BOBRES_ADMIN_TELEGRAM_ID lists them; the
// first unless --id names another) a way into the
// dashboard that needs neither the bot nor a /start: it creates the owner's
// account when it is missing, and sets a random initial password for the
// login "owner" (the owner's own username when they have one). It prints the
// password once, on stdout, and never logs it. The first login with it may
// only choose a new password and set up an authenticator app. Running it
// again resets the login and ends the owner's sessions. `bobres admin
// password` and `bobres install` run it inside the core container.
func adminPasswordCmd(args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("admin-password", flag.ContinueOnError)
	fs.SetOutput(errOut)
	id := fs.Int64("id", 0, "Telegram id of the owner (default: the main owner, the first one in BOBRES_ADMIN_TELEGRAM_ID)")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 || *id < 0 {
		fmt.Fprintln(errOut, "usage: admin-password [--id <telegram id>]")
		return 2
	}
	cfg, err := config.LoadCore()
	if err != nil {
		fmt.Fprintln(errOut, "load core config:", err)
		return 1
	}
	if len(cfg.AdminTelegramIDs) == 0 {
		fmt.Fprintln(errOut, "BOBRES_ADMIN_TELEGRAM_ID is not set: there is no owner to give a password to (bobres menu > Owners)")
		return 1
	}
	if cfg.MasterKey == "" {
		fmt.Fprintln(errOut, "the core master key (BOBRES_CORE_MASTER_KEY in .env) is not set: password logins are off on this install (re-run bobres install)")
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
	login, err := dom.IssueInitialPassword(store.WithSource(ctx, store.SourceCLI), *id)
	if err != nil {
		fmt.Fprintln(errOut, "error: set the initial password:", err)
		return 1
	}
	fmt.Fprintf(out, "%s%s\n%s%s\n", adminPasswordUserPrefix, login.Username, adminPasswordPassPrefix, login.Password)
	fmt.Fprintln(out, "Shown once, never stored in the clear: at the first login you must choose a new password and set up two-factor authentication.")
	return 0
}
