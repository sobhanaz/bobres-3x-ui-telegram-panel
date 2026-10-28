package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/crypto"
)

const rotateKeyUsage = `usage: bobres rotate-key [--dir D] [--dry-run] [--yes] [--forget-old]
Replaces the master keys BOBRES encrypts its secrets with (BOBRES_CORE_MASTER_KEY, BOBRES_PROVISIONER_MASTER_KEY,
BOBRES_PAYMENTS_MASTER_KEY) and seals everything they protect again with the new ones: staff authenticator
secrets, 3x-ui API tokens, gateway settings, an own HTTPS private key, the license server and supplier keys.
  --dry-run     check and count every encrypted value and change nothing (no backup, no new key, no restart)
  --forget-old  do not keep the replaced keys in .env as comments
  --yes         do not ask for confirmation
BOBRES_BACKUP_KEY is another key (it encrypts the backup files, not the database) and is not changed.
See docs/runbooks/rotate-secrets.md`

// keySvc is a service that keeps encrypted values, with the .env names of its keys.
type keySvc struct {
	service string
	master  string // the key in use
	prev    string // the key a rotation replaces: set while it runs (or was cut short)
	tag     string // the key of the identifiers made with crypto.Envelope.Tag; set by the first rotation, kept
}

// keyServices are the services with a master key, in the order they start: the ones core calls come first.
var keyServices = []keySvc{
	{"provisioner", "BOBRES_PROVISIONER_MASTER_KEY", "BOBRES_PROVISIONER_MASTER_KEY_PREVIOUS", "BOBRES_PROVISIONER_TAG_KEY"},
	{"payments", "BOBRES_PAYMENTS_MASTER_KEY", "BOBRES_PAYMENTS_MASTER_KEY_PREVIOUS", "BOBRES_PAYMENTS_TAG_KEY"},
	{"core", "BOBRES_CORE_MASTER_KEY", "BOBRES_CORE_MASTER_KEY_PREVIOUS", "BOBRES_CORE_TAG_KEY"},
}

// keyServicesOf are the services of this install that have a master key. Core and provisioner must (the
// installer makes them); payments may not, on an install from before it had one.
func keyServicesOf(env map[string]string) ([]keySvc, error) {
	var out []keySvc
	for _, k := range keyServices {
		v := env[k.master]
		switch {
		case v == "" && k.service == "payments":
			continue
		case v == "":
			return nil, fmt.Errorf("%s is not set in .env: run `bobres install` once, it makes the keys that are missing", k.master)
		case len(v) < 32:
			return nil, fmt.Errorf("%s in .env is shorter than 32 characters, which no BOBRES service accepts as a master key", k.master)
		}
		if p := env[k.prev]; p != "" && len(p) < 32 {
			return nil, fmt.Errorf("%s in .env is shorter than 32 characters", k.prev)
		}
		out = append(out, k)
	}
	return out, nil
}

// keyID is the public fingerprint of a master key, the number its values carry: what the messages show of a key.
func keyID(key string) string {
	e, err := crypto.NewEnvelope([]byte(key))
	if err != nil {
		return "?"
	}
	return e.KeyID()
}

// rotateKeyCmd is `bobres rotate-key`: it replaces the master keys and seals again with the new ones every value
// the old ones sealed, in every schema. Exit 0 when done (or a dry run that found nothing wrong), 1 when it was
// refused or failed, 2 for usage errors. See docs/runbooks/rotate-secrets.md.
func (r *rotator) rotateKeyCmd(args []string) int {
	fs := flag.NewFlagSet("rotate-key", flag.ContinueOnError)
	fs.SetOutput(r.errOut)
	dir := fs.String("dir", defaultDir(), dirUsage)
	dry := fs.Bool("dry-run", false, "check and count every encrypted value, and change nothing")
	yes := fs.Bool("yes", false, "do not ask for confirmation")
	forget := fs.Bool("forget-old", false, "do not keep the replaced keys in .env as comments")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return 2
	}
	if len(pos) != 0 {
		fmt.Fprintln(r.errOut, rotateKeyUsage)
		return 2
	}
	if *dry && *forget {
		fmt.Fprintln(r.errOut, "error: --forget-old changes .env, and a dry run changes nothing")
		return 2
	}
	env, ok := r.requireInstall(*dir)
	if !ok {
		return 2
	}
	svcs, err := keyServicesOf(env)
	if err != nil {
		fmt.Fprintf(r.errOut, "error: %v. Nothing was changed.\n", err)
		return 1
	}
	// One update, rollback, restore or rotation at a time.
	unlock, err := r.lock(*dir)
	if err != nil {
		fmt.Fprintf(r.errOut, "error: %v. Nothing was changed.\n", err)
		return 1
	}
	defer unlock()
	k := &keyRotation{r: r, dir: *dir, envPath: filepath.Join(*dir, ".env"), env: env, svcs: svcs, yes: *yes, forget: *forget}
	k.resuming = k.unfinished()
	if *dry {
		return k.dryRun(context.Background())
	}
	return k.run(context.Background())
}

// keyRotation is one run of rotate-key.
type keyRotation struct {
	r       *rotator
	dir     string
	envPath string
	env     map[string]string // .env as the run began
	svcs    []keySvc
	yes     bool
	forget  bool
	// resuming: .env already holds a previous key, because an earlier run was cut short. This run finishes that
	// rotation with the keys .env has; it makes no new key.
	resuming bool
}

// copyPath is where the .env from before the rotation is kept, until the rotation holds.
func (k *keyRotation) copyPath() string { return k.envPath + ".before-key-rotation" }

// unfinished: some service still has a previous key in .env.
func (k *keyRotation) unfinished() bool {
	for _, s := range k.svcs {
		if k.env[s.prev] != "" {
			return true
		}
	}
	return false
}

// requireStack refuses to begin unless the store is up and healthy. A cut-short rotation may have left services
// stopped: finishing it needs only the database.
func (k *keyRotation) requireStack(ctx context.Context) error {
	ps, err := k.r.run(ctx, k.dir, composeArgs("ps", "--format", "json")...)
	if err != nil {
		return fmt.Errorf("docker compose ps failed: %w: %s", err, strings.TrimSpace(string(ps)))
	}
	health := parseHealth(ps)
	need := []string{"postgres"}
	if !k.resuming {
		need = append(need, "redis")
		need = append(need, appServices(k.dir, k.env)...)
	}
	var bad []string
	for _, s := range need {
		if health[s] != "healthy" {
			bad = append(bad, s+"="+orUnknown(health[s]))
		}
	}
	if len(bad) > 0 {
		return fmt.Errorf("the store is not healthy (%s): start it and look at it first (bobres start, bobres status), then run this again", strings.Join(bad, ", "))
	}
	return nil
}

// rekey runs each service's `rekey` in a container of its own, with the keys .env has now. Their output is the
// user's. args are given to it (--dry-run).
func (k *keyRotation) rekey(ctx context.Context, args ...string) error {
	for _, s := range k.svcs {
		fmt.Fprintf(k.r.out, "\n%s:\n", s.service)
		cargs := append([]string{"run", "--rm", "--no-deps", "-T", s.service, "rekey"}, args...)
		if err := k.r.stream(ctx, k.dir, composeArgs(cargs...)...); err != nil {
			return fmt.Errorf("%s: %w", s.service, err)
		}
	}
	return nil
}

func (k *keyRotation) dryRun(ctx context.Context) int {
	o := k.r
	if err := k.requireStack(ctx); err != nil {
		fmt.Fprintf(o.errOut, "error: %v. Nothing was changed.\n", err)
		return 1
	}
	fmt.Fprintln(o.out, "Checking every encrypted value against the keys in .env (nothing is written)...")
	if err := k.rekey(ctx, "--dry-run"); err != nil {
		fmt.Fprintf(o.errOut, "\nerror: %v\nNothing was changed. The keys in .env must read every encrypted value before they can be replaced; see docs/runbooks/rotate-secrets.md.\n", err)
		return 1
	}
	if k.resuming {
		fmt.Fprintln(o.out, "\nAn earlier rotation was cut short: the values counted above as to re-encrypt are what is left of it. "+
			"Run bobres rotate-key (without --dry-run) to finish it.")
	}
	fmt.Fprintln(o.out, "\nDry run: nothing was changed. A real run takes and checks a backup, makes new keys, stops the BOBRES services, "+
		"seals everything again, and starts them.")
	return 0
}

func (k *keyRotation) run(ctx context.Context) int {
	o := k.r
	if err := k.requireStack(ctx); err != nil {
		fmt.Fprintf(o.errOut, "error: %v. Nothing was changed.\n", err)
		return 1
	}
	if gb := o.freeGB(k.dir); gb >= 0 && gb < minUpdateGB {
		fmt.Fprintf(o.errOut, "error: %.1f GB free in %s: a rotation needs at least %d GB there (the backup). Free some space and try again. Nothing was changed.\n", gb, k.dir, minUpdateGB)
		return 1
	}
	fmt.Fprintln(o.out, "Checking that the keys in .env read every encrypted value (nothing is written)...")
	if err := k.rekey(ctx, "--dry-run"); err != nil {
		fmt.Fprintf(o.errOut, "\nerror: %v\nNothing was changed. The keys in .env must read every encrypted value before they can be replaced; see docs/runbooks/rotate-secrets.md.\n", err)
		return 1
	}
	prompt := "\nReplace the master keys? A backup is taken and checked first. Then the BOBRES services stop, everything the keys protect is sealed\n" +
		"again with the new keys (a minute or so; the database stays up), and the services start again. If anything fails, the old keys are\n" +
		"put back. BOBRES_BACKUP_KEY does not change. [y/N]: "
	if k.resuming {
		prompt = "\nAn earlier rotation was cut short. Finish it? A backup is taken and checked first; then the BOBRES services stop, what is left\n" +
			"is sealed again with the keys .env has, and the services start again. [y/N]: "
	}
	if !k.yes && !o.confirm(prompt) {
		fmt.Fprintln(o.out, "Cancelled. Nothing was changed.")
		return 1
	}

	safety, err := o.safetyBackup(ctx, k.dir, k.env, "before-key-rotation", "made by bobres rotate-key before it changed the master keys")
	if err != nil {
		fmt.Fprintf(o.errOut, "error: the backup before the rotation failed: %v. Nothing was changed.\n", err)
		return 1
	}
	backupPath := filepath.Join(k.dir, "backups", safety)
	sum, err := verifyBackupFile(backupPath, []byte(k.env["BOBRES_BACKUP_KEY"]))
	if err != nil {
		fmt.Fprintf(o.errOut, "error: the backup %s was written but does not read back: %v. Nothing was changed.\n", backupPath, err)
		return 1
	}
	if missing := missingKeys(sum.KeyNames, k.svcs); len(missing) > 0 {
		fmt.Fprintf(o.errOut, "error: the backup %s does not carry %s. Nothing was changed.\n", backupPath, strings.Join(missing, ", "))
		return 1
	}
	fmt.Fprintf(o.out, "Backup saved and checked: %s\n", backupPath)

	forward, err := k.forwardEnv()
	if err != nil {
		fmt.Fprintf(o.errOut, "error: %v. Nothing was changed.\n", err)
		return 1
	}
	orig, err := os.ReadFile(k.envPath) //nolint:gosec // the install's own file
	if err != nil {
		fmt.Fprintf(o.errOut, "error: read .env: %v. Nothing was changed.\n", err)
		return 1
	}
	// The .env from before is kept until the rotation holds, for a run cut short. A run that finishes an earlier one keeps
	// the copy that one made: it is the older, the one from before the first key was changed.
	madeCopy := !k.resuming || !exists(k.copyPath())
	if madeCopy {
		if err := writeAtomic(k.copyPath(), orig, 0o600); err != nil {
			fmt.Fprintf(o.errOut, "error: keep a copy of .env: %v. Nothing was changed.\n", err)
			return 1
		}
	}
	dropCopy := func() {
		if madeCopy {
			_ = os.Remove(k.copyPath())
		}
	}

	if err := o.stopApp(ctx, k.dir, k.env); err != nil {
		fmt.Fprintf(o.errOut, "error: %v\nStarting the services again as they were...\n", err)
		k.restartAsBefore(ctx)
		dropCopy()
		return 1
	}
	if err := writeEnv(k.envPath, forward); err != nil {
		fmt.Fprintf(o.errOut, "error: %v\nStarting the services again as they were...\n", err)
		k.restartAsBefore(ctx)
		dropCopy()
		return 1
	}
	fmt.Fprintln(o.out, "The new keys are in .env; the old ones are still read. Sealing everything again...")
	if err := k.rekey(ctx); err != nil {
		fmt.Fprintf(o.errOut, "\nerror: %v\n", err)
		return k.undo(ctx, orig, forward, "the re-encryption failed")
	}
	retired := retiredKeyBlock(o.now(), k.retiredNow(forward))
	final := maps.Clone(forward)
	for _, s := range k.svcs {
		delete(final, s.prev)
	}
	if err := writeAtomic(k.envPath, renderEnvRotated(k.envPath, final, retired, k.forget), 0o600); err != nil {
		fmt.Fprintf(o.errOut, "error: %v\n", err)
		return k.undo(ctx, orig, forward, "the new .env could not be written")
	}
	fmt.Fprintln(o.out, "Everything is sealed with the new keys. Starting the services...")
	if code := k.start(ctx, final); code != 0 {
		fmt.Fprintln(o.errOut, "\nerror: the services did not come back healthy with the new keys.")
		return k.undo(ctx, orig, forward, "the services were not healthy")
	}

	_ = os.Remove(k.copyPath())
	k.report(forward, final, backupPath)
	return 0
}

// missingKeys names the master keys of svcs that the backup does not carry.
func missingKeys(carried []string, svcs []keySvc) []string {
	have := map[string]bool{}
	for _, n := range carried {
		have[n] = true
	}
	var out []string
	for _, s := range svcs {
		if !have[s.master] {
			out = append(out, s.master)
		}
	}
	return out
}

// forwardEnv is .env while the rotation runs: the new key is the master key, the old one is the previous key (still
// read), and the tag key is kept at what the old master key made, so identifiers made with it do not change. A run
// that finishes an earlier one changes nothing.
func (k *keyRotation) forwardEnv() (map[string]string, error) {
	next := maps.Clone(k.env)
	if k.resuming {
		return next, nil
	}
	for _, s := range k.svcs {
		old := k.env[s.master]
		tag := k.env[s.tag]
		if tag == "" {
			var err error
			if tag, err = crypto.TagKeyHex([]byte(old)); err != nil {
				return nil, fmt.Errorf("%s: %w", s.master, err)
			}
		}
		fresh := k.r.random(32)
		if fresh == old {
			return nil, errors.New("the new key came out equal to the old one")
		}
		next[s.master], next[s.prev], next[s.tag] = fresh, old, tag
	}
	return next, nil
}

// swapped is .env with the keys the other way round: the old key is the master key again, the new one still read.
func (k *keyRotation) swapped(forward map[string]string) map[string]string {
	back := maps.Clone(forward)
	for _, s := range k.svcs {
		back[s.master], back[s.prev] = forward[s.prev], forward[s.master]
	}
	return back
}

// retiredNow are the keys this rotation replaced, under the .env names the comments carry.
func (k *keyRotation) retiredNow(forward map[string]string) map[string]string {
	old := map[string]string{}
	for _, s := range k.svcs {
		old[s.prev] = forward[s.prev]
	}
	return old
}

// renderEnvRotated renders .env with the retired keys: the ones already there and the new block, or none at all
// (forget).
func renderEnvRotated(path string, env map[string]string, add []string, forget bool) []byte {
	prev, _ := os.ReadFile(path) //nolint:gosec // the install's own .env
	out := renderEnvComments(env, envComments(prev))
	if forget {
		return out
	}
	return appendRetiredKeys(out, append(retiredKeyLines(prev), add...))
}

// start starts the services that stopped, in the order they depend on each other: the ones core calls, core, the
// rest. It waits for each stage to be healthy.
func (k *keyRotation) start(ctx context.Context, env map[string]string) int {
	all := dataServices(k.dir, env) // the tls-agent has no keys and was not stopped
	first := []string{"provisioner", "payments"}
	rest := make([]string, 0, len(all))
	for _, s := range all {
		if s != "provisioner" && s != "payments" && s != "core" {
			rest = append(rest, s)
		}
	}
	for _, stage := range [][]string{first, {"core"}, rest} {
		stage = installedOnly(k.dir, stage, env)
		if len(stage) == 0 {
			continue
		}
		if code := k.r.upAndWait(ctx, k.dir, stage); code != 0 {
			return code
		}
	}
	return 0
}

// restartAsBefore starts the services again with .env as it was, when the run stopped them and could not go on.
func (k *keyRotation) restartAsBefore(ctx context.Context) {
	if code := k.start(ctx, k.env); code != 0 {
		fmt.Fprintf(k.r.errOut, "The services did not come back: bobres logs --dir %s core. .env was not changed.\n", k.dir)
		return
	}
	fmt.Fprintln(k.r.errOut, "Nothing was changed.")
}

// undo puts the old keys back after a failure: the keys swapped, everything sealed again with the old one, then the
// .env from before, byte for byte, and the services. When it cannot, it leaves the store running with both keys, in
// which every value is readable, and says how to finish. The forward .env is what a rotation runs with.
func (k *keyRotation) undo(ctx context.Context, orig []byte, forward map[string]string, why string) int {
	o := k.r
	fmt.Fprintf(o.errOut, "Putting the old keys back (%s)...\n", why)
	leaveBoth := func(reason string) int {
		// .env as it was, with both keys: not with the retired keys of this very rotation, which did not take.
		both := appendRetiredKeys(renderEnvComments(forward, envComments(orig)), retiredKeyLines(orig))
		if err := writeAtomic(k.envPath, both, 0o600); err != nil {
			fmt.Fprintf(o.errOut, "error: %v\nThe .env from before is in %s. See docs/runbooks/rotate-secrets.md.\n", err, k.copyPath())
			return 1
		}
		code := k.start(ctx, forward)
		copyNote := ""
		if exists(k.copyPath()) {
			copyNote = fmt.Sprintf("\nThe .env from before the rotation is in %s.", k.copyPath())
		}
		fmt.Fprintf(o.errOut, "\n%s\nThe store runs with both the old and the new key, which reads every value. Nothing is lost. "+
			"Look at the messages above, then run `sudo bobres rotate-key` again: it finishes the rotation.%s\n", reason, copyNote)
		if code != 0 {
			fmt.Fprintf(o.errOut, "The services are not healthy yet: bobres logs --dir %s core\n", k.dir)
		}
		return 1
	}
	if k.resuming {
		return leaveBoth("This run finished an earlier rotation and could not.")
	}
	if err := o.stopApp(ctx, k.dir, forward); err != nil {
		fmt.Fprintf(o.errOut, "warning: %v\n", err)
	}
	if err := writeEnv(k.envPath, k.swapped(forward)); err != nil {
		fmt.Fprintf(o.errOut, "error: %v\n", err)
		return leaveBoth("The old keys could not be put back.")
	}
	if err := k.rekey(ctx); err != nil {
		fmt.Fprintf(o.errOut, "\nerror: %v\n", err)
		return leaveBoth("The old keys could not be put back.")
	}
	if err := writeAtomic(k.envPath, orig, 0o600); err != nil {
		fmt.Fprintf(o.errOut, "error: %v\n", err)
		return leaveBoth("The old keys could not be put back.")
	}
	if code := k.start(ctx, k.env); code != 0 {
		fmt.Fprintf(o.errOut, "The old keys are back in .env and in the database, but the services are not healthy: the cause is elsewhere (bobres logs --dir %s core).\n", k.dir)
		_ = os.Remove(k.copyPath())
		return 1
	}
	_ = os.Remove(k.copyPath())
	fmt.Fprintf(o.errOut, "The old keys are back and the services are healthy: nothing was rotated (%s). See the messages above.\n", why)
	return 1
}

// report says what the rotation did. It names no key, only the public fingerprints of them.
func (k *keyRotation) report(forward, final map[string]string, backupPath string) {
	o := k.r
	fmt.Fprintln(o.out, "\nDone: the master keys were replaced and everything they protect was sealed again.")
	for _, s := range k.svcs {
		fmt.Fprintf(o.out, "  %-12s key %s (was %s)\n", s.service, keyID(final[s.master]), keyID(forward[s.prev]))
	}
	if k.forget {
		fmt.Fprintln(o.out, "The old keys are not kept in .env (--forget-old).")
	} else {
		fmt.Fprintf(o.out, "The old keys are kept in %s as comments, so that a database copy or a backup older than this can still be opened.\n"+
			"Delete those lines when you no longer need such copies (sudo bobres rotate-key --forget-old does it at the next rotation).\n", k.envPath)
	}
	fmt.Fprintf(o.out, "The backup made before it, %s, holds the old keys and the old data: it restores as it was, and it is as secret as the old keys.\n"+
		"BOBRES_BACKUP_KEY did not change. Make a backup now, so that the newest one holds the new keys: sudo bobres backup\n", backupPath)
}

// keyHealthNote is a line for bobres check when a rotation was cut short.
func keyHealthNote(env map[string]string) string {
	for _, k := range keyServices {
		if env[k.prev] != "" {
			return "a master key rotation was left unfinished (both keys are read, nothing is lost): sudo bobres rotate-key finishes it"
		}
	}
	return ""
}
