package snapshot

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Decode parses and validates a snapshot (Normalize): the bytes are
// untrusted. now is the reader's clock.
func Decode(data []byte, now time.Time) (*Snapshot, error) {
	if len(data) > MaxFileBytes {
		return nil, ErrTooLarge
	}
	var s Snapshot
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if err := s.Normalize(now); err != nil {
		return nil, err
	}
	return &s, nil
}

// Read reads the snapshot in dir (FileName). A missing file is an error
// wrapping fs.ErrNotExist. The file is opened without following a link and
// must be a regular file of at most MaxFileBytes.
func Read(dir string, now time.Time) (*Snapshot, error) {
	f, err := os.OpenFile(filepath.Join(dir, FileName), os.O_RDONLY|syscall.O_NOFOLLOW, 0) //nolint:gosec // the monitor directory, read-only in core
	if err != nil {
		return nil, err
	}
	defer f.Close() //nolint:errcheck // read-only
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %s is not a regular file", ErrInvalid, FileName)
	}
	if fi.Size() > MaxFileBytes {
		return nil, ErrTooLarge
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxFileBytes+1))
	if err != nil {
		return nil, err
	}
	return Decode(data, now)
}

// Exists reports whether dir holds a snapshot file at all (of any content).
func Exists(dir string) bool {
	_, err := os.Lstat(filepath.Join(dir, FileName))
	return err == nil
}

// Write replaces dir's snapshot with s, atomically and readable by everyone
// (mode 0644: core, which runs as another user, reads it): the data goes to a
// temporary file in the same directory (made exclusively and without following
// a link), is synced, and renamed over the old one, so a reader sees the old
// snapshot or the new one, never half of one. Leftovers of runs that were
// killed are removed. The directory must exist.
func Write(dir string, s *Snapshot) error {
	data, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("snapshot: %w", err)
	}
	if len(data) > MaxFileBytes {
		return fmt.Errorf("snapshot: %w (%d bytes)", ErrTooLarge, len(data))
	}
	removeLeftovers(dir, 10*time.Minute)
	var rnd [6]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return err
	}
	tmp := filepath.Join(dir, tmpPrefix+hex.EncodeToString(rnd[:]))
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o644) //nolint:gosec // our own temp name in the monitor directory
	if err != nil {
		return fmt.Errorf("snapshot: %w", err)
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Chmod(0o644) // whatever the umask took away
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, filepath.Join(dir, FileName))
	}
	if err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("snapshot: %w", err)
	}
	if d, err := os.Open(dir); err == nil { //nolint:gosec // the directory just written
		_ = d.Sync() // the rename survives a power cut
		_ = d.Close()
	}
	return nil
}

// tmpPrefix starts every temporary file Write makes.
const tmpPrefix = ".snapshot.json.tmp-"

// removeLeftovers deletes temporary files older than age (a killed run).
func removeLeftovers(dir string, age time.Duration) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), tmpPrefix) {
			continue
		}
		if fi, err := e.Info(); err == nil && fi.Mode().IsRegular() && time.Since(fi.ModTime()) > age {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// Remove deletes the snapshot (the collector was turned off). A missing file
// is not an error.
func Remove(dir string) error {
	if err := os.Remove(filepath.Join(dir, FileName)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
