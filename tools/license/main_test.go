package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/license"
)

// keygen writes a 0600 key outside the checkout and prints the public key in
// the key file format; sign makes a token core verifies with it.
func TestKeygenAndSign(t *testing.T) {
	dir := t.TempDir()
	if gitCheckout(dir) != "" {
		t.Skip("the temporary directory is inside a git checkout")
	}
	keyPath := filepath.Join(dir, "vendor.key")
	var out, errOut bytes.Buffer
	if code := run([]string{"keygen", "-kid", "v1-test", "-out", keyPath}, nil, &out, &errOut); code != 0 {
		t.Fatalf("keygen: %d %s", code, errOut.String())
	}
	if fi, err := os.Stat(keyPath); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("key file: %v %v", fi, err)
	}
	pub, err := license.ParsePublicKey(out.String())
	if err != nil {
		t.Fatalf("public key line %q: %v", out.String(), err)
	}
	if strings.Contains(out.String(), "private") {
		t.Fatal("the private key went to stdout")
	}
	// Never overwritten.
	if code := run([]string{"keygen", "-kid", "v1-test", "-out", keyPath}, nil, &out, &errOut); code != 1 {
		t.Fatalf("second keygen over the same file: %d", code)
	}

	payload := `{"license_id":"lic_1","customer":"Shop","domain":"panel.example.com","tier":"standard",
		"expires_at":"2027-10-11T00:00:00Z","grace_days":14,
		"entitlements":{"max_servers":2,"max_active_services":-1,"dashboard_staff_seats":3,"resellers":true,"gateways":["zarinpal"]}}`
	out.Reset()
	errOut.Reset()
	if code := run([]string{"sign", "-key", keyPath, "-now", "2026-10-11T00:00:00Z"}, strings.NewReader(payload), &out, &errOut); code != 0 {
		t.Fatalf("sign: %d %s", code, errOut.String())
	}
	tok, err := license.Verify(license.KeyRing{"v1-test": pub}, strings.TrimSpace(out.String()),
		license.Options{Domain: "panel.example.com", RequireBound: true})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if tok.KeyID != "v1-test" || tok.V != 1 || !tok.IssuedAt.Equal(time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)) ||
		tok.Entitlements.MaxServers != 2 || !tok.Entitlements.Resellers {
		t.Fatalf("token: %+v", tok)
	}

	// Payloads core would refuse are refused here.
	for name, p := range map[string]string{
		"no domain":     `{"license_id":"lic_1","tier":"standard","expires_at":"2027-10-11T00:00:00Z"}`,
		"another kid":   `{"kid":"other","license_id":"lic_1","domain":"a.example","tier":"standard","expires_at":"2027-10-11T00:00:00Z"}`,
		"unknown field": `{"license_id":"lic_1","domain":"a.example","tier":"standard","expires_at":"2027-10-11T00:00:00Z","sig":"x"}`,
		"bad tier":      `{"license_id":"lic_1","domain":"a.example","tier":"gold","expires_at":"2027-10-11T00:00:00Z"}`,
		"expired first": `{"license_id":"lic_1","domain":"a.example","tier":"trial","issued_at":"2027-01-01T00:00:00Z","expires_at":"2026-01-01T00:00:00Z"}`,
	} {
		if code := run([]string{"sign", "-key", keyPath}, strings.NewReader(p), &out, &errOut); code != 1 {
			t.Errorf("%s: %d", name, code)
		}
	}
}

// The private key is never written inside a git checkout (this test runs in
// one: the repository).
func TestKeygenRefusesCheckout(t *testing.T) {
	wd, _ := os.Getwd()
	if gitCheckout(wd) == "" {
		t.Skip("not run from a git checkout")
	}
	path := filepath.Join(wd, "never.key")
	var out, errOut bytes.Buffer
	if code := run([]string{"keygen", "-kid", "v1", "-out", path}, nil, &out, &errOut); code != 1 ||
		!strings.Contains(errOut.String(), "outside any repository") {
		t.Fatalf("%d %s", code, errOut.String())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		_ = os.Remove(path)
		t.Fatal("a key file was written inside the checkout")
	}
}

func TestUsage(t *testing.T) {
	for _, args := range [][]string{nil, {"verify"}, {"keygen"}, {"keygen", "-kid", "bad id", "-out", "x"}, {"sign"}} {
		var out, errOut bytes.Buffer
		if code := run(args, nil, &out, &errOut); code != 2 {
			t.Errorf("%v: %d", args, code)
		}
	}
	var out, errOut bytes.Buffer
	if code := run([]string{"sign", "-key", filepath.Join(t.TempDir(), "missing.key")}, strings.NewReader("{}"), &out, &errOut); code != 1 {
		t.Errorf("missing key file: %d", code)
	}
}
