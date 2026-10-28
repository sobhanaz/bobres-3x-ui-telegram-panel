package main

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/config"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/botfiles"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

const approvalUsage = "usage: approval status | off"

// approvalCmd is the server's way to see, and to switch off, the approval of dashboard sign-ins (docs/04-dashboard.md, "Signing in"). It is
// the way out that cannot lock the owner out: with the bot down, or Telegram out of reach, no approval can come, and this needs neither. It
// changes the setting through the same domain code the dashboard uses (so the audit log says who, from where, and the owners are told in
// Telegram when the bot can be reached), ends the sign-ins that wait, and never turns the approval on: that is the owner's, signed in to the
// dashboard. `bobres admin approval status|off` runs it inside the core container.
func approvalCmd(args []string, out, errOut io.Writer) int {
	if len(args) != 1 || (args[0] != "status" && args[0] != "off") {
		fmt.Fprintln(errOut, approvalUsage)
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
	if cfg.BotURL != "" {
		dom.SetLoginApprovalBot(botfiles.New(cfg.BotURL, cfg.BotInternalToken)) // to tell the owners; the change does not wait for it
	}
	ctx = store.WithSource(ctx, store.SourceCLI)
	state, err := dom.LoginApprovalState(ctx)
	if err != nil {
		fmt.Fprintln(errOut, "error: the setting could not be read:", err)
		return 1
	}
	waiting, err := dom.WaitingLoginApprovals(ctx)
	if err != nil {
		fmt.Fprintln(errOut, "error: the waiting sign-ins could not be counted:", err)
		return 1
	}
	if args[0] == "status" {
		printApprovalStatus(out, state, waiting)
		return 0
	}
	if !state.Enabled {
		fmt.Fprintln(out, "The approval of dashboard sign-ins is off already: nothing to change.")
		return 0
	}
	if err := dom.SetLoginApproval(ctx, nil, "server", false, nil); err != nil {
		fmt.Fprintln(errOut, "error: the approval could not be switched off:", err)
		return 1
	}
	fmt.Fprintln(out, "The approval of dashboard sign-ins is now off: staff sign-ins open at once again.")
	if waiting > 0 {
		fmt.Fprintf(out, "%d sign-in(s) that were waiting for an approval were ended; those who waited sign in again.\n", waiting)
	}
	fmt.Fprintln(out, "The owners are told in Telegram when the bot can be reached. It is turned on again in the dashboard (Settings, Sign-in security), as the owner.")
	return 0
}

// printApprovalStatus says whether the approval is on and what that means for a sign-in, with the command to switch it off.
func printApprovalStatus(out io.Writer, st domain.LoginApprovalState, waiting int) {
	yesNo := func(b bool) string {
		if b {
			return "yes"
		}
		return "no"
	}
	if !st.Enabled {
		fmt.Fprintln(out, "The approval of dashboard sign-ins is off: a staff sign-in opens at once.")
		fmt.Fprintln(out, "The owner turns it on in the dashboard (Settings, Sign-in security).")
		return
	}
	fmt.Fprintln(out, "The approval of dashboard sign-ins is ON: a staff sign-in opens only after the staff member, or an owner, approves it in Telegram within 5 minutes.")
	fmt.Fprintf(out, "  the owners are asked for other staff members' sign-ins: %s\n", yesNo(st.NotifyOwner))
	fmt.Fprintf(out, "  sign-ins waiting for an answer now: %d\n", waiting)
	fmt.Fprintln(out, "If the bot or Telegram cannot be reached, switch it off:  sudo bobres admin approval off")
}
