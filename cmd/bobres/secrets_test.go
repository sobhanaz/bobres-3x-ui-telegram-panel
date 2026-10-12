package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// rotateDB is fakeDB that also runs alterRolesScript, keeping each run's
// stdin and command.
type rotateDB struct {
	*fakeDB
	roleRuns []string
	roleArgs [][]string
	roleErr  []error // the result of each run in turn (nil: success)
}

func (f *rotateDB) pipe(ctx context.Context, dir string, in io.Reader, w io.Writer, args ...string) error {
	if args[len(args)-1] != alterRolesScript {
		return f.fakeDB.pipe(ctx, dir, in, w, args...)
	}
	f.steps = append(f.steps, "roles")
	b, err := io.ReadAll(in)
	if err != nil {
		return err
	}
	f.roleRuns = append(f.roleRuns, string(b))
	f.roleArgs = append(f.roleArgs, args)
	if n := len(f.roleRuns); n <= len(f.roleErr) {
		return f.roleErr[n-1]
	}
	return nil
}

type botCheck struct {
	tokens, roots []string
	err           error
}

func testRotator(stdin string, f *rotateDB) (*rotator, *bytes.Buffer, *bytes.Buffer, *botCheck) {
	o, out, errOut := dbOps(stdin, f.fakeDB)
	o.pipe = f.pipe
	bc := &botCheck{}
	n := 0
	r := &rotator{
		ops: o,
		random: func(size int) string {
			n++
			return fmt.Sprintf("new%02d-%d", n, size)
		},
		checkBot: func(_ context.Context, token, root string) (string, error) {
			bc.tokens, bc.roots = append(bc.tokens, token), append(bc.roots, root)
			return "new_bot", bc.err
		},
	}
	return r, out, errOut, bc
}

const newBotToken = "987654321:BBH-abcdefghijklmnopqrstuvwxyz987654" //nolint:gosec // test fixture, gitleaks:allow

// envUnchanged checks that .env is byte for byte what it was, with no copy left.
func envUnchanged(t *testing.T, dir, before string) {
	t.Helper()
	if _, after := readEnvFile(t, dir); after != before {
		t.Errorf(".env changed:\n%s", after)
	}
	if exists(filepath.Join(dir, ".env.before-rotation")) {
		t.Error(".env.before-rotation left behind")
	}
}

func TestRotateServiceTokens(t *testing.T) {
	dir := fullInstall(t, true)
	before, _ := readEnvFile(t, dir)
	f := &rotateDB{fakeDB: &fakeDB{ps: func() string { return healthyWithBackup }}}
	r, out, errOut, _ := testRotator("", f)
	if code := r.secretsCmd([]string{"rotate", "service-tokens", "--yes", "--dir", dir}); code != 0 {
		t.Fatalf("exit %d %s", code, errOut)
	}
	env, _ := readEnvFile(t, dir)
	seen := map[string]bool{}
	if len(serviceTokens) != 6 {
		t.Fatalf("service tokens: %v", serviceTokens)
	}
	for _, k := range serviceTokens {
		if env[k] == before[k] || !strings.HasPrefix(env[k], "new") || !strings.HasSuffix(env[k], "-32") || seen[env[k]] {
			t.Errorf("%s: %q", k, env[k])
		}
		seen[env[k]] = true
	}
	for k, v := range before {
		if !strings.HasPrefix(k, "BOBRES_TOKEN_") && env[k] != v {
			t.Errorf("%s changed", k)
		}
	}
	// Every service with a token restarts at once; the database is not touched.
	if got := strings.Join(f.steps, "|"); got != "up -d --no-deps core payments provisioner bot backup" {
		t.Fatalf("steps %q", got)
	}
	if exists(filepath.Join(dir, ".env.before-rotation")) || !strings.Contains(out.String(), "Done: service tokens rotated.") ||
		!strings.Contains(out.String(), "Restarting core, payments, provisioner, bot, backup") {
		t.Fatalf("output: %s", out)
	}
	// Never a secret in the output.
	for _, v := range env {
		if strings.HasPrefix(v, "new") && strings.Contains(out.String()+errOut.String(), v) {
			t.Fatalf("a new value is shown: %s", out)
		}
	}
}

func TestRotateRedisPassword(t *testing.T) {
	dir := fullInstall(t, false)
	f := &rotateDB{fakeDB: &fakeDB{}}
	r, _, errOut, _ := testRotator("y\n", f)
	if code := r.secretsCmd([]string{"rotate", "redis-password", "--dir", dir}); code != 0 {
		t.Fatalf("exit %d %s", code, errOut)
	}
	env, _ := readEnvFile(t, dir)
	if env["REDIS_PASSWORD"] != "new01-24" || env["BOBRES_REDIS_URL"] != "redis://:new01-24@redis:6379/0" {
		t.Fatalf("redis: %q %q", env["REDIS_PASSWORD"], env["BOBRES_REDIS_URL"])
	}
	if got := strings.Join(f.steps, "|"); got != "up -d --no-deps redis bot" {
		t.Fatalf("steps %q", got)
	}
}

func TestRotateBotToken(t *testing.T) {
	dir := fullInstall(t, false)
	f := &rotateDB{fakeDB: &fakeDB{}}
	r, out, errOut, bc := testRotator(newBotToken+"\n", f)
	if code := r.secretsCmd([]string{"rotate", "bot-token", "--dir", dir}); code != 0 {
		t.Fatalf("exit %d %s", code, errOut)
	}
	if env, _ := readEnvFile(t, dir); env["BOBRES_TELEGRAM_BOT_TOKEN"] != newBotToken {
		t.Fatalf("token: %q", env["BOBRES_TELEGRAM_BOT_TOKEN"])
	}
	if strings.Join(f.steps, "|") != "up -d --no-deps bot" || len(bc.tokens) != 1 || bc.tokens[0] != newBotToken || bc.roots[0] != "" {
		t.Fatalf("steps %q, getMe %+v", f.steps, bc)
	}
	if !strings.Contains(out.String(), "Bot token OK: @new_bot") || strings.Contains(out.String(), newBotToken) {
		t.Fatalf("output: %s", out)
	}

	// The getMe check goes where the bot goes: a local Bot API server, from the host.
	dir = fullInstall(t, false)
	o, _, _ := dbOps("", &fakeDB{})
	if code := o.configSet(dir, "BOBRES_TELEGRAM_API_URL", "http://host.docker.internal:8081", true); code != 0 {
		t.Fatal(code)
	}
	r, _, _, bc = testRotator(newBotToken+"\n", &rotateDB{fakeDB: &fakeDB{}})
	if code := r.secretsCmd([]string{"rotate", "bot-token", "--dir", dir}); code != 0 || bc.roots[0] != "http://127.0.0.1:8081" {
		t.Fatalf("api root: %d %+v", code, bc)
	}
}

func TestRotateBotTokenRefusals(t *testing.T) {
	for _, c := range []struct {
		name, stdin string
		args        []string
		checkErr    error
		code        int
		want        string
	}{
		{"no token", "\n", nil, nil, 1, "no token was given"},
		{"not a token", "hello\n", nil, nil, 1, "does not look like a @BotFather token"},
		{"Telegram says no", newBotToken + "\n", nil, errors.New("Unauthorized"), 1, "Telegram rejected the token or is unreachable: Unauthorized"},
		{"the same", goodBotToken + "\n", nil, nil, 0, "already uses"},
	} {
		dir := fullInstall(t, false)
		_, before := readEnvFile(t, dir)
		f := &rotateDB{fakeDB: &fakeDB{}}
		r, out, errOut, bc := testRotator(c.stdin, f)
		bc.err = c.checkErr
		code := r.secretsCmd(append([]string{"rotate", "bot-token", "--dir", dir}, c.args...))
		if code != c.code || !strings.Contains(out.String()+errOut.String(), c.want) || len(f.steps) != 0 {
			t.Errorf("%s: %d %q %s%s", c.name, code, f.steps, out, errOut)
		}
		envUnchanged(t, dir, before)
	}
	// --offline: no check.
	dir := fullInstall(t, false)
	r, _, errOut, bc := testRotator(newBotToken+"\n", &rotateDB{fakeDB: &fakeDB{}})
	if code := r.secretsCmd([]string{"rotate", "bot-token", "--offline", "--dir", dir}); code != 0 || len(bc.tokens) != 0 {
		t.Fatalf("--offline: %d %+v %s", code, bc, errOut)
	}
}

func TestRotateDBPasswords(t *testing.T) {
	dir := fullInstall(t, true)
	before, _ := readEnvFile(t, dir)
	f := &rotateDB{fakeDB: &fakeDB{dump: "DUMP BEFORE THE ROTATION", ps: func() string { return healthyWithBackup }}}
	r, out, errOut, _ := testRotator("y\n", f)
	if code := r.secretsCmd([]string{"rotate", "db-passwords", "--dir", dir}); code != 0 {
		t.Fatalf("exit %d %s", code, errOut)
	}
	// A backup first, then the database, then the services that log in.
	if got := strings.Join(f.steps, "|"); got != "dump|roles|up -d --no-deps core payments provisioner backup" {
		t.Fatalf("steps %q", got)
	}
	env, _ := readEnvFile(t, dir)
	vars := []string{"DB_BACKUP_PASSWORD", "DB_CORE_PASSWORD", "DB_PAYMENTS_PASSWORD", "DB_PROVISIONER_PASSWORD", "POSTGRES_PASSWORD"}
	roles := []string{"svc_backup", "svc_core", "svc_payments", "svc_provisioner", "CURRENT_USER"}
	script := f.roleRuns[0]
	for i, k := range vars {
		if env[k] == before[k] || !strings.HasSuffix(env[k], "-24") {
			t.Errorf("%s: %q", k, env[k])
		}
		// The passwords go in on stdin and through \getenv: on no command line.
		if !strings.Contains(script, fmt.Sprintf("BOBRES_PW_%d=%s\n", i, env[k])) ||
			!strings.Contains(script, fmt.Sprintf("\\getenv pw BOBRES_PW_%d\nALTER ROLE %s WITH PASSWORD :'pw';", i, roles[i])) ||
			strings.Contains(strings.Join(f.roleArgs[0], " "), env[k]) {
			t.Errorf("%s in the script:\n%s", k, script)
		}
	}
	if !strings.Contains(script, "\n\nBEGIN;\n") || !strings.HasSuffix(script, "COMMIT;\n") {
		t.Fatalf("one transaction:\n%s", script)
	}
	backups, _ := filepath.Glob(filepath.Join(dir, "backups", "*-before-rotation.bkp"))
	if len(backups) != 1 {
		t.Fatalf("safety backup: %v", backups)
	}
	if _, dump := openBackup(t, backups[0], installKey); dump != "DUMP BEFORE THE ROTATION" {
		t.Fatalf("backup holds %q", dump)
	}
	if !strings.Contains(out.String(), "Backup saved in") || !strings.Contains(out.String(), "Done: database passwords rotated.") {
		t.Fatalf("output: %s", out)
	}
}

func TestRotateDBOneRole(t *testing.T) {
	dir := fullInstall(t, true)
	before, _ := readEnvFile(t, dir)
	f := &rotateDB{fakeDB: &fakeDB{ps: func() string { return healthyWithBackup }}}
	r, _, errOut, _ := testRotator("", f)
	if code := r.secretsCmd([]string{"rotate", "db-passwords", "--role", "payments", "--yes", "--dir", dir}); code != 0 {
		t.Fatalf("exit %d %s", code, errOut)
	}
	env, _ := readEnvFile(t, dir)
	for k, v := range before {
		if changed := env[k] != v; changed != (k == "DB_PAYMENTS_PASSWORD") {
			t.Errorf("%s changed: %v", k, changed)
		}
	}
	if got := strings.Join(f.steps, "|"); got != "dump|roles|up -d --no-deps payments" || !strings.Contains(f.roleRuns[0], "ALTER ROLE svc_payments") {
		t.Fatalf("steps %q\n%s", got, f.roleRuns)
	}
	// The superuser's: nothing logs in with it, nothing restarts.
	f.steps = nil
	if code := r.secretsCmd([]string{"rotate", "db-passwords", "--role", "postgres", "--yes", "--dir", dir}); code != 0 ||
		strings.Join(f.steps, "|") != "dump|roles" || !strings.Contains(f.roleRuns[1], "ALTER ROLE CURRENT_USER") {
		t.Fatalf("postgres role: %d %q", code, f.steps)
	}
}

func TestRotateDBRefusals(t *testing.T) {
	// No backup service: no svc_backup to rotate, and "all" leaves it out.
	dir := fullInstall(t, false)
	_, before := readEnvFile(t, dir)
	f := &rotateDB{fakeDB: &fakeDB{}}
	r, _, errOut, _ := testRotator("", f)
	if code := r.secretsCmd([]string{"rotate", "db-passwords", "--role", "backup", "--yes", "--dir", dir}); code != 1 || !strings.Contains(errOut.String(), "no svc_backup role") {
		t.Fatalf("backup role: %d %s", code, errOut)
	}
	if code := r.secretsCmd([]string{"rotate", "db-passwords", "--role", "nope", "--yes", "--dir", dir}); code != 2 {
		t.Fatalf("unknown role: %d", code)
	}
	envUnchanged(t, dir, before)
	if code := r.secretsCmd([]string{"rotate", "db-passwords", "--yes", "--dir", dir}); code != 0 || strings.Contains(f.roleRuns[0], "svc_backup") {
		t.Fatalf("all without the backup service: %d %s", code, f.roleRuns)
	}

	for _, c := range []struct {
		name string
		set  func(*rotateDB)
		want string
	}{
		{"stopped", func(f *rotateDB) {
			f.ps = func() string { return `[{"Service":"postgres","State":"exited","Health":""}]` }
		}, "the database is not running"},
		{"backup fails", func(f *rotateDB) { f.dumpErr = errors.New("exit status 1") }, "the backup before the rotation failed"},
		{"database refuses", func(f *rotateDB) { f.roleErr = []error{errors.New("exit status 3")} }, "the database did not take the new passwords"},
	} {
		dir := fullInstall(t, true)
		_, before := readEnvFile(t, dir)
		f := &rotateDB{fakeDB: &fakeDB{ps: func() string { return healthyWithBackup }}}
		c.set(f)
		r, _, errOut, _ := testRotator("", f)
		if code := r.secretsCmd([]string{"rotate", "db-passwords", "--yes", "--dir", dir}); code != 1 || !strings.Contains(errOut.String(), c.want) ||
			!strings.Contains(errOut.String(), "Nothing was changed") {
			t.Errorf("%s: %d %s", c.name, code, errOut)
		}
		for _, s := range f.steps {
			if strings.HasPrefix(s, "up") {
				t.Errorf("%s: restarted: %q", c.name, f.steps)
			}
		}
		envUnchanged(t, dir, before)
	}
}

// The services do not come back healthy: the previous values go back, in
// the database too, and the services start with them.
func TestRotateRollsBackWhenUnhealthy(t *testing.T) {
	dir := fullInstall(t, true)
	_, before := readEnvFile(t, dir)
	unhealthyWithNew := func() string {
		if env, _, _ := readEnv(filepath.Join(dir, ".env")); strings.HasPrefix(env["DB_CORE_PASSWORD"], "new") {
			return strings.Replace(healthyWithBackup, `"core","State":"running","Health":"healthy"`, `"core","State":"running","Health":"unhealthy"`, 1)
		}
		return healthyWithBackup
	}
	f := &rotateDB{fakeDB: &fakeDB{ps: unhealthyWithNew}}
	r, _, errOut, _ := testRotator("", f)
	if code := r.secretsCmd([]string{"rotate", "db-passwords", "--yes", "--dir", dir}); code != 1 {
		t.Fatalf("exit %d", code)
	}
	envUnchanged(t, dir, before)
	if got := strings.Join(f.steps, "|"); got != "dump|roles|up -d --no-deps core payments provisioner backup|roles|up -d --no-deps core payments provisioner backup" {
		t.Fatalf("steps %q", got)
	}
	if !strings.Contains(f.roleRuns[1], "BOBRES_PW_1=old-db_core_password\n") {
		t.Fatalf("the old passwords go back:\n%s", f.roleRuns[1])
	}
	if !strings.Contains(errOut.String(), "putting the previous values back") || !strings.Contains(errOut.String(), "nothing was rotated") {
		t.Fatalf("errors: %s", errOut)
	}

	// The database cannot take the old ones back: .env keeps the new ones
	// (they match the database) and the copy from before stays.
	dir = fullInstall(t, true)
	f = &rotateDB{fakeDB: &fakeDB{ps: func() string {
		return strings.Replace(healthyWithBackup, `"core","State":"running","Health":"healthy"`, `"core","State":"running","Health":"unhealthy"`, 1)
	}}, roleErr: []error{nil, errors.New("exit status 2")}}
	r, _, errOut, _ = testRotator("", f)
	if code := r.secretsCmd([]string{"rotate", "db-passwords", "--yes", "--dir", dir}); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if env, _ := readEnvFile(t, dir); !strings.HasPrefix(env["DB_CORE_PASSWORD"], "new") {
		t.Fatalf(".env must match the database: %q", env["DB_CORE_PASSWORD"])
	}
	if b, err := os.ReadFile(filepath.Join(dir, ".env.before-rotation")); err != nil || !strings.Contains(string(b), "old-db_core_password") {
		t.Fatalf("the copy from before: %v", err)
	}
	if !strings.Contains(errOut.String(), "the database kept the new passwords") {
		t.Fatalf("errors: %s", errOut)
	}
}

func TestRotateBotTokenRollsBack(t *testing.T) {
	dir := fullInstall(t, false)
	_, before := readEnvFile(t, dir)
	f := &rotateDB{fakeDB: &fakeDB{}}
	f.ps = func() string {
		if env, _, _ := readEnv(filepath.Join(dir, ".env")); env["BOBRES_TELEGRAM_BOT_TOKEN"] == newBotToken {
			return strings.Replace(healthyPS, `"bot","State":"running","Health":"healthy"`, `"bot","State":"restarting","Health":"unhealthy"`, 1)
		}
		return healthyPS
	}
	r, _, _, _ := testRotator(newBotToken+"\n", f)
	if code := r.secretsCmd([]string{"rotate", "bot-token", "--dir", dir}); code != 1 {
		t.Fatalf("exit %d", code)
	}
	envUnchanged(t, dir, before)
	if got := strings.Join(f.steps, "|"); got != "up -d --no-deps bot|up -d --no-deps bot" {
		t.Fatalf("steps %q", got)
	}
}

func TestRotateWhileStopped(t *testing.T) {
	dir := fullInstall(t, false)
	f := &rotateDB{fakeDB: &fakeDB{ps: func() string { return `[{"Service":"postgres","State":"exited","Health":""}]` }}}
	r, out, _, _ := testRotator("", f)
	if code := r.secretsCmd([]string{"rotate", "service-tokens", "--yes", "--dir", dir}); code != 0 || len(f.steps) != 0 ||
		!strings.Contains(out.String(), "BOBRES is not running: it starts with the new service tokens") {
		t.Fatalf("stopped: %d %q %s", code, f.steps, out)
	}
	if env, _ := readEnvFile(t, dir); !strings.HasPrefix(env["BOBRES_TOKEN_CORE"], "new") {
		t.Fatal("not written")
	}
}

func TestSecretsRefusalsAndUsage(t *testing.T) {
	dir := fullInstall(t, true)
	_, before := readEnvFile(t, dir)
	f := &rotateDB{fakeDB: &fakeDB{}}
	for _, c := range []struct {
		args  []string
		stdin string
		code  int
		want  string
	}{
		{[]string{"rotate", "master-keys"}, "", 1, "re-encrypting with a new key is future work"},
		{[]string{"rotate", "BOBRES_CORE_MASTER_KEY"}, "", 1, "The master keys"},
		{[]string{"rotate", "backup-key"}, "", 1, "every backup made so far opens only with the current key"},
		{[]string{"rotate", "service-tokens"}, "n\n", 1, "Cancelled. Nothing was changed."},
		{[]string{"rotate", "redis-password"}, "", 1, "Cancelled. Nothing was changed."},
		{[]string{"rotate", "db-passwords"}, "no\n", 1, "Cancelled. Nothing was changed."},
		{[]string{"rotate", "xui-token"}, "", 2, "unknown secret"},
		{[]string{"rotate"}, "", 2, "usage: bobres secrets rotate"},
		{[]string{"list"}, "", 2, "usage: bobres secrets rotate"},
		{nil, "", 2, "usage: bobres secrets rotate"},
		{[]string{"rotate", "bot-token", "--role", "core"}, "", 2, "--role is for db-passwords"},
		{[]string{"rotate", "bot-token", "extra"}, "", 2, "usage"},
	} {
		r, out, errOut, _ := testRotator(c.stdin, f)
		if code := r.secretsCmd(append(c.args, "--dir", dir)); code != c.code || !strings.Contains(out.String()+errOut.String(), c.want) {
			t.Errorf("%v: %d %s%s", c.args, code, out, errOut)
		}
	}
	if len(f.steps) != 0 || len(f.roleRuns) != 0 {
		t.Fatalf("steps %q", f.steps)
	}
	envUnchanged(t, dir, before)
	r, _, errOut, _ := testRotator("", f)
	if code := r.secretsCmd([]string{"rotate", "service-tokens", "--dir", t.TempDir()}); code != 2 || !strings.Contains(errOut.String(), "no install found") {
		t.Fatalf("no install: %d", code)
	}
}

func TestHostAPIRoot(t *testing.T) {
	for in, want := range map[string]string{
		"": "", "https://tg.example.com": "https://tg.example.com",
		"http://host.docker.internal:8081": "http://127.0.0.1:8081", "http://HOST.DOCKER.INTERNAL/x": "http://127.0.0.1/x",
	} {
		if got := hostAPIRoot(in); got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
}

// The last update's restore point gets the new values too: bobres rollback
// puts its .env back, and the database roles keep their new passwords.
func TestRotateUpdatesTheRestorePoint(t *testing.T) {
	dir := fullInstall(t, true)
	files := filepath.Join(dir, restorePointDir, restorePointFiles)
	if err := os.MkdirAll(files, 0o700); err != nil {
		t.Fatal(err)
	}
	// The install's own test values (fullInstall), from before an update to 1.2.3.
	old, _ := readEnvFile(t, dir)
	old["BOBRES_VERSION"] = "1.1.0"
	if err := os.WriteFile(filepath.Join(files, ".env"), renderEnv(old), 0o600); err != nil {
		t.Fatal(err)
	}
	f := &rotateDB{fakeDB: &fakeDB{ps: func() string { return healthyWithBackup }}}
	r, out, errOut, _ := testRotator("", f)
	if code := r.secretsCmd([]string{"rotate", "db-passwords", "--role", "core", "--yes", "--dir", dir}); code != 0 {
		t.Fatalf("exit %d %s", code, errOut)
	}
	rp, _, _ := readEnv(filepath.Join(files, ".env"))
	env, _ := readEnvFile(t, dir)
	if rp["DB_CORE_PASSWORD"] != env["DB_CORE_PASSWORD"] || rp["POSTGRES_PASSWORD"] != "old-postgres_password" || rp["BOBRES_VERSION"] != "1.1.0" {
		t.Fatalf("restore point .env: %v", rp)
	}
	if st, _ := os.Stat(filepath.Join(files, ".env")); st.Mode().Perm() != 0o600 || !strings.Contains(out.String(), "bobres rollback keeps them") {
		t.Fatalf("mode %v\n%s", st.Mode(), out)
	}
	// A rotation that did not go through leaves it alone.
	f.ps = func() string {
		return strings.Replace(healthyWithBackup, `"payments","State":"running","Health":"healthy"`, `"payments","State":"running","Health":"unhealthy"`, 1)
	}
	before, _ := os.ReadFile(filepath.Join(files, ".env"))
	if code := r.secretsCmd([]string{"rotate", "db-passwords", "--role", "payments", "--yes", "--dir", dir}); code != 1 {
		t.Fatalf("unhealthy: %d", code)
	}
	if after, _ := os.ReadFile(filepath.Join(files, ".env")); string(after) != string(before) {
		t.Fatalf("restore point changed by a failed rotation:\n%s", after)
	}
}
