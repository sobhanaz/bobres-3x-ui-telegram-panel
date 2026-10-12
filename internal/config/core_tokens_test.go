package config

import (
	"strings"
	"testing"
)

// Core presents a token of its own to provisioner and to the bot, never one
// another peer holds: each is strict when set, different from every other
// token core knows, and core's own token only while unset (a configuration
// from before them).
func TestLoadCorePeerTokens(t *testing.T) {
	baseEnv(t)
	t.Setenv("BOBRES_SERVICE_TOKEN", coreTok)
	t.Setenv("BOBRES_PEER_BOT_TOKEN", botTok)
	t.Setenv("BOBRES_PAYMENTS_GRPC_ADDR", "payments:9090")
	t.Setenv("BOBRES_PROVISIONER_GRPC_ADDR", "provisioner:9090")
	t.Setenv("BOBRES_PROVISIONER_TOKEN", "")
	t.Setenv("BOBRES_BOT_INTERNAL_TOKEN", "")
	t.Setenv("BOBRES_BACKUP_TOKEN", "")
	c, err := LoadCore()
	if err != nil || c.ProvisionerToken != coreTok || c.BotInternalToken != coreTok {
		t.Fatalf("unset: %v %q %q", err, c.ProvisionerToken, c.BotInternalToken)
	}

	provTok, botPeerTok, backupTok := strings.Repeat("p", 40), strings.Repeat("q", 40), strings.Repeat("d", 40)
	t.Setenv("BOBRES_PROVISIONER_TOKEN", provTok)
	t.Setenv("BOBRES_BOT_INTERNAL_TOKEN", botPeerTok)
	t.Setenv("BOBRES_BACKUP_TOKEN", backupTok)
	if c, err = LoadCore(); err != nil || c.ServiceToken != coreTok || c.ProvisionerToken != provTok ||
		c.BotInternalToken != botPeerTok || c.BackupToken != backupTok {
		t.Fatalf("set: %v %+v", err, c)
	}

	for _, name := range []string{"BOBRES_PROVISIONER_TOKEN", "BOBRES_BOT_INTERNAL_TOKEN"} {
		keep := provTok
		if name == "BOBRES_BOT_INTERNAL_TOKEN" {
			keep = botPeerTok
		}
		for bad, why := range map[string]string{
			"short": name, "CHANGE_ME_" + strings.Repeat("x", 32): name,
			coreTok: "different", botTok: "different", backupTok: "different",
		} {
			t.Setenv(name, bad)
			if _, err := LoadCore(); err == nil || !strings.Contains(err.Error(), why) {
				t.Errorf("%s=%.12s... accepted: %v", name, bad, err)
			}
		}
		t.Setenv(name, keep)
	}
	t.Setenv("BOBRES_BOT_INTERNAL_TOKEN", provTok)
	if _, err := LoadCore(); err == nil || !strings.Contains(err.Error(), "different") {
		t.Fatalf("one token for provisioner and the bot accepted: %v", err)
	}
}
