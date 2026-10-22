// Package adapter abstracts the 3x-ui panel behind PanelAdapter so the
// provisioner never hardcodes a panel version; older panels get their own
// adapters later.
package adapter

import (
	"context"
	"regexp"
	"strconv"
	"strings"
)

// PanelAdapter is the 3x-ui-facing contract (latest v3.x semantics).
type PanelAdapter interface {
	// CreateClient adds a client to all listed inbound IDs. expiryMs is a unix
	// millisecond deadline; 0 means unlimited. trafficBytes 0 = unlimited.
	CreateClient(ctx context.Context, email, subID string, expiryMs, trafficBytes int64, inboundIDs []int) error
	// Renew extends expiry and/or traffic.
	Renew(ctx context.Context, email string, addDays, addBytes int64) error
	// SetLimits sets absolute limits (expiryMs unix ms, totalBytes; 0 =
	// unlimited) and enables the client, so a retry is harmless.
	SetLimits(ctx context.Context, email string, expiryMs, totalBytes int64) error
	// SetEnabled turns the client off (it cannot connect) or on; its limits
	// and usage stay.
	SetEnabled(ctx context.Context, email string, enabled bool) error
	// Status reports a client's usage and current limits.
	Status(ctx context.Context, email string) (ClientStatus, error)
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
	// HealthCheck pings the panel and reports its version (see PanelVersion).
	HealthCheck(ctx context.Context) (version string, err error)
	// PanelVersion is the panel's version, "v3.9.0", or UnknownVersion when
	// the panel does not say. It never fails: the version is informational.
	PanelVersion(ctx context.Context) string
}

// GroupTagger is implemented by adapters whose panel has client groups (3x-ui,
// as tested from v3.8.5 to v3.10.2; Marzban has none). The group is a label the panel shows and
// filters on, nothing more: the adapter puts every client it creates in the bot
// group (xui.BotGroup), and these calls bring the clients made before that into
// it. An adapter that is not a GroupTagger has nothing to tag.
type GroupTagger interface {
	// TagUngrouped puts the clients of managed (email to subscription id, "" for
	// any) that are on the panel under that subscription id and in NO group into
	// the bot group. A client in some other group (an operator put it there) and
	// any client not in managed are left alone, so a repeat changes nothing. It
	// lists the panel again after tagging and reports Converged when that listing
	// shows nothing left to tag (a client deleted meanwhile can move the paged
	// listing and hide another one from the first).
	TagUngrouped(ctx context.Context, managed map[string]string) (TagResult, error)
	// EnsureTagged puts one client in the bot group unless it is in a group.
	EnsureTagged(ctx context.Context, email string) error
}

// TagResult says what TagUngrouped found and did.
type TagResult struct {
	Managed   int  // clients asked about
	Tagged    int  // put in the bot group by this call
	Grouped   int  // already in a group (the bot's or an operator's) at the first listing: left alone
	Missing   int  // not on the panel, or on it under another subscription id
	Converged bool // the last listing found no client left to tag
}

// UnknownVersion stands for a v3 panel that did not report its version.
const UnknownVersion = "v3.x"

// The panel versions CI tests the adapter against (.github/workflows/ci.yml,
// job xui), both inclusive. Move them with that matrix.
const (
	TestedMinVersion = "v3.8.5"
	TestedMaxVersion = "v3.10.2"
)

var semver = regexp.MustCompile(`^v?(\d{1,4})\.(\d{1,4})\.(\d{1,4})$`)

// NormalizeVersion turns what the panel reports into "vX.Y.Z" (anything
// else, such as "dev+1a2b3c4d", is kept as is, bounded).
func NormalizeVersion(v string) string {
	v = strings.TrimSpace(v)
	switch {
	case v == "":
		return UnknownVersion
	case semver.MatchString(v):
		return "v" + strings.TrimPrefix(v, "v")
	case len(v) > 32:
		return v[:32]
	}
	return v
}

// Untested reports a version outside the tested range. An unknown version
// is not flagged: the panel answered the v3 API.
func Untested(v string) bool {
	if v == UnknownVersion || v == "" {
		return false
	}
	n, ok := versionNumber(v)
	if !ok {
		return true
	}
	lo, _ := versionNumber(TestedMinVersion)
	hi, _ := versionNumber(TestedMaxVersion)
	return n < lo || n > hi
}

// versionNumber orders "vX.Y.Z" as one number (each part below 10000).
func versionNumber(v string) (int64, bool) {
	m := semver.FindStringSubmatch(v)
	if m == nil {
		return 0, false
	}
	var n int64
	for _, p := range m[1:] {
		d, _ := strconv.ParseInt(p, 10, 64)
		n = n*10000 + d
	}
	return n, true
}

// ClientStatus is a client's usage and limits as the panel holds them.
type ClientStatus struct {
	UsedBytes  int64
	TotalBytes int64 // 0 = unlimited
	ExpiryMs   int64 // unix ms; 0 = unlimited, negative = starts on first use
	Enabled    bool
}

// Inbound is one panel inbound with its enable flag.
type Inbound struct {
	ID       int
	Tag      string
	Enabled  bool
	Protocol string
	Port     int
}
