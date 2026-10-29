package config

import (
	"bytes"
	"strings"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/crypto"
)

const (
	rotMaster = "new-master-key-0123456789abcdef0123456789"                        //gitleaks:allow test fixture
	rotOld    = "old-master-key-0123456789abcdef0123456789"                        //gitleaks:allow test fixture
	rotTag    = "c827c38d6cc42925f8b006751562358f1d53e779ec09be6630c7113623a17d51" //gitleaks:allow test fixture
)

func keyEnv(t *testing.T, master, previous, tag string) {
	t.Helper()
	t.Setenv("BOBRES_MASTER_KEY", master)
	t.Setenv("BOBRES_MASTER_KEY_PREVIOUS", previous)
	t.Setenv("BOBRES_TAG_KEY", tag)
	t.Setenv("BOBRES_DATABASE_URL", "postgres://svc_core:pw@postgres:5432/bobres?sslmode=disable")
}

func TestKeyExtrasAreOptionalAndChecked(t *testing.T) {
	keyEnv(t, rotMaster, "", "")
	c, err := LoadKeyConfig()
	if err != nil || c.MasterKey != rotMaster || c.KeyExtras != (KeyExtras{}) || c.DatabaseURL == "" {
		t.Fatalf("a plain install: %+v, %v", c, err)
	}
	if k := c.KeyExtras.Keyring(c.MasterKey); k.Master != rotMaster || len(k.Previous) != 0 || k.Tag != "" {
		t.Fatalf("keyring: %+v", k)
	}

	keyEnv(t, rotMaster, rotOld, rotTag)
	c, err = LoadKeyConfig()
	if err != nil || c.KeyExtras.Previous != rotOld || c.KeyExtras.Tag != rotTag {
		t.Fatalf("a rotated install: %+v, %v", c, err)
	}
	// The same keys build the envelope that reads both and keeps the tag.
	e, err := crypto.NewKeyring(c.KeyExtras.Keyring(c.MasterKey))
	if err != nil || !e.HasPrevious() {
		t.Fatalf("envelope: %v", err)
	}
	old, _ := crypto.NewEnvelope([]byte(rotOld))
	ct, _ := old.Encrypt([]byte("secret"))
	if pt, err := e.Decrypt(ct); err != nil || !bytes.Equal(pt, []byte("secret")) {
		t.Fatalf("a value of the old key: %q %v", pt, err)
	}
	for _, bad := range []struct{ prev, tag, want string }{
		{"short", "", "BOBRES_MASTER_KEY_PREVIOUS must be at least 32"},
		{"CHANGE_ME" + strings.Repeat("x", 30), "", "placeholder"},
		{"", "nothex", "BOBRES_TAG_KEY must be 64 hexadecimal"},
		{"", rotTag[:62], "BOBRES_TAG_KEY must be 64 hexadecimal"},
	} {
		keyEnv(t, rotMaster, bad.prev, bad.tag)
		if _, err := LoadKeyConfig(); err == nil || !strings.Contains(err.Error(), bad.want) {
			t.Errorf("previous %q tag %q: %v", bad.prev, bad.tag, err)
		}
	}
	// A key is never in the error.
	keyEnv(t, rotMaster, "short", rotTag[:62])
	if _, err := LoadKeyConfig(); err != nil && (strings.Contains(err.Error(), rotTag[:62]) || strings.Contains(err.Error(), "pw@")) {
		t.Errorf("the error shows a secret: %v", err)
	}
	// No key at all is not an error here: payments may have none.
	keyEnv(t, "", "", "")
	if c, err := LoadKeyConfig(); err != nil || c.MasterKey != "" {
		t.Errorf("no master key: %+v, %v", c, err)
	}
	keyEnv(t, rotMaster, "", "")
	t.Setenv("BOBRES_DATABASE_URL", "")
	if _, err := LoadKeyConfig(); err == nil {
		t.Error("no database URL was accepted")
	}
}

func TestServiceConfigsReadTheExtras(t *testing.T) {
	// Provisioner is the service that must have a key; its config carries what a rotation leaves.
	t.Setenv("BOBRES_ENV", "dev")
	t.Setenv("BOBRES_DATABASE_URL", "postgres://svc_provisioner:pw@postgres:5432/bobres?sslmode=disable")
	t.Setenv("BOBRES_PEER_CORE_TOKEN", coreTok)
	t.Setenv("BOBRES_MASTER_KEY", rotMaster)
	t.Setenv("BOBRES_MASTER_KEY_PREVIOUS", rotOld)
	t.Setenv("BOBRES_TAG_KEY", rotTag)
	p, err := LoadProvisioner()
	if err != nil || p.KeyExtras.Previous != rotOld || p.KeyExtras.Tag != rotTag {
		t.Fatalf("provisioner: %+v, %v", p.KeyExtras, err)
	}
	t.Setenv("BOBRES_TAG_KEY", "oops")
	if _, err := LoadProvisioner(); err == nil || !strings.Contains(err.Error(), "BOBRES_TAG_KEY") {
		t.Fatalf("a bad tag key was accepted: %v", err)
	}
}

func TestBackupsCarryTheTagAndPreviousKeys(t *testing.T) {
	backupEnv(t)
	t.Setenv("BOBRES_CORE_MASTER_KEY", rotMaster)
	t.Setenv("BOBRES_CORE_TAG_KEY", rotTag)
	t.Setenv("BOBRES_CORE_MASTER_KEY_PREVIOUS", rotOld)
	for _, k := range []string{"BOBRES_PROVISIONER_TAG_KEY", "BOBRES_PAYMENTS_TAG_KEY", "BOBRES_PROVISIONER_MASTER_KEY_PREVIOUS", "BOBRES_PAYMENTS_MASTER_KEY_PREVIOUS"} {
		t.Setenv(k, "")
	}
	c, err := LoadBackup()
	if err != nil {
		t.Fatal(err)
	}
	if c.MasterKeys["BOBRES_CORE_TAG_KEY"] != rotTag || c.MasterKeys["BOBRES_CORE_MASTER_KEY_PREVIOUS"] != rotOld || c.MasterKeys["BOBRES_CORE_MASTER_KEY"] != rotMaster {
		t.Fatalf("the keys a backup carries: %v", c.MasterKeys)
	}
	// The extras are never reported missing: only the master keys are.
	if len(c.MissingKeys) != 2 {
		t.Fatalf("missing: %v", c.MissingKeys)
	}
	t.Setenv("BOBRES_CORE_TAG_KEY", "nothex")
	if _, err := LoadBackup(); err == nil || !strings.Contains(err.Error(), "BOBRES_CORE_TAG_KEY must be 64 hexadecimal") {
		t.Fatalf("a bad tag key: %v", err)
	}
	t.Setenv("BOBRES_CORE_TAG_KEY", rotTag)
	t.Setenv("BOBRES_CORE_MASTER_KEY_PREVIOUS", "short")
	if _, err := LoadBackup(); err == nil || !strings.Contains(err.Error(), "BOBRES_CORE_MASTER_KEY_PREVIOUS must be at least 32") {
		t.Fatalf("a short previous key: %v", err)
	}
}
