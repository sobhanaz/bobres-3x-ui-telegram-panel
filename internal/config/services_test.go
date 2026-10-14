package config

import (
	"strings"
	"testing"
)

const (
	coreTok = "core-token-0123456789abcdef0123456789" //gitleaks:allow test fixture
	botTok  = "bot-token-0123456789abcdef01234567890" //gitleaks:allow test fixture
)

func baseEnv(t *testing.T) {
	t.Helper()
	t.Setenv("BOBRES_ENV", "prod")
	t.Setenv("BOBRES_DATABASE_URL", "postgres://x")
	t.Setenv("BOBRES_REDIS_URL", "redis://x")
}

func TestLoadBot(t *testing.T) {
	baseEnv(t)
	t.Setenv("BOBRES_SERVICE_TOKEN", botTok)
	t.Setenv("BOBRES_CORE_GRPC_ADDR", "core:9090")
	if _, err := LoadBot(); err == nil || !strings.Contains(err.Error(), "TELEGRAM_BOT_TOKEN") {
		t.Fatalf("bot config accepted without Telegram bot token: %v", err)
	}
	t.Setenv("BOBRES_TELEGRAM_BOT_TOKEN", "123:abc")
	t.Setenv("BOBRES_ADMIN_TELEGRAM_ID", "4242")
	c, err := LoadBot()
	if err != nil {
		t.Fatalf("LoadBot: %v", err)
	}
	if c.TelegramBotToken != "123:abc" || c.CoreGRPCAddr != "core:9090" || c.AdminTelegramID != 4242 {
		t.Errorf("bad bot config: %+v", c)
	}
	t.Setenv("BOBRES_TELEGRAM_WEBHOOK_URL", "https://panel.example.com/tg")
	if _, err := LoadBot(); err == nil || !strings.Contains(err.Error(), "WEBHOOK_SECRET") {
		t.Fatalf("webhook mode without a secret accepted: %v", err)
	}
	t.Setenv("BOBRES_TELEGRAM_WEBHOOK_URL", "")
	t.Setenv("BOBRES_TELEGRAM_API_URL", "ftp://x")
	if _, err := LoadBot(); err == nil || !strings.Contains(err.Error(), "TELEGRAM_API_URL") {
		t.Fatalf("bad API root accepted: %v", err)
	}
	t.Setenv("BOBRES_TELEGRAM_API_URL", "http://host.docker.internal:8081")
	if c, err := LoadBot(); err != nil || c.TelegramAPIURL != "http://host.docker.internal:8081" {
		t.Fatalf("API root: %v %+v", err, c.TelegramAPIURL)
	}
	// The backup service's token is optional, strict when set, and never
	// the bot's own or core's.
	t.Setenv("BOBRES_PEER_BACKUP_TOKEN", "short")
	if _, err := LoadBot(); err == nil || !strings.Contains(err.Error(), "PEER_BACKUP_TOKEN") {
		t.Fatalf("short backup token accepted: %v", err)
	}
	t.Setenv("BOBRES_PEER_CORE_TOKEN", coreTok)
	t.Setenv("BOBRES_PEER_BACKUP_TOKEN", coreTok)
	if _, err := LoadBot(); err == nil || !strings.Contains(err.Error(), "different") {
		t.Fatalf("core's token accepted as the backup token: %v", err)
	}
	backupTok := strings.Repeat("b", 40)
	t.Setenv("BOBRES_PEER_BACKUP_TOKEN", backupTok)
	if c, err := LoadBot(); err != nil || c.PeerBackupToken != backupTok {
		t.Fatalf("backup token: %v %q", err, c.PeerBackupToken)
	}
}

func TestAdminTelegramIDIsStrict(t *testing.T) {
	for _, bad := range []string{"123abc", "0", "-5", "12 34", "CHANGE_ME_numeric_telegram_id"} {
		t.Setenv("BOBRES_ADMIN_TELEGRAM_ID", bad)
		if _, err := telegramID("BOBRES_ADMIN_TELEGRAM_ID"); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	t.Setenv("BOBRES_ADMIN_TELEGRAM_ID", "")
	if id, err := telegramID("BOBRES_ADMIN_TELEGRAM_ID"); err != nil || id != 0 {
		t.Errorf("unset id: %d %v", id, err)
	}
}

func TestLoadCore(t *testing.T) {
	baseEnv(t)
	t.Setenv("BOBRES_SERVICE_TOKEN", coreTok)
	t.Setenv("BOBRES_PEER_BOT_TOKEN", botTok)
	if _, err := LoadCore(); err == nil {
		t.Fatal("core config accepted without peer gRPC addresses")
	}
	t.Setenv("BOBRES_PAYMENTS_GRPC_ADDR", "payments:9090")
	t.Setenv("BOBRES_PROVISIONER_GRPC_ADDR", "provisioner:9090")
	c, err := LoadCore()
	if err != nil {
		t.Fatalf("LoadCore (master key is optional): %v", err)
	}
	if c.ServiceToken != coreTok || c.BotToken != botTok {
		t.Errorf("tokens not loaded: %+v", c)
	}
	// The peers' HTTP addresses default to the compose service names; the
	// backup service is not in the compose file yet, so it has none.
	if c.BotURL != "" || c.PaymentsURL != "http://payments:8080" || c.ProvisionerURL != "http://provisioner:8080" ||
		c.BackupURL != "" || c.Domain != "" {
		t.Errorf("peer defaults: %q %q %q %q %q", c.BotURL, c.PaymentsURL, c.ProvisionerURL, c.BackupURL, c.Domain)
	}
	t.Setenv("BOBRES_BACKUP_URL", "http://10.0.0.5:8081/")
	t.Setenv("BOBRES_DOMAIN", " panel.example.com ")
	if c, err = LoadCore(); err != nil || c.BackupURL != "http://10.0.0.5:8081" || c.Domain != "panel.example.com" {
		t.Fatalf("backup URL and domain: %q %q %v", c.BackupURL, c.Domain, err)
	}
	// The seller's contact has no default (a made-up one could belong to a
	// stranger); the owner sets it, and stray space is dropped.
	if c.SellerContact != "" {
		t.Errorf("a default seller contact: %q", c.SellerContact)
	}
	t.Setenv("BOBRES_SELLER_CONTACT", "  @test_seller ")
	if c, err = LoadCore(); err != nil || c.SellerContact != "@test_seller" {
		t.Fatalf("seller contact: %q %v", c.SellerContact, err)
	}
	t.Setenv("BOBRES_SELLER_CONTACT", "")
	for _, name := range []string{"BOBRES_BOT_URL", "BOBRES_PAYMENTS_URL", "BOBRES_PROVISIONER_URL", "BOBRES_BACKUP_URL"} {
		t.Setenv(name, "payments:8080")
		if _, err := LoadCore(); err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("%s without a scheme accepted: %v", name, err)
		}
		t.Setenv(name, "")
	}
	t.Setenv("BOBRES_MASTER_KEY", "short")
	if _, err := LoadCore(); err == nil {
		t.Fatal("short master key accepted")
	}
	t.Setenv("BOBRES_MASTER_KEY", "")
	// Core's token for the backup service: optional, strict when set, and
	// never core's own token (the backup container would hold it) or the bot's.
	if c.BackupToken != "" {
		t.Fatalf("backup token without one set: %q", c.BackupToken)
	}
	backupTok := strings.Repeat("d", 40)
	t.Setenv("BOBRES_BACKUP_TOKEN", backupTok)
	if c, err := LoadCore(); err != nil || c.BackupToken != backupTok || c.ServiceToken != coreTok {
		t.Fatalf("backup token: %v %q", err, c.BackupToken)
	}
	for _, bad := range []string{"short", coreTok, botTok} {
		t.Setenv("BOBRES_BACKUP_TOKEN", bad)
		if _, err := LoadCore(); err == nil || !strings.Contains(err.Error(), "BOBRES_BACKUP_TOKEN") {
			t.Errorf("backup token %q accepted: %v", bad, err)
		}
	}
	t.Setenv("BOBRES_BACKUP_TOKEN", "")
	t.Setenv("BOBRES_PEER_BOT_TOKEN", coreTok)
	if _, err := LoadCore(); err == nil || !strings.Contains(err.Error(), "different") {
		t.Fatalf("one token for two identities accepted: %v", err)
	}
}

func TestLoadPaymentsRequiresCoreToken(t *testing.T) {
	baseEnv(t)
	if _, err := LoadPayments(); err == nil {
		t.Fatal("payments accepted without core's token")
	}
	t.Setenv("BOBRES_PEER_CORE_TOKEN", coreTok)
	if _, err := LoadPayments(); err != nil {
		t.Fatalf("LoadPayments: %v", err)
	}
}

func TestLoadProvisioner(t *testing.T) {
	baseEnv(t)
	t.Setenv("BOBRES_PEER_CORE_TOKEN", coreTok)
	if _, err := LoadProvisioner(); err == nil {
		t.Fatal("provisioner accepted without master key")
	}
	t.Setenv("BOBRES_MASTER_KEY", "short")
	if _, err := LoadProvisioner(); err == nil {
		t.Fatal("short master key accepted")
	}
	t.Setenv("BOBRES_MASTER_KEY", strings.Repeat("k", 32))
	if _, err := LoadProvisioner(); err != nil {
		t.Fatalf("LoadProvisioner: %v", err)
	}
	t.Setenv("BOBRES_XUI_URL", "https://panel.example.com:2053")
	if _, err := LoadProvisioner(); err == nil {
		t.Fatal("panel URL without token accepted")
	}
	t.Setenv("BOBRES_XUI_TOKEN", "panel-token")
	t.Setenv("BOBRES_XUI_ALLOW_PRIVATE", "true")
	p, err := LoadProvisioner()
	if err != nil || p.XUIURL == "" || p.XUIToken != "panel-token" || !p.XUIAllowPrivate {
		t.Fatalf("panel seed not loaded: %+v %v", p, err)
	}
}

func TestZarinpalConfig(t *testing.T) {
	baseEnv(t)
	t.Setenv("BOBRES_DATABASE_URL", "postgres://x")
	t.Setenv("BOBRES_PEER_CORE_TOKEN", strings.Repeat("c", 40))
	t.Setenv("BOBRES_ZARINPAL_MERCHANT_ID", "0b7e2f3a-1c9d-4e5f-8a6b-7c8d9e0f1a2b")
	t.Setenv("BOBRES_PUBLIC_URL", "https://shop.example.com")
	if c, err := LoadPayments(); err != nil || c.ZarinpalPublicURL != "https://shop.example.com" {
		t.Fatalf("defaults to the public URL: %v %+v", err, c.ZarinpalPublicURL)
	}
	// The sandbox gives service for free (any merchant, no card): never in prod.
	t.Setenv("BOBRES_ZARINPAL_SANDBOX", "true")
	if _, err := LoadPayments(); err == nil || !strings.Contains(err.Error(), "SANDBOX") {
		t.Fatalf("sandbox accepted in prod: %v", err)
	}
	t.Setenv("BOBRES_ZARINPAL_SANDBOX", "false")
	t.Setenv("BOBRES_ZARINPAL_PUBLIC_URL", "https://pay.example.ir/path")
	if _, err := LoadPayments(); err == nil {
		t.Fatal("a public URL with a path was accepted")
	}
}

// The payments master key is optional (the gateway settings then stay as the
// environment has them); when set it follows the other services' rules.
func TestLoadPaymentsMasterKey(t *testing.T) {
	baseEnv(t)
	t.Setenv("BOBRES_PEER_CORE_TOKEN", coreTok)
	if c, err := LoadPayments(); err != nil || c.MasterKey != "" {
		t.Fatalf("no key: %v %q", err, c.MasterKey)
	}
	for _, bad := range []string{"short", "CHANGE_ME" + strings.Repeat("k", 32)} {
		t.Setenv("BOBRES_MASTER_KEY", bad)
		if _, err := LoadPayments(); err == nil || !strings.Contains(err.Error(), "BOBRES_MASTER_KEY") {
			t.Errorf("master key %q accepted: %v", bad, err)
		}
	}
	key := strings.Repeat("p", 40)
	t.Setenv("BOBRES_MASTER_KEY", key)
	c, err := LoadPayments()
	if err != nil || c.MasterKey != key {
		t.Fatalf("key not loaded: %v", err)
	}
	// Logging the configuration never prints the key.
	if s := c.String(); strings.Contains(s, key) {
		t.Fatalf("master key in %q", s)
	}
}

func TestCheckZarinpalPublicURL(t *testing.T) {
	for _, tc := range []struct {
		url, env string
		ok       bool
	}{
		{"https://shop.example.com", "prod", true},
		{"https://shop.example.com:8443", "prod", true},
		{"http://localhost:8080", "dev", true},
		{"http://shop.example.com", "prod", false},
		{"http://shop.example.com", "staging", false},
		{"https://shop.example.com/pay", "prod", false},
		{"https://shop.example.com?x=1", "prod", false},
		{"https://", "prod", false},
		{"", "dev", false},
	} {
		if err := CheckZarinpalPublicURL(tc.url, tc.env); (err == nil) != tc.ok {
			t.Errorf("%q in %s: %v", tc.url, tc.env, err)
		}
	}
}
