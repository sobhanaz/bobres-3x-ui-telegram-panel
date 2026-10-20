package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/backup"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/config"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/s3"
)

const restoreUsage = "usage: bobres restore <file | s3://bucket/key> [--dir D] [--key-file F] [--yes] [--keep-keys]"

// s3Download bounds downloading a backup from S3-compatible storage.
const s3Download = 2 * time.Hour

// dumpScript dumps the whole database in pg_dump's custom format to stdout.
// It runs in the postgres container as the superuser over the local socket
// (which the image trusts), so no password is needed or shown.
const dumpScript = `exec pg_dump --username "$POSTGRES_USER" --format=custom --no-password "$POSTGRES_DB"`

// restoreScript loads a pg_dump custom archive read on stdin into the
// database in ONE transaction. It drops the three service schemas first, so
// that tables a later version added after the backup cannot block the
// restore (pg_restore --clean drops only what the archive holds, and a schema
// that still has other tables refuses to go), then replays the archive, which
// pg_restore turns into SQL for psql. COMMIT is sent only once pg_restore has
// read the whole archive; when it fails, psql gets an error instead and the
// server rolls everything back. Owners and grants come from the archive: the
// service roles exist on every install (postgres-init.sh).
const restoreScript = `{
  echo 'BEGIN;'
  echo 'DROP SCHEMA IF EXISTS core, payments, provisioner CASCADE;'
  if pg_restore --clean --if-exists --no-password --file=-; then
    echo 'COMMIT;'
  else
    printf '\n\\.\nDO $$ BEGIN RAISE EXCEPTION $m$pg_restore failed: the database is unchanged$m$; END $$;\n'
  fi
} | PGOPTIONS='-c client_min_messages=warning' psql -X -q -v ON_ERROR_STOP=1 --no-password --output=/dev/null --username "$POSTGRES_USER" --dbname "$POSTGRES_DB"`

// restoreCmd replaces the database with a backup's: it checks the whole file
// first, stops the app services, takes a safety backup of the current data,
// loads the dump in one transaction, puts the backup's master keys in .env,
// then starts and waits until healthy. The backup is a file, or an object in
// S3-compatible storage (s3://bucket/key, with this install's S3 settings),
// downloaded first. Exit 0 when done, 1 when refused or failed, 2 for usage
// errors.
func (o *ops) restoreCmd(args []string) int {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	fs.SetOutput(o.errOut)
	dir := fs.String("dir", defaultDir(), dirUsage)
	keyFile := fs.String("key-file", "", "file holding the backup key on one line (for a backup made on another server)")
	yes := fs.Bool("yes", false, "do not ask for confirmation")
	keepKeys := fs.Bool("keep-keys", false, "keep this install's master keys in .env even where the backup's differ")
	files, err := parseArgs(fs, args)
	if err != nil {
		return 2
	}
	if len(files) != 1 {
		fmt.Fprintln(o.errOut, restoreUsage)
		return 2
	}
	source := files[0]
	env, ok := o.requireInstall(*dir)
	if !ok {
		return 2
	}
	// One restore, update or rollback at a time (the dashboard's updater takes the same lock).
	unlock, err := o.lock(*dir)
	if err != nil {
		fmt.Fprintf(o.errOut, "error: %v. Nothing was changed.\n", err)
		return 1
	}
	defer unlock()
	// The safety backup is made with this install's own key, whatever opens
	// the file: said now, before the file is read and the operator asked.
	if len(strings.TrimSpace(env["BOBRES_BACKUP_KEY"])) < backup.MinKeyLen {
		fmt.Fprintf(o.errOut, "error: this install has no backup key (BOBRES_BACKUP_KEY in .env), and the restore backs up the current data with it first.\n"+
			"Run `sudo bobres install --dir %s` once (it keeps the settings and creates the key), then restore again\n"+
			"(with --key-file for a backup made on another server). Nothing was changed.\n", *dir)
		return 1
	}
	key, err := restoreKey(env, *keyFile)
	if err != nil {
		fmt.Fprintf(o.errOut, "error: %v. Nothing was changed.\n", err)
		return 1
	}

	ctx := context.Background()
	// The file is opened once: the check and the restore read the same open
	// file, so whatever is put at its path in between (backups/ is the backup
	// service user's) is not what gets restored.
	var file *os.File
	if strings.HasPrefix(source, s3Scheme) {
		dl, done, err := o.downloadBackup(ctx, *dir, env, source)
		if err != nil {
			fmt.Fprintf(o.errOut, "error: %s: %v. Nothing was changed.\n", source, err)
			return 1
		}
		defer done()
		file = dl
	} else {
		file, err = os.Open(source) //nolint:gosec // the operator's own file
		if err != nil {
			fmt.Fprintf(o.errOut, "error: %s: %v. Nothing was changed.\n", source, err)
			return 1
		}
		defer file.Close() //nolint:errcheck // read-only
	}

	// 1. The whole file is checked before anything is touched.
	sum, err := verifyBackup(file, key)
	if err == nil {
		if _, serr := file.Seek(0, io.SeekStart); serr != nil {
			err = fmt.Errorf("it cannot be read a second time for the restore (a file is needed, not a pipe): %w", serr)
		}
	}
	if err != nil {
		fmt.Fprintf(o.errOut, "error: %s: %v. Nothing was changed.\n", source, err)
		return 1
	}
	describeBackup(o.out, source, sum)
	if !*yes && !o.confirm(fmt.Sprintf("\nThis REPLACES all current data of %s (customers, orders, ledger, settings) with the backup's.\n"+
		"A backup of the current data is taken first. Continue? [y/N]: ", orDash(env["BOBRES_DOMAIN"]))) {
		fmt.Fprintln(o.out, "Cancelled. Nothing was changed.")
		return 1
	}

	// The services stop first, so that nothing is written after the safety
	// backup is taken (it would be in neither that backup nor the restore).
	if err := o.stopApp(ctx, *dir, env); err != nil {
		fmt.Fprintf(o.errOut, "error: %v. Nothing was changed; starting the services again...\n", err)
		o.startAndWait(ctx, *dir)
		return 1
	}
	safety, err := o.safetyBackup(ctx, *dir, env, "before-restore", "made by bobres restore before it replaced the data")
	if err != nil {
		fmt.Fprintf(o.errOut, "error: the backup of the current data failed: %v. Nothing was changed; starting the services again...\n", err)
		o.startAndWait(ctx, *dir)
		return 1
	}
	safetyPath := filepath.Join(*dir, "backups", safety)
	fmt.Fprintf(o.out, "Current data saved in %s\n", safetyPath)

	archiveKeys, err := o.loadDumpFrom(ctx, *dir, file, key)
	if err != nil {
		fmt.Fprintf(o.errOut, "error: the restore failed: %v\nThe database was not changed (the restore runs in one transaction). Starting the services again...\n", err)
		o.startAndWait(ctx, *dir)
		return 1
	}
	fmt.Fprintln(o.out, "Database restored.")

	changed, err := applyMasterKeys(*dir, archiveKeys, *keepKeys)
	if err != nil {
		fmt.Fprintf(o.errOut, "error: the database is restored, but .env could not take the backup's master keys: %v\n"+
			"Fix it and run: bobres restore %s --yes   (or go back with: bobres restore %s --yes)\n", err, source, safetyPath)
		return 1
	}
	o.reportKeys(changed, *keepKeys)
	code := o.startAndWait(ctx, *dir)
	o.healthHints(*dir, code == 0, *keepKeys && len(changed) > 0)
	if code != 0 {
		fmt.Fprintf(o.errOut, "The data is restored but the store is not healthy yet. To go back to the data from before: bobres restore %s --yes\n", safetyPath)
		return 1
	}
	fmt.Fprintf(o.out, "\nRestore complete: the store runs on the data of %s (backup made %s).\nThe data from before the restore is in %s\n",
		path.Base(filepath.ToSlash(source)), sum.Created.Format("2006-01-02 15:04 MST"), safetyPath)
	return 0
}

// s3Scheme marks a backup in S3-compatible storage: s3://bucket/key.
const s3Scheme = "s3://"

// parseS3Source splits s3://bucket/key.
func parseS3Source(src string) (bucket, key string, err error) {
	rest := strings.TrimPrefix(src, s3Scheme)
	bucket, key, _ = strings.Cut(rest, "/")
	switch {
	case !config.ValidBucket(bucket):
		return "", "", fmt.Errorf("%q is not a bucket name (s3://bucket/folder/bobres-<stamp>.bkp)", bucket)
	case key == "" || strings.HasSuffix(key, "/"):
		return "", "", errors.New("no object key after the bucket (s3://bucket/folder/bobres-<stamp>.bkp)")
	}
	return bucket, key, nil
}

// downloadBackup downloads s3://bucket/key with this install's S3 settings
// (its endpoint, region and keys; the bucket is the one named) into a new
// folder in <dir>/backups, which is on the disk meant for backups. The
// folder is the running user's (root), mode 0700, and the file 0600, so the
// backup service's user, who owns backups/, cannot write to the download.
// That user can still rename the folder (renaming within backups/ needs
// write permission on backups/ only), so the file is never opened again by
// its path: it comes back open, at its start, and the check and the restore
// both read that open file. done closes it and removes the folder.
func (o *ops) downloadBackup(ctx context.Context, dir string, env map[string]string, src string) (*os.File, func(), error) {
	bucket, key, err := parseS3Source(src)
	if err != nil {
		return nil, nil, err
	}
	settings, err := config.ParseBackupS3(env)
	if err != nil || !settings.Enabled() {
		why := "this install has no S3 settings"
		if err != nil {
			why = "this install's S3 settings are not usable: " + strings.ReplaceAll(err.Error(), "\n", "; ")
		}
		return nil, nil, fmt.Errorf("%s (BOBRES_BACKUP_S3_ENDPOINT and its keys in .env; sudo bobres install --s3-endpoint <url> --s3-bucket <bucket> "+
			"sets them up). Or download the file yourself and restore it by its path", why)
	}
	cfg := s3.Config{Endpoint: settings.Endpoint, Bucket: settings.Bucket, Region: settings.Region,
		AccessKey: settings.AccessKey, SecretKey: settings.SecretKey, HTTPClient: o.s3c}.WithBucket(bucket)
	client, err := s3.New(cfg)
	if err != nil {
		return nil, nil, err
	}
	bdir := filepath.Join(dir, "backups")
	if err := os.MkdirAll(bdir, 0o700); err != nil {
		return nil, nil, err
	}
	tmp, err := os.MkdirTemp(bdir, ".s3-restore-*")
	if err != nil {
		return nil, nil, err
	}
	name := path.Base(key)
	if !safeFileRe.MatchString(name) {
		name = "backup.bkp"
	}
	f, err := os.OpenFile(filepath.Join(tmp, name), os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // in our own new folder
	if err != nil {
		_ = os.RemoveAll(tmp)
		return nil, nil, err
	}
	done := func() {
		_ = f.Close()
		_ = os.RemoveAll(tmp)
	}
	fmt.Fprintf(o.out, "Downloading %s into %s...\n", src, bdir)
	dctx, cancel := context.WithTimeout(ctx, s3Download)
	defer cancel()
	n, err := client.Get(dctx, key, f)
	if err == nil {
		err = f.Sync()
	}
	if err == nil {
		_, err = f.Seek(0, io.SeekStart)
	}
	if err != nil {
		done()
		if s3.IsNotFound(err) {
			return nil, nil, fmt.Errorf("there is no such backup in the bucket (%w)", err)
		}
		return nil, nil, fmt.Errorf("the download failed: %w", err)
	}
	fmt.Fprintf(o.out, "Downloaded %s.\n", humanSize(n))
	return f, done, nil
}

// safeFileRe is a plain file name: what a downloaded backup is saved as.
var safeFileRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,200}$`)

// parseArgs parses flags placed before or after the positional arguments
// (bobres restore FILE --yes), where flag.Parse alone stops at the first
// positional one. It returns the positional arguments.
func parseArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return pos, nil
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

// confirm asks a yes/no question; anything but y or yes is no.
func (o *ops) confirm(prompt string) bool {
	fmt.Fprint(o.out, prompt)
	var answer string
	fmt.Fscanln(o.in, &answer) //nolint:errcheck // an empty answer means no
	answer = strings.TrimSpace(answer)
	return strings.EqualFold(answer, "y") || strings.EqualFold(answer, "yes")
}

// restoreKey is the key that opens the backup: the one line of keyFile (a
// backup from another server, whose install made its own key), else this
// install's BOBRES_BACKUP_KEY.
func restoreKey(env map[string]string, keyFile string) ([]byte, error) {
	key, from := env["BOBRES_BACKUP_KEY"], "BOBRES_BACKUP_KEY in .env"
	if keyFile != "" {
		b, err := os.ReadFile(keyFile) //nolint:gosec // the operator's own file
		if err != nil {
			return nil, fmt.Errorf("read the key file: %w", err)
		}
		var lines []string
		for _, l := range strings.Split(string(b), "\n") {
			if l = strings.TrimSpace(l); l != "" {
				lines = append(lines, l)
			}
		}
		if len(lines) != 1 {
			return nil, fmt.Errorf("the key file %s must hold the backup key on one line", keyFile)
		}
		// A line copied from .env works too.
		key, from = strings.TrimPrefix(lines[0], "BOBRES_BACKUP_KEY="), keyFile
	}
	key = strings.TrimSpace(key)
	switch {
	case key == "":
		return nil, errors.New("there is no BOBRES_BACKUP_KEY in .env to open the backup with")
	case len(key) < backup.MinKeyLen:
		return nil, fmt.Errorf("the backup key from %s is too short (%d characters; backup keys have at least %d)", from, len(key), backup.MinKeyLen)
	}
	return []byte(key), nil
}

// verifyBackupFile reads the whole backup at path and checks every part of it.
func verifyBackupFile(path string, key []byte) (backup.Summary, error) {
	f, err := os.Open(path) //nolint:gosec // the operator's own file
	if err != nil {
		return backup.Summary{}, err
	}
	defer f.Close() //nolint:errcheck // read-only
	return verifyBackup(f, key)
}

// verifyBackup reads the whole backup from r and checks every part of it.
func verifyBackup(r io.Reader, key []byte) (backup.Summary, error) {
	s, err := backup.Verify(r, key)
	if err != nil {
		return s, errors.New(backupProblem(err))
	}
	return s, nil
}

// backupProblem says in plain words why a backup cannot be read.
func backupProblem(err error) string {
	switch {
	case errors.Is(err, backup.ErrFormat):
		return "this is not a BOBRES backup file (or it is cut short)"
	case errors.Is(err, backup.ErrVersion):
		return "the backup was made by a newer BOBRES: update bobres first (bobres update)"
	case errors.Is(err, backup.ErrKey):
		return "wrong backup key, or the file is damaged at its start (a backup from another server needs that server's key: --key-file)"
	case errors.Is(err, backup.ErrCorrupt):
		return "the file is damaged or incomplete (an interrupted copy or download?)"
	}
	return err.Error()
}

// describeBackup prints what a verified backup holds: never key values.
func describeBackup(w io.Writer, file string, s backup.Summary) {
	names := append([]string(nil), s.KeyNames...)
	sort.Strings(names)
	keys := strings.Join(names, ", ")
	if keys == "" {
		keys = "none"
	}
	fmt.Fprintf(w, "Backup %s (checked: key and contents are good)\n  made:        %s\n  database:    %s\n  master keys: %s\n",
		file, s.Created.Format("2006-01-02 15:04 MST"), humanSize(s.DumpSize), keys)
}

// humanSize prints a byte count the short way (1.5 MB).
func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

// masterKeys are the services' master keys set in env, which every backup
// carries (as the backup service's do).
func masterKeys(env map[string]string) map[string]string {
	keys := map[string]string{}
	for _, name := range config.BackupMasterKeys {
		if v := env[name]; v != "" {
			keys[name] = v
		}
	}
	return keys
}

// safetyBackup backs up the current database with this install's backup key
// and master keys into <dir>/backups as bobres-<stamp>-<tag>.bkp, with a
// sidecar as the backup service writes (why says there why it was not sent
// to Telegram), so the System page lists it. The name is outside the
// service's retention of 7: a restore tried again and again never pushes out
// the daily backups, and the file stays until the operator deletes it. It
// returns the file name.
func (o *ops) safetyBackup(ctx context.Context, dir string, env map[string]string, tag, why string) (string, error) {
	if len(env["BOBRES_BACKUP_KEY"]) < backup.MinKeyLen {
		return "", errors.New("this install has no backup key (BOBRES_BACKUP_KEY): run `bobres install` once to create it")
	}
	bdir := filepath.Join(dir, "backups")
	if err := os.MkdirAll(bdir, 0o700); err != nil {
		return "", err
	}
	created := o.now().UTC().Truncate(time.Second)
	name := safetyName(created, tag)
	for exists(filepath.Join(bdir, name)) { // an earlier one, that same second
		created = created.Add(time.Second)
		name = safetyName(created, tag)
	}
	// The backup service's user owns backups/ and reads the records: the
	// files are handed to it, by their open descriptors.
	uid := -1
	if os.Geteuid() == 0 {
		uid = backupUID
	}
	fmt.Fprintln(o.out, "Backing up the current database first...")
	size, sum, err := o.backupTo(ctx, dir, bdir, name, env, uid)
	if err != nil {
		return "", err
	}
	sc := backup.Sidecar{File: name, Size: size, SHA256: sum, CreatedAt: created, TelegramError: why + "; kept on this server only"}
	if err := writeSidecar(filepath.Join(bdir, backup.SidecarName(name)), sc, uid); err != nil {
		fmt.Fprintf(o.errOut, "warning: the backup's record (%s) was not written: %v\n", backup.SidecarName(name), err)
	}
	return name, nil
}

// safetyName is a CLI safety backup's file name, e.g.
// bobres-20261011T120000Z-before-restore.bkp.
func safetyName(t time.Time, tag string) string {
	return strings.TrimSuffix(backup.FileName(t), ".bkp") + "-" + tag + ".bkp"
}

// backupTo writes a backup of the current database (with env's backup key
// and master keys) to destDir/name: a temp file there, given to uid (-1:
// kept) by its descriptor, synced, renamed, then the directory synced, so
// the backup is on disk before anything destructive follows. It returns the
// size and the SHA-256.
func (o *ops) backupTo(ctx context.Context, dir, destDir, name string, env map[string]string, uid int) (int64, string, error) {
	// Not the backup service's temp pattern, which a run of it would delete.
	tmp, err := os.CreateTemp(destDir, ".cli-backup-*.tmp")
	if err != nil {
		return 0, "", err
	}
	defer os.Remove(tmp.Name()) //nolint:errcheck // gone after the rename
	defer tmp.Close()           //nolint:errcheck // closed (and checked) below on success
	h := sha256.New()
	n := &byteCount{}
	if err := o.dumpInto(ctx, dir, io.MultiWriter(tmp, h, n), []byte(env["BOBRES_BACKUP_KEY"]), masterKeys(env)); err != nil {
		return 0, "", err
	}
	if uid >= 0 {
		if err := tmp.Chown(uid, uid); err != nil {
			return 0, "", err
		}
	}
	if err := tmp.Sync(); err != nil {
		return 0, "", err
	}
	if err := tmp.Close(); err != nil {
		return 0, "", err
	}
	if err := os.Rename(tmp.Name(), filepath.Join(destDir, name)); err != nil {
		return 0, "", err
	}
	if err := syncDir(destDir); err != nil {
		return 0, "", err
	}
	return n.n, hex.EncodeToString(h.Sum(nil)), nil
}

// writeSidecar writes a backup's record as backup.WriteSidecar does, given
// to uid (-1: kept) by its descriptor.
func writeSidecar(path string, sc backup.Sidecar, uid int) error {
	b, err := json.MarshalIndent(sc, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomicAs(path, append(b, '\n'), 0o600, uid)
}

// dumpInto streams pg_dump from the postgres container into backup.Write, so
// the dump is never on disk in the clear. A dump that fails is reported as
// such, not as the write error it causes.
func (o *ops) dumpInto(ctx context.Context, dir string, w io.Writer, key []byte, keys map[string]string) error {
	dctx, cancel := context.WithCancel(ctx)
	defer cancel()
	pr, pw := io.Pipe()
	dumped := make(chan error, 1)
	go func() {
		err := o.pipe(dctx, dir, nil, pw, composeArgs("exec", "-T", "postgres", "sh", "-c", dumpScript)...)
		if err != nil {
			err = fmt.Errorf("pg_dump in the postgres container: %w", err)
		}
		_ = pw.CloseWithError(err) // nil: EOF, the end of the dump
		dumped <- err
	}()
	werr := backup.Write(w, key, keys, pr)
	if werr != nil {
		_ = pr.CloseWithError(werr)
		cancel()
	}
	derr := <-dumped
	switch {
	case derr != nil && (werr == nil || errors.Is(werr, derr)):
		return derr
	case werr != nil:
		return werr
	}
	return nil
}

// stopApp stops the services that use the database; postgres, redis, caddy
// and the tls-agent stay up.
func (o *ops) stopApp(ctx context.Context, dir string, env map[string]string) error {
	fmt.Fprintln(o.out, "Stopping the BOBRES services (the database stays up)...")
	if out, err := o.run(ctx, dir, composeArgs(append([]string{"stop"}, dataServices(dir, env)...)...)...); err != nil {
		return fmt.Errorf("docker compose stop failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// loadDump streams the dump of the backup at file, decrypted as it is read,
// into restoreScript in the postgres container. It returns the backup's
// master keys.
func (o *ops) loadDump(ctx context.Context, dir, file string, key []byte) (map[string]string, error) {
	f, err := os.Open(file) //nolint:gosec // the operator's own file
	if err != nil {
		return nil, err
	}
	defer f.Close() //nolint:errcheck // read-only
	return o.loadDumpFrom(ctx, dir, f, key)
}

// loadDumpFrom is loadDump for the backup read from src.
func (o *ops) loadDumpFrom(ctx context.Context, dir string, src io.Reader, key []byte) (map[string]string, error) {
	r, err := backup.Open(src, key)
	if err != nil {
		return nil, errors.New(backupProblem(err))
	}
	fmt.Fprintln(o.out, "Restoring the database (this can take a few minutes)...")
	if err := o.pipe(ctx, dir, r.Dump(), nil, composeArgs("exec", "-T", "postgres", "sh", "-c", restoreScript)...); err != nil {
		return nil, err
	}
	return r.Keys(), nil
}

// applyMasterKeys puts the backup's master keys in .env where they differ
// (unless keep): without them the restored 3x-ui tokens, staff 2FA and
// gateway settings cannot be read. It returns the names that differ, sorted.
// A key the backup does not carry stays as it is.
func applyMasterKeys(dir string, archive map[string]string, keep bool) ([]string, error) {
	envPath := filepath.Join(dir, ".env")
	env, _, err := readEnv(envPath)
	if err != nil {
		return nil, err
	}
	var changed []string
	for _, name := range config.BackupMasterKeys {
		v := archive[name]
		if v == "" || v == env[name] {
			continue
		}
		if strings.ContainsAny(v, "'\n\r") {
			return nil, fmt.Errorf("the backup's %s holds a quote or a line break", name)
		}
		changed = append(changed, name)
		env[name] = v
	}
	sort.Strings(changed)
	if len(changed) == 0 || keep {
		return changed, nil
	}
	return changed, writeEnv(envPath, env)
}

// reportKeys says which master keys differed (names only).
func (o *ops) reportKeys(changed []string, keep bool) {
	switch {
	case len(changed) == 0:
		fmt.Fprintln(o.out, "Master keys: the same as this install's.")
	case keep:
		fmt.Fprintf(o.out, "Master keys: the backup's differ (%s) but stay as they are (--keep-keys).\n"+
			"Data those keys encrypted (3x-ui tokens, staff 2FA, gateway settings) cannot be read until .env has the backup's keys.\n",
			strings.Join(changed, ", "))
	default:
		fmt.Fprintf(o.out, "Master keys: replaced in .env with the backup's: %s\n", strings.Join(changed, ", "))
	}
}

// startAndWait starts the stack as .env now says (extra goes to compose up)
// and waits until it is healthy.
func (o *ops) startAndWait(ctx context.Context, dir string, extra ...string) int {
	env, _, err := readEnv(filepath.Join(dir, ".env"))
	if err != nil {
		fmt.Fprintf(o.errOut, "error: read %s: %v\n", filepath.Join(dir, ".env"), err)
		return 1
	}
	fmt.Fprintln(o.out, "Starting BOBRES...")
	if out, err := o.run(ctx, dir, composeArgs(append([]string{"up", "-d"}, extra...)...)...); err != nil {
		fmt.Fprintf(o.errOut, "error: docker compose up failed: %v\n%s\n", err, strings.TrimSpace(string(out)))
		return 1
	}
	return waitHealthy(ctx, o.run, o.sleep, dir, appServices(dir, env), o.out, o.errOut)
}

// healthHints prints what to look at next, in the spirit of bobres doctor.
func (o *ops) healthHints(dir string, healthy, keysKept bool) {
	if gb := o.freeGB(dir); gb >= 0 && gb < minDiskGB {
		fmt.Fprintf(o.out, "warning: %.1f GB free in %s: backups and updates need room (at least %d GB).\n", gb, dir, minDiskGB)
	}
	if healthy {
		return
	}
	fmt.Fprintf(o.errOut, "Check: bobres status --dir %s   and   bobres logs --dir %s core\n", dir, dir)
	if keysKept {
		fmt.Fprintln(o.errOut, "The master keys were kept (--keep-keys): a service that cannot read its data with them does not start.")
	}
}

// exists reports whether path exists.
func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

type byteCount struct{ n int64 }

func (c *byteCount) Write(p []byte) (int, error) {
	c.n += int64(len(p))
	return len(p), nil
}
