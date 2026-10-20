package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/deploy"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/config"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/release"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/version"
	"golang.org/x/term"
)

// installer holds the side effects install needs, so tests can replace them.
type installer struct {
	in        *bufio.Reader
	out       io.Writer
	errOut    io.Writer
	getenv    func(string) string
	readHide  func(prompt string) (string, error) // hidden input; nil when not a terminal
	run       func(ctx context.Context, dir string, args ...string) ([]byte, error)
	checkBot  func(ctx context.Context, token, apiRoot string) (string, error) // getMe at apiRoot ("": api.telegram.org)
	lookupIP  func(ctx context.Context, host string) ([]net.IP, error)
	preflight func(domain string) []Result
	sleep     func(time.Duration)
	// harden runs the hardening step (bobres harden); nil skips it.
	harden func(ctx context.Context, opt hardenOptions) int
	// quiet leaves out the first-steps summary (a settings change from the menu).
	quiet bool
	// login is the owner's initial dashboard login, issued by a fresh install
	// after the stack is up; only summary reads it and it is never written
	// anywhere (not the log, not .env, not the state files).
	login *ownerLogin
	// runLines runs docker with each line of its output handed to onLine as it
	// comes (the progress of `compose up`); nil: run is used and nothing is measured.
	runLines func(ctx context.Context, dir string, onLine func(string), args ...string) ([]byte, error)
	// prog shows the install's progress; nil outside install (it does nothing).
	prog *progress
	// ui is how this run's output looks (set when the run starts).
	ui termUI
	// logPath is the install log ("" for none); logFile is it while open.
	logPath string
	logFile *os.File
	// panelProbe looks at a 3x-ui panel the way the provisioner does (nil: the
	// panel is not checked).
	panelProbe func(ctx context.Context, baseURL, token string, allowPrivate bool) (panelInfo, error)
	// survive makes the run outlive a dropped SSH connection (nil: nothing).
	survive func()
	// botName is the bot's username once Telegram has said it.
	botName string
	// units installs the dashboard updater's systemd units once the store is up (nil: it is
	// not installed). exe is the bobres they run and stateDir the release state directory.
	units    *systemUnits
	exe      func() (string, error)
	stateDir string
}

func newInstaller(in io.Reader, out, errOut io.Writer) *installer {
	br, ok := in.(*bufio.Reader) // the menu shares its reader, so no typed-ahead input is lost
	if !ok {
		br = bufio.NewReader(in)
	}
	ins := &installer{
		in: br, out: out, errOut: errOut, getenv: os.Getenv,
		lookupIP: func(ctx context.Context, host string) ([]net.IP, error) {
			return net.DefaultResolver.LookupIP(ctx, "ip", host)
		},
		run: func(ctx context.Context, dir string, args ...string) ([]byte, error) {
			cmd := exec.CommandContext(ctx, "docker", args...) //nolint:gosec // fixed program, arguments built here
			cmd.Dir = dir
			return cmd.CombinedOutput()
		},
		checkBot:  botUsername,
		preflight: func(domain string) []Result { return runDoctor(domain, false) },
		sleep:     time.Sleep,
		runLines:  streamDocker,
		logPath:   installLogPath, panelProbe: probePanel, survive: keepGoing,
		units: newSystemUnits(), exe: selfExe, stateDir: defaultStateDir(),
	}
	if p := strings.TrimSpace(os.Getenv("BOBRES_INSTALL_LOG")); p != "" {
		ins.logPath = p
	}
	if f, ok := in.(*os.File); ok {
		ins.readHide = hiddenInput(f, out)
	}
	// It asks on the installer's reader, as it is when the step runs.
	ins.harden = func(ctx context.Context, opt hardenOptions) int {
		h := newHardener(ins.in, ins.out, ins.errOut)
		if opt.auto {
			// Nothing is asked and the commands' output is the log's: the
			// screen gets one line per step.
			h.out, h.errOut, h.notify = ins.logOut(), ins.logOut(), ins.prog.Note
		}
		return h.apply(ctx, opt)
	}
	return ins
}

// hiddenInput reads a secret from the terminal f without echoing it; nil when
// f is not a terminal.
func hiddenInput(f *os.File, out io.Writer) func(prompt string) (string, error) {
	if !term.IsTerminal(int(f.Fd())) {
		return nil
	}
	return func(prompt string) (string, error) {
		fmt.Fprint(out, prompt)
		b, err := term.ReadPassword(int(f.Fd()))
		fmt.Fprintln(out)
		return strings.TrimSpace(string(b)), err
	}
}

func installCmd(args []string, in io.Reader, out, errOut io.Writer) int {
	return newInstaller(in, out, errOut).install(args)
}

// envOrder is the order of keys in the generated .env.
var envOrder = []string{
	"BOBRES_ENV", "BOBRES_LOG_LEVEL", "BOBRES_LOG_FORMAT", "BOBRES_VERSION",
	"BOBRES_IMAGE_DIGEST_CORE", "BOBRES_IMAGE_DIGEST_BOT", "BOBRES_IMAGE_DIGEST_PAYMENTS",
	"BOBRES_IMAGE_DIGEST_PROVISIONER", "BOBRES_IMAGE_DIGEST_BACKUP", "BOBRES_DOMAIN",
	"POSTGRES_PASSWORD", "DB_CORE_PASSWORD", "DB_PAYMENTS_PASSWORD", "DB_PROVISIONER_PASSWORD", "DB_BACKUP_PASSWORD",
	"REDIS_PASSWORD", "BOBRES_REDIS_URL",
	"BOBRES_TOKEN_CORE", "BOBRES_TOKEN_CORE_PROVISIONER", "BOBRES_TOKEN_CORE_BOT",
	"BOBRES_TOKEN_BOT", "BOBRES_TOKEN_BACKUP", "BOBRES_TOKEN_CORE_BACKUP",
	"BOBRES_CORE_MASTER_KEY", "BOBRES_PROVISIONER_MASTER_KEY", "BOBRES_PAYMENTS_MASTER_KEY",
	"BOBRES_BACKUP_KEY", "BOBRES_BACKUP_TIME", "COMPOSE_PROFILES", "BOBRES_BACKUP_URL",
	"BOBRES_BACKUP_S3_ENDPOINT", "BOBRES_BACKUP_S3_BUCKET", "BOBRES_BACKUP_S3_REGION", "BOBRES_BACKUP_S3_PREFIX",
	"BOBRES_BACKUP_S3_ACCESS_KEY", "BOBRES_BACKUP_S3_SECRET_KEY", "BOBRES_BACKUP_S3_KEEP", "BOBRES_BACKUP_NETWORK",
	"BOBRES_TELEGRAM_BOT_TOKEN", "BOBRES_ADMIN_TELEGRAM_ID", "BOBRES_TIMEZONE",
	"BOBRES_XUI_URL", "BOBRES_XUI_TOKEN", "BOBRES_XUI_SUB_URL", "BOBRES_XUI_ALLOW_PRIVATE",
	"BOBRES_ZARINPAL_MERCHANT_ID", "BOBRES_ZARINPAL_PROXY", "BOBRES_ZARINPAL_PUBLIC_URL", "BOBRES_ZARINPAL_HOST",
}

// serviceTokens are the internal service tokens (each a different random
// value), which `bobres secrets rotate service-tokens` changes together. A
// service presents its own token to core (BOBRES_TOKEN_BOT) or to the bot
// (BOBRES_TOKEN_BACKUP); core presents a different one to each service it
// calls: payments BOBRES_TOKEN_CORE, provisioner BOBRES_TOKEN_CORE_PROVISIONER,
// the bot BOBRES_TOKEN_CORE_BOT and the backup service BOBRES_TOKEN_CORE_BACKUP.
// So no container holds a token another one accepts as core.
var serviceTokens = []string{
	"BOBRES_TOKEN_CORE", "BOBRES_TOKEN_CORE_PROVISIONER", "BOBRES_TOKEN_CORE_BOT", "BOBRES_TOKEN_CORE_BACKUP",
	"BOBRES_TOKEN_BOT", "BOBRES_TOKEN_BACKUP",
}

// generated secrets: created once, then kept on every re-run (changing a
// database password would lock the services out of an existing database).
var generated = map[string]int{ // key -> random bytes (hex-encoded)
	"POSTGRES_PASSWORD": 24, "DB_CORE_PASSWORD": 24, "DB_PAYMENTS_PASSWORD": 24, "DB_PROVISIONER_PASSWORD": 24,
	"DB_BACKUP_PASSWORD": 24, "REDIS_PASSWORD": 24, "BOBRES_TOKEN_CORE": 32, "BOBRES_TOKEN_BOT": 32, "BOBRES_TOKEN_BACKUP": 32,
	// Core presents a token of its own to each service it calls (serviceTokens):
	// one shared token would let any of them act as core toward the others.
	"BOBRES_TOKEN_CORE_PROVISIONER": 32, "BOBRES_TOKEN_CORE_BOT": 32,
	"BOBRES_TOKEN_CORE_BACKUP": 32, "BOBRES_CORE_MASTER_KEY": 32, "BOBRES_PROVISIONER_MASTER_KEY": 32, "BOBRES_PAYMENTS_MASTER_KEY": 32,
	// Encrypts the backups: shown once by the run that saves it, again in the menu.
	"BOBRES_BACKUP_KEY": 32,
}

var (
	domainRe   = regexp.MustCompile(`(?i)^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`)
	botTokenRe = regexp.MustCompile(`^\d{5,15}:[A-Za-z0-9_-]{30,64}$`)
	safeEnvRe  = regexp.MustCompile(`^[A-Za-z0-9_\-:./@,+=]*$`)
)

// install runs the installation with its banner and progress: the steps of
// installSteps, one bar from 0 to 100 (or its share of a script's bar), a plain
// line per step where output is not a terminal.
func (ins *installer) install(args []string) int {
	out, errOut := ins.out, ins.errOut
	defer func() {
		ins.prog.Close()
		if ins.logFile != nil {
			_ = ins.logFile.Close()
		}
		ins.prog, ins.logFile, ins.out, ins.errOut = nil, nil, out, errOut
	}()
	code := ins.runInstall(args)
	if code == 0 {
		ins.prog.Complete()
		return code
	}
	failedAt := ins.prog.failedStep()
	ins.prog.Fail(fmt.Sprintf("exit %d", code))
	// A failure after the questions: the end of the log, and what to do.
	if failedAt != "" && failedAt != stepWizard && failedAt != stepChecks && ins.logPath != "" {
		if tail := tailLog(ins.logPath, 15); tail != "" {
			fmt.Fprintf(errOut, "\nThe last lines of %s:\n%s\n", ins.logPath, tail)
		}
		fmt.Fprintf(errOut, "\nWhat to do: fix the cause named above, then run  sudo bobres install  again: it keeps your settings and\n"+
			"secrets and picks up where this stopped. More: bobres doctor, bobres logs.\n")
	}
	return code
}

func (ins *installer) runInstall(args []string) int {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	fs.SetOutput(ins.errOut)
	dir := fs.String("dir", defaultDir(), dirUsage)
	domain := fs.String("domain", "", "public domain of this install (DNS must point here)")
	adminID := fs.String("admin-id", "", "Telegram user id of the owner; several owners: their ids separated by commas, the main owner first")
	xuiURL := fs.String("xui-url", "", "first 3x-ui panel URL (https; plain http only with --xui-allow-private), including any web base path; optional: servers can be added in the dashboard")
	xuiSub := fs.String("xui-sub-url", "", "public prefix of the panel's subscription links, e.g. https://sub.example.com:2096/sub/")
	allowPrivate := fs.Bool("xui-allow-private", false, "the panel is on this host or a private network")
	zpMerchant := fs.String("zarinpal-merchant-id", "", "enable Zarinpal with this merchant id (36-character id from the Zarinpal panel)")
	zpProxy := fs.String("zarinpal-proxy", "", "http(s) proxy on a server whose IP is registered in the Zarinpal panel")
	zpPublic := fs.String("zarinpal-public-url", "", "domain registered with Zarinpal for the pay page and callback (default: https://<domain>)")
	s3Endpoint := fs.String("s3-endpoint", "", "copy each backup to this S3-compatible storage, https://host (Arvan, MinIO, Backblaze B2, AWS); the bucket and keys are asked if not given")
	s3Bucket := fs.String("s3-bucket", "", "the bucket for --s3-endpoint")
	s3Region := fs.String("s3-region", "", "the bucket's region (default: us-east-1)")
	s3Prefix := fs.String("s3-prefix", "", "a folder in the bucket for the backups (default: none)")
	imageVersion := fs.String("version", "", "image tag to run (default: this CLI's version)")
	noStart := fs.Bool("no-start", false, "write the files but do not start the stack")
	offline := fs.Bool("offline", false, "skip network checks (bot token)")
	skipChecks := fs.Bool("skip-checks", false, "skip the system checks")
	quiet := fs.Bool("quiet", false, "leave out the first-steps summary (bobres update runs install this way)")
	yes := fs.Bool("yes", false, "never ask: a value still missing is an error, and the hardening step is skipped (bobres update runs install this way)")
	noHarden := fs.Bool("no-harden", false, "skip the hardening step (firewall, fail2ban, security updates, swap; bobres harden runs it later)")
	askHarden := fs.Bool("ask-harden", false, "hardening: ask before each step instead of doing what is safe by itself")
	sshPort := fs.Int("ssh-port", 0, "hardening: an SSH port the firewall keeps open (needed when it cannot be detected)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *sshPort < 0 || *sshPort > 65535 {
		fmt.Fprintln(ins.errOut, "error: --ssh-port must be a port number")
		return 2
	}
	if *quiet {
		ins.quiet = true
	}
	if *yes {
		ins.in, ins.readHide = bufio.NewReader(strings.NewReader("")), nil
	}
	ctx := context.Background()

	// The banner once, for a run that is not a script's (install.sh has shown
	// its own) and not quiet; then the progress, from here on.
	ho, ui := parseHandoff(ins.getenv(handoffEnv)), detectUI(ins.out, ins.getenv)
	if !ins.quiet && !ho.ok && !bannerOff(ins.getenv) {
		printBanner(ins.out, ui)
	}
	ins.startProgress(ui, ho)
	ins.prog.Begin(stepWizard)

	envPath := filepath.Join(*dir, ".env")
	env, existing, err := readEnv(envPath)
	if err != nil {
		fmt.Fprintf(ins.errOut, "error: read %s: %v\n", envPath, err)
		return 1
	}
	if existing {
		fmt.Fprintf(ins.out, "Existing install found in %s: keeping its secrets and settings (repair/upgrade).\n", *dir)
	}

	set := func(key, v string) {
		if v = strings.TrimSpace(v); v != "" {
			env[key] = v
		}
	}
	panelBefore := panelValues(env)
	set("BOBRES_DOMAIN", ins.getenv("BOBRES_DOMAIN"))
	set("BOBRES_ADMIN_TELEGRAM_ID", ins.getenv("BOBRES_ADMIN_TELEGRAM_ID"))
	set("BOBRES_DOMAIN", *domain)
	set("BOBRES_ADMIN_TELEGRAM_ID", *adminID)
	set("BOBRES_XUI_URL", *xuiURL)
	set("BOBRES_XUI_SUB_URL", *xuiSub)
	set("BOBRES_TELEGRAM_BOT_TOKEN", ins.getenv("BOBRES_TELEGRAM_BOT_TOKEN"))
	set("BOBRES_XUI_TOKEN", ins.getenv("BOBRES_XUI_TOKEN"))
	if *allowPrivate {
		env["BOBRES_XUI_ALLOW_PRIVATE"] = "true"
	}
	set("BOBRES_ZARINPAL_MERCHANT_ID", strings.ToLower(*zpMerchant))
	set("BOBRES_ZARINPAL_PROXY", *zpProxy)
	set("BOBRES_ZARINPAL_PUBLIC_URL", *zpPublic)
	// A Zarinpal domain other than the main one is served by the same Caddy site.
	env["BOBRES_ZARINPAL_HOST"] = zarinpalHost(env)
	set("BOBRES_BACKUP_S3_ENDPOINT", strings.TrimRight(strings.TrimSpace(*s3Endpoint), "/"))
	set("BOBRES_BACKUP_S3_BUCKET", strings.ToLower(*s3Bucket))
	set("BOBRES_BACKUP_S3_REGION", *s3Region)
	set("BOBRES_BACKUP_S3_PREFIX", *s3Prefix)
	// The keys only from the environment (or asked), never a flag: ps and the
	// shell history keep command lines.
	set("BOBRES_BACKUP_S3_ACCESS_KEY", ins.getenv("BOBRES_BACKUP_S3_ACCESS_KEY"))
	set("BOBRES_BACKUP_S3_SECRET_KEY", ins.getenv("BOBRES_BACKUP_S3_SECRET_KEY"))
	if existing && env["BOBRES_XUI_URL"] != "" && panelValues(env) != panelBefore {
		fmt.Fprintln(ins.out, "Note: the 3x-ui panel values only add the first server while the store has none. "+
			"To change a panel the store already uses, edit it in the dashboard (Servers).")
	}

	// Ask for what is still missing (secrets without echo): only what has no
	// safe default. Everything else is decided here or comes from flags.
	introduced := false
	intro := func() {
		if !introduced && !*yes {
			introduced = true
			fmt.Fprintln(ins.out, "A few questions; everything else is automatic.")
		}
	}
	for _, q := range []struct{ key, prompt string }{
		{"BOBRES_DOMAIN", "Domain of this install (e.g. panel.example.com): "},
		{"BOBRES_ADMIN_TELEGRAM_ID", "Your numeric Telegram user id (the owner; several owners: separate the ids with commas): "},
		{"BOBRES_TELEGRAM_BOT_TOKEN", "Telegram bot token from @BotFather: "},
	} {
		if env[q.key] != "" {
			continue
		}
		intro()
		v, err := ins.ask(q.prompt, strings.HasSuffix(q.key, "_TOKEN"))
		if err != nil {
			fmt.Fprintf(ins.errOut, "error: %s is required (flag, environment variable, or an interactive terminal)\n", q.key)
			return 2
		}
		set(q.key, v)
	}
	// The first 3x-ui panel is optional (servers are added in the dashboard)
	// and asked on a fresh install only; the provisioner registers it while
	// it has no server yet.
	if !existing && env["BOBRES_XUI_URL"] == "" {
		intro()
		set("BOBRES_XUI_URL", ins.askOptional("3x-ui panel URL (https://host:port/path; Enter to add servers later in the dashboard): "))
	}
	if env["BOBRES_XUI_URL"] != "" {
		ins.panelOnThisServer(env)
	}
	if env["BOBRES_XUI_URL"] != "" && env["BOBRES_XUI_TOKEN"] == "" {
		intro()
		v, err := ins.ask(panelTokenPrompt, true)
		if err != nil {
			fmt.Fprintln(ins.errOut, "error: BOBRES_XUI_TOKEN is required with the panel URL (environment variable, or an interactive terminal)")
			return 2
		}
		set("BOBRES_XUI_TOKEN", v)
	}
	// A panel that is new in this run is looked at now, before anything is
	// written: a token it refuses must not pass for an install that works.
	if env["BOBRES_XUI_URL"] != "" && !*offline && (!existing || panelValues(env) != panelBefore) {
		if code := ins.verifyPanel(ctx, env); code != 0 {
			return code
		}
	}

	defaults := map[string]string{
		"BOBRES_ENV": "prod", "BOBRES_LOG_LEVEL": "info", "BOBRES_LOG_FORMAT": "json",
		"BOBRES_TIMEZONE": "Asia/Tehran", "BOBRES_XUI_ALLOW_PRIVATE": "false", "BOBRES_BACKUP_TIME": "03:30",
	}
	for k, v := range defaults {
		if env[k] == "" {
			env[k] = v
		}
	}
	if v := strings.TrimSpace(*imageVersion); v != "" {
		env["BOBRES_VERSION"] = v
	} else if env["BOBRES_VERSION"] == "" || !existing {
		env["BOBRES_VERSION"] = imageTag(version.Version)
	}
	// This CLI's own images run by the digests its signed release carries,
	// not by their tag, which whoever can push to the registry can move.
	pins, err := releaseDigests(imageDigests)
	if err != nil {
		fmt.Fprintf(ins.errOut, "error: this bobres build's image digests are broken (%v): download the release again\n", err)
		return 1
	}
	pinImages(env, pins)
	// Follows the version both ways: a rollback to a release from before the
	// backup service would otherwise fail to pull its image.
	if backupImage(env["BOBRES_VERSION"]) {
		enableBackups(env)
	} else {
		disableBackups(env)
	}
	// The same for the tls-agent: the compose file always defines it, so an
	// image without it (0.1.0-rc.1) must keep it out of what compose starts.
	if tlsAgentImage(env["BOBRES_VERSION"]) {
		addProfile(env, tlsProfile)
	} else {
		dropProfile(env, tlsProfile)
	}
	if code := ins.askS3(env); code != 0 {
		return code
	}
	env["BOBRES_BACKUP_NETWORK"] = backupNetwork(env)
	// The run that creates the backup key is the one that shows it.
	newBackupKey := env["BOBRES_BACKUP_KEY"] == "" || strings.Contains(env["BOBRES_BACKUP_KEY"], "CHANGE_ME")
	for k, n := range generated {
		if env[k] == "" || strings.Contains(env[k], "CHANGE_ME") {
			env[k] = randomHex(n)
		}
	}
	env["BOBRES_REDIS_URL"] = "redis://:" + env["REDIS_PASSWORD"] + "@redis:6379/0"

	if errs := validateEnv(env); len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintln(ins.errOut, "error:", e)
		}
		return 2
	}

	ins.prog.Finish() // the questions are over
	if ins.survive != nil {
		ins.survive()
	}
	if !ins.quiet && !*yes {
		fmt.Fprintln(ins.out, "The questions are done: from here the install runs by itself, and keeps going if your SSH connection drops.")
		if ins.logPath != "" {
			fmt.Fprintf(ins.out, "Follow it from a new session with  tail -f %s  (next time connect with  ssh -o ServerAliveInterval=30 ... ).\n", ins.logPath)
		} else {
			fmt.Fprintln(ins.out, "Next time connect with  ssh -o ServerAliveInterval=30 ...  to keep the connection alive.")
		}
	}
	if *skipChecks && *offline {
		ins.prog.Skip(stepChecks, "--skip-checks and --offline")
	} else {
		ins.prog.Begin(stepChecks)
	}
	if !*skipChecks {
		if !ins.checks(env["BOBRES_DOMAIN"], existing) {
			fmt.Fprintln(ins.errOut, "System checks failed; fix the items above or re-run with --skip-checks.")
			return 1
		}
	}
	if !*offline {
		if err := ins.checkPlainPanel(ctx, env["BOBRES_XUI_URL"]); err != nil {
			fmt.Fprintln(ins.errOut, "error:", err)
			return 2
		}
		// The check goes where the bot goes: a local Bot API server when
		// BOBRES_TELEGRAM_API_URL names one (a server that reaches Telegram
		// only through it), else api.telegram.org.
		root := telegramAPIRoot(env)
		cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		name, err := ins.checkBot(cctx, env["BOBRES_TELEGRAM_BOT_TOKEN"], root)
		cancel()
		if err != nil {
			via := ""
			if root != "" {
				via = " (through the Bot API server " + root + ", BOBRES_TELEGRAM_API_URL)"
			}
			fmt.Fprintf(ins.errOut, "error: Telegram rejected the bot token or is unreachable%s: %v (use --offline to skip)\n", via, err)
			return 1
		}
		ins.botName = name
		fmt.Fprintf(ins.out, "Bot token OK: @%s\n", name)
	}

	if newBackupKey {
		// No later run shows the key, so the run that saves it does, however
		// it ends: after the summary, after a failed start, with --no-start,
		// and quietly from the menu too.
		defer ins.savedBackupKeyNotice(envPath, env["BOBRES_BACKUP_KEY"])
	}
	ins.prog.Begin(stepWrite)
	notes := ins.out
	if ins.quiet {
		notes = io.Discard
	}
	if err := writeFiles(*dir, env, notes, ins.errOut); err != nil {
		fmt.Fprintf(ins.errOut, "error: %v\n", err)
		return 1
	}
	fmt.Fprintf(ins.out, "Wrote %s (.env is readable by root only).\n", *dir)
	if *noStart {
		for _, k := range []string{stepPull, stepStart, stepHealthy, stepHarden} {
			ins.prog.Skip(k, "--no-start")
		}
		fmt.Fprintf(ins.out, "Not starting (--no-start). Start with: cd %s && docker compose up -d\n", *dir)
		return 0
	}
	if code := ins.start(ctx, *dir, env); code != 0 {
		return code
	}
	// The dashboard's updater, once the store it updates is up. Never a failed install.
	ins.ensureUpdater(ctx, *dir, env)
	// Hardening runs by itself where it is safe (--ask-harden asks step by
	// step instead); never from a run that cannot ask (--yes, as bobres update
	// runs it) or a settings change from the menu (quiet).
	if ins.harden != nil && !*noHarden && !*yes && !ins.quiet {
		if *askHarden {
			ins.prog.setKind(stepHarden, askStep)
		}
		ins.prog.Begin(stepHarden)
		if *askHarden {
			fmt.Fprintln(ins.out, "\nServer hardening (skip it with --no-harden; run it again any time with: sudo bobres harden)")
		}
		if ins.harden(ctx, hardenOptions{sshPort: *sshPort, dir: *dir, auto: !*askHarden}) != 0 {
			fmt.Fprintln(ins.errOut, "The store is installed; the hardening did not finish (see above). Run it again with: sudo bobres harden")
		}
	} else {
		why := "--no-harden"
		switch {
		case *yes:
			why = "--yes never asks"
		case ins.quiet:
			why = "a settings change"
		case ins.harden == nil:
			why = "not available here"
		}
		ins.prog.Skip(stepHarden, why)
	}
	ins.prog.Begin(stepSummary)
	if !ins.quiet {
		if !existing {
			ins.login = ins.issueOwnerLogin(ctx, *dir, env)
		}
		ins.summary(env, *dir)
	}
	return 0
}

// ownerLogin is the owner's one-time dashboard login (see core's
// admin-password): the password is shown once, in the summary.
type ownerLogin struct{ username, password string }

var ownerLoginRe = regexp.MustCompile(`(?m)^Username: (\S+)\r?\n^Password: (\S+)\r?$`)

// issueOwnerLogin asks core (up and healthy by now) for the owner's initial
// dashboard password, so a fresh install needs no Telegram step to sign in.
// It returns nil, and says why, when it cannot: the install is still fine and
// `bobres admin password` does the same later. The output is parsed here and
// printed by summary alone: it is never logged.
func (ins *installer) issueOwnerLogin(ctx context.Context, dir string, env map[string]string) *ownerLogin {
	if env["BOBRES_ADMIN_TELEGRAM_ID"] == "" {
		return nil
	}
	out, err := ins.run(ctx, dir, composeArgs("exec", "-T", "core", "/app", "admin-password")...)
	if err != nil {
		fmt.Fprintf(ins.errOut, "warning: the initial dashboard password was not created (%v): run  bobres admin password  once core is up.\n%s\n",
			err, strings.TrimSpace(string(out)))
		return nil
	}
	m := ownerLoginRe.FindStringSubmatch(string(out))
	if m == nil {
		fmt.Fprintln(ins.errOut, "warning: core did not return a dashboard password: run  bobres admin password  to create one.")
		return nil
	}
	return &ownerLogin{username: m[1], password: m[2]}
}

// askS3 completes the optional copy of each backup to S3-compatible storage.
// The install never offers it (it is off by default: the final tip says how to
// turn it on); but once flags or the environment name an endpoint, a missing
// bucket or key is asked (the secret key without echo), as the panel's token
// is. It returns an exit code (0: go on).
func (ins *installer) askS3(env map[string]string) int {
	if !backupEnabled(env) || env["BOBRES_BACKUP_S3_ENDPOINT"] == "" {
		return 0
	}
	if env["BOBRES_BACKUP_S3_BUCKET"] == "" {
		v, err := ins.ask("S3 bucket name: ", false)
		if err != nil {
			fmt.Fprintln(ins.errOut, "error: BOBRES_BACKUP_S3_BUCKET is required with the S3 endpoint (--s3-bucket, or an interactive terminal)")
			return 2
		}
		env["BOBRES_BACKUP_S3_BUCKET"] = strings.ToLower(v)
	}
	for _, q := range []struct {
		key, prompt string
		secret      bool
	}{
		{"BOBRES_BACKUP_S3_ACCESS_KEY", "S3 access key: ", false},
		{"BOBRES_BACKUP_S3_SECRET_KEY", "S3 secret key: ", true},
	} {
		if env[q.key] != "" {
			continue
		}
		v, err := ins.ask(q.prompt, q.secret)
		if err != nil {
			fmt.Fprintf(ins.errOut, "error: %s is required with the S3 endpoint (environment variable, or an interactive terminal)\n", q.key)
			return 2
		}
		env[q.key] = v
	}
	return 0
}

// backupNetwork is the backup service's second network: egress (the
// internet) while backups are copied to S3, else "" (compose then puts it on
// the internal "offline" network of its own: no internet).
func backupNetwork(env map[string]string) string {
	if strings.TrimSpace(env["BOBRES_BACKUP_S3_ENDPOINT"]) != "" {
		return "egress"
	}
	return ""
}

// askOptional reads an answer that may be left empty (Enter, or no terminal).
func (ins *installer) askOptional(prompt string) string {
	fmt.Fprint(ins.out, prompt)
	line, _ := ins.in.ReadString('\n')
	return strings.TrimSpace(line)
}

func (ins *installer) ask(prompt string, secret bool) (string, error) {
	if secret && ins.readHide != nil {
		v, err := ins.readHide(prompt)
		if err != nil || v == "" {
			return "", errors.New("no input")
		}
		return v, nil
	}
	fmt.Fprint(ins.out, prompt)
	line, err := ins.in.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		if err == nil {
			err = errors.New("empty")
		}
		return "", err
	}
	return line, nil
}

func (ins *installer) checks(domain string, existing bool) bool {
	ok := true
	for _, r := range ins.preflight(domain) {
		if ins.ui.fancy {
			fmt.Fprintf(ins.out, "  %s %-18s %s\n", ins.ui.mark(statusMark(r.Status)), r.Name, r.Detail)
		} else {
			fmt.Fprintf(ins.out, "%-5s %-18s %s\n", r.Status, r.Name, r.Detail)
		}
		if r.Status != Fail {
			continue
		}
		switch {
		case existing && strings.HasPrefix(r.Name, "Port "):
			// our own gateway holds the ports on a re-run
		case r.Name == "DNS":
			fmt.Fprintln(ins.out, "      (continuing: HTTPS certificates are issued once DNS points here)")
		default:
			ok = false
		}
	}
	return ok
}

// start pulls and starts the stack, then waits until every service is healthy.
func (ins *installer) start(ctx context.Context, dir string, env map[string]string) int {
	fmt.Fprintln(ins.out, "Pulling images and starting the stack (this can take a few minutes)...")
	ins.prog.Begin(stepPull)
	var up []byte
	var err error
	if ins.runLines != nil {
		up, err = ins.runLines(ctx, dir, ins.upProgress(), composeArgs("up", "-d")...)
	} else {
		up, err = ins.run(ctx, dir, composeArgs("up", "-d")...)
	}
	fmt.Fprintf(ins.logOut(), "%s\n", strings.TrimSpace(string(up)))
	if err != nil {
		fmt.Fprintf(ins.errOut, "error: docker compose up failed: %v\n%s\n", err, up)
		return 1
	}
	ins.prog.Begin(stepStart)
	ins.reloadCaddy(ctx, dir)
	if backupEnabled(env) {
		ins.ensureBackupRole(ctx, dir, env["DB_BACKUP_PASSWORD"])
	}
	if !tlsAgentImage(env["BOBRES_VERSION"]) {
		ins.removeTLSAgent(ctx, dir)
	}
	ins.prog.Begin(stepHealthy)
	return waitHealthyProgress(ctx, ins.run, ins.sleep, dir, appServices(dir, env), ins.out, ins.errOut,
		func(healthy, total int) {
			ins.prog.Fraction(healthy, total, fmt.Sprintf("%d/%d healthy", healthy, total))
		})
}

// removeTLSAgent removes the tls-agent container an earlier run of a newer
// release left: with its profile off compose neither starts, updates nor
// stops it, and it would go on running a newer image than the rest of the
// stack (a downgrade). Best effort: the install does not depend on it.
func (ins *installer) removeTLSAgent(ctx context.Context, dir string) {
	if !composeDefines(dir, tlsAgentService) {
		return
	}
	if out, err := ins.run(ctx, dir, composeArgs("rm", "--stop", "--force", tlsAgentService)...); err != nil {
		fmt.Fprintf(ins.errOut, "warning: an old %s container was not removed (%v): docker rm -f bobres-%s-1\n%s\n",
			tlsAgentService, err, tlsAgentService, strings.TrimSpace(string(out)))
	}
}

// caddyWait is how long start waits for the gateway to run steadily.
const caddyWait = 30 * time.Second

// reloadCaddy makes Caddy read its Caddyfile again (SIGUSR1: it works with or
// without the admin socket, and the tls-agent's loads keep the file name it
// needs), then waits up to caddyWait for the gateway to be running and to stay
// so for two looks in a row (a crash-looping container shows "running" now and
// then). The Caddyfile is a directory mount, so an updated one is there, but a
// running Caddy reads it only when told to. Both steps are best effort: a
// failure is a warning, never a failed install or update.
func (ins *installer) reloadCaddy(ctx context.Context, dir string) {
	if out, err := ins.run(ctx, dir, composeArgs("kill", "-s", "SIGUSR1", "caddy")...); err != nil {
		fmt.Fprintf(ins.errOut, "warning: Caddy was not told to read its Caddyfile again (%v): it reads it when it starts (bobres restart).\n%s\n",
			err, strings.TrimSpace(string(out)))
	}
	state, steady := "", 0
	for waited := time.Duration(0); ; waited += 2 * time.Second {
		out, err := ins.run(ctx, dir, composeArgs("ps", "--all", "--format", "json", "caddy")...)
		state = ""
		if err == nil {
			state = parseServices(out)["caddy"].State
		}
		if state == "running" {
			if steady++; steady >= 2 {
				return
			}
		} else {
			steady = 0
		}
		if waited >= caddyWait {
			break
		}
		ins.sleep(2 * time.Second)
	}
	if state == "" {
		state = "no container"
	}
	fmt.Fprintf(ins.errOut, "warning: Caddy, the gateway, is not running steadily (state: %s). HTTPS will not work until it does: bobres logs caddy\n", state)
}

// backupRoleScript creates or updates the backup service's read-only role in
// a running database, for installs whose database predates it (postgres-init.sh
// runs only on a new database). It runs in the postgres container, whose
// environment has DB_BACKUP_PASSWORD; psql reads it with \getenv, so the
// password is on no command line.
const backupRoleScript = `test -n "$DB_BACKUP_PASSWORD" || { echo "DB_BACKUP_PASSWORD is not set in the postgres container" >&2; exit 3; }
psql -v ON_ERROR_STOP=1 -q --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<'SQL'
\getenv pw DB_BACKUP_PASSWORD
\getenv db POSTGRES_DB
SELECT 'CREATE ROLE svc_backup LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE'
  WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'svc_backup') \gexec
ALTER ROLE svc_backup WITH LOGIN PASSWORD :'pw';
GRANT pg_read_all_data TO svc_backup;
GRANT CONNECT ON DATABASE :"db" TO svc_backup;
SQL`

// ensureBackupRole runs backupRoleScript. A failure is a warning: the store
// runs without backups, and the next install (or the manual command in
// docs/06-installer.md) creates the role.
func (ins *installer) ensureBackupRole(ctx context.Context, dir, password string) {
	out, err := ins.run(ctx, dir, composeArgs("exec", "-T", "postgres", "sh", "-c", backupRoleScript)...)
	if err == nil {
		return
	}
	msg := strings.TrimSpace(string(out))
	if password != "" {
		msg = strings.ReplaceAll(msg, password, "••••")
	}
	fmt.Fprintf(ins.errOut, "warning: the backup database role was not created (%v): backups fail until it exists.\n%s\n"+
		"Re-run `bobres install` once the database is running, or see docs/06-installer.md (Backups).\n", err, msg)
}

// firstBackupRelease is the first release whose images include
// bobres-backup (ghcr.io/sobhanaz/bobres-backup). An install pinned to an
// older one has no backup image to pull.
const firstBackupRelease = "0.1.0"

// backupImage reports whether the images tagged v have the backup service:
// "latest", this CLI's own release, any release from firstBackupRelease on
// (its pre-releases too), or anything for a development build of the CLI
// (which runs images built from its source).
func backupImage(v string) bool {
	if v == "latest" || v == imageTag(version.Version) || version.Version == "dev" {
		return true
	}
	rel, _, _ := strings.Cut(strings.TrimPrefix(v, "v"), "-")
	return release.Valid(rel) && release.Compare(rel, firstBackupRelease) >= 0
}

// enableBackups turns the backup Compose profile on, with core's address for
// it.
func enableBackups(env map[string]string) {
	addProfile(env, backupProfile)
	if env["BOBRES_BACKUP_URL"] == "" {
		env["BOBRES_BACKUP_URL"] = "http://backup:8080"
	}
}

// disableBackups turns the backup Compose profile off (keeping any other
// profile) and clears core's address for it, for images without the service.
func disableBackups(env map[string]string) {
	dropProfile(env, backupProfile)
	env["BOBRES_BACKUP_URL"] = ""
}

// backupEnabled reports whether the install runs the backup service.
func backupEnabled(env map[string]string) bool { return hasProfile(env, backupProfile) }

// The Compose profiles the installer turns on and off with the version.
const (
	backupProfile = "backup"
	tlsProfile    = "tls"
)

// addProfile turns a Compose profile on in env's COMPOSE_PROFILES, after the
// ones that are there.
func addProfile(env map[string]string, name string) {
	if hasProfile(env, name) {
		return
	}
	profiles := strings.Trim(env["COMPOSE_PROFILES"], ", ")
	if profiles != "" {
		profiles += ","
	}
	env["COMPOSE_PROFILES"] = profiles + name
}

// dropProfile turns a Compose profile off, keeping the others.
func dropProfile(env map[string]string, name string) {
	var keep []string
	for _, p := range strings.Split(env["COMPOSE_PROFILES"], ",") {
		if p = strings.TrimSpace(p); p != "" && p != name {
			keep = append(keep, p)
		}
	}
	env["COMPOSE_PROFILES"] = strings.Join(keep, ",")
}

// hasProfile reports whether env's COMPOSE_PROFILES names the profile.
func hasProfile(env map[string]string, name string) bool {
	for _, p := range strings.Split(env["COMPOSE_PROFILES"], ",") {
		if strings.TrimSpace(p) == name {
			return true
		}
	}
	return false
}

// savedBackupKeyNotice shows the backup key, on the install that creates
// it, once it is saved in the .env at envPath (a run that wrote nothing has
// not created it: the next one makes another).
func (ins *installer) savedBackupKeyNotice(envPath, key string) {
	if saved, _, err := readEnv(envPath); err != nil || saved["BOBRES_BACKUP_KEY"] != key {
		return
	}
	fmt.Fprintf(ins.out, `
Backup key (shown only now; bobres menu > "Backup key" shows it again):
  %s
Store this somewhere safe, away from this server: without it the backups cannot be read.
`, key)
}

func orUnknown(s string) string {
	if s == "" {
		return "starting"
	}
	return s
}

// parseHealth reads `docker compose ps --format json` (a JSON array, or one
// object per line depending on the Compose version) into service -> health.
func parseHealth(out []byte) map[string]string {
	type row struct {
		Service string `json:"Service"`
		Health  string `json:"Health"`
		State   string `json:"State"`
	}
	var rows []row
	trimmed := bytes.TrimSpace(out)
	if bytes.HasPrefix(trimmed, []byte("[")) {
		_ = json.Unmarshal(trimmed, &rows)
	} else {
		for _, line := range bytes.Split(trimmed, []byte("\n")) {
			var r row
			if json.Unmarshal(line, &r) == nil {
				rows = append(rows, r)
			}
		}
	}
	m := map[string]string{}
	for _, r := range rows {
		h := r.Health
		if h == "" {
			h = r.State
		}
		m[r.Service] = h
	}
	return m
}

// httpsSummary is the summary's paragraph about the certificate: the commands
// of the HTTPS manager where this install has it, else where to get it.
func (ins *installer) httpsSummary(env map[string]string, dir string) string {
	if !tlsAgentOn(dir, env) {
		return "The HTTPS certificate is automatic. The images this install runs (" + orDash(env["BOBRES_VERSION"]) +
			") predate the HTTPS manager (sudo bobres ssl, System > HTTPS in the dashboard): sudo bobres update brings it."
	}
	return "The HTTPS certificate is automatic (see it: sudo bobres ssl status). For the server's IP address or your own\n" +
		"    certificate instead: sudo bobres ssl ip | manual (or the dashboard, System > HTTPS)."
}

var merchantRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// panelValues are the .env values of the first 3x-ui panel, which the
// provisioner reads only to add a server while it has none.
func panelValues(env map[string]string) [4]string {
	return [4]string{env["BOBRES_XUI_URL"], env["BOBRES_XUI_TOKEN"], env["BOBRES_XUI_SUB_URL"], env["BOBRES_XUI_ALLOW_PRIVATE"]}
}

// loopbackHost: localhost or a loopback IP literal.
func loopbackHost(h string) bool {
	if strings.EqualFold(h, "localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// checkPlainPanel refuses a plain-http panel whose name resolves to a public
// address, which the provisioner would refuse on every call. host.docker.internal
// only resolves inside the containers, so it is not looked up here.
func (ins *installer) checkPlainPanel(ctx context.Context, panelURL string) error {
	u, err := url.Parse(panelURL)
	if err != nil || u.Scheme != "http" || strings.EqualFold(u.Hostname(), "host.docker.internal") {
		return nil
	}
	ips := []net.IP{net.ParseIP(u.Hostname())}
	if ips[0] == nil {
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if ips, err = ins.lookupIP(cctx, u.Hostname()); err != nil {
			return fmt.Errorf("cannot resolve the 3x-ui host %s (use --offline to skip): %w", u.Hostname(), err)
		}
	}
	for _, ip := range ips {
		if !ip.IsLoopback() && !ip.IsPrivate() && !ip.IsLinkLocalUnicast() {
			return fmt.Errorf("the 3x-ui URL is plain http but %s resolves to the public address %s: use the panel's https URL "+
				"(plain http is only for a panel on this server or a private network)", u.Hostname(), ip)
		}
	}
	return nil
}

func validateEnv(env map[string]string) []error {
	var errs []error
	if !domainRe.MatchString(env["BOBRES_DOMAIN"]) {
		errs = append(errs, fmt.Errorf("domain %q is not a valid host name", env["BOBRES_DOMAIN"]))
	}
	if err := checkAdminIDs(env["BOBRES_ADMIN_TELEGRAM_ID"]); err != nil {
		errs = append(errs, fmt.Errorf("admin Telegram id: %w", err))
	}
	if !botTokenRe.MatchString(env["BOBRES_TELEGRAM_BOT_TOKEN"]) {
		errs = append(errs, errors.New("the bot token does not look like a @BotFather token (123456:ABC...)"))
	}
	// The first panel is optional; when set, URL and token go together.
	privatePanel := env["BOBRES_XUI_ALLOW_PRIVATE"] == "true"
	if panel := env["BOBRES_XUI_URL"]; panel == "" {
		if env["BOBRES_XUI_TOKEN"] != "" {
			errs = append(errs, errors.New("a 3x-ui API token needs the panel URL (--xui-url)"))
		}
	} else if u, err := url.Parse(panel); err != nil || u.Host == "" || u.User != nil ||
		(u.Scheme != "https" && (u.Scheme != "http" || !privatePanel)) {
		errs = append(errs, errors.New("the 3x-ui URL must be https://host[:port][/path] without credentials (http:// only with --xui-allow-private)"))
	} else if loopbackHost(u.Hostname()) {
		errs = append(errs, fmt.Errorf("the 3x-ui URL points to %s, which inside the BOBRES containers is the container itself: "+
			"for a panel on this server use http://host.docker.internal:<port>/<path> with --xui-allow-private", u.Hostname()))
	} else if env["BOBRES_XUI_TOKEN"] == "" {
		errs = append(errs, errors.New("the 3x-ui API token is required with the panel URL"))
	}
	if sub := env["BOBRES_XUI_SUB_URL"]; sub != "" {
		if u, err := url.Parse(sub); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			errs = append(errs, errors.New("the subscription URL must be an http(s) URL"))
		}
	}
	if m := env["BOBRES_ZARINPAL_MERCHANT_ID"]; m != "" && !merchantRe.MatchString(m) {
		errs = append(errs, errors.New("the Zarinpal merchant id must be the 36-character id from the Zarinpal panel"))
	}
	if pu := env["BOBRES_ZARINPAL_PUBLIC_URL"]; pu != "" {
		if u, err := url.Parse(pu); err != nil || u.Scheme != "https" || u.Host == "" || (u.Path != "" && u.Path != "/") {
			errs = append(errs, errors.New("the Zarinpal public URL must be https://host of the domain registered with Zarinpal"))
		}
	}
	if px := env["BOBRES_ZARINPAL_PROXY"]; px != "" {
		if u, err := url.Parse(px); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			errs = append(errs, errors.New("the Zarinpal proxy must be an http(s) URL"))
		}
	}
	if _, err := config.ParseBackupS3(env); err != nil {
		errs = append(errs, splitErrors(err)...)
	}
	for k, v := range env {
		if strings.ContainsAny(v, "'\n\r") {
			errs = append(errs, fmt.Errorf("%s contains a quote or a line break", k))
		}
	}
	return errs
}

// splitErrors lists the errors errors.Join put together, one per line.
func splitErrors(err error) []error {
	if j, ok := err.(interface{ Unwrap() []error }); ok { //nolint:errorlint // only a joined error is split
		return j.Unwrap()
	}
	return []error{err}
}

// readEnv parses KEY=VALUE lines (quotes stripped). A missing file is not an error.
func readEnv(path string) (map[string]string, bool, error) {
	b, err := os.ReadFile(path) //nolint:gosec // operator-chosen install dir
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return parseEnv(b), true, nil
}

// envLine is one KEY=VALUE line of an env file: its value, and the comment
// at the end of the line ("# ...", or "").
type envLine struct{ key, value, comment string }

// envLines reads the KEY=VALUE lines of an env file; blank lines and comment
// lines are skipped.
func envLines(b []byte) []envLine {
	var out []envLine
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		v, c := splitEnvValue(v)
		out = append(out, envLine{key: strings.TrimSpace(k), value: v, comment: c})
	}
	return out
}

// splitEnvValue splits what follows a key's "=" into the value and the
// comment after it, by Compose's rules: a quoted value ends at its closing
// quote (one after a backslash does not count), so a # inside it is kept,
// and a # after it starts the comment; in an unquoted value, " #" starts
// the comment (a # with no space before it is part of the value). A quoted
// value with anything but a comment after it is read as it always was: the
// outer quotes stripped when both are there.
func splitEnvValue(raw string) (value, comment string) {
	v := strings.TrimSpace(raw)
	if v == "" || (v[0] != '\'' && v[0] != '"') {
		if i := strings.Index(v, " #"); i >= 0 {
			return strings.TrimSpace(v[:i]), strings.TrimSpace(v[i:])
		}
		return v, ""
	}
	q := v[0]
	for i := 1; i < len(v); i++ {
		if v[i] != q || v[i-1] == '\\' {
			continue
		}
		if rest := strings.TrimSpace(v[i+1:]); rest == "" || rest[0] == '#' {
			return v[1:i], rest
		}
		break
	}
	if len(v) >= 2 && v[len(v)-1] == q {
		return v[1 : len(v)-1], ""
	}
	return v, ""
}

// parseEnv reads the values of an env file (quotes and comments stripped).
func parseEnv(b []byte) map[string]string {
	env := map[string]string{}
	for _, l := range envLines(b) {
		env[l.key] = l.value
	}
	return env
}

// envComments maps each key of an env file to the comment at the end of its
// line, for the keys that have one.
func envComments(b []byte) map[string]string {
	comments := map[string]string{}
	for _, l := range envLines(b) {
		if l.comment != "" {
			comments[l.key] = l.comment
		} else {
			delete(comments, l.key) // the last line of a key is the one that counts
		}
	}
	return comments
}

// writeEnv writes env to path (root only, atomically), keeping the comments
// at the end of the lines of the file it replaces.
func writeEnv(path string, env map[string]string) error {
	return writeAtomic(path, renderEnvFor(path, env), 0o600)
}

// renderEnvFor renders env with the line-end comments of the env file at
// path (none when it cannot be read).
func renderEnvFor(path string, env map[string]string) []byte {
	prev, _ := os.ReadFile(path) //nolint:gosec // the install's own .env
	return renderEnvComments(env, envComments(prev))
}

// renderEnv writes the env file; values with characters Compose would
// interpret ($, spaces, #...) are single-quoted (no interpolation inside).
func renderEnv(env map[string]string) []byte { return renderEnvComments(env, nil) }

// renderEnvComments is renderEnv with a comment at the end of a key's line
// (comments: key -> "# ..."), as the operator wrote it there.
func renderEnvComments(env, comments map[string]string) []byte {
	var b strings.Builder
	b.WriteString("# BOBRES environment, generated by `bobres install`. Keep it secret and backed up.\n")
	b.WriteString("# Re-running the installer keeps these secrets. See .env.example for documentation.\n")
	seen := map[string]bool{}
	write := func(k string) {
		v, c := env[k], comments[k]
		// An empty value before a comment is quoted too: Compose would read
		// "KEY= # note" as the value "# note".
		if !safeEnvRe.MatchString(v) || (v == "" && c != "") {
			v = "'" + v + "'"
		}
		if c != "" {
			v += " " + c
		}
		fmt.Fprintf(&b, "%s=%s\n", k, v)
		seen[k] = true
	}
	for _, k := range envOrder {
		write(k)
	}
	var extra []string
	for k := range env {
		if !seen[k] {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	for _, k := range extra { // keep operator-added settings
		write(k)
	}
	return []byte(b.String())
}

// installedCompose drops the source-build sections: servers run the released
// images and must never try to build from a checkout they do not have.
func installedCompose(src []byte) []byte {
	var out []string
	skip := false
	for _, line := range strings.Split(string(src), "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "x-build:"):
			skip = true
			continue
		case skip && (strings.HasPrefix(line, " ") || trimmed == ""):
			if trimmed == "" {
				skip = false
				out = append(out, line)
			}
			continue
		case strings.HasPrefix(trimmed, "build:"):
			continue
		}
		skip = false
		out = append(out, line)
	}
	return []byte(strings.Join(out, "\n"))
}

// writeFiles writes the install's files. Caddy's main Caddyfile is caddy/Caddyfile
// (compose mounts the directory); the top-level Caddyfile an install from before
// that change has is left as it is, neither rewritten nor deleted, because the
// compose file of that version (a rollback brings it back) mounts it. tls/ gets
// its default snippets when it has none (prepareTLS); notes receives what the
// operator should hear about it, warnings what a quiet run must not leave out.
func writeFiles(dir string, env map[string]string, notes, warnings io.Writer) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	if err := prepareCaddy(dir); err != nil {
		return err
	}
	files := []struct {
		name string
		data []byte
		mode os.FileMode
	}{
		{".env", renderEnvFor(filepath.Join(dir, ".env"), env), 0o600},
		{".env.example", deploy.EnvExample, 0o644},
		{"docker-compose.yml", installedCompose(deploy.ComposeFile), 0o644},
		{filepath.Join(caddyDirName, "Caddyfile"), deploy.Caddyfile, 0o644},
		{"postgres-init.sh", deploy.PostgresInit, 0o755},
	}
	for _, f := range files {
		if err := writeAtomic(filepath.Join(dir, f.name), f.data, f.mode); err != nil {
			return err
		}
	}
	if err := prepareTLS(dir, env, notes, warnings); err != nil {
		return err
	}
	if err := prepareUpdateDir(dir, notes); err != nil {
		return err
	}
	// The backup service (uid 65532 in its image) writes here; created now so
	// Docker does not create it as root.
	backups := filepath.Join(dir, "backups")
	if err := os.MkdirAll(backups, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", backups, err)
	}
	if asRoot.euid() == 0 {
		if err := asRoot.chownPath(backups, backupUID, backupUID); err != nil {
			return fmt.Errorf("chown %s: %w", backups, err)
		}
	}
	return nil
}

// backupUID owns the backups directory: the backup image's user.
const backupUID = 65532

// writeAtomic writes to a temp file in the same directory, syncs it, renames
// it, then syncs the directory: after a power cut the file is either the old
// one or the new one, never empty (.env holds the backup key).
func writeAtomic(path string, data []byte, mode os.FileMode) error {
	return writeAtomicAs(path, data, mode, -1)
}

// writeAtomicAs is writeAtomic giving the file to uid (-1: kept) by its open
// descriptor, before the rename: never by path, in a directory another user
// controls, where the path could be swapped for a link.
func writeAtomicAs(path string, data []byte, mode os.FileMode, uid int) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	defer os.Remove(tmp.Name()) //nolint:errcheck // gone after a successful rename
	fail := func(err error) error {
		_ = tmp.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := tmp.Chmod(mode); err != nil {
		return fail(err)
	}
	if uid >= 0 {
		if err := tmp.Chown(uid, uid); err != nil {
			return fail(err)
		}
	}
	if _, err := tmp.Write(data); err != nil {
		return fail(err)
	}
	if err := tmp.Sync(); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := syncDir(filepath.Dir(path)); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// syncDir flushes a directory's entries (a rename into it) to disk. A file
// system that cannot sync a directory is not an error.
func syncDir(dir string) error {
	d, err := os.Open(dir) //nolint:gosec // a directory this CLI writes in
	if err != nil {
		return err
	}
	defer d.Close() //nolint:errcheck // read-only
	if err := d.Sync(); err != nil && !errors.Is(err, syscall.EINVAL) && !errors.Is(err, errors.ErrUnsupported) {
		return fmt.Errorf("sync %s: %w", dir, err)
	}
	return nil
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// imageTag maps the CLI version (v1.2.3) to the image tag (1.2.3).
func imageTag(v string) string {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if v == "" || v == "dev" || !regexp.MustCompile(`^\d+\.\d+\.\d+`).MatchString(v) {
		return "latest"
	}
	return v
}
