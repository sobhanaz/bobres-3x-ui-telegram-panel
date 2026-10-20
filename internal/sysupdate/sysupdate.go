// Package sysupdate is core's side of the folder it shares with the server's
// updater: how "Update now" in the dashboard reaches `bobres update` without
// core ever holding the Docker socket or any power over the host.
//
// BOBRES runs as Docker Compose. A root systemd path unit on the host (the
// updater, installed by `bobres update`/`bobres install`) watches a folder; core
// sees it at DefaultDir (BOBRES_UPDATE_DIR) and talks to the updater through
// two small files, protocol version 1:
//
//   - request.json: core writes it (a temporary file, then a rename, mode
//     0640, never through a symbolic link) to ask for ONE thing: the update
//     of the install to a release, on a channel. It holds a version number
//     (X.Y.Z or X.Y.Z-label.N, or "" for the newest of the channel), who
//     asked and when, and nothing else: no address, no flag, no environment.
//     The updater takes none of it on trust: it runs the same verified update
//     as `sudo bobres update` (signature check, backup, pull, recreate, health
//     check, automatic rollback).
//   - status.json: the updater writes it, core only reads it, to show progress.
//     Every field is untrusted text: it is read without following links,
//     bounded in size, checked field by field and cut to length; core decides
//     nothing from it except what it must refuse (a helper that is missing or
//     busy).
//
// The folder missing, or status.json saying the updater is not installed,
// means updating from the dashboard is "not available yet".
package sysupdate

import (
	"bytes"
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

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/release"
)

// DefaultDir is where core sees the updater's folder (BOBRES_UPDATE_DIR).
const DefaultDir = "/run/bobres-update"

// The files, the protocol version and the limits.
const (
	RequestFile     = "request.json"
	StatusFile      = "status.json"
	ProtocolVersion = 1
	// ActionUpdate is the only action there is.
	ActionUpdate = "update"
	// MaxRequest is the most a request may take: it carries nothing but its
	// seven short fields. MaxStatus bounds what core will read of status.json.
	MaxRequest = 2 << 10
	MaxStatus  = 64 << 10
	// RequestMode: owner and group (the updater's) read it; nobody else.
	RequestMode os.FileMode = 0o640
	// MaxBy is the longest name of who asked (characters).
	MaxBy = 64
	// MaxLog is how many lines of the updater's log are kept (the newest), each
	// at most MaxLine characters; MaxMessage bounds the one-sentence message.
	MaxLog     = 40
	MaxLine    = 300
	MaxMessage = 300
)

// Errors that say why the updater is not usable; ReadStatus wraps them with
// the details (never with the file's own words).
var (
	// ErrNoDir: the folder is not there (or is not a folder core may use).
	ErrNoDir = errors.New("the updater's folder is missing")
	// ErrNoStatus: the folder is there but the updater has written no status.
	ErrNoStatus = errors.New("the updater has written no status")
	// ErrBadStatus: status.json is not what protocol 1 says (a link, too big,
	// not JSON, a state nobody knows).
	ErrBadStatus = errors.New("the updater's status is unreadable")
	// ErrUnsupported: a protocol version this core does not speak.
	ErrUnsupported = errors.New("the updater speaks another protocol version")
	// ErrNotInstalled: the status says the updater is not installed.
	ErrNotInstalled = errors.New("the updater is not installed")
)

// The updater's states and steps (status.json).
const (
	StateIdle       = "idle"
	StateRunning    = "running"
	StateDone       = "done"
	StateFailed     = "failed"
	StateRolledBack = "rolled_back"

	StepVerify   = "verify"
	StepBackup   = "backup"
	StepPull     = "pull"
	StepRestart  = "restart"
	StepHealth   = "health"
	StepRollback = "rollback"
	StepDone     = "done"
)

var (
	states = []string{StateIdle, StateRunning, StateDone, StateFailed, StateRolledBack}
	steps  = []string{StepVerify, StepBackup, StepPull, StepRestart, StepHealth, StepRollback, StepDone}
)

// Finished reports whether a state is the end of an update.
func Finished(state string) bool {
	return state == StateDone || state == StateFailed || state == StateRolledBack
}

// Request is the one thing core asks of the updater. The field order is the
// protocol's.
type Request struct {
	V       int    `json:"v"`
	ID      string `json:"id"`
	Action  string `json:"action"`
	Channel string `json:"channel"`
	// Version is "" for the newest release of the channel, else exactly the
	// release to install.
	Version string `json:"version"`
	// By is the dashboard user who asked; At when (RFC 3339, UTC).
	By string `json:"by"`
	At string `json:"at"`
}

var idRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// NewRequest makes a request for an update to version ("" for the newest) on
// channel, by a person at at. by is cut to MaxBy characters and made one
// line; an id, channel, version or time that is not valid is an error.
func NewRequest(id, channel, version, by string, at time.Time) (Request, error) {
	r := Request{V: ProtocolVersion, ID: id, Action: ActionUpdate, Channel: channel, Version: version,
		By: Clean(by, MaxBy), At: at.UTC().Format(time.RFC3339)}
	if r.By == "" {
		r.By = "dashboard"
	}
	if err := r.Validate(); err != nil {
		return Request{}, err
	}
	return r, nil
}

// Validate checks every field as the updater will.
func (r Request) Validate() error {
	switch {
	case r.V != ProtocolVersion:
		return fmt.Errorf("sysupdate: request version %d is not %d", r.V, ProtocolVersion)
	case !idRe.MatchString(r.ID):
		return errors.New("sysupdate: the request id is not a lower-case UUID")
	case r.Action != ActionUpdate:
		return fmt.Errorf("sysupdate: action %q is not %q", r.Action, ActionUpdate)
	case r.Channel != release.ChannelStable && r.Channel != release.ChannelBeta:
		return fmt.Errorf("sysupdate: channel %q is not stable or beta", r.Channel)
	case r.Version != "" && !release.ValidNumber(r.Version):
		return fmt.Errorf("sysupdate: version %q is not a release number", r.Version)
	case r.By == "" || utf8.RuneCountInString(r.By) > MaxBy || r.By != Clean(r.By, MaxBy):
		return errors.New("sysupdate: by is not one line of at most 64 characters")
	}
	if at, err := time.Parse(time.RFC3339, r.At); err != nil || !strings.HasSuffix(r.At, "Z") || at.Location() != time.UTC {
		return errors.New("sysupdate: at is not an RFC 3339 time in UTC")
	}
	return nil
}

// Marshal is the request as written: one JSON line, at most MaxRequest bytes.
func (r Request) Marshal() ([]byte, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(r); err != nil {
		return nil, err
	}
	if buf.Len() > MaxRequest {
		return nil, fmt.Errorf("sysupdate: the request is %d bytes, at most %d", buf.Len(), MaxRequest)
	}
	return buf.Bytes(), nil
}

// Status is the updater's status.json after core checked it: every text is
// cut and cleaned, every version, state and step is one the protocol knows
// (anything else is left empty), times are parsed or nil. It is for display.
type Status struct {
	Installed bool
	// Helper is the updater's own version.
	Helper string
	State  string
	// ID is the request being run, or the last one run ("" for none).
	ID       string
	From, To string
	Channel  string
	Step     string
	// Message is one short sentence from the updater.
	Message    string
	StartedAt  *time.Time
	FinishedAt *time.Time
	// Log is the newest lines of the update's log, already scrubbed by the
	// updater and again here (at most MaxLog lines of MaxLine characters).
	Log []string
}

// rawStatus is status.json as the updater writes it. Fields core does not
// know are ignored.
type rawStatus struct {
	V          int      `json:"v"`
	Installed  bool     `json:"installed"`
	Helper     string   `json:"helper"`
	State      string   `json:"state"`
	ID         string   `json:"id"`
	From       string   `json:"from"`
	To         string   `json:"to"`
	Channel    string   `json:"channel"`
	Step       string   `json:"step"`
	Message    string   `json:"message"`
	StartedAt  string   `json:"started_at"`
	FinishedAt string   `json:"finished_at"`
	Log        []string `json:"log"`
}

// Dir is the updater's folder as core sees it.
type Dir struct{ path string }

// NewDir is the folder at path (DefaultDir when empty).
func NewDir(path string) *Dir {
	if path == "" {
		path = DefaultDir
	}
	return &Dir{path: filepath.Clean(path)}
}

// Path is the folder.
func (d *Dir) Path() string { return d.path }

// checkDir is nil when the folder is a real folder (not a link to one).
func (d *Dir) checkDir() error {
	fi, err := os.Lstat(d.path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return ErrNoDir
	case err != nil:
		return fmt.Errorf("%w: %w", ErrNoDir, err)
	case !fi.IsDir():
		return fmt.Errorf("%w: it is not a folder", ErrNoDir)
	}
	return nil
}

// openRegular opens name in the folder for reading without following a link
// and without blocking on anything but a plain file, and checks it is one no
// bigger than limit.
func (d *Dir) openRegular(name string, limit int64) (*os.File, error) {
	f, err := os.OpenFile(filepath.Join(d.path, name), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0) //nolint:gosec // a fixed name in the updater's folder
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, os.ErrNotExist
		}
		return nil, fmt.Errorf("%w: %s cannot be opened (a link?)", ErrBadStatus, name)
	}
	fi, err := f.Stat()
	switch {
	case err != nil:
		_ = f.Close()
		return nil, fmt.Errorf("%w: %s: %w", ErrBadStatus, name, err)
	case !fi.Mode().IsRegular():
		_ = f.Close()
		return nil, fmt.Errorf("%w: %s is not a plain file", ErrBadStatus, name)
	case fi.Size() > limit:
		_ = f.Close()
		return nil, fmt.Errorf("%w: %s is larger than %d bytes", ErrBadStatus, name, limit)
	}
	return f, nil
}

// readSmall reads a whole small file (at most limit bytes).
func (d *Dir) readSmall(name string, limit int64) ([]byte, error) {
	f, err := d.openRegular(name, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrBadStatus, name, err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%w: %s is larger than %d bytes", ErrBadStatus, name, limit)
	}
	return data, nil
}

// ReadStatus reads and checks status.json. It returns an error wrapping
// ErrNoDir, ErrNoStatus, ErrBadStatus, ErrUnsupported or ErrNotInstalled
// (with the status it could read, for the last one) when the updater cannot
// be used; the file's own words never go into an error.
func (d *Dir) ReadStatus() (Status, error) {
	if err := d.checkDir(); err != nil {
		return Status{}, err
	}
	data, err := d.readSmall(StatusFile, MaxStatus)
	if errors.Is(err, os.ErrNotExist) {
		return Status{}, ErrNoStatus
	}
	if err != nil {
		return Status{}, err
	}
	var rawp *rawStatus
	if err := json.Unmarshal(data, &rawp); err != nil || rawp == nil { // null is not a status
		return Status{}, fmt.Errorf("%w: it is not the JSON protocol 1 describes", ErrBadStatus)
	}
	raw := *rawp
	if raw.V != ProtocolVersion {
		return Status{}, fmt.Errorf("%w: version %d", ErrUnsupported, raw.V)
	}
	st := Status{Installed: raw.Installed}
	if !contains(states, raw.State) {
		return Status{}, fmt.Errorf("%w: it names a state this core does not know", ErrBadStatus)
	}
	st.State = raw.State
	st.Helper = number(raw.Helper)
	st.ID = id(raw.ID)
	st.From, st.To = number(raw.From), number(raw.To)
	if c, err := release.ParseChannel(raw.Channel); err == nil && strings.TrimSpace(raw.Channel) != "" {
		st.Channel = c
	}
	if contains(steps, raw.Step) {
		st.Step = raw.Step
	}
	st.Message = Clean(raw.Message, MaxMessage)
	st.StartedAt, st.FinishedAt = when(raw.StartedAt), when(raw.FinishedAt)
	st.Log = cleanLog(raw.Log)
	if !st.Installed {
		return st, ErrNotInstalled
	}
	return st, nil
}

// WriteRequest hands the updater a request: the file is written in full next
// to its place (.request.json.tmp, created fresh, never through a link, mode
// RequestMode), synced, and renamed over request.json, so the updater never
// sees half of it, and a link left at either name is replaced, not followed.
// Two writers must not run at once (core allows one open update at a time).
func (d *Dir) WriteRequest(r Request) error {
	data, err := r.Marshal()
	if err != nil {
		return err
	}
	if err := d.checkDir(); err != nil {
		return err
	}
	final := filepath.Join(d.path, RequestFile)
	tmp := filepath.Join(d.path, "."+RequestFile+".tmp")
	if err := os.Remove(tmp); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("sysupdate: clear the temporary file: %w", err)
	}
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, RequestMode) //nolint:gosec // our own fixed temp name
	if err != nil {
		return fmt.Errorf("sysupdate: create the request: %w", err)
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Chmod(RequestMode) // whatever the umask took away
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, final)
	}
	if err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("sysupdate: write the request: %w", err)
	}
	if dir, err := os.Open(d.path); err == nil { //nolint:gosec // the updater's folder
		_ = dir.Sync() // the rename survives a power cut
		_ = dir.Close()
	}
	return nil
}

// ReadRequest reads request.json (the one core wrote): os.ErrNotExist when
// there is none, an error wrapping ErrBadStatus when it is not a valid
// request.
func (d *Dir) ReadRequest() (*Request, error) {
	if err := d.checkDir(); err != nil {
		return nil, err
	}
	data, err := d.readSmall(RequestFile, MaxRequest)
	if err != nil {
		return nil, err
	}
	var r Request
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("%w: the request is not JSON", ErrBadStatus)
	}
	if err := r.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrBadStatus, err)
	}
	return &r, nil
}

// RemoveRequest takes request.json away when it is the request with this id
// (the updater may have removed it already, which is fine; a request of
// another id, or one that cannot be read, is left alone). It reports whether
// a file was removed.
func (d *Dir) RemoveRequest(requestID string) (bool, error) {
	r, err := d.ReadRequest()
	switch {
	case errors.Is(err, os.ErrNotExist):
		return false, nil
	case err != nil:
		return false, err
	case r.ID != requestID:
		return false, nil
	}
	if err := os.Remove(filepath.Join(d.path, RequestFile)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("sysupdate: remove the request: %w", err)
	}
	return true, nil
}

// Clean makes text one short line fit to show: invalid UTF-8, control
// characters and the characters that turn text around (bidirectional
// overrides and isolates) are dropped, runs of white space become one space,
// and what is longer than max characters is cut with an ellipsis.
func Clean(s string, max int) string {
	s = strings.ToValidUTF8(s, "")
	var b strings.Builder
	space := false
	for _, r := range s {
		switch {
		case unicode.IsSpace(r) || r == '\u00a0':
			space = b.Len() > 0
			continue
		case unicode.IsControl(r) || bidi(r) || r == '\ufeff':
			continue
		}
		if space {
			b.WriteByte(' ')
			space = false
		}
		b.WriteRune(r)
	}
	out := b.String()
	if utf8.RuneCountInString(out) > max {
		out = string([]rune(out)[:max-1]) + "…"
	}
	return out
}

// bidi reports the characters that change the direction text is shown in.
func bidi(r rune) bool {
	return r == '\u200e' || r == '\u200f' || (r >= '\u202a' && r <= '\u202e') || (r >= '\u2066' && r <= '\u2069') || r == '\u061c'
}

// number is a version the updater names, or "" when it is not a release
// number.
func number(v string) string {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if release.ValidNumber(v) {
		return v
	}
	return ""
}

// id is a request id the updater names ("" when it is not one: it can then
// never match a request of ours).
func id(v string) string {
	if idRe.MatchString(v) {
		return v
	}
	return ""
}

func when(s string) *time.Time {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(s))
	if err != nil {
		return nil
	}
	t = t.UTC()
	return &t
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

var (
	// secretRe finds a value after a name that holds one; bearerRe an
	// authorization header's token; botTokenRe a Telegram bot token.
	secretRe   = regexp.MustCompile(`(?i)\b(token|secret|password|passwd|authorization|api[_-]?key)\b(\s*[:=]\s*)\S+`)
	bearerRe   = regexp.MustCompile(`(?i)\bbearer\s+\S+`)
	botTokenRe = regexp.MustCompile(`\b\d{6,12}:[A-Za-z0-9_-]{30,}\b`)
)

// scrub masks what looks like a secret in a log line: the updater scrubs its
// log, and this is a second look, not the first.
func scrub(s string) string {
	s = botTokenRe.ReplaceAllString(s, "***")
	s = bearerRe.ReplaceAllString(s, "Bearer ***")
	return secretRe.ReplaceAllString(s, "$1$2***")
}

// cleanLog keeps the newest MaxLog non-empty lines, each cleaned, scrubbed
// and cut.
func cleanLog(lines []string) []string {
	out := make([]string, 0, min(len(lines), MaxLog))
	for _, l := range lines {
		if l = Clean(scrub(strings.ToValidUTF8(l, "")), MaxLine); l != "" {
			out = append(out, l)
		}
	}
	if len(out) > MaxLog {
		out = out[len(out)-MaxLog:]
	}
	return out
}
