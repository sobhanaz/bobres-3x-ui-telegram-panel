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
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/deploy"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
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
	checkBot  func(ctx context.Context, token string) (string, error)
	preflight func(domain string) []Result
	sleep     func(time.Duration)
}

func newInstaller(in io.Reader, out, errOut io.Writer) *installer {
	ins := &installer{
		in: bufio.NewReader(in), out: out, errOut: errOut, getenv: os.Getenv,
		run: func(ctx context.Context, dir string, args ...string) ([]byte, error) {
			cmd := exec.CommandContext(ctx, "docker", args...) //nolint:gosec // fixed program, arguments built here
			cmd.Dir = dir
			return cmd.CombinedOutput()
		},
		checkBot: func(ctx context.Context, token string) (string, error) {
			me, err := tg.New(token).GetMe(ctx)
			if err != nil {
				return "", err
			}
			return me.Username, nil
		},
		preflight: func(domain string) []Result { return runDoctor(domain, false) },
		sleep:     time.Sleep,
	}
	if f, ok := in.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		ins.readHide = func(prompt string) (string, error) {
			fmt.Fprint(out, prompt)
			b, err := term.ReadPassword(int(f.Fd()))
			fmt.Fprintln(out)
			return strings.TrimSpace(string(b)), err
		}
	}
	return ins
}

func installCmd(args []string, in io.Reader, out, errOut io.Writer) int {
	return newInstaller(in, out, errOut).install(args)
}

// envOrder is the order of keys in the generated .env.
var envOrder = []string{
	"BOBRES_ENV", "BOBRES_LOG_LEVEL", "BOBRES_LOG_FORMAT", "BOBRES_VERSION", "BOBRES_DOMAIN",
	"POSTGRES_PASSWORD", "DB_CORE_PASSWORD", "DB_PAYMENTS_PASSWORD", "DB_PROVISIONER_PASSWORD",
	"REDIS_PASSWORD", "BOBRES_REDIS_URL",
	"BOBRES_TOKEN_CORE", "BOBRES_TOKEN_BOT", "BOBRES_CORE_MASTER_KEY", "BOBRES_PROVISIONER_MASTER_KEY",
	"BOBRES_TELEGRAM_BOT_TOKEN", "BOBRES_ADMIN_TELEGRAM_ID", "BOBRES_TIMEZONE",
	"BOBRES_XUI_URL", "BOBRES_XUI_TOKEN", "BOBRES_XUI_SUB_URL", "BOBRES_XUI_ALLOW_PRIVATE",
}

// generated secrets: created once, then kept on every re-run (changing a
// database password would lock the services out of an existing database).
var generated = map[string]int{ // key -> random bytes (hex-encoded)
	"POSTGRES_PASSWORD": 24, "DB_CORE_PASSWORD": 24, "DB_PAYMENTS_PASSWORD": 24, "DB_PROVISIONER_PASSWORD": 24,
	"REDIS_PASSWORD": 24, "BOBRES_TOKEN_CORE": 32, "BOBRES_TOKEN_BOT": 32,
	"BOBRES_CORE_MASTER_KEY": 32, "BOBRES_PROVISIONER_MASTER_KEY": 32,
}

var (
	domainRe   = regexp.MustCompile(`(?i)^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`)
	botTokenRe = regexp.MustCompile(`^\d{5,15}:[A-Za-z0-9_-]{30,64}$`)
	safeEnvRe  = regexp.MustCompile(`^[A-Za-z0-9_\-:./@,+=]*$`)
)

func (ins *installer) install(args []string) int {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	fs.SetOutput(ins.errOut)
	dir := fs.String("dir", "/opt/bobres", "install directory")
	domain := fs.String("domain", "", "public domain of this install (DNS must point here)")
	adminID := fs.String("admin-id", "", "Telegram user id of the owner")
	xuiURL := fs.String("xui-url", "", "3x-ui panel URL (https), including any web base path")
	xuiSub := fs.String("xui-sub-url", "", "public prefix of the panel's subscription links, e.g. https://sub.example.com:2096/sub/")
	allowPrivate := fs.Bool("xui-allow-private", false, "the panel is on this host or a private network")
	imageVersion := fs.String("version", "", "image tag to run (default: this CLI's version)")
	noStart := fs.Bool("no-start", false, "write the files but do not start the stack")
	offline := fs.Bool("offline", false, "skip network checks (bot token)")
	skipChecks := fs.Bool("skip-checks", false, "skip the system checks")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	ctx := context.Background()

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
	set("BOBRES_DOMAIN", *domain)
	set("BOBRES_ADMIN_TELEGRAM_ID", *adminID)
	set("BOBRES_XUI_URL", *xuiURL)
	set("BOBRES_XUI_SUB_URL", *xuiSub)
	set("BOBRES_TELEGRAM_BOT_TOKEN", ins.getenv("BOBRES_TELEGRAM_BOT_TOKEN"))
	set("BOBRES_XUI_TOKEN", ins.getenv("BOBRES_XUI_TOKEN"))
	if *allowPrivate {
		env["BOBRES_XUI_ALLOW_PRIVATE"] = "true"
	}

	// Ask for what is still missing (secrets without echo).
	for _, q := range []struct{ key, prompt string }{
		{"BOBRES_DOMAIN", "Domain of this install (e.g. panel.example.com): "},
		{"BOBRES_ADMIN_TELEGRAM_ID", "Your numeric Telegram user id (the owner): "},
		{"BOBRES_TELEGRAM_BOT_TOKEN", "Telegram bot token from @BotFather: "},
		{"BOBRES_XUI_URL", "3x-ui panel URL (https://host:port/path): "},
		{"BOBRES_XUI_TOKEN", "3x-ui API token (panel Settings > Security): "},
	} {
		if env[q.key] != "" {
			continue
		}
		v, err := ins.ask(q.prompt, strings.HasSuffix(q.key, "_TOKEN"))
		if err != nil {
			fmt.Fprintf(ins.errOut, "error: %s is required (flag, environment variable, or an interactive terminal)\n", q.key)
			return 2
		}
		set(q.key, v)
	}

	defaults := map[string]string{
		"BOBRES_ENV": "prod", "BOBRES_LOG_LEVEL": "info", "BOBRES_LOG_FORMAT": "json",
		"BOBRES_TIMEZONE": "Asia/Tehran", "BOBRES_XUI_ALLOW_PRIVATE": "false",
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

	if !*skipChecks {
		if !ins.checks(env["BOBRES_DOMAIN"], existing) {
			fmt.Fprintln(ins.errOut, "System checks failed; fix the items above or re-run with --skip-checks.")
			return 1
		}
	}
	if !*offline {
		cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		name, err := ins.checkBot(cctx, env["BOBRES_TELEGRAM_BOT_TOKEN"])
		cancel()
		if err != nil {
			fmt.Fprintf(ins.errOut, "error: Telegram rejected the bot token or is unreachable: %v (use --offline to skip)\n", err)
			return 1
		}
		fmt.Fprintf(ins.out, "Bot token OK: @%s\n", name)
	}

	if err := writeFiles(*dir, env); err != nil {
		fmt.Fprintf(ins.errOut, "error: %v\n", err)
		return 1
	}
	fmt.Fprintf(ins.out, "Wrote %s (.env is readable by root only).\n", *dir)
	if *noStart {
		fmt.Fprintf(ins.out, "Not starting (--no-start). Start with: cd %s && docker compose up -d\n", *dir)
		return 0
	}
	if code := ins.start(ctx, *dir); code != 0 {
		return code
	}
	ins.summary(env, *dir)
	return 0
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
		fmt.Fprintf(ins.out, "%-5s %-18s %s\n", r.Status, r.Name, r.Detail)
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
func (ins *installer) start(ctx context.Context, dir string) int {
	compose := []string{"compose", "--project-name", "bobres", "-f", "docker-compose.yml", "--env-file", ".env"}
	fmt.Fprintln(ins.out, "Pulling images and starting the stack (this can take a few minutes)...")
	if out, err := ins.run(ctx, dir, append(compose, "up", "-d")...); err != nil {
		fmt.Fprintf(ins.errOut, "error: docker compose up failed: %v\n%s\n", err, out)
		return 1
	}
	want := []string{"core", "payments", "provisioner", "bot"}
	deadline := 5 * time.Minute
	for waited := time.Duration(0); ; waited += 5 * time.Second {
		out, err := ins.run(ctx, dir, append(compose, "ps", "--format", "json")...)
		health := map[string]string{}
		if err == nil {
			health = parseHealth(out)
		}
		var pending []string
		for _, s := range want {
			if health[s] != "healthy" {
				pending = append(pending, s+"="+orUnknown(health[s]))
			}
		}
		if len(pending) == 0 {
			fmt.Fprintln(ins.out, "All services are healthy.")
			return 0
		}
		if waited >= deadline {
			fmt.Fprintf(ins.errOut, "error: services not healthy after %s: %s\nInspect with: cd %s && docker compose logs\n",
				deadline, strings.Join(pending, ", "), dir)
			return 1
		}
		ins.sleep(5 * time.Second)
	}
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

func (ins *installer) summary(env map[string]string, dir string) {
	fmt.Fprintf(ins.out, `
BOBRES is running.

Next steps (in Telegram, from the owner account %s):
  1. Open your bot and send /start.
  2. Set your card:      /set payments.card_number 6037-...  and  /set payments.card_holder Your Name
  3. Add a plan:         /plan_add 150000 IRT 30 50 Monthly 50GB | ماهانه ۵۰ گیگ
  4. Offer a trial:      /trial 1 1
  5. Open the admin panel with /admin.

Your domain https://%s must point to this server for HTTPS. Keep %s/.env backed up:
it holds the keys that encrypt your panel token.
`, env["BOBRES_ADMIN_TELEGRAM_ID"], env["BOBRES_DOMAIN"], dir)
}

func validateEnv(env map[string]string) []error {
	var errs []error
	if !domainRe.MatchString(env["BOBRES_DOMAIN"]) {
		errs = append(errs, fmt.Errorf("domain %q is not a valid host name", env["BOBRES_DOMAIN"]))
	}
	if id, err := strconv.ParseInt(env["BOBRES_ADMIN_TELEGRAM_ID"], 10, 64); err != nil || id <= 0 {
		errs = append(errs, errors.New("admin Telegram id must be a positive number (ask @userinfobot)"))
	}
	if !botTokenRe.MatchString(env["BOBRES_TELEGRAM_BOT_TOKEN"]) {
		errs = append(errs, errors.New("the bot token does not look like a @BotFather token (123456:ABC...)"))
	}
	if u, err := url.Parse(env["BOBRES_XUI_URL"]); err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		errs = append(errs, errors.New("the 3x-ui URL must be https://host[:port][/path] without credentials"))
	}
	if sub := env["BOBRES_XUI_SUB_URL"]; sub != "" {
		if u, err := url.Parse(sub); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			errs = append(errs, errors.New("the subscription URL must be an http(s) URL"))
		}
	}
	if env["BOBRES_XUI_TOKEN"] == "" {
		errs = append(errs, errors.New("the 3x-ui API token is required"))
	}
	for k, v := range env {
		if strings.ContainsAny(v, "'\n\r") {
			errs = append(errs, fmt.Errorf("%s contains a quote or a line break", k))
		}
	}
	return errs
}

// readEnv parses KEY=VALUE lines (quotes stripped). A missing file is not an error.
func readEnv(path string) (map[string]string, bool, error) {
	env := map[string]string{}
	b, err := os.ReadFile(path) //nolint:gosec // operator-chosen install dir
	if errors.Is(err, os.ErrNotExist) {
		return env, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '\'' || v[0] == '"') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		env[strings.TrimSpace(k)] = v
	}
	return env, true, nil
}

// renderEnv writes the env file; values with characters Compose would
// interpret ($, spaces...) are single-quoted (no interpolation inside).
func renderEnv(env map[string]string) []byte {
	var b strings.Builder
	b.WriteString("# BOBRES environment, generated by `bobres install`. Keep it secret and backed up.\n")
	b.WriteString("# Re-running the installer keeps these secrets. See .env.example for documentation.\n")
	seen := map[string]bool{}
	write := func(k string) {
		v := env[k]
		if !safeEnvRe.MatchString(v) {
			v = "'" + v + "'"
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

func writeFiles(dir string, env map[string]string) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	files := []struct {
		name string
		data []byte
		mode os.FileMode
	}{
		{".env", renderEnv(env), 0o600},
		{".env.example", deploy.EnvExample, 0o644},
		{"docker-compose.yml", installedCompose(deploy.ComposeFile), 0o644},
		{"Caddyfile", deploy.Caddyfile, 0o644},
		{"postgres-init.sh", deploy.PostgresInit, 0o755},
	}
	for _, f := range files {
		if err := writeAtomic(filepath.Join(dir, f.name), f.data, f.mode); err != nil {
			return err
		}
	}
	return nil
}

// writeAtomic writes to a temp file in the same directory, then renames it.
func writeAtomic(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	defer os.Remove(tmp.Name()) //nolint:errcheck // gone after a successful rename
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
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
