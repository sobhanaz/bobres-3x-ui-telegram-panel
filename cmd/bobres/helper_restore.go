package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/backup"
)

// The dashboard's restore (System > Backups, docs/06-installer.md "Restoring from the dashboard").
// It is the update helper's second action: core drops request.json with "action": "restore" into the
// same folder, and the same root service reads it with the same care, then runs the same code as
// `sudo bobres restore` (restore.go: restoreRun) under the install lock, with one addition the command
// line does not make: when the restored data does not come up healthy, it goes back to the backup of
// the data from before on its own. Protocol version 1, request.json for a restore:
//
//	{"v":1, "id":"<uuid>", "action":"restore", "source":"backup|upload", "file":"<name>", "upload":"<uuid>|",
//	 "sha256":"<hex>", "size":<bytes>, "by":"<name>", "at":"<RFC 3339>"}
//
//   - source "backup": file is a backup core's backup service lists (bobres-<time>[-tag].bkp), which the
//     helper reads from <install dir>/backups; upload is "".
//   - source "upload": the owner uploaded a backup; core wrote it as <install dir>/run/update/upload/<upload>.bkp
//     (a folder only core can write) and file is the name it had when it was uploaded, for the messages.
//
// Core's sha256 and size are what it saw; the helper copies the file into a file of its own (root's,
// mode 0600, in a folder of its own) while it hashes it, and compares: whatever core does to the
// upload afterwards is not what is restored. The format and the key are checked on that copy before
// anything is stopped.
const (
	actionUpdate  = "update"
	actionRestore = "restore"

	sourceBackup = "backup"
	sourceUpload = "upload"

	// uploadDirName is the folder in run/update that core writes uploads to.
	uploadDirName = "upload"
	// maxRestoreBytes bounds a backup the dashboard restores (core refuses a larger upload).
	maxRestoreBytes = 2 << 30
	// maxRestoreFile bounds the name an upload had, in characters.
	maxRestoreFile = 120
	// restoreSettle is how long the helper waits, once the backup is checked, before it stops
	// anything: the notices core queued about the request reach the owners' Telegram first (the bot
	// fetches them every second), as the restore replaces the database they are queued in.
	restoreSettle = 6 * time.Second
	// restoreRoom is the free space, besides twice the backup's size (the copy and the safety
	// backup of the current data), a restore needs.
	restoreRoom = 512 << 20
)

var sha256Re = regexp.MustCompile(`^[0-9a-f]{64}$`)

// restoreRequest is a validated request to restore a backup.
type restoreRequest struct {
	ID     string
	Source string // backup or upload
	File   string
	Upload string // the id of the upload; "" for a backup
	SHA256 string
	Size   int64
	By     string
	At     time.Time
}

// rawRestoreRequest is the JSON of a restore request; every field is a pointer so that one that is
// missing is not taken for an empty one.
type rawRestoreRequest struct {
	V      *int    `json:"v"`
	ID     *string `json:"id"`
	Action *string `json:"action"`
	Source *string `json:"source"`
	File   *string `json:"file"`
	Upload *string `json:"upload"`
	SHA256 *string `json:"sha256"`
	Size   *int64  `json:"size"`
	By     *string `json:"by"`
	At     *string `json:"at"`
}

// requestAction is the action a request file names ("update" when it does not say, or is not
// readable: the update's own parser then says what is wrong).
func requestAction(b []byte) string {
	var v struct {
		Action string `json:"action"`
	}
	if json.Unmarshal(b, &v) == nil && v.Action == actionRestore {
		return actionRestore
	}
	return actionUpdate
}

// uploadHint is the upload a request names when the file shows one that looks like an id ("" otherwise),
// whether or not the request is valid: the file of an upload is removed with its request. Nothing but
// a UUID comes back, so it is never a path.
func uploadHint(b []byte) string {
	var v struct {
		Upload string `json:"upload"`
	}
	if json.Unmarshal(b, &v) != nil || !requestIDRe.MatchString(v.Upload) {
		return ""
	}
	return v.Upload
}

// parseRestoreRequest checks a restore request strictly, as parseUpdateRequest does an update's:
// one JSON object with exactly these fields, every value of the form the protocol says, dated
// within a quarter of an hour before now and a minute after it.
func parseRestoreRequest(b []byte, now time.Time) (*restoreRequest, error) {
	if len(b) > maxRequestBytes {
		return nil, fmt.Errorf("it is larger than %d bytes", maxRequestBytes)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var raw rawRestoreRequest
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("it is not the JSON object of the protocol (%s)", firstLine(err.Error()))
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("there is more in the file than the one JSON object")
	}
	for _, f := range []struct {
		name    string
		missing bool
	}{
		{"v", raw.V == nil}, {"id", raw.ID == nil}, {"action", raw.Action == nil}, {"source", raw.Source == nil},
		{"file", raw.File == nil}, {"upload", raw.Upload == nil}, {"sha256", raw.SHA256 == nil}, {"size", raw.Size == nil},
		{"by", raw.By == nil}, {"at", raw.At == nil},
	} {
		if f.missing {
			return nil, fmt.Errorf("the field %q is missing", f.name)
		}
	}
	if *raw.V != requestProtocol {
		return nil, fmt.Errorf("protocol version %d is not %d", *raw.V, requestProtocol)
	}
	if *raw.Action != actionRestore {
		return nil, fmt.Errorf("the action %s is not restore", shown(*raw.Action))
	}
	if !requestIDRe.MatchString(strings.ToLower(*raw.ID)) {
		return nil, fmt.Errorf("the id %s is not a UUID", shown(*raw.ID))
	}
	switch *raw.Source {
	case sourceBackup:
		if !backup.ValidFileName(*raw.File) {
			return nil, fmt.Errorf("the backup %s is not a backup file name like bobres-20261011T033000Z.bkp", shown(*raw.File))
		}
		if *raw.Upload != "" {
			return nil, errors.New("a backup from the server's list has no upload")
		}
	case sourceUpload:
		if err := checkRestoreName(*raw.File); err != nil {
			return nil, err
		}
		if !requestIDRe.MatchString(*raw.Upload) { // lower case, as core makes it: it is a file name
			return nil, fmt.Errorf("the upload %s is not a UUID", shown(*raw.Upload))
		}
	default:
		return nil, fmt.Errorf("the source %s is neither backup nor upload", shown(*raw.Source))
	}
	if !sha256Re.MatchString(*raw.SHA256) {
		return nil, errors.New("the sha256 is not 64 lower case hexadecimal digits")
	}
	if *raw.Size < 1 || *raw.Size > maxRestoreBytes {
		return nil, fmt.Errorf("the size %d is not between 1 and %d bytes", *raw.Size, int64(maxRestoreBytes))
	}
	if err := checkRequestBy(*raw.By); err != nil {
		return nil, err
	}
	at, err := time.Parse(time.RFC3339, *raw.At)
	if err != nil {
		return nil, fmt.Errorf("the time %s is not RFC 3339", shown(*raw.At))
	}
	switch age := now.Sub(at); {
	case age > requestMaxAge:
		return nil, fmt.Errorf("it is older than %d minutes", int(requestMaxAge/time.Minute))
	case -age > requestMaxFuture:
		return nil, errors.New("it is dated in the future")
	}
	return &restoreRequest{ID: strings.ToLower(*raw.ID), Source: *raw.Source, File: *raw.File, Upload: *raw.Upload, SHA256: *raw.SHA256,
		Size: *raw.Size, By: *raw.By, At: at}, nil
}

// checkRestoreName holds the name an uploaded backup had to one plain line: it goes into logs and
// notices (it is never a path).
func checkRestoreName(name string) error {
	if name == "" || strings.TrimSpace(name) != name {
		return errors.New("the file name is empty or has spaces around it")
	}
	if len([]rune(name)) > maxRestoreFile {
		return fmt.Errorf("the file name is longer than %d characters", maxRestoreFile)
	}
	if !utf8.ValidString(name) {
		return errors.New("the file name is not text")
	}
	for _, r := range name { // the same plain-line rule as a user name
		if unicode.IsControl(r) || r == 0x2028 || r == 0x2029 || (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069) {
			return errors.New("the file name has a control character in it")
		}
	}
	return nil
}

// uploadDirPath is the folder core writes the uploads to.
func uploadDirPath(dir string) string { return filepath.Join(updateDirPath(dir), uploadDirName) }

// uploadPath is the file an upload is written as.
func uploadPath(dir, id string) string { return filepath.Join(uploadDirPath(dir), id+".bkp") }

// checkUploadDir looks at the upload folder before anything is read from it: a real folder (not a
// link) that root or core owns and no one else can write to. A folder that is missing is no upload.
func checkUploadDir(dir string, uid int) error {
	path := uploadDirPath(dir)
	fi, err := os.Lstat(path)
	switch {
	case err != nil:
		return err
	case fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir():
		return fmt.Errorf("%s is not a directory (a link or a file in its place?)", path)
	case fi.Mode().Perm()&0o022 != 0:
		return fmt.Errorf("%s can be written by others (mode %04o)", path, fi.Mode().Perm())
	}
	if owner, ok := asRoot.owner(fi); ok && owner != uid && owner != os.Geteuid() {
		return fmt.Errorf("%s belongs to uid %d", path, owner)
	}
	return nil
}

// openBackupFile opens path for reading without following a link and without waiting for a pipe,
// and checks it is a regular file of exactly size bytes that one of owners owns. The file is looked
// at through the descriptor, so whatever is put at its path afterwards is not what is read.
func openBackupFile(path string, size int64, owners ...int) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_NOCTTY, 0) //nolint:gosec // opened without following links, then looked at through the descriptor
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil, errors.New("there is no such file")
	case errors.Is(err, syscall.ELOOP):
		return nil, errors.New("it is a symbolic link")
	case err != nil:
		return nil, fmt.Errorf("it cannot be opened (%w)", err)
	}
	fi, err := f.Stat()
	switch {
	case err != nil:
		_ = f.Close()
		return nil, err
	case !fi.Mode().IsRegular():
		_ = f.Close()
		return nil, errors.New("it is not a regular file")
	case fi.Size() != size:
		_ = f.Close()
		return nil, fmt.Errorf("it is %d bytes, not the %d the request names", fi.Size(), size)
	}
	owner, ok := asRoot.owner(fi)
	for _, o := range owners {
		if ok && owner == o {
			return f, nil
		}
	}
	_ = f.Close()
	return nil, errors.New("it does not belong to the user it should (not written by core or the backup service)")
}

// stageBackup copies the backup the request names into a file of the helper's own, hashing it on
// the way, and returns that copy open at its start. done closes and removes it. The copy is what
// is checked and restored: the original is in a place somebody else can write to.
func (h *helperRun) stageBackup(req *restoreRequest) (copyFile *os.File, done func(), err error) {
	var src *os.File
	switch req.Source {
	case sourceUpload:
		if err := checkUploadDir(h.dir, h.coreUID); err != nil {
			return nil, nil, fmt.Errorf("the upload folder is not trusted: %w", err)
		}
		src, err = openBackupFile(uploadPath(h.dir, req.Upload), req.Size, h.coreUID)
	default:
		bdir := filepath.Join(h.dir, "backups")
		src, err = openBackupFile(filepath.Join(bdir, req.File), req.Size, h.backupOwner, 0)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("the backup %s cannot be used: %w", shown(req.File), err)
	}
	defer src.Close() //nolint:errcheck // read-only
	if gb := h.u.freeGB(h.dir); gb >= 0 && gb*1e9 < float64(2*req.Size+restoreRoom) {
		return nil, nil, fmt.Errorf("there is not enough free disk space (%.1f GB) to restore a backup of %s: it needs room for a copy of it and for the backup of the current data",
			gb, humanSize(req.Size))
	}
	bdir := filepath.Join(h.dir, "backups")
	if err := os.MkdirAll(bdir, 0o700); err != nil {
		return nil, nil, err
	}
	tmp, err := os.MkdirTemp(bdir, ".dashboard-restore-*")
	if err != nil {
		return nil, nil, err
	}
	dst, err := os.OpenFile(filepath.Join(tmp, "backup.bkp"), os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // in our own new folder
	if err != nil {
		_ = os.RemoveAll(tmp)
		return nil, nil, err
	}
	done = func() {
		_ = dst.Close()
		_ = os.RemoveAll(tmp)
	}
	sum := sha256.New()
	n, err := io.Copy(io.MultiWriter(dst, sum), io.LimitReader(src, req.Size+1))
	switch {
	case err != nil:
		done()
		return nil, nil, fmt.Errorf("the backup %s could not be read: %w", shown(req.File), err)
	case n != req.Size:
		done()
		return nil, nil, fmt.Errorf("the backup %s changed while it was read (%d bytes, not %d)", shown(req.File), n, req.Size)
	case hex.EncodeToString(sum.Sum(nil)) != req.SHA256:
		done()
		return nil, nil, fmt.Errorf("the backup %s is not the file core saw: its SHA-256 differs", shown(req.File))
	}
	if err := dst.Sync(); err != nil {
		done()
		return nil, nil, err
	}
	if _, err := dst.Seek(0, io.SeekStart); err != nil {
		done()
		return nil, nil, err
	}
	return dst, done, nil
}

// runRestore is the restore itself, as `bobres restore` makes it, with the backup checked first and
// the data from before put back if the restored data does not come up healthy.
func (h *helperRun) runRestore(req *restoreRequest, env map[string]string) int {
	u := h.u
	from := u.installed(env)
	started := h.now()
	fmt.Fprintf(u.out, "Restore requested by %s from the dashboard (request %s): the backup %s (%s).\n", req.By, req.ID, req.File, req.Source)
	h.st.update(func(s *helperStatus) {
		*s = helperStatus{V: requestProtocol, Installed: true, Helper: h.helper, State: helperRunning, ID: req.ID, From: from, To: from,
			Action: actionRestore, Subject: req.File, Source: req.Source, By: req.By, RequestedAt: stamp(req.At),
			Step: stepVerifying, Message: "Checking the backup.", StartedAt: stamp(started)}
	})
	fail := func(format string, args ...any) int {
		fmt.Fprintf(u.errOut, "error: "+format+". Nothing was changed.\n", args...)
		return h.concludeRestore(1, restoreResult{}, req, nil)
	}

	key, err := restoreKey(env, "")
	if err != nil {
		return fail("%v", err)
	}
	if len(strings.TrimSpace(env["BOBRES_BACKUP_KEY"])) < backup.MinKeyLen {
		return fail("this install has no backup key (BOBRES_BACKUP_KEY in .env), and the restore backs up the current data with it first")
	}
	file, done, err := h.stageBackup(req)
	if err != nil {
		return fail("%v", err)
	}
	defer done()
	// The whole backup is read before anything is touched: the format, the key and every chunk.
	sum, err := verifyBackup(file, key)
	if err == nil {
		_, err = file.Seek(0, io.SeekStart)
	}
	if err != nil {
		return fail("%s: %s", shown(req.File), restoreAdvice(err))
	}
	describeBackup(u.out, req.File, sum)
	h.event(updateEvent{Step: stepVerifying, Message: "The backup is good. Starting in a few seconds."})
	if h.settle > 0 {
		u.sleep(h.settle)
	}

	plan := &restorePlan{dir: h.dir, env: env, key: key, file: file, source: req.File, sum: sum, rollback: true,
		event: func(ev restoreEvent) { h.event(updateEvent{Step: ev.Step, Message: ev.Message}) }}
	code, res := u.restoreRun(h.context(), plan)
	return h.concludeRestore(code, res, req, &sum)
}

// restoreAdvice is why the backup cannot be restored from the dashboard, with what to do.
func restoreAdvice(err error) string {
	msg := err.Error()
	if strings.Contains(msg, "wrong backup key") {
		return msg + ". The dashboard restores backups made by this server; one from another server needs its key: sudo bobres restore FILE --key-file KEYFILE"
	}
	return msg
}

// context is the helper's: cancelled by SIGTERM, so that a stop of the service during the restore
// ends it through its own failure path (the services are started again).
func (h *helperRun) context() context.Context {
	if h.ctx != nil {
		return h.ctx
	}
	return context.Background()
}

// concludeRestore writes the final status of a restore that ended with code and returns the exit
// code. sum is the checked backup (nil when it never was).
func (h *helperRun) concludeRestore(code int, res restoreResult, req *restoreRequest, sum *backup.Summary) int {
	state, step, message := helperFailed, "", ""
	safety := ""
	if res.safety != "" {
		safety = filepath.Base(res.safety)
	}
	switch {
	case code == 0:
		state, step = helperDone, stepFinished
		message = fmt.Sprintf("BOBRES now runs the data of %s.", shownName(req.File))
		if sum != nil {
			message = fmt.Sprintf("BOBRES now runs the data of %s (made %s).", shownName(req.File), sum.Created.UTC().Format("2006-01-02 15:04 MST"))
		}
		if safety != "" {
			message += " The data from before is kept in backups/" + safety + "."
		}
	case res.rolledBack:
		state, step = helperRolledBack, stepRollingBk
		message = "The restore did not go through: BOBRES runs the data from before it again."
	case res.rollbackIncomplete:
		step = stepRollingBk
		message = "The restore failed and going back did not finish (sudo bobres status)."
		if safety != "" {
			message += " To go back by hand: sudo bobres restore backups/" + safety + " --yes"
		}
	default:
		step = h.st.current().Step
		message = h.st.lastError()
		if res.replaced {
			message = "The restore stopped after the data was replaced: " + message
		}
	}
	if h.flush != nil {
		h.flush()
	}
	finished := h.now()
	h.st.update(func(s *helperStatus) {
		s.State, s.Step, s.Message, s.FinishedAt = state, step, clipSentence(message), stamp(finished)
	})
	return code
}

// shownName cuts a file name for a status line.
func shownName(s string) string {
	if r := []rune(s); len(r) > 60 {
		return string(r[:57]) + "..."
	}
	return s
}
