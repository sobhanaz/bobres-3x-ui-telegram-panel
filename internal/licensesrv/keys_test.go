package licensesrv

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/license"
)

// writeKeyFile writes a throwaway key in tools/license keygen's format.
func writeKeyFile(t *testing.T, dir, kid string, mode os.FileMode) (string, ed25519.PublicKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(keyFile{KeyID: kid, PrivateKey: base64.StdEncoding.EncodeToString(priv)})
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, kid+".key")
	if err := os.WriteFile(p, raw, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
	return p, pub
}

func TestLoadSigner(t *testing.T) {
	dir := t.TempDir()
	path, pub := writeKeyFile(t, dir, "srv-2026", 0o600)
	var warnings []string
	s, err := LoadSigner(path, func(m string) { warnings = append(warnings, m) })
	if err != nil {
		t.Fatal(err)
	}
	if s.KeyID != "srv-2026" || !s.Public().Equal(pub) || len(warnings) != 0 {
		t.Fatalf("signer %s, warnings %v", s.KeyID, warnings)
	}
	// What it signs verifies with the matching .pub, as installs check it.
	now := time.Now().UTC()
	tok, err := s.Sign(license.Token{LicenseID: "lic_x", Domain: "a.example", Tier: license.TierPro, IssuedAt: now, ExpiresAt: now.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	ring, err := license.ReadKeyRing(os.DirFS(writePub(t, "srv-2026", pub)))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := license.Parse(ring, tok); err != nil || got.KeyID != "srv-2026" {
		t.Fatalf("token from the online key: %v %+v", err, got)
	}

	// Readable by others: works, with a warning.
	loose, _ := writeKeyFile(t, dir, "srv-loose", 0o644)
	warnings = nil
	if _, err := LoadSigner(loose, func(m string) { warnings = append(warnings, m) }); err != nil || len(warnings) != 1 {
		t.Fatalf("mode 0644: %v %v", err, warnings)
	}

	// Inside a git checkout: refused.
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(repo, "secrets")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	inRepo, _ := writeKeyFile(t, sub, "srv-2026", 0o600)
	if _, err := LoadSigner(inRepo, nil); err == nil || !strings.Contains(err.Error(), "git checkout") {
		t.Fatalf("key inside a checkout: %v", err)
	}

	for name, body := range map[string]string{
		"not json":  "BOBRES",
		"bad key":   `{"kid":"srv-2026","private_key":"AAAA"}`,
		"bad kid":   `{"kid":"../x","private_key":"` + base64.StdEncoding.EncodeToString(make([]byte, ed25519.PrivateKeySize)) + `"}`,
		"no base64": `{"kid":"srv-2026","private_key":"!!"}`,
	} {
		p := filepath.Join(dir, "bad.key")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadSigner(p, nil); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := LoadSigner(filepath.Join(dir, "missing.key"), nil); err == nil {
		t.Fatal("a missing key file was accepted")
	}
}

func writePub(t *testing.T, kid string, pub ed25519.PublicKey) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, kid+".pub"), []byte(license.FormatPublicKey(pub)), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestVerifyRing(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	s, err := NewSigner("srv-2026", priv)
	if err != nil {
		t.Fatal(err)
	}
	offline, _, _ := ed25519.GenerateKey(rand.Reader)
	ring, err := VerifyRing(s, license.KeyRing{"v1-2026": offline, "srv-2026": s.Public()})
	if err != nil || len(ring) != 2 || !ring["srv-2026"].Equal(s.Public()) || !ring["v1-2026"].Equal(offline) {
		t.Fatalf("ring %v %v", ring, err)
	}
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := VerifyRing(s, license.KeyRing{"srv-2026": other}); err == nil {
		t.Fatal("a build carrying another key under the signer's kid was accepted")
	}
	if _, err := NewSigner("bad kid", priv); err == nil {
		t.Fatal("bad kid accepted")
	}
	if _, err := NewSigner("srv-2026", priv[:10]); err == nil {
		t.Fatal("short key accepted")
	}
}

func TestLoadConfig(t *testing.T) {
	t.Setenv("BOBRES_DATABASE_URL", "postgres://licensesrv@db/licensesrv")
	t.Setenv("BOBRES_DATABASE_URL_FILE", "")
	t.Setenv("BOBRES_LICENSESRV_SIGNING_KEY_FILE", "/etc/bobres-licensesrv/srv-2026.key")
	t.Setenv("BOBRES_LICENSESRV_TRUSTED_PROXIES", "")
	t.Setenv("BOBRES_LICENSESRV_RATE_IP", "")
	t.Setenv("BOBRES_LICENSESRV_RATE_KEY", "")
	t.Setenv("BOBRES_LICENSESRV_ALLOW_HTTP", "")
	c, err := LoadConfig(true)
	if err != nil {
		t.Fatal(err)
	}
	if c.IPRate.String() != "120/1h0m0s" || c.KeyRate.String() != "10/1h0m0s" || len(c.TrustedProxies) != 2 || c.AllowHTTP {
		t.Fatalf("defaults: %+v", c)
	}

	t.Setenv("BOBRES_LICENSESRV_TRUSTED_PROXIES", "172.18.0.0/16, 10.0.0.5")
	t.Setenv("BOBRES_LICENSESRV_RATE_IP", "30/10m")
	t.Setenv("BOBRES_LICENSESRV_ALLOW_HTTP", "true")
	if c, err = LoadConfig(true); err != nil {
		t.Fatal(err)
	}
	if len(c.TrustedProxies) != 2 || c.TrustedProxies[1].String() != "10.0.0.5/32" || c.IPRate.Max != 30 || !c.AllowHTTP {
		t.Fatalf("set: %+v", c)
	}

	t.Setenv("BOBRES_LICENSESRV_TRUSTED_PROXIES", "proxy.local")
	t.Setenv("BOBRES_LICENSESRV_RATE_KEY", "lots")
	t.Setenv("BOBRES_LICENSESRV_ALLOW_HTTP", "yes")
	t.Setenv("BOBRES_LICENSESRV_SIGNING_KEY_FILE", "")
	t.Setenv("BOBRES_DATABASE_URL", "")
	_, err = LoadConfig(true)
	for _, want := range []string{"TRUSTED_PROXIES", "RATE_KEY", "ALLOW_HTTP", "SIGNING_KEY_FILE", "BOBRES_DATABASE_URL"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("error %v does not mention %s", err, want)
		}
	}
	// The admin commands need no signing key.
	t.Setenv("BOBRES_DATABASE_URL", "postgres://x@db/x")
	t.Setenv("BOBRES_LICENSESRV_TRUSTED_PROXIES", "")
	t.Setenv("BOBRES_LICENSESRV_RATE_KEY", "")
	t.Setenv("BOBRES_LICENSESRV_ALLOW_HTTP", "")
	if _, err := LoadConfig(false); err != nil {
		t.Fatalf("admin config: %v", err)
	}
}
