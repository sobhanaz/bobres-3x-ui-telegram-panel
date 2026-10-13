package deploy

import (
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/config"
)

// loadAs runs load with only the environment compose gives service for the
// .env values env.
func loadAs[T any](t *testing.T, service string, env map[string]string, load func() (T, error)) T {
	t.Helper()
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); strings.HasPrefix(k, "BOBRES_") {
			t.Setenv(k, "")
		}
	}
	for k, v := range composeEnv(t, service, env) {
		t.Setenv(k, v)
	}
	c, err := load()
	if err != nil {
		t.Fatalf("%s does not start with the compose environment: %v", service, err)
	}
	return c
}

// Core presents a different token to each service it calls, and each one
// accepts exactly the token core presents to it, and no other container
// holds it. With one shared token any of them could act as core toward the
// others: a compromised bot could change the payment gateways on payments or
// delete every VPN account on provisioner, and a compromised payments could
// send messages as the store's bot.
func TestComposeGivesEachPeerItsOwnCoreToken(t *testing.T) {
	env := backupInstallEnv(nil)
	core := loadAs(t, "core", env, config.LoadCore)
	presents := map[string]string{ // what core presents to each peer
		"payments": core.ServiceToken, "provisioner": core.ProvisionerToken,
		"bot": core.BotInternalToken, "backup": core.BackupToken,
	}
	accepts := map[string]string{ // what each peer accepts as core
		"payments":    loadAs(t, "payments", env, config.LoadPayments).CoreToken,
		"provisioner": loadAs(t, "provisioner", env, config.LoadProvisioner).CoreToken,
		"bot":         loadAs(t, "bot", env, config.LoadBot).PeerCoreToken,
		"backup":      loadAs(t, "backup", env, config.LoadBackup).CoreToken,
	}
	seen := map[string]string{}
	for peer, tok := range presents {
		if tok == "" || accepts[peer] != tok {
			t.Errorf("core presents to %s a token it does not accept as core", peer)
		}
		if other, dup := seen[tok]; dup && tok != "" {
			t.Errorf("core presents one token to both %s and %s", other, peer)
		}
		seen[tok] = peer
	}
	all := []string{"caddy", "postgres", "redis", "core", "bot", "payments", "provisioner", "backup"}
	for peer, tok := range accepts {
		for _, svc := range all {
			if svc == "core" || svc == peer {
				continue
			}
			for k, v := range composeEnv(t, svc, env) {
				if v == tok {
					t.Errorf("%s holds the token %s accepts as core (%s): it could act as core there", svc, peer, k)
				}
			}
		}
	}

	// Docker's own reading of the file agrees (where docker is installed; the
	// template's COMPOSE_PROFILES turns the backup service on).
	if _, err := exec.LookPath("docker"); err == nil {
		for _, svc := range []string{"core", "bot", "payments", "provisioner", "backup"} {
			if got, want := dockerComposeEnv(t, svc, env), composeEnv(t, svc, env); !reflect.DeepEqual(got, want) {
				t.Errorf("docker compose config gives %s\n%v\ncomposeEnv\n%v", svc, got, want)
			}
		}
	}
}
