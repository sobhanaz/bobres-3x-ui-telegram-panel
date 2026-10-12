package paymentsclient

import (
	"context"
	"fmt"

	paymentsv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/payments/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/logging"
)

// RecentLogs reads the service's kept warnings and errors (already
// redacted), oldest first.
func (c *Client) RecentLogs(ctx context.Context) ([]logging.Line, error) {
	resp, err := c.rpc.RecentLogs(ctx, &paymentsv1.RecentLogsRequest{})
	if err != nil {
		return nil, fmt.Errorf("payments.RecentLogs: %w", err)
	}
	out := make([]logging.Line, 0, len(resp.GetLines()))
	for _, l := range resp.GetLines() {
		out = append(out, logging.Line{Time: l.GetTime(), Level: l.GetLevel(), Msg: l.GetMsg(), Attrs: l.GetAttrs()})
	}
	return out, nil
}
