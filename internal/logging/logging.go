// Package logging builds the structured logger used by every service and
// redacts values that must never reach logs (tokens, keys, passwords).
package logging

import (
	"io"
	"log/slog"
	"os"
	"regexp"
	"strings"
)

var sensitiveKeys = []string{
	"token", "secret", "password", "passwd", "apikey", "api_key", "authorization", "auth",
	"cookie", "private", "bearer", "credential", "dsn", "database_url", "redis_url",
	"license", "signature", "key",
}

// urlCreds matches scheme://user:pass@ so a DSN inside an error string is scrubbed.
var urlCreds = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)[^\s/@:]*:?[^\s/@]*@`)

const redacted = "[REDACTED]"

// New returns a JSON (default) or text logger tagged with the service name.
func New(service, level, format string) *slog.Logger {
	return NewWithWriter(os.Stdout, service, level, format)
}

// NewWithWriter is New with an explicit destination (used by tests).
func NewWithWriter(w io.Writer, service, level, format string) *slog.Logger {
	opts := &slog.HandlerOptions{Level: parseLevel(level), ReplaceAttr: redact}
	var h slog.Handler
	if format == "text" {
		h = slog.NewTextHandler(w, opts)
	} else {
		h = slog.NewJSONHandler(w, opts)
	}
	return slog.New(h).With("service", service)
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func redact(_ []string, a slog.Attr) slog.Attr {
	k := strings.ToLower(a.Key)
	for _, s := range sensitiveKeys {
		if strings.Contains(k, s) {
			return slog.String(a.Key, redacted)
		}
	}
	switch a.Value.Kind() {
	case slog.KindString:
		return slog.String(a.Key, urlCreds.ReplaceAllString(a.Value.String(), "${1}[REDACTED]@"))
	case slog.KindAny:
		if err, ok := a.Value.Any().(error); ok {
			return slog.String(a.Key, urlCreds.ReplaceAllString(err.Error(), "${1}[REDACTED]@"))
		}
	}
	return a
}
