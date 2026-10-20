package sysupdate

import (
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
	"unicode/utf8"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/backup"
)

// The dashboard's restore (System > Backups) is the updater's second action. Core asks for it the
// same way it asks for an update: one small request.json, dropped into the folder the updater
// watches. A restore request names the backup to restore and nothing else:
//
//	{"v":1, "id":"<uuid>", "action":"restore", "source":"backup|upload", "file":"<name>", "upload":"<uuid>|",
//	 "sha256":"<hex>", "size":<bytes>, "by":"<name>", "at":"<RFC 3339>"}
//
//   - source "backup": file is a backup the backup service lists (bobres-<time>[-tag].bkp); the updater
//     reads it from the install's backups folder, which core cannot reach. upload is "".
//   - source "upload": the owner uploaded a backup. Core streams it into upload/<upload>.bkp in this folder
//     (StageUpload: a file of its own, mode 0600, never through a link, never more than MaxUpload
//     bytes), and file is the name it had, for the messages (it is never used as a path).
//
// The sha256 and the size are what core measured. The updater copies the file into one of its own
// while it hashes it, and refuses a file that differs: whatever core does to the upload afterwards
// is not what is restored. The key that opens a backup is the install's, in .env, which core does not
// hold, so the format and the key are checked by the updater, before anything is stopped.
const (
	// ActionRestore asks the updater to restore a backup.
	ActionRestore = "restore"
	// SourceBackup and SourceUpload say where the backup of a restore comes from.
	SourceBackup = "backup"
	SourceUpload = "upload"
	// UploadDirName is the folder in the updater's folder that core writes uploads to.
	UploadDirName = "upload"
	// MaxUpload is the largest backup the dashboard restores (the updater refuses more as well).
	MaxUpload = 2 << 30
	// MaxFileName bounds the name an upload had, MaxSubject what the status says of a backup.
	MaxFileName = 120
	MaxSubject  = 120
	// UploadMode: the owner (core) reads and writes it; the updater, root, reads it.
	UploadMode os.FileMode = 0o600
)

// ErrTooLarge: an upload is larger than the limit. ErrEmptyUpload: nothing was uploaded.
var (
	ErrTooLarge    = errors.New("sysupdate: the upload is larger than the limit")
	ErrEmptyUpload = errors.New("sysupdate: the upload is empty")
)

// RestoreRequest is the request to restore a backup. The field order is the protocol's.
type RestoreRequest struct {
	V      int    `json:"v"`
	ID     string `json:"id"`
	Action string `json:"action"`
	Source string `json:"source"`
	// File is the backup's file name (SourceBackup), or the name an upload had (SourceUpload).
	File string `json:"file"`
	// Upload is the id of the upload, "" for SourceBackup.
	Upload string `json:"upload"`
	// SHA256 (lower case hexadecimal) and Size are the file's, as core measured them.
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	// By is the dashboard user who asked; At when (RFC 3339, UTC).
	By string `json:"by"`
	At string `json:"at"`
}

var sha256Re = regexp.MustCompile(`^[0-9a-f]{64}$`)

// NewRestoreRequest makes a request to restore the file (a backup of the server, or an upload with
// its id) whose SHA-256 and size are given, by a person at at. by is cut to MaxBy characters and made
// one line, and so is the name of an upload; anything that is not valid is an error.
func NewRestoreRequest(id, source, file, upload, sum string, size int64, by string, at time.Time) (RestoreRequest, error) {
	if source == SourceUpload {
		file = Clean(file, MaxFileName)
	}
	r := RestoreRequest{V: ProtocolVersion, ID: id, Action: ActionRestore, Source: source, File: file, Upload: upload, SHA256: sum, Size: size,
		By: Clean(by, MaxBy), At: at.UTC().Format(time.RFC3339)}
	if r.By == "" {
		r.By = "dashboard"
	}
	if err := r.Validate(); err != nil {
		return RestoreRequest{}, err
	}
	return r, nil
}

// Validate checks every field as the updater will.
func (r RestoreRequest) Validate() error {
	switch {
	case r.V != ProtocolVersion:
		return fmt.Errorf("sysupdate: request version %d is not %d", r.V, ProtocolVersion)
	case !idRe.MatchString(r.ID):
		return errors.New("sysupdate: the request id is not a lower-case UUID")
	case r.Action != ActionRestore:
		return fmt.Errorf("sysupdate: action %q is not %q", r.Action, ActionRestore)
	case !sha256Re.MatchString(r.SHA256):
		return errors.New("sysupdate: the sha256 is not 64 lower-case hexadecimal digits")
	case r.Size < 1 || r.Size > MaxUpload:
		return fmt.Errorf("sysupdate: the size %d is not between 1 and %d bytes", r.Size, int64(MaxUpload))
	case r.By == "" || utf8.RuneCountInString(r.By) > MaxBy || r.By != Clean(r.By, MaxBy):
		return errors.New("sysupdate: by is not one line of at most 64 characters")
	}
	switch r.Source {
	case SourceBackup:
		if !backup.ValidFileName(r.File) || r.Upload != "" {
			return errors.New("sysupdate: a backup of the server is a backup file name and has no upload")
		}
	case SourceUpload:
		if r.File == "" || utf8.RuneCountInString(r.File) > MaxFileName || r.File != Clean(r.File, MaxFileName) || !idRe.MatchString(r.Upload) {
			return errors.New("sysupdate: an upload has a one-line name of at most 120 characters and an id")
		}
	default:
		return fmt.Errorf("sysupdate: the source %q is neither backup nor upload", r.Source)
	}
	if at, err := time.Parse(time.RFC3339, r.At); err != nil || !strings.HasSuffix(r.At, "Z") || at.Location() != time.UTC {
		return errors.New("sysupdate: at is not an RFC 3339 time in UTC")
	}
	return nil
}

// Marshal is the request as written: one JSON line, at most MaxRequest bytes.
func (r RestoreRequest) Marshal() ([]byte, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	b, err := marshalLine(r)
	if err != nil {
		return nil, err
	}
	if len(b) > MaxRequest {
		return nil, fmt.Errorf("sysupdate: the request is %d bytes, at most %d", len(b), MaxRequest)
	}
	return b, nil
}

// marshalLine is v as one JSON line, without escaping < > &.
func marshalLine(v any) ([]byte, error) {
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return []byte(buf.String()), nil
}

// WriteRestoreRequest hands the updater a request to restore a backup, as WriteRequest does an update's.
func (d *Dir) WriteRestoreRequest(r RestoreRequest) error {
	data, err := r.Marshal()
	if err != nil {
		return err
	}
	return d.writeRequestBytes(data)
}

// Upload is a backup staged for a restore.
type Upload struct {
	ID     string
	Size   int64
	SHA256 string
}

// uploadFolder is the upload folder, made when it is missing (an install from before the restore
// has none until `bobres install` puts it right): a real folder, not a link to one.
func (d *Dir) uploadFolder() (string, error) {
	if err := d.checkDir(); err != nil {
		return "", err
	}
	path := filepath.Join(d.path, UploadDirName)
	fi, err := os.Lstat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return "", fmt.Errorf("sysupdate: make the upload folder: %w", err)
		}
		return path, nil
	case err != nil:
		return "", fmt.Errorf("sysupdate: the upload folder: %w", err)
	case !fi.IsDir():
		return "", errors.New("sysupdate: the upload folder is not a folder (a link or a file in its place?)")
	}
	return path, nil
}

// StageUpload writes the upload to upload/<id>.bkp: a file made for it alone (O_EXCL, never through a
// link, mode UploadMode), at most max bytes, hashed on the way, synced, and only then renamed to its
// name, so the updater never sees half of it. The whole of r is read: ErrTooLarge when it has more than
// max bytes, ErrEmptyUpload when it has none; a read that fails leaves nothing behind. Two uploads
// must not run at once (core allows one).
func (d *Dir) StageUpload(id string, r io.Reader, max int64) (Upload, error) {
	if !idRe.MatchString(id) {
		return Upload{}, errors.New("sysupdate: the upload id is not a lower-case UUID")
	}
	if max < 1 || max > MaxUpload {
		max = MaxUpload
	}
	folder, err := d.uploadFolder()
	if err != nil {
		return Upload{}, err
	}
	tmp := filepath.Join(folder, "."+id+".part")
	if err := os.Remove(tmp); err != nil && !errors.Is(err, os.ErrNotExist) {
		return Upload{}, fmt.Errorf("sysupdate: clear the temporary file: %w", err)
	}
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, UploadMode) //nolint:gosec // a name made from a UUID in the upload folder
	if err != nil {
		return Upload{}, fmt.Errorf("sysupdate: create the upload: %w", err)
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(r, max+1))
	switch {
	case err != nil:
		err = fmt.Errorf("sysupdate: read the upload: %w", err)
	case n > max:
		err = ErrTooLarge
	case n == 0:
		err = ErrEmptyUpload
	default:
		err = f.Chmod(UploadMode) // whatever the umask took away
		if err == nil {
			err = f.Sync()
		}
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, filepath.Join(folder, id+".bkp"))
	}
	if err != nil {
		_ = os.Remove(tmp)
		return Upload{}, err
	}
	return Upload{ID: id, Size: n, SHA256: hex.EncodeToString(h.Sum(nil))}, nil
}

// RemoveUpload removes a staged upload (the updater removes the file it used; this takes away one
// that was never used). A file already gone is not an error.
func (d *Dir) RemoveUpload(id string) error {
	if !idRe.MatchString(id) {
		return errors.New("sysupdate: the upload id is not a lower-case UUID")
	}
	for _, name := range []string{id + ".bkp", "." + id + ".part"} {
		if err := os.Remove(filepath.Join(d.path, UploadDirName, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("sysupdate: remove the upload: %w", err)
		}
	}
	return nil
}

// PurgeUploads removes what is left in the upload folder: every entry last written more than
// olderThan before now, except the uploads in keep (by id). It returns how many it removed. A folder
// that is not there holds nothing.
func (d *Dir) PurgeUploads(keep map[string]bool, olderThan time.Duration, now time.Time) (int, error) {
	if err := d.checkDir(); err != nil {
		return 0, err
	}
	folder := filepath.Join(d.path, UploadDirName)
	if fi, err := os.Lstat(folder); err != nil || fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
		return 0, nil
	}
	entries, err := os.ReadDir(folder)
	if err != nil {
		return 0, fmt.Errorf("sysupdate: read the upload folder: %w", err)
	}
	removed := 0
	for _, e := range entries {
		name := strings.TrimSuffix(strings.TrimPrefix(e.Name(), "."), ".part")
		name = strings.TrimSuffix(name, ".bkp")
		if keep[name] {
			continue
		}
		fi, err := e.Info()
		if err != nil || now.Sub(fi.ModTime()) <= olderThan {
			continue
		}
		if err := os.RemoveAll(filepath.Join(folder, e.Name())); err == nil {
			removed++
		}
	}
	return removed, nil
}
