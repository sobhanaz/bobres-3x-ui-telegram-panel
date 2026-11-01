package monitor

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// A bot that does not answer right after core starts (an update or a reboot starts core first) is logged as
// information; one that still does not answer after botStartGrace is a warning again.
func TestCIFactsBotDownAtStartIsNotAWarning(t *testing.T) {
	e := newEnv(t)
	var buf bytes.Buffer
	e.m = New(Config{Store: e.st, Domain: e.dom, Dir: e.dir, Health: e.health, Servers: e.servers, Gateways: e.gateways, Payments: e.payments,
		Backups: e.backups, Bot: e.bot, Logs: e.logs, Version: "0.7.0", Now: e.clk.now,
		Log: slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))})
	e.bot.ciErr = errBoom

	e.clk.add(5 * time.Second)
	e.m.refreshCI(context.Background())
	if out := buf.String(); !strings.Contains(out, "level=INFO") || strings.Contains(out, "level=WARN") {
		t.Fatalf("at start: %s", out)
	}

	buf.Reset()
	e.clk.add(botStartGrace)
	e.m.refreshCI(context.Background())
	if out := buf.String(); !strings.Contains(out, "level=WARN") {
		t.Fatalf("after the grace: %s", out)
	}
}
