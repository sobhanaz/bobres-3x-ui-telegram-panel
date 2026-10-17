package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/config"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/version"
)

// summary is the card the install ends with: where the store is, how to get
// in, how healthy it is, what the license says and what to do next.
func (ins *installer) summary(env map[string]string, dir string) {
	t := ins.ui
	title := func(s string) {
		rule := "-"
		if t.unicode {
			rule = "─"
		}
		fmt.Fprintf(ins.out, "\n%s\n%s\n", t.paint(roleBold, t.paint(roleTeal, s)), t.paint(roleNavy, strings.Repeat(rule, min(max(t.width-1, 20), 60))))
	}
	row := func(label, value string) {
		fmt.Fprintf(ins.out, "  %s %s\n", t.paint(roleTeal, fmt.Sprintf("%-12s", label)), value)
	}

	title("BOBRES is ready")
	row("Store", "https://"+env["BOBRES_DOMAIN"])
	if ins.botName != "" {
		row("Bot", "@"+ins.botName)
	}
	row("Dashboard", "send /dashboard to your bot, or run:  sudo bobres admin link")
	if l := ins.login; l != nil {
		// The owner's initial login, issued once after the stack came up. It is
		// printed here only: not logged, not in .env, not in any state file.
		title("Sign in to the dashboard (no Telegram needed)")
		fmt.Fprintf(ins.out, "  Address:   https://%s/admin\n  Username:  %s\n  Password:  %s\n", env["BOBRES_DOMAIN"], l.username, l.password)
		fmt.Fprintln(ins.out, "  This password is shown once and is not stored anywhere in the clear: you must change it\n"+
			"  at your first login, where you also set up two-factor authentication. Lost it before then?\n"+
			"  Run  bobres admin password  for a new one.")
	} else if env["BOBRES_ADMIN_TELEGRAM_ID"] != "" {
		fmt.Fprintln(ins.out, "\n  No Telegram, or want a password login? Run  bobres admin password  and sign in at https://"+env["BOBRES_DOMAIN"]+"/admin")
	}
	row("License", ins.licenseLine(dir))
	row("Install dir", fmt.Sprintf("%s  (version %s)", dir, orDash(env["BOBRES_VERSION"])))
	if ins.logPath != "" {
		row("Log", ins.logPath)
	}

	if health := ins.healthRows(env, dir); len(health) > 0 {
		title("Health")
		for _, h := range health {
			fmt.Fprintf(ins.out, "  %s %-13s %s\n", t.mark(h.kind), h.name, h.state)
		}
	}

	bot := "your bot"
	if ins.botName != "" {
		bot = "@" + ins.botName
	}
	title("Next steps")
	fmt.Fprintf(ins.out, "  1. Open %s in Telegram and send /start from the owner's account (id %s):\n"+
		"     that makes you the owner. Until then the dashboard link below is refused.\n", bot, orDash(env["BOBRES_ADMIN_TELEGRAM_ID"]))
	fmt.Fprintln(ins.out, "  2. Open the dashboard: send /dashboard to the bot, or run  sudo bobres admin link.")
	fmt.Fprintln(ins.out, "  3. Set up the store in the bot: /set payments.card_number 6037-...  /set payments.card_holder Your Name\n"+
		"     /plan_add 150000 IRT 30 50 Monthly 50GB   /trial 1 1   then /admin.")
	fmt.Fprintln(ins.out, "     The bot photo is the BOBRES logo; to use your own, upload it in the dashboard: Branding & texts > Store > Telegram bot")

	title("Good to know")
	fmt.Fprintf(ins.out, "  - Your domain https://%s must point to this server for HTTPS. Keep %s/.env backed up:\n"+
		"    it holds the keys that encrypt your panel tokens and gateway settings.\n", env["BOBRES_DOMAIN"], dir)
	fmt.Fprintf(ins.out, "  - %s\n", strings.TrimSpace(ins.httpsSummary(env, dir)))
	if backupEnabled(env) {
		fmt.Fprintf(ins.out, "  - Backups: every day at %s (%s) into %s, each also sent to you on Telegram. One now: bobres backup\n"+
			"    They are encrypted with your backup key: keep it away from this server (bobres menu > \"Backup key\" shows it).\n",
			env["BOBRES_BACKUP_TIME"], env["BOBRES_TIMEZONE"], filepath.Join(dir, "backups"))
		if s, err := config.ParseBackupS3(env); err == nil && s.Enabled() {
			fmt.Fprintf(ins.out, "    Each backup is also copied to %s (the newest %d are kept there).\n", s.Target(), s.Keep)
		} else {
			fmt.Fprintln(ins.out, "  - Off-site backups (optional): sudo bobres install --s3-endpoint https://<host> --s3-bucket <name>\n"+
				"    (the keys are asked); each backup is then also copied to that S3-compatible bucket, still encrypted.")
		}
	} else {
		fmt.Fprintf(ins.out, "  - Backups are off: version %s has no backup service. Upgrade with: bobres install --version %s\n",
			env["BOBRES_VERSION"], imageTag(version.Version))
	}
	if env["BOBRES_XUI_URL"] == "" {
		fmt.Fprintln(ins.out, "  - No 3x-ui panel yet: add your servers in the dashboard (Servers) before you sell.")
	}
	fmt.Fprintln(ins.out, "  - Server hardening (firewall, fail2ban, security updates, swap): sudo bobres harden; bobres doctor shows it.")
	fmt.Fprintln(ins.out, "  - Manage the store from this server at any time: run  bobres  for the menu.")

	title("Help")
	for _, l := range []struct{ label, url string }{
		{"Docs", brandRepo}, {"Support", brandIssues}, {"Security", brandSecurity}, {"Website", brandSite},
	} {
		row(l.label, l.url)
	}
	copyright := brandCopyright
	if !t.unicode {
		copyright = strings.ReplaceAll(copyright, "©", "(c)")
	}
	fmt.Fprintf(ins.out, "\n%s\n%s\n", t.paint(roleCream, copyright), t.paint(roleCream, brandMadeBy))
}

// healthRow is one service of the health table.
type healthRow struct {
	name, state string
	kind        markKind
}

// healthRows reads the services' state for the card (none when docker cannot
// say).
func (ins *installer) healthRows(env map[string]string, dir string) []healthRow {
	if ins.run == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := ins.run(ctx, dir, composeArgs("ps", "--all", "--format", "json")...)
	if err != nil {
		return nil
	}
	rows := parseServices(out)
	var list []healthRow
	for _, name := range allServices(dir, env) {
		r, ok := rows[name]
		switch {
		case !ok:
			list = append(list, healthRow{name, "missing", markFail})
		case r.State == "running" && (r.Health == "" || r.Health == "healthy"):
			state := "running"
			if r.Health == "healthy" {
				state = "healthy"
			}
			list = append(list, healthRow{name, state, markOK})
		default:
			list = append(list, healthRow{name, orDash(r.Health) + " (" + r.State + ")", markWarn})
		}
	}
	return list
}

// licenseLine says what license the new install runs on: the trial every
// install starts with (and its days left), a license, or the free tier.
func (ins *installer) licenseLine(dir string) string {
	const fallback = "30-day trial from the first start (System > License in the dashboard shows it)"
	if ins.run == nil {
		return fallback
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := ins.run(ctx, dir, composeArgs("exec", "-T", "core", "/app", "license", "info", "--json")...)
	if err != nil {
		return fallback
	}
	var info struct {
		Effective struct {
			Basis         string `json:"basis"`
			Tier          string `json:"tier"`
			TrialDaysLeft int    `json:"trial_days_left"`
			TrialDays     int    `json:"trial_days"`
		} `json:"effective"`
	}
	if json.Unmarshal(out, &info) != nil {
		return fallback
	}
	e := info.Effective
	switch e.Basis {
	case "trial":
		return fmt.Sprintf("%d-day trial, %d days left (activate a key: sudo bobres license activate)", e.TrialDays, e.TrialDaysLeft)
	case "license", "grace":
		return "license active (" + orDash(e.Tier) + ")"
	case "free":
		return "free tier (activate a key: sudo bobres license activate)"
	}
	return fallback
}
