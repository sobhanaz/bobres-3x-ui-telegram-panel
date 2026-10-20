package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/config"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tlsprobe"
)

// composeProject is the Compose project name every bobres command uses, so
// status/logs/uninstall find what install started.
const composeProject = "bobres"

func composeArgs(extra ...string) []string {
	return append([]string{"compose", "--project-name", composeProject, "-f", "docker-compose.yml", "--env-file", ".env"}, extra...)
}

// defaultDir is where commands look for the install: $BOBRES_DIR, else /opt/bobres.
func defaultDir() string {
	if d := strings.TrimSpace(os.Getenv("BOBRES_DIR")); d != "" {
		return d
	}
	return "/opt/bobres"
}

const dirUsage = "install directory (default: $BOBRES_DIR, else /opt/bobres)"

// ops holds the side effects of status/logs/uninstall, replaceable in tests.
type ops struct {
	in     io.Reader
	out    io.Writer
	errOut io.Writer
	// run captures a docker command's output.
	run func(ctx context.Context, dir string, args ...string) ([]byte, error)
	// stream runs a docker command with its output going straight to the user.
	stream func(ctx context.Context, dir string, args ...string) error
	// pipe runs a docker command reading stdin from in and writing stdout to
	// w (nil: discarded), its errors going to the user: a database dump
	// streamed out of or into a container.
	pipe   func(ctx context.Context, dir string, in io.Reader, w io.Writer, args ...string) error
	freeGB func(path string) float64
	sleep  func(time.Duration)
	now    func() time.Time
	// lookupBot tells the bot's username for the install in dir (nil: asked of
	// Telegram with the bot token in dir's .env).
	lookupBot func(ctx context.Context, dir string) string
	// httpc calls the vendor's license server (nil: a 30-second client;
	// redirects are never followed).
	httpc *http.Client
	// s3c downloads a backup for `bobres restore s3://...` (nil: a plain
	// client; the download's own timeout applies).
	s3c *http.Client
	// probeTLS reads what this host's port 443 serves for an address (nil:
	// a TLS handshake with 127.0.0.1:443).
	probeTLS func(ctx context.Context, address string) tlsprobe.Result
	// detectIP is this server's public IP address, "" when unknown (nil:
	// publicIP, which asks two web services).
	detectIP func() string
	// gatewayState is the state of the install's gateway container, "" when
	// there is none (nil: caddyState, which asks Docker).
	gatewayState func(ctx context.Context) string
	// host is how the day-one uninstall reaches the server outside Docker (nil:
	// the real one).
	host *hostSys
	// tty says a person is at the terminal although in is not the terminal
	// itself (the menu reads through its own buffer).
	tty bool
	// units looks at, and removes, the dashboard updater's systemd units (nil: not asked), and
	// exe is the bobres they run (nil: this one).
	units *systemUnits
	exe   func() (string, error)
}

func newOps(in io.Reader, out, errOut io.Writer) *ops {
	return &ops{
		in: in, out: out, errOut: errOut,
		units: newSystemUnits(),
		run: func(ctx context.Context, dir string, args ...string) ([]byte, error) {
			cmd := exec.CommandContext(ctx, "docker", args...) //nolint:gosec // fixed program, arguments built here
			cmd.Dir = dir
			// A container that hangs must not hold the call past its context.
			cmd.WaitDelay = 2 * time.Second
			return cmd.CombinedOutput()
		},
		stream: func(ctx context.Context, dir string, args ...string) error {
			cmd := exec.CommandContext(ctx, "docker", args...) //nolint:gosec // fixed program, arguments built here
			cmd.Dir, cmd.Stdout, cmd.Stderr, cmd.Stdin = dir, out, errOut, os.Stdin
			return cmd.Run()
		},
		pipe: func(ctx context.Context, dir string, in io.Reader, w io.Writer, args ...string) error {
			cmd := exec.CommandContext(ctx, "docker", args...) //nolint:gosec // fixed program, arguments built here
			cmd.Dir, cmd.Stdin, cmd.Stdout, cmd.Stderr = dir, in, w, errOut
			return cmd.Run()
		},
		freeGB: freeDiskGB,
		sleep:  time.Sleep,
		now:    time.Now,
	}
}

// appServices are the BOBRES services the installer waits for: the backup
// service only where its profile is on, the tls-agent only where this install
// has one (tlsAgentOn: a rollback to an older release must not wait for a
// service that release does not run).
func appServices(dir string, env map[string]string) []string {
	s := []string{"core", "payments", "provisioner", "bot"}
	if tlsAgentOn(dir, env) {
		s = append(s, tlsAgentService)
	}
	if backupEnabled(env) {
		s = append(s, "backup")
	}
	return s
}

// dataServices are the app services that use the database and hold the
// service tokens: appServices without the tls-agent, which has neither (it
// only writes Caddy's HTTPS files). A restore stops these, a token rotation
// restarts them.
func dataServices(dir string, env map[string]string) []string {
	return slices.DeleteFunc(appServices(dir, env), func(s string) bool { return s == tlsAgentService })
}

// allServices are every container of an install, as status lists them.
func allServices(dir string, env map[string]string) []string {
	s := []string{"caddy", "postgres", "redis", "core", "payments", "provisioner", "bot"}
	if tlsAgentOn(dir, env) {
		s = append(s, tlsAgentService)
	}
	if backupEnabled(env) {
		s = append(s, "backup")
	}
	return s
}

// requireInstall checks that dir holds an install and returns its .env.
func (o *ops) requireInstall(dir string) (map[string]string, bool) {
	env, ok, err := readEnv(filepath.Join(dir, ".env"))
	if err != nil {
		fmt.Fprintf(o.errOut, "error: read %s: %v\n", filepath.Join(dir, ".env"), err)
		return nil, false
	}
	if !ok {
		fmt.Fprintf(o.errOut, "error: no install found in %s (run `bobres install`, or pass --dir)\n", dir)
		return nil, false
	}
	return env, true
}

// statusCmd shows the services, versions and disk; exit 1 when something is
// not healthy, so it doubles as a monitoring probe.
func (o *ops) statusCmd(args []string) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(o.errOut)
	dir := fs.String("dir", defaultDir(), dirUsage)
	asJSON := fs.Bool("json", false, "print the dashboard (services, license, HTTPS, backup) as JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	env, ok := o.requireInstall(*dir)
	if !ok {
		return 2
	}
	if *asJSON {
		return o.statusJSON(*dir, env)
	}
	t := detectUI(o.out, os.Getenv)
	label := func(s string) string { return t.paint(roleTeal, fmt.Sprintf("%-9s", s)) }
	fmt.Fprintf(o.out, "%s %s\n%s %s\n%s %s\n", label("Install:"), *dir, label("Version:"), orDash(env["BOBRES_VERSION"]), label("Domain:"), orDash(env["BOBRES_DOMAIN"]))
	if gb := o.freeGB(*dir); gb >= 0 {
		fmt.Fprintf(o.out, "%s %.1f GB free\n", label("Disk:"), gb)
	}
	out, err := o.run(context.Background(), *dir, composeArgs("ps", "--all", "--format", "json")...)
	if err != nil {
		fmt.Fprintf(o.errOut, "error: docker compose ps failed: %v\n%s\n", err, strings.TrimSpace(string(out)))
		return 1
	}
	rows := parseServices(out)
	want := allServices(*dir, env)
	fmt.Fprintln(o.out, "\n"+t.paint(roleBold, "SERVICE       STATE        HEALTH"))
	problems := 0
	seen := map[string]bool{}
	// serviceRow prints one service: on a terminal with a mark in front of it.
	serviceRow := func(name, state, health string, ok bool) {
		if !t.fancy {
			fmt.Fprintf(o.out, "%-13s %-12s %s\n", name, state, health)
			return
		}
		kind := markOK
		if !ok {
			kind = markFail
		}
		fmt.Fprintf(o.out, "%s %-13s %-12s %s\n", t.mark(kind), name, state, health)
	}
	for _, name := range want {
		r, found := rows[name]
		seen[name] = true
		if !found {
			serviceRow(name, "missing", "-", false)
			problems++
			continue
		}
		health := r.Health
		if health == "" {
			health = "-"
		}
		good := r.State == "running" && (r.Health == "" || r.Health == "healthy")
		serviceRow(name, r.State, health, good)
		if !good {
			problems++
		}
	}
	var extra []string
	for name := range rows {
		if !seen[name] {
			extra = append(extra, name)
		}
	}
	sort.Strings(extra)
	for _, name := range extra {
		r := rows[name]
		serviceRow(name, r.State, orDash(r.Health), r.State == "running")
	}
	if problems > 0 {
		fmt.Fprintf(o.out, "\n%s\n", t.paint(roleRed, fmt.Sprintf("%d service(s) need attention. See: bobres logs <service>", problems)))
		return 1
	}
	fmt.Fprintf(o.out, "\n%s\n", t.paint(roleTeal, "All services are running."))
	return 0
}

type serviceRow struct {
	State  string
	Health string
}

// parseServices reads `docker compose ps --format json` (an array, or one
// object per line depending on the Compose version) into service -> row.
func parseServices(out []byte) map[string]serviceRow {
	type row struct {
		Service string `json:"Service"`
		State   string `json:"State"`
		Health  string `json:"Health"`
	}
	var list []row
	trimmed := bytes.TrimSpace(out)
	if bytes.HasPrefix(trimmed, []byte("[")) {
		_ = json.Unmarshal(trimmed, &list)
	} else {
		for _, line := range bytes.Split(trimmed, []byte("\n")) {
			var r row
			if json.Unmarshal(line, &r) == nil && r.Service != "" {
				list = append(list, r)
			}
		}
	}
	rows := make(map[string]serviceRow, len(list))
	for _, r := range list {
		rows[r.Service] = serviceRow{State: r.State, Health: r.Health}
	}
	return rows
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// logsCmd streams service logs.
func (o *ops) logsCmd(args []string) int {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	fs.SetOutput(o.errOut)
	dir := fs.String("dir", defaultDir(), dirUsage)
	follow := fs.Bool("f", false, "follow")
	tail := fs.String("tail", "200", "number of lines to show from the end of each log")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if _, ok := o.requireInstall(*dir); !ok {
		return 2
	}
	cargs := composeArgs("logs", "--tail", *tail, "--no-color")
	if *follow {
		cargs = append(cargs, "-f")
	}
	cargs = append(cargs, fs.Args()...)
	if err := o.stream(context.Background(), *dir, cargs...); err != nil {
		var ee *exec.ExitError
		if *follow && errors.As(err, &ee) && (ee.ExitCode() == 130 || ee.ExitCode() == -1) {
			return 0 // Ctrl+C ends following (the menu keeps running)
		}
		fmt.Fprintf(o.errOut, "error: docker compose logs: %v\n", err)
		return 1
	}
	return 0
}

// uninstallCmd stops and removes the containers; data (database, Redis,
// certificates) and the install directory stay. With --purge it is the day-one
// command (uninstall.go): everything BOBRES put on this server goes, so that it
// is as before. --verify only scans the server for what is left.
func (o *ops) uninstallCmd(args []string) int {
	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	fs.SetOutput(o.errOut)
	dir := fs.String("dir", defaultDir(), dirUsage)
	purge := fs.Bool("purge", false, "remove everything BOBRES put on this server, data and backups included (irreversible): the server is as on day one")
	yes := fs.Bool("yes", false, "do not ask for confirmation (with --purge only together with --confirm)")
	confirm := fs.String("confirm", "", "with --purge --yes: the token you would have typed (the install's domain, else this host's name)")
	dryRun := fs.Bool("dry-run", false, "print what would be removed and change nothing")
	verify := fs.Bool("verify", false, "scan the server for what is left of BOBRES (exit 1 if anything is)")
	backupTo := fs.String("backup-to", "", "with --purge: write a verified final backup of the database to this file (or into this directory) first")
	noBackup := fs.Bool("no-backup", false, "with --purge: no final backup (and no question about one)")
	keepBackups := fs.Bool("keep-backups", false, "with --purge: keep <dir>/backups")
	removeDocker := fs.Bool("remove-docker", false, "with --purge: also remove Docker, when BOBRES installed it and nothing else uses it")
	removePackages := fs.Bool("remove-packages", false, "with --purge: also remove the packages BOBRES installed (ufw, fail2ban...), when nothing else needs them")
	disableFirewall := fs.Bool("disable-firewall", false, "with --purge: turn ufw off again, when BOBRES turned it on")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintln(o.errOut, uninstallUsage)
		return 2
	}
	opts := purgeOptions{dir: *dir, yes: *yes, confirm: *confirm, dryRun: *dryRun, backupTo: *backupTo, noBackup: *noBackup,
		keepBackups: *keepBackups, removeDocker: *removeDocker, removePackages: *removePackages, disableFirewall: *disableFirewall}
	if msg := uninstallFlagProblem(opts, *purge, *verify); msg != "" {
		fmt.Fprintf(o.errOut, "error: %s\n%s\n", msg, uninstallUsage)
		return 2
	}
	switch {
	case *verify:
		return newUninstaller(o).verifyCmd(context.Background(), opts)
	case *purge:
		return newUninstaller(o).purge(context.Background(), opts, args)
	}
	return o.uninstallContainers(opts)
}

// uninstallFlagProblem says what is wrong with a combination of flags, "" when it is fine.
func uninstallFlagProblem(o purgeOptions, purge, verify bool) string {
	purgeOnly := map[string]bool{
		"--confirm": o.confirm != "", "--backup-to": o.backupTo != "", "--no-backup": o.noBackup, "--keep-backups": o.keepBackups,
		"--remove-docker": o.removeDocker, "--remove-packages": o.removePackages, "--disable-firewall": o.disableFirewall,
	}
	var used []string
	for _, k := range []string{"--confirm", "--backup-to", "--no-backup", "--keep-backups", "--remove-docker", "--remove-packages", "--disable-firewall"} {
		if purgeOnly[k] {
			used = append(used, k)
		}
	}
	switch {
	case verify && (purge || o.yes || o.dryRun || len(used) > 0):
		return "--verify only scans: it takes no other flag than --dir"
	case !purge && len(used) > 0:
		return strings.Join(used, ", ") + " only work with --purge"
	case !purge:
		return ""
	case o.backupTo != "" && o.noBackup:
		return "--backup-to and --no-backup contradict each other"
	case o.confirm != "" && !o.yes:
		return "--confirm goes with --yes (without --yes the token is typed)"
	case o.yes && !o.dryRun && o.confirm == "":
		return "--purge --yes needs --confirm <token>: the install's domain (else this host's name). Run --dry-run to see it"
	case o.yes && !o.dryRun && o.backupTo == "" && !o.noBackup:
		return "--purge --yes needs --backup-to FILE (a verified final backup) or --no-backup"
	}
	return ""
}

// uninstallContainers is the plain uninstall: it stops and removes the containers and keeps the
// data, the certificates and the install directory.
func (o *ops) uninstallContainers(opts purgeOptions) int {
	env, ok := o.requireInstall(opts.dir)
	if !ok {
		return 2
	}
	if opts.dryRun {
		fmt.Fprintf(o.out, "Dry run: would stop and remove the BOBRES containers in %s. The data, the certificates and %s stay. Nothing was changed.\n",
			opts.dir, opts.dir)
		return 0
	}
	if !opts.yes {
		fmt.Fprintf(o.out, "Stop and remove the BOBRES containers in %s? Data is kept. [y/N]: ", opts.dir)
		var answer string
		fmt.Fscanln(o.in, &answer) //nolint:errcheck // an empty answer means no
		if !strings.EqualFold(strings.TrimSpace(answer), "y") {
			fmt.Fprintln(o.out, "Cancelled.")
			return 1
		}
	}
	_ = env
	// Every profile is on, so that a container of a service whose profile is off
	// now (the tls-agent of a release the install went back from) goes too.
	cargs := composeArgs("--profile", "*", "down", "--remove-orphans")
	if out, err := o.run(context.Background(), opts.dir, cargs...); err != nil {
		fmt.Fprintf(o.errOut, "error: docker compose down failed: %v\n%s\n", err, strings.TrimSpace(string(out)))
		return 1
	}
	fmt.Fprintf(o.out, "Containers removed. Kept: the database and Redis volumes (%s_*), certificates, and %s (run `bobres install` to start again, or `bobres uninstall --purge` to delete everything).\n",
		composeProject, opts.dir)
	return 0
}

// dirOnly parses the --dir flag of start/stop/restart/backup and checks the
// install, returning its .env.
func (o *ops) dirOnly(name string, args []string) (string, map[string]string, bool) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(o.errOut)
	dir := fs.String("dir", defaultDir(), dirUsage)
	if err := fs.Parse(args); err != nil {
		return "", nil, false
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(o.errOut, "bobres %s: unexpected argument %q\n", name, fs.Arg(0))
		return "", nil, false
	}
	env, ok := o.requireInstall(*dir)
	if !ok {
		return "", nil, false
	}
	return *dir, env, true
}

// startCmd starts the stack (creating containers removed by uninstall) and
// waits until it is healthy.
func (o *ops) startCmd(args []string) int {
	dir, env, ok := o.dirOnly("start", args)
	if !ok {
		return 2
	}
	fmt.Fprintln(o.out, "Starting BOBRES...")
	if out, err := o.run(context.Background(), dir, composeArgs("up", "-d")...); err != nil {
		fmt.Fprintf(o.errOut, "error: docker compose up failed: %v\n%s\n", err, strings.TrimSpace(string(out)))
		return 1
	}
	return waitHealthy(context.Background(), o.run, o.sleep, dir, appServices(dir, env), o.out, o.errOut)
}

// stopCmd stops the containers; nothing is removed.
func (o *ops) stopCmd(args []string) int {
	dir, _, ok := o.dirOnly("stop", args)
	if !ok {
		return 2
	}
	if out, err := o.run(context.Background(), dir, composeArgs("stop")...); err != nil {
		fmt.Fprintf(o.errOut, "error: docker compose stop failed: %v\n%s\n", err, strings.TrimSpace(string(out)))
		return 1
	}
	fmt.Fprintln(o.out, "BOBRES is stopped. The bot does not answer until you start it again (bobres start).")
	return 0
}

// restartCmd restarts the containers and waits until they are healthy. It does
// not apply edited settings; `bobres install` (or the menu) does.
func (o *ops) restartCmd(args []string) int {
	dir, env, ok := o.dirOnly("restart", args)
	if !ok {
		return 2
	}
	fmt.Fprintln(o.out, "Restarting BOBRES...")
	if out, err := o.run(context.Background(), dir, composeArgs("restart")...); err != nil {
		fmt.Fprintf(o.errOut, "error: docker compose restart failed: %v\n%s\n", err, strings.TrimSpace(string(out)))
		return 1
	}
	return waitHealthy(context.Background(), o.run, o.sleep, dir, appServices(dir, env), o.out, o.errOut)
}

// backupCmd makes a backup now: the backup service's `run-now` asks the
// running service (one backup at a time) and waits for the result.
func (o *ops) backupCmd(args []string) int {
	dir, env, ok := o.dirOnly("backup", args)
	if !ok {
		return 2
	}
	if !backupEnabled(env) {
		fmt.Fprintf(o.errOut, "error: backups are off on this install (version %s has no backup service). Upgrade with: bobres install --version <new version>\n",
			orDash(env["BOBRES_VERSION"]))
		return 1
	}
	fmt.Fprintln(o.out, "Backing up the database (this can take a few minutes)...")
	if err := o.stream(context.Background(), dir, composeArgs("exec", "-T", "backup", "/app", "run-now")...); err != nil {
		fmt.Fprintf(o.errOut, "error: the backup did not complete: %v (see above; bobres logs backup)\n", err)
		return 1
	}
	return 0
}

// waitHealthy polls the services until every one of want (appServices)
// reports healthy, for five minutes at most.
func waitHealthy(ctx context.Context, run func(context.Context, string, ...string) ([]byte, error),
	sleep func(time.Duration), dir string, want []string, out, errOut io.Writer) int {
	return waitHealthyProgress(ctx, run, sleep, dir, want, out, errOut, nil)
}

// waitHealthyProgress is waitHealthy telling onProgress (when set) how many of
// the services are healthy after each look, for a progress bar.
func waitHealthyProgress(ctx context.Context, run func(context.Context, string, ...string) ([]byte, error),
	sleep func(time.Duration), dir string, want []string, out, errOut io.Writer, onProgress func(healthy, total int)) int {
	deadline := 5 * time.Minute
	for waited := time.Duration(0); ; waited += 5 * time.Second {
		ps, err := run(ctx, dir, composeArgs("ps", "--format", "json")...)
		health := map[string]string{}
		if err == nil {
			health = parseHealth(ps)
		}
		var pending []string
		for _, s := range want {
			if health[s] != "healthy" {
				pending = append(pending, s+"="+orUnknown(health[s]))
			}
		}
		if onProgress != nil {
			onProgress(len(want)-len(pending), len(want))
		}
		if len(pending) == 0 {
			fmt.Fprintln(out, "All services are healthy.")
			return 0
		}
		if waited >= deadline {
			fmt.Fprintf(errOut, "error: services not healthy after %s: %s\nInspect with: bobres logs --dir %s\n",
				deadline, strings.Join(pending, ", "), dir)
			return 1
		}
		sleep(5 * time.Second)
	}
}

// coreExitNotStaff is the exit status of core's login-link for a Telegram id
// that is not an active staff member yet (cmd/core exitNotStaff).
const coreExitNotStaff = 3

// notStaffAdvice is what to do when the login link is refused because the
// owner has not started the bot: one sentence, with the bot's name when it is
// known.
func notStaffAdvice(bot string) string {
	who := "your bot"
	if bot != "" {
		who = "@" + strings.TrimPrefix(bot, "@")
	}
	return "Open " + who + " in Telegram and send /start once, then run this again."
}

// botName is the install's bot username (@name without the @), asked of
// Telegram with the bot token in dir's .env; "" when it cannot be told.
func (o *ops) botName(ctx context.Context, dir string) string {
	if o.lookupBot != nil {
		return o.lookupBot(ctx, dir)
	}
	env, ok, err := readEnv(filepath.Join(dir, ".env"))
	if err != nil || !ok || env["BOBRES_TELEGRAM_BOT_TOKEN"] == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	name, err := botUsername(ctx, env["BOBRES_TELEGRAM_BOT_TOKEN"], telegramAPIRoot(env))
	if err != nil {
		return ""
	}
	return name
}

const licenseCmdUsage = "usage: bobres license status|import [--dir D]  (import reads the token on stdin)\n" +
	"       " + licenseActivateUsage

// licenseCmd: "bobres license status" shows the installed license as core
// sees it; "bobres license import" installs the token read from stdin (a
// file or a paste ended with Ctrl-D). Both run inside the core container.
// "bobres license activate" gets the token from the license server.
func (o *ops) licenseCmd(args []string) int {
	if len(args) > 0 && args[0] == "activate" {
		return o.licenseActivate(args[1:])
	}
	if len(args) == 0 || (args[0] != "status" && args[0] != "import") {
		fmt.Fprintln(o.errOut, licenseCmdUsage)
		return 2
	}
	fs := flag.NewFlagSet("license "+args[0], flag.ContinueOnError)
	fs.SetOutput(o.errOut)
	dir := fs.String("dir", defaultDir(), dirUsage)
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintln(o.errOut, licenseCmdUsage)
		return 2
	}
	if _, ok := o.requireInstall(*dir); !ok {
		return 2
	}
	if args[0] == "import" && interactive(o.in, o.out) {
		fmt.Fprintln(o.out, "Paste the license token, then press Enter and Ctrl-D:")
	}
	if err := o.stream(context.Background(), *dir, composeArgs("exec", "-T", "core", "/app", "license", args[0])...); err != nil {
		fmt.Fprintf(o.errOut, "error: %v (is core running? bobres status)\n", err)
		return 1
	}
	return 0
}

const licenseActivateUsage = "usage: bobres license activate [<key>] [--server URL] [--dir D]  " +
	"(without <key> it is asked for, or read on stdin: out of the shell history)"

// licenseActivate: "bobres license activate [<key>]" turns a license key
// into this install's license. The host makes the call (core has no internet
// access): it reads the install id and domain from core (/app license info),
// sends them with the key to the license server's /v1/activate
// (BOBRES_LICENSE_SERVER, https only, no redirects) and pipes the token it
// answers into /app license import --activated, which verifies it. Core then
// renews it once a day through the bot. The key is never printed or kept.
func (o *ops) licenseActivate(args []string) int {
	fs := flag.NewFlagSet("license activate", flag.ContinueOnError)
	fs.SetOutput(o.errOut)
	dir := fs.String("dir", defaultDir(), dirUsage)
	server := fs.String("server", "", "the license server (default: BOBRES_LICENSE_SERVER from the environment, else from <dir>/.env)")
	rest, err := parseArgs(fs, args)
	if err != nil {
		return 2
	}
	var key string
	switch len(rest) {
	case 0:
		if key, err = o.readLicenseKey(); err != nil {
			fmt.Fprintf(o.errOut, "error: read the license key: %v\n", err)
			return 2
		}
	case 1:
		key = rest[0]
	default:
		fmt.Fprintln(o.errOut, licenseActivateUsage)
		return 2
	}
	if key = strings.TrimSpace(key); key == "" || strings.ContainsFunc(key, unicode.IsSpace) {
		fmt.Fprintln(o.errOut, licenseActivateUsage)
		return 2
	}
	env, ok := o.requireInstall(*dir)
	if !ok {
		return 2
	}
	url, err := licenseServer(*server, env, *dir)
	if err != nil {
		fmt.Fprintf(o.errOut, "error: %v\n", err)
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// 1. This install as core binds a license to it.
	var info bytes.Buffer
	if err := o.pipe(ctx, *dir, strings.NewReader(""), &info, composeArgs("exec", "-T", "core", "/app", "license", "info", "--json")...); err != nil {
		fmt.Fprintf(o.errOut, "error: read this install's id from core: %v (is core running? bobres status)\n", err)
		return 1
	}
	var inst licenseActivation
	if err := json.Unmarshal(info.Bytes(), &inst); err != nil || inst.InstallID == "" {
		fmt.Fprintln(o.errOut, "error: core did not say this install's id (is this core older than this bobres? run bobres update)")
		return 1
	}
	if inst.Domain == "" {
		fmt.Fprintln(o.errOut, "error: this install has no domain (BOBRES_DOMAIN), and a license is bound to one")
		return 1
	}
	inst.Key = key

	// 2. The license server answers a token bound to it.
	fmt.Fprintf(o.out, "Activating the license for %s (install %s) with %s ...\n", inst.Domain, inst.InstallID, url)
	token, err := o.requestLicense(ctx, url, inst)
	if err != nil {
		fmt.Fprintf(o.errOut, "error: %v\nNothing was changed.\n", err)
		return 1
	}

	// 3. Core verifies it and installs it.
	if err := o.pipe(ctx, *dir, strings.NewReader(token+"\n"), o.out,
		composeArgs("exec", "-T", "core", "/app", "license", "import", "--activated")...); err != nil {
		fmt.Fprintf(o.errOut, "error: core did not install the license: %v\n", err)
		return 1
	}
	return 0
}

// licenseActivation is the license server's POST /v1/activate body; core's
// `license info --json` gives the install id, domain and version.
type licenseActivation struct {
	Key       string `json:"key"`
	InstallID string `json:"install_id"`
	Domain    string `json:"domain"`
	Version   string `json:"version"`
}

// maxLicenseKeyInput bounds what is read as a license key.
const maxLicenseKeyInput = 4 << 10

// readLicenseKey asks for the key without echoing it on a terminal, else
// reads the first line of stdin.
func (o *ops) readLicenseKey() (string, error) {
	if f, ok := o.in.(*os.File); ok {
		if read := hiddenInput(f, o.out); read != nil {
			return read("License key: ")
		}
	}
	b, err := io.ReadAll(io.LimitReader(o.in, maxLicenseKeyInput))
	if err != nil {
		return "", err
	}
	line, _, _ := strings.Cut(string(b), "\n")
	return strings.TrimSpace(line), nil
}

// licenseServer is the license server's address: --server, else
// BOBRES_LICENSE_SERVER from the environment, else from .env; https only.
func licenseServer(flagValue string, env map[string]string, dir string) (string, error) {
	raw := flagValue
	if raw == "" {
		raw = os.Getenv("BOBRES_LICENSE_SERVER")
	}
	if strings.TrimSpace(raw) == "" {
		raw = env["BOBRES_LICENSE_SERVER"]
	}
	url, err := config.LicenseServerURL(raw)
	if err != nil {
		return "", err
	}
	if url == "" {
		set := "sudo bobres config set BOBRES_LICENSE_SERVER https://…"
		if dir != defaultDir() {
			set += " --dir " + dir
		}
		return "", fmt.Errorf("no license server: set the vendor's address with  %s  (or pass --server https://…)", set)
	}
	return url, nil
}

// maxLicenseAnswer bounds the license server's answer.
const maxLicenseAnswer = 64 << 10

// requestLicense POSTs a to the license server's /v1/activate and answers
// its token, or its refusal in words. Errors never hold the key.
func (o *ops) requestLicense(ctx context.Context, server string, a licenseActivation) (string, error) {
	body, err := json.Marshal(a)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server+"/v1/activate", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("the license server address is not usable: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	hc := &http.Client{Timeout: 30 * time.Second}
	if o.httpc != nil {
		c := *o.httpc
		hc = &c
	}
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("the license server did not answer: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		Token   string `json:"token"`
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxLicenseAnswer+1))
	if err == nil && len(raw) <= maxLicenseAnswer {
		err = json.Unmarshal(raw, &out)
	} else if err == nil {
		err = errors.New("too long")
	}
	switch {
	case err == nil && resp.StatusCode == http.StatusOK && out.Token != "":
		return out.Token, nil
	case err == nil && resp.StatusCode/100 == 4 && out.Error != "":
		msg := fmt.Sprintf("the license server refused: %s (%s)", out.Message, out.Error)
		if ra := resp.Header.Get("Retry-After"); resp.StatusCode == http.StatusTooManyRequests && ra != "" {
			msg += "; try again in " + ra + " seconds"
		}
		return "", errors.New(msg)
	}
	return "", fmt.Errorf("the license server answered %d", resp.StatusCode)
}
