package domain

import (
	"context"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// Payment gateway settings (milestone 6a). Payments keeps Zarinpal's merchant
// id and proxy encrypted under its own key; core forwards what the owner
// types and audits that something changed, never the values. The manual
// methods and Telegram Stars are switched on and off with settings
// (payments.<method>.enabled), checked here.

// GatewayAdmin is core's view of payments' gateway settings.
type GatewayAdmin interface {
	GatewayConfigs(ctx context.Context) ([]GatewayConfig, error)
	SetGatewayConfig(ctx context.Context, u GatewayUpdate, actorID string) (*GatewayConfig, error)
	// TestGateway checks the stored settings (nil candidate) or candidate
	// ones with a read-only call; nothing is stored.
	TestGateway(ctx context.Context, provider string, c *GatewayCandidate) (*GatewayTest, error)
	GatewayReport(ctx context.Context, provider string, from, to int64) (*GatewayReport, error)
}

// GatewayConfig is an automated gateway's settings as payments reports
// them: whether the secrets are set, never their values.
type GatewayConfig struct {
	Provider     string
	Enabled      bool // the owner's switch
	Configured   bool // has credentials: open payments keep being checked
	Offered      bool // new payments can use it now
	Sandbox      bool
	MerchantHint string // the merchant id's last 4 characters
	ProxySet     bool
	KeyMissing   bool   // payments has no master key: read only
	FromEnv      bool   // the settings come from the environment
	Problem      string // "" | unreadable | sandbox_refused | public_url | invalid
	UpdatedAt    int64
	UpdatedBy    string
}

// GatewayUpdate replaces a gateway's settings. MerchantID and Proxy are
// write-only: "" keeps the stored value.
type GatewayUpdate struct {
	Provider          string
	Enabled, Sandbox  bool
	MerchantID, Proxy string
	ClearProxy        bool
}

// GatewayCandidate is settings to test before saving them ("" = stored).
type GatewayCandidate struct {
	Sandbox           bool
	MerchantID, Proxy string
	ClearProxy        bool
}

// GatewayTest is the result of a gateway test.
type GatewayTest struct {
	OK         bool
	Detail     string // why it failed; never a secret
	Unverified int32  // paid payments the gateway waits for us to verify
	LatencyMS  int64
}

// GatewayEvent is one entry of payments' gateway trail.
type GatewayEvent struct {
	At             int64
	Kind, IntentID string
	Detail         string // already redacted by payments
}

// GatewayStatusTotal is a report's payments of one status.
type GatewayStatusTotal struct {
	Status  string
	Count   int64
	Amounts map[string]int64 // charged, per currency
}

// GatewayReport is one payment method's payments over a period, and its
// current open payments and latest events.
type GatewayReport struct {
	Provider     string
	From, To     int64
	Totals       []GatewayStatusTotal
	OpenCount    int64
	OldestOpenAt int64 // 0 = none open
	LastError    *GatewayEvent
	LastPaid     *GatewayEvent
}

// Healthy: payments go through. No error so far, a payment went through
// since the last error, or that error is over an hour old.
func (r *GatewayReport) Healthy(now time.Time) bool {
	switch {
	case r.LastError == nil:
		return true
	case r.LastPaid != nil && r.LastPaid.At >= r.LastError.At:
		return true
	}
	return now.Sub(time.Unix(r.LastError.At, 0)) > time.Hour
}

// Payment methods that have an on/off setting, and the setting.
var methodToggles = map[string]string{
	"manual_card":     "payments.manual_card.enabled",
	"manual_crypto":   "payments.manual_crypto.enabled",
	"manual_zarinpal": "payments.manual_zarinpal.enabled",
	Stars:             "payments.stars.enabled",
}

// MethodToggle is the setting that switches a payment method on and off; ""
// when it has none (Zarinpal is switched in payments).
func MethodToggle(provider string) string { return methodToggles[provider] }

// methodEnabled: the owner left the method switched on (unset counts as on,
// so installs from before the switch keep working).
func (s *Service) methodEnabled(ctx context.Context, provider string) bool {
	key := methodToggles[provider]
	return key == "" || s.setting(ctx, key) != "false"
}

var merchantRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// normalizeMerchant reads a typed merchant id (either digit set, any case,
// stray spaces).
func normalizeMerchant(v string) string {
	return strings.ToLower(strings.Join(strings.Fields(latinDigits(v)), ""))
}

// checkGatewaySecrets normalises the write-only fields and checks them. The
// messages never repeat a value: a proxy URL can carry a password.
func checkGatewaySecrets(merchant, proxy string, clearProxy bool) (string, string, error) {
	merchant, proxy = normalizeMerchant(merchant), strings.TrimSpace(proxy)
	if merchant != "" && !merchantRe.MatchString(merchant) {
		return "", "", &FieldError{"merchant_id", "the 36-character merchant id from the Zarinpal panel"}
	}
	switch {
	case proxy != "" && clearProxy:
		return "", "", &FieldError{"proxy", "set a proxy or clear it, not both"}
	case proxy != "":
		u, err := url.Parse(proxy)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || hasSpace(proxy) {
			return "", "", &FieldError{"proxy", "an http(s) proxy address, such as http://user:password@203.0.113.10:3128"}
		}
	}
	return merchant, proxy, nil
}

// gatewayAudit is what an audit entry keeps of a gateway's settings.
func gatewayAudit(c *GatewayConfig) map[string]any {
	return map[string]any{"enabled": c.Enabled, "sandbox": c.Sandbox}
}

// SetGatewayConfig saves a gateway's settings in payments and audits the
// change: the switches before and after, and "changed" or "cleared" for a
// secret that was sent (never its value).
func (s *Service) SetGatewayConfig(ctx context.Context, actor *store.User, ga GatewayAdmin, u GatewayUpdate) (*GatewayConfig, error) {
	var err error
	u.Provider = strings.ToLower(strings.TrimSpace(u.Provider))
	if u.MerchantID, u.Proxy, err = checkGatewaySecrets(u.MerchantID, u.Proxy, u.ClearProxy); err != nil {
		return nil, err
	}
	configs, err := ga.GatewayConfigs(ctx)
	if err != nil {
		return nil, err
	}
	var before *GatewayConfig
	for i := range configs {
		if configs[i].Provider == u.Provider {
			before = &configs[i]
		}
	}
	if before == nil {
		return nil, store.ErrNotFound
	}
	if u.Enabled && !before.Enabled {
		if err := s.requireGateway(ctx, u.Provider); err != nil {
			return nil, err
		}
	}
	after, err := ga.SetGatewayConfig(ctx, u, actor.ID)
	if err != nil {
		return nil, err
	}
	b, a := gatewayAudit(before), gatewayAudit(after)
	if u.MerchantID != "" {
		a["merchant_id"] = "changed"
	}
	switch {
	case u.ClearProxy && before.ProxySet:
		a["proxy"] = "cleared"
	case u.Proxy != "":
		a["proxy"] = "changed"
	}
	if len(a) == len(b) && a["enabled"] == b["enabled"] && a["sandbox"] == b["sandbox"] {
		return after, nil // nothing changed
	}
	if err := s.auditChange(ctx, nil, actor, "gateway.update", "gateway", u.Provider, b, a, ""); err != nil {
		return nil, err
	}
	return after, nil
}

// TestGateway checks a gateway's stored settings (nil candidate) or the
// candidate ones, read-only.
func (s *Service) TestGateway(ctx context.Context, ga GatewayAdmin, provider string, c *GatewayCandidate) (*GatewayTest, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if c != nil {
		m, p, err := checkGatewaySecrets(c.MerchantID, c.Proxy, c.ClearProxy)
		if err != nil {
			return nil, err
		}
		c = &GatewayCandidate{Sandbox: c.Sandbox, MerchantID: m, Proxy: p, ClearProxy: c.ClearProxy}
	}
	return ga.TestGateway(ctx, provider, c)
}
