package store

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
)

// GatewayConfig mirrors payments.gateway_configs. The secrets stay encrypted
// here; the domain holds the master key.
type GatewayConfig struct {
	Provider    string
	Enabled     bool
	Sandbox     bool
	MerchantEnc []byte // nil = no merchant id
	ProxyEnc    []byte // nil = no proxy
	AddressEnc  []byte // usdt_trc20_auto: the store's TRC20 address; nil = none
	APIKeyEnc   []byte // usdt_trc20_auto: the TronGrid API key; nil = none
	UpdatedAt   time.Time
	UpdatedBy   *string // the staff user; nil for the environment seed
}

const gatewayConfigCols = `provider, enabled, sandbox, merchant_enc, proxy_enc, address_enc, api_key_enc, updated_at, updated_by::text`

func scanGatewayConfig(row pgx.Row) (*GatewayConfig, error) {
	var c GatewayConfig
	err := row.Scan(&c.Provider, &c.Enabled, &c.Sandbox, &c.MerchantEnc, &c.ProxyEnc, &c.AddressEnc, &c.APIKeyEnc, &c.UpdatedAt, &c.UpdatedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &c, err
}

// GetGatewayConfig returns a provider's stored settings (ErrNotFound if none).
func (s *Store) GetGatewayConfig(ctx context.Context, tx pgx.Tx, provider string) (*GatewayConfig, error) {
	c, err := scanGatewayConfig(s.q(tx).QueryRow(ctx,
		`SELECT `+gatewayConfigCols+` FROM payments.gateway_configs WHERE provider = $1`, provider))
	if err != nil {
		return nil, fmt.Errorf("get gateway config: %w", err)
	}
	return c, nil
}

// SeedGatewayConfig stores the environment's settings unless the provider
// has settings already (they win from then on). It reports whether it wrote.
func (s *Store) SeedGatewayConfig(ctx context.Context, c *GatewayConfig) (bool, error) {
	tag, err := s.db.Exec(ctx, `
		INSERT INTO payments.gateway_configs (provider, enabled, sandbox, merchant_enc, proxy_enc)
		VALUES ($1, $2, $3, $4, $5) ON CONFLICT (provider) DO NOTHING`,
		c.Provider, c.Enabled, c.Sandbox, c.MerchantEnc, c.ProxyEnc)
	if err != nil {
		return false, fmt.Errorf("seed gateway config: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// SaveGatewayConfig writes a provider's settings whole and returns the row.
func (s *Store) SaveGatewayConfig(ctx context.Context, tx pgx.Tx, c *GatewayConfig) (*GatewayConfig, error) {
	saved, err := scanGatewayConfig(s.q(tx).QueryRow(ctx, `
		INSERT INTO payments.gateway_configs (provider, enabled, sandbox, merchant_enc, proxy_enc, address_enc, api_key_enc,
			updated_at, updated_by)
		VALUES ($1, $2, $3, $4, $5, $7, $8, now(), $6)
		ON CONFLICT (provider) DO UPDATE SET enabled = EXCLUDED.enabled, sandbox = EXCLUDED.sandbox,
			merchant_enc = EXCLUDED.merchant_enc, proxy_enc = EXCLUDED.proxy_enc,
			address_enc = EXCLUDED.address_enc, api_key_enc = EXCLUDED.api_key_enc,
			updated_at = EXCLUDED.updated_at, updated_by = EXCLUDED.updated_by
		RETURNING `+gatewayConfigCols,
		c.Provider, c.Enabled, c.Sandbox, c.MerchantEnc, c.ProxyEnc, c.UpdatedBy, c.AddressEnc, c.APIKeyEnc))
	if err != nil {
		return nil, fmt.Errorf("save gateway config: %w", err)
	}
	return saved, nil
}

// openIntentWhere matches the payments a provider may still settle: pending or
// confirming. For an automated gateway (Zarinpal) these are the reconciler's
// queue: an intent whose creation failed there has no external id (nobody was
// sent to pay it) and is left out, and an expired one stays open until the
// reconciler has asked the gateway. An on-chain payment ($3) likewise stays
// open past its expiry until the watcher, having read the chain past it,
// expires it. Any other intent past its expiry (an unpaid Stars invoice) can
// no longer be paid.
const openIntentWhere = `provider = $1 AND status IN ('pending','confirming')
	AND (external_id IS NOT NULL OR provider <> ALL($2))
	AND (provider = ANY($2) OR provider = ANY($3) OR expires_at IS NULL OR expires_at > now())`

// OpenIntents counts a provider's open payments and returns when the oldest
// was created (nil when there is none).
func (s *Store) OpenIntents(ctx context.Context, tx pgx.Tx, provider string) (int64, *time.Time, error) {
	var (
		n      int64
		oldest *time.Time
	)
	if err := s.q(tx).QueryRow(ctx, `
		SELECT count(*), min(created_at) FROM payments.payment_intents WHERE `+openIntentWhere,
		provider, GatewayProviders, ChainProviders).Scan(&n, &oldest); err != nil {
		return 0, nil, fmt.Errorf("open intents: %w", err)
	}
	return n, oldest, nil
}

// Money is an amount in one currency.
type Money struct {
	Amount   int64
	Currency string
}

// StatusTotal is the payments of one status in a report.
type StatusTotal struct {
	Status string
	Count  int64
	// Amounts sums what was charged, per currency; GatewayAmounts what the
	// gateway was asked for (Rial for Zarinpal, Stars), per currency.
	Amounts        []Money
	GatewayAmounts []Money
}

// ReasonCount is how many payments failed for one reason.
type ReasonCount struct {
	Reason string
	Count  int64
}

// GatewayEvent is one entry of the gateway audit trail, as reported to staff.
type GatewayEvent struct {
	At       time.Time
	Kind     string
	IntentID string
	Detail   string // the error, for an error event (no address or secret)
}

// GatewayReport summarises a provider's payments created in [From, To) and
// what is open now.
type GatewayReport struct {
	Provider   string
	From, To   time.Time
	Totals     []StatusTotal // by status
	Failures   []ReasonCount // failed or expired, by reason, most frequent first
	OpenCount  int64
	OldestOpen *time.Time
	LastError  *GatewayEvent // not a failed check: those are CheckErrors
	LastPaid   *GatewayEvent
	// CheckErrors are the failed payment checks (the gateway could not be
	// asked, or refused) kept in [From, To), newest first, at most
	// MaxCheckErrors; CheckErrorCount is how many there are. Each intent's
	// are kept at most once an hour.
	CheckErrors     []GatewayEvent
	CheckErrorCount int64
	Chain           *ChainReport // usdt_trc20_auto only
}

// MaxCheckErrors bounds the failed checks a report lists.
const MaxCheckErrors = 20

// GatewayReport builds the report for one provider.
func (s *Store) GatewayReport(ctx context.Context, provider string, from, to time.Time) (*GatewayReport, error) {
	r := &GatewayReport{Provider: provider, From: from, To: to}
	rows, err := s.db.Query(ctx, `
		SELECT status, currency, COALESCE(gateway_currency, ''), count(*),
		       COALESCE(sum(amount), 0)::bigint, COALESCE(sum(gateway_amount), 0)::bigint
		FROM payments.payment_intents
		WHERE provider = $1 AND created_at >= $2 AND created_at < $3
		GROUP BY status, currency, gateway_currency
		ORDER BY status, currency, gateway_currency`, provider, from, to)
	if err != nil {
		return nil, fmt.Errorf("gateway report: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			status, cur, gcur string
			n, sum, gsum      int64
		)
		if err := rows.Scan(&status, &cur, &gcur, &n, &sum, &gsum); err != nil {
			return nil, fmt.Errorf("gateway report: %w", err)
		}
		if len(r.Totals) == 0 || r.Totals[len(r.Totals)-1].Status != status {
			r.Totals = append(r.Totals, StatusTotal{Status: status})
		}
		t := &r.Totals[len(r.Totals)-1]
		t.Count += n
		t.Amounts = addMoney(t.Amounts, Money{Amount: sum, Currency: cur})
		if gcur != "" {
			t.GatewayAmounts = addMoney(t.GatewayAmounts, Money{Amount: gsum, Currency: gcur})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("gateway report: %w", err)
	}

	reasons, err := s.db.Query(ctx, `
		SELECT failure_reason, count(*) FROM payments.payment_intents
		WHERE provider = $1 AND created_at >= $2 AND created_at < $3
		  AND status IN ('failed','expired') AND failure_reason IS NOT NULL
		GROUP BY failure_reason ORDER BY count(*) DESC, failure_reason`, provider, from, to)
	if err != nil {
		return nil, fmt.Errorf("gateway report: %w", err)
	}
	defer reasons.Close()
	for reasons.Next() {
		var rc ReasonCount
		if err := reasons.Scan(&rc.Reason, &rc.Count); err != nil {
			return nil, fmt.Errorf("gateway report: %w", err)
		}
		r.Failures = append(r.Failures, rc)
	}
	if err := reasons.Err(); err != nil {
		return nil, fmt.Errorf("gateway report: %w", err)
	}

	if r.OpenCount, r.OldestOpen, err = s.OpenIntents(ctx, nil, provider); err != nil {
		return nil, err
	}
	if r.LastError, err = s.lastGatewayEvent(ctx, provider, "error"); err != nil {
		return nil, err
	}
	if r.LastPaid, err = s.lastGatewayEvent(ctx, provider, "paid"); err != nil {
		return nil, err
	}
	if r.CheckErrors, r.CheckErrorCount, err = s.checkErrors(ctx, provider, from, to); err != nil {
		return nil, err
	}
	return r, nil
}

// checkErrors returns the provider's failed checks kept in [from, to), newest
// first (at most MaxCheckErrors), and how many there are.
func (s *Store) checkErrors(ctx context.Context, provider string, from, to time.Time) ([]GatewayEvent, int64, error) {
	rows, err := s.db.Query(ctx, `
		SELECT created_at, kind, COALESCE(intent_id::text, ''), COALESCE(detail->>'error', ''), count(*) OVER ()
		FROM payments.gateway_events
		WHERE provider = $1 AND kind = 'check' AND outcome = 'error' AND created_at >= $2 AND created_at < $3
		ORDER BY id DESC LIMIT $4`, provider, from, to, MaxCheckErrors)
	if err != nil {
		return nil, 0, fmt.Errorf("check errors: %w", err)
	}
	defer rows.Close()
	var (
		out   []GatewayEvent
		total int64
	)
	for rows.Next() {
		var e GatewayEvent
		if err := rows.Scan(&e.At, &e.Kind, &e.IntentID, &e.Detail, &total); err != nil {
			return nil, 0, fmt.Errorf("check errors: %w", err)
		}
		e.Detail = addressRe.ReplaceAllString(e.Detail, "[address]")
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("check errors: %w", err)
	}
	return out, total, nil
}

// addMoney adds m to the total of its currency.
func addMoney(list []Money, m Money) []Money {
	for i := range list {
		if list[i].Currency == m.Currency {
			list[i].Amount += m.Amount
			return list
		}
	}
	return append(list, m)
}

// addressRe matches host:port and [ip]:port: events written before gateway
// errors were scrubbed at the source could name the proxy's address.
var addressRe = regexp.MustCompile(`(\[[0-9A-Fa-f:.]+\]|[A-Za-z0-9.-]+):[0-9]{1,5}\b`)

// lastGatewayEvent returns the provider's latest event with this outcome. A
// failed check is not the gateway's last error: core's health and alerts read
// that, and one timed-out or rate-limited poll (the next one retries) would
// mark the gateway unhealthy, and alert, for an hour. The report lists failed
// checks apart (checkErrors).
func (s *Store) lastGatewayEvent(ctx context.Context, provider, outcome string) (*GatewayEvent, error) {
	var e GatewayEvent
	err := s.db.QueryRow(ctx, `
		SELECT created_at, kind, COALESCE(intent_id::text, ''), COALESCE(detail->>'error', '')
		FROM payments.gateway_events
		WHERE provider = $1 AND outcome = $2 AND NOT (kind = 'check' AND outcome = 'error')
		ORDER BY id DESC LIMIT 1`, provider, outcome).Scan(&e.At, &e.Kind, &e.IntentID, &e.Detail)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("last gateway event: %w", err)
	}
	e.Detail = addressRe.ReplaceAllString(e.Detail, "[address]")
	return &e, nil
}
