package server

import (
	"context"
	"math"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
)

// GetTopInviters is a page of the top-inviters board for the customer who asks:
// the names and Telegram ids on it are masked here, so the bot (and anything
// between) never holds a whole one. enabled is false while the board is off.
func (s *Server) GetTopInviters(ctx context.Context, req *corev1.GetTopInvitersRequest) (*corev1.TopInviters, error) {
	res, err := s.dom.TopInviters(ctx, req.GetUserId(), int(req.GetPage()), int(req.GetPageSize()))
	if err != nil {
		return nil, fail(err)
	}
	if !res.Enabled {
		return &corev1.TopInviters{}, nil
	}
	out := &corev1.TopInviters{Enabled: true, Page: clampInt32(int64(res.Page)), PageSize: clampInt32(int64(res.PageSize)),
		Total: clampInt32(int64(res.Total)), More: res.More, YourInvited: clampInt32(res.YourInvited), YourRank: clampInt32(int64(res.YourRank))}
	for _, e := range res.Entries {
		out.Entries = append(out.Entries, &corev1.TopInviter{Rank: clampInt32(int64(e.Rank)), Name: e.Name, TelegramId: e.TelegramID,
			Invited: clampInt32(e.Invited), You: e.You})
	}
	return out, nil
}

// clampInt32 is n as an int32 (a count that big is not a count anyone reads).
func clampInt32(n int64) int32 {
	return int32(min(max(n, 0), math.MaxInt32)) //nolint:gosec // bounded just above
}
