package deploy

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tlsagent"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tlsconf"
)

// mount is one entry of a service's volumes: a named volume or a bind mount
// of the install directory.
type mount struct {
	source, target string
	readOnly       bool
}

// serviceBlock returns the lines of one service in docker-compose.yml, after
// its "  name:" line.
func serviceBlock(t *testing.T, name string) []string {
	t.Helper()
	lines := strings.Split(string(ComposeFile), "\n")
	start := -1
	inServices := false
	for i, line := range lines {
		if line == "services:" {
			inServices = true
			continue
		}
		if inServices && line == "  "+name+":" {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatalf("no service %q in docker-compose.yml", name)
	}
	var block []string
	for _, line := range lines[start+1:] {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(line, "    ") && !strings.HasPrefix(trimmed, "#") {
			break // the next service, or the top-level networks and volumes
		}
		block = append(block, line)
	}
	return block
}

// serviceScalar reads a service's own "key: value" line ("" when it has none).
func serviceScalar(t *testing.T, name, key string) string {
	t.Helper()
	for _, line := range serviceBlock(t, name) {
		if v, ok := strings.CutPrefix(line, "    "+key+": "); ok {
			return strings.Trim(strings.TrimSpace(v), `"`)
		}
	}
	return ""
}

// serviceMounts reads a service's volumes, in the block style
// ("      - ./tls:/tls:ro") or the flow style ("volumes: [redis_data:/data]").
func serviceMounts(t *testing.T, name string) []mount {
	t.Helper()
	var specs []string
	block := serviceBlock(t, name)
	for i, line := range block {
		if v, ok := strings.CutPrefix(line, "    volumes: "); ok {
			v = strings.Trim(strings.TrimSpace(v), "[]")
			for _, s := range strings.Split(v, ",") {
				specs = append(specs, strings.TrimSpace(s))
			}
			break
		}
		if line != "    volumes:" {
			continue
		}
		for _, l := range block[i+1:] {
			trimmed := strings.TrimSpace(l)
			if strings.HasPrefix(trimmed, "#") {
				continue
			}
			spec, ok := strings.CutPrefix(trimmed, "- ")
			if !ok || !strings.HasPrefix(l, "      ") {
				break
			}
			specs = append(specs, spec)
		}
		break
	}
	var out []mount
	for _, s := range specs {
		parts := strings.Split(s, ":")
		if len(parts) < 2 {
			t.Fatalf("service %s: volume %q not understood", name, s)
		}
		m := mount{source: parts[0], target: parts[1]}
		switch {
		case len(parts) == 2:
		case len(parts) == 3 && parts[2] == "ro":
			m.readOnly = true
		default:
			t.Fatalf("service %s: volume %q not understood", name, s)
		}
		out = append(out, m)
	}
	return out
}

// composeServices are the services of docker-compose.yml.
func composeServices() []string {
	var out []string
	inServices := false
	for _, line := range strings.Split(string(ComposeFile), "\n") {
		switch {
		case line == "services:":
			inServices = true
		case inServices && line != "" && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "#"):
			inServices = false
		case inServices && regexp.MustCompile(`^  [a-z][a-z-]*:$`).MatchString(line):
			out = append(out, strings.TrimSuffix(strings.TrimSpace(line), ":"))
		}
	}
	return out
}

// mountedBy lists the services that mount source, with how.
func mountedBy(t *testing.T, source string) map[string]mount {
	t.Helper()
	out := map[string]mount{}
	for _, svc := range composeServices() {
		for _, m := range serviceMounts(t, svc) {
			if m.source == source {
				out[svc] = m
			}
		}
	}
	return out
}

func names(m map[string]mount) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Who can reach what in the HTTPS design. Caddy's admin socket is root on
// the gateway: only Caddy and the tls-agent mount its volume. The install's
// tls/ directory holds the files Caddy loads: only the tls-agent writes it.
// Core, which faces the dashboard's users and holds the database, mounts
// none of that: it talks to the agent through two small volumes, one it
// writes and one it only reads.
func TestComposeHTTPSMounts(t *testing.T) {
	if got, want := names(mountedBy(t, "caddy_admin")), []string{"caddy", "tls-agent"}; !reflect.DeepEqual(got, want) {
		t.Errorf("caddy_admin (Caddy's admin socket) is mounted by %v, want %v", got, want)
	}
	for svc, m := range mountedBy(t, "caddy_admin") {
		if m.target != "/run/caddy" || m.readOnly {
			t.Errorf("%s mounts caddy_admin at %s (read-only %v): the socket lives in /run/caddy and the agent connects to it", svc, m.target, m.readOnly)
		}
	}

	tlsMounts := mountedBy(t, "./tls")
	if got, want := names(tlsMounts), []string{"caddy", "tls-agent"}; !reflect.DeepEqual(got, want) {
		t.Errorf("./tls is mounted by %v, want %v", got, want)
	}
	if m := tlsMounts["caddy"]; m.target != tlsconf.Dir || !m.readOnly {
		t.Errorf("caddy mounts ./tls at %s (read-only %v), want %s read-only", m.target, m.readOnly, tlsconf.Dir)
	}
	if m := tlsMounts["tls-agent"]; m.target != tlsconf.Dir || m.readOnly {
		t.Errorf("the tls-agent mounts ./tls at %s (read-only %v), want %s read-write: it is the only writer", m.target, m.readOnly, tlsconf.Dir)
	}

	// Caddy's main Caddyfile is a directory mount: a bind-mounted file keeps
	// its old inode when the installer replaces it by rename. The agent reads
	// the same directory (read-only) to post the file to Caddy.
	caddyMounts := mountedBy(t, "./caddy")
	if got, want := names(caddyMounts), []string{"caddy", "tls-agent"}; !reflect.DeepEqual(got, want) {
		t.Errorf("./caddy is mounted by %v, want %v", got, want)
	}
	for svc, m := range caddyMounts {
		if !m.readOnly {
			t.Errorf("%s mounts ./caddy read-write", svc)
		}
	}
	if got := caddyMounts["caddy"].target; got != filepath.Dir(tlsagent.DefaultSourceFile) {
		t.Errorf("caddy mounts ./caddy at %s, but the agent tells Caddy its config file is %s", got, tlsagent.DefaultSourceFile)
	}
	if got := caddyMounts["tls-agent"].target; got != filepath.Dir(tlsagent.DefaultCaddyfile) {
		t.Errorf("the tls-agent mounts ./caddy at %s, but it reads %s", got, tlsagent.DefaultCaddyfile)
	}
	for _, svc := range composeServices() {
		for _, m := range serviceMounts(t, svc) {
			if strings.HasSuffix(m.source, "Caddyfile") {
				t.Errorf("%s mounts the single file %s: the installer replaces it by rename and the container keeps the old one", svc, m.source)
			}
		}
	}

	// The request volume: core writes, the agent reads. The result volume:
	// the agent writes, core reads.
	request := mountedBy(t, "tls_request")
	if got, want := names(request), []string{"core", "tls-agent"}; !reflect.DeepEqual(got, want) {
		t.Errorf("tls_request is mounted by %v, want %v", got, want)
	}
	if m := request["core"]; m.target != tlsconf.CoreRequestDir || m.readOnly {
		t.Errorf("core mounts tls_request at %s (read-only %v), want %s read-write", m.target, m.readOnly, tlsconf.CoreRequestDir)
	}
	if m := request["tls-agent"]; m.target != tlsconf.AgentRequestDir || !m.readOnly {
		t.Errorf("the tls-agent mounts tls_request at %s (read-only %v), want %s read-only", m.target, m.readOnly, tlsconf.AgentRequestDir)
	}
	result := mountedBy(t, "tls_result")
	if got, want := names(result), []string{"core", "tls-agent"}; !reflect.DeepEqual(got, want) {
		t.Errorf("tls_result is mounted by %v, want %v", got, want)
	}
	if m := result["core"]; m.target != tlsconf.CoreResultDir || !m.readOnly {
		t.Errorf("core mounts tls_result at %s (read-only %v), want %s read-only", m.target, m.readOnly, tlsconf.CoreResultDir)
	}
	if m := result["tls-agent"]; m.target != tlsconf.AgentResultDir || m.readOnly {
		t.Errorf("the tls-agent mounts tls_result at %s (read-only %v), want %s read-write", m.target, m.readOnly, tlsconf.AgentResultDir)
	}

	// Core in particular: nothing of Caddy's, and of the install's files only the directory of the
	// dashboard's update requests (TestComposeUpdateRequestMount).
	for _, m := range serviceMounts(t, "core") {
		if m.source == "caddy_admin" || (strings.HasPrefix(m.source, "./") && m.source != updateRequestDir) {
			t.Errorf("core mounts %s: it must not reach Caddy's admin socket or the install's files", m.source)
		}
	}

	// Every named volume in use is declared, and every declared one is used.
	declared := map[string]bool{}
	inVolumes := false
	for _, line := range strings.Split(string(ComposeFile), "\n") {
		switch {
		case line == "volumes:":
			inVolumes = true
		case inVolumes && regexp.MustCompile(`^  [a-z_]+: \{\}$`).MatchString(line):
			declared[strings.TrimSuffix(strings.Fields(line)[0], ":")] = true
		}
	}
	used := map[string]bool{}
	for _, svc := range composeServices() {
		for _, m := range serviceMounts(t, svc) {
			if !strings.HasPrefix(m.source, ".") {
				used[m.source] = true
			}
		}
	}
	if !reflect.DeepEqual(declared, used) {
		t.Errorf("named volumes declared %v, used %v", declared, used)
	}
}

// The tls-agent can change what the gateway serves, so it is as small as it
// can be: no network, no secret, no capability, and no more rights than it
// needs on the files.
func TestComposeTLSAgentIsIsolated(t *testing.T) {
	if got := serviceScalar(t, "tls-agent", "network_mode"); got != "none" {
		t.Errorf("tls-agent network_mode %q, want none", got)
	}
	for _, line := range serviceBlock(t, "tls-agent") {
		if strings.HasPrefix(line, "    networks:") || strings.HasPrefix(line, "    ports:") || strings.HasPrefix(line, "    extra_hosts:") {
			t.Errorf("tls-agent has %q: it has no network", strings.TrimSpace(line))
		}
	}
	if got := serviceScalar(t, "tls-agent", "user"); got != "65532:65532" {
		t.Errorf("tls-agent user %q, want 65532:65532", got)
	}
	if got := serviceScalar(t, "tls-agent", "command"); got != `["tls-agent"]` {
		t.Errorf("tls-agent command %s", got)
	}
	if got := serviceScalar(t, "tls-agent", "mem_limit"); got != "64m" {
		t.Errorf("tls-agent mem_limit %q, want 64m", got)
	}
	// Same image as core, so the same digest pin.
	img, core := serviceScalar(t, "tls-agent", "image"), serviceScalar(t, "core", "image")
	if img == "" || img != core {
		t.Errorf("tls-agent image %q, core image %q: the agent runs core's image", img, core)
	}
	// The agent keeps the shared hardening (read-only root, no capability, no new privileges).
	if !strings.Contains(strings.Join(serviceBlock(t, "tls-agent"), "\n"), "<<: *app") {
		t.Error("tls-agent does not merge *app: read_only, cap_drop and no-new-privileges")
	}
	// Its environment is the log settings and the install's two names (it refuses a request for any
	// other): no token, no key.
	for k := range composeEnv(t, "tls-agent", installEnv(nil)) {
		switch k {
		case "BOBRES_ENV", "BOBRES_LOG_LEVEL", "BOBRES_LOG_FORMAT", "BOBRES_DOMAIN", "BOBRES_ZARINPAL_HOST":
		default:
			t.Errorf("tls-agent gets %s", k)
		}
	}
}

// The agent pins the install's names: it gets BOBRES_DOMAIN and BOBRES_ZARINPAL_HOST from .env exactly
// as core does. The domain is required (compose refuses an install without it: an agent without it
// answers every request invalid); the Zarinpal host is optional.
func TestComposeTLSAgentKnowsTheInstallsNames(t *testing.T) {
	set := backupInstallEnv(map[string]string{"BOBRES_DOMAIN": "shop.example.com", "BOBRES_ZARINPAL_HOST": "pay.example.net"})
	unset := backupInstallEnv(map[string]string{"BOBRES_DOMAIN": "shop.example.com", "BOBRES_ZARINPAL_HOST": ""})
	agent := composeEnv(t, "tls-agent", set)
	if agent["BOBRES_DOMAIN"] != "shop.example.com" || agent["BOBRES_ZARINPAL_HOST"] != "pay.example.net" {
		t.Errorf("tls-agent's names are %q and %q", agent["BOBRES_DOMAIN"], agent["BOBRES_ZARINPAL_HOST"])
	}
	if got := composeEnv(t, "tls-agent", unset)["BOBRES_ZARINPAL_HOST"]; got != "" {
		t.Errorf("without a Zarinpal host the tls-agent's BOBRES_ZARINPAL_HOST is %q", got)
	}
	for _, env := range []map[string]string{set, unset} {
		core, agent := composeEnv(t, "core", env), composeEnv(t, "tls-agent", env)
		for _, k := range []string{"BOBRES_DOMAIN", "BOBRES_ZARINPAL_HOST"} {
			if core[k] != agent[k] {
				t.Errorf("%s: core %q, tls-agent %q: they must be the same", k, core[k], agent[k])
			}
		}
	}
	block := strings.Join(serviceBlock(t, "tls-agent"), "\n")
	for _, want := range []string{"BOBRES_DOMAIN: ${BOBRES_DOMAIN:?set BOBRES_DOMAIN}", "BOBRES_ZARINPAL_HOST: ${BOBRES_ZARINPAL_HOST:-}"} {
		if !strings.Contains(block, want) {
			t.Errorf("tls-agent lost %q", want)
		}
	}
	// Docker's own reading agrees (where docker is installed; no daemon is needed).
	if _, err := exec.LookPath("docker"); err == nil {
		for _, env := range []map[string]string{set, unset} {
			if got, want := dockerComposeEnv(t, "tls-agent", env), composeEnv(t, "tls-agent", env); !reflect.DeepEqual(got, want) {
				t.Errorf("docker compose config gives the tls-agent\n%v\ncomposeEnv\n%v", got, want)
			}
		}
	}
}

// Caddy runs as root with group 65532 (the files the agent writes are 0640
// 65532:65532, and the admin socket 0220 is for that group), from an image
// pinned by version and digest, at a version that issues IP certificates.
func TestComposeCaddyService(t *testing.T) {
	if got := serviceScalar(t, "caddy", "user"); got != "0:65532" {
		t.Errorf("caddy user %q, want 0:65532", got)
	}
	img := serviceScalar(t, "caddy", "image")
	m := regexp.MustCompile(`^caddy:(\d+)\.(\d+)\.(\d+)-alpine@sha256:[0-9a-f]{64}$`).FindStringSubmatch(img)
	if m == nil {
		t.Fatalf("caddy image %q: want caddy:X.Y.Z-alpine@sha256:<index digest>", img)
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	patch, _ := strconv.Atoi(m[3])
	if major != 2 || minor < 11 || (minor == 11 && patch < 3) {
		t.Errorf("caddy %s.%s.%s: Let's Encrypt IP address certificates need 2.11.3 or later", m[1], m[2], m[3])
	}
	block := strings.Join(serviceBlock(t, "caddy"), "\n")
	for _, want := range []string{"read_only: true", "cap_drop: [ALL]", "cap_add: [NET_BIND_SERVICE]", `"no-new-privileges:true"`} {
		if !strings.Contains(block, want) {
			t.Errorf("caddy lost %s", want)
		}
	}
}

// Core's environment gains the Zarinpal host (it hands it to the agent);
// nothing else of the gateway.
func TestComposeCoreKnowsTheZarinpalHost(t *testing.T) {
	env := backupInstallEnv(map[string]string{"BOBRES_ZARINPAL_HOST": "pay.example.net"})
	if got := composeEnv(t, "core", env)["BOBRES_ZARINPAL_HOST"]; got != "pay.example.net" {
		t.Errorf("core's BOBRES_ZARINPAL_HOST is %q", got)
	}
	if got := composeEnv(t, "core", backupInstallEnv(nil))["BOBRES_ZARINPAL_HOST"]; got != "" {
		t.Errorf("without the line core's BOBRES_ZARINPAL_HOST is %q", got)
	}
}

// The images are FROM scratch, so a new named volume gets its owner from the
// image's directory at the mount point: /request, /result and /tls-result
// must be there, owned by 65532, or core cannot write its request.
func TestDockerfileMakesTheVolumeMountPoints(t *testing.T) {
	b, err := os.ReadFile("Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	final := string(b)
	if i := strings.LastIndex(final, "\nFROM scratch"); i >= 0 {
		final = final[i:]
	} else {
		t.Fatal("no scratch stage in deploy/Dockerfile")
	}
	for _, dir := range []string{tlsconf.CoreRequestDir, tlsconf.AgentResultDir, tlsconf.CoreResultDir} {
		re := regexp.MustCompile(`(?m)^COPY --from=\S+ --chown=65532:65532 (--chmod=0?[0-7]{3} )?` + regexp.QuoteMeta(dir) + ` ` + regexp.QuoteMeta(dir) + `$`)
		if !re.MatchString(final) {
			t.Errorf("the final image does not create %s owned by 65532:65532", dir)
		}
	}
	if !strings.Contains(string(b), "install -d -o 65532 -g 65532") {
		t.Error("the runtime-base stage does not create the mount points")
	}
}

// The static Caddyfile and the code agree on the names: the imports are the
// files the agent writes, the sites it renders import the snippet the
// Caddyfile defines (before it imports them), and the admin socket is the
// one the agent connects to.
func TestCaddyfileMatchesTheAgent(t *testing.T) {
	text := string(Caddyfile)
	global := strings.Index(text, "\n{\n")
	if global < 0 {
		t.Fatal("the Caddyfile has no global options block")
	}
	closeGlobal := strings.Index(text[global+1:], "\n}\n")
	if closeGlobal < 0 {
		t.Fatal("the Caddyfile's global options block is not closed")
	}
	block := text[global : global+2+closeGlobal] // up to and including the last line's newline
	if !strings.Contains(block, "import "+tlsconf.GlobalPath+"\n") {
		t.Errorf("the global options block does not import %s", tlsconf.GlobalPath)
	}
	if !strings.Contains(block, "admin unix/"+tlsagent.DefaultAdminSocket+"|0220\n") {
		t.Errorf("the global options block does not put the admin API on %s with mode 0220", tlsagent.DefaultAdminSocket)
	}
	if strings.Contains(text, "admin off") || strings.Contains(text, "admin localhost") {
		t.Error("the admin API must be on the unix socket only")
	}
	snippet := strings.Index(text, "\n("+tlsconf.SiteSnippet+") {\n")
	sites := strings.Index(text, "\nimport "+tlsconf.SitesPath+"\n")
	if snippet < 0 || sites < 0 || snippet > sites {
		t.Errorf("the Caddyfile must define (%s) before it imports %s (snippet at %d, import at %d)", tlsconf.SiteSnippet, tlsconf.SitesPath, snippet, sites)
	}
	if strings.Contains(text, "{$BOBRES_DOMAIN}") {
		t.Error("the site addresses come from /tls/sites.caddy now, not from BOBRES_DOMAIN")
	}
	// The agent posts this file to Caddy as /etc/caddy/Caddyfile.
	if filepath.Base(tlsagent.DefaultCaddyfile) != "Caddyfile" || filepath.Base(tlsagent.DefaultSourceFile) != "Caddyfile" {
		t.Error("the agent and Caddy name the main file differently")
	}
}

// Docker's own reading of the file agrees with the one above (where docker
// is installed; no daemon is needed).
func TestDockerComposeReadsTheHTTPSMountsTheSame(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker is not installed")
	}
	dir, err := filepath.EvalSymlinks(t.TempDir()) // docker prints real paths
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docker-compose.yml"), ComposeFile, 0o600); err != nil {
		t.Fatal(err)
	}
	var dotenv strings.Builder
	for k, v := range backupInstallEnv(nil) {
		dotenv.WriteString(k + "=" + v + "\n")
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(dotenv.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "compose", "-f", "docker-compose.yml", "--env-file", ".env", "config", "--format", "json")
	cmd.Dir = dir
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "BOBRES_") && !strings.HasPrefix(kv, "COMPOSE_") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("docker compose config: %v", err)
	}
	var project struct {
		Services map[string]struct {
			User        string `json:"user"`
			NetworkMode string `json:"network_mode"`
			Volumes     []struct {
				Type     string `json:"type"`
				Source   string `json:"source"`
				Target   string `json:"target"`
				ReadOnly bool   `json:"read_only"`
			} `json:"volumes"`
		} `json:"services"`
	}
	if err := json.Unmarshal(out, &project); err != nil {
		t.Fatal(err)
	}
	for _, svc := range []string{"caddy", "core", "tls-agent"} {
		var got []mount
		for _, v := range project.Services[svc].Volumes {
			src := v.Source
			if v.Type == "bind" {
				rel, err := filepath.Rel(dir, src)
				if err != nil {
					t.Fatalf("%s: %v", src, err)
				}
				src = "./" + rel
			}
			got = append(got, mount{src, v.Target, v.ReadOnly})
		}
		want := serviceMounts(t, svc)
		less := func(a []mount) func(i, j int) bool {
			return func(i, j int) bool { return a[i].target < a[j].target }
		}
		sort.Slice(got, less(got))
		sort.Slice(want, less(want))
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: docker compose reads the volumes as\n%v\nthe test as\n%v", svc, got, want)
		}
	}
	if s := project.Services["tls-agent"]; s.NetworkMode != "none" || s.User != "65532:65532" {
		t.Errorf("tls-agent: docker compose reads network_mode %q, user %q", s.NetworkMode, s.User)
	}
	if s := project.Services["caddy"]; s.User != "0:65532" {
		t.Errorf("caddy: docker compose reads user %q", s.User)
	}
}

// The tls-agent is behind the "tls" Compose profile, which `bobres install`
// turns on with the image version (the core image of 0.1.0-rc.1 has no
// `tls-agent` command: the service would restart for ever there). The other
// services start without it and wait for nothing of it, and the template turns
// the profile on for a manual install.
func TestComposeTLSAgentIsBehindItsProfile(t *testing.T) {
	if got := serviceScalar(t, "tls-agent", "profiles"); got != "[tls]" {
		t.Errorf("tls-agent profiles %q, want [tls]", got)
	}
	for _, svc := range []string{"caddy", "postgres", "redis", "core", "bot", "payments", "provisioner"} {
		if p := serviceScalar(t, svc, "profiles"); p != "" {
			t.Errorf("%s is in the profiles %s: it must start without any", svc, p)
		}
		for _, line := range serviceBlock(t, svc) {
			if !strings.HasPrefix(strings.TrimSpace(line), "#") && strings.Contains(line, "tls-agent") {
				t.Errorf("%s has %q: the stack must start without the agent", svc, strings.TrimSpace(line))
			}
		}
	}
	if !regexp.MustCompile(`(?m)^COMPOSE_PROFILES=.*\btls\b`).Match(EnvExample) || !regexp.MustCompile(`(?m)^COMPOSE_PROFILES=.*\bbackup\b`).Match(EnvExample) {
		t.Error(".env.example does not turn the backup and tls profiles on")
	}

	// Where docker is installed, compose agrees (no daemon is needed): without the profile
	// there is no tls-agent, with it there is, and the other services are the same.
	if _, err := exec.LookPath("docker"); err != nil {
		return
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "docker-compose.yml"), ComposeFile, 0o600); err != nil {
		t.Fatal(err)
	}
	var dotenv strings.Builder
	for k, v := range installEnv(nil) {
		if k != "COMPOSE_PROFILES" {
			dotenv.WriteString(k + "=" + v + "\n")
		}
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(dotenv.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	services := func(args ...string) []string {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, "docker", append([]string{"compose", "-f", "docker-compose.yml", "--env-file", ".env"}, append(args, "config", "--services")...)...) //nolint:gosec // a fixed program, with this test's own arguments
		cmd.Dir = dir
		for _, kv := range os.Environ() {
			if !strings.HasPrefix(kv, "BOBRES_") && !strings.HasPrefix(kv, "COMPOSE_") {
				cmd.Env = append(cmd.Env, kv)
			}
		}
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("docker compose config --services %v: %v", args, err)
		}
		list := strings.Fields(string(out))
		sort.Strings(list)
		return list
	}
	off, on := services(), services("--profile", "tls")
	if want := []string{"bot", "caddy", "core", "payments", "postgres", "provisioner", "redis"}; !reflect.DeepEqual(off, want) {
		t.Errorf("without the profiles: %v, want %v", off, want)
	}
	if want := append(append([]string(nil), off...), "tls-agent"); !reflect.DeepEqual(on, want) {
		t.Errorf("with the tls profile: %v, want %v", on, want)
	}
}
