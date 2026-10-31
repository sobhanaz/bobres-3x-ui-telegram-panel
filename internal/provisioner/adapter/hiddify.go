package adapter

import (
	"context"
	"crypto/sha1" //nolint:gosec // a name-based (version 5) UUID is SHA-1 by definition; nothing secret rests on it
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/hiddify"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/xui"
)

// HiddifyInboundID is the one inbound a Hiddify server lists: a Hiddify user
// gets every proxy the panel serves, so there is nothing to choose.
const HiddifyInboundID = 1

const (
	oneGiB = 1 << 30
	// hiddifyUnlimitedGB is the largest quota the panel keeps (it caps a larger
	// one there): BOBRES's "unlimited" is written as it and read back as 0.
	hiddifyUnlimitedGB = 1_000_000
	// hiddifyUnlimitedDays stands for "no expiry" (the panel has none): the most
	// days its own dashboard takes. A package this long or longer reads as 0.
	hiddifyUnlimitedDays int64 = 10_000
)

// hiddifyNS is the namespace of the UUIDs BOBRES gives its Hiddify users.
var hiddifyNS = [16]byte{0x6b, 0x0b, 0x2e, 0x47, 0x3c, 0x9a, 0x4f, 0x51, 0x9d, 0x0e, 0x5a, 0x63, 0xb1, 0x7c, 0x20, 0x8e}

// HiddifyUserUUID is the UUID of the Hiddify user BOBRES makes for a derived
// subscription id: a name-based UUID (version 5) of it, so a retried create
// meets the user an earlier attempt made, and a client made again after an
// operator deleted it gets the same UUID, which is in the customer's links.
func HiddifyUserUUID(subID string) string {
	h := sha1.New() //nolint:gosec // see the import
	h.Write(hiddifyNS[:])
	h.Write([]byte("bobres-sub:" + subID))
	b := h.Sum(nil)[:16]
	b[6] = b[6]&0x0f | 0x50
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// Hiddify implements PanelAdapter against a Hiddify Manager panel, through its
// API v2 and an admin's API key (the admin's UUID). Checked against a real
// v14.0.5 panel (CI job "Hiddify integration", not a required check: see
// .github/workflows/ci.yml).
//
// What it absorbs: a Hiddify user is addressed by its UUID, which is also its
// secret (it is in its share and subscription links), and found by name only in
// a listing of all users (a nameIndex remembers the UUIDs); BOBRES's client is a
// user named by the client's email, with the UUID HiddifyUserUUID gives and the
// derived subscription id in its comment ("bobres-sub:<id>"). The panel counts
// traffic in GiB (exact for any byte count) and has no "unlimited": BOBRES's 0
// is its largest quota. It counts expiry in whole days from a start date, by the
// panel's own calendar (UTC in its Docker image): an expiry is written as the
// last day the client may connect and read back as midnight (UTC) after it, and
// "start on first use" is a package with no start date. Every user gets every
// proxy (one inbound, HiddifyInboundID); there are no client groups (the panel's
// tags are not used), so it is neither a GroupedCreator nor a GroupRegrouper.
// The panel owns the subscription address (SubscriptionLinker).
type Hiddify struct {
	c   *hiddify.Client
	idx *nameIndex[string]
	now func() time.Time

	mu         sync.Mutex
	clientPath string
}

var (
	_ PanelAdapter       = (*Hiddify)(nil)
	_ SubscriptionLinker = (*Hiddify)(nil)
)

// NewHiddify wraps the panel address with the admin proxy path and an admin's
// UUID.
func NewHiddify(baseURL, apiKey string, opts ...xui.Option) (*Hiddify, error) {
	c, err := hiddify.New(baseURL, apiKey, opts...)
	if err != nil {
		return nil, err
	}
	return &Hiddify{c: c, idx: newNameIndex[string](), now: time.Now}, nil
}

func hiddifySubOf(u *hiddify.User) (string, bool) {
	id, ok := strings.CutPrefix(u.CommentText(), subNotePrefix)
	return strings.TrimSpace(id), ok
}

// ours reports a user BOBRES made: its comment names a subscription id whose
// UUID is the user's.
func hiddifyOurs(u *hiddify.User) bool {
	sub, ok := hiddifySubOf(u)
	return ok && strings.EqualFold(HiddifyUserUUID(sub), u.UUID)
}

// list is every user name with its UUID. Names need not be unique on the panel;
// for a name more than one user has, the one BOBRES made wins.
func (a *Hiddify) list(ctx context.Context) (map[string]string, error) {
	users, err := a.c.Users(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(users))
	ourName := map[string]bool{}
	for i := range users {
		u := &users[i]
		if u.Deleted != nil && *u.Deleted {
			continue
		}
		if _, dup := out[u.Name]; dup && (ourName[u.Name] || !hiddifyOurs(u)) {
			continue
		}
		out[u.Name] = strings.ToLower(u.UUID)
		ourName[u.Name] = hiddifyOurs(u)
	}
	return out, nil
}

// user reads the user named email; a *hiddify.NotFoundError when the panel has
// none.
func (a *Hiddify) user(ctx context.Context, email string) (*hiddify.User, error) {
	if id, ok := a.idx.get(email); ok {
		looked := a.now()
		u, err := a.c.User(ctx, id)
		switch {
		case err == nil && u.Name == email:
			return u, nil
		case err != nil && !hiddify.IsNotFound(err):
			return nil, err
		}
		a.idx.forget(email)
		return a.lookup(ctx, email, looked)
	}
	return a.lookup(ctx, email, time.Time{})
}

func (a *Hiddify) lookup(ctx context.Context, email string, since time.Time) (*hiddify.User, error) {
	id, ok, err := a.idx.refresh(ctx, email, since, a.list)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, &hiddify.NotFoundError{Name: email}
	}
	u, err := a.c.User(ctx, id)
	if hiddify.IsNotFound(err) || (err == nil && u.Name != email) {
		a.idx.forget(email)
		return nil, &hiddify.NotFoundError{Name: email}
	}
	return u, err
}

func gib(bytes int64) float64 { return float64(bytes) / oneGiB }

func bytesOf(gb *float64) int64 {
	if gb == nil || *gb <= 0 || math.IsNaN(*gb) {
		return 0
	}
	return int64(math.Round(*gb * oneGiB))
}

// hiddifyQuota is the panel's quota for BOBRES's (0 = unlimited).
func hiddifyQuota(totalBytes int64) float64 {
	if totalBytes <= 0 {
		return hiddifyUnlimitedGB
	}
	return min(gib(totalBytes), hiddifyUnlimitedGB)
}

func utcDay(t time.Time) time.Time {
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

const dateLayout = "2006-01-02"

// hiddifyExpiry is the package (start date and days) for BOBRES's expiry.
func (a *Hiddify) hiddifyExpiry(expiryMs int64) map[string]any {
	today := utcDay(a.now())
	switch {
	case expiryMs > 0:
		last := utcDay(time.UnixMilli(expiryMs - 1)) // the last day the client may connect
		days := int64(last.Sub(today) / (24 * time.Hour))
		if days < 0 { // over already: a package that ended that day
			return map[string]any{"start_date": last.Format(dateLayout), "package_days": int64(0)}
		}
		return map[string]any{"start_date": today.Format(dateLayout), "package_days": min(days, hiddifyUnlimitedDays-1)}
	case expiryMs < 0:
		days := (-expiryMs + dayMs - 1) / dayMs
		return map[string]any{"start_date": nil, "package_days": min(max(days, 1), hiddifyUnlimitedDays-1)}
	}
	return map[string]any{"start_date": today.Format(dateLayout), "package_days": hiddifyUnlimitedDays}
}

// hiddifyStatus is the user's limits as the interface counts them.
func hiddifyStatus(u *hiddify.User) ClientStatus {
	st := ClientStatus{UsedBytes: bytesOf(u.CurrentUsageGB), Enabled: u.Enabled()}
	if u.UsageLimitGB != nil && *u.UsageLimitGB < hiddifyUnlimitedGB-0.5 {
		st.TotalBytes = max(bytesOf(u.UsageLimitGB), 1) // a zero quota is not "unlimited"
	}
	var days int64
	if u.PackageDays != nil {
		days = *u.PackageDays
	}
	switch {
	case days >= hiddifyUnlimitedDays:
	case u.StartDate == nil || *u.StartDate == "":
		st.ExpiryMs = -max(days, 1) * dayMs
	default:
		if start, ok := parseHiddifyDate(*u.StartDate); ok {
			st.ExpiryMs = start.AddDate(0, 0, int(days)+1).UnixMilli()
		}
	}
	return st
}

// parseHiddifyDate reads a start date: the API's "2006-01-02", or the forms a
// date takes when a JSON encoder other than the schema's writes it.
func parseHiddifyDate(s string) (time.Time, bool) {
	for _, layout := range []string{dateLayout, time.RFC1123, time.RFC3339, "2006-01-02 15:04:05", "2006-01-02T15:04:05"} {
		if t, err := time.Parse(layout, strings.TrimSpace(s)); err == nil {
			return utcDay(t), true
		}
	}
	return time.Time{}, false
}

func (a *Hiddify) Inbounds(ctx context.Context) ([]Inbound, error) {
	if err := a.c.Me(ctx); err != nil {
		return nil, err
	}
	return []Inbound{{ID: HiddifyInboundID, Tag: "all proxies", Enabled: true, Protocol: "hiddify"}}, nil
}

func (a *Hiddify) CreateClient(ctx context.Context, email, subID string, expiryMs, trafficBytes int64, inboundIDs []int) error {
	if email == "" || len(email) > 512 || strings.TrimSpace(email) != email {
		return errors.New("hiddify: invalid user name")
	}
	for _, id := range inboundIDs {
		if id != HiddifyInboundID {
			return fmt.Errorf("hiddify: inbound %d does not exist", id)
		}
	}
	// Names need not be unique on the panel, but BOBRES finds its users by
	// name: one that someone else has is refused, as other panels do; one of
	// ours (an earlier attempt) is reported the same way and adopted by the
	// caller.
	a.idx.stale()
	switch u, err := a.user(ctx, email); {
	case err == nil && hiddifyOurs(u):
		return &hiddify.APIError{Status: http.StatusConflict, Msg: "the user exists"}
	case err == nil:
		return &hiddify.APIError{Status: http.StatusConflict, Msg: "another user has this name"}
	case !hiddify.IsNotFound(err):
		return err
	}
	id := HiddifyUserUUID(subID)
	body := map[string]any{
		"uuid": id, "name": email, "comment": subNotePrefix + subID, "enable": true, "mode": "no_reset",
		"usage_limit_GB": hiddifyQuota(trafficBytes),
	}
	for k, v := range a.hiddifyExpiry(expiryMs) {
		if v != nil {
			body[k] = v
		}
	}
	if _, err := a.c.CreateUser(ctx, body); err != nil {
		return err
	}
	a.idx.put(email, id)
	return nil
}

func (a *Hiddify) Renew(ctx context.Context, email string, addDays, addBytes int64) error {
	u, err := a.user(ctx, email)
	if err != nil {
		return err
	}
	st := hiddifyStatus(u)
	body := map[string]any{}
	if addDays != 0 {
		if st.ExpiryMs == 0 {
			return fmt.Errorf("renew skipped for %s: unlimited expiry", email)
		}
		var days int64
		if u.PackageDays != nil {
			days = *u.PackageDays
		}
		body["package_days"] = min(max(days+addDays, 0), hiddifyUnlimitedDays-1)
	}
	if addBytes != 0 {
		if st.TotalBytes == 0 {
			return fmt.Errorf("renew skipped for %s: unlimited traffic", email)
		}
		body["usage_limit_GB"] = hiddifyQuota(max(st.TotalBytes+addBytes, 1))
	}
	if len(body) == 0 {
		return nil
	}
	_, err = a.c.PatchUser(ctx, u.UUID, body)
	return err
}

func (a *Hiddify) SetLimits(ctx context.Context, email string, expiryMs, totalBytes int64) error {
	if expiryMs < 0 || totalBytes < 0 {
		return errors.New("hiddify: SetLimits needs limits >= 0")
	}
	u, err := a.user(ctx, email)
	if err != nil {
		return err
	}
	body := a.hiddifyExpiry(expiryMs)
	body["usage_limit_GB"] = hiddifyQuota(totalBytes)
	body["enable"] = true
	_, err = a.c.PatchUser(ctx, u.UUID, body)
	return err
}

func (a *Hiddify) SetEnabled(ctx context.Context, email string, enabled bool) error {
	u, err := a.user(ctx, email)
	if err != nil {
		return err
	}
	if u.Enabled() == enabled {
		return nil
	}
	_, err = a.c.PatchUser(ctx, u.UUID, map[string]any{"enable": enabled})
	return err
}

func (a *Hiddify) Status(ctx context.Context, email string) (ClientStatus, error) {
	u, err := a.user(ctx, email)
	if err != nil {
		return ClientStatus{}, err
	}
	return hiddifyStatus(u), nil
}

func (a *Hiddify) Delete(ctx context.Context, email string) error {
	u, err := a.user(ctx, email)
	if err != nil {
		return err
	}
	if err := a.c.DeleteUser(ctx, u.UUID); err != nil {
		return err
	}
	a.idx.forget(email)
	return nil
}

// ResetTraffic zeroes the user's usage. Its switch stays as it is; a user over
// its quota connects again (the panel only counted it out).
func (a *Hiddify) ResetTraffic(ctx context.Context, email string) error {
	u, err := a.user(ctx, email)
	if err != nil {
		return err
	}
	_, err = a.c.PatchUser(ctx, u.UUID, map[string]any{"current_usage_GB": 0})
	return err
}

func (a *Hiddify) Usage(ctx context.Context, email string) (int64, error) {
	u, err := a.user(ctx, email)
	if err != nil {
		return 0, err
	}
	return bytesOf(u.CurrentUsageGB), nil
}

// path is the panel's client proxy path, asked once and kept.
func (a *Hiddify) path(ctx context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.clientPath != "" {
		return a.clientPath, nil
	}
	p, err := a.c.ClientPath(ctx)
	if err != nil {
		return "", err
	}
	a.clientPath = p
	return p, nil
}

func (a *Hiddify) configs(ctx context.Context, email string) (*hiddify.User, []hiddify.Config, error) {
	u, err := a.user(ctx, email)
	if err != nil {
		return nil, nil, err
	}
	p, err := a.path(ctx)
	if err != nil {
		return nil, nil, err
	}
	cfgs, err := a.c.UserConfigs(ctx, p, u.UUID)
	return u, cfgs, err
}

// Links are the user's share links, one per proxy and domain.
func (a *Hiddify) Links(ctx context.Context, email string) ([]string, error) {
	_, cfgs, err := a.configs(ctx, email)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, c := range cfgs {
		if c.Category != "subscription" && strings.Contains(c.Link, "://") {
			out = append(out, c.Link)
		}
	}
	return out, nil
}

// SubLinks is not offered: the panel serves a subscription by the user's UUID.
func (a *Hiddify) SubLinks(context.Context, string) ([]string, error) {
	return nil, errors.New("hiddify: subscriptions are addressed by the user's UUID")
}

// ClientSubID reads the derived id BOBRES left in the user's comment; a user it
// did not create has none ("").
func (a *Hiddify) ClientSubID(ctx context.Context, email string) (string, error) {
	u, err := a.user(ctx, email)
	if err != nil {
		return "", err
	}
	if !hiddifyOurs(u) {
		return "", nil
	}
	sub, _ := hiddifySubOf(u)
	return sub, nil
}

// SubscriptionLink is the user's automatic subscription (the panel's "Auto"
// entry: it answers each app in its own format). subBase, when set, is the
// address with the client proxy path (https://sub.example.com/<client path>)
// and replaces the panel's.
func (a *Hiddify) SubscriptionLink(ctx context.Context, email, subBase string) (string, error) {
	if subBase != "" {
		u, err := a.user(ctx, email)
		if err != nil {
			return "", err
		}
		return strings.TrimRight(subBase, "/") + "/" + strings.ToLower(u.UUID) + "/auto/", nil
	}
	u, cfgs, err := a.configs(ctx, email)
	if err != nil {
		return "", err
	}
	for _, c := range cfgs {
		if c.Category != "subscription" || !strings.EqualFold(c.Name, "auto") {
			continue
		}
		l, err := url.Parse(c.Link)
		if err != nil || l.Host == "" {
			break
		}
		l.RawQuery = "" // asn: the network BOBRES asked from, not the customer's
		return l.String(), nil
	}
	p, err := a.path(ctx)
	if err != nil {
		return "", err
	}
	return a.c.Origin() + p + "/" + strings.ToLower(u.UUID) + "/auto/", nil
}

func (a *Hiddify) HealthCheck(ctx context.Context) (string, error) {
	if err := a.c.Me(ctx); err != nil {
		return "", fmt.Errorf("health: %w", err)
	}
	return a.PanelVersion(ctx), nil
}

// PanelVersion is what the panel reports, or "" when it does not say.
func (a *Hiddify) PanelVersion(ctx context.Context) string {
	v, err := a.c.Version(ctx)
	if err != nil {
		return ""
	}
	if v = NormalizeVersion(v); v == UnknownVersion {
		return ""
	}
	return v
}
