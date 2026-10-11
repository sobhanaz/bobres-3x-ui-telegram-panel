package config

import (
	"strings"
	"testing"
	"time"
)

const (
	backupKey = "backup-key-0123456789abcdef0123456789" //gitleaks:allow test fixture
	backupTok = "backup-token-0123456789abcdef01234567" //gitleaks:allow test fixture
)

func backupEnv(t *testing.T) {
	t.Helper()
	t.Setenv("BOBRES_ENV", "prod")
	t.Setenv("BOBRES_BACKUP_KEY", backupKey)
	t.Setenv("BOBRES_BACKUP_DATABASE_URL", "postgres://svc_backup:pw@postgres:5432/bobres?sslmode=disable")
	for _, k := range []string{"BOBRES_BACKUP_DIR", "BOBRES_BACKUP_TIME", "BOBRES_TIMEZONE", "BOBRES_PEER_CORE_TOKEN",
		"BOBRES_SERVICE_TOKEN", "BOBRES_BOT_URL", "BOBRES_CORE_MASTER_KEY", "BOBRES_PROVISIONER_MASTER_KEY", "BOBRES_PAYMENTS_MASTER_KEY"} {
		t.Setenv(k, "")
	}
}

func TestLoadBackupDefaults(t *testing.T) {
	backupEnv(t)
	c, err := LoadBackup()
	if err != nil {
		t.Fatal(err)
	}
	if c.Dir != "/backups" || c.Hour != 3 || c.Minute != 30 || c.Location != time.UTC || c.BotURL != "" || c.CoreToken != "" {
		t.Fatalf("defaults: %+v", c)
	}
	if len(c.MasterKeys) != 0 || len(c.MissingKeys) != 3 {
		t.Fatalf("master keys: %v missing %v", c.MasterKeys, c.MissingKeys)
	}
	if s := c.String(); strings.Contains(s, backupKey) || strings.Contains(s, "pw@") {
		t.Fatalf("String leaks a secret: %s", s)
	}
}

func TestLoadBackupFull(t *testing.T) {
	backupEnv(t)
	t.Setenv("BOBRES_BACKUP_DIR", "/srv/backups")
	t.Setenv("BOBRES_BACKUP_TIME", "4:05")
	t.Setenv("BOBRES_TIMEZONE", "Asia/Tehran")
	t.Setenv("BOBRES_PEER_CORE_TOKEN", coreTok)
	t.Setenv("BOBRES_SERVICE_TOKEN", backupTok)
	t.Setenv("BOBRES_BOT_URL", "http://bot:8080/")
	t.Setenv("BOBRES_CORE_MASTER_KEY", "core-master-key-0123456789abcdef0123")
	t.Setenv("BOBRES_PAYMENTS_MASTER_KEY", "payments-master-key-0123456789abcdef")
	c, err := LoadBackup()
	if err != nil {
		t.Fatal(err)
	}
	if c.Dir != "/srv/backups" || c.Hour != 4 || c.Minute != 5 || c.Location.String() != "Asia/Tehran" ||
		c.CoreToken != coreTok || c.ServiceToken != backupTok || c.BotURL != "http://bot:8080" {
		t.Fatalf("config: %+v", c)
	}
	if len(c.MasterKeys) != 2 || c.MasterKeys["BOBRES_CORE_MASTER_KEY"] == "" ||
		len(c.MissingKeys) != 1 || c.MissingKeys[0] != "BOBRES_PROVISIONER_MASTER_KEY" {
		t.Fatalf("master keys: %v missing %v", c.MasterKeys, c.MissingKeys)
	}
}

func TestLoadBackupRejects(t *testing.T) {
	for _, c := range []struct {
		name, key, value, want string
	}{
		{"no key", "BOBRES_BACKUP_KEY", "", "BOBRES_BACKUP_KEY must be at least 32"},
		{"short key", "BOBRES_BACKUP_KEY", "short", "BOBRES_BACKUP_KEY must be at least 32"},
		{"placeholder key", "BOBRES_BACKUP_KEY", "CHANGE_ME_backup_key_32_characters_min", "placeholder"},
		{"no database", "BOBRES_BACKUP_DATABASE_URL", "", "BOBRES_BACKUP_DATABASE_URL is required"},
		{"not postgres", "BOBRES_BACKUP_DATABASE_URL", "mysql://secret-pw@db/x", "must be a postgres:// URL"},
		{"placeholder database", "BOBRES_BACKUP_DATABASE_URL", "postgres://svc_backup:CHANGE_ME@postgres/bobres", "placeholder"},
		{"bad time", "BOBRES_BACKUP_TIME", "25:00", "BOBRES_BACKUP_TIME"},
		{"bad time format", "BOBRES_BACKUP_TIME", "3.30", "BOBRES_BACKUP_TIME"},
		{"signed time", "BOBRES_BACKUP_TIME", "+3:30", "BOBRES_BACKUP_TIME"},
		{"bad zone", "BOBRES_TIMEZONE", "Mars/Olympus", "BOBRES_TIMEZONE"},
		{"short core token", "BOBRES_PEER_CORE_TOKEN", "short", "BOBRES_PEER_CORE_TOKEN"},
		{"short own token", "BOBRES_SERVICE_TOKEN", "short", "BOBRES_SERVICE_TOKEN"},
		{"bad bot url", "BOBRES_BOT_URL", "bot:8080", "BOBRES_BOT_URL"},
		{"short master key", "BOBRES_CORE_MASTER_KEY", "short", "BOBRES_CORE_MASTER_KEY must be at least 32"},
	} {
		t.Run(c.name, func(t *testing.T) {
			backupEnv(t)
			t.Setenv(c.key, c.value)
			_, err := LoadBackup()
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want %q", err, c.want)
			}
			if strings.Contains(err.Error(), "secret-pw") {
				t.Fatalf("error quotes the database password: %v", err)
			}
		})
	}
	backupEnv(t)
	t.Setenv("BOBRES_PEER_CORE_TOKEN", coreTok)
	t.Setenv("BOBRES_SERVICE_TOKEN", coreTok)
	if _, err := LoadBackup(); err == nil || !strings.Contains(err.Error(), "different tokens") {
		t.Fatalf("a shared token accepted: %v", err)
	}
}
