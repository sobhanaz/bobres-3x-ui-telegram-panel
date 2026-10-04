package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func setEnv(t *testing.T, env string) {
	t.Helper()
	t.Setenv("BOBRES_ENV", env)
}

func TestLoadDefaultsAndFailClosedEnv(t *testing.T) {
	t.Setenv("BOBRES_ENV", "")
	c, err := Load("core")
	if err != nil {
		t.Fatal(err)
	}
	if c.Env != "prod" || c.LogFormat != "json" || c.HTTPAddr != ":8080" || c.Service != "core" {
		t.Fatalf("unexpected defaults: %+v", c)
	}
}

func TestTokenLengthDependsOnEnv(t *testing.T) {
	for _, env := range []string{"dev", "staging", "prod"} {
		t.Setenv("BOBRES_X_TOKEN", "")
		if _, err := requireToken("BOBRES_X_TOKEN", env); err == nil {
			t.Errorf("%s: empty token must be rejected", env)
		}
	}
	t.Setenv("BOBRES_X_TOKEN", "sixteen-chars-ok!")
	if _, err := requireToken("BOBRES_X_TOKEN", "dev"); err != nil {
		t.Fatalf("dev accepts 16+: %v", err)
	}
	if _, err := requireToken("BOBRES_X_TOKEN", "prod"); err == nil {
		t.Fatal("prod must require 32+")
	}
}

func TestPlaceholderSecretsRejected(t *testing.T) {
	t.Setenv("BOBRES_X_TOKEN", "CHANGE_ME_random_32_chars_minimum_value")
	if _, err := requireToken("BOBRES_X_TOKEN", "prod"); err == nil || !strings.Contains(err.Error(), "placeholder") {
		t.Fatalf("placeholder token must be rejected, got %v", err)
	}
	setEnv(t, "prod")
	t.Setenv("BOBRES_DATABASE_URL", "postgres://u:CHANGE_ME_pw@db/x")
	if _, err := Load("core"); err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("placeholder DSN must be rejected, got %v", err)
	}
}

func TestInvalidValuesRejected(t *testing.T) {
	setEnv(t, "banana")
	t.Setenv("BOBRES_LOG_FORMAT", "xml")
	_, err := Load("core")
	if err == nil || !strings.Contains(err.Error(), "BOBRES_ENV") || !strings.Contains(err.Error(), "BOBRES_LOG_FORMAT") {
		t.Fatalf("want both errors, got %v", err)
	}
}

func TestSecretFromFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s")
	if err := os.WriteFile(p, []byte("  from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BOBRES_X", "from-env")
	t.Setenv("BOBRES_X_FILE", p)
	if got, err := Secret("BOBRES_X"); err != nil || got != "from-file" {
		t.Fatalf("got %q %v", got, err)
	}
}

func TestSecretFileFailsClosed(t *testing.T) {
	t.Setenv("BOBRES_X", "from-env-must-not-be-used")
	t.Setenv("BOBRES_X_FILE", "/nonexistent/secret")
	if got, err := Secret("BOBRES_X"); err == nil || got != "" {
		t.Fatalf("unreadable file must error and never fall back: %q %v", got, err)
	}
	empty := filepath.Join(t.TempDir(), "empty")
	if err := os.WriteFile(empty, []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BOBRES_X_FILE", empty)
	if _, err := Secret("BOBRES_X"); err == nil {
		t.Fatal("empty secret file must error")
	}
	// and Load surfaces it
	setEnv(t, "prod")
	t.Setenv("BOBRES_DATABASE_URL_FILE", "/nonexistent/db")
	if _, err := Load("core"); err == nil || !strings.Contains(err.Error(), "DATABASE_URL_FILE") {
		t.Fatalf("Load must fail on unreadable secret file, got %v", err)
	}
}

func TestSecretsNeverPrinted(t *testing.T) {
	c := Common{Service: "core", Env: "prod", DatabaseURL: "postgres://u:hunter2@db/x"} //nolint:gosec // test fixture
	co := Core{Common: c, ServiceToken: "supersecrettoken", BotToken: "botsecrettoken"}
	for _, s := range []string{fmt.Sprint(c), fmt.Sprintf("%v", c), fmt.Sprintf("%+v", c), c.LogValue().String(),
		fmt.Sprint(co), fmt.Sprintf("%+v", co)} {
		if strings.Contains(s, "supersecrettoken") || strings.Contains(s, "botsecrettoken") || strings.Contains(s, "hunter2") {
			t.Fatalf("secret leaked: %s", s)
		}
	}
}

func TestDurationParsing(t *testing.T) {
	cases := map[string]time.Duration{"3s": 3 * time.Second, "7": 7 * time.Second, "junk": time.Second, "-5s": time.Second, "0": time.Second, "1.5": time.Second, "": time.Second}
	for in, want := range cases {
		t.Setenv("D", in)
		if got := getDuration("D", time.Second); got != want {
			t.Errorf("%q: got %v want %v", in, got, want)
		}
	}
}
