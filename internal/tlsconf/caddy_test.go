package tlsconf

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// mainCaddyfile stands in for the static Caddyfile when the deploy copy is
// not there yet: the global block with its import, the shared site snippet
// (with a matcher and handles like the real one), and the sites import.
const mainCaddyfile = `{
	admin unix//run/caddy/admin.sock|0220
	servers {
		protocols h1 h2 h3
	}
	import /tls/global.caddy
}

(bobres_site) {
	encode zstd gzip
	header {
		-Server
	}
	@zarinpalHostOther {
		expression ` + "`{host} == \"{$BOBRES_ZARINPAL_HOST}\"`" + `
		not path /pay/* /webhooks/zarinpal /webhooks/idpay /webhooks/nextpay /webhooks/aqayepardakht
	}
	handle @zarinpalHostOther {
		respond "Not found" 404
	}
	handle /admin* {
		reverse_proxy core:8080
	}
	@payments path /webhooks/* /pay/*
	handle @payments {
		reverse_proxy payments:8080
	}
	handle {
		respond "Not found" 404
	}
}

import /tls/sites.caddy
`

// With BOBRES_CADDY set to a caddy binary (2.11.3 or later), every mode's
// files are checked by Caddy itself (caddy validate), with the main
// Caddyfile from deploy/caddy/ when it exists. Without it the test is
// skipped: CI validates the same way in the Caddy image.
func TestCaddyAcceptsEveryMode(t *testing.T) {
	bin := os.Getenv("BOBRES_CADDY")
	if bin == "" {
		t.Skip("BOBRES_CADDY is not set")
	}
	main := mainCaddyfile
	if b, err := os.ReadFile(filepath.Join("..", "..", "deploy", "caddy", "Caddyfile")); err == nil {
		main = string(b)
	}
	now := time.Now()
	ca := newCA(t, "BOBRES Test Root", now)
	leaf := ca.leaf(t, now, []string{"panel.example.com"}, nil)
	for _, tc := range renderCases() {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			fix := func(s string) string { return strings.ReplaceAll(s, Dir+"/", dir+"/") }
			f, err := Render(tc.st)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(dir, CertsDir), 0o750); err != nil {
				t.Fatal(err)
			}
			pair := leaf
			if IsIP(tc.st.Domain) { // manual mode's pair must name the address (IPv6 included)
				pair = ca.leaf(t, now, nil, []string{tc.st.Domain})
			}
			files := map[string]string{GlobalFile: fix(f.Global), SitesFile: fix(f.Sites), "Caddyfile": fix(main)}
			if f.Manual {
				files[filepath.Join(CertsDir, CertFile)] = string(pair.certPEM)
				files[filepath.Join(CertsDir, KeyFile)] = string(pair.keyPEM)
			}
			for name, content := range files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command(bin, "validate", "--config", filepath.Join(dir, "Caddyfile"), "--adapter", "caddyfile") //nolint:gosec // a test binary
			cmd.Env = append(os.Environ(), "BOBRES_ZARINPAL_HOST="+tc.st.ZarinpalHost, "XDG_DATA_HOME="+dir, "XDG_CONFIG_HOME="+dir)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("caddy validate: %v\n%s\n--- global\n%s\n--- sites\n%s", err, out, f.Global, f.Sites)
			}
		})
	}
}
