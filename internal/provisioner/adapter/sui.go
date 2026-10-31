package adapter

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/sui"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/xui"
)

// SUI implements PanelAdapter against an S-UI panel (sing-box), through its API
// v2 and an API token. Checked against a real v1.6.6 panel (CI job "S-UI
// integration").
//
// What it absorbs: S-UI addresses a client by a number of its own and finds one
// by name only in a listing of all of them (a nameIndex remembers the numbers);
// a client's name is its subscription id, so the derived subscription id BOBRES
// uses to tell its clients from a stranger's is kept in the client's
// description ("bobres-sub:<id>", as on Marzban), and the panel owns the
// subscription URL (SubscriptionLinker). Expiry is in seconds; "start on first
// use" is the panel's delayed start, counted in whole days. An edit sends the
// whole client back: the panel keeps its traffic counters unless the edit sends
// both as zero, which resets them (that is ResetTraffic). Clients are grouped
// (GroupedCreator, GroupRegrouper) as on 3x-ui.
type SUI struct {
	c   *sui.Client
	idx *nameIndex[int]
	now func() time.Time
}

var (
	_ PanelAdapter       = (*SUI)(nil)
	_ SubscriptionLinker = (*SUI)(nil)
	_ GroupedCreator     = (*SUI)(nil)
	_ GroupRegrouper     = (*SUI)(nil)
)

// NewSUI wraps a panel address (with its web path) and an API token.
func NewSUI(baseURL, token string, opts ...xui.Option) (*SUI, error) {
	c, err := sui.New(baseURL, token, opts...)
	if err != nil {
		return nil, err
	}
	return &SUI{c: c, idx: newNameIndex[int](), now: time.Now}, nil
}

const dayMs = 24 * 60 * 60 * 1000

// list is every client name with its id, for the nameIndex.
func (a *SUI) list(ctx context.Context) (map[string]int, error) {
	rows, err := a.c.Clients(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]int, len(rows))
	for _, r := range rows {
		if _, dup := out[r.Name]; !dup {
			out[r.Name] = r.ID
		}
	}
	return out, nil
}

// record reads the client named email in full; a *sui.NotFoundError when the
// panel has none.
func (a *SUI) record(ctx context.Context, email string) (sui.Record, error) {
	if id, ok := a.idx.get(email); ok {
		looked := a.now()
		r, found, err := a.c.Client(ctx, id)
		if err != nil {
			return nil, err
		}
		if found && r.Str("name") == email {
			return r, nil
		}
		a.idx.forget(email)
		return a.lookup(ctx, email, looked)
	}
	return a.lookup(ctx, email, time.Time{})
}

func (a *SUI) lookup(ctx context.Context, email string, since time.Time) (sui.Record, error) {
	id, ok, err := a.idx.refresh(ctx, email, since, a.list)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, &sui.NotFoundError{Name: email}
	}
	r, found, err := a.c.Client(ctx, id)
	if err != nil {
		return nil, err
	}
	if !found || r.Str("name") != email {
		a.idx.forget(email)
		return nil, &sui.NotFoundError{Name: email}
	}
	return r, nil
}

// edit sends a changed client back. Unless reset, the traffic counters are
// sent non-zero so the panel keeps its own (it resets them when both are zero).
func (a *SUI) edit(ctx context.Context, r sui.Record, reset bool) error {
	if reset {
		r.Set("up", 0)
		r.Set("down", 0)
	} else if r.Int("up") == 0 && r.Int("down") == 0 {
		r.Set("up", 1) // any non-zero: the panel puts its stored counters back
	}
	return a.c.Save(ctx, "edit", r)
}

func (a *SUI) Inbounds(ctx context.Context) ([]Inbound, error) {
	ins, err := a.c.Inbounds(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Inbound, 0, len(ins))
	for _, in := range ins {
		// An inbound without users (tun, direct...) takes no clients: it is
		// listed as off so no plan can choose it.
		out = append(out, Inbound{ID: in.ID, Tag: in.Tag, Enabled: in.TakesUsers, Protocol: in.Type, Port: in.Port})
	}
	return out, nil
}

// expiryFields turns the interface's millisecond expiry into the panel's:
// 0 none, positive an absolute time (seconds), negative a delayed start of
// whole days.
func suiExpiry(r sui.Record, expiryMs int64) {
	switch {
	case expiryMs > 0:
		r.Set("expiry", expiryMs/1000)
		r.Set("delayStart", false)
	case expiryMs < 0:
		days := (-expiryMs + dayMs - 1) / dayMs
		r.Set("expiry", 0)
		r.Set("delayStart", true)
		r.Set("resetDays", max(days, 1))
	default:
		r.Set("expiry", 0)
		r.Set("delayStart", false)
	}
}

func (a *SUI) CreateClient(ctx context.Context, email, subID string, expiryMs, trafficBytes int64, inboundIDs []int) error {
	return a.CreateClientInGroup(ctx, "", email, subID, expiryMs, trafficBytes, inboundIDs)
}

// CreateClientInGroup creates the client on the inbounds given (all that take
// clients when none is given), in the named group ("" is xui.BotGroup).
func (a *SUI) CreateClientInGroup(ctx context.Context, group, email, subID string, expiryMs, trafficBytes int64, inboundIDs []int) error {
	if strings.TrimSpace(email) != email || email == "" {
		return errors.New("s-ui: invalid client name")
	}
	if group == "" {
		group = xui.BotGroup
	}
	ids := slices.Clone(inboundIDs)
	if len(ids) == 0 {
		ins, err := a.Inbounds(ctx)
		if err != nil {
			return err
		}
		for _, in := range ins {
			if in.Enabled {
				ids = append(ids, in.ID)
			}
		}
		if len(ids) == 0 {
			return errors.New("s-ui: the panel has no inbound that takes clients")
		}
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	cfg, err := sui.RandomConfigs(email)
	if err != nil {
		return err
	}
	r := sui.Record{}
	r.Set("enable", true)
	r.Set("name", email)
	r.Set("config", cfg)
	r.Set("inbounds", ids)
	r.Set("links", []sui.Link{})
	r.Set("volume", max(trafficBytes, 0))
	r.Set("up", 0)
	r.Set("down", 0)
	r.Set("desc", subNotePrefix+subID)
	r.Set("group", group)
	r.Set("remark", "")
	r.Set("autoReset", false)
	r.Set("resetDays", 0)
	suiExpiry(r, expiryMs)
	if err := a.c.Save(ctx, "new", r); err != nil {
		a.idx.stale() // the name may be taken by a client made since the last listing
		return err
	}
	a.idx.forget(email) // the panel gave it an id: the next lookup finds it
	a.idx.stale()
	return nil
}

func (a *SUI) Renew(ctx context.Context, email string, addDays, addBytes int64) error {
	r, err := a.record(ctx, email)
	if err != nil {
		return err
	}
	if addDays != 0 {
		switch {
		case r.Bool("delayStart") && r.Int("expiry") == 0:
			r.Set("resetDays", max(r.Int("resetDays")+addDays, 1))
		case r.Int("expiry") == 0:
			return fmt.Errorf("renew skipped for %s: unlimited expiry", email)
		default:
			r.Set("expiry", r.Int("expiry")+addDays*86400)
		}
	}
	if addBytes != 0 {
		if r.Int("volume") == 0 {
			return fmt.Errorf("renew skipped for %s: unlimited traffic", email)
		}
		r.Set("volume", max(r.Int("volume")+addBytes, 1))
	}
	if addDays == 0 && addBytes == 0 {
		return nil
	}
	return a.edit(ctx, r, false)
}

func (a *SUI) SetLimits(ctx context.Context, email string, expiryMs, totalBytes int64) error {
	if expiryMs < 0 || totalBytes < 0 {
		return errors.New("s-ui: SetLimits needs limits >= 0")
	}
	r, err := a.record(ctx, email)
	if err != nil {
		return err
	}
	suiExpiry(r, expiryMs)
	r.Set("volume", totalBytes)
	r.Set("enable", true)
	return a.edit(ctx, r, false)
}

func (a *SUI) SetEnabled(ctx context.Context, email string, enabled bool) error {
	r, err := a.record(ctx, email)
	if err != nil {
		return err
	}
	if r.Bool("enable") == enabled {
		return nil
	}
	r.Set("enable", enabled)
	return a.edit(ctx, r, false)
}

// suiStatus is the client's limits as the interface counts them.
func suiStatus(r sui.Record) ClientStatus {
	exp := r.Int("expiry") * 1000
	if exp == 0 && r.Bool("delayStart") {
		exp = -max(r.Int("resetDays"), 1) * dayMs
	}
	return ClientStatus{UsedBytes: r.Int("up") + r.Int("down"), TotalBytes: r.Int("volume"), ExpiryMs: exp, Enabled: r.Bool("enable")}
}

func (a *SUI) Status(ctx context.Context, email string) (ClientStatus, error) {
	r, err := a.record(ctx, email)
	if err != nil {
		return ClientStatus{}, err
	}
	return suiStatus(r), nil
}

func (a *SUI) Delete(ctx context.Context, email string) error {
	r, err := a.record(ctx, email)
	if err != nil {
		return err
	}
	if err := a.c.Save(ctx, "del", r.Int("id")); err != nil {
		return err
	}
	a.idx.forget(email)
	return nil
}

// ResetTraffic zeroes the counters (the panel adds them to the client's
// totals). The panel's own job switched off a client that used up its quota;
// such a client, not expired, comes back on, as its counter is zero now. One
// switched off for another reason stays off.
func (a *SUI) ResetTraffic(ctx context.Context, email string) error {
	r, err := a.record(ctx, email)
	if err != nil {
		return err
	}
	vol, used, exp := r.Int("volume"), r.Int("up")+r.Int("down"), r.Int("expiry")
	if !r.Bool("enable") && vol > 0 && used >= vol && (exp == 0 || exp > a.now().Unix()) {
		r.Set("enable", true)
	}
	return a.edit(ctx, r, true)
}

func (a *SUI) Usage(ctx context.Context, email string) (int64, error) {
	r, err := a.record(ctx, email)
	if err != nil {
		return 0, err
	}
	return r.Int("up") + r.Int("down"), nil
}

func (a *SUI) Links(ctx context.Context, email string) ([]string, error) {
	r, err := a.record(ctx, email)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, l := range r.Links() {
		if l.URI != "" {
			out = append(out, l.URI)
		}
	}
	return out, nil
}

// SubLinks is not offered: the panel serves a subscription by the client's
// name, not by the derived id. Links and SubscriptionLink cover it.
func (a *SUI) SubLinks(context.Context, string) ([]string, error) {
	return nil, errors.New("s-ui: subscriptions are addressed by the client's name")
}

// ClientSubID reads the derived id BOBRES left in the client's description; a
// client it did not create has none ("").
func (a *SUI) ClientSubID(ctx context.Context, email string) (string, error) {
	r, err := a.record(ctx, email)
	if err != nil {
		return "", err
	}
	if id, ok := strings.CutPrefix(r.Str("desc"), subNotePrefix); ok {
		return strings.TrimSpace(id), nil
	}
	return "", nil
}

// SubscriptionLink is the panel's subscription address with the client's name:
// subBase when set, else what the panel itself would show (its subURI setting,
// or its subscription port and path on the panel's host).
func (a *SUI) SubscriptionLink(ctx context.Context, email, subBase string) (string, error) {
	if _, err := a.record(ctx, email); err != nil {
		return "", err
	}
	name := url.PathEscape(email)
	if subBase != "" {
		return strings.TrimRight(subBase, "/") + "/" + name, nil
	}
	set, err := a.c.Settings(ctx)
	if err != nil {
		return "", err
	}
	if uri := strings.TrimSpace(set["subURI"]); uri != "" {
		return strings.TrimRight(uri, "/") + "/" + name, nil
	}
	pu, err := url.Parse(a.c.Base())
	if err != nil {
		return "", err
	}
	scheme := "http"
	if set["subKeyFile"] != "" && set["subCertFile"] != "" {
		scheme = "https"
	}
	host := pu.Hostname()
	if d := strings.TrimSpace(set["subDomain"]); d != "" {
		host = d
	}
	port := strings.TrimSpace(set["subPort"])
	if port == "" {
		port = "2096"
	}
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}
	if port != "" {
		host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	path := set["subPath"]
	if path == "" {
		path = "/sub/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if !strings.HasSuffix(path, "/") {
		path += "/"
	}
	return scheme + "://" + host + path + name, nil
}

func (a *SUI) HealthCheck(ctx context.Context) (string, error) {
	if _, err := a.c.Inbounds(ctx); err != nil {
		return "", fmt.Errorf("health: %w", err)
	}
	return a.PanelVersion(ctx), nil
}

// PanelVersion is what the panel's status reports, or "" when it does not say.
func (a *SUI) PanelVersion(ctx context.Context) string {
	v, err := a.c.Version(ctx)
	if err != nil {
		return ""
	}
	if v = NormalizeVersion(v); v == UnknownVersion {
		return ""
	}
	return v
}

// EnsureGroup puts one client in the group unless it is in one.
func (a *SUI) EnsureGroup(ctx context.Context, email, group string) error {
	if group == "" {
		group = xui.BotGroup
	}
	r, err := a.record(ctx, email)
	if err != nil {
		return err
	}
	if r.Str("group") != "" {
		return nil
	}
	r.Set("group", group)
	return a.edit(ctx, r, false)
}

// Regroup: see GroupRegrouper. The panel has no call that moves clients
// between groups, so each one is edited.
func (a *SUI) Regroup(ctx context.Context, want map[string]GroupWant) (TagResult, error) {
	res := TagResult{Managed: len(want)}
	if len(want) == 0 {
		res.Converged = true
		return res, nil
	}
	target := func(email string) string {
		if g := want[email].Group; g != "" && xui.ValidateGroup(g) == nil {
			return g
		}
		return xui.BotGroup
	}
	for pass := 0; pass < tagPasses; pass++ {
		rows, err := a.c.Clients(ctx)
		if err != nil {
			return res, err
		}
		seen, left := map[string]bool{}, 0
		todo := map[string]int{} // email -> id
		for _, r := range rows {
			w, ok := want[r.Name]
			sub, ours := strings.CutPrefix(r.Desc, subNotePrefix)
			if !ok || seen[r.Name] || (w.SubID != "" && (!ours || strings.TrimSpace(sub) != w.SubID)) {
				continue
			}
			seen[r.Name] = true
			if g := target(r.Name); r.Group == g || !xui.IsBotGroup(r.Group) {
				left++
				continue
			}
			todo[r.Name] = r.ID
			a.idx.put(r.Name, r.ID)
		}
		if pass == 0 {
			res.Grouped = left
		}
		res.Missing = len(want) - len(seen)
		if len(todo) == 0 {
			res.Converged = true
			return res, nil
		}
		for _, email := range slices.Sorted(maps.Keys(todo)) {
			r, found, err := a.c.Client(ctx, todo[email])
			if err != nil {
				return res, err
			}
			if !found || r.Str("name") != email || !xui.IsBotGroup(r.Str("group")) {
				continue // gone or changed meanwhile: the next listing tells
			}
			r.Set("group", target(email))
			if err := a.edit(ctx, r, false); err != nil {
				return res, err
			}
			res.Tagged++
		}
	}
	return res, nil
}
