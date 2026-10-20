package logging

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRingKeepsRedactedWarningsAndErrors(t *testing.T) {
	ring := NewRing(10)
	l := newLogger(&bytes.Buffer{}, ring, "core", "info", "text")
	l.Info("not kept", "user_id", 1)
	l.Warn("slow panel", "bot_token", "999:SecretBotTok", "user_id", 42,
		"err", errors.New("dial postgres://bobres:hunter2@db:5432/x failed"))
	l.WithGroup("req").Error("failed", "Authorization", "Bearer abc", "path", "/x")

	lines := ring.Lines()
	if len(lines) != 2 {
		t.Fatalf("want the warning and the error, got %+v", lines)
	}
	w, e := lines[0], lines[1]
	if w.Level != "WARN" || w.Msg != "slow panel" || e.Level != "ERROR" || e.Msg != "failed" {
		t.Fatalf("bad lines: %+v", lines)
	}
	if d := time.Since(time.Unix(w.Time, 0)); d < 0 || d > time.Minute {
		t.Fatalf("bad time %d", w.Time)
	}
	all := w.Attrs + e.Attrs
	for _, leak := range []string{"SecretBotTok", "hunter2", "Bearer abc", `"service"`} {
		if strings.Contains(all, leak) {
			t.Fatalf("kept %q: %s", leak, all)
		}
	}
	for _, want := range []string{`"bot_token":"[REDACTED]"`, `"user_id":42`, "postgres://[REDACTED]@db:5432"} {
		if !strings.Contains(w.Attrs, want) {
			t.Fatalf("missing %s in %s", want, w.Attrs)
		}
	}
	if e.Attrs != `{"req":{"Authorization":"[REDACTED]","path":"/x"}}` {
		t.Fatalf("bad grouped attrs: %s", e.Attrs)
	}
}

func TestRingKeepsWarningsWhenStdoutIsQuieter(t *testing.T) {
	ring := NewRing(10)
	var out bytes.Buffer
	l := newLogger(&out, ring, "bot", "error", "json")
	l.Warn("kept only in the buffer")
	if out.Len() != 0 || len(ring.Lines()) != 1 || ring.Lines()[0].Attrs != "{}" {
		t.Fatalf("stdout %q, ring %+v", out.String(), ring.Lines())
	}
}

func TestRingWrapsOldestFirstAndCutsLongFields(t *testing.T) {
	ring := NewRing(3)
	l := newLogger(&bytes.Buffer{}, ring, "core", "info", "json")
	for i := range 5 {
		l.Warn(fmt.Sprintf("m%d", i))
	}
	got := ring.Lines()
	if len(got) != 3 || got[0].Msg != "m2" || got[2].Msg != "m4" {
		t.Fatalf("bad order: %+v", got)
	}
	l.Error(strings.Repeat("ی", 800), "detail", strings.Repeat("x", 2000))
	last := ring.Lines()[2]
	if len(last.Msg) > maxField+len("…") || !strings.HasSuffix(last.Msg, "…") || !strings.HasSuffix(last.Attrs, "…") {
		t.Fatalf("not cut: %d %d", len(last.Msg), len(last.Attrs))
	}
	if !strings.HasPrefix(last.Msg, "ی") || strings.ContainsRune(last.Msg, '�') {
		t.Fatalf("cut inside a rune")
	}
}

// The counters never wrap, unlike the lines the ring keeps, and count what stdout's level hides.
func TestRingCountsEveryWarningAndError(t *testing.T) {
	ring := NewRing(2)
	l := newLogger(&bytes.Buffer{}, ring, "core", "error", "json")
	for range 5 {
		l.Warn("w")
	}
	l.Error("e")
	l.Info("i")
	l.Debug("d")
	if w, e := ring.Counts(); w != 5 || e != 1 {
		t.Fatalf("counts %d warnings, %d errors, want 5 and 1", w, e)
	}
	if len(ring.Lines()) != 2 {
		t.Fatalf("lines: %d", len(ring.Lines()))
	}
	w0, e0 := Counts()
	NewWithWriter(&bytes.Buffer{}, "core", "info", "json").Error("counted in the process's ring")
	if w, e := Counts(); w != w0 || e != e0+1 {
		t.Fatalf("process counts %d,%d after %d,%d", w, e, w0, e0)
	}
}

func TestRingConcurrentWrites(t *testing.T) {
	ring := NewRing(RingSize)
	l := newLogger(&bytes.Buffer{}, ring, "core", "warn", "json")
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 100 {
				l.Warn("busy")
				_ = ring.Lines()
			}
		})
	}
	wg.Wait()
	if n := len(ring.Lines()); n != RingSize {
		t.Fatalf("kept %d", n)
	}
}

func TestNewTeesIntoRecent(t *testing.T) {
	NewWithWriter(&bytes.Buffer{}, "core", "info", "json").Warn("recent marker 7f3a", "api_key", "k-leak-7f3a")
	lines := Recent()
	if len(lines) == 0 {
		t.Fatal("nothing kept")
	}
	l := lines[len(lines)-1]
	if l.Msg != "recent marker 7f3a" || strings.Contains(l.Attrs, "k-leak-7f3a") {
		t.Fatalf("bad line: %+v", l)
	}
}
