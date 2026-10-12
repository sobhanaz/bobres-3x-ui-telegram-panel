package server

import (
	"context"

	provisionerv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/provisioner/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/logging"
)

// RecentLogs returns this process's kept warnings and errors, already
// redacted, for core's System page (core is the only peer).
func (s *Server) RecentLogs(context.Context, *provisionerv1.RecentLogsRequest) (*provisionerv1.RecentLogsResponse, error) {
	kept := logging.Recent()
	out := &provisionerv1.RecentLogsResponse{Lines: make([]*provisionerv1.LogLine, 0, len(kept))}
	for _, l := range kept {
		out.Lines = append(out.Lines, &provisionerv1.LogLine{Time: l.Time, Level: l.Level, Msg: l.Msg, Attrs: l.Attrs})
	}
	return out, nil
}
