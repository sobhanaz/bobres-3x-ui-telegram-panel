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
	t.Setenv("BOBRES_MASTER_KEY", "short")
	if _, err := LoadCore(); err == nil {
		t.Fatal("short master key accepted")
	}
	t.Setenv("BOBRES_MASTER_KEY", "")
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
