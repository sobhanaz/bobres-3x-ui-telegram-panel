package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/migrate"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testdb"
)

func TestMain(m *testing.M) { testdb.Main(m) }

// coreEnv is the environment `/app license` runs with in the core container.
func coreEnv(t *testing.T) {
	t.Helper()
	dsn := testdb.DSN(t)
	if err := migrate.Up(context.Background(), dsn, "core"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for k, v := range map[string]string{
		"BOBRES_ENV": "prod", "BOBRES_DATABASE_URL": dsn, "BOBRES_DOMAIN": "Panel.Example.com",
		"BOBRES_SERVICE_TOKEN": strings.Repeat("c", 40), "BOBRES_PEER_BOT_TOKEN": strings.Repeat("b", 40),
		"BOBRES_PAYMENTS_GRPC_ADDR": "payments:9090", "BOBRES_PROVISIONER_GRPC_ADDR": "provisioner:9090",
	} {
		t.Setenv(k, v)
	}
}

// license info answers this install's id and domain from the store, as
// `bobres license activate` reads them; an activated import is checked like
// any other (this build trusts no key yet, so it is refused).
func TestLicenseInfoAndImportWithTheStore(t *testing.T) {
	coreEnv(t)
	var out, errOut bytes.Buffer
	if code := licenseCmd([]string{"info", "--json"}, strings.NewReader(""), &out, &errOut); code != 0 {
		t.Fatalf("info: %d %s", code, errOut.String())
	}
	var info struct {
		InstallID string  `json:"install_id"`
		Domain    string  `json:"domain"`
		State     string  `json:"state"`
		Source    *string `json:"source"`
	}
	if err := json.Unmarshal(out.Bytes(), &info); err != nil || len(info.InstallID) != 36 || info.Domain != "panel.example.com" ||
		info.State != "missing" || info.Source != nil {
		t.Fatalf("info: %+v %v\n%s", info, err, out.String())
	}
	out.Reset()
	errOut.Reset()
	code := licenseCmd([]string{"import", "--activated"}, strings.NewReader("eyJ2IjoxfQ.c2ln\n"), &out, &errOut)
	if code != 1 || !strings.Contains(errOut.String(), "not installed:") || !strings.Contains(errOut.String(), "(unknown_key)") &&
		!strings.Contains(errOut.String(), "(bad_signature)") {
		t.Fatalf("import: %d %q %q", code, out.String(), errOut.String())
	}
}
