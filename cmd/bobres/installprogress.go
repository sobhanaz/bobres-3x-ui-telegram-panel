package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/version"
)

// The steps of `bobres install` and their weights (they add up to 100): the
// pull and the wait for the services are the long ones. install/install.sh keeps
// the steps before this one, and installCLISteps there must equal len(installSteps)
// (TestInstallShStepsMatchGo fails when it does not).
const (
	stepWizard  = "wizard"
	stepChecks  = "checks"
	stepWrite   = "write"
	stepPull    = "pull"
	stepStart   = "start"
	stepHealthy = "healthy"
	stepHarden  = "harden"
	stepSummary = "summary"
)

var installSteps = []stepDef{
	{stepWizard, "Asking for your settings", 8, askStep, "Setup"},
	{stepChecks, "Checking this server", 7, liveStep, "Setup"},
	{stepWrite, "Writing the install files", 5, liveStep, "Install"},
	{stepPull, "Pulling images", 35, liveStep, "Install"},
	{stepStart, "Starting the stack", 10, liveStep, "Install"},
	{stepHealthy, "Waiting for the services to be healthy", 25, liveStep, "Install"},
	{stepHarden, "Hardening the server", 8, liveStep, "Secure"},
	{stepSummary, "Summary", 2, liveStep, "Done"},
}

// startProgress begins the install's progress on ins.out and ins.errOut and
// sends the install's other output through it.
func (ins *installer) startProgress(t termUI, h handoff) {
	p := newProgress(ins.out, ins.errOut, t, installSteps, h)
	p.quiet = ins.quiet
	p.log = ins.openLog()
	ins.ui, ins.prog, ins.out, ins.errOut = t, p, p.Out(), p.Err()
}

// installLogPath is where the install writes its log: every step, the output
// of the programs it runs and what the hardening changed, never a secret.
const installLogPath = "/var/log/bobres-install.log"

// openLog opens the install log (append, readable by root only) and writes
// the start of this run to it; nil when there is none or it cannot be opened
// (a run that is not root's).
func (ins *installer) openLog() io.Writer {
	if ins.logPath == "" {
		return nil
	}
	f, err := os.OpenFile(ins.logPath, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600) //nolint:gosec // a fixed log path
	if err != nil {
		return nil
	}
	_ = f.Chmod(0o600)
	ins.logFile = f
	fmt.Fprintf(f, "\n==== bobres install %s, %s ====\n", version.Version, time.Now().Format(time.RFC3339))
	return f
}

// logOut is where the output of programs the install runs goes: the log, or
// nowhere.
func (ins *installer) logOut() io.Writer {
	if ins.logFile != nil {
		return ins.logFile
	}
	return io.Discard
}

// tailLog is the last n lines of the install log, "" when there is none.
func tailLog(path string, n int) string {
	b, err := os.ReadFile(path) //nolint:gosec // the install log
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// keepGoing makes the process outlive a dropped SSH connection: a hang-up
// (the terminal went away) and a write to a closed pipe no longer end it. What
// it prints goes to the log too, so the install can be followed from a new
// session.
func keepGoing() {
	signal.Ignore(syscall.SIGHUP, syscall.SIGPIPE)
}

// composeEvents follows what `docker compose up -d` prints when its output is
// not a terminal: one line per event, such as " Image caddy:2 Pulled" and
// " Container bobres-core-1  Started" (older versions put a symbol first and
// leave "Image" out). It counts the images that are pulled and the containers
// that are started; a line it does not know is ignored, so when compose prints
// something else nothing is measured and the step simply ends with the
// command.
type composeEvents struct {
	pulling, pulled     map[string]bool
	containers, started map[string]bool
}

func newComposeEvents() *composeEvents {
	return &composeEvents{pulling: map[string]bool{}, pulled: map[string]bool{},
		containers: map[string]bool{}, started: map[string]bool{}}
}

var composeEventRe = regexp.MustCompile(`^[^0-9A-Za-z_]*(?:(Image|Container)\s+)?(\S+)\s+(Pulling|Pulled|Creating|Created|Starting|Started|Running|Healthy)[^0-9A-Za-z_]*$`)

// feed reads one line and reports whether it was an event.
func (e *composeEvents) feed(line string) bool {
	m := composeEventRe.FindStringSubmatch(line)
	if m == nil {
		return false
	}
	kind, name, what := m[1], m[2], m[3]
	switch what {
	case "Pulling":
		e.pulling[name] = true
	case "Pulled":
		e.pulling[name], e.pulled[name] = true, true
	default:
		if kind == "Image" {
			return true
		}
		e.containers[name] = true
		if what == "Started" || what == "Running" || what == "Healthy" {
			e.started[name] = true
		}
	}
	return true
}

// lineWriter keeps what is written to it and hands each finished line (ended by
// a newline or a carriage return) to onLine.
type lineWriter struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	line   []byte
	onLine func(string)
}

func (w *lineWriter) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf.Write(b)
	for _, c := range b {
		if c == '\n' || c == '\r' {
			w.emit()
			continue
		}
		w.line = append(w.line, c)
	}
	return len(b), nil
}

func (w *lineWriter) emit() {
	if len(w.line) > 0 && w.onLine != nil {
		w.onLine(string(w.line))
	}
	w.line = w.line[:0]
}

// streamDocker runs docker with its combined output handed to onLine line by
// line as it appears, and returned whole when it ends.
func streamDocker(ctx context.Context, dir string, onLine func(string), args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "docker", args...) //nolint:gosec // fixed program, arguments built here
	cmd.Dir = dir
	lw := &lineWriter{onLine: onLine}
	cmd.Stdout, cmd.Stderr = lw, lw
	err := cmd.Run()
	lw.mu.Lock()
	lw.emit()
	lw.mu.Unlock()
	return lw.buf.Bytes(), err
}

// upProgress is the line handler of `docker compose up -d`: the pull's measured
// part while images come in, then the step of starting the stack, with its
// measured part while the containers start.
func (ins *installer) upProgress() func(string) {
	ev := newComposeEvents()
	begun := false
	return func(line string) {
		if !ev.feed(line) {
			return
		}
		if n := len(ev.containers); n > 0 {
			if !begun {
				begun = true
				ins.prog.Begin(stepStart)
			}
			ins.prog.Fraction(len(ev.started), n, fmt.Sprintf("%d/%d containers", len(ev.started), n))
			return
		}
		if n := len(ev.pulling); n > 0 {
			ins.prog.Fraction(len(ev.pulled), n, fmt.Sprintf("%d/%d images", len(ev.pulled), n))
		}
	}
}

// The steps of `bobres update`. Its install is another process (childStep): it
// is given the share of the bar its weight covers (handoffEnv).
const (
	stepUpVerify   = "verify"
	stepUpDownload = "download"
	stepUpBackup   = "backup"
	stepUpInstall  = "install"
	stepUpWait     = "wait"
)

var updateSteps = []stepDef{
	{stepUpVerify, "Checking the release signature", 10, liveStep, ""},
	{stepUpDownload, "Downloading the new bobres", 10, liveStep, ""},
	{stepUpBackup, "Backing up the database", 15, liveStep, ""},
	{stepUpInstall, "Installing the new release", 50, childStep, ""},
	{stepUpWait, "Waiting for the services to be healthy", 15, liveStep, ""},
}

// startProgress begins the update's progress on u.out and u.errOut and sends
// the update's other output through it.
func (u *updater) startProgress(t termUI) {
	p := newProgress(u.out, u.errOut, t, updateSteps, handoff{})
	u.prog, u.out, u.errOut = p, p.Out(), p.Err()
}

// handoffEnv is the environment that gives a process the step named key's share
// of the bar (its own steps are not numbered), and plain output where this one
// is plain.
func (p *progress) handoffEnv(key string) []string {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	base, span := p.m.shareOf(key)
	if span < 1 {
		return nil
	}
	env := []string{handoffEnv + "=" + handoff{ok: true, base: base, span: span}.String()}
	if !p.t.fancy {
		env = append(env, "BOBRES_PLAIN=1")
	}
	return env
}
