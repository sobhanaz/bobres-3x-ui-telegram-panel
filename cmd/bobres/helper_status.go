package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/version"
)

// The states and steps status.json reports (protocol version 1).
const (
	helperIdle       = "idle"
	helperRunning    = "running"
	helperDone       = "done"
	helperFailed     = "failed"
	helperRolledBack = "rolled_back"

	stepVerifying  = "verify"
	stepBackingUp  = "backup"
	stepPulling    = "pull"
	stepRestarting = "restart"
	stepWaiting    = "health"
	stepRollingBk  = "rollback"
	stepFinished   = "done"

	// statusLogLines is how many lines of the update's output status.json keeps (the newest).
	statusLogLines = 40
	// statusLineRunes cuts a line of that log short.
	statusLineRunes = 200
	// statusHeartbeat is how often a running update's status is written when nothing changed, so
	// that its age tells whether the helper is alive.
	statusHeartbeat = 30 * time.Second
)

// helperStatus is status.json. The helper writes it (root's file, mode 0644, replaced atomically)
// and core only reads it. The dashboard knows the helper exists by "installed": a missing file means
// it is not installed yet.
type helperStatus struct {
	V         int    `json:"v"`
	Installed bool   `json:"installed"`
	Helper    string `json:"helper"` // the version of the bobres the units run
	State     string `json:"state"`  // idle, running, done, failed or rolled_back
	ID        string `json:"id"`     // of the request this is about
	From      string `json:"from"`
	To        string `json:"to"`
	Channel   string `json:"channel"`
	Step      string `json:"step"`
	Message   string `json:"message"` // one short sentence
	// StartedAt and FinishedAt are RFC 3339 times in UTC, null while they are not known.
	StartedAt  *string  `json:"started_at"`
	FinishedAt *string  `json:"finished_at"`
	Log        []string `json:"log"` // the last lines of the update's output, secrets scrubbed
}

// stamp is a time as status.json has it.
func stamp(t time.Time) *string {
	s := t.UTC().Truncate(time.Second).Format(time.RFC3339)
	return &s
}

// helperVersion is the version this bobres reports as the helper's.
func helperVersion() string { return strings.TrimPrefix(version.Version, "v") }

// statusPath is where the helper reports.
func statusPath(dir string) string { return filepath.Join(updateDirPath(dir), statusFileName) }

// readHelperStatus reads status.json the way a file in a directory core controls must be read:
// without following a link or waiting for a pipe, a regular file of a sane size. ok is false when
// there is none or it is not a status of this protocol.
func readHelperStatus(path string) (st helperStatus, ok bool) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_NOCTTY, 0) //nolint:gosec // opened without following links, then looked at through the descriptor
	if err != nil {
		return st, false
	}
	defer f.Close() //nolint:errcheck // read-only
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() || fi.Size() > 256<<10 {
		return st, false
	}
	b, err := io.ReadAll(io.LimitReader(f, 256<<10))
	if err != nil || json.Unmarshal(b, &st) != nil || st.V != requestProtocol {
		return helperStatus{}, false
	}
	return st, true
}

// writeHelperStatus replaces status.json in one rename; the directory is core's, so a link or
// anything else at its place is replaced, never followed.
func writeHelperStatus(path string, st *helperStatus) error {
	if st.Log == nil {
		st.Log = []string{}
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, append(b, '\n'), 0o644)
}

// idleStatus is the status of a helper that is installed and has nothing to say.
func idleStatus(installed bool, helper string) helperStatus {
	return helperStatus{V: requestProtocol, Installed: installed, Helper: helper, State: helperIdle, Log: []string{}}
}

// ensureStatus makes status.json say the helper is installed (installed true), or that it is not
// (a helper the owner switched off). `bobres install` and `bobres update` call it. It writes
// "idle" when there is no status yet, when it is not readable, or when the helper's version changed
// (the last result belongs to the release before); a result of the same version stays until the
// next request, and a run in progress is never touched. It reports whether it wrote.
func ensureStatus(dir, helper string, installed bool) (wrote bool, err error) {
	path := statusPath(dir)
	st, ok := readHelperStatus(path)
	switch {
	case ok && st.State == helperRunning:
		return false, nil
	case ok && st.Installed == installed && (!installed || st.Helper == helper):
		return false, nil
	case !ok && !installed:
		return false, nil // no file already says "not installed"
	}
	idle := idleStatus(installed, helper)
	return true, writeHelperStatus(path, &idle)
}

// clearStaleStatus is called by whoever holds the install lock: nothing can be running then, so a
// status that says "running" is one the helper never finished (it was killed, or the server went
// down). It says so instead of leaving the dashboard waiting.
func clearStaleStatus(dir string, now time.Time) {
	path := statusPath(dir)
	st, ok := readHelperStatus(path)
	if !ok || st.State != helperRunning {
		return
	}
	st.State = helperFailed
	st.Message = "The update was interrupted before it finished (bobres or the server stopped). Check the store: sudo bobres status"
	st.FinishedAt = stamp(now)
	_ = writeHelperStatus(path, &st)
}

// statusFile is the status of the update the helper is running. It keeps what the update printed
// (the log tail) and writes status.json at each change of step, and now and then while nothing
// changes, so that its age says the helper is alive.
type statusFile struct {
	mu        sync.Mutex
	path      string
	cur       helperStatus
	lines     []string
	dirty     bool
	lastWrite time.Time
	now       func() time.Time
	// redact scrubs a line of output before it is kept.
	redact func(string) string
	// warn says a failure to write the status (nowhere else to say it: the status is the report).
	warn   func(format string, args ...any)
	warned bool
	// lineDelay and heartbeat are how long new log lines, and nothing at all, wait for a write.
	lineDelay, heartbeat time.Duration
	// observe, when set, sees every status as it is written (tests).
	observe func(helperStatus)
	stop    chan struct{}
	done    chan struct{}
}

func newStatusFile(path string, redact func(string) string) *statusFile {
	s := &statusFile{path: path, now: time.Now, redact: redact, lineDelay: 2 * time.Second, heartbeat: statusHeartbeat}
	s.warn = func(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...) }
	if s.redact == nil {
		s.redact = func(l string) string { return l }
	}
	return s
}

// update changes the status and writes it.
func (s *statusFile) update(change func(*helperStatus)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	change(&s.cur)
	s.writeLocked()
}

// writeLocked writes status.json; a failure is said once and never stops the update.
func (s *statusFile) writeLocked() {
	s.cur.Log = slices.Clone(s.lines)
	if err := writeHelperStatus(s.path, &s.cur); err != nil && !s.warned {
		s.warned = true
		s.warn("warning: the dashboard's status file could not be written: %v", err)
	}
	s.dirty, s.lastWrite = false, s.now()
	if s.observe != nil {
		s.observe(s.cur)
	}
}

// current is a copy of the status.
func (s *statusFile) current() helperStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.cur
	c.Log = slices.Clone(s.lines)
	return c
}

// line keeps one line of the update's output in the log tail.
func (s *statusFile) line(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	text = s.redact(text) // under the lock: the scrubber keeps a table of its own
	if r := []rune(text); len(r) > statusLineRunes {
		text = string(r[:statusLineRunes-3]) + "..."
	}
	s.lines = append(s.lines, text)
	if len(s.lines) > statusLogLines {
		s.lines = s.lines[len(s.lines)-statusLogLines:]
	}
	s.dirty = true
	// The new release's install runs as a program of its own, and prints the progress of its
	// steps: its start of the stack is the dashboard's "restart" step.
	if s.cur.State == helperRunning && s.cur.Step == stepPulling && strings.Contains(text, installStepName(stepStart)) {
		s.cur.Step, s.cur.Message = stepRestarting, "Starting the new release."
		s.writeLocked()
	}
}

// installStepName is the name the install shows for one of its steps.
func installStepName(key string) string {
	for _, d := range installSteps {
		if d.key == key {
			return d.name
		}
	}
	return key
}

// startFlusher writes the status while an update runs: new log lines at most every lineDelay, and
// a heartbeat every heartbeat. The returned function stops it.
func (s *statusFile) startFlusher(tick time.Duration) (stop func()) {
	s.stop, s.done = make(chan struct{}), make(chan struct{})
	go func() {
		defer close(s.done)
		t := time.NewTicker(tick)
		defer t.Stop()
		for {
			select {
			case <-s.stop:
				return
			case <-t.C:
				s.mu.Lock()
				age := s.now().Sub(s.lastWrite)
				if s.cur.State == helperRunning && ((s.dirty && age >= s.lineDelay) || age >= s.heartbeat) {
					s.writeLocked()
				}
				s.mu.Unlock()
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			close(s.stop)
			<-s.done
		})
	}
}

// logTee passes what is written to it on to w and keeps its lines in the status log.
type logTee struct {
	w   io.Writer
	st  *statusFile
	mu  sync.Mutex
	buf []byte
}

func (t *logTee) Write(p []byte) (int, error) {
	n, err := t.w.Write(p)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p[:n]...)
	for {
		i := bytes.IndexAny(t.buf, "\r\n")
		if i < 0 {
			break
		}
		t.st.line(string(t.buf[:i]))
		t.buf = t.buf[i+1:]
	}
	if len(t.buf) > 4096 { // a line that never ends
		t.st.line(string(t.buf))
		t.buf = nil
	}
	return n, err
}

// flush keeps the line that was not ended.
func (t *logTee) flush() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.buf) > 0 {
		t.st.line(string(t.buf))
		t.buf = nil
	}
}
