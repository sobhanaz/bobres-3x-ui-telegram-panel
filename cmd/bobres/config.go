package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"maps"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"time"
	_ "time/tzdata" // BOBRES_TIMEZONE is checked as the bot and backup images read it

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/config"
)

const configUsage = "usage: bobres config list | get <key> | set <key> <value>  [--dir D] [--no-restart]"

// configKey is a .env setting `bobres config` may change: one whose new value
// needs no other change (no secret, no database, no other key).
type configKey struct {
	name string
	// services are the containers that read it: recreated after a change.
	services []string
	// optional values may be set to empty (the default then applies).
	optional bool
	// lower: the value is kept in lower case.
	lower bool
	help  string
	check func(v string) error
	// apply, when set, puts the new value into effect on the host instead of recreating
	// containers (it gets the install's new .env); its result is the command's exit code.
	apply func(o *ops, dir string, env map[string]string) int
}

// logServices are the services that read the log settings.
var logServices = []string{"core", "payments", "provisioner", "bot", "backup"}

var configKeys = []configKey{
	// Core and the tls-agent (which writes Caddy's HTTPS files) both read the domain and the Zarinpal host
	// from the environment: core hands them to the agent in its request, and the agent refuses a request
	// for any other name. Both are recreated with Caddy, or the agent would turn the next request down.
	// installedOnly leaves the tls-agent out of an install that has none (tlsAgentOn).
	{name: "BOBRES_DOMAIN", services: []string{"caddy", "core", "payments", "tls-agent"}, lower: true,
		help: "the store's domain; DNS must point here (Caddy gets a certificate for it)"},
	{name: "BOBRES_TIMEZONE", services: []string{"core", "bot", "backup"}, help: "time zone of the bot's dates, the sales reports' days and the backup time, e.g. Asia/Tehran",
		check: func(v string) error {
			if _, err := time.LoadLocation(v); err != nil || v == "Local" {
				return fmt.Errorf("unknown time zone %q (e.g. Asia/Tehran, Europe/Berlin, UTC)", v)
			}
			return nil
		}},
	{name: "BOBRES_ADMIN_TELEGRAM_ID", services: []string{"core", "bot"}, check: checkAdminIDs,
		help: "the owners' numeric Telegram ids, separated by commas, the main owner first (core makes each of them an owner when it starts; bobres admin owners adds and removes one)"},
	{name: "BOBRES_LOG_LEVEL", services: logServices, lower: true, help: "debug, info, warn or error",
		check: oneOf("debug", "info", "warn", "error")},
	{name: "BOBRES_LOG_FORMAT", services: logServices, lower: true, help: "json or text", check: oneOf("json", "text")},
	{name: "BOBRES_BACKUP_TIME", services: []string{"backup"}, help: "the daily backup time, HH:MM in BOBRES_TIMEZONE",
		check: func(v string) error { _, _, err := config.ParseClock(v); return err }},
	{name: "BOBRES_TELEGRAM_API_URL", services: []string{"bot"}, optional: true,
		help: "a local Bot API server, e.g. http://host.docker.internal:8081 (empty: api.telegram.org)",
		check: func(v string) error {
			if u, err := url.Parse(v); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
				return errors.New("BOBRES_TELEGRAM_API_URL must be an http(s) URL without credentials")
			}
			return nil
		}},
	{name: "BOBRES_ZARINPAL_PUBLIC_URL", services: []string{"caddy", "core", "payments", "tls-agent"}, optional: true,
		help: "https://host registered with Zarinpal for the pay page (empty: https://<domain>)"},
	{name: "BOBRES_UPDATE_CHANNEL", services: []string{"bot"}, lower: true,
		help: "stable or beta: which releases the update check and bobres update offer", check: oneOf("stable", "beta")},
	{name: dashboardUpdatesKey, lower: true, check: oneOf("on", "off"), apply: applyDashboardUpdates,
		help: "on or off: whether the dashboard (System > Updates) may update this server through the bobres-updater helper; off removes the helper"},
	{name: bootKey, lower: true, check: oneOf("on", "off"), apply: applyStartAtBoot,
		help: "on or off: whether the unit bobres.service starts the whole store by itself after every boot, whatever Docker's restart policy did; off removes it"},
	{name: monitorCollectorKey, lower: true, check: oneOf("on", "off"), apply: applyMonitorCollector,
		help: "on or off: whether the host collector (bobres-monitor.timer) takes a snapshot of this server every 30 seconds for the dashboard's Monitoring section; off removes it"},
	{name: "BOBRES_LICENSE_SERVER", services: []string{"bot"}, optional: true,
		help: "the vendor's license server, https://... (empty: no online activation or refresh)",
		check: func(v string) error {
			if u, err := url.Parse(v); err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
				return errors.New("BOBRES_LICENSE_SERVER must be an https URL without credentials")
			}
			return nil
		}},
	{name: "BOBRES_SELLER_CONTACT", services: []string{"core"}, optional: true,
		help: "whom the License tab and `bobres license status` tell you to ask to buy or renew a license (a Telegram handle, an address; empty: \"ask the seller you got BOBRES from\")"},
	// The S3 upload's plain settings; its keys are secrets (bobres install asks
	// for them). install's validateEnv checks them together.
	{name: "BOBRES_BACKUP_S3_ENDPOINT", services: []string{"backup"}, optional: true,
		help: "copy each backup to this S3-compatible storage, https://host (empty: no copy); BOBRES_BACKUP_NETWORK follows it"},
	{name: "BOBRES_BACKUP_S3_BUCKET", services: []string{"backup"}, optional: true, lower: true,
		help: "the bucket the backups are copied to"},
	{name: "BOBRES_BACKUP_S3_REGION", services: []string{"backup"}, optional: true,
		help: "the bucket's region (empty: us-east-1)"},
	{name: "BOBRES_BACKUP_S3_PREFIX", services: []string{"backup"}, optional: true,
		help: "a folder in the bucket for the backups (empty: none)"},
	{name: "BOBRES_BACKUP_S3_KEEP", services: []string{"backup"}, optional: true,
		help: fmt.Sprintf("how many backups the bucket keeps (empty: %d)", config.DefaultS3Keep)},
}

// derivedKeys follow another setting: shown, never set.
var derivedKeys = map[string]string{
	"BOBRES_ZARINPAL_HOST":  "BOBRES_ZARINPAL_PUBLIC_URL",
	"BOBRES_BACKUP_NETWORK": "BOBRES_BACKUP_S3_ENDPOINT",
}

// checkAdminIDs checks BOBRES_ADMIN_TELEGRAM_ID: one Telegram id or several
// separated by commas, at least one.
func checkAdminIDs(v string) error {
	ids, err := config.ParseTelegramIDs(v)
	if err != nil {
		return fmt.Errorf("the owners are positive numbers separated by commas (ask @userinfobot): %w", err)
	}
	if len(ids) == 0 {
		return errors.New("name at least one owner")
	}
	return nil
}

func oneOf(allowed ...string) func(string) error {
	return func(v string) error {
		for _, a := range allowed {
			if v == a {
				return nil
			}
		}
		return fmt.Errorf("must be one of %s", strings.Join(allowed, ", "))
	}
}

func settable(name string) (configKey, bool) {
	for _, k := range configKeys {
		if k.name == name {
			return k, true
		}
	}
	return configKey{}, false
}

// zarinpalHost is the extra domain Caddy serves for Zarinpal: the public
// URL's host when it is not the main domain, else "".
func zarinpalHost(env map[string]string) string {
	if u, err := url.Parse(env["BOBRES_ZARINPAL_PUBLIC_URL"]); err == nil && u.Hostname() != "" && !strings.EqualFold(u.Hostname(), env["BOBRES_DOMAIN"]) {
		return u.Hostname()
	}
	return ""
}

// isSecretKey: every .env value is a secret but the plain settings the
// support bundle keeps.
func isSecretKey(name string) bool { return !notSecretEnv[name] }

// keyHint says how a key bobres config leaves alone is changed.
func keyHint(name string) string {
	for _, m := range config.BackupMasterKeys {
		if name == m {
			return "never rotated by bobres: see docs/runbooks/rotate-secrets.md"
		}
	}
	switch {
	case name == "BOBRES_TELEGRAM_BOT_TOKEN":
		return "bobres secrets rotate bot-token"
	case name == "POSTGRES_PASSWORD" || (strings.HasPrefix(name, "DB_") && strings.HasSuffix(name, "_PASSWORD")):
		return "bobres secrets rotate db-passwords"
	case strings.HasPrefix(name, "BOBRES_TOKEN_"):
		return "bobres secrets rotate service-tokens"
	case name == "REDIS_PASSWORD" || name == "BOBRES_REDIS_URL":
		return "bobres secrets rotate redis-password"
	case name == "BOBRES_BACKUP_KEY":
		return `bobres menu > "Backup key" shows it; docs/runbooks/rotate-secrets.md replaces it`
	case name == "BOBRES_BACKUP_S3_ACCESS_KEY" || name == "BOBRES_BACKUP_S3_SECRET_KEY":
		return "bobres install --s3-endpoint <url> asks for it while it is empty; to replace it, edit .env and run bobres install --skip-checks"
	case name == "BOBRES_VERSION" || strings.HasPrefix(name, "BOBRES_IMAGE_DIGEST_"):
		// The digests follow the version: the signed release CLI sets both.
		return "bobres update"
	case strings.HasPrefix(name, "BOBRES_XUI_"):
		return "servers are managed in the dashboard (Servers)"
	case strings.HasPrefix(name, "BOBRES_ZARINPAL_"), strings.HasPrefix(name, "BOBRES_IDPAY_"), strings.HasPrefix(name, "BOBRES_NEXTPAY_"),
		strings.HasPrefix(name, "BOBRES_AQAYEPARDAKHT_"):
		return "the dashboard (Payment gateways)"
	case name == "BOBRES_ENV" || name == "COMPOSE_PROFILES" || name == "BOBRES_BACKUP_URL":
		return "set by bobres install"
	}
	if src, ok := derivedKeys[name]; ok {
		return "follows " + src
	}
	return ""
}

// maskSecret shows that a secret is set, never its value; a bot token keeps
// its public bot id.
func maskSecret(v string) string {
	switch {
	case v == "":
		return "-"
	case botTokenRe.MatchString(v):
		id, _, _ := strings.Cut(v, ":")
		return id + ":••••"
	default:
		return "••••"
	}
}

// configCmd: `bobres config list | get <key> | set <key> <value>`. Exit 0
// when done, 1 when refused or failed, 2 for usage errors.
func (o *ops) configCmd(args []string) int {
	fs := flag.NewFlagSet("config", flag.ContinueOnError)
	fs.SetOutput(o.errOut)
	dir := fs.String("dir", defaultDir(), dirUsage)
	noRestart := fs.Bool("no-restart", false, "set: only write .env; the change applies when the store next starts")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return 2
	}
	switch {
	case len(pos) == 1 && pos[0] == "list":
		return o.configList(*dir)
	case len(pos) == 2 && pos[0] == "get":
		return o.configGet(*dir, pos[1])
	case len(pos) == 3 && pos[0] == "set":
		return o.configSet(*dir, pos[1], pos[2], *noRestart)
	}
	fmt.Fprintln(o.errOut, configUsage)
	return 2
}

// configList shows the settings, then every other .env key with its secret
// masked and how it is changed.
func (o *ops) configList(dir string) int {
	env, ok := o.requireInstall(dir)
	if !ok {
		return 2
	}
	fmt.Fprintln(o.out, "Settings (bobres config set <key> <value>):")
	shown := map[string]bool{}
	for _, k := range configKeys {
		fmt.Fprintf(o.out, "  %-30s %s\n", k.name, orDash(env[k.name]))
		shown[k.name] = true
	}
	for name, src := range derivedKeys {
		fmt.Fprintf(o.out, "  %-30s %s   (follows %s)\n", name, orDash(env[name]), src)
		shown[name] = true
	}
	var rest []string
	for name := range env {
		if !shown[name] {
			rest = append(rest, name)
		}
	}
	sort.Strings(rest)
	if len(rest) == 0 {
		return 0
	}
	fmt.Fprintln(o.out, "\nOther keys (secrets masked; not changed with bobres config):")
	for _, name := range rest {
		v := env[name]
		if isSecretKey(name) {
			v = maskSecret(v)
		}
		hint := ""
		if h := keyHint(name); h != "" {
			hint = "   (" + h + ")"
		}
		fmt.Fprintf(o.out, "  %-30s %s%s\n", name, orDash(v), hint)
	}
	return 0
}

// refuseKey says why bobres config does not show or change a key.
func (o *ops) refuseKey(name string, env map[string]string) {
	_, inEnv := env[name]
	hint := keyHint(name)
	switch {
	case isSecretKey(name) && (inEnv || hint != ""):
		fmt.Fprintf(o.errOut, "error: %s is a secret: bobres config never shows or changes it.\n", name)
	case inEnv || hint != "":
		fmt.Fprintf(o.errOut, "error: %s is not a setting bobres config changes.\n", name)
	default:
		fmt.Fprintf(o.errOut, "error: %s is not a setting bobres config knows.\n", name)
	}
	if hint != "" {
		fmt.Fprintf(o.errOut, "Instead: %s\n", hint)
	}
	fmt.Fprintln(o.errOut, "The settings: bobres config list")
}

// configGet prints one setting's value, alone on its line (for scripts).
func (o *ops) configGet(dir, name string) int {
	env, ok := o.requireInstall(dir)
	if !ok {
		return 2
	}
	name = strings.ToUpper(strings.TrimSpace(name))
	if _, ok := settable(name); !ok && derivedKeys[name] == "" {
		o.refuseKey(name, env)
		return 1
	}
	fmt.Fprintln(o.out, env[name])
	return 0
}

// newErrors are install's validation errors of next that env did not have:
// a setting is checked by the same rules as install, and an old problem
// elsewhere in .env does not block it.
func newErrors(env, next map[string]string) []error {
	old := map[string]bool{}
	for _, e := range validateEnv(env) {
		old[e.Error()] = true
	}
	var errs []error
	for _, e := range validateEnv(next) {
		if !old[e.Error()] {
			errs = append(errs, e)
		}
	}
	return errs
}

// configSet validates the new value as install does, writes .env (0600, in
// one rename) and recreates only the services that read the setting.
func (o *ops) configSet(dir, name, value string, noRestart bool) int {
	env, ok := o.requireInstall(dir)
	if !ok {
		return 2
	}
	name = strings.ToUpper(strings.TrimSpace(name))
	key, ok := settable(name)
	if !ok {
		if src, derived := derivedKeys[name]; derived {
			fmt.Fprintf(o.errOut, "error: %s follows %s: set that one instead.\n", name, src)
			return 1
		}
		o.refuseKey(name, env)
		return 1
	}
	value = strings.TrimSpace(value)
	if key.lower {
		value = strings.ToLower(value)
	}
	switch {
	case value == "" && !key.optional:
		fmt.Fprintf(o.errOut, "error: %s cannot be empty (%s). Nothing was changed.\n", name, key.help)
		return 1
	case value != "" && key.check != nil:
		if err := key.check(value); err != nil {
			fmt.Fprintf(o.errOut, "error: %s: %v. Nothing was changed.\n", name, err)
			return 1
		}
	}
	if env[name] == value {
		fmt.Fprintf(o.out, "%s is already %q: nothing to change.\n", name, value)
		return 0
	}
	next := maps.Clone(env)
	next[name] = value
	next["BOBRES_ZARINPAL_HOST"] = zarinpalHost(next)
	next["BOBRES_BACKUP_NETWORK"] = backupNetwork(next)
	if errs := newErrors(env, next); len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintln(o.errOut, "error:", e)
		}
		fmt.Fprintln(o.errOut, "Nothing was changed.")
		return 1
	}
	envPath := filepath.Join(dir, ".env")
	if err := writeEnv(envPath, next); err != nil {
		fmt.Fprintf(o.errOut, "error: %v. Nothing was changed.\n", err)
		return 1
	}
	fmt.Fprintf(o.out, "Saved %s=%s in %s.\n", name, value, envPath)
	if name == "BOBRES_DOMAIN" {
		fmt.Fprintf(o.out, "The domain %s must point to this server: Caddy gets its HTTPS certificate once it does (bobres doctor --domain %s).\n", value, value)
		fmt.Fprintln(o.out, "A certificate of your own must cover the new domain, or HTTPS falls back to the automatic one (bobres ssl status).")
	}

	if key.apply != nil {
		return key.apply(o, dir, next)
	}
	services := installedOnly(dir, key.services, next)
	switch {
	case len(services) == 0:
		fmt.Fprintln(o.out, "No service of this install reads it now (the backup service is off): nothing to restart.")
		return 0
	case noRestart:
		fmt.Fprintf(o.out, "Not restarted (--no-restart): it applies at the next start of %s (bobres start --dir %s).\n",
			strings.Join(services, ", "), dir)
		return 0
	}
	ctx := context.Background()
	running, err := o.storeRunning(ctx, dir)
	if err != nil {
		fmt.Fprintf(o.errOut, "error: %v\nThe setting is saved; it applies when the store starts (bobres start --dir %s).\n", err, dir)
		return 1
	}
	if !running {
		fmt.Fprintf(o.out, "BOBRES is not running: the change applies when it starts (bobres start --dir %s).\n", dir)
		return 0
	}
	if o.upAndWait(ctx, dir, services) != 0 {
		back := env[name]
		if back == "" {
			back = `""`
		}
		fmt.Fprintf(o.errOut, "The setting is saved, but %s are not healthy (bobres logs --dir %s %s).\n"+
			"To go back: bobres config set %s %s --dir %s\n", strings.Join(services, ", "), dir, services[0], name, back, dir)
		return 1
	}
	return 0
}

// installedOnly keeps the services this install runs (backup only where its
// profile is on, the tls-agent only where tlsAgentOn), in their order.
func installedOnly(dir string, services []string, env map[string]string) []string {
	have := map[string]bool{}
	for _, s := range allServices(dir, env) {
		have[s] = true
	}
	var out []string
	for _, s := range services {
		if have[s] {
			out = append(out, s)
		}
	}
	return out
}

// storeRunning reports whether the store is up: its database runs.
func (o *ops) storeRunning(ctx context.Context, dir string) (bool, error) {
	out, err := o.run(ctx, dir, composeArgs("ps", "--all", "--format", "json")...)
	if err != nil {
		return false, fmt.Errorf("docker compose ps failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return parseServices(out)["postgres"].State == "running", nil
}

// upAndWait makes services take a changed .env: compose up -d --no-deps
// recreates those whose settings changed, and nothing else; then it waits
// until they are healthy (Caddy has no health check: it only has to run).
func (o *ops) upAndWait(ctx context.Context, dir string, services []string) int {
	fmt.Fprintf(o.out, "Restarting %s with the values in .env...\n", strings.Join(services, ", "))
	if out, err := o.run(ctx, dir, composeArgs(append([]string{"up", "-d", "--no-deps"}, services...)...)...); err != nil {
		fmt.Fprintf(o.errOut, "error: docker compose up failed: %v\n%s\n", err, strings.TrimSpace(string(out)))
		return 1
	}
	var wait []string
	for _, s := range services {
		if s != "caddy" {
			wait = append(wait, s)
		}
	}
	if len(wait) == 0 {
		fmt.Fprintln(o.out, "Done.")
		return 0
	}
	return waitHealthy(ctx, o.run, o.sleep, dir, wait, o.out, o.errOut)
}
