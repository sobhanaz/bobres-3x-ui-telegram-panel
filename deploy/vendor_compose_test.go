package deploy

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/licensesrv"
)

// vendorService is what the checks read of a service in `docker compose
// config --format json` for the vendor stack (deploy/vendor).
type vendorService struct {
	Image       string            `json:"image"`
	User        string            `json:"user"`
	ReadOnly    bool              `json:"read_only"`
	CapAdd      []string          `json:"cap_add"`
	CapDrop     []string          `json:"cap_drop"`
	SecurityOpt []string          `json:"security_opt"`
	Privileged  bool              `json:"privileged"`
	Environment map[string]string `json:"environment"`
	Networks    map[string]any    `json:"networks"`
	Ports       []struct {
		Target    int    `json:"target"`
		Published string `json:"published"`
		Protocol  string `json:"protocol"`
	} `json:"ports"`
	Healthcheck *struct {
		Test []string `json:"test"`
	} `json:"healthcheck"`
	Secrets []struct {
		Source string `json:"source"`
		Target string `json:"target"`
	} `json:"secrets"`
	Volumes []struct {
		Type, Source, Target string
		ReadOnly             bool `json:"read_only"`
	} `json:"volumes"`
}

type vendorProject struct {
	Services map[string]vendorService `json:"services"`
	Networks map[string]struct {
		Internal bool `json:"internal"`
		Ipam     struct {
			Config []struct {
				Subnet string `json:"subnet"`
			} `json:"config"`
		} `json:"ipam"`
	} `json:"networks"`
	Secrets map[string]struct {
		File string `json:"file"`
	} `json:"secrets"`
}

// vendorConfig is docker's own reading of deploy/vendor with its .env.example
// (no daemon is needed); the test is skipped where docker is not installed.
func vendorConfig(t *testing.T) vendorProject {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker is not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "compose", "-f", "vendor/docker-compose.yml",
		"--env-file", "vendor/.env.example", "config", "--format", "json")
	for _, kv := range os.Environ() { // only the .env file counts
		if !strings.HasPrefix(kv, "BOBRES_") && !strings.HasPrefix(kv, "COMPOSE_") && !strings.HasPrefix(kv, "LICENSE_") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("docker compose config: %v", err)
	}
	var p vendorProject
	if err := json.Unmarshal(out, &p); err != nil {
		t.Fatalf("docker compose config: %v", err)
	}
	return p
}

// The license server stack is hardened like the install's: the server runs as
// a non-root user on a read-only filesystem with no capability, the private
// key is a mounted file (never an environment variable), only Caddy has
// published ports, and the server and its database are on internal networks.
func TestVendorComposeIsHardened(t *testing.T) {
	p := vendorConfig(t)
	srv, ok := p.Services["licensesrv"]
	if !ok || len(p.Services) != 3 {
		t.Fatalf("services: %v", p.Services)
	}
	if srv.User != "65532:65532" || !srv.ReadOnly || len(srv.CapAdd) != 0 || strings.Join(srv.CapDrop, ",") != "ALL" ||
		srv.Privileged || !contains(srv.SecurityOpt, "no-new-privileges:true") {
		t.Errorf("licensesrv is not hardened: %+v", srv)
	}
	if srv.Healthcheck == nil || strings.Join(srv.Healthcheck.Test, " ") != "CMD /app --healthcheck" {
		t.Errorf("licensesrv healthcheck: %+v", srv.Healthcheck)
	}
	for name, s := range p.Services {
		if len(s.CapDrop) != 1 || s.CapDrop[0] != "ALL" || !contains(s.SecurityOpt, "no-new-privileges:true") || s.Privileged {
			t.Errorf("%s: capabilities or privileges: %+v", name, s)
		}
		if s.Healthcheck == nil {
			t.Errorf("%s has no healthcheck", name)
		}
		if name != "caddy" && len(s.Ports) > 0 {
			t.Errorf("%s publishes ports %v: only Caddy should", name, s.Ports)
		}
	}
	if len(srv.Networks) != 2 || !hasKey(srv.Networks, "edge") || !hasKey(srv.Networks, "backend") || hasKey(srv.Networks, "public") {
		t.Errorf("licensesrv networks: %v", srv.Networks)
	}
	if got := p.Services["postgres"].Networks; len(got) != 1 || !hasKey(got, "backend") {
		t.Errorf("postgres networks: %v", got)
	}
	for _, n := range []string{"edge", "backend"} {
		if !p.Networks[n].Internal {
			t.Errorf("network %s is not internal", n)
		}
	}

	// Caddy: the only published ports, 80 and 443.
	var ports []string
	for _, pt := range p.Services["caddy"].Ports {
		ports = append(ports, pt.Published+"/"+pt.Protocol)
	}
	if strings.Join(ports, ",") != "80/tcp,443/tcp,443/udp" || !hasKey(p.Services["caddy"].Networks, "public") {
		t.Errorf("caddy ports %v networks %v", ports, p.Services["caddy"].Networks)
	}
	if got := p.Services["caddy"].CapAdd; len(got) != 1 || got[0] != "NET_BIND_SERVICE" {
		t.Errorf("caddy capabilities: %v", got)
	}

	// The key is a file, read-only for the server, and no variable holds key material.
	if len(srv.Secrets) != 1 || srv.Secrets[0].Source != "signing_key" || srv.Secrets[0].Target != "/run/secrets/signing_key" ||
		srv.Environment["BOBRES_LICENSESRV_SIGNING_KEY_FILE"] != "/run/secrets/signing_key" {
		t.Errorf("signing key: secrets %+v, file variable %q", srv.Secrets, srv.Environment["BOBRES_LICENSESRV_SIGNING_KEY_FILE"])
	}
	if p.Secrets["signing_key"].File == "" || strings.Contains(p.Secrets["signing_key"].File, "..") {
		t.Errorf("signing key file: %+v", p.Secrets["signing_key"])
	}
	for name, s := range p.Services {
		for k, v := range s.Environment {
			if strings.Contains(strings.ToLower(k+v), "private_key") || strings.Contains(v, "PRIVATE KEY") {
				t.Errorf("%s: %s holds key material", name, k)
			}
		}
	}

	// Caddy and the server trust each other through the edge network's subnet only.
	subnet := ""
	if c := p.Networks["edge"].Ipam.Config; len(c) == 1 {
		subnet = c[0].Subnet
	}
	if subnet == "" || srv.Environment["BOBRES_LICENSESRV_TRUSTED_PROXIES"] != subnet {
		t.Errorf("trusted proxies %q, edge subnet %q", srv.Environment["BOBRES_LICENSESRV_TRUSTED_PROXIES"], subnet)
	}

	// Caddy is the same pinned image as a customer install's gateway.
	if got, want := p.Services["caddy"].Image, mainCaddyImage(t); got != want || !strings.Contains(got, "@sha256:") {
		t.Errorf("caddy image %q, the install's is %q", got, want)
	}
}

// The server starts with the environment compose gives it: every variable
// is one the server reads, and its values parse (rates, trusted proxies).
func TestVendorComposeGivesTheServerItsSettings(t *testing.T) {
	p := vendorConfig(t)
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); strings.HasPrefix(k, "BOBRES_") {
			t.Setenv(k, "")
		}
	}
	for k, v := range p.Services["licensesrv"].Environment {
		t.Setenv(k, v)
	}
	c, err := licensesrv.LoadConfig(true)
	if err != nil {
		t.Fatalf("the license server does not start with the compose environment: %v", err)
	}
	ipRate, _ := licensesrv.ParseRate(licensesrv.DefaultIPRate)
	keyRate, _ := licensesrv.ParseRate(licensesrv.DefaultKeyRate)
	if c.SigningKeyFile != "/run/secrets/signing_key" || len(c.TrustedProxies) != 1 || c.AllowHTTP ||
		c.IPRate != ipRate || c.KeyRate != keyRate || !strings.Contains(c.DatabaseURL, "@postgres:5432/bobres_license") {
		t.Errorf("config: %+v", c)
	}
}

// Every value the stack needs is in .env.example, with a CHANGE_ME placeholder for each
// secret (the server refuses to start with one), and nothing that looks like key material.
func TestVendorEnvExample(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("vendor", ".env.example"))
	if err != nil {
		t.Fatal(err)
	}
	compose, err := os.ReadFile(filepath.Join("vendor", "docker-compose.yml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, m := range regexp.MustCompile(`\$\{([A-Z_]+):\?`).FindAllStringSubmatch(string(compose), -1) {
		if !regexp.MustCompile(`(?m)^` + m[1] + `=`).MatchString(text) {
			t.Errorf(".env.example does not set %s, which the compose file requires", m[1])
		}
	}
	for _, name := range []string{"POSTGRES_PASSWORD", "DB_LICENSESRV_PASSWORD"} {
		if !regexp.MustCompile(`(?m)^` + name + `=CHANGE_ME`).MatchString(text) {
			t.Errorf("%s is not a CHANGE_ME placeholder", name)
		}
	}
	for _, bad := range []string{"PRIVATE KEY", "private_key", "BOBRES-"} {
		if strings.Contains(text, bad) {
			t.Errorf(".env.example contains %q", bad)
		}
	}
	if !regexp.MustCompile(`(?m)^LICENSE_SIGNING_KEY_FILE=/`).MatchString(text) {
		t.Error("the signing key is not given as an absolute path")
	}
}

// mainCaddyImage is the Caddy reference in the customer compose file.
func mainCaddyImage(t *testing.T) string {
	t.Helper()
	m := regexp.MustCompile(`(?m)^\s+image: (caddy:\S+)$`).FindStringSubmatch(string(ComposeFile))
	if m == nil {
		t.Fatal("no caddy image in docker-compose.yml")
	}
	return m[1]
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func hasKey(m map[string]any, k string) bool { _, ok := m[k]; return ok }
