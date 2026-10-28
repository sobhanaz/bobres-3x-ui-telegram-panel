package rekey

import (
	"bytes"
	"strings"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/crypto"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testdb"
)

// The command as a container runs it: the database URL and the keys come from the environment, exactly as Compose passes
// them (BOBRES_MASTER_KEY, BOBRES_MASTER_KEY_PREVIOUS, BOBRES_TAG_KEY), and nothing else is read.
func TestCommandFromTheEnvironment(t *testing.T) {
	p := pool(t)
	want := seed(t, p)
	env := func(master, previous, tag string) {
		t.Setenv("BOBRES_DATABASE_URL", testdb.DSN(t))
		t.Setenv("BOBRES_MASTER_KEY", master)
		t.Setenv("BOBRES_MASTER_KEY_PREVIOUS", previous)
		t.Setenv("BOBRES_TAG_KEY", tag)
	}
	var out, errOut bytes.Buffer
	run := func(service string, args ...string) int {
		out.Reset()
		errOut.Reset()
		return Command(service, args, &out, &errOut)
	}

	// Before the rotation: the container has the key the data is sealed with. A dry run is a check, and says all is well.
	env(oldMaster, "", "")
	if code := run("provisioner", "--dry-run"); code != 0 || !strings.Contains(out.String(), "0 of 2 values are not on the current key yet") {
		t.Fatalf("dry run with the right key: %d\n%s%s", code, out.String(), errOut.String())
	}
	// With a key that is not the one of the data: refused, and it says so without showing a value.
	env(otherMaster, "", "")
	if code := run("provisioner", "--dry-run"); code != 1 || !strings.Contains(errOut.String(), "provisioner.xui_servers") || !strings.Contains(errOut.String(), "nothing was written") {
		t.Fatalf("dry run with the wrong key: %d\n%s%s", code, out.String(), errOut.String())
	}
	env(newMaster, "", "") // the new key without the old one: also refused
	if code := run("core"); code != 1 {
		t.Fatalf("a rotation with no previous key: %d", code)
	}

	// During the rotation: the new key, the old one still read, and the tag key.
	tag, err := crypto.TagKeyHex([]byte(oldMaster))
	if err != nil {
		t.Fatal(err)
	}
	env(newMaster, oldMaster, tag)
	for _, s := range Services {
		if code := run(s); code != 0 {
			t.Fatalf("%s: %d\n%s%s", s, code, out.String(), errOut.String())
		}
	}
	requireReadable(t, p, fresh(t), want)
	// And again: nothing left to do.
	if code := run("core"); code != 0 || !strings.Contains(out.String(), "0 of 8 values re-encrypted") {
		t.Fatalf("again: %d\n%s", code, out.String())
	}

	// A service without a master key has nothing encrypted: payments on an install from before it had one.
	env("", "", "")
	if code := run("payments"); code != 0 || !strings.Contains(out.String(), "no master key is set") {
		t.Fatalf("no key: %d\n%s%s", code, out.String(), errOut.String())
	}
	// Bad values are named, never shown.
	env(newMaster, "short", "")
	if code := run("core"); code != 1 || !strings.Contains(errOut.String(), "BOBRES_MASTER_KEY_PREVIOUS") || strings.Contains(errOut.String(), newMaster) {
		t.Fatalf("a short previous key: %d\n%s", code, errOut.String())
	}
	env(newMaster, "", "xyz")
	if code := run("core"); code != 1 || !strings.Contains(errOut.String(), "BOBRES_TAG_KEY") {
		t.Fatalf("a bad tag key: %d\n%s", code, errOut.String())
	}
}
