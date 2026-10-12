package config

import (
	"strings"
	"testing"
)

// The bot's update check follows BOBRES_UPDATE_CHANNEL: stable unless it
// says beta, and a value that is neither stops the bot from starting.
func TestBotUpdateChannel(t *testing.T) {
	baseEnv(t)
	t.Setenv("BOBRES_SERVICE_TOKEN", botTok)
	t.Setenv("BOBRES_CORE_GRPC_ADDR", "core:9090")
	t.Setenv("BOBRES_TELEGRAM_BOT_TOKEN", "123:abc")
	for value, want := range map[string]string{"": "stable", " ": "stable", "stable": "stable", "beta": "beta", " Beta ": "beta"} {
		t.Setenv("BOBRES_UPDATE_CHANNEL", value)
		if c, err := LoadBot(); err != nil || c.UpdateChannel != want {
			t.Errorf("%q: %q %v", value, c.UpdateChannel, err)
		}
	}
	for _, bad := range []string{"nightly", "latest", "stable beta"} {
		t.Setenv("BOBRES_UPDATE_CHANNEL", bad)
		if _, err := LoadBot(); err == nil || !strings.Contains(err.Error(), "BOBRES_UPDATE_CHANNEL must be stable or beta") {
			t.Errorf("%q accepted: %v", bad, err)
		}
	}
}
