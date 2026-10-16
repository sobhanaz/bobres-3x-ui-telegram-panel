package main

import (
	"fmt"
	"io"
	"math"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// A run (install, update) is a list of steps, each with a weight. The percent
// shown is honest: the weights of the steps that are over, plus the part of the
// running step that was measured (images pulled, services healthy). It never
// goes down, and it reaches 100 only when the run really finished.

// stepKind says how a step is shown.
type stepKind int

const (
	// liveStep is drawn as a live line while it runs.
	liveStep stepKind = iota
	// askStep asks questions: never drawn live (a prompt must not be redrawn
	// over), and its time is not counted.
	askStep
	// childStep is run by another process that shows its own progress (the
	// install bobres update starts): never drawn live, its time counted.
	childStep
)

// stepDef is one step of a run.
type stepDef struct {
	key    string
	name   string
	weight int
	kind   stepKind
	// section is the header the step's line comes under ("" for none): it is
	// written when the section changes.
	section string
}

type stepState int

const (
	stPending stepState = iota
	stRunning
	stDone
	stSkipped
	stFailed
)

// handoffEnv carries the share of a bigger run to the bobres a script starts:
// "BASE:SPAN:DONE:TOTAL", the percent already done, the percent left for this
// run, the steps already done and the steps in all (0: this run's steps are
// not numbered). install.sh sets it, so its steps and the CLI's are one bar.
const handoffEnv = "BOBRES_PROGRESS"

// handoff is a parsed handoffEnv. The zero value (not ok) means a run of its
// own, 0 to 100.
type handoff struct {
	ok         bool
	base, span int
	done, all  int
}

func (h handoff) String() string {
	return fmt.Sprintf("%d:%d:%d:%d", h.base, h.span, h.done, h.all)
}

// parseHandoff reads handoffEnv's value; a value that does not make sense is
// not a hand-off.
func parseHandoff(s string) handoff {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) != 4 {
		return handoff{}
	}
	var n [4]int
	for i, p := range parts {
		v, err := strconv.Atoi(p)
		if err != nil || v < 0 || v > 100 {
			return handoff{}
		}
		n[i] = v
	}
	h := handoff{ok: true, base: n[0], span: n[1], done: n[2], all: n[3]}
	if h.base > 99 || h.span < 1 || h.base+h.span > 100 || (h.all != 0 && h.all <= h.done) {
		return handoff{}
	}
	return h
}

// progressModel is the arithmetic of a run, with no output.
type progressModel struct {
	defs     []stepDef
	state    []stepState
	h        handoff
	numbered bool
	cur      int // the running step, -1 for none
	last     int // the step begun last, -1 for none
	frac     float64
	finished bool
	high     int
	total    int
}

func newProgressModel(defs []stepDef, h handoff) *progressModel {
	m := &progressModel{defs: defs, state: make([]stepState, len(defs)), cur: -1, last: -1, h: h, numbered: true}
	if !h.ok {
		m.h = handoff{ok: true, base: 0, span: 100, all: len(defs)}
	} else {
		m.numbered = h.all > 0
	}
	for _, d := range defs {
		m.total += d.weight
	}
	return m
}

func (m *progressModel) index(key string) int {
	for i, d := range m.defs {
		if d.key == key {
			return i
		}
	}
	return -1
}

// percent is how far the run is, 0..100. It never goes down, and it stays one
// under the end of its share until the run is finished.
func (m *progressModel) percent() int {
	done := 0.0
	for i, s := range m.state {
		switch s {
		case stDone, stSkipped:
			done += float64(m.defs[i].weight)
		case stRunning:
			done += float64(m.defs[i].weight) * m.frac
		}
	}
	end := m.h.base + m.h.span
	p := m.h.base
	if m.total > 0 {
		p += int(math.Floor(float64(m.h.span) * done / float64(m.total)))
	}
	switch {
	case m.finished:
		p = end
	case p >= end:
		p = end - 1
	}
	if p < m.high {
		p = m.high
	}
	m.high = p
	return p
}

func (m *progressModel) begin(i int) {
	m.state[i], m.cur, m.last, m.frac = stRunning, i, i, 0
}

func (m *progressModel) finish() {
	if m.cur >= 0 {
		m.state[m.cur] = stDone
	}
	m.cur, m.frac = -1, 0
}

func (m *progressModel) fail() {
	if m.cur >= 0 {
		m.state[m.cur] = stFailed
	}
	m.cur = -1
}

// setFraction sets how much of the running step is done; it only grows.
func (m *progressModel) setFraction(f float64) {
	f = math.Max(0, math.Min(1, f))
	if f > m.frac {
		m.frac = f
	}
}

// shareOf is the part of the bar the step named key covers: the percent when it
// begins and how many points it spans.
func (m *progressModel) shareOf(key string) (base, span int) {
	i := m.index(key)
	if i < 0 || m.total == 0 {
		return m.h.base, 0
	}
	before := 0
	for _, d := range m.defs[:i] {
		before += d.weight
	}
	at := func(w int) int { return m.h.base + m.h.span*w/m.total }
	return at(before), at(before+m.defs[i].weight) - at(before)
}

// tag is how a step is named in a line: "Step 4/10", or its name when the
// steps are not numbered.
func (m *progressModel) tag(i int) string {
	if !m.numbered {
		return m.defs[i].name
	}
	return fmt.Sprintf("Step %d/%d", m.h.done+i+1, m.h.all)
}

// title is a step's tag and name: "Step 4/10: Pulling images".
func (m *progressModel) title(i int) string {
	if !m.numbered {
		return m.defs[i].name
	}
	return m.tag(i) + ": " + m.defs[i].name
}

const (
	escHide  = "\033[?25l"
	escShow  = "\033[?25h"
	escClear = "\r\033[K"
)

var (
	spinUnicode = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	spinASCII   = []string{"|", "/", "-", "\\"}
)

// progress shows a run's progress. On a terminal it is one live line at the
// bottom (a bar, the percent, the step, a spinner and the elapsed time),
// redrawn in place while a step that does not ask questions runs, with a
// permanent line for each step that ends. Anywhere else it is one plain line
// per change of step. A nil *progress does nothing.
type progress struct {
	mu    sync.Mutex
	m     *progressModel
	t     termUI
	w, ew io.Writer // the real output and error output
	now   func() time.Time
	// quiet leaves out the lines of skipped steps.
	quiet bool
	// auto runs the spinner's ticker and the signal guard (a real terminal).
	auto bool

	started, stepStart time.Time
	paused             time.Duration // time spent in interactive steps
	detail             string        // the running step's measured part, in words
	lastPlain          string        // the last plain line (no repeats)
	live               bool          // a step that is drawn live is running
	drawn              bool          // the live line is on the cursor's line
	dirty              bool          // the cursor is inside a line that is not ours
	hidden             bool          // the cursor is hidden
	frame              int
	stopTick           chan struct{}
	stopGuard          func()
	closed, aborted    bool
	failed             bool
	warns              int       // warnings reported by Note
	section            string    // the header printed last
	log                io.Writer // every line also goes here (nil: nowhere)
}

// newProgress starts a run of defs on out. h is the hand-off of the script that
// started it (the zero value for a run of its own).
func newProgress(out, errOut io.Writer, t termUI, defs []stepDef, h handoff) *progress {
	_, real := rawWriter(out).(*os.File)
	p := &progress{
		m: newProgressModel(defs, h), t: t, w: rawWriter(out), ew: rawWriter(errOut),
		now: time.Now, auto: t.fancy && real,
	}
	p.started = p.now()
	return p
}

// Out and Err are the writers the run's other output goes through, so that it
// never lands in the middle of the live line.
func (p *progress) Out() io.Writer { return &progWriter{p: p, w: p.w} }
func (p *progress) Err() io.Writer { return &progWriter{p: p, w: p.ew} }

// progWriter writes to w, taking the live line out of the way and putting it
// back below the text.
type progWriter struct {
	p *progress
	w io.Writer
}

func (pw *progWriter) Write(b []byte) (int, error) {
	p := pw.p
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.log != nil {
		_, _ = p.log.Write(b)
	}
	if !p.live || !p.t.fancy || p.closed {
		return pw.w.Write(b)
	}
	if p.drawn {
		_, _ = io.WriteString(p.w, escClear)
		p.drawn = false
	}
	n, err := pw.w.Write(b)
	// Text that does not end its line (a prompt) leaves the cursor in it:
	// nothing is drawn until a line is ended.
	p.dirty = len(b) > 0 && b[len(b)-1] != '\n'
	if !p.dirty {
		p.drawLocked()
	}
	return n, err
}

// failedStep is the key of the step that is running, or was begun last ("" for
// none): where a run that fails stopped.
func (p *progress) failedStep() string {
	if p == nil {
		return ""
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	i := p.m.cur
	if i < 0 {
		i = p.m.last
	}
	if i < 0 {
		return ""
	}
	return p.m.defs[i].key
}

// setKind changes how a step is shown, before it begins.
func (p *progress) setKind(key string, k stepKind) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if i := p.m.index(key); i >= 0 {
		p.m.defs[i].kind = k
	}
}

// Percent is the run's percent now.
func (p *progress) Percent() int {
	if p == nil {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.m.percent()
}

// Begin starts the step named key (the one running is ended first). A step
// of a new section gets the section's header first.
func (p *progress) Begin(key string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	i := p.m.index(key)
	if i < 0 || p.closed || p.m.cur == i {
		return
	}
	p.finishLocked()
	p.m.begin(i)
	p.detail, p.stepStart = "", p.now()
	if sec := p.m.defs[i].section; sec != "" && sec != p.section {
		p.section = sec
		p.headerLocked(sec)
	}
	switch {
	case !p.t.fancy:
		p.plainLocked(p.m.title(i))
	case p.m.defs[i].kind != liveStep:
		p.logLocked(p.m.title(i))
		_, _ = io.WriteString(p.w, p.barLine(false)+"\n")
	default:
		p.logLocked(p.m.title(i))
		p.goLiveLocked()
	}
}

// headerLocked writes a section header: a title and a rule on a terminal,
// "== Title ==" anywhere else.
func (p *progress) headerLocked(title string) {
	p.logLocked("== " + title + " ==")
	if !p.t.fancy {
		_, _ = io.WriteString(p.w, "== "+title+" ==\n")
		return
	}
	rule := "-"
	if p.t.unicode {
		rule = "─"
	}
	n := min(p.t.width-1, 60) - width(title) - 1
	p.permLocked("\n" + p.t.paint(roleBold, p.t.paint(roleTeal, title)) + " " + p.t.paint(roleNavy, strings.Repeat(rule, max(n, 0))))
}

// Fraction says how much of the running step is measured to be done (done of
// total images, services...), with the words to show for it.
func (p *progress) Fraction(done, total int, detail string) {
	if p == nil || total <= 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.m.cur < 0 || p.closed {
		return
	}
	// A measure that is below the best so far (a count that grew: more images
	// seen) changes nothing, not even the words.
	if f := float64(done) / float64(total); f < p.m.frac {
		return
	}
	p.m.setFraction(float64(done) / float64(total))
	p.detail = detail
	switch {
	case !p.t.fancy:
		p.plainLocked(p.m.title(p.m.cur) + " (" + detail + ")")
	case p.live && !p.dirty:
		p.drawLocked()
	}
}

// Finish ends the running step.
func (p *progress) Finish() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.finishLocked()
}

// finishLocked ends the running step, if any, with its line: OK, the step's
// name and the time it took (a step that asks questions has no time).
func (p *progress) finishLocked() {
	i := p.m.cur
	if i < 0 || p.closed {
		return
	}
	p.stopLiveLocked()
	d := p.now().Sub(p.stepStart)
	p.m.finish()
	took := fmtDur(d)
	if p.m.defs[i].kind == askStep {
		p.paused += d
		took = ""
	}
	p.resultLocked(i, markOK, p.m.defs[i].name, took, true)
}

// Skip marks a step that does not apply as done, saying why.
func (p *progress) Skip(key, why string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	i := p.m.index(key)
	if i < 0 || p.m.state[i] != stPending || p.closed {
		return
	}
	p.finishLocked() // what ran before it is over, and its line comes first
	p.m.state[i] = stSkipped
	if p.quiet {
		return
	}
	p.resultLocked(i, markSkip, p.m.defs[i].name, why, true)
}

// Note reports one result inside the running step (the hardening steps): a
// mark, a label and what happened. A warning is counted for the last line.
func (p *progress) Note(kind markKind, label, text string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	if kind == markWarn {
		p.warns++
	}
	i := p.m.cur
	if i < 0 {
		i = p.m.last
	}
	p.resultLocked(i, kind, label, text, false)
}

// markKind is the outcome a result line shows.
type markKind int

const (
	markOK markKind = iota
	markWarn
	markFail
	markSkip
)

// labelCol is the width of the label column of the result lines.
const labelCol = 38

// resultLocked writes a result line of step i. On a terminal it is
//
//	<mark> <label, padded>  <detail>
//
// and in plain mode (and in the log) "[ 42%] Step 4/10: OK label (detail)",
// with ": detail" instead of the parentheses for a note. paren says which.
func (p *progress) resultLocked(i int, kind markKind, label, detail string, paren bool) {
	plain := p.t
	plain.fancy, plain.unicode, plain.color = false, false, colorNone
	text := plain.mark(kind) + " " + label
	switch {
	case detail == "":
	case paren:
		text += " (" + detail + ")"
	default:
		text += ": " + detail
	}
	if i >= 0 && p.m.numbered {
		text = p.m.tag(i) + ": " + text
	}
	if !p.t.fancy {
		p.plainLocked(text)
		return
	}
	p.logLocked(text)
	col := min(labelCol, max(p.t.width-24, 12))
	room := p.t.width - 1 - (2 + 1 + 1 + col + 2)
	d := clip(detail, room, p.t.unicode)
	if paren || kind == markSkip {
		d = p.t.paint(roleDim, d)
	}
	line := "  " + p.t.mark(kind) + " " + fmt.Sprintf("%-*s", col, clip(label, col, p.t.unicode))
	if d != "" {
		line += "  " + d
	}
	p.permLocked(line)
}

// permLocked writes a line that stays, under the live line's place: the live
// line is taken out of the way and drawn again below it.
func (p *progress) permLocked(text string) {
	if p.drawn {
		_, _ = io.WriteString(p.w, escClear)
		p.drawn = false
	}
	_, _ = io.WriteString(p.w, text+"\n")
	if p.live && !p.dirty {
		p.drawLocked()
	}
}

// Fail ends the run at the step that was running: the percent stays where it
// was, and a line says where it stopped.
func (p *progress) Fail(note string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.aborted || p.m.finished || p.failed {
		return
	}
	p.failed = true
	i := p.m.cur
	if i < 0 {
		i = p.m.last
	}
	p.stopLiveLocked()
	p.m.fail()
	if i < 0 {
		return
	}
	pct := p.m.percent()
	if p.t.fancy {
		p.resultLocked(i, markFail, p.m.defs[i].name, fmt.Sprintf("failed at %d%%%s", pct, noteSuffix(note)), false)
		return
	}
	p.resultLocked(i, markFail, p.m.defs[i].name, strings.TrimSpace(note), true)
}

func noteSuffix(note string) string {
	if note = strings.TrimSpace(note); note != "" {
		return " (" + note + ")"
	}
	return ""
}

// Complete ends the run: whatever is still pending is skipped, and the line
// that says 100% (the end of this run's share) is written.
func (p *progress) Complete() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.aborted || p.m.finished || p.failed {
		return
	}
	p.finishLocked()
	for i, s := range p.m.state {
		if s == stPending {
			p.m.state[i] = stSkipped
		}
	}
	p.m.finished = true
	pct := p.m.percent()
	word := "Done"
	if p.m.h.base+p.m.h.span < 100 {
		word = "Finished" // a part of a bigger run: the rest goes on
	}
	took := fmtDur(p.now().Sub(p.started) - p.paused)
	extra := ""
	if p.warns > 0 {
		extra = fmt.Sprintf(", %d warning%s", p.warns, map[bool]string{true: "s", false: ""}[p.warns > 1])
	}
	if p.t.fancy {
		p.logLocked(word + " in " + took + extra)
		_, _ = io.WriteString(p.w, p.barLine(false)+"  "+p.t.paint(roleTeal, "in "+took+extra)+"\n")
		return
	}
	p.plainLocked(word+" in "+took+extra, pct)
}

// Abort ends the run with no line: nothing was done, or the person said no.
func (p *progress) Abort() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stopLiveLocked()
	p.aborted = true
}

// Close puts the terminal back as it was: the half-drawn line is taken out and
// the cursor is shown. It is safe to call again, from a signal handler too.
func (p *progress) Close() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	p.stopLiveLocked()
	if p.t.fancy {
		// Also what a killed child left: its half-drawn line, its hidden cursor.
		_, _ = io.WriteString(p.w, escShow)
	}
	p.closed = true
}

// plainLocked writes one plain line: "[ 42%] text". pct, when given, is the
// percent to show (the model's own when not). It also goes to the log.
func (p *progress) plainLocked(text string, pct ...int) {
	v := p.m.percent()
	if len(pct) > 0 {
		v = pct[0]
	}
	line := fmt.Sprintf("[%3d%%] %s", v, text)
	if line == p.lastPlain {
		return
	}
	p.lastPlain = line
	p.logRaw(line)
	_, _ = io.WriteString(p.w, line+"\n")
}

// logLocked writes the plain form of an event that a terminal shows in
// another way to the log.
func (p *progress) logLocked(text string) {
	p.logRaw(fmt.Sprintf("[%3d%%] %s", p.m.percent(), text))
}

// logRaw writes a line, with the time, to the log.
func (p *progress) logRaw(line string) {
	if p.log != nil {
		_, _ = io.WriteString(p.log, p.now().Format("2006-01-02 15:04:05")+" "+line+"\n")
	}
}

// goLiveLocked starts the live line: the cursor is hidden, the line drawn, the
// spinner turning, and a signal puts the terminal back before the process dies.
func (p *progress) goLiveLocked() {
	p.live = true
	if !p.hidden {
		_, _ = io.WriteString(p.w, escHide)
		p.hidden = true
	}
	p.drawLocked()
	if p.auto && p.stopTick == nil {
		stop := make(chan struct{})
		p.stopTick = stop
		go p.ticker(stop)
		c := make(chan os.Signal, 1)
		// Not SIGHUP: a dropped connection must not end an install (keepGoing).
		signal.Notify(c, os.Interrupt, syscall.SIGTERM)
		p.stopGuard = watchSignals(c, p.Close, func(sig os.Signal) {
			signal.Stop(c)
			signal.Reset(os.Interrupt, syscall.SIGTERM)
			if s, ok := sig.(syscall.Signal); ok {
				_ = syscall.Kill(os.Getpid(), s)
			}
		})
	}
}

// stopLiveLocked takes the live line away and shows the cursor again.
func (p *progress) stopLiveLocked() {
	if p.stopTick != nil {
		close(p.stopTick)
		p.stopTick = nil
	}
	if p.stopGuard != nil {
		p.stopGuard()
		p.stopGuard = nil
	}
	if !p.live && !p.hidden {
		return
	}
	p.live, p.dirty = false, false
	if p.t.fancy {
		if p.drawn {
			_, _ = io.WriteString(p.w, escClear)
			p.drawn = false
		}
		if p.hidden {
			_, _ = io.WriteString(p.w, escShow)
		}
	}
	p.hidden = false
}

// ticker turns the spinner and the elapsed time until stop is closed.
func (p *progress) ticker(stop chan struct{}) {
	t := time.NewTicker(100 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			p.Tick()
		}
	}
}

// Tick moves the spinner one frame and draws the live line again.
func (p *progress) Tick() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.live && !p.dirty && !p.closed {
		p.frame++
		p.drawLocked()
	}
}

func (p *progress) drawLocked() {
	if !p.t.fancy {
		return
	}
	_, _ = io.WriteString(p.w, escClear+p.barLine(true))
	p.drawn = true
}

// elapsed is the time of the run, without the time spent answering questions.
func (p *progress) elapsed() time.Duration { return p.now().Sub(p.started) - p.paused }

// barLine is "[bar] pct%  text", with the spinner and the elapsed time when
// spin is set. It is never wider than the terminal less one column, so it
// never wraps.
func (p *progress) barLine(spin bool) string {
	t := p.t
	pct := p.m.percent()
	bw := 20
	if t.width < 80 {
		bw = 10
	}
	i := p.m.cur
	if i < 0 {
		i = p.m.last
	}
	text := ""
	switch {
	case p.m.finished:
		text = "Done"
		if p.m.h.base+p.m.h.span < 100 {
			text = "Finished"
		}
	case i >= 0:
		text = p.m.title(i)
		if p.detail != "" {
			text += "  " + p.detail
		}
	}
	tail := ""
	if spin {
		frames := spinASCII
		if t.unicode {
			frames = spinUnicode
		}
		tail = frames[p.frame%len(frames)] + " " + fmtClock(p.elapsed())
	}
	room := t.width - 1 - (bw + 9) - width(tail) - 2
	text = clip(text, room, t.unicode)
	fill, empty := "#", "-"
	if t.unicode {
		fill, empty = "█", "░"
	}
	n := pct * bw / 100
	bar := t.paint(roleTeal, strings.Repeat(fill, n)) + t.paint(roleNavy, strings.Repeat(empty, bw-n))
	s := fmt.Sprintf("[%s] %s  %s", bar, t.paint(roleBold, fmt.Sprintf("%3d%%", pct)), text)
	if tail != "" {
		s += "  " + t.paint(roleBrown, tail)
	}
	return s
}

// clip shortens s to at most max columns, ending it with an ellipsis.
func clip(s string, max int, unicode bool) string {
	if max < 1 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	dots := "..."
	if unicode {
		dots = "…"
	}
	keep := max - width(dots)
	if keep < 0 {
		keep = 0
	}
	return string(r[:keep]) + dots
}

// fmtDur is a time taken: 38s, 1m05s, 1h02m.
func fmtDur(d time.Duration) string {
	s := int(d.Round(time.Second) / time.Second)
	switch {
	case s < 60:
		return fmt.Sprintf("%ds", s)
	case s < 3600:
		return fmt.Sprintf("%dm%02ds", s/60, s%60)
	}
	return fmt.Sprintf("%dh%02dm", s/3600, s%3600/60)
}

// fmtClock is the elapsed-time counter: 0:38, 12:05, 1:02:03.
func fmtClock(d time.Duration) string {
	s := int(d / time.Second)
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s%3600/60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// watchSignals calls restore and then reraise when a signal arrives on c,
// until the returned stop is called.
func watchSignals(c <-chan os.Signal, restore func(), reraise func(os.Signal)) (stop func()) {
	done := make(chan struct{})
	var once sync.Once
	go func() {
		select {
		case sig := <-c:
			select {
			case <-done: // stopped as the signal came: leave it alone
				return
			default:
			}
			restore()
			reraise(sig)
		case <-done:
		}
	}()
	return func() { once.Do(func() { close(done) }) }
}
