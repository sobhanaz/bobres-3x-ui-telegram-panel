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
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/config"
)

// backupInstallEnv is an install's .env with low-entropy test values for
// what the backup service checks (never real ones), then extra.
func backupInstallEnv(extra map[string]string) map[string]string {
	env := installEnv(map[string]string{
		"BOBRES_BACKUP_KEY": strings.Repeat("k", 40), "DB_BACKUP_PASSWORD": strings.Repeat("p", 24),
		"BOBRES_TOKEN_CORE_BACKUP":      strings.Repeat("c", 40),
		"BOBRES_CORE_MASTER_KEY":        strings.Repeat("1", 40),
		"BOBRES_PROVISIONER_MASTER_KEY": strings.Repeat("2", 40),
		"BOBRES_PAYMENTS_MASTER_KEY":    strings.Repeat("3", 40),
	})
	for k, v := range extra {
		env[k] = v
	}
	return env
}

// loadBackup runs config.LoadBackup with only the environment compose gives
// the backup service for the .env values env.
func loadBackup(t *testing.T, env map[string]string) config.Backup {
	t.Helper()
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); strings.HasPrefix(k, "BOBRES_") {
			t.Setenv(k, "")
		}
	}
	for k, v := range composeEnv(t, "backup", env) {
		t.Setenv(k, v)
	}
	c, err := config.LoadBackup()
	if err != nil {
		t.Fatalf("the backup service does not start with the compose environment: %v", err)
	}
	return c
}

// The S3 settings in .env reach the backup service through the compose file
// (it has no env_file); without them the upload is off. Its second network
// follows BOBRES_BACKUP_NETWORK: egress for the upload, else an internal one
// of its own (no internet).
func TestComposeGivesTheBackupServiceItsS3Settings(t *testing.T) {
	access, secret := strings.Repeat("A", 20), strings.Repeat("s", 40)
	set := backupInstallEnv(map[string]string{
		"BOBRES_BACKUP_S3_ENDPOINT": "https://s3.example.com", "BOBRES_BACKUP_S3_BUCKET": "shop-backups",
		"BOBRES_BACKUP_S3_REGION": "eu-central-1", "BOBRES_BACKUP_S3_PREFIX": "daily",
		"BOBRES_BACKUP_S3_ACCESS_KEY": access, "BOBRES_BACKUP_S3_SECRET_KEY": secret, "BOBRES_BACKUP_S3_KEEP": "14",
		"BOBRES_BACKUP_NETWORK": "egress",
	})
	c := loadBackup(t, set)
	want := config.BackupS3{Endpoint: "https://s3.example.com", Bucket: "shop-backups", Region: "eu-central-1", Prefix: "daily/",
		AccessKey: access, SecretKey: secret, Keep: 14}
	if c.S3Err != nil || c.S3 != want {
		t.Fatalf("the backup service's S3 settings: %+v %v", c.S3, c.S3Err)
	}
	unset := backupInstallEnv(nil)
	if c := loadBackup(t, unset); c.S3Err != nil || c.S3.Enabled() {
		t.Fatalf("without the lines: %+v %v", c.S3, c.S3Err)
	}
	// Only the backup service gets the keys.
	for _, svc := range []string{"core", "payments", "provisioner", "bot"} {
		for k := range composeEnv(t, svc, set) {
			if strings.HasPrefix(k, "BOBRES_BACKUP_S3_") {
				t.Errorf("%s gets %s", svc, k)
			}
		}
	}

	if !strings.Contains(string(ComposeFile), "      - ${BOBRES_BACKUP_NETWORK:-offline}\n") ||
		!regexp.MustCompile(`(?m)^  offline:\n    internal: true$`).Match(ComposeFile) {
		t.Error("the backup service's second network is not ${BOBRES_BACKUP_NETWORK:-offline}, an internal network")
	}
	for _, line := range []string{"#BOBRES_BACKUP_S3_ENDPOINT=https://", "#BOBRES_BACKUP_S3_KEEP=30", "#BOBRES_BACKUP_NETWORK=egress"} {
		if !strings.Contains(string(EnvExample), line) {
			t.Errorf(".env.example does not document %s", line)
		}
	}

	if _, err := exec.LookPath("docker"); err != nil {
		return
	}
	for _, env := range []map[string]string{set, unset} {
		if got, want := dockerComposeEnv(t, "backup", env), composeEnv(t, "backup", env); !reflect.DeepEqual(got, want) {
			t.Errorf("docker compose config gives the backup service\n%v\ncomposeEnv\n%v", got, want)
		}
	}
	if got := dockerComposeNetworks(t, "backup", set); !reflect.DeepEqual(got, []string{"backend", "egress"}) {
		t.Errorf("with the upload: networks %v", got)
	}
	if got := dockerComposeNetworks(t, "backup", unset); !reflect.DeepEqual(got, []string{"backend", "offline"}) {
		t.Errorf("without the upload: networks %v", got)
	}
}

// dockerComposeNetworks are the networks `docker compose config` puts
// service on with the .env values env, sorted.
func dockerComposeNetworks(t *testing.T, service string, env map[string]string) []string {
	t.Helper()
	dir := t.TempDir()
	var dotenv strings.Builder
	for k, v := range env {
		dotenv.WriteString(k + "=" + v + "\n")
	}
	if err := os.WriteFile(filepath.Join(dir, "docker-compose.yml"), ComposeFile, 0o600); err != nil {
		t.Fatal(err)
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
	got, err := cmd.Output()
	if err != nil {
		t.Fatalf("docker compose config: %v", err)
	}
	var project struct {
		Services map[string]struct {
			Networks map[string]any `json:"networks"`
		} `json:"services"`
	}
	if err := json.Unmarshal(got, &project); err != nil {
		t.Fatalf("docker compose config: %v", err)
	}
	var names []string
	for n := range project.Services[service].Networks {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
