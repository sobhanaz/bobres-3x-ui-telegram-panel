// Package adapter abstracts the 3x-ui panel behind PanelAdapter so the
// provisioner never hardcodes a panel version; older panels get their own
// adapters later.
package adapter

import "context"

// PanelAdapter is the 3x-ui-facing contract (latest v3.x semantics).
type PanelAdapter interface {
	// CreateClient adds a client to all listed inbound IDs. expiryMs is a unix
	// millisecond deadline; 0 means unlimited. trafficBytes 0 = unlimited.
	CreateClient(ctx context.Context, email, subID string, expiryMs, trafficBytes int64, inboundIDs []int) error
	// Renew extends expiry and/or traffic.
	Renew(ctx context.Context, email string, addDays, addBytes int64) error
	Delete(ctx context.Context, email string) error
	ResetTraffic(ctx context.Context, email string) error
	Usage(ctx context.Context, email string) (usedBytes int64, err error)
	Links(ctx context.Context, email string) ([]string, error)
	// SubLinks returns the share links served for a subscription id.
	SubLinks(ctx context.Context, subID string) ([]string, error)
	// ClientSubID returns the subscription id of an existing client, so a
	// retried create can tell our own client from someone else's.
	ClientSubID(ctx context.Context, email string) (string, error)
	Inbounds(ctx context.Context) ([]Inbound, error)
	// HealthCheck pings the panel and reports health metadata.
	HealthCheck(ctx context.Context) (version string, err error)
}

// Inbound is one panel inbound with its enable flag.
type Inbound struct {
	ID      int
	Tag     string
	Enabled bool
}
