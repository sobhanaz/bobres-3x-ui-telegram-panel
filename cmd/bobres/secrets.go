package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
)

const rotateUsage = `usage: bobres secrets rotate <name> [--dir D] [--yes]
  bot-token        the Telegram bot token (asked without echo, checked with Telegram; --offline skips that)
  db-passwords     the database passwords (--role core|payments|provisioner|backup|postgres; default all)
  service-tokens   the tokens the services present to each other (all four together)
  redis-password   the Redis password
The master keys have their own command, bobres rotate-key. The backup key is not rotated: see docs/runbooks/rotate-secrets.md`

// dbRole is a database password in .env and the role that has it.
type dbRole struct {
	name    string // --role
	env     string
	role    string // in ALTER ROLE
	service string // the container that logs in with it ("" for none)
}

var dbRoles = []dbRole{
	{"core", "DB_CORE_PASSWORD", "svc_core", "core"},
	{"payments", "DB_PAYMENTS_PASSWORD", "svc_payments", "payments"},
	{"provisioner", "DB_PROVISIONER_PASSWORD", "svc_provisioner", "provisioner"},
	{"backup", "DB_BACKUP_PASSWORD", "svc_backup", "backup"},
	// The superuser: the CLI uses the local socket, so nothing logs in with it.
	{"postgres", "POSTGRES_PASSWORD", "CURRENT_USER", ""},
}

// rotator holds what secrets rotate needs beyond Docker, replaceable in tests.
type rotator struct {
	*ops
	// readHide reads a secret without echo; nil when stdin is not a terminal.
	readHide func(prompt string) (string, error)
	checkBot func(ctx context.Context, token, apiRoot string) (string, error)
	random   func(n int) string
}

func newRotator(o *ops) *rotator {
	r := &rotator{ops: o, random: randomHex, checkBot: botUsername}
	if f, ok := o.in.(*os.File); ok {
		r.readHide = hiddenInput(f, o.out)
	}
	return r
}

// secretsCmd: `bobres secrets rotate <name>`. Every rotation writes the new
// values to .env, restarts only the services that use them and waits until
// they are healthy; when they are not, the previous values come back. Exit
// 0 when done, 1 when refused or failed, 2 for usage errors.
func (r *rotator) secretsCmd(args []string) int {
	if len(args) == 0 || args[0] != "rotate" {
		fmt.Fprintln(r.errOut, rotateUsage)
		return 2
	}
	fs := flag.NewFlagSet("secrets rotate", flag.ContinueOnError)
	fs.SetOutput(r.errOut)
	dir := fs.String("dir", defaultDir(), dirUsage)
	yes := fs.Bool("yes", false, "do not ask for confirmation")
	role := fs.String("role", "all", "db-passwords: core, payments, provisioner, backup, postgres or all")
	offline := fs.Bool("offline", false, "bot-token: do not check the token with Telegram")
	pos, err := parseArgs(fs, args[1:])
	if err != nil {
		return 2
	}
	if len(pos) != 1 {
		fmt.Fprintln(r.errOut, rotateUsage)
		return 2
	}
	name := strings.ToLower(pos[0])
	switch {
	case name == "bot-token" || name == "db-passwords" || name == "service-tokens" || name == "redis-password":
	case strings.Contains(name, "master"):
		fmt.Fprintln(r.errOut, "The master keys (BOBRES_CORE_MASTER_KEY, BOBRES_PROVISIONER_MASTER_KEY, BOBRES_PAYMENTS_MASTER_KEY) have a command of\n"+
			"their own: they encrypt the 3x-ui tokens, staff authenticators and gateway settings stored in the database, so a new\n"+
			"key must seal all of them again, after a backup, and that is what it does:\n"+
			"  sudo bobres rotate-key --dry-run   check and count, change nothing\n"+
			"  sudo bobres rotate-key\n"+
			"See docs/runbooks/rotate-secrets.md (Master keys). Nothing was changed.")
		return 1
	case strings.Contains(name, "backup-key"):
		fmt.Fprintln(r.errOut, "The backup key is not rotated: every backup made so far opens only with the current key. To start new\n"+
			"backups with a new key, follow docs/runbooks/rotate-secrets.md (The backup key). Nothing was changed.")
		return 1
	default:
		fmt.Fprintf(r.errOut, "bobres secrets: unknown secret %q\n%s\n", pos[0], rotateUsage)
		return 2
	}
	if *role != "all" && name != "db-passwords" {
		fmt.Fprintln(r.errOut, "error: --role is for db-passwords")
		return 2
	}
	env, ok := r.requireInstall(*dir)
	if !ok {
		return 2
	}
	ctx := context.Background()
	switch name {
	case "bot-token":
		return r.rotateBotToken(ctx, *dir, env, *offline)
	case "service-tokens":
		return r.rotateServiceTokens(ctx, *dir, env, *yes)
	case "redis-password":
		return r.rotateRedis(ctx, *dir, env, *yes)
	}
	return r.rotateDB(ctx, *dir, env, *role, *yes)
}

// rotation is one secrets change.
type rotation struct {
	what     string            // for the messages, e.g. "service tokens"
	values   map[string]string // the new .env values
	services []string          // the containers that read them
	// roles maps each database password in values to its role: the database
	// takes them first (nil: none).
	roles map[string]string
}

// readSecret reads one secret: without echo on a terminal, else a line of stdin.
func (r *rotator) readSecret(prompt string) (string, error) {
	if r.readHide != nil {
		return r.readHide(prompt)
	}
	fmt.Fprint(r.out, prompt)
	line, err := bufio.NewReader(r.in).ReadString('\n')
	if line = strings.TrimSpace(line); line != "" {
		return line, nil
	}
	if err == nil {
		err = errors.New("empty")
	}
	return "", err
}

// botUsername asks Telegram whose token this is (getMe at apiRoot; "" for
// api.telegram.org) and answers the bot's user name.
func botUsername(ctx context.Context, token, apiRoot string) (string, error) {
	me, err := tg.New(token, tg.WithAPIRoot(apiRoot)).GetMe(ctx)
	if err != nil {
		return "", err
	}
	return me.Username, nil
}

// telegramAPIRoot is where this server checks a bot token: the Bot API server
// in BOBRES_TELEGRAM_API_URL, which the bot uses, when it is an http(s) URL
// without credentials (as seen from the host: hostAPIRoot), else ""
// (api.telegram.org).
func telegramAPIRoot(env map[string]string) string {
	v := strings.TrimSpace(env["BOBRES_TELEGRAM_API_URL"])
	u, err := url.Parse(v)
	if v == "" || err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return ""
	}
	return hostAPIRoot(v)
}

// hostAPIRoot is BOBRES_TELEGRAM_API_URL as seen from this server: the
// containers reach the host as host.docker.internal, the CLI as 127.0.0.1.
func hostAPIRoot(v string) string {
	u, err := url.Parse(v)
	if v == "" || err != nil || !strings.EqualFold(u.Hostname(), "host.docker.internal") {
		return v
	}
	host := "127.0.0.1"
	if p := u.Port(); p != "" {
		host += ":" + p
	}
	u.Host = host
	return u.String()
}

func (r *rotator) rotateBotToken(ctx context.Context, dir string, env map[string]string, offline bool) int {
	tok, err := r.readSecret("New bot token from @BotFather: ")
	if err != nil || tok == "" {
		fmt.Fprintln(r.errOut, "error: no token was given. Nothing was changed.")
		return 1
	}
	if !botTokenRe.MatchString(tok) {
		fmt.Fprintln(r.errOut, "error: that does not look like a @BotFather token (123456789:AAH...). Nothing was changed.")
		return 1
	}
	if tok == env["BOBRES_TELEGRAM_BOT_TOKEN"] {
		fmt.Fprintln(r.out, "That is the token BOBRES already uses: nothing to change.")
		return 0
	}
	if !offline {
		cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		name, err := r.checkBot(cctx, tok, telegramAPIRoot(env))
		cancel()
		if err != nil {
			fmt.Fprintf(r.errOut, "error: Telegram rejected the token or is unreachable: %v (--offline skips this check). Nothing was changed.\n", err)
			return 1
		}
		fmt.Fprintf(r.out, "Bot token OK: @%s\n", name)
	}
	return r.rotate(ctx, dir, env, rotation{what: "bot token", values: map[string]string{"BOBRES_TELEGRAM_BOT_TOKEN": tok}, services: []string{"bot"}})
}

func (r *rotator) rotateServiceTokens(ctx context.Context, dir string, env map[string]string, yes bool) int {
	services := dataServices(dir, env)
	if !yes && !r.confirm(fmt.Sprintf("Rotate the service tokens? %s restart together with the new ones (seconds). [y/N]: ",
		strings.Join(services, ", "))) {
		fmt.Fprintln(r.out, "Cancelled. Nothing was changed.")
		return 1
	}
	// All pairs change at once, so no service is left with a stale peer token.
	values := map[string]string{}
	for _, k := range serviceTokens {
		values[k] = r.random(generated[k])
	}
	return r.rotate(ctx, dir, env, rotation{what: "service tokens", values: values, services: services})
}

func (r *rotator) rotateRedis(ctx context.Context, dir string, env map[string]string, yes bool) int {
	if !yes && !r.confirm("Rotate the Redis password? Redis and the bot restart (seconds; the data in Redis is kept). [y/N]: ") {
		fmt.Fprintln(r.out, "Cancelled. Nothing was changed.")
		return 1
	}
	// Redis takes its password from its environment when it starts: the new
	// container is the configuration change.
	pw := r.random(generated["REDIS_PASSWORD"])
	return r.rotate(ctx, dir, env, rotation{what: "Redis password",
		values:   map[string]string{"REDIS_PASSWORD": pw, "BOBRES_REDIS_URL": "redis://:" + pw + "@redis:6379/0"},
		services: []string{"redis", "bot"}})
}

func (r *rotator) rotateDB(ctx context.Context, dir string, env map[string]string, which string, yes bool) int {
	var chosen []dbRole
	for _, d := range dbRoles {
		if which != "all" && which != d.name {
			continue
		}
		// The backup service's role exists only where the service runs.
		if d.name == "backup" && (!backupEnabled(env) || env[d.env] == "") {
			if which == "backup" {
				fmt.Fprintln(r.errOut, "error: this install has no backup service, so no svc_backup role. Nothing was changed.")
				return 1
			}
			continue
		}
		chosen = append(chosen, d)
	}
	if len(chosen) == 0 {
		fmt.Fprintf(r.errOut, "error: unknown --role %q (core, payments, provisioner, backup, postgres or all)\n", which)
		return 2
	}
	running, err := r.storeRunning(ctx, dir)
	if err != nil {
		fmt.Fprintf(r.errOut, "error: %v. Nothing was changed.\n", err)
		return 1
	}
	if !running {
		fmt.Fprintf(r.errOut, "error: the database is not running, and it must take the new passwords: start BOBRES first (bobres start --dir %s). Nothing was changed.\n", dir)
		return 1
	}
	values, roles := map[string]string{}, map[string]string{}
	var names, services []string
	for _, d := range chosen {
		values[d.env], roles[d.env] = r.random(generated[d.env]), d.role
		names = append(names, d.role)
		if d.service != "" {
			services = append(services, d.service)
		}
	}
	restart := "nothing restarts (no service logs in with the superuser's password)"
	if len(services) > 0 {
		restart = strings.Join(services, ", ") + " restart (seconds)"
	}
	if !yes && !r.confirm(fmt.Sprintf("Rotate the database passwords (%s)? A backup of the database is taken first; then %s. [y/N]: ",
		strings.ReplaceAll(strings.Join(names, ", "), "CURRENT_USER", "the superuser"), restart)) {
		fmt.Fprintln(r.out, "Cancelled. Nothing was changed.")
		return 1
	}
	safety, err := r.safetyBackup(ctx, dir, env, "before-rotation", "made by bobres secrets rotate before it changed the database passwords")
	if err != nil {
		fmt.Fprintf(r.errOut, "error: the backup before the rotation failed: %v. Nothing was changed.\n", err)
		return 1
	}
	fmt.Fprintf(r.out, "Backup saved in %s\n", filepath.Join(dir, "backups", safety))
	return r.rotate(ctx, dir, env, rotation{what: "database passwords", values: values, services: services, roles: roles})
}

// rotate applies one rotation: the database first (when it has roles), then
// .env, then a restart of the services that read the values and a wait until
// they are healthy. When they are not, the previous values go back the same
// way. A copy of the old .env (.env.before-rotation, root only) is kept
// until the run ends well, for a run cut short.
func (r *rotator) rotate(ctx context.Context, dir string, env map[string]string, rot rotation) int {
	envPath := filepath.Join(dir, ".env")
	copyPath := envPath + ".before-rotation"
	next := maps.Clone(env)
	old := map[string]string{}
	for k, v := range rot.values {
		next[k], old[k] = v, env[k]
	}
	running, err := r.storeRunning(ctx, dir)
	if err != nil {
		fmt.Fprintf(r.errOut, "error: %v. Nothing was changed.\n", err)
		return 1
	}
	b, err := os.ReadFile(envPath) //nolint:gosec // the install's own file
	if err == nil {
		err = writeAtomic(copyPath, b, 0o600)
	}
	if err != nil {
		fmt.Fprintf(r.errOut, "error: keep a copy of .env: %v. Nothing was changed.\n", err)
		return 1
	}
	if rot.roles != nil {
		fmt.Fprintln(r.out, "Setting the new passwords in the database...")
		if err := r.setRolePasswords(ctx, dir, rot.roles, rot.values); err != nil {
			_ = os.Remove(copyPath)
			fmt.Fprintf(r.errOut, "error: the database did not take the new passwords: %v. Nothing was changed.\n", err)
			return 1
		}
	}
	if err := writeEnv(envPath, next); err != nil {
		fmt.Fprintf(r.errOut, "error: %v\n", err)
		if rot.roles != nil && r.setRolePasswords(ctx, dir, rot.roles, old) != nil {
			fmt.Fprintf(r.errOut, "The database has the new passwords and .env the old ones; the .env from before is in %s. See docs/runbooks/rotate-secrets.md.\n", copyPath)
			return 1
		}
		_ = os.Remove(copyPath)
		fmt.Fprintln(r.errOut, "Nothing was changed.")
		return 1
	}
	fmt.Fprintf(r.out, "New %s saved in %s.\n", rot.what, envPath)
	services := installedOnly(dir, rot.services, next)
	switch {
	case len(services) == 0:
		_ = os.Remove(copyPath)
		r.syncRestorePoint(dir, rot.values)
		fmt.Fprintf(r.out, "Done: %s rotated; no service restarts.\n", rot.what)
		return 0
	case !running:
		_ = os.Remove(copyPath)
		r.syncRestorePoint(dir, rot.values)
		fmt.Fprintf(r.out, "BOBRES is not running: it starts with the new %s (bobres start --dir %s).\n", rot.what, dir)
		return 0
	}
	if r.upAndWait(ctx, dir, services) == 0 {
		_ = os.Remove(copyPath)
		r.syncRestorePoint(dir, rot.values)
		fmt.Fprintf(r.out, "Done: %s rotated.\n", rot.what)
		return 0
	}

	fmt.Fprintf(r.errOut, "\nerror: the services did not come back healthy with the new %s: putting the previous values back...\n", rot.what)
	if rot.roles != nil {
		if err := r.setRolePasswords(ctx, dir, rot.roles, old); err != nil {
			fmt.Fprintf(r.errOut, "error: the database kept the new passwords (%v), so .env keeps them too.\n"+
				"The .env from before is in %s. See docs/runbooks/rotate-secrets.md.\n", err, copyPath)
			return 1
		}
	}
	if err := writeEnv(envPath, env); err != nil {
		fmt.Fprintf(r.errOut, "error: %v\nThe .env from before is in %s: copy it back over %s and run bobres start.\n", err, copyPath, envPath)
		return 1
	}
	if r.upAndWait(ctx, dir, services) != 0 {
		fmt.Fprintf(r.errOut, "The previous values are back in .env, but the services are still not healthy: the cause is elsewhere\n"+
			"(bobres logs --dir %s %s).\n", dir, services[0])
		_ = os.Remove(copyPath)
		return 1
	}
	_ = os.Remove(copyPath)
	fmt.Fprintf(r.errOut, "The previous values are back and the services are healthy: nothing was rotated. See the messages above\n"+
		"(bobres logs --dir %s %s).\n", dir, services[0])
	return 1
}

// syncRestorePoint writes rotated values into the last update's restore
// point too, so that bobres rollback, which puts its .env back, keeps them:
// the database roles keep their new passwords through a restore (pg_dump
// does not carry roles), and a revoked bot token or an old service token
// must not come back either.
func (r *rotator) syncRestorePoint(dir string, values map[string]string) {
	path := filepath.Join(restorePointPath(dir), restorePointFiles, ".env")
	if !lexists(path) {
		return
	}
	env, _, err := readEnv(path)
	if err == nil {
		maps.Copy(env, values)
		err = writeEnv(path, env)
	}
	if err != nil {
		fmt.Fprintf(r.errOut, "warning: the last update's restore point keeps the old values (%v): after bobres rollback, rotate again.\n", err)
		return
	}
	fmt.Fprintln(r.out, "The last update's restore point has the new values too (bobres rollback keeps them).")
}

// alterRolesScript sets role passwords in one transaction, in the postgres
// container. Its stdin starts with NAME=value lines (the passwords) ended by
// an empty line, which the shell exports; psql reads the SQL after them and
// takes each password with \getenv, so none is on a command line.
const alterRolesScript = `while IFS= read -r kv && [ -n "$kv" ]; do export "$kv"; done
exec psql -X -q -v ON_ERROR_STOP=1 --no-password --username "$POSTGRES_USER" --dbname "$POSTGRES_DB"`

// setRolePasswords gives each role in roles (env name -> role) the password
// values has under that env name.
func (o *ops) setRolePasswords(ctx context.Context, dir string, roles, values map[string]string) error {
	names := make([]string, 0, len(roles))
	for name := range roles {
		names = append(names, name)
	}
	sort.Strings(names)
	var in strings.Builder
	for i, name := range names {
		fmt.Fprintf(&in, "BOBRES_PW_%d=%s\n", i, values[name])
	}
	in.WriteString("\nBEGIN;\n")
	for i, name := range names {
		fmt.Fprintf(&in, "\\getenv pw BOBRES_PW_%d\nALTER ROLE %s WITH PASSWORD :'pw';\n", i, roles[name])
	}
	in.WriteString("COMMIT;\n")
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	if err := o.pipe(ctx, dir, strings.NewReader(in.String()), nil, composeArgs("exec", "-T", "postgres", "sh", "-c", alterRolesScript)...); err != nil {
		return fmt.Errorf("psql in the postgres container: %w", err)
	}
	return nil
}
