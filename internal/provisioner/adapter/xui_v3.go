package adapter

import (
	"context"
	"fmt"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/xui"
)

// XUIv3 implements PanelAdapter against 3x-ui v3.x using the hardened
// internal/xui client (token auth, per-email locks, SSRF guard).
type XUIv3 struct {
	c *xui.Client
}

// NewXUIv3 wraps a base URL + API token.
func NewXUIv3(baseURL, apiToken string, opts ...xui.Option) (*XUIv3, error) {
	c, err := xui.New(baseURL, apiToken, opts...)
	if err != nil {
		return nil, err
	}
	return &XUIv3{c: c}, nil
}

func (a *XUIv3) CreateClient(ctx context.Context, email, subID string, expiryMs, trafficBytes int64, inboundIDs []int) error {
	spec := xui.ClientSpec{
		Email:      email,
		SubID:      subID,
		ExpiryTime: expiryMs,
		TotalGB:    trafficBytes,
		Enable:     true,
	}
	return a.c.AddClient(ctx, spec, inboundIDs)
}

func (a *XUIv3) Renew(ctx context.Context, email string, addDays, addBytes int64) error {
	res, err := a.c.BulkAdjust(ctx, xui.AdjustRequest{
		Emails:   []string{email},
		AddDays:  int(addDays),
		AddBytes: addBytes,
	})
	if err != nil {
		return err
	}
	if len(res.Skipped) > 0 {
		return fmt.Errorf("renew skipped for %s: %s", res.Skipped[0].Email, res.Skipped[0].Reason)
	}
	return nil
}

func (a *XUIv3) SetLimits(ctx context.Context, email string, expiryMs, totalBytes int64) error {
	return a.c.SetLimits(ctx, email, expiryMs, totalBytes)
}

func (a *XUIv3) SetEnabled(ctx context.Context, email string, enabled bool) error {
	return a.c.SetEnabled(ctx, email, enabled)
}

func (a *XUIv3) Status(ctx context.Context, email string) (ClientStatus, error) {
	d, err := a.c.GetClient(ctx, email)
	if err != nil {
		return ClientStatus{}, err
	}
	return ClientStatus{UsedBytes: d.UsedTraffic, TotalBytes: d.Client.TotalGB, ExpiryMs: d.Client.ExpiryTime, Enabled: d.Client.Enable}, nil
}

func (a *XUIv3) Delete(ctx context.Context, email string) error {
	return a.c.DeleteClient(ctx, email, false)
}

func (a *XUIv3) ResetTraffic(ctx context.Context, email string) error {
	return a.c.ResetTraffic(ctx, email)
}

func (a *XUIv3) Usage(ctx context.Context, email string) (int64, error) {
	t, err := a.c.Traffic(ctx, email)
	if err != nil {
		return 0, err
	}
	return t.Used(), nil
}

func (a *XUIv3) Links(ctx context.Context, email string) ([]string, error) {
	return a.c.Links(ctx, email)
}

func (a *XUIv3) SubLinks(ctx context.Context, subID string) ([]string, error) {
	return a.c.SubLinks(ctx, subID)
}

func (a *XUIv3) ClientSubID(ctx context.Context, email string) (string, error) {
	d, err := a.c.GetClient(ctx, email)
	if err != nil {
		return "", err
	}
	return d.Client.SubID, nil
}

func (a *XUIv3) Inbounds(ctx context.Context) ([]Inbound, error) {
	opts, err := a.c.InboundOptions(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Inbound, 0, len(opts))
	for _, o := range opts {
		out = append(out, Inbound{ID: o.ID, Tag: o.Remark, Enabled: o.Enable})
	}
	return out, nil
}

func (a *XUIv3) HealthCheck(ctx context.Context) (string, error) {
	// A cheap authenticated call doubles as the health probe; the panel has
	// no dedicated version endpoint on the v3 API we target.
	if _, err := a.c.InboundOptions(ctx); err != nil {
		return "", fmt.Errorf("health: %w", err)
	}
	return "v3.x", nil
}
