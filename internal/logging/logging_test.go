package logging

import (
	"bytes"
	"errors"
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

func TestScrubsCredentialsInsideValuesAndErrors(t *testing.T) {
	var buf bytes.Buffer
	l := NewWithWriter(&buf, "core", "info", "json")
	l.Error("db failed", "err", errors.New("dial postgres://bobres:hunter2@db:5432/x failed"), "msg2", "redis://:pw123@redis:6379")
	out := buf.String()
	for _, leak := range []string{"hunter2", "pw123"} {
		if strings.Contains(out, leak) {
			t.Fatalf("leaked %q in %s", leak, out)
		}
	}
	if !strings.Contains(out, "postgres://[REDACTED]@db:5432") {
		t.Fatalf("host should stay visible for debugging: %s", out)
	}
}

func TestBroaderKeysRedacted(t *testing.T) {
	var buf bytes.Buffer
	l := NewWithWriter(&buf, "core", "info", "json")
	// Leak markers must not occur naturally in the log line: the old "1:2" also
	// matched timestamps such as 21:26, so the test failed a few percent of runs.
	l.Info("x", "database_url", "postgres://a", "license", "tok", "Authorization", "Bearer abc", "bot_token", "999:SecretBotTok", "user_id", 7)
	out := buf.String()
	for _, leak := range []string{"postgres://a", `"tok"`, "Bearer abc", "SecretBotTok"} {
		if strings.Contains(out, leak) {
			t.Fatalf("leaked %q: %s", leak, out)
		}
	}
	if !strings.Contains(out, `"user_id":7`) {
		t.Fatalf("normal field lost: %s", out)
	}
}

func TestBooleansAreNotRedacted(t *testing.T) {
	var buf bytes.Buffer
	l := NewWithWriter(&buf, "core", "info", "json")
	l.Info("starting", "database_url_set", true, "service_token", "s3cr3t")
	out := buf.String()
	if !strings.Contains(out, `"database_url_set":true`) || strings.Contains(out, "s3cr3t") {
		t.Fatalf("bad redaction: %s", out)
	}
}
