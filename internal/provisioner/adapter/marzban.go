package adapter

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"net/url"
	"path"
	"sort"
	"strings"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/marzban"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/xui"
)

// SubscriptionLinker is implemented by adapters whose panel owns the
// subscription URL (Marzban derives a token from the username), instead of
// the provisioner composing it from the derived subscription id.
type SubscriptionLinker interface {
	// SubscriptionLink is the client's subscription URL; subBase, when set,
	// replaces the prefix the panel reports (a CDN or reverse proxy).
	SubscriptionLink(ctx context.Context, email, subBase string) (string, error)
}

// subNotePrefix tags the note of a Marzban user BOBRES created: Marzban has
// no field for the derived subscription id, and the id is how the provisioner
// tells its own client from a stranger's under the same username.
const subNotePrefix = "bobres-sub:"

// Marzban implements PanelAdapter against a Marzban panel.
//
// Differences from 3x-ui it absorbs: Marzban counts expiry in seconds
// (BOBRES in milliseconds), starts the clock on first use through the
// "on_hold" status (3x-ui: a negative expiry), has no numeric inbound ids
// (they are derived from protocol and tag, see Inbounds), and owns the
// subscription token (SubscriptionLinker).
type Marzban struct {
	c    *marzban.Client
	base string
}

// NewMarzban wraps a panel URL and the admin credentials ("username:password",
// as stored for a Marzban server).
func NewMarzban(baseURL, credentials string, opts ...xui.Option) (*Marzban, error) {
	user, pass, err := marzban.ParseCredentials(credentials)
	if err != nil {
		return nil, err
	}
	c, err := marzban.New(baseURL, user, pass, opts...)
	if err != nil {
		return nil, err
	}
	return &Marzban{c: c, base: strings.TrimRight(strings.TrimSpace(baseURL), "/")}, nil
}

var (
	_ PanelAdapter       = (*Marzban)(nil)
	_ SubscriptionLinker = (*Marzban)(nil)
)

// mzInbound is a Marzban inbound with the id BOBRES knows it by.
type mzInbound struct {
	id       int
	protocol string
	tag      string
	port     int
}

// inboundTable gives every inbound a stable positive id from its protocol and
// tag (the same inbound keeps its id while the others change). A hash
// collision moves the later one to the next free id; the order is sorted, so
// the answer is the same for the same set.
func inboundTable(by map[string][]marzban.Inbound) []mzInbound {
	var all []mzInbound
	for proto, list := range by {
		for _, in := range list {
			all = append(all, mzInbound{protocol: proto, tag: in.Tag, port: in.PortNumber()})
		}
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].protocol != all[j].protocol {
			return all[i].protocol < all[j].protocol
		}
		return all[i].tag < all[j].tag
	})
	used := map[int]bool{}
	for i := range all {
		h := fnv.New32a()
		_, _ = h.Write([]byte(all[i].protocol + "\x00" + all[i].tag))
		id := int(h.Sum32()&0x7fffffff) | 1 // positive and never 0
		for used[id] {
			id = id%0x7fffffff + 1
		}
		used[id] = true
		all[i].id = id
	}
	return all
}

func (a *Marzban) table(ctx context.Context) ([]mzInbound, error) {
	by, err := a.c.Inbounds(ctx)
	if err != nil {
		return nil, err
	}
	return inboundTable(by), nil
}

func (a *Marzban) Inbounds(ctx context.Context) ([]Inbound, error) {
	t, err := a.table(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Inbound, 0, len(t))
	for _, in := range t {
		out = append(out, Inbound{ID: in.id, Tag: in.tag, Enabled: true, Protocol: in.protocol, Port: in.port})
	}
	return out, nil
}

// expiryFields turns the interface's millisecond expiry into Marzban's:
// 0 unlimited, positive an absolute time, negative a duration that starts on
// first use.
func expiryFields(expiryMs int64) map[string]any {
	switch {
	case expiryMs > 0:
		return map[string]any{"status": marzban.StatusActive, "expire": expiryMs / 1000}
	case expiryMs < 0:
		return map[string]any{"status": marzban.StatusOnHold, "on_hold_expire_duration": max(-expiryMs/1000, 1)}
	}
	return map[string]any{"status": marzban.StatusActive, "expire": 0}
}

func (a *Marzban) CreateClient(ctx context.Context, email, subID string, expiryMs, trafficBytes int64, inboundIDs []int) error {
	t, err := a.table(ctx)
	if err != nil {
		return err
	}
	proxies := map[string]any{}
	inbounds := map[string][]string{}
	if len(inboundIDs) == 0 {
		// All of them: every protocol the panel serves, no tag filter.
		for _, in := range t {
			proxies[in.protocol] = map[string]any{}
		}
	} else {
		byID := map[int]mzInbound{}
		for _, in := range t {
			byID[in.id] = in
		}
		for _, id := range inboundIDs {
			in, ok := byID[id]
			if !ok {
				return fmt.Errorf("marzban: inbound %d does not exist", id)
			}
			proxies[in.protocol] = map[string]any{}
			inbounds[in.protocol] = append(inbounds[in.protocol], in.tag)
		}
	}
	if len(proxies) == 0 {
		return errors.New("marzban: the panel has no inbounds")
	}
	body := map[string]any{
		"username":                  email,
		"proxies":                   proxies,
		"data_limit":                trafficBytes,
		"data_limit_reset_strategy": "no_reset",
		"note":                      subNotePrefix + subID,
	}
	if len(inbounds) > 0 {
		body["inbounds"] = inbounds
	}
	for k, v := range expiryFields(expiryMs) {
		body[k] = v
	}
	return a.c.CreateUser(ctx, body)
}

func (a *Marzban) Renew(ctx context.Context, email string, addDays, addBytes int64) error {
	u, err := a.c.GetUser(ctx, email)
	if err != nil {
		return err
	}
	body := map[string]any{}
	if addDays != 0 {
		switch {
		case u.Status == marzban.StatusOnHold && u.HoldSeconds() > 0:
			body["on_hold_expire_duration"] = max(u.HoldSeconds()+addDays*86400, 1)
		case u.ExpireSeconds() == 0:
			return fmt.Errorf("renew skipped for %s: unlimited expiry", email)
		default:
			body["expire"] = u.ExpireSeconds() + addDays*86400
		}
	}
	if addBytes != 0 {
		if u.DataLimitBytes() == 0 {
			return fmt.Errorf("renew skipped for %s: unlimited traffic", email)
		}
		body["data_limit"] = max(u.DataLimitBytes()+addBytes, 1)
	}
	if len(body) == 0 {
		return nil
	}
	return a.c.ModifyUser(ctx, email, body)
}

func (a *Marzban) SetLimits(ctx context.Context, email string, expiryMs, totalBytes int64) error {
	if expiryMs < 0 || totalBytes < 0 {
		return errors.New("marzban: SetLimits needs limits >= 0")
	}
	body := expiryFields(expiryMs)
	body["data_limit"] = totalBytes
	return a.c.ModifyUser(ctx, email, body)
}

func (a *Marzban) SetEnabled(ctx context.Context, email string, enabled bool) error {
	if !enabled {
		return a.c.ModifyUser(ctx, email, map[string]any{"status": marzban.StatusDisabled})
	}
	u, err := a.c.GetUser(ctx, email)
	if err != nil {
		return err
	}
	// A client that never connected keeps waiting for its first use rather
	// than becoming an unlimited active one.
	st := marzban.StatusActive
	if u.ExpireSeconds() == 0 && u.HoldSeconds() > 0 && u.UsedTraffic == 0 {
		st = marzban.StatusOnHold
	}
	return a.c.ModifyUser(ctx, email, map[string]any{"status": st})
}

func (a *Marzban) Status(ctx context.Context, email string) (ClientStatus, error) {
	u, err := a.c.GetUser(ctx, email)
	if err != nil {
		return ClientStatus{}, err
	}
	exp := u.ExpireSeconds() * 1000
	if u.Status == marzban.StatusOnHold && exp == 0 {
		exp = -u.HoldSeconds() * 1000
	}
	return ClientStatus{
		UsedBytes: u.UsedTraffic, TotalBytes: u.DataLimitBytes(), ExpiryMs: exp,
		Enabled: u.Status == marzban.StatusActive || u.Status == marzban.StatusOnHold,
	}, nil
}

func (a *Marzban) Delete(ctx context.Context, email string) error { return a.c.DeleteUser(ctx, email) }

func (a *Marzban) ResetTraffic(ctx context.Context, email string) error {
	return a.c.ResetUsage(ctx, email)
}

func (a *Marzban) Usage(ctx context.Context, email string) (int64, error) {
	u, err := a.c.GetUser(ctx, email)
	if err != nil {
		return 0, err
	}
	return u.UsedTraffic, nil
}

func (a *Marzban) Links(ctx context.Context, email string) ([]string, error) {
	u, err := a.c.GetUser(ctx, email)
	if err != nil {
		return nil, err
	}
	return u.Links, nil
}

// SubLinks is not offered: Marzban serves a subscription by a token it owns,
// not by the derived id. Links and SubscriptionLink cover it.
func (a *Marzban) SubLinks(context.Context, string) ([]string, error) {
	return nil, errors.New("marzban: subscriptions are addressed by the panel's own token")
}

// ClientSubID reads the derived id BOBRES left in the user's note. A user it
// did not create has none (""), so the provisioner never takes it for its own.
func (a *Marzban) ClientSubID(ctx context.Context, email string) (string, error) {
	u, err := a.c.GetUser(ctx, email)
	if err != nil {
		return "", err
	}
	if id, ok := strings.CutPrefix(u.NoteText(), subNotePrefix); ok {
		return strings.TrimSpace(id), nil
	}
	return "", nil
}

func (a *Marzban) SubscriptionLink(ctx context.Context, email, subBase string) (string, error) {
	u, err := a.c.GetUser(ctx, email)
	if err != nil {
		return "", err
	}
	raw := strings.TrimSpace(u.SubscriptionURL)
	if raw == "" {
		return "", errors.New("marzban: the panel gave no subscription URL")
	}
	if subBase != "" {
		tok := path.Base(strings.TrimRight(raw, "/"))
		return strings.TrimRight(subBase, "/") + "/" + tok, nil
	}
	if strings.HasPrefix(raw, "/") {
		return a.base + raw, nil
	}
	if p, err := url.Parse(raw); err != nil || p.Host == "" {
		return "", errors.New("marzban: the panel gave an unusable subscription URL")
	}
	return raw, nil
}

func (a *Marzban) HealthCheck(ctx context.Context) (string, error) {
	// Provisioning needs the inbounds: an admin that can log in but not read
	// them is not healthy.
	if _, err := a.c.Inbounds(ctx); err != nil {
		return "", fmt.Errorf("health: %w", err)
	}
	return a.PanelVersion(ctx), nil
}

// PanelVersion is what /api/system reports, or "" when it does not say (the
// UnknownVersion "v3.x" names a 3x-ui generation and would mislead here).
func (a *Marzban) PanelVersion(ctx context.Context) string {
	v, err := a.c.Version(ctx)
	if err != nil {
		return ""
	}
	if v = NormalizeVersion(v); v == UnknownVersion {
		return ""
	}
	return v
}
