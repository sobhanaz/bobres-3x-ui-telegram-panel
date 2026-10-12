package config

import (
	"os"
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
	for _, k := range BackupS3Keys {
		t.Setenv(k, "")
		t.Setenv(k+"_FILE", "")
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

// Low-entropy stand-ins for S3 keys (never real ones).
var (
	s3Access = strings.Repeat("A", 20)
	s3Secret = strings.Repeat("s", 40)
)

func s3Env(t *testing.T) {
	t.Helper()
	backupEnv(t)
	t.Setenv("BOBRES_BACKUP_S3_ENDPOINT", "https://s3.ir-thr-at1.arvanstorage.ir/")
	t.Setenv("BOBRES_BACKUP_S3_BUCKET", "shop-backups")
	t.Setenv("BOBRES_BACKUP_S3_ACCESS_KEY", s3Access)
	t.Setenv("BOBRES_BACKUP_S3_SECRET_KEY", s3Secret)
}

func TestLoadBackupS3(t *testing.T) {
	backupEnv(t)
	c, err := LoadBackup()
	if err != nil || c.S3.Enabled() || c.S3Err != nil || c.S3.Target() != "" {
		t.Fatalf("off by default: %+v %v %v", c.S3, c.S3Err, err)
	}

	s3Env(t)
	t.Setenv("BOBRES_BACKUP_S3_PREFIX", "/shop/daily/")
	t.Setenv("BOBRES_BACKUP_S3_REGION", "ir-thr-at1")
	c, err = LoadBackup()
	want := BackupS3{Endpoint: "https://s3.ir-thr-at1.arvanstorage.ir", Bucket: "shop-backups", Region: "ir-thr-at1",
		Prefix: "shop/daily/", AccessKey: s3Access, SecretKey: s3Secret, Keep: DefaultS3Keep}
	if err != nil || c.S3Err != nil || c.S3 != want || c.S3.Target() != "s3://shop-backups/shop/daily/" {
		t.Fatalf("S3 %+v %v %v", c.S3, c.S3Err, err)
	}
	for _, s := range []string{c.S3.String(), c.String()} {
		if strings.Contains(s, s3Secret) || strings.Contains(s, s3Access) {
			t.Fatalf("String shows a key: %s", s)
		}
	}

	// The secret key from a file (Docker secrets), and the bucket's keep.
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/s3", []byte(s3Secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BOBRES_BACKUP_S3_SECRET_KEY", "")
	t.Setenv("BOBRES_BACKUP_S3_SECRET_KEY_FILE", dir+"/s3")
	t.Setenv("BOBRES_BACKUP_S3_KEEP", "90")
	if c, err = LoadBackup(); err != nil || c.S3Err != nil || c.S3.SecretKey != s3Secret || c.S3.Keep != 90 {
		t.Fatalf("from a file: %+v %v %v", c.S3, c.S3Err, err)
	}
	t.Setenv("BOBRES_BACKUP_S3_SECRET_KEY_FILE", dir+"/missing")
	if c, err = LoadBackup(); err != nil || c.S3Err == nil || c.S3.Enabled() {
		t.Fatalf("an unreadable key file: %+v %v %v", c.S3, c.S3Err, err)
	}
}

// Settings that cannot be used never stop the service (LoadBackup has no
// error): S3Err says why, without a key's value.
func TestLoadBackupS3Problems(t *testing.T) {
	for _, c := range []struct {
		name, key, value, want string
	}{
		{"plain http", "BOBRES_BACKUP_S3_ENDPOINT", "http://s3.example.com", "must be an https URL"},
		{"endpoint path", "BOBRES_BACKUP_S3_ENDPOINT", "https://s3.example.com/bucket", "must be an https URL"},
		{"endpoint credentials", "BOBRES_BACKUP_S3_ENDPOINT", "https://user:pw@s3.example.com", "must be an https URL"},
		{"endpoint query", "BOBRES_BACKUP_S3_ENDPOINT", "https://s3.example.com?x=1", "must be an https URL"},
		{"no bucket", "BOBRES_BACKUP_S3_BUCKET", "", "BOBRES_BACKUP_S3_BUCKET is required"},
		{"bad bucket", "BOBRES_BACKUP_S3_BUCKET", "Shop_Backups", "not a bucket name"},
		{"short bucket", "BOBRES_BACKUP_S3_BUCKET", "ab", "not a bucket name"},
		{"bad region", "BOBRES_BACKUP_S3_REGION", "ir thr", "not a region name"},
		{"bad prefix", "BOBRES_BACKUP_S3_PREFIX", "shop/../etc", "BOBRES_BACKUP_S3_PREFIX may hold"},
		{"prefix characters", "BOBRES_BACKUP_S3_PREFIX", "shop backups", "BOBRES_BACKUP_S3_PREFIX may hold"},
		{"no access key", "BOBRES_BACKUP_S3_ACCESS_KEY", "", "BOBRES_BACKUP_S3_ACCESS_KEY is required"},
		{"no secret key", "BOBRES_BACKUP_S3_SECRET_KEY", "", "BOBRES_BACKUP_S3_SECRET_KEY is required"},
		{"placeholder", "BOBRES_BACKUP_S3_SECRET_KEY", "CHANGE_ME_" + strings.Repeat("x", 8), "placeholder"},
		{"keep zero", "BOBRES_BACKUP_S3_KEEP", "0", "BOBRES_BACKUP_S3_KEEP must be a number from 1 to 1000"},
		{"keep text", "BOBRES_BACKUP_S3_KEEP", "thirty", "BOBRES_BACKUP_S3_KEEP"},
	} {
		t.Run(c.name, func(t *testing.T) {
			s3Env(t)
			t.Setenv(c.key, c.value)
			cfg, err := LoadBackup()
			if err != nil {
				t.Fatalf("the service would not start: %v", err)
			}
			if cfg.S3Err == nil || !strings.Contains(cfg.S3Err.Error(), c.want) || cfg.S3.Enabled() {
				t.Fatalf("got %v (%+v), want %q", cfg.S3Err, cfg.S3, c.want)
			}
			if strings.Contains(cfg.S3Err.Error(), s3Secret) {
				t.Fatalf("the error shows the secret key: %v", cfg.S3Err)
			}
		})
	}
	// Without an endpoint the rest is not looked at (but a bad keep is still said).
	s, err := ParseBackupS3(map[string]string{"BOBRES_BACKUP_S3_BUCKET": "Bad_Name"})
	if err != nil || s.Enabled() {
		t.Fatalf("no endpoint: %+v %v", s, err)
	}
	if _, err := ParseBackupS3(map[string]string{"BOBRES_BACKUP_S3_KEEP": "-1"}); err == nil {
		t.Fatal("a bad keep without an endpoint was accepted")
	}
	if !ValidBucket("shop.backups-1") || ValidBucket("shop..backups") || ValidBucket("-shop") {
		t.Fatal("ValidBucket")
	}
}
