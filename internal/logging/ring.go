package logging

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

// RingSize is how many lines Recent keeps.
const RingSize = 500

// maxField caps a kept line's msg and attrs, in bytes.
const maxField = 1000

// recent is the process's buffer: every logger built here tees into it, and
// the System page reads it (core directly, the other services for core).
var recent = NewRing(RingSize)

// Recent returns this process's kept warnings and errors, oldest first.
func Recent() []Line { return recent.Lines() }

// Counts is how many warnings and errors this process has logged since it started (the ring keeps
// only the newest lines; these never wrap). The monitoring page turns them into rates.
func Counts() (warnings, errs uint64) { return recent.Counts() }

// Line is one kept log line, redacted like stdout.
type Line struct {
	Time  int64  `json:"time"`  // unix seconds
	Level string `json:"level"` // WARN | ERROR
	Msg   string `json:"msg"`
	// Attrs are the line's other fields as a JSON object ("{}" when none);
	// past maxField it is cut and ends with "…".
	Attrs string `json:"attrs"`
}

// Ring keeps the newest lines a JSON handler writes to it, in memory only.
type Ring struct {
	mu    sync.Mutex
	lines []Line
	next  int
	full  bool
	// warns and errs count every WARN and ERROR line written, kept or not.
	warns, errs atomic.Uint64
}

// Counts returns how many WARN and ERROR lines the ring has been written.
func (r *Ring) Counts() (warnings, errs uint64) { return r.warns.Load(), r.errs.Load() }

// NewRing keeps the newest n lines.
func NewRing(n int) *Ring { return &Ring{lines: make([]Line, max(n, 1))} }

// Lines returns a copy of the kept lines, oldest first.
func (r *Ring) Lines() []Line {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.full {
		return append([]Line{}, r.lines[:r.next]...)
	}
	return append(append(make([]Line, 0, len(r.lines)), r.lines[r.next:]...), r.lines[:r.next]...)
}

// Write takes one JSON line from slog.JSONHandler (it writes each record in a
// single call). A line it cannot read is dropped: logging never fails on it.
func (r *Ring) Write(p []byte) (int, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(p, &fields); err != nil {
		return len(p), nil
	}
	var l Line
	var at string
	_ = json.Unmarshal(fields[slog.TimeKey], &at)
	if t, err := time.Parse(time.RFC3339Nano, at); err == nil {
		l.Time = t.Unix()
	}
	_ = json.Unmarshal(fields[slog.LevelKey], &l.Level)
	switch l.Level {
	case "WARN":
		r.warns.Add(1)
	case "ERROR":
		r.errs.Add(1)
	}
	_ = json.Unmarshal(fields[slog.MessageKey], &l.Msg)
	l.Msg = cut(l.Msg)
	// The service is the same on every line of a process.
	for _, k := range []string{slog.TimeKey, slog.LevelKey, slog.MessageKey, "service"} {
		delete(fields, k)
	}
	attrs, _ := json.Marshal(fields)
	l.Attrs = cut(string(attrs))

	r.mu.Lock()
	r.lines[r.next] = l
	r.next = (r.next + 1) % len(r.lines)
	if r.next == 0 {
		r.full = true
	}
	r.mu.Unlock()
	return len(p), nil
}

// cut shortens s to maxField bytes on a rune boundary, marking the cut.
func cut(s string) string {
	if len(s) <= maxField {
		return s
	}
	n := maxField
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}

// tee sends every record to both handlers, each at its own level.
type tee struct{ a, b slog.Handler }

func (t tee) Enabled(ctx context.Context, l slog.Level) bool {
	return t.a.Enabled(ctx, l) || t.b.Enabled(ctx, l)
}

func (t tee) Handle(ctx context.Context, r slog.Record) error {
	var errA, errB error
	if t.a.Enabled(ctx, r.Level) {
		errA = t.a.Handle(ctx, r)
	}
	if t.b.Enabled(ctx, r.Level) {
		errB = t.b.Handle(ctx, r)
	}
	return errors.Join(errA, errB)
}

func (t tee) WithAttrs(as []slog.Attr) slog.Handler {
	return tee{t.a.WithAttrs(as), t.b.WithAttrs(as)}
}

func (t tee) WithGroup(name string) slog.Handler {
	return tee{t.a.WithGroup(name), t.b.WithGroup(name)}
}
