// Package backupsvc is the backup service: it dumps the database with
// pg_dump, streams the dump through internal/backup's encrypted file format
// into the backup directory, hands each file to the bot for the owner's
// Telegram, and keeps the newest backup.Keep. One backup runs at a time, daily
// at a set time and on demand (core's System page, `bobres backup`).
package backupsvc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/backup"
)

const (
	// DeliverTimeout bounds handing one backup to the bot (it uploads to
	// Telegram before answering).
	DeliverTimeout = 20 * time.Minute
	// MinFree is the free space a first backup needs; later ones need twice
	// the size of the last.
	MinFree = 100 << 20
	// tempPattern names the file a backup is written to before its rename.
	tempPattern = ".bobres-*.tmp"
)

// ErrRunning is returned when a backup is asked for while one runs.
var ErrRunning = errors.New("backupsvc: a backup is already running")

// Dumper writes a database dump to w.
type Dumper interface {
	Dump(ctx context.Context, w io.Writer) error
}

// DumpFunc is a Dumper made of a function.
type DumpFunc func(ctx context.Context, w io.Writer) error

// Dump calls f.
func (f DumpFunc) Dump(ctx context.Context, w io.Writer) error { return f(ctx, w) }

// Options configures a Runner.
type Options struct {
	// Dir holds the backups and their .json sidecars.
	Dir string
	// Key is BOBRES_BACKUP_KEY; Keys are the master keys each backup carries.
	Key  []byte
	Keys map[string]string
	Dump Dumper
	// BotURL and Token deliver each backup to the bot (POST
	// /internal/backups/deliver). Either empty = backups are kept only.
	BotURL string
	Token  string
	// Hour and Minute in Location are the daily run time.
	Hour, Minute int
	Location     *time.Location
	// Client sends to the bot (nil: a plain client; DeliverTimeout applies).
	Client *http.Client
	// Now and FreeSpace replace the clock and statfs in tests.
	Now       func() time.Time
	FreeSpace func(dir string) (uint64, error)
	Log       *slog.Logger
}

// Runner runs backups one at a time.
type Runner struct {
	o    Options
	kick chan struct{}

	mu      sync.Mutex
	running bool
	ran     bool   // a run finished in this process: lastErr is current
	lastErr string // that run's error ("" = it worked)
	next    time.Time
}

// New returns a Runner; Loop runs its schedule.
func New(o Options) (*Runner, error) {
	if o.Dir == "" {
		return nil, errors.New("backupsvc: no backup directory")
	}
	if len(o.Key) < backup.MinKeyLen {
		return nil, fmt.Errorf("backupsvc: the backup key must be at least %d bytes", backup.MinKeyLen)
	}
	if o.Dump == nil {
		return nil, errors.New("backupsvc: no dumper")
	}
	if o.Location == nil {
		o.Location = time.UTC
	}
	if o.Client == nil {
		o.Client = &http.Client{}
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.FreeSpace == nil {
		o.FreeSpace = FreeSpace
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	return &Runner{o: o, kick: make(chan struct{}, 1)}, nil
}

// NextRun is the first hour:minute in loc strictly after now. Days are added
// on the calendar, so a daylight saving change does not shift the time.
func NextRun(now time.Time, hour, minute int, loc *time.Location) time.Time {
	l := now.In(loc)
	t := time.Date(l.Year(), l.Month(), l.Day(), hour, minute, 0, 0, loc)
	for i := 1; !t.After(now); i++ {
		t = time.Date(l.Year(), l.Month(), l.Day()+i, hour, minute, 0, 0, loc)
	}
	return t
}

// Loop runs a backup every day at the set time, and whenever Trigger asks,
// until ctx ends.
func (r *Runner) Loop(ctx context.Context) error {
	next := NextRun(r.o.Now(), r.o.Hour, r.o.Minute, r.o.Location)
	r.setNext(next)
	r.o.Log.Info("backups scheduled", "next", next)
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	for {
		// Sleep at most an hour at a time, so a clock change is noticed.
		wait := min(next.Sub(r.o.Now()), time.Hour)
		if wait <= 0 {
			if r.begin() { // else a triggered run is waiting in kick: it stands in for this one
				_, _ = r.run(ctx) // logged and recorded in its sidecar
			}
			next = NextRun(r.o.Now(), r.o.Hour, r.o.Minute, r.o.Location)
			r.setNext(next)
			continue
		}
		timer.Reset(wait)
		select {
		case <-ctx.Done():
			return nil
		case <-r.kick:
			_, _ = r.run(ctx) // Trigger marked it running; the outcome is in its sidecar
			// A run that went past the scheduled time stands in for it.
			if now := r.o.Now(); !next.After(now) {
				next = NextRun(now, r.o.Hour, r.o.Minute, r.o.Location)
				r.setNext(next)
			}
		case <-timer.C:
		}
	}
}

// Trigger asks Loop for a backup now. It is false when one is already
// running (or waiting to).
func (r *Runner) Trigger() bool {
	if !r.begin() {
		return false
	}
	r.kick <- struct{}{} // cannot block: running guards the one slot
	return true
}

// RunOnce runs one backup now, unless one is running (ErrRunning).
func (r *Runner) RunOnce(ctx context.Context) (backup.Sidecar, error) {
	if !r.begin() {
		return backup.Sidecar{}, ErrRunning
	}
	return r.run(ctx)
}

func (r *Runner) begin() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running {
		return false
	}
	r.running = true
	return true
}

// run makes a backup; begin has been called. It records the outcome and
// ends the running state.
func (r *Runner) run(ctx context.Context) (backup.Sidecar, error) {
	sc, err := r.backup(ctx)
	r.mu.Lock()
	r.running, r.ran, r.lastErr = false, true, sc.Error
	r.mu.Unlock()
	return sc, err
}

func (r *Runner) setNext(t time.Time) {
	r.mu.Lock()
	r.next = t
	r.mu.Unlock()
}

// backup makes one backup: disk check, dump, sidecar, delivery, retention.
// A failed run leaves only a sidecar holding its error.
func (r *Runner) backup(ctx context.Context) (backup.Sidecar, error) {
	start := r.o.Now().UTC().Truncate(time.Second)
	name := backup.FileName(start)
	scPath := filepath.Join(r.o.Dir, backup.SidecarName(name))
	sc := backup.Sidecar{File: name, CreatedAt: start}
	r.o.Log.Info("backup started", "file", name)
	r.removeTemps()

	fail := func(err error) (backup.Sidecar, error) {
		sc.Error = err.Error()
		r.o.Log.Error("backup failed", "file", name, "err", err)
		if werr := backup.WriteSidecar(scPath, sc); werr != nil {
			r.o.Log.Error("backup failure not recorded", "file", name, "err", werr)
		}
		r.prune()
		return sc, err
	}
	if err := r.checkDisk(); err != nil {
		return fail(err)
	}
	size, sum, err := r.write(ctx, name)
	if err != nil {
		return fail(err)
	}
	sc.Size, sc.SHA256 = size, sum
	if err := backup.WriteSidecar(scPath, sc); err != nil {
		r.o.Log.Error("backup sidecar not written", "file", name, "err", err)
	}
	r.o.Log.Info("backup written", "file", name, "size", size)

	if r.o.BotURL != "" && r.o.Token != "" {
		sc.SentAt, sc.TelegramError = r.deliver(ctx, name, size)
		if sc.TelegramError != "" {
			r.o.Log.Warn("backup not sent to Telegram; it is kept on the server", "file", name, "reason", sc.TelegramError)
		}
		if err := backup.WriteSidecar(scPath, sc); err != nil {
			r.o.Log.Error("backup sidecar not written", "file", name, "err", err)
		}
	}
	r.prune()
	return sc, nil
}

// checkDisk refuses a backup when the directory has less free space than
// twice the last backup (MinFree before the first one).
func (r *Runner) checkDisk() error {
	need := uint64(MinFree)
	if last := r.lastSize(); last > 0 {
		need = 2 * uint64(last)
	}
	free, err := r.o.FreeSpace(r.o.Dir)
	if err != nil {
		return fmt.Errorf("check free disk space: %w", err)
	}
	if free < need {
		return fmt.Errorf("not enough free disk space in %s: %s free, %s needed (twice the last backup)",
			r.o.Dir, humanBytes(free), humanBytes(need))
	}
	return nil
}

// lastSize is the size of the newest backup file (0 when there is none).
func (r *Runner) lastSize() int64 {
	names, _ := r.names()
	for i := len(names) - 1; i >= 0; i-- {
		if strings.HasSuffix(names[i], ".bkp") {
			if st, err := os.Stat(filepath.Join(r.o.Dir, names[i])); err == nil {
				return st.Size()
			}
		}
	}
	return 0
}

// write streams pg_dump into an encrypted temp file, syncs it and renames it
// to name. It returns the file's size and SHA-256.
func (r *Runner) write(ctx context.Context, name string) (int64, string, error) {
	tmp, err := os.CreateTemp(r.o.Dir, tempPattern)
	if err != nil {
		return 0, "", fmt.Errorf("create the backup file: %w", err)
	}
	defer os.Remove(tmp.Name()) //nolint:errcheck // gone after the rename
	defer tmp.Close()           //nolint:errcheck // closed (and checked) below on success

	h := sha256.New()
	cw := &countWriter{}
	if err := r.encrypt(ctx, io.MultiWriter(tmp, h, cw)); err != nil {
		return 0, "", err
	}
	if err := tmp.Sync(); err != nil {
		return 0, "", fmt.Errorf("write the backup file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return 0, "", fmt.Errorf("write the backup file: %w", err)
	}
	if err := os.Rename(tmp.Name(), filepath.Join(r.o.Dir, name)); err != nil {
		return 0, "", fmt.Errorf("write the backup file: %w", err)
	}
	syncDir(r.o.Dir)
	return cw.n, hex.EncodeToString(h.Sum(nil)), nil
}

// encrypt runs the dump into backup.Write through a pipe, so the dump is
// never on disk in the clear. A dump that fails is reported as such, not as
// the write error it causes.
func (r *Runner) encrypt(ctx context.Context, w io.Writer) error {
	dctx, cancel := context.WithCancel(ctx)
	defer cancel()
	pr, pw := io.Pipe()
	dumped := make(chan error, 1)
	go func() {
		err := r.o.Dump.Dump(dctx, pw)
		_ = pw.CloseWithError(err) // nil: EOF, the end of the dump
		dumped <- err
	}()
	werr := backup.Write(w, r.o.Key, r.o.Keys, pr)
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

// deliverAnswer is the bot's answer to /internal/backups/deliver.
type deliverAnswer struct {
	Sent      bool   `json:"sent"`
	MessageID int64  `json:"message_id"`
	Reason    string `json:"reason"`
	Error     string `json:"error"`
}

// deliver POSTs the file to the bot, streamed from disk. It returns when it
// was sent, or why not.
func (r *Runner) deliver(ctx context.Context, name string, size int64) (*time.Time, string) {
	f, err := os.Open(filepath.Join(r.o.Dir, name)) //nolint:gosec // our own file name in our own directory
	if err != nil {
		return nil, "open the backup: " + err.Error()
	}
	defer f.Close() //nolint:errcheck // read-only
	ctx, cancel := context.WithTimeout(ctx, DeliverTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(r.o.BotURL, "/")+"/internal/backups/deliver", f)
	if err != nil {
		return nil, err.Error()
	}
	req.ContentLength = size
	req.Header.Set("Authorization", "Bearer "+r.o.Token)
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-Backup-Name", name)
	resp, err := r.o.Client.Do(req)
	if err != nil {
		return nil, "the bot is unreachable: " + err.Error()
	}
	defer resp.Body.Close() //nolint:errcheck // read below
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var a deliverAnswer
	if jerr := json.Unmarshal(body, &a); jerr != nil {
		return nil, fmt.Sprintf("the bot answered HTTP %d: %s", resp.StatusCode, shorten(strings.TrimSpace(string(body)), 200))
	}
	if a.Sent {
		t := r.o.Now().UTC()
		return &t, ""
	}
	msg := a.Reason
	if a.Error != "" {
		msg = strings.TrimPrefix(msg+": "+a.Error, ": ")
	}
	if msg == "" {
		msg = fmt.Sprintf("the bot answered HTTP %d", resp.StatusCode)
	}
	return nil, msg
}

// prune deletes what backup.Prune says is past retention.
func (r *Runner) prune() {
	names, err := r.names()
	if err != nil {
		r.o.Log.Warn("backups not pruned", "err", err)
		return
	}
	for _, n := range backup.Prune(names, backup.Keep) {
		if err := os.Remove(filepath.Join(r.o.Dir, n)); err != nil && !errors.Is(err, os.ErrNotExist) {
			r.o.Log.Warn("old backup not deleted", "file", n, "err", err)
		}
	}
}

// removeTemps deletes temp files a crashed run left; only one run is ever
// writing, and it has not started yet.
func (r *Runner) removeTemps() {
	m, _ := filepath.Glob(filepath.Join(r.o.Dir, tempPattern))
	for _, p := range m {
		_ = os.Remove(p)
	}
}

// names lists the directory, sorted (backup names sort in time order).
func (r *Runner) names() ([]string, error) {
	entries, err := os.ReadDir(r.o.Dir)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.Type().IsRegular() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// Status is what core shows on the System page.
type Status struct {
	// Backups are the sidecars, newest first: kept backups and failed runs.
	Backups []backup.Sidecar `json:"backups"`
	Running bool             `json:"running"`
	// LastError is the last run's error ("" when it worked).
	LastError string `json:"last_error"`
	// NextRun is the next scheduled run, in unix seconds.
	NextRun  int64  `json:"next_run"`
	DiskFree uint64 `json:"disk_free"`
	Dir      string `json:"dir"`
}

// Status lists the backups and the runner's state.
func (r *Runner) Status() Status {
	r.mu.Lock()
	st := Status{Running: r.running, LastError: r.lastErr, Dir: r.o.Dir}
	ran, next := r.ran, r.next
	r.mu.Unlock()
	if next.IsZero() {
		next = NextRun(r.o.Now(), r.o.Hour, r.o.Minute, r.o.Location)
	}
	st.NextRun = next.Unix()
	st.Backups = r.sidecars()
	if !ran && len(st.Backups) > 0 {
		st.LastError = st.Backups[0].Error // the last run before this process started
	}
	if free, err := r.o.FreeSpace(r.o.Dir); err == nil {
		st.DiskFree = free
	}
	return st
}

// sidecars reads every sidecar in the directory, newest first.
func (r *Runner) sidecars() []backup.Sidecar {
	names, err := r.names()
	if err != nil {
		r.o.Log.Warn("backup directory not readable", "err", err)
	}
	out := []backup.Sidecar{}
	for i := len(names) - 1; i >= 0; i-- {
		n := names[i]
		if !strings.HasPrefix(n, "bobres-") || !strings.HasSuffix(n, ".json") {
			continue
		}
		sc, err := backup.ReadSidecar(filepath.Join(r.o.Dir, n))
		if err != nil {
			r.o.Log.Warn("backup sidecar not readable", "file", n, "err", err)
			continue
		}
		out = append(out, sc)
	}
	return out
}

// CheckDir is a readiness check: the backup directory must be writable.
func (r *Runner) CheckDir(context.Context) error {
	f, err := os.CreateTemp(r.o.Dir, ".ready-*.tmp")
	if err != nil {
		return fmt.Errorf("backup directory not writable: %w", err)
	}
	_ = f.Close()
	return os.Remove(f.Name())
}

// FreeSpace is the space available to this user on dir's filesystem.
func FreeSpace(dir string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil //nolint:gosec,unconvert // block size is positive; field types differ by OS
}

// syncDir makes a rename durable (best effort).
func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil { //nolint:gosec // the backup directory
		_ = d.Sync()
		_ = d.Close()
	}
}

type countWriter struct{ n int64 }

func (c *countWriter) Write(p []byte) (int, error) {
	c.n += int64(len(p))
	return len(p), nil
}

func humanBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func shorten(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
