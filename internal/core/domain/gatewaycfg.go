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
// id and proxy, and automatic USDT's address, TronGrid API key and proxy,
// encrypted under its own key; core forwards what the owner types and audits
// that something changed, never a secret (the USDT address is public:
// customers are shown it, so the audit keeps it). The manual methods and
// Telegram Stars are switched on and off with settings
// (payments.<method>.enabled), checked here.

// GatewayAdmin is core's view of payments' gateway settings.
type GatewayAdmin interface {
	GatewayConfigs(ctx context.Context) ([]GatewayConfig, error)
	SetGatewayConfig(ctx context.Context, u GatewayUpdate, actorID string) (*GatewayConfig, error)
	// TestGateway checks the stored settings (nil candidate) or candidate
	// ones with a read-only call; nothing is stored.
	TestGateway(ctx context.Context, provider string, c *GatewayCandidate) (*GatewayTest, error)
	GatewayReport(ctx context.Context, provider string, from, to int64) (*GatewayReport, error)
	// ChainTransfers is automatic USDT's transfer log, newest first, with the
	// number of matches (page from 1).
	ChainTransfers(ctx context.Context, f ChainTransferFilter, page, size int) ([]ChainTransfer, int64, error)
	// ResolveChainTransfer marks an unmatched deposit handled by staff.
	ResolveChainTransfer(ctx context.Context, txid string, index int, actorID, note string) (*ChainTransfer, error)
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
	Address      string // automatic USDT: the store's TRON address (public)
	APIKeySet    bool   // automatic USDT: a TronGrid API key is set
	ProxySet     bool
	KeyMissing   bool   // payments has no master key: read only
	FromEnv      bool   // the settings come from the environment
	Problem      string // "" | unreadable | sandbox_refused | public_url | invalid
	UpdatedAt    int64
	UpdatedBy    string
}

// GatewayUpdate replaces a gateway's settings. MerchantID, Proxy and APIKey
// are write-only, and they and Address keep the stored value when "".
type GatewayUpdate struct {
	Provider          string
	Enabled, Sandbox  bool
	MerchantID, Proxy string
	ClearProxy        bool
	// Automatic USDT: the store's TRON address and TronGrid's API key.
	Address, APIKey string
	ClearAPIKey     bool
}

// GatewayCandidate is settings to test before saving them ("" = stored).
type GatewayCandidate struct {
	Sandbox           bool
	MerchantID, Proxy string
	ClearProxy        bool
	Address, APIKey   string // automatic USDT
	ClearAPIKey       bool   // automatic USDT
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
	// The failed payment checks (Zarinpal) kept in the period, newest first,
	// at most 20 (each payment's at most once an hour), and how many there are.
	CheckErrors     []GatewayEvent
	CheckErrorCount int64
	// Automatic USDT only: the deposits staff must still credit by hand
	// (whatever the period; newest first, at most 50) and how many there
	// are, and the watcher's reads of the store's address (unix seconds, 0 =
	// never; ScanError is the last read's error, "" when it worked).
	Unmatched                []ChainTransfer
	UnmatchedCount           int64
	LastScanAt, LastScanOKAt int64
	ScanError                string
}

// chainReadGrace: automatic USDT is unhealthy once its reads of the address
// have failed for this long (the watcher reads every 30 seconds; TronGrid
// refuses one now and then).
const chainReadGrace = 5 * time.Minute

// Healthy: payments go through. No error so far, a payment went through
// since the last error, or that error is over an hour old. Automatic USDT:
// the watcher can read the chain (its last read worked, or one worked within
// the last 5 minutes).
func (r *GatewayReport) Healthy(now time.Time) bool {
	if r.Provider == USDTAuto {
		return r.ScanError == "" || (r.LastScanOKAt > 0 && now.Sub(time.Unix(r.LastScanOKAt, 0)) <= chainReadGrace)
	}
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
// when it has none (Zarinpal and automatic USDT are switched in payments).
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

// gatewayAudit is what an audit entry keeps of a gateway's settings: the
// switches, and automatic USDT's address (public, and who changed where
// customers' money goes matters most).
func gatewayAudit(c *GatewayConfig) map[string]any {
	if c.Provider == USDTAuto {
		return map[string]any{"enabled": c.Enabled, "address": c.Address}
	}
	return map[string]any{"enabled": c.Enabled, "sandbox": c.Sandbox}
}

// sameAudit: two audit snapshots say the same (their values are strings and
// bools).
func sameAudit(a, b map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

// checkProviderFields checks the fields only one provider has: automatic
// USDT's address and API key (normalised), Zarinpal's merchant id and
// sandbox.
func checkProviderFields(provider, address, apiKey string, clearKey bool, merchant string, sandbox bool) (string, string, error) {
	if provider != USDTAuto {
		switch {
		case normalizeTronAddress(address) != "":
			return "", "", &FieldError{"address", "only automatic USDT has an address"}
		case strings.TrimSpace(apiKey) != "" || clearKey:
			return "", "", &FieldError{"api_key", "only automatic USDT has an API key"}
		}
		return "", "", nil
	}
	switch {
	case merchant != "":
		return "", "", &FieldError{"merchant_id", "automatic USDT has no merchant id"}
	case sandbox:
		return "", "", &FieldError{"sandbox", "automatic USDT has no sandbox"}
	}
	return checkChainSecrets(address, apiKey, clearKey)
}

// SetGatewayConfig saves a gateway's settings in payments and audits the
// change: the switches (and automatic USDT's address) before and after, and
// "changed" or "cleared" for a secret that was sent (never its value).
func (s *Service) SetGatewayConfig(ctx context.Context, actor *store.User, ga GatewayAdmin, u GatewayUpdate) (*GatewayConfig, error) {
	var err error
	u.Provider = strings.ToLower(strings.TrimSpace(u.Provider))
	if u.MerchantID, u.Proxy, err = checkGatewaySecrets(u.MerchantID, u.Proxy, u.ClearProxy); err != nil {
		return nil, err
	}
	if u.Address, u.APIKey, err = checkProviderFields(u.Provider, u.Address, u.APIKey, u.ClearAPIKey, u.MerchantID, u.Sandbox); err != nil {
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
	switch {
	case u.ClearAPIKey && before.APIKeySet:
		a["api_key"] = "cleared"
	case u.APIKey != "":
		a["api_key"] = "changed"
	}
	if sameAudit(a, b) {
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
		a, k, err := checkProviderFields(provider, c.Address, c.APIKey, c.ClearAPIKey, m, c.Sandbox)
		if err != nil {
			return nil, err
		}
		c = &GatewayCandidate{Sandbox: c.Sandbox, MerchantID: m, Proxy: p, ClearProxy: c.ClearProxy,
			Address: a, APIKey: k, ClearAPIKey: c.ClearAPIKey}
	}
	return ga.TestGateway(ctx, provider, c)
}
