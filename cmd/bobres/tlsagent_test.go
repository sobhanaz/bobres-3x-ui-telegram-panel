package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/deploy"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tlsconf"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tlsprobe"
)

// writeInstalledCompose gives dir the docker-compose.yml a current install has
// (the embedded one, as the installer writes it): it defines the tls-agent.
func writeInstalledCompose(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "docker-compose.yml"), installedCompose(deploy.ComposeFile), 0o644); err != nil { //nolint:gosec // as the installer writes it
		t.Fatal(err)
	}
}

// oldCompose is the compose file of a release from before the HTTPS manager:
// no tls-agent (and none of its volumes). A rollback brings this back.
const oldCompose = `# BOBRES stack of 0.1.0-rc.1
x-app: &app
  restart: unless-stopped
services:
  caddy:
    image: caddy:2-alpine
    volumes:
      - ./Caddyfile:/etc/caddy/Caddyfile:ro
  postgres:
    image: postgres:16-alpine
  core:
    <<: *app
    image: ghcr.io/sobhanaz/bobres-core:${BOBRES_VERSION:-latest}
    depends_on:
      postgres: { condition: service_healthy }
  bot:
    <<: *app
    image: ghcr.io/sobhanaz/bobres-bot:${BOBRES_VERSION:-latest}
networks:
  public: {}
volumes:
  pg_data: {}
`

func writeOldCompose(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte(oldCompose), 0o644); err != nil { //nolint:gosec // a fixture
		t.Fatal(err)
	}
}

// setVersion rewrites BOBRES_VERSION in dir's .env.
func setVersion(t *testing.T, dir, v string) {
	t.Helper()
	env, _ := readEnvFile(t, dir)
	env["BOBRES_VERSION"] = v
	if err := os.WriteFile(filepath.Join(dir, ".env"), renderEnv(env), 0o600); err != nil {
		t.Fatal(err)
	}
}

// psWithout is the `compose ps` of a healthy install, minus one service.
func psWithout(t *testing.T, ps, service string) string {
	t.Helper()
	var rows []map[string]string
	if err := json.Unmarshal([]byte(ps), &rows); err != nil {
		t.Fatal(err)
	}
	rows = slices.DeleteFunc(rows, func(r map[string]string) bool { return r["Service"] == service })
	b, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// An image has the tls-agent unless its tag names a release from before it:
// a tag that is no release (ci, lab builds), `latest` and an empty tag run
// images built from the source that has it.
func TestTLSAgentImageRule(t *testing.T) {
	withRelease(t, "v0.1.0", "")
	for v, want := range map[string]bool{
		"0.1.0-rc.1": false, "v0.1.0-rc.1": false, "0.0.9": false, "0.1.0-beta.1": false, "0.1.0-alpha.3": false, "0.0.1-rc.9": false,
		"0.1.0-rc.2": true, "0.1.0-rc.10": true, "0.1.0-rc.2+build5": true, "0.1.0": true, "v0.1.0": true, "0.1.1": true, "0.2.3": true,
		"1.0.0": true, "1.0.0-rc.1": true,
		"": true, "latest": true, "ci": true, "0.1.0-lab.3": true, "0.1.0-lab.12": true, "garbage": true, "sha-1a2b3c": true, "dev": true,
	} {
		if got := tlsAgentImage(v); got != want {
			t.Errorf("tlsAgentImage(%q) = %v, want %v", v, got, want)
		}
	}
	// A development build of the CLI runs images built from its source.
	withRelease(t, "dev", "")
	if !tlsAgentImage("0.1.0-rc.1") {
		t.Error("a development build has the tls-agent in every image")
	}
	// A CLI that carries the agent runs its own release's images.
	withRelease(t, "v0.1.0-rc.2", "")
	if !tlsAgentImage("0.1.0-rc.2") || tlsAgentImage("0.1.0-rc.1") {
		t.Error("the CLI of rc.2 has the agent in its own images and not in rc.1's")
	}
}

func TestComposeTextDefines(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		want       bool
	}{
		{"the embedded compose file", string(deploy.ComposeFile), true},
		{"the installed one", string(installedCompose(deploy.ComposeFile)), true},
		{"a compose file from before the agent", oldCompose, false},
		{"empty", "", false},
		{"only a dependency on it", "services:\n  core:\n    depends_on:\n      tls-agent: { condition: service_healthy }\n", false},
		{"a longer name", "services:\n  tls-agent-2:\n    image: x\n", false},
		{"a shorter name", "services:\n  tls:\n    image: x\n", false},
		{"in a comment", "services:\n  # tls-agent:\n  core:\n    image: x\n", false},
		{"as a volume", "services:\n  core:\n    image: x\nvolumes:\n  tls-agent: {}\n", false},
		{"as an extension field", "x-tls-agent: &a\n  image: x\nservices:\n  core:\n    image: x\n", false},
		{"under services", "services:\n  tls-agent:\n    image: x\n", true},
		{"four-space indentation", "services:\n    core:\n        image: x\n    tls-agent:\n        image: x\n", true},
		{"quoted", "services:\n  \"tls-agent\":\n    image: x\n", true},
		{"with a comment after the key", "services:\n  tls-agent: # the agent\n    image: x\n", true},
		{"services after other keys", "x-app: &app\n  restart: always\nservices:\n  tls-agent:\n    <<: *app\n", true},
		{"windows line ends", "services:\r\n  tls-agent:\r\n    image: x\r\n", true},
		{"a services key with a comment", "services: # all of them\n  tls-agent:\n    image: x\n", true},
		{"not under services", "other:\n  tls-agent:\n    image: x\nservices:\n  core:\n    image: x\n", false},
	} {
		if got := composeTextDefines(tc.text, "tls-agent"); got != tc.want {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
	// The embedded file's own services, as docker lists them.
	if !composeTextDefines(string(deploy.ComposeFile), "backup") || composeTextDefines(string(deploy.ComposeFile), "nothing") {
		t.Error("the embedded compose file's services are not read right")
	}
}

// The one predicate: the compose file on disk defines the agent AND the images
// have it. A missing file says no, a file that cannot be read says yes.
func TestTLSAgentOn(t *testing.T) {
	withRelease(t, "v0.1.0", "")
	type fixture struct {
		name  string
		write func(t *testing.T, dir string)
	}
	current := fixture{"the current compose", writeInstalledCompose}
	old := fixture{"an old compose", writeOldCompose}
	none := fixture{"no compose file", func(*testing.T, string) {}}
	for _, tc := range []struct {
		fx      fixture
		version string
		want    bool
	}{
		{current, "0.1.0-rc.2", true}, {current, "0.1.0", true}, {current, "latest", true}, {current, "", true},
		{current, "ci", true}, {current, "0.1.0-lab.3", true},
		{current, "0.1.0-rc.1", false}, {current, "0.0.9", false},
		{old, "0.1.0-rc.2", false}, {old, "0.1.0", false}, {old, "latest", false}, {old, "", false}, {old, "ci", false},
		{old, "0.1.0-lab.3", false}, {old, "0.1.0-rc.1", false},
		{none, "0.1.0", false}, {none, "latest", false},
	} {
		dir := t.TempDir()
		tc.fx.write(t, dir)
		if got := tlsAgentOn(dir, map[string]string{"BOBRES_VERSION": tc.version}); got != tc.want {
			t.Errorf("%s at %q: tlsAgentOn = %v, want %v", tc.fx.name, tc.version, got, tc.want)
		}
	}
	// Unreadable (not root): nothing can be said against it.
	if os.Geteuid() != 0 {
		dir := t.TempDir()
		writeInstalledCompose(t, dir)
		if err := os.Chmod(filepath.Join(dir, "docker-compose.yml"), 0); err != nil {
			t.Fatal(err)
		}
		if !tlsAgentOn(dir, map[string]string{"BOBRES_VERSION": "0.1.0"}) {
			t.Error("a compose file that cannot be read said no")
		}
		if tlsAgentOn(dir, map[string]string{"BOBRES_VERSION": "0.1.0-rc.1"}) {
			t.Error("rc.1 has no agent, readable file or not")
		}
	}
}

// installs of every kind the predicate sees: the services that are waited for,
// listed by status and left out of restarts follow it, not the image tag alone.
var predicateCases = []struct {
	name     string
	compose  func(t *testing.T, dir string)
	version  string
	hasAgent bool
}{
	// A new CLI over an install of the release before the agent (the downgrade, or a re-run).
	{"current compose, rc.1 images", writeInstalledCompose, "0.1.0-rc.1", false},
	// What a rollback leaves: the old compose file, with whatever .env says.
	{"old compose, rc.1", writeOldCompose, "0.1.0-rc.1", false},
	{"old compose, latest", writeOldCompose, "latest", false},
	{"old compose, ci", writeOldCompose, "ci", false},
	{"old compose, lab build", writeOldCompose, "0.1.0-lab.3", false},
	{"old compose, empty version", writeOldCompose, "", false},
	// A release CLI with a tag that is no release: the agent runs.
	{"current compose, ci", writeInstalledCompose, "ci", true},
	{"current compose, lab build", writeInstalledCompose, "0.1.0-lab.3", true},
	{"current compose, latest", writeInstalledCompose, "latest", true},
	{"current compose, empty version", writeInstalledCompose, "", true},
	{"current compose, rc.2", writeInstalledCompose, "0.1.0-rc.2", true},
}

// Through the real predicate: appServices, dataServices and allServices
// follow it, status judges a `ps` without the agent by it (a stack that has no
// agent is healthy without one, one that has an agent is not), and start waits
// for it exactly then.
func TestServicesFollowTheAgentPredicate(t *testing.T) {
	withRelease(t, "v0.1.0", "")
	noAgent := psWithout(t, healthyPS, "tls-agent")
	for _, tc := range predicateCases {
		t.Run(tc.name, func(t *testing.T) {
			dir := fullInstall(t, true)
			tc.compose(t, dir)
			setVersion(t, dir, tc.version)
			env, _ := readEnvFile(t, dir)

			if got := slices.Contains(appServices(dir, env), "tls-agent"); got != tc.hasAgent {
				t.Errorf("appServices has tls-agent: %v, want %v", got, tc.hasAgent)
			}
			if got := slices.Contains(allServices(dir, env), "tls-agent"); got != tc.hasAgent {
				t.Errorf("allServices has tls-agent: %v, want %v", got, tc.hasAgent)
			}
			if slices.Contains(dataServices(dir, env), "tls-agent") {
				t.Error("dataServices holds the tls-agent")
			}

			// status with a `ps` that has no agent (and a backup service, which fullInstall turns on)
			ps := strings.TrimSuffix(noAgent, "]") + `,{"Service":"backup","State":"running","Health":"healthy"}]`
			o, out, errOut := testOps("", &fakeOps{ps: ps})
			code := o.statusCmd([]string{"--dir", dir})
			if tc.hasAgent && (code != 1 || !strings.Contains(out.String(), "tls-agent     missing")) {
				t.Errorf("status of an install whose agent is missing: %d\n%s%s", code, out, errOut)
			}
			if !tc.hasAgent && (code != 0 || strings.Contains(out.String(), "tls-agent") || !strings.Contains(out.String(), "All services are running")) {
				t.Errorf("status of an install without an agent: %d\n%s%s", code, out, errOut)
			}

			// start: waitHealthy waits for the agent only where there is one (the fake sleep
			// lets the five minutes pass at once)
			f := &fakeOps{ps: ps}
			o, out, errOut = testOps("", f)
			code = o.startCmd([]string{"--dir", dir})
			if tc.hasAgent && (code != 1 || !strings.Contains(errOut.String(), "tls-agent=starting")) {
				t.Errorf("start waited for no agent: %d\n%s%s", code, out, errOut)
			}
			if !tc.hasAgent && (code != 0 || !strings.Contains(out.String(), "All services are healthy")) {
				t.Errorf("start waited for an agent that this install does not have: %d\n%s%s", code, out, errOut)
			}
		})
	}
}

// The recreate list of `config set` names the agent only where it runs: it
// reads the domain and the Zarinpal host, so it must follow a change of them
// (a release CLI with a ci or lab tag runs it), and compose must not be asked
// to start one a rolled-back compose file does not have.
func TestConfigSetRecreatesTheAgentByThePredicate(t *testing.T) {
	withRelease(t, "v0.1.0", "")
	for _, tc := range predicateCases {
		t.Run(tc.name, func(t *testing.T) {
			dir := fullInstall(t, false)
			tc.compose(t, dir)
			setVersion(t, dir, tc.version)
			want := "up -d --no-deps caddy core payments"
			if tc.hasAgent {
				want += " tls-agent"
			}
			for _, set := range [][2]string{{"BOBRES_DOMAIN", "shop.example.com"}, {"BOBRES_ZARINPAL_PUBLIC_URL", "https://pay.example.net"}} {
				f := &fakeDB{ps: func() string { return healthyPS }}
				o, _, errOut := dbOps("", f)
				if code := o.configCmd([]string{"set", set[0], set[1], "--dir", dir}); code != 0 || strings.Join(f.steps, "|") != want {
					t.Errorf("set %s: %d %q (want %q) %s", set[0], code, f.steps, want, errOut)
				}
			}
		})
	}
}

// bobres install over what an older release left: the compose file always
// defines the agent, so the tls profile follows the image version, as the
// backup profile does. Its container is not started for 0.1.0-rc.1 (it would
// restart in a loop), the wait does not include it, and one an earlier release
// left is removed; moving to rc.2 turns it on and waits for it. Where docker
// is installed, compose itself reads the written files.
func TestInstallTurnsTheTLSProfileWithTheVersion(t *testing.T) {
	withRelease(t, "v0.1.0", "")
	// Every service of either release is up, with the agent or without it.
	base := `[{"Service":"core","Health":"healthy"},{"Service":"payments","Health":"healthy"},{"Service":"provisioner","Health":"healthy"},` +
		`{"Service":"bot","Health":"healthy"},{"Service":"backup","Health":"healthy"}`
	without, with := base+`]`, base+`,{"Service":"tls-agent","Health":"healthy"}]`
	composeServices := func(t *testing.T, dir string) []string {
		t.Helper()
		if _, err := exec.LookPath("docker"); err != nil {
			return nil
		}
		cmd := exec.Command("docker", "compose", "-f", "docker-compose.yml", "--env-file", ".env", "config", "--services")
		cmd.Dir = dir
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("docker compose config --services: %v", err)
		}
		return strings.Fields(string(out))
	}
	dir := t.TempDir()

	// A first install at rc.2: the profile is on and the agent is waited for.
	docker := &fakeDocker{health: []string{with}}
	ins, out, errOut := testInstaller("", secrets, docker)
	if code := ins.install(append(baseArgs(dir), "--version", "0.1.0-rc.2")); code != 0 {
		t.Fatalf("rc.2: %d %s", code, errOut)
	}
	if !strings.Contains(out.String(), "See it with  sudo bobres ssl status") {
		t.Errorf("rc.2: the summary does not point to bobres ssl:\n%s", out)
	}
	env, _, _ := readEnv(filepath.Join(dir, ".env"))
	if !hasProfile(env, tlsProfile) {
		t.Fatalf("rc.2: COMPOSE_PROFILES=%q has no tls", env["COMPOSE_PROFILES"])
	}
	if got := composeServices(t, dir); got != nil && !slices.Contains(got, "tls-agent") {
		t.Errorf("rc.2: compose does not run the agent: %v", got)
	}
	for _, c := range docker.calls {
		if contains(c, "rm") {
			t.Errorf("rc.2 removed a container: %v", c)
		}
	}

	// Back to rc.1 (the documented downgrade, or a plain re-run over an rc.1 install): the
	// profile goes off, compose is not asked for the agent, the old container is removed, and
	// the wait is for the four services of that release.
	docker = &fakeDocker{health: []string{without}}
	ins, out, errOut = testInstaller("", nil, docker)
	if code := ins.install([]string{"--dir", dir, "--version", "0.1.0-rc.1", "--yes", "--skip-checks", "--offline"}); code != 0 {
		t.Fatalf("rc.1: %d %s", code, errOut)
	}
	if strings.Contains(out.String(), "sudo bobres ssl status") || !strings.Contains(out.String(), "predate the HTTPS manager") {
		t.Errorf("rc.1: the summary sends the operator to a command that cannot work:\n%s", out)
	}
	env, _, _ = readEnv(filepath.Join(dir, ".env"))
	if hasProfile(env, tlsProfile) || env["BOBRES_VERSION"] != "0.1.0-rc.1" {
		t.Fatalf("rc.1: COMPOSE_PROFILES=%q, version %q", env["COMPOSE_PROFILES"], env["BOBRES_VERSION"])
	}
	if got := composeServices(t, dir); got != nil && slices.Contains(got, "tls-agent") {
		t.Errorf("rc.1: compose would start the agent from an image that cannot run it: %v", got)
	}
	var removed bool
	for _, c := range docker.calls {
		if slices.Contains(c, "rm") && slices.Contains(c, "tls-agent") && slices.Contains(c, "--force") {
			removed = true
		}
	}
	if !removed {
		t.Errorf("the old agent container was not removed: %v", composeVerbs(docker.calls))
	}
	// A re-run that changes nothing leaves the same: no profile, no wait for an agent.
	docker = &fakeDocker{health: []string{without}}
	ins, _, errOut = testInstaller("", nil, docker)
	if code := ins.install([]string{"--dir", dir, "--yes", "--skip-checks", "--offline"}); code != 0 {
		t.Fatalf("rc.1 re-run: %d %s", code, errOut)
	}

	// Update back to rc.2: the profile is on, the agent starts and is waited for.
	docker = &fakeDocker{health: []string{without}}
	ins, _, errOut = testInstaller("", nil, docker)
	if code := ins.install([]string{"--dir", dir, "--version", "0.1.0-rc.2", "--yes", "--skip-checks", "--offline"}); code != 1 ||
		!strings.Contains(errOut.String(), "tls-agent=starting") {
		t.Fatalf("rc.2 without an agent: %d %s", code, errOut)
	}
	docker = &fakeDocker{health: []string{with}}
	ins, _, errOut = testInstaller("", nil, docker)
	if code := ins.install([]string{"--dir", dir, "--version", "0.1.0-rc.2", "--yes", "--skip-checks", "--offline"}); code != 0 {
		t.Fatalf("rc.2 again: %d %s", code, errOut)
	}
	env, _, _ = readEnv(filepath.Join(dir, ".env"))
	if !hasProfile(env, tlsProfile) {
		t.Errorf("rc.2 again: COMPOSE_PROFILES=%q has no tls", env["COMPOSE_PROFILES"])
	}
	if got := composeServices(t, dir); got != nil && !slices.Contains(got, "tls-agent") {
		t.Errorf("rc.2 again: compose does not run the agent: %v", got)
	}
	// Any tag that is no release runs the agent too (the CI install uses `ci`).
	for _, v := range []string{"ci", "0.1.0-lab.3", "latest"} {
		docker = &fakeDocker{health: []string{with}}
		ins, _, errOut = testInstaller("", nil, docker)
		if code := ins.install([]string{"--dir", dir, "--version", v, "--yes", "--skip-checks", "--offline"}); code != 0 {
			t.Fatalf("%s: %d %s", v, code, errOut)
		}
		if env, _, _ = readEnv(filepath.Join(dir, ".env")); !hasProfile(env, tlsProfile) {
			t.Errorf("%s: COMPOSE_PROFILES=%q has no tls", v, env["COMPOSE_PROFILES"])
		}
	}
	// The other profiles are kept as they are.
	env["COMPOSE_PROFILES"] = "debug,tls,backup"
	if err := writeFiles(dir, env, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	docker = &fakeDocker{health: []string{without}}
	ins, _, errOut = testInstaller("", nil, docker)
	if code := ins.install([]string{"--dir", dir, "--version", "0.1.0-rc.1", "--yes", "--skip-checks", "--offline"}); code != 0 {
		t.Fatalf("rc.1 with other profiles: %d %s", code, errOut)
	}
	if env, _, _ = readEnv(filepath.Join(dir, ".env")); strings.Contains(env["COMPOSE_PROFILES"], "tls") || !strings.Contains(env["COMPOSE_PROFILES"], "debug") {
		t.Errorf("COMPOSE_PROFILES=%q: tls must go and debug stay", env["COMPOSE_PROFILES"])
	}
}

// A failing removal of an old agent container is a warning, not a failed install.
func TestInstallWarnsWhenTheOldAgentCannotBeRemoved(t *testing.T) {
	withRelease(t, "v0.1.0", "")
	dir := t.TempDir()
	docker := &failingRm{fakeDocker: fakeDocker{health: []string{`[{"Service":"core","Health":"healthy"},{"Service":"payments","Health":"healthy"},{"Service":"provisioner","Health":"healthy"},{"Service":"bot","Health":"healthy"},{"Service":"backup","Health":"healthy"}]`}}}
	ins, out, errOut := testInstaller("", secrets, &docker.fakeDocker)
	ins.run = docker.run
	if code := ins.install(append(baseArgs(dir), "--version", "0.1.0-rc.1")); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !strings.Contains(errOut.String(), "an old tls-agent container was not removed") || !strings.Contains(out.String(), "All services are healthy") {
		t.Errorf("no warning, or the install failed:\n%s\n%s", out, errOut)
	}
}

type failingRm struct{ fakeDocker }

func (f *failingRm) run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	if contains(args, "rm") {
		return []byte("no such service"), errors.New("exit status 1")
	}
	return f.fakeDocker.run(ctx, dir, args...)
}

// The updater, rollback and restore, through the real predicate: after a
// rollback to the compose file of a release from before the agent the stack
// is waited for without it, whatever version .env names (latest, an empty
// one and a ci or lab tag included, which count as having the agent when the
// compose file does). Before the predicate looked at the file, `latest` made
// the rollback wait five minutes for a service its compose file lacks, and
// exit 1 on a healthy stack.
func TestRollbackToAnOldComposeDoesNotWaitForTheAgent(t *testing.T) {
	withRelease(t, "v1.2.3", "")
	for _, v := range []string{"0.1.0-rc.1", "latest", "ci", "0.1.0-lab.3", ""} {
		t.Run("restored .env "+v, func(t *testing.T) {
			ut := newUpdateTest(t, "")
			setVersion(t, ut.dir, v) // the pre-agent install; its compose file is "compose of 1.2.3"
			// The update's install writes the current compose file (which defines the agent).
			ut.cli.do = func() error {
				env, _, err := readEnv(filepath.Join(ut.dir, ".env"))
				if err != nil {
					return err
				}
				env["BOBRES_VERSION"] = "1.3.0"
				if err := writeAtomic(filepath.Join(ut.dir, ".env"), renderEnv(env), 0o600); err != nil {
					return err
				}
				return writeAtomic(filepath.Join(ut.dir, "docker-compose.yml"), installedCompose(deploy.ComposeFile), 0o644)
			}
			if code := ut.u.updateCmd([]string{"--dir", ut.dir, "--yes"}); code != 0 {
				t.Fatalf("update: %d\n%s\n%s", code, ut.out, ut.errOut)
			}
			if !strings.Contains(ut.out.String(), "All services are healthy") {
				t.Fatalf("the update did not wait for the stack:\n%s", ut.out)
			}

			// The old stack has no agent: its ps lists none, and the rollback is healthy.
			ut.db.ps = func() string { return psWithout(t, healthyPS, "tls-agent") }
			ut.out.Reset()
			ut.errOut.Reset()
			if code := ut.u.rollbackCmd([]string{"--dir", ut.dir, "--yes"}); code != 0 {
				t.Fatalf("rollback: %d\n%s\n%s", code, ut.out, ut.errOut)
			}
			if strings.Contains(ut.errOut.String(), "not healthy") || !strings.Contains(ut.out.String(), "All services are healthy") {
				t.Errorf("the rollback waited for a service the old stack lacks:\n%s\n%s", ut.out, ut.errOut)
			}
			if b := ut.read(t, "docker-compose.yml"); b != "compose of 1.2.3\n" {
				t.Errorf("the old compose file is not back: %q", b)
			}
		})
	}

	// The update that fails its health check rolls back on its own: with the agent
	// missing from `ps`, the new stack is unhealthy; the old one is waited for
	// without an agent and the rollback says it settled.
	ut := newUpdateTest(t, "")
	setVersion(t, ut.dir, "latest")
	ut.cli.do = func() error {
		env, _, err := readEnv(filepath.Join(ut.dir, ".env"))
		if err != nil {
			return err
		}
		env["BOBRES_VERSION"] = "1.3.0"
		if err := writeAtomic(filepath.Join(ut.dir, ".env"), renderEnv(env), 0o600); err != nil {
			return err
		}
		return writeAtomic(filepath.Join(ut.dir, "docker-compose.yml"), installedCompose(deploy.ComposeFile), 0o644)
	}
	ut.db.ps = func() string { return psWithout(t, healthyPS, "tls-agent") }
	if code := ut.u.updateCmd([]string{"--dir", ut.dir, "--yes"}); code != 1 {
		t.Fatalf("update: %d\n%s\n%s", code, ut.out, ut.errOut)
	}
	if !strings.Contains(ut.errOut.String(), "BOBRES 1.3.0 did not become healthy") ||
		!strings.Contains(ut.errOut.String(), "BOBRES runs 1.2.3 again") || strings.Contains(ut.errOut.String(), "did not start") {
		t.Errorf("the new stack lacks an agent and must roll back to the old one, which has none:\n%s", ut.errOut)
	}
}

// A restore stops the data services and starts the stack: with an install
// whose compose file predates the agent it waits for no agent either.
func TestRestoreDoesNotWaitForAnAgentTheInstallLacks(t *testing.T) {
	withRelease(t, "v1.2.3", "")
	dir := restoreInstall(t) // its compose file is "compose of 1.2.3": no agent
	setVersion(t, dir, "latest")
	file := writeBackup(t, installKey, backupKeys(), "DUMP OF THE BACKUP")
	f := &fakeDB{dump: "DUMP OF THE CURRENT DATA", ps: func() string { return psWithout(t, healthyPS, "tls-agent") }}
	o, out, errOut := dbOps("", f)
	if code := o.restoreCmd([]string{"--dir", dir, "--yes", file}); code != 0 {
		t.Fatalf("restore: %d\n%s\n%s", code, out, errOut)
	}
}

// bobres ssl asks core's `ssl` command, which only images from the HTTPS
// manager's release have; in an older core `/app ssl status` would start a
// second core process. On an install that predates it nothing runs in core.
func TestSSLOnAnInstallBeforeTheHTTPSManager(t *testing.T) {
	withRelease(t, "v0.1.0", "")
	for _, tc := range predicateCases {
		if tc.hasAgent {
			continue
		}
		for _, args := range [][]string{{"status"}, {}, {"auto"}, {"ip", "--ip", "93.184.216.34", "--yes"}, {"manual", "--cert", "c.pem", "--key", "k.pem"}} {
			t.Run(tc.name+" "+strings.Join(args, " "), func(t *testing.T) {
				dir := installDir(t)
				tc.compose(t, dir)
				setVersion(t, dir, tc.version)
				f := &fakeSSL{json: coreJSON}
				o, out, errOut := f.ops("")
				o.probeTLS = func(context.Context, string) tlsprobe.Result {
					t.Error("the host's port 443 was looked at")
					return tlsprobe.Result{}
				}
				code := o.sslCmd(append(slices.Clone(args), "--dir", dir))
				if code != 1 {
					t.Fatalf("exit %d, want 1\n%s%s", code, out, errOut)
				}
				if want := "this install predates the HTTPS manager (release " + orDash(tc.version) + "): run `sudo bobres update`"; !strings.Contains(errOut.String(), want) {
					t.Errorf("no %q in\n%s", want, errOut)
				}
				if len(f.runs)+len(f.streams)+len(f.pipes) != 0 {
					t.Errorf("something ran in core: runs %v streams %v pipes %v", f.runs, f.streams, f.pipes)
				}
			})
		}
	}
	// Usage errors and help are still the command's own, whatever the install.
	dir := installDir(t)
	writeOldCompose(t, dir)
	f := &fakeSSL{}
	o, out, errOut := f.ops("")
	if code := o.sslCmd([]string{"help"}); code != 0 || !strings.Contains(out.String(), "usage: bobres ssl") {
		t.Errorf("help: %d %s", code, out)
	}
	if code := o.sslCmd([]string{"bogus", "--dir", dir}); code != 2 {
		t.Errorf("unknown command: %d %s", code, errOut)
	}
	// An install that has the agent is not told it predates anything.
	dir = installDir(t)
	o, _, errOut = f.ops("")
	if code := o.sslCmd([]string{"auto", "--dir", dir}); code != 0 || strings.Contains(errOut.String(), "predates") {
		t.Errorf("a current install: %d %s", code, errOut)
	}
}

// The HTTPS item of the menu says the same, shows none of its choices, and
// runs nothing.
func TestMenuHTTPSItemOnAnInstallBeforeTheHTTPSManager(t *testing.T) {
	withRelease(t, "v0.1.0", "")
	dir := installDir(t)
	writeOldCompose(t, dir)
	setVersion(t, dir, "latest")
	f := &fakeOps{ps: psWithout(t, healthyPS, "tls-agent")}
	// the HTTPS item, Enter to come back, exit
	m, out, errOut, _, _ := testMenu(dir, "17\n\n0\n", f)
	if code := m.loop(); code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(errOut.String(), "this install predates the HTTPS manager (release latest): run `sudo bobres update`") {
		t.Errorf("no message:\n%s", errOut)
	}
	if strings.Contains(out.String(), "Choose an option [0-4]") || strings.Contains(out.String(), "Your own certificate files") {
		t.Errorf("the HTTPS choices were offered:\n%s", out)
	}
	if !strings.Contains(out.String(), "running (7/7 services)") {
		t.Errorf("the menu's state counts an agent the install lacks:\n%s", out)
	}
	if len(f.stream) != 0 {
		t.Errorf("something was run: %v", f.stream)
	}
}

// doctor's HTTPS lines read the gateway's state and core's list of served
// addresses (the output of `ssl status --json`), then add the domain it was
// given; an install that predates the HTTPS manager gets one Skip line and
// nothing is run in core.
func TestHostTLSResults(t *testing.T) {
	withRelease(t, "v0.1.0", "")
	trusted := true
	cert := tlsconf.CertInfo{Issuer: "CN=E7,O=Let's Encrypt", NotBefore: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), NotAfter: time.Date(2026, 12, 30, 0, 0, 0, 0, time.UTC)}
	var probed []string
	mkOps := func(f *fakeSSL, state string) *ops {
		o, _, _ := f.ops("")
		o.gatewayState = func(context.Context) string { return state }
		o.probeTLS = func(_ context.Context, address string) tlsprobe.Result {
			probed = append(probed, address)
			if address == "pay.example.net" {
				return tlsprobe.Result{Error: "tls: internal error"}
			}
			return tlsprobe.Result{Served: true, NameOK: true, Trusted: &trusted, Info: cert}
		}
		return o
	}
	lines := func(rs []Result) string {
		var b strings.Builder
		for _, r := range rs {
			b.WriteString(string(r.Status) + " " + r.Name + " " + r.Detail + "\n")
		}
		return b.String()
	}

	// Core answers: its list (domain and Zarinpal host) is what is probed, in its order; the
	// domain given is already in it.
	dir := installDir(t)
	f := &fakeSSL{json: "WARN something\n" + coreJSON}
	o := mkOps(f, "running")
	rs := hostTLSResults(context.Background(), o, dir, "panel.example.com")
	if len(f.runs) != 1 || coreArgs(f.runs[0]) != "exec -T core /app ssl status --json" {
		t.Errorf("core's list was asked as %v", f.runs)
	}
	if !slices.Equal(probed, []string{"panel.example.com", "pay.example.net"}) {
		t.Errorf("probed %v", probed)
	}
	got := lines(rs)
	for _, want := range []string{"PASS TLS panel.example.com: ", "77 days left", "FAIL TLS zarinpal pay.example.net: no certificate is served for pay.example.net: tls: internal error"} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in\n%s", want, got)
		}
	}

	// Core's list has no domain: the one given goes first.
	probed = nil
	f = &fakeSSL{json: `{"status":{"addresses":[{"address":"93.184.216.34","role":"ip"}]}}`}
	rs = hostTLSResults(context.Background(), mkOps(f, "running"), dir, "shop.example.com")
	if !slices.Equal(probed, []string{"shop.example.com", "93.184.216.34"}) || len(rs) != 2 || rs[0].Name != "TLS" || rs[1].Name != "TLS ip" {
		t.Errorf("probed %v, results %v", probed, rs)
	}
	// Core does not answer (or answers nonsense, or an empty list): .env's names. The domain
	// given is added only when .env has none (a user who cannot read it).
	for _, f := range []*fakeSSL{{runErr: errors.New("service core is not running")}, {json: "not json"}, {json: `{"status":{"addresses":[]}}`}} {
		probed = nil
		hostTLSResults(context.Background(), mkOps(f, "running"), dir, "shop.example.com")
		if !slices.Equal(probed, []string{"panel.example.com"}) {
			t.Errorf("core says %q / %v: probed %v", f.json, f.runErr, probed)
		}
	}
	unreadable := t.TempDir() // no .env to read: only the domain given is known
	writeInstalledCompose(t, unreadable)
	probed = nil
	hostTLSResults(context.Background(), mkOps(&fakeSSL{runErr: errors.New("service core is not running")}, "running"), unreadable, "shop.example.com")
	if !slices.Equal(probed, []string{"shop.example.com"}) {
		t.Errorf("without .env: probed %v", probed)
	}
	// No domain anywhere: one Skip line.
	probed = nil
	bare := t.TempDir()
	writeInstalledCompose(t, bare)
	rs = hostTLSResults(context.Background(), mkOps(&fakeSSL{runErr: errors.New("down")}, "running"), bare, "")
	if len(probed) != 0 || len(rs) != 1 || rs[0].Status != Skip || !strings.Contains(rs[0].Detail, "no domain known") {
		t.Errorf("no domain: %v probed %v", rs, probed)
	}

	// The gateway's state decides first.
	for state, want := range map[string]Status{"restarting": Fail, "exited": Skip, "": Skip} {
		f := &fakeSSL{json: coreJSON}
		probed = nil
		rs = hostTLSResults(context.Background(), mkOps(f, state), dir, "panel.example.com")
		if len(rs) != 1 || rs[0].Status != want || len(f.runs) != 0 || len(probed) != 0 {
			t.Errorf("gateway %q: %v, core runs %v, probed %v", state, rs, f.runs, probed)
		}
	}

	// An install that predates the HTTPS manager: a Skip line, and nothing runs in core
	// or is probed, whichever way it predates it.
	for _, tc := range predicateCases {
		if tc.hasAgent {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			dir := installDir(t)
			tc.compose(t, dir)
			setVersion(t, dir, tc.version)
			f := &fakeSSL{json: coreJSON}
			probed = nil
			rs := hostTLSResults(context.Background(), mkOps(f, "running"), dir, "panel.example.com")
			if len(rs) != 1 || rs[0].Name != "TLS" || rs[0].Status != Skip || !strings.Contains(rs[0].Detail, "HTTPS manager not installed yet") ||
				!strings.Contains(rs[0].Detail, "sudo bobres update") {
				t.Errorf("results: %v", rs)
			}
			if len(f.runs)+len(f.streams)+len(f.pipes) != 0 || len(probed) != 0 {
				t.Errorf("core runs %v %v, probed %v", f.runs, f.streams, probed)
			}
		})
	}
	// A crash-looping gateway is still a failure on such an install.
	dir = installDir(t)
	writeOldCompose(t, dir)
	rs = hostTLSResults(context.Background(), mkOps(&fakeSSL{}, "restarting"), dir, "panel.example.com")
	if len(rs) != 1 || rs[0].Status != Fail {
		t.Errorf("a restarting gateway: %v", rs)
	}
}

// Core is asked for the served addresses only on an install that has the
// agent: in an older core `/app ssl status --json` would start a second core
// process. The two callers check first; this is the last line.
func TestServedAddressesDoNotAskAnOlderCore(t *testing.T) {
	withRelease(t, "v0.1.0", "")
	for _, tc := range predicateCases {
		dir := installDir(t)
		tc.compose(t, dir)
		setVersion(t, dir, tc.version)
		env, _ := readEnvFile(t, dir)
		f := &fakeSSL{json: coreJSON}
		o, _, _ := f.ops("")
		got := o.servedAddresses(context.Background(), dir, env)
		if asked := len(f.runs) > 0; asked != tc.hasAgent {
			t.Errorf("%s: core asked %v (runs %v), want %v", tc.name, asked, f.runs, tc.hasAgent)
		}
		if !tc.hasAgent && !slices.Equal(got, []served{{"panel.example.com", tlsconf.RoleDomain}}) {
			t.Errorf("%s: addresses %v, want .env's domain", tc.name, got)
		}
		if tc.hasAgent && len(got) != 2 {
			t.Errorf("%s: addresses %v, want core's two", tc.name, got)
		}
	}
}

// The gateway's state is the first line of `docker ps --format {{.State}}`.
func TestGatewayStateOf(t *testing.T) {
	for _, tc := range []struct {
		out  string
		err  error
		want string
	}{
		{"running\n", nil, "running"},
		{"restarting", nil, "restarting"},
		{"  exited  \nrunning\n", nil, "exited"},
		{"", nil, ""},
		{"\n", nil, ""},
		{"running\n", errors.New("exit status 1"), ""},
	} {
		if got := gatewayStateOf([]byte(tc.out), tc.err); got != tc.want {
			t.Errorf("gatewayStateOf(%q, %v) = %q, want %q", tc.out, tc.err, got, tc.want)
		}
	}
}

// uninstall --purge removes every volume of the compose project, also the ones
// the install's compose file does not declare: after a rollback to a release
// from before the HTTPS manager, down --volumes leaves tls_request (the request
// for the HTTPS setting, which holds an own certificate's private key),
// tls_result and caddy_admin.
func TestPurgeRemovesEveryVolumeOfTheProject(t *testing.T) {
	const listed = "bobres_pg_data\nbobres_redis_data\nbobres_tls_request\nbobres_tls_result\nbobres_caddy_admin\nbobres_caddy_data\n"
	run := func(t *testing.T, left string, rmErr error, args ...string) (*fakeVolumes, *bytes.Buffer, *bytes.Buffer, int, string) {
		t.Helper()
		dir := installDir(t)
		writeOldCompose(t, dir) // the rolled-back compose file: it declares only pg_data
		f := &fakeVolumes{left: left, rmErr: rmErr}
		o, out, errOut := testOps("", &fakeOps{})
		o.run = f.run
		code := o.uninstallCmd(append([]string{"--dir", dir, "--purge", "--yes"}, args...))
		return f, out, errOut, code, dir
	}

	f, out, errOut, code, dir := run(t, listed, nil)
	if code != 0 {
		t.Fatalf("exit %d: %s%s", code, out, errOut)
	}
	// Every profile is on for the down: a container of a service whose profile is off (the
	// agent of a release the install went back from) goes too, with its volume and network.
	if len(f.calls) != 3 || !strings.HasSuffix(f.calls[0], "--profile * down --remove-orphans --volumes") {
		t.Fatalf("calls %q", f.calls)
	}
	if f.calls[1] != "volume ls --quiet --filter label=com.docker.compose.project=bobres" {
		t.Errorf("the volumes were listed by %q: the label is the project's", f.calls[1])
	}
	if f.calls[2] != "volume rm bobres_pg_data bobres_redis_data bobres_tls_request bobres_tls_result bobres_caddy_admin bobres_caddy_data" {
		t.Errorf("removed by %q", f.calls[2])
	}
	for _, want := range []string{"bobres_tls_request", "bobres_tls_result", "bobres_caddy_admin", "Removed containers, volumes and " + dir} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the output does not say %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("the install directory is still there")
	}

	// Nothing left: no removal, and no talk of volumes down left.
	f, out, _, code, _ = run(t, "", nil)
	if code != 0 || len(f.calls) != 2 || strings.Contains(out.String(), "Also removed") {
		t.Errorf("nothing left: %d %q\n%s", code, f.calls, out)
	}
	// A warning line in the listing is not a volume name.
	f, _, _, code, _ = run(t, "WARNING: a variable is not set. Defaulting to a blank string.\nbobres_tls_request\n\n", nil)
	if code != 0 || len(f.calls) != 3 || f.calls[2] != "volume rm bobres_tls_request" {
		t.Errorf("a warning in the listing: %d %q", code, f.calls)
	}
	// A volume that cannot be removed: the message says so, the directory stays, exit 1.
	_, out, errOut, code, dir = run(t, listed, errors.New("exit status 1"))
	if code != 1 || !strings.Contains(errOut.String(), "docker volume rm") || !strings.Contains(errOut.String(), "still on this server") {
		t.Errorf("a failing removal: %d\n%s%s", code, out, errOut)
	}
	if strings.Contains(out.String(), "Nothing of BOBRES remains") {
		t.Errorf("it says nothing remains:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(dir, ".env")); err != nil {
		t.Errorf("the install directory was removed while data is left: %v", err)
	}
	// Without --purge no volume is touched.
	dir = installDir(t)
	g := &fakeVolumes{left: listed}
	o, out2, _ := testOps("", &fakeOps{})
	o.run = g.run
	if code := o.uninstallCmd([]string{"--dir", dir, "--yes"}); code != 0 || len(g.calls) != 1 || strings.Contains(g.calls[0], "--volumes") {
		t.Errorf("uninstall without --purge: %d %q", code, g.calls)
	}
	if !strings.Contains(out2.String(), "Kept") {
		t.Errorf("output:\n%s", out2)
	}
}

type fakeVolumes struct {
	calls []string
	// left is what `volume ls` prints, after down.
	left  string
	rmErr error
}

func (f *fakeVolumes) run(_ context.Context, _ string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, strings.TrimPrefix(strings.Join(args, " "), strings.Join(composeArgs(), " ")+" "))
	switch {
	case len(args) > 1 && args[0] == "volume" && args[1] == "ls":
		return []byte(f.left), nil
	case len(args) > 1 && args[0] == "volume" && args[1] == "rm":
		return nil, f.rmErr
	}
	return nil, nil
}
