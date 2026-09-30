package logging

import (
	"bytes"
	"strings"
	"testing"
)

func TestRedactsSensitiveKeys(t *testing.T) {
	var buf bytes.Buffer
	l := NewWithWriter(&buf, "core", "info", "json")
	l.Info("event", "bot_token", "123:ABC", "Password", "hunter2", "api_key", "k", "user_id", 42)
	out := buf.String()
	for _, leak := range []string{"123:ABC", "hunter2", `"k"`} {
		if strings.Contains(out, leak) {
			t.Fatalf("leaked %q in %s", leak, out)
		}
	}
	if !strings.Contains(out, `"user_id":42`) || !strings.Contains(out, `"service":"core"`) {
		t.Fatalf("missing normal fields: %s", out)
	}
}

func TestLevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	l := NewWithWriter(&buf, "x", "warn", "text")
	l.Info("hidden")
	l.Warn("shown")
	if strings.Contains(buf.String(), "hidden") || !strings.Contains(buf.String(), "shown") {
		t.Fatalf("bad filtering: %s", buf.String())
	}
}
