package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// statusKeys are the fields of status.json, protocol version 1: core and the helper agree on
// exactly these.
// The dashboard's restore added action, restore, subject, source, by and requested_at; core ignores what it does not know, and an
// older helper's status has none of them.
var statusKeys = []string{"v", "installed", "helper", "state", "id", "from", "to", "channel", "step", "message", "started_at", "finished_at", "log",
	"action", "restore", "subject", "source", "by", "requested_at"}

func readStatusMap(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("status.json is not JSON: %v\n%s", err, b)
	}
	return m
}

func sortedKeys(m map[string]any) []string {
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func TestStatusHasTheProtocolsFields(t *testing.T) {
	dir, _ := updateDirFixture(t)
	path := statusPath(dir)

	// Idle: every key is there, the times are null, the log is an empty list.
	if wrote, err := ensureStatus(dir, "1.2.3", true); err != nil || !wrote {
		t.Fatalf("ensureStatus: %v %v", wrote, err)
	}
	m := readStatusMap(t, path)
	want := append([]string(nil), statusKeys...)
	sort.Strings(want)
	if got := sortedKeys(m); !reflect.DeepEqual(got, want) {
		t.Errorf("keys %v, want %v", got, want)
	}
	if m["v"] != float64(1) || m["installed"] != true || m["helper"] != "1.2.3" || m["state"] != "idle" ||
		m["started_at"] != nil || m["finished_at"] != nil {
		t.Errorf("idle status: %v", m)
	}
	if log, ok := m["log"].([]any); !ok || len(log) != 0 {
		t.Errorf("log %#v, want []", m["log"])
	}
	for _, k := range []string{"id", "from", "to", "channel", "step", "message"} {
		if m[k] != "" {
			t.Errorf("%s = %#v, want an empty string", k, m[k])
		}
	}
	if st, err := os.Stat(path); err != nil || st.Mode().Perm() != 0o644 {
		t.Errorf("status.json: %v %v, want mode 0644", st, err)
	}

	// A running update: the times are RFC 3339 in UTC, to the second.
	sf := newStatusFile(path, nil)
	started := time.Date(2026, 10, 20, 12, 0, 0, 999, time.FixedZone("x", 3*3600))
	sf.update(func(s *helperStatus) {
		*s = helperStatus{V: 1, Installed: true, Helper: "1.2.3", State: helperRunning, ID: reqID, From: "1.2.3", To: "1.3.0",
			Channel: "beta", Step: stepBackingUp, Message: "Backing up.", StartedAt: stamp(started)}
	})
	m = readStatusMap(t, path)
	if m["started_at"] != "2026-10-20T09:00:00Z" || m["finished_at"] != nil || m["state"] != "running" || m["step"] != "backup" || m["channel"] != "beta" {
		t.Errorf("running status: %v", m)
	}
}

// The directory is core's, so a link, a pipe or a directory where status.json goes is replaced or
// reported, never followed, and no temp file stays behind.
func TestWriteHelperStatusIsSafeInCoresDirectory(t *testing.T) {
	dir, upd := updateDirFixture(t)
	path := statusPath(dir)
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	st := idleStatus(true, "1.2.3")

	if err := os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	if err := writeHelperStatus(path, &st); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(outside); string(b) != "mine" {
		t.Errorf("the target of the link was written: %q", b)
	}
	if fi, err := os.Lstat(path); err != nil || !fi.Mode().IsRegular() {
		t.Errorf("status.json is %v %v, want a regular file", fi, err)
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- writeHelperStatus(path, &st) }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("a pipe in the way: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("writing past a pipe blocked")
	}
	if fi, _ := os.Lstat(path); fi == nil || !fi.Mode().IsRegular() {
		t.Errorf("status.json is %v after the pipe", fi)
	}

	// A directory cannot be replaced by a file: an error, and the update goes on (statusFile says so once).
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "x"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeHelperStatus(path, &st); err == nil {
		t.Error("a directory in the way was not reported")
	}
	var said []string
	sf := newStatusFile(path, nil)
	sf.warn = func(f string, a ...any) { said = append(said, fmt.Sprintf(f, a...)) }
	sf.update(func(s *helperStatus) { s.State = helperRunning })
	sf.update(func(s *helperStatus) { s.Step = stepBackingUp })
	if len(said) != 1 || !strings.Contains(said[0], "could not be written") {
		t.Errorf("the failure was said %d times: %q", len(said), said)
	}

	entries, _ := os.ReadDir(upd)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("a temp file stayed: %s", e.Name())
		}
	}
}

func TestReadHelperStatus(t *testing.T) {
	dir, _ := updateDirFixture(t)
	path := statusPath(dir)
	if _, ok := readHelperStatus(path); ok {
		t.Error("read a status that is not there")
	}
	for name, body := range map[string]string{"not JSON": "hello", "another protocol": `{"v":2,"installed":true}`, "empty": ""} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, ok := readHelperStatus(path); ok {
			t.Errorf("%s: taken for a status", name)
		}
	}
	if err := os.WriteFile(path, make([]byte, 300<<10), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := readHelperStatus(path); ok {
		t.Error("a huge file taken for a status")
	}
	_ = os.Remove(path)
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan bool, 1)
	go func() { _, ok := readHelperStatus(path); done <- ok }()
	select {
	case ok := <-done:
		if ok {
			t.Error("a pipe taken for a status")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reading a pipe blocked")
	}
	_ = os.Remove(path)
	outside := filepath.Join(t.TempDir(), "s.json")
	if err := os.WriteFile(outside, []byte(`{"v":1,"installed":true,"state":"idle"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	if _, ok := readHelperStatus(path); ok {
		t.Error("followed a link")
	}
}

func TestEnsureStatusRules(t *testing.T) {
	dir, _ := updateDirFixture(t)
	path := statusPath(dir)
	state := func() helperStatus {
		t.Helper()
		st, ok := readHelperStatus(path)
		if !ok {
			t.Fatal("no status")
		}
		return st
	}

	// Switched off and no status yet: nothing to say.
	if wrote, err := ensureStatus(dir, "1.2.3", false); err != nil || wrote {
		t.Errorf("off, no file: %v %v", wrote, err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a status was made for a helper that is off: %v", err)
	}
	// Installed: idle.
	if wrote, err := ensureStatus(dir, "1.2.3", true); err != nil || !wrote {
		t.Fatalf("install: %v %v", wrote, err)
	}
	if st := state(); st.State != helperIdle || !st.Installed || st.Helper != "1.2.3" {
		t.Errorf("after install: %+v", st)
	}
	// The same again changes nothing.
	if wrote, _ := ensureStatus(dir, "1.2.3", true); wrote {
		t.Error("wrote a status that was right")
	}
	// A result of this version stays through a settings change (an install of the same version).
	done := state()
	done.State, done.ID, done.Message = helperDone, reqID, "BOBRES was updated."
	if err := writeHelperStatus(path, &done); err != nil {
		t.Fatal(err)
	}
	if wrote, _ := ensureStatus(dir, "1.2.3", true); wrote {
		t.Error("an install of the same version wiped the last result")
	}
	if st := state(); st.State != helperDone || st.ID != reqID {
		t.Errorf("the result was lost: %+v", st)
	}
	// A new version belongs to a new release: idle again.
	if wrote, _ := ensureStatus(dir, "1.3.0", true); !wrote {
		t.Error("a new version left the old result")
	}
	if st := state(); st.State != helperIdle || st.Helper != "1.3.0" || st.ID != "" {
		t.Errorf("after a new version: %+v", st)
	}
	// A run in progress is never touched.
	run := state()
	run.State, run.ID = helperRunning, reqID
	if err := writeHelperStatus(path, &run); err != nil {
		t.Fatal(err)
	}
	if wrote, _ := ensureStatus(dir, "1.4.0", true); wrote {
		t.Error("touched the status of a run in progress")
	}
	if st := state(); st.State != helperRunning || st.Helper != "1.3.0" {
		t.Errorf("a running status changed: %+v", st)
	}
	// Switched off after it was on: installed false.
	run.State = helperIdle
	if err := writeHelperStatus(path, &run); err != nil {
		t.Fatal(err)
	}
	if wrote, _ := ensureStatus(dir, "1.3.0", false); !wrote {
		t.Error("did not say the helper is off")
	}
	if st := state(); st.Installed {
		t.Errorf("still installed: %+v", st)
	}
	// Corrupt: replaced.
	if err := os.WriteFile(path, []byte("{{"), 0o644); err != nil { //nolint:gosec // a fixture
		t.Fatal(err)
	}
	if wrote, _ := ensureStatus(dir, "1.3.0", true); !wrote || !state().Installed {
		t.Error("a corrupt status was left")
	}
}

func TestClearStaleStatus(t *testing.T) {
	dir, _ := updateDirFixture(t)
	path := statusPath(dir)
	clearStaleStatus(dir, reqNow) // no file: nothing happens
	if _, err := os.Stat(path); err == nil {
		t.Fatal("made a status")
	}
	run := idleStatus(true, "1.2.3")
	run.State, run.ID, run.Step, run.To, run.StartedAt = helperRunning, reqID, stepPulling, "1.3.0", stamp(reqNow.Add(-time.Hour))
	run.Log = []string{"[ 40%] Pulling images"}
	if err := writeHelperStatus(path, &run); err != nil {
		t.Fatal(err)
	}
	clearStaleStatus(dir, reqNow)
	st, _ := readHelperStatus(path)
	if st.State != helperFailed || st.ID != reqID || st.Step != stepPulling || st.FinishedAt == nil || *st.FinishedAt != "2026-10-20T12:00:00Z" ||
		!strings.Contains(st.Message, "interrupted") || len(st.Log) != 1 {
		t.Errorf("a stale run: %+v", st)
	}
	// A result is left alone.
	before, _ := os.ReadFile(path)
	clearStaleStatus(dir, reqNow.Add(time.Hour))
	if after, _ := os.ReadFile(path); !bytes.Equal(before, after) {
		t.Error("a final status was rewritten")
	}
}

func TestStatusLogKeepsTheLastLinesScrubbed(t *testing.T) {
	dir, _ := updateDirFixture(t)
	secret := "s3cr3t-value-of-the-backup-key-0123456789"
	sf := newStatusFile(statusPath(dir), newRedactor(map[string]string{"BOBRES_BACKUP_KEY": secret, "BOBRES_DOMAIN": "panel.example.com"}).line)
	sf.update(func(s *helperStatus) { *s = helperStatus{V: 1, Installed: true, State: helperRunning} })
	for i := 1; i <= 60; i++ {
		sf.line(fmt.Sprintf("line %d", i))
	}
	sf.line("the key is " + secret + " and the bot is 123456789:AAH-abcdefghijklmnopqrstuvwxyz012345")
	sf.line("   ") // nothing
	sf.line(strings.Repeat("x ", 400))
	sf.update(func(s *helperStatus) {})
	m := readStatusMap(t, statusPath(dir))
	log := m["log"].([]any)
	if len(log) != statusLogLines {
		t.Fatalf("%d lines kept, want %d", len(log), statusLogLines)
	}
	text := fmt.Sprint(log)
	if strings.Contains(text, secret) || strings.Contains(text, "AAH-abcdef") {
		t.Errorf("a secret reached the log:\n%s", text)
	}
	if scrubbed := log[len(log)-2].(string); !strings.HasPrefix(scrubbed, "the key is ") || strings.Count(scrubbed, redacted) != 2 {
		t.Errorf("the line with the secrets: %q", scrubbed)
	}
	if first := log[0].(string); first != "line 23" { // 60 + 2 lines, the newest 40
		t.Errorf("the oldest kept line is %q", first)
	}
	last := log[len(log)-1].(string)
	if len([]rune(last)) > statusLineRunes {
		t.Errorf("a line of %d characters was kept", len([]rune(last)))
	}
}

func TestLogTeeSplitsLinesAndForwards(t *testing.T) {
	dir, _ := updateDirFixture(t)
	sf := newStatusFile(statusPath(dir), nil)
	var out bytes.Buffer
	tee := &logTee{w: &out, st: sf}
	for _, chunk := range []string{"one\ntw", "o\r three\r\n", "\n\nfour"} {
		if n, err := tee.Write([]byte(chunk)); err != nil || n != len(chunk) {
			t.Fatalf("write: %d %v", n, err)
		}
	}
	if out.String() != "one\ntwo\r three\r\n\n\nfour" {
		t.Errorf("not forwarded as written: %q", out.String())
	}
	if got := sf.current().Log; !reflect.DeepEqual(got, []string{"one", "two", "three"}) {
		t.Errorf("lines %q", got)
	}
	tee.flush()
	if got := sf.current().Log; !reflect.DeepEqual(got, []string{"one", "two", "three", "four"}) {
		t.Errorf("after flush %q", got)
	}
	// A line that never ends is cut.
	tee.Write([]byte(strings.Repeat("y", 5000))) //nolint:errcheck // a bytes.Buffer
	if got := sf.current().Log; len(got) != 5 {
		t.Errorf("a long line made %d lines", len(got))
	}
	// The writer's error is passed on.
	bad := &logTee{w: failingWriter{}, st: sf}
	if _, err := bad.Write([]byte("x\n")); err == nil {
		t.Error("a failing writer was not reported")
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

// Two streams (stdout and stderr of a child) write at once.
func TestStatusFileIsSafeForConcurrentWriters(t *testing.T) {
	dir, _ := updateDirFixture(t)
	sf := newStatusFile(statusPath(dir), newRedactor(nil).line)
	sf.update(func(s *helperStatus) {
		*s = helperStatus{V: 1, Installed: true, State: helperRunning, Step: stepPulling}
	})
	sf.lineDelay = 0
	stop := sf.startFlusher(time.Millisecond)
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tee := &logTee{w: io.Discard, st: sf}
			for i := 0; i < 200; i++ {
				fmt.Fprintf(tee, "worker line %d from 203.0.113.%d\n", i, i%250)
				if i%50 == 0 {
					sf.update(func(s *helperStatus) { s.Message = "m" })
				}
			}
			tee.flush()
		}()
	}
	wg.Wait()
	stop()
	stop() // a second call is harmless
	if st, ok := readHelperStatus(statusPath(dir)); !ok || st.State != helperRunning {
		t.Errorf("status after the writers: %+v %v", st, ok)
	}
}

func TestStatusFlusherWritesNewLinesAndTheHeartbeat(t *testing.T) {
	dir, _ := updateDirFixture(t)
	var mu sync.Mutex
	writes := 0
	sf := newStatusFile(statusPath(dir), nil)
	sf.lineDelay, sf.heartbeat = 20*time.Millisecond, 80*time.Millisecond
	sf.observe = func(helperStatus) { mu.Lock(); writes++; mu.Unlock() }
	count := func() int { mu.Lock(); defer mu.Unlock(); return writes }
	sf.update(func(s *helperStatus) { *s = helperStatus{V: 1, Installed: true, State: helperRunning} })
	stop := sf.startFlusher(5 * time.Millisecond)
	defer stop()

	sf.line("a new line")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if st, ok := readHelperStatus(statusPath(dir)); ok && len(st.Log) == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if st, _ := readHelperStatus(statusPath(dir)); len(st.Log) != 1 {
		t.Fatalf("the new line was not written: %+v", st)
	}
	before := count()
	time.Sleep(400 * time.Millisecond) // nothing changes: only the heartbeat writes
	if n := count() - before; n < 2 {
		t.Errorf("%d heartbeat writes in 400ms, want several", n)
	}
	// Once the update is over, nothing writes any more.
	sf.update(func(s *helperStatus) { s.State = helperDone })
	before = count()
	time.Sleep(300 * time.Millisecond)
	if n := count() - before; n != 0 {
		t.Errorf("%d writes after the final status", n)
	}
}

// The install's own step names are how the helper tells "pull" from "restart".
func TestStatusStepFollowsTheInstallsProgress(t *testing.T) {
	dir, _ := updateDirFixture(t)
	sf := newStatusFile(statusPath(dir), nil)
	sf.update(func(s *helperStatus) {
		*s = helperStatus{V: 1, Installed: true, State: helperRunning, Step: stepBackingUp}
	})
	sf.line("[ 70%] " + installStepName(stepStart)) // not in the pull step yet
	if got := sf.current().Step; got != stepBackingUp {
		t.Errorf("step %q", got)
	}
	sf.update(func(s *helperStatus) { s.Step = stepPulling })
	sf.line("[ 50%] " + installStepName(stepPull))
	if got := sf.current().Step; got != stepPulling {
		t.Errorf("step %q while pulling", got)
	}
	sf.line("[ 70%] " + installStepName(stepStart))
	if st := readStatusMap(t, statusPath(dir)); st["step"] != "restart" {
		t.Errorf("step %v after the stack starts", st["step"])
	}
}
