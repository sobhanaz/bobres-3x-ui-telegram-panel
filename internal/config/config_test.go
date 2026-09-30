package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("BOBRES_ENV", "")
	c, err := Load("core")
	if err != nil {
		t.Fatal(err)
	}
	if c.Env != "dev" || c.LogFormat != "json" || c.HTTPAddr != ":8080" || c.Service != "core" {
		t.Fatalf("unexpected defaults: %+v", c)
	}
}

func TestProdRequiresStrongServiceToken(t *testing.T) {
	t.Setenv("BOBRES_ENV", "prod")
	t.Setenv("BOBRES_SERVICE_TOKEN", "short")
	if _, err := Load("core"); err == nil || !strings.Contains(err.Error(), "SERVICE_TOKEN") {
		t.Fatalf("want service token error, got %v", err)
	}
	t.Setenv("BOBRES_SERVICE_TOKEN", strings.Repeat("a", 32))
	if _, err := Load("core"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInvalidValuesRejected(t *testing.T) {
	t.Setenv("BOBRES_ENV", "banana")
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
	if got := Secret("BOBRES_X"); got != "from-file" {
		t.Fatalf("got %q", got)
	}
}

func TestDurationParsing(t *testing.T) {
	t.Setenv("D", "3s")
	if getDuration("D", time.Second) != 3*time.Second {
		t.Fatal("duration string")
	}
	t.Setenv("D", "7")
	if getDuration("D", time.Second) != 7*time.Second {
		t.Fatal("plain seconds")
	}
	t.Setenv("D", "junk")
	if getDuration("D", time.Second) != time.Second {
		t.Fatal("fallback")
	}
}
