package web

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/logging"
)

type fakeLogs struct {
	lines    []logging.Line
	err      error
	deadline time.Duration // how far off the call's deadline was
}

func (f *fakeLogs) RecentLogs(ctx context.Context) ([]logging.Line, error) {
	if d, ok := ctx.Deadline(); ok {
		f.deadline = time.Until(d)
	}
	return f.lines, f.err
}

// GET /api/v1/system/logs: the owner only (logs carry Telegram ids); core
// reads its own buffer, the others are asked within 3 seconds.
func TestSystemLogs(t *testing.T) {
	f, owner, _ := licenseFixture(t)
	f.staffUser(8911, "support")
	f.staffUser(8912, "admin")
	sup, adm := f.browser(), f.browser()
	sup.loginWithLink(8911)
	adm.loginWithLink(8912)
	for name, b := range map[string]*browser{"support": sup, "admin": adm} {
		if code, _ := b.do(http.MethodGet, "/api/v1/system/logs?service=core", nil); code != http.StatusForbidden {
			t.Errorf("%s: %d", name, code)
		}
	}

	// Core's own lines, redacted before they were kept.
	logging.NewWithWriter(io.Discard, "core", "info", "json").Warn("core logs marker", "service_token", "tok-leak-91", "user_id", 7)
	code, out := owner.do(http.MethodGet, "/api/v1/system/logs", nil)
	if code != 200 || out["service"] != "core" || out["error"] != "" {
		t.Fatalf("core: %d %v", code, out)
	}
	lines := out["lines"].([]any)
	last := obj(lines[len(lines)-1])
	if last["msg"] != "core logs marker" || last["level"] != "WARN" || last["time"] == 0.0 ||
		last["attrs"] != `{"service_token":"[REDACTED]","user_id":7}` || strings.Contains(asJSON(out), "tok-leak-91") {
		t.Fatalf("core line: %v", last)
	}

	bot := &fakeLogs{lines: []logging.Line{{Time: 1760000000, Level: "ERROR", Msg: "send failed", Attrs: "{}"}}}
	f.api.cfg.Logs = map[string]LogSource{"bot": bot, "payments": &fakeLogs{err: errors.New("rpc error: code = Unavailable")}}
	if _, out = owner.do(http.MethodGet, "/api/v1/system/logs?service=bot", nil); asJSON(out) !=
		`{"error":"","lines":[{"attrs":"{}","level":"ERROR","msg":"send failed","time":1760000000}],"service":"bot"}` {
		t.Fatalf("bot: %v", out)
	}
	if bot.deadline <= 0 || bot.deadline > 3*time.Second {
		t.Fatalf("the bot was asked with deadline %v", bot.deadline)
	}
	if _, out = owner.do(http.MethodGet, "/api/v1/system/logs?service=payments", nil); asJSON(out) !=
		`{"error":"unreachable","lines":[],"service":"payments"}` {
		t.Fatalf("payments down: %v", out)
	}
	if _, out = owner.do(http.MethodGet, "/api/v1/system/logs?service=provisioner", nil); asJSON(out) !=
		`{"error":"not configured","lines":[],"service":"provisioner"}` {
		t.Fatalf("provisioner missing: %v", out)
	}
	for _, svc := range []string{"postgres", "backup"} {
		if code, _ := owner.do(http.MethodGet, "/api/v1/system/logs?service="+svc, nil); code != http.StatusBadRequest {
			t.Errorf("%s: %d", svc, code)
		}
	}
}
