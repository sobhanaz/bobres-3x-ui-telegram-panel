package server

import (
	"context"
	"io"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/logging"
)

func TestRecentLogsReturnsKeptLines(t *testing.T) {
	logging.NewWithWriter(io.Discard, "provisioner", "info", "json").Warn("provisioner logs marker", "secret", "s-leak")
	resp, err := (&Server{}).RecentLogs(context.Background(), nil)
	if err != nil || len(resp.GetLines()) == 0 {
		t.Fatalf("lines %v, err %v", resp.GetLines(), err)
	}
	l := resp.GetLines()[len(resp.GetLines())-1]
	if l.GetMsg() != "provisioner logs marker" || l.GetLevel() != "WARN" || l.GetAttrs() != `{"secret":"[REDACTED]"}` || l.GetTime() == 0 {
		t.Fatalf("bad line %v", l)
	}
}
