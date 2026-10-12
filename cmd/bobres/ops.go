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
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/config"
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
	// httpc calls the vendor's license server (nil: a 30-second client;
	// redirects are never followed).
	httpc *http.Client
	// s3c downloads a backup for `bobres restore s3://...` (nil: a plain
	// client; the download's own timeout applies).
	s3c *http.Client
}

func newOps(in io.Reader, out, errOut io.Writer) *ops {
	return &ops{
		in: in, out: out, errOut: errOut,
		run: func(ctx context.Context, dir string, args ...string) ([]byte, error) {
			cmd := exec.CommandContext(ctx, "docker", args...) //nolint:gosec // fixed program, arguments built here
			cmd.Dir = dir
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
// service only where its profile is on.
func appServices(env map[string]string) []string {
	s := []string{"core", "payments", "provisioner", "bot"}
	if backupEnabled(env) {
		s = append(s, "backup")
	}
	return s
}

// allServices are every container of an install, as status lists them.
func allServices(env map[string]string) []string {
	s := []string{"caddy", "postgres", "redis", "core", "payments", "provisioner", "bot"}
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
	if err := fs.Parse(args); err != nil {
		return 2
	}
	env, ok := o.requireInstall(*dir)
	if !ok {
		return 2
	}
	fmt.Fprintf(o.out, "Install:  %s\nVersion:  %s\nDomain:   %s\n", *dir, orDash(env["BOBRES_VERSION"]), orDash(env["BOBRES_DOMAIN"]))
	if gb := o.freeGB(*dir); gb >= 0 {
		fmt.Fprintf(o.out, "Disk:     %.1f GB free\n", gb)
	}
	out, err := o.run(context.Background(), *dir, composeArgs("ps", "--all", "--format", "json")...)
	if err != nil {
		fmt.Fprintf(o.errOut, "error: docker compose ps failed: %v\n%s\n", err, strings.TrimSpace(string(out)))
		return 1
	}
	rows := parseServices(out)
	want := allServices(env)
	fmt.Fprintln(o.out, "\nSERVICE       STATE        HEALTH")
	problems := 0
	seen := map[string]bool{}
	for _, name := range want {
		r, found := rows[name]
		seen[name] = true
		if !found {
			fmt.Fprintf(o.out, "%-13s %-12s %s\n", name, "missing", "-")
			problems++
			continue
		}
		health := r.Health
		if health == "" {
			health = "-"
		}
		fmt.Fprintf(o.out, "%-13s %-12s %s\n", name, r.State, health)
		if r.State != "running" || (r.Health != "" && r.Health != "healthy") {
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
		fmt.Fprintf(o.out, "%-13s %-12s %s\n", name, r.State, orDash(r.Health))
	}
	if problems > 0 {
		fmt.Fprintf(o.out, "\n%d service(s) need attention. See: bobres logs <service>\n", problems)
		return 1
	}
	fmt.Fprintln(o.out, "\nAll services are running.")
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

// uninstallCmd stops and removes the containers. Data (database, Redis,
// certificates) and the install directory stay unless --purge is given.
func (o *ops) uninstallCmd(args []string) int {
	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	fs.SetOutput(o.errOut)
	dir := fs.String("dir", defaultDir(), dirUsage)
	purge := fs.Bool("purge", false, "also delete the database, Redis data, certificates and the install directory (irreversible)")
	yes := fs.Bool("yes", false, "do not ask for confirmation")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	env, ok := o.requireInstall(*dir)
	if !ok {
		return 2
	}
	if *purge {
		// The backups live in the install directory: they go too.
		fmt.Fprintf(o.out, "Warning: --purge also deletes every backup in %s. Copy them elsewhere first if you may need them.\n",
			filepath.Join(*dir, "backups"))
	}
	if !*yes {
		if *purge {
			fmt.Fprintf(o.out, "This deletes ALL data of %s (users, orders, ledger, panel token, backups) and %s.\nType the domain (%s) to confirm: ",
				orDash(env["BOBRES_DOMAIN"]), *dir, env["BOBRES_DOMAIN"])
		} else {
			fmt.Fprintf(o.out, "Stop and remove the BOBRES containers in %s? Data is kept. [y/N]: ", *dir)
		}
		var answer string
		fmt.Fscanln(o.in, &answer) //nolint:errcheck // an empty answer means no
		answer = strings.TrimSpace(answer)
		confirmed := (*purge && answer == env["BOBRES_DOMAIN"] && answer != "") || (!*purge && strings.EqualFold(answer, "y"))
		if !confirmed {
			fmt.Fprintln(o.out, "Cancelled.")
			return 1
		}
	}
	cargs := composeArgs("down", "--remove-orphans")
	if *purge {
		cargs = append(cargs, "--volumes")
	}
	if out, err := o.run(context.Background(), *dir, cargs...); err != nil {
		fmt.Fprintf(o.errOut, "error: docker compose down failed: %v\n%s\n", err, strings.TrimSpace(string(out)))
		return 1
	}
	if !*purge {
		fmt.Fprintf(o.out, "Containers removed. Kept: the database and Redis volumes (%s_*), certificates, and %s (run `bobres install` to start again, or `bobres uninstall --purge` to delete everything).\n",
			composeProject, *dir)
		return 0
	}
	if err := os.RemoveAll(*dir); err != nil {
		fmt.Fprintf(o.errOut, "error: remove %s: %v\n", *dir, err)
		return 1
	}
	fmt.Fprintf(o.out, "Removed containers, volumes and %s. Nothing of BOBRES remains on this server except the images (docker image prune to drop them).\n", *dir)
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
	return waitHealthy(context.Background(), o.run, o.sleep, dir, appServices(env), o.out, o.errOut)
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
	return waitHealthy(context.Background(), o.run, o.sleep, dir, appServices(env), o.out, o.errOut)
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

// adminCmd: "bobres admin link [--dir D] [telegram id]" prints a one-time
// dashboard login link for the owner (or that staff member): the way into
// the dashboard when the bot is down. It works once, within 2 minutes.
func (o *ops) adminCmd(args []string) int {
	if len(args) == 0 || args[0] != "link" {
		fmt.Fprintln(o.errOut, "usage: bobres admin link [--dir D] [telegram id]")
		return 2
	}
	fs := flag.NewFlagSet("admin link", flag.ContinueOnError)
	fs.SetOutput(o.errOut)
	dir := fs.String("dir", defaultDir(), dirUsage)
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if fs.NArg() > 1 {
		fmt.Fprintln(o.errOut, "usage: bobres admin link [--dir D] [telegram id]")
		return 2
	}
	if _, ok := o.requireInstall(*dir); !ok {
		return 2
	}
	cargs := append(composeArgs("exec", "-T", "core", "/app", "login-link"), fs.Args()...)
	if err := o.stream(context.Background(), *dir, cargs...); err != nil {
		fmt.Fprintf(o.errOut, "error: %v (is core running? bobres status)\n", err)
		return 1
	}
	return 0
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
