package config

import (
	"strings"
	"testing"
)

func baseEnv(t *testing.T) {
	t.Helper()
	t.Setenv("BOBRES_DATABASE_URL", "postgres://x")
	t.Setenv("BOBRES_REDIS_URL", "redis://x")
	t.Setenv("BOBRES_SERVICE_TOKEN", strings.Repeat("s", 32))
}

func TestLoadBotRequiresToken(t *testing.T) {
	baseEnv(t)
	t.Setenv("BOBRES_CORE_GRPC_ADDR", "core:9090")
	if _, err := LoadBot(); err == nil {
		t.Fatal("bot config accepted without TELEGRAM bot token")
	}
	t.Setenv("BOBRES_TELEGRAM_BOT_TOKEN", "123:abc")
	c, err := LoadBot()
	if err != nil {
		t.Fatalf("LoadBot: %v", err)
	}
	if c.TelegramBotToken != "123:abc" || c.CoreGRPCAddr != "core:9090" {
		t.Errorf("bad bot config: %+v", c)
	}
}

func TestLoadCoreRequiresPeerAddrs(t *testing.T) {
	baseEnv(t)
	if _, err := LoadCore(); err == nil {
		t.Fatal("core config accepted without peer gRPC addresses")
	}
	t.Setenv("BOBRES_PAYMENTS_GRPC_ADDR", "payments:9090")
	t.Setenv("BOBRES_PROVISIONER_GRPC_ADDR", "provisioner:9090")
	t.Setenv("BOBRES_MASTER_KEY", strings.Repeat("k", 32))
	c, err := LoadCore()
	if err != nil {
		t.Fatalf("LoadCore: %v", err)
	}
	if c.PaymentsGRPCAddr == "" || c.ProvisionerGRPCAddr == "" {
		t.Errorf("peer addrs missing: %+v", c)
	}
}

func TestLoadProvisionerRequiresMasterKey(t *testing.T) {
	baseEnv(t)
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
}
