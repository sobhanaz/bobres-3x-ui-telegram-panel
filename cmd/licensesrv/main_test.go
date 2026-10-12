package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/app"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/health"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/license"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testdb"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
)

func TestMain(m *testing.M) { testdb.Main(m) }

func runCmd(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := run(args, strings.NewReader(stdin), &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestUsage(t *testing.T) {
	t.Setenv("BOBRES_HTTP_ADDR", "")
	if code, _, errOut := runCmd(t, ""); code != 2 || !strings.Contains(errOut, "usage") {
		t.Fatalf("no args: %d %s", code, errOut)
	}
	if code, _, _ := runCmd(t, "", "license"); code != 2 {
		t.Fatalf("unknown command: %d", code)
	}
	if code, out, _ := runCmd(t, "", "version"); code != 0 || !strings.HasPrefix(out, "licensesrv ") {
		t.Fatalf("version: %d %q", code, out)
	}
	if got := os.Getenv("BOBRES_HTTP_ADDR"); got != defaultAddr {
		t.Fatalf("default listen address %q, want %s", got, defaultAddr)
	}
	t.Setenv("BOBRES_DATABASE_URL", "")
	t.Setenv("BOBRES_DATABASE_URL_FILE", "")
	if code, _, errOut := runCmd(t, "", "key", "list"); code != 2 || !strings.Contains(errOut, "BOBRES_DATABASE_URL") {
		t.Fatalf("no database: %d %s", code, errOut)
	}
}

// keyFile writes a throwaway online key the way tools/license keygen does.
func keyFile(t *testing.T, dir, kid string) (string, ed25519.PublicKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]string{"kid": kid, "private_key": base64.StdEncoding.EncodeToString(priv)})
	p := filepath.Join(dir, kid+".key")
	if err := os.WriteFile(p, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return p, pub
}

func newRuntime(t *testing.T, logs *bytes.Buffer) *app.Runtime {
	t.Helper()
	log := slog.New(slog.NewTextHandler(logs, nil))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	mux := http.NewServeMux()
	h := health.New("licensesrv", log)
	h.Register(mux)
	return &app.Runtime{Ctx: ctx, Mux: mux, Health: h, Log: log}
}

// TestServeEndToEnd wires `serve` as the binary does and activates a key
// made with `key create`.
func TestServeEndToEnd(t *testing.T) {
	t.Setenv("BOBRES_DATABASE_URL", testdb.DSN(t))
	t.Setenv("BOBRES_DATABASE_URL_FILE", "")
	keyPath, pub := keyFile(t, t.TempDir(), "srv-e2e")
	t.Setenv("BOBRES_LICENSESRV_SIGNING_KEY_FILE", keyPath)
	t.Setenv("BOBRES_LICENSESRV_TRUSTED_PROXIES", "")
	t.Setenv("BOBRES_LICENSESRV_RATE_IP", "")
	t.Setenv("BOBRES_LICENSESRV_RATE_KEY", "")
	t.Setenv("BOBRES_LICENSESRV_ALLOW_HTTP", "")
	t.Setenv("BOBRES_HTTP_ADDR", "")

	if code, _, errOut := runCmd(t, "", "migrate"); code != 0 {
		t.Fatalf("migrate: %d %s", code, errOut)
	}
	code, key, errOut := runCmd(t, `{"max_servers": 1, "max_active_services": 100, "dashboard_staff_seats": 1}`,
		"key", "create", "-customer", "E2E", "-tier", "trial", "-entitlements", "-", "-days", "14")
	if code != 0 {
		t.Fatalf("key create: %d %s", code, errOut)
	}
	key = strings.TrimSpace(key)
	if code, out, errOut := runCmd(t, "", "key", "list"); code != 0 || !strings.Contains(out, "E2E") {
		t.Fatalf("key list: %d %s %s", code, out, errOut)
	}

	var logs bytes.Buffer
	rt := newRuntime(t, &logs)
	if err := setup(rt); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if !strings.Contains(logs.String(), "does not carry the signing key's public key") {
		t.Fatalf("no warning about the missing .pub:\n%s", logs.String())
	}
	srv := httptest.NewServer(rt.Mux)
	defer srv.Close()

	install := uuid.MustV7().String()
	body, _ := json.Marshal(map[string]string{"key": key, "install_id": install, "domain": "e2e.example", "version": "0.1.0"})
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/activate", bytes.NewReader(body))
	req.Header.Set("X-Forwarded-Proto", "https")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got struct {
		Token string `json:"token"`
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil || resp.StatusCode != http.StatusOK || got.Token == "" {
		t.Fatalf("activate: %d %+v %v", resp.StatusCode, got, err)
	}
	// Signed by the online key and bound to the install, as core checks it.
	tok, err := license.Verify(license.KeyRing{"srv-e2e": pub}, got.Token,
		license.Options{Domain: "e2e.example", InstallID: install, RequireBound: true})
	if err != nil || tok.KeyID != "srv-e2e" || tok.Customer != "E2E" || tok.Tier != license.TierTrial || tok.LicenseEndsAt == nil {
		t.Fatalf("token: %+v %v", tok, err)
	}

	ready, err := srv.Client().Get(srv.URL + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	ready.Body.Close()
	if ready.StatusCode != http.StatusOK {
		t.Fatalf("readyz: %d", ready.StatusCode)
	}
}

func TestServeRefusesBadSetup(t *testing.T) {
	t.Setenv("BOBRES_DATABASE_URL", testdb.DSN(t))
	t.Setenv("BOBRES_DATABASE_URL_FILE", "")
	t.Setenv("BOBRES_LICENSESRV_TRUSTED_PROXIES", "")
	t.Setenv("BOBRES_LICENSESRV_RATE_IP", "")
	t.Setenv("BOBRES_LICENSESRV_RATE_KEY", "")
	t.Setenv("BOBRES_LICENSESRV_ALLOW_HTTP", "")

	t.Setenv("BOBRES_LICENSESRV_SIGNING_KEY_FILE", "")
	if err := setup(newRuntime(t, &bytes.Buffer{})); err == nil || !strings.Contains(err.Error(), "SIGNING_KEY_FILE") {
		t.Fatalf("no key: %v", err)
	}
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	inRepo, _ := keyFile(t, repo, "srv-2026")
	t.Setenv("BOBRES_LICENSESRV_SIGNING_KEY_FILE", inRepo)
	if err := setup(newRuntime(t, &bytes.Buffer{})); err == nil || !strings.Contains(err.Error(), "git checkout") {
		t.Fatalf("key in a checkout: %v", err)
	}
}
