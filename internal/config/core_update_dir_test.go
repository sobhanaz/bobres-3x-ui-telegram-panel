package config

import (
	"strings"
	"testing"
)

// The folder core shares with the server's updater: /run/bobres-update unless
// BOBRES_UPDATE_DIR names another absolute path.
func TestLoadCoreUpdateDir(t *testing.T) {
	baseEnv(t)
	t.Setenv("BOBRES_SERVICE_TOKEN", coreTok)
	t.Setenv("BOBRES_PEER_BOT_TOKEN", botTok)
	t.Setenv("BOBRES_PAYMENTS_GRPC_ADDR", "payments:9090")
	t.Setenv("BOBRES_PROVISIONER_GRPC_ADDR", "provisioner:9090")
	for _, unset := range []string{"", "   "} {
		t.Setenv("BOBRES_UPDATE_DIR", unset)
		if c, err := LoadCore(); err != nil || c.UpdateDir != "/run/bobres-update" {
			t.Fatalf("default for %q: %q %v", unset, c.UpdateDir, err)
		}
	}
	t.Setenv("BOBRES_UPDATE_DIR", " /srv/bobres-update/ ")
	if c, err := LoadCore(); err != nil || c.UpdateDir != "/srv/bobres-update" {
		t.Fatalf("set: %q %v", c.UpdateDir, err)
	}
	for _, bad := range []string{"relative/dir", "./here", "bobres-update", "~/x"} {
		t.Setenv("BOBRES_UPDATE_DIR", bad)
		if _, err := LoadCore(); err == nil || !strings.Contains(err.Error(), "BOBRES_UPDATE_DIR") {
			t.Errorf("%q was accepted: %v", bad, err)
		}
	}
}
