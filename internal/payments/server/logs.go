package server

import (
	"context"

	paymentsv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/payments/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/logging"
)

// RecentLogs returns this process's kept warnings and errors, already
// redacted, for core's System page (core is the only peer).
func (s *Server) RecentLogs(context.Context, *paymentsv1.RecentLogsRequest) (*paymentsv1.RecentLogsResponse, error) {
	kept := logging.Recent()
	out := &paymentsv1.RecentLogsResponse{Lines: make([]*paymentsv1.LogLine, 0, len(kept))}
	for _, l := range kept {
		out.Lines = append(out.Lines, &paymentsv1.LogLine{Time: l.Time, Level: l.Level, Msg: l.Msg, Attrs: l.Attrs})
	}
	return out, nil
}
