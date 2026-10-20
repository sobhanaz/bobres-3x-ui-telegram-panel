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

var (
	_ PanelAdapter = (*XUIv3)(nil)
	_ GroupTagger  = (*XUIv3)(nil)
)

// NewXUIv3 wraps a base URL + API token.
func NewXUIv3(baseURL, apiToken string, opts ...xui.Option) (*XUIv3, error) {
	c, err := xui.New(baseURL, apiToken, opts...)
	if err != nil {
		return nil, err
	}
	return &XUIv3{c: c}, nil
}

// CreateClient adds the client in the bot group (xui.BotGroup). Later changes
// to it keep the group: SetLimits and SetEnabled send the stored row back with
// its group, and the panel's own bulkAdjust and resetTraffic leave it.
func (a *XUIv3) CreateClient(ctx context.Context, email, subID string, expiryMs, trafficBytes int64, inboundIDs []int) error {
	spec := xui.ClientSpec{
		Email:      email,
		SubID:      subID,
		ExpiryTime: expiryMs,
		TotalGB:    trafficBytes,
		Enable:     true,
		Group:      xui.BotGroup,
	}
	return a.c.AddClient(ctx, spec, inboundIDs)
}

// tagPasses bounds how often TagUngrouped lists the panel: the first listing
// finds the clients to tag, the next ones check that none is left.
const tagPasses = 3

func (a *XUIv3) TagUngrouped(ctx context.Context, managed map[string]string) (TagResult, error) {
	res := TagResult{Managed: len(managed)}
	if len(managed) == 0 {
		res.Converged = true
		return res, nil
	}
	for pass := 0; pass < tagPasses; pass++ {
		rows, err := a.c.ListClients(ctx)
		if err != nil {
			return res, err
		}
		seen, grouped := map[string]bool{}, 0
		var todo []string
		for _, r := range rows {
			sub, ok := managed[r.Email]
			if !ok || seen[r.Email] || (sub != "" && r.SubID != sub) {
				continue
			}
			seen[r.Email] = true
			switch {
			case r.Group != "":
				grouped++
			case xui.ValidateIdentifier(r.Email) == nil:
				todo = append(todo, r.Email)
			}
		}
		if pass == 0 {
			res.Grouped = grouped
		}
		res.Missing = len(managed) - len(seen)
		if len(todo) == 0 {
			res.Converged = true
			return res, nil
		}
		n, err := a.c.AddToGroup(ctx, todo, xui.BotGroup)
		res.Tagged += n
		if err != nil {
			return res, err
		}
	}
	return res, nil
}

func (a *XUIv3) EnsureTagged(ctx context.Context, email string) error {
	_, err := a.c.AddToGroupIfNone(ctx, email, xui.BotGroup)
	return err
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
		out = append(out, Inbound{ID: o.ID, Tag: o.Remark, Enabled: o.Enable, Protocol: o.Protocol, Port: o.Port})
	}
	return out, nil
}

func (a *XUIv3) HealthCheck(ctx context.Context) (string, error) {
	// The call provisioning depends on doubles as the health probe: a token
	// that may read the status but not the inbounds is not healthy.
	if _, err := a.c.InboundOptions(ctx); err != nil {
		return "", fmt.Errorf("health: %w", err)
	}
	return a.PanelVersion(ctx), nil
}

func (a *XUIv3) PanelVersion(ctx context.Context) string {
	v, err := a.c.PanelVersion(ctx)
	if err != nil {
		return UnknownVersion
	}
	return NormalizeVersion(v)
}
