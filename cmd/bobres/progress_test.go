package main

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// tenSteps is a faked 10-step run (the weights add up to 100).
var tenSteps = []stepDef{
	{"s1", "Checking this system", 5, liveStep, "Prepare"},
	{"s2", "Installing Docker", 5, liveStep, "Prepare"},
	{"s3", "Downloading the CLI", 10, liveStep, "Prepare"},
	{"s4", "Verifying", 10, liveStep, "Prepare"},
	{"s5", "Asking for your settings", 5, askStep, "Setup"},
	{"s6", "Pulling images", 30, liveStep, "Install"},
	{"s7", "Starting the stack", 10, liveStep, "Install"},
	{"s8", "Waiting for health", 15, liveStep, "Install"},
	{"s9", "Hardening", 5, liveStep, "Secure"},
	{"s10", "Summary", 5, liveStep, "Done"},
}

var (
	plainUI = termUI{width: 80}
	fancyUI = termUI{tty: true, fancy: true, unicode: true, color: color256, width: 100}
)

// clocked is a progress on a buffer with a clock that moves only when asked.
func clocked(t termUI, defs []stepDef, h handoff) (*progress, *bytes.Buffer, func(time.Duration)) {
	var buf bytes.Buffer
	p := newProgress(&buf, &buf, t, defs, h)
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	p.now = func() time.Time { return now }
	p.started = now
	return p, &buf, func(d time.Duration) { now = now.Add(d) }
}

// runTenSteps plays the faked run, with measured parts in three steps.
func runTenSteps(p *progress, advance func(time.Duration)) {
	for _, d := range tenSteps {
		p.Begin(d.key)
		switch d.key {
		case "s3":
			p.Fraction(1, 4, "1/4 files")
			p.Fraction(4, 4, "4/4 files")
		case "s6":
			p.Fraction(1, 6, "1/6 images")
			p.Fraction(3, 6, "3/6 images")
			p.Fraction(2, 6, "2/6 images") // a count that fell: ignored
			p.Fraction(6, 6, "6/6 images")
		case "s8":
			p.Fraction(2, 5, "2/5 healthy")
		}
		advance(7 * time.Second)
	}
	p.Complete()
}

func TestProgressModelWeights(t *testing.T) {
	m := newProgressModel(tenSteps, handoff{})
	if m.total != 100 {
		t.Fatalf("the weights add up to %d", m.total)
	}
	if got := m.percent(); got != 0 {
		t.Fatalf("start: %d", got)
	}
	m.begin(0)
	m.finish() // 5
	m.begin(1)
	m.finish() // 10
	if got := m.percent(); got != 10 {
		t.Fatalf("two steps done: %d", got)
	}
	m.begin(2)
	m.setFraction(0.5) // 10 + 10*0.5
	if got := m.percent(); got != 15 {
		t.Fatalf("half of the third: %d", got)
	}
	m.state[3] = stSkipped // a skipped step counts as done
	if got := m.percent(); got != 25 {
		t.Fatalf("skipped step: %d", got)
	}
}

func TestProgressNeverGoesDownAndIsHundredOnlyAtTheEnd(t *testing.T) {
	m := newProgressModel(tenSteps, handoff{})
	last := -1
	check := func() {
		t.Helper()
		if p := m.percent(); p < last {
			t.Fatalf("the percent went down: %d after %d", p, last)
		} else {
			last = p
		}
	}
	for i := range tenSteps {
		m.begin(i)
		for _, f := range []float64{0.2, 0.9, 0.4, 1} { // 0.4 after 0.9 must not lower it
			m.setFraction(f)
			check()
		}
		m.finish()
		check()
	}
	if last != 99 {
		t.Fatalf("every step done but the run not finished: %d, want 99", last)
	}
	m.finished = true
	if got := m.percent(); got != 100 {
		t.Fatalf("finished: %d", got)
	}
}

func TestProgressFailedStepKeepsThePercent(t *testing.T) {
	p, buf, adv := clocked(plainUI, tenSteps, handoff{})
	for _, k := range []string{"s1", "s2", "s3"} {
		p.Begin(k)
		adv(time.Second)
	}
	p.Begin("s4")
	p.Fraction(1, 2, "half")
	before := p.Percent()
	p.Fail("exit 1")
	if after := p.Percent(); after != before {
		t.Fatalf("percent %d -> %d after the failure", before, after)
	}
	p.Complete() // a failed run is never complete
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	last := lines[len(lines)-1]
	if want := fmt.Sprintf("[%3d%%] Step 4/10: FAIL Verifying (exit 1)", before); last != want {
		t.Fatalf("last line %q, want %q", last, want)
	}
	if strings.Contains(buf.String(), "[100%]") || strings.Contains(buf.String(), "Done in") {
		t.Fatalf("a failed run said it was done:\n%s", buf)
	}
}

func TestHandoffParseAndShare(t *testing.T) {
	for in, want := range map[string]handoff{
		"30:70:5:13": {true, 30, 70, 5, 13},
		"35:50:0:0":  {true, 35, 50, 0, 0},
	} {
		if got := parseHandoff(in); got != want {
			t.Errorf("parseHandoff(%q) = %+v", in, got)
		}
		if got := parseHandoff(in).String(); got != in {
			t.Errorf("String() = %q", got)
		}
	}
	for _, bad := range []string{"", "x", "30:70:5", "30:80:5:13", "100:1:0:0", "-1:10:0:0", "30:70:13:13", "30:0:0:0", "a:b:c:d"} {
		if got := parseHandoff(bad); got.ok {
			t.Errorf("parseHandoff(%q) accepted: %+v", bad, got)
		}
	}
	// A run inside a script's bar: 30..100, numbered after the script's steps, ending at 100.
	p, buf, adv := clocked(plainUI, installSteps, handoff{true, 30, 70, 5, 13})
	if got := p.Percent(); got != 30 {
		t.Fatalf("starts at %d, want 30", got)
	}
	p.Begin(stepWizard)
	adv(time.Second)
	p.Begin(stepChecks)
	if !strings.Contains(buf.String(), "[ 30%] Step 6/13: Asking for your settings") || !strings.Contains(buf.String(), "Step 7/13: Checking this server") {
		t.Fatalf("numbering:\n%s", buf)
	}
	for _, d := range installSteps {
		p.Begin(d.key)
	}
	p.Complete()
	if got := p.Percent(); got != 100 {
		t.Fatalf("ends at %d", got)
	}
	// A run that is a part of another (the install of an update): its end is not 100.
	p2, buf2, _ := clocked(plainUI, installSteps, handoff{true, 35, 50, 0, 0})
	for _, d := range installSteps {
		p2.Begin(d.key)
	}
	p2.Complete()
	if got := p2.Percent(); got != 85 || !strings.Contains(buf2.String(), "[ 85%] Finished in") || strings.Contains(buf2.String(), "Step ") {
		t.Fatalf("a part of a run: %d\n%s", got, buf2)
	}
	if base, span := newProgressModel(updateSteps, handoff{}).shareOf(stepUpInstall); base != 35 || span != 50 {
		t.Fatalf("the update's install covers %d..+%d, want 35..+50", base, span)
	}
}

var plainLineRe = regexp.MustCompile(`^(\[ *\d+%\] .+|== .+ ==)$`)

func TestPlainProgressRun(t *testing.T) {
	p, buf, adv := clocked(plainUI, tenSteps, handoff{})
	runTenSteps(p, adv)
	p.Close()
	out := buf.String()
	if strings.ContainsAny(out, "\x1b\r") {
		t.Fatalf("escape or carriage-return bytes in plain mode: %q", out)
	}
	for _, r := range out {
		if r > 126 {
			t.Fatalf("not ASCII: %q", out)
		}
	}
	prev := -1
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if !plainLineRe.MatchString(l) {
			t.Fatalf("line %q is not [ nn%%] text", l)
		}
		if m := regexp.MustCompile(`^\[ *(\d+)%\]`).FindStringSubmatch(l); m != nil {
			n, _ := strconv.Atoi(m[1])
			if n < prev {
				t.Fatalf("the percent went down at %q (after %d)", l, prev)
			}
			prev = n
		}
	}
	if prev != 100 {
		t.Fatalf("ends at %d", prev)
	}
	for _, want := range []string{
		"== Prepare ==", "== Install ==", "[  0%] Step 1/10: Checking this system", "Step 1/10: OK Checking this system (7s)",
		"Step 5/10: OK Asking for your settings\n", // a step that asks has no time
		"Step 6/10: Pulling images (3/6 images)", "[100%] Done in ",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "2/6 images") {
		t.Errorf("a lower count was shown:\n%s", out)
	}
	if strings.Count(out, "[100%]") != 1 {
		t.Errorf("100%% must appear once, at the end:\n%s", out)
	}
	// the time of the step that asks is not counted
	if !strings.Contains(out, "Done in 1m03s") { // 9 steps of 7s; the asking step's 7s left out
		t.Errorf("elapsed time:\n%s", out)
	}
}

func TestSkipAndNoteLines(t *testing.T) {
	p, buf, _ := clocked(plainUI, tenSteps, handoff{})
	p.Skip("s2", "already installed")
	p.Begin("s9")
	p.Note(markOK, "Firewall (ufw)", "on: SSH 22")
	p.Note(markWarn, "Swap file", "skipped: low disk")
	p.Complete()
	out := buf.String()
	for _, want := range []string{
		"Step 2/10: SKIP Installing Docker (already installed)",
		"Step 9/10: OK Firewall (ufw): on: SSH 22", "Step 9/10: WARN Swap file: skipped: low disk", "Done in 0s, 1 warning",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	p, buf, _ = clocked(plainUI, tenSteps, handoff{})
	p.quiet = true
	p.Skip("s2", "x")
	if buf.Len() != 0 {
		t.Errorf("a quiet run printed a skip line: %q", buf)
	}
}

// visible strips the escape sequences of a fancy run, leaving what a terminal
// would end with on each line (a redraw starts at a carriage return).
var escRe = regexp.MustCompile("\x1b\\[[0-9;?]*[A-Za-z]")

func lastCursorCommand(s string) string {
	hide, show := strings.LastIndex(s, "\x1b[?25l"), strings.LastIndex(s, "\x1b[?25h")
	switch {
	case hide < 0 && show < 0:
		return ""
	case hide > show:
		return "hide"
	}
	return "show"
}

func TestFancyProgressRun(t *testing.T) {
	p, buf, adv := clocked(fancyUI, tenSteps, handoff{})
	runTenSteps(p, adv)
	p.Close()
	out := buf.String()
	for _, want := range []string{"\r\x1b[K", "█", "░", "✔", "Pulling images", "100%"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q", want)
		}
	}
	// The cursor is hidden while a live step runs and shown at the end.
	if !strings.Contains(out, "\x1b[?25l") || lastCursorCommand(out) != "show" {
		t.Errorf("cursor: last command %q", lastCursorCommand(out))
	}
	for _, seg := range strings.FieldsFunc(escRe.ReplaceAllString(out, ""), func(r rune) bool { return r == '\n' || r == '\r' }) {
		if w := width(seg); w > fancyUI.width-1 {
			t.Fatalf("a line of %d columns in a %d-column terminal: %q", w, fancyUI.width, seg)
		}
	}
	// the last line is the bar at 100% with the time
	lines := strings.Split(strings.TrimRight(escRe.ReplaceAllString(out, ""), "\n"), "\n")
	if last := lines[len(lines)-1]; !strings.Contains(last, "100%") || !strings.Contains(last, "in 1m03s") {
		t.Errorf("last line %q", last)
	}
}

func TestFancyBarNeverWrapsOnNarrowTerminals(t *testing.T) {
	for _, w := range []int{60, 70, 80, 120} {
		ui := fancyUI
		ui.width = w
		defs := []stepDef{{"x", strings.Repeat("A very long step name ", 6), 100, liveStep, ""}}
		p, buf, _ := clocked(ui, defs, handoff{})
		p.Begin("x")
		p.Fraction(1, 3, "a long detail of the measured part: 1/3 images, 40 MB of 120 MB")
		p.Tick()
		p.Close()
		for _, seg := range strings.FieldsFunc(escRe.ReplaceAllString(buf.String(), ""), func(r rune) bool { return r == '\n' || r == '\r' }) {
			if got := width(seg); got > w-1 {
				t.Errorf("width %d: a line of %d columns: %q", w, got, seg)
			}
		}
	}
}

// Cancelling in the middle of a live step (Ctrl-C, an error): Close erases the
// line, and the last thing written is the cursor shown again.
func TestFancyCloseRestoresTheCursorAndTheLine(t *testing.T) {
	p, buf, _ := clocked(fancyUI, tenSteps, handoff{})
	p.Begin("s1")
	p.Tick()
	p.Close()
	out := buf.String()
	if !strings.Contains(out, "\x1b[?25l") || lastCursorCommand(out) != "show" {
		t.Fatalf("cursor not restored: %q", out)
	}
	if !strings.HasSuffix(out, "\x1b[?25h") || !strings.Contains(out[strings.LastIndex(out, "\r\x1b[K"):], "\x1b[?25h") {
		t.Fatalf("the half-drawn line was not taken out before the cursor came back: %q", out)
	}
	n := buf.Len()
	p.Tick()
	p.Close() // twice is fine, and nothing is drawn after it
	if buf.Len() != n {
		t.Fatalf("drawing after Close: %q", buf.String()[n:])
	}
}

func TestFailLeavesAFancyTerminalClean(t *testing.T) {
	p, buf, _ := clocked(fancyUI, tenSteps, handoff{})
	p.Begin("s1")
	p.Begin("s2")
	p.Fail("exit 1")
	p.Close()
	out := buf.String()
	if lastCursorCommand(out) != "show" || !strings.Contains(escRe.ReplaceAllString(out, ""), "✗ Installing Docker") ||
		!strings.Contains(out, "failed at 5% (exit 1)") {
		t.Fatalf("%q", out)
	}
}

// Other output goes through the progress writer: each line lands above the live
// line, which is drawn again below it; a prompt (no newline) is never drawn over.
func TestFancyWriterKeepsOtherOutputIntact(t *testing.T) {
	p, buf, _ := clocked(fancyUI, tenSteps, handoff{})
	w := p.Out()
	p.Begin("s1")
	buf.Reset()
	w.Write([]byte("a line\n")) //nolint:errcheck // a buffer
	if got := buf.String(); !strings.HasPrefix(got, "\r\x1b[Ka line\n") || !strings.Contains(got, "\r\x1b[K[") {
		t.Fatalf("a line: %q", got)
	}
	buf.Reset()
	w.Write([]byte("Bot token: ")) //nolint:errcheck // a buffer
	if got := buf.String(); got != "\r\x1b[KBot token: " {
		t.Fatalf("a prompt must stay as it is, with no bar after it: %q", got)
	}
	buf.Reset()
	p.Tick()
	if buf.Len() != 0 {
		t.Fatalf("the spinner drew over a prompt: %q", buf)
	}
	w.Write([]byte("typed\n")) //nolint:errcheck // a buffer
	if got := buf.String(); !strings.HasPrefix(got, "typed\n") {
		t.Fatalf("the answer's line must not be erased: %q", got)
	}
}

// A step that asks questions is never drawn live: no cursor change, no redraw
// codes, and what the prompts print reaches the terminal untouched.
func TestAskStepIsNeverLive(t *testing.T) {
	p, buf, _ := clocked(fancyUI, tenSteps, handoff{})
	p.Begin("s5")
	p.Out().Write([]byte("Domain: ")) //nolint:errcheck // a buffer
	p.Tick()
	out := buf.String()
	if strings.Contains(out, "\x1b[?25") || strings.Contains(out, "\r") {
		t.Fatalf("an asking step was drawn live: %q", out)
	}
	if !strings.HasSuffix(out, "Domain: ") {
		t.Fatalf("the prompt: %q", out)
	}
}

func TestPlainWriterPassesThroughAndLogs(t *testing.T) {
	p, buf, _ := clocked(plainUI, tenSteps, handoff{})
	var log bytes.Buffer
	p.log = &log
	p.Begin("s1")
	p.Out().Write([]byte("hello\n")) //nolint:errcheck // a buffer
	p.Finish()
	if !strings.Contains(buf.String(), "hello\n") || !strings.Contains(log.String(), "hello\n") {
		t.Fatalf("screen %q log %q", buf, &log)
	}
	if strings.ContainsAny(log.String(), "\x1b\r") || !strings.Contains(log.String(), "2026-10-14 12:00:00 [  0%] Step 1/10: Checking this system") {
		t.Fatalf("log %q", &log)
	}
}

func TestWatchSignalsRestoresThenReraises(t *testing.T) {
	c := make(chan os.Signal, 1)
	var calls []string
	done := make(chan struct{})
	stop := watchSignals(c, func() { calls = append(calls, "restore") }, func(s os.Signal) {
		calls = append(calls, "reraise "+s.String())
		close(done)
	})
	defer stop()
	c <- syscall.SIGINT
	<-done
	if strings.Join(calls, ",") != "restore,reraise interrupt" {
		t.Fatalf("%v", calls)
	}
	// stopped: a signal is not acted on
	c2 := make(chan os.Signal, 1)
	stop2 := watchSignals(c2, func() { t.Error("restore after stop") }, func(os.Signal) { t.Error("reraise after stop") })
	stop2()
	stop2() // again is fine
	c2 <- syscall.SIGTERM
	time.Sleep(20 * time.Millisecond)
}

func TestComposeEventsAndLineWriter(t *testing.T) {
	ev := newComposeEvents()
	for _, l := range []string{
		" Image caddy:2 Pulling ", " Image ghcr.io/x/core:1 Pulling ", " Image caddy:2 Pulled ",
		" ✔ Image ghcr.io/x/core:1 Pulled", " Container bobres-redis-1  Creating", " Container bobres-redis-1  Started",
		" ⠿ Container bobres-core-1  Running", "something else entirely",
	} {
		ev.feed(l)
	}
	if len(ev.pulling) != 2 || len(ev.pulled) != 2 || len(ev.containers) != 2 || len(ev.started) != 2 {
		t.Fatalf("%+v", ev)
	}
	if ev.feed("some random text") {
		t.Fatal("an unknown line counted as an event")
	}
	var got []string
	lw := &lineWriter{onLine: func(s string) { got = append(got, s) }}
	lw.Write([]byte("one\ntw"))    //nolint:errcheck // a buffer
	lw.Write([]byte("o\r\nthree")) //nolint:errcheck // a buffer
	lw.mu.Lock()
	lw.emit()
	lw.mu.Unlock()
	if strings.Join(got, "|") != "one|two|three" || lw.buf.String() != "one\ntwo\r\nthree" {
		t.Fatalf("%v %q", got, lw.buf.String())
	}
}
