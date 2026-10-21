package domain

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/license"
)

// The client of the license server's signed admin API (internal/licensesrv, docs/13-license-server.md). Core signs
// each request with the admin key the owner stored (the private half; the server holds only the public half) using
// the same code the server verifies with (internal/license/adminsig.go).

// VendorLicenseInfo is a license as the server describes it. Key is set only in the answer that made it (or
// re-keyed it); Replayed says the request's ref had been used before and nothing changed.
type VendorLicenseInfo struct {
	ID           string     `json:"id"`
	Customer     string     `json:"customer"`
	Tier         string     `json:"tier"`
	Status       string     `json:"status"`
	EndsAt       *time.Time `json:"ends_at"`
	CreatedAt    time.Time  `json:"created_at"`
	Reason       string     `json:"reason,omitempty"`
	ActiveDomain string     `json:"active_domain,omitempty"`
	LastSeenAt   *time.Time `json:"last_seen_at,omitempty"`
	GraceDays    int        `json:"grace_days,omitempty"`
	Moves        int        `json:"moves,omitempty"`
	Key          string     `json:"key,omitempty"`
	Replayed     bool       `json:"replayed,omitempty"`
	// Detail (Get only): what the license grants, where it ran, and its audit trail.
	Entitlements json.RawMessage    `json:"entitlements,omitempty"`
	Activations  []VendorActivation `json:"activations,omitempty"`
	Audit        []VendorAudit      `json:"audit,omitempty"`
}

// VendorActivation is where a license ran (or runs).
type VendorActivation struct {
	InstallID     string     `json:"install_id"`
	Domain        string     `json:"domain"`
	Counted       bool       `json:"counted"`
	ActivatedAt   time.Time  `json:"activated_at"`
	LastSeenAt    time.Time  `json:"last_seen_at"`
	Version       string     `json:"version,omitempty"`
	DeactivatedAt *time.Time `json:"deactivated_at,omitempty"`
}

// VendorAudit is one entry of a license's audit trail on the license server.
type VendorAudit struct {
	At      time.Time      `json:"at"`
	Actor   string         `json:"actor"`
	Action  string         `json:"action"`
	IP      string         `json:"ip,omitempty"`
	Details map[string]any `json:"details,omitempty"`
}

// VendorCreate is a license made by hand: Tier trial, standard or pro; the end is Months, EndsAt (YYYY-MM-DD) or NoEnd
// (a trial may give none: its preset's days).
type VendorCreate struct {
	Customer string
	Tier     string
	Months   int
	EndsAt   string
	NoEnd    bool
}

// VendorUpdate changes a license: any of the customer, the tier (taking its preset) and the end (a date, or none).
type VendorUpdate struct {
	Customer string
	Tier     string
	EndsAt   string
	NoEnd    bool
}

// VendorError is an answer of the license server that is not a success. A 4xx other than 408 and 429 is final: the
// server looked at the request and refused it, so retrying cannot help. Anything else may pass.
type VendorError struct {
	Status  int
	Code    string
	Message string
}

func (e *VendorError) Error() string {
	return fmt.Sprintf("license server: %d %s: %s", e.Status, e.Code, e.Message)
}

// asVendorError reports whether err is a *VendorError and sets *out to it.
func asVendorError(err error, out **VendorError) bool { return errors.As(err, out) }

// Final reports whether retrying the same request can never succeed.
func (e *VendorError) Final() bool {
	return e.Status >= 400 && e.Status < 500 && e.Status != http.StatusRequestTimeout && e.Status != http.StatusTooManyRequests
}

// VendorClient is what core needs from the license server.
type VendorClient interface {
	Create(ctx context.Context, ref, customer, tier string, months int) (*VendorLicenseInfo, error)
	CreateCustom(ctx context.Context, ref string, in VendorCreate) (*VendorLicenseInfo, error)
	Update(ctx context.Context, ref, id string, in VendorUpdate) (*VendorLicenseInfo, error)
	Extend(ctx context.Context, ref, id string, months int) (*VendorLicenseInfo, error)
	Rekey(ctx context.Context, ref, id string) (*VendorLicenseInfo, error)
	Revoke(ctx context.Context, ref, id, reason string) (*VendorLicenseInfo, error)
	Get(ctx context.Context, id string) (*VendorLicenseInfo, error)
	List(ctx context.Context, status string) ([]VendorLicenseInfo, error)
}

// VendorTransport carries one signed admin request to the license server and brings back its status and body. In
// production it is the bot (core has no internet access; botfiles.Client): the bot posts to the one license server
// it is configured for, whatever the request names. The path is an admin API path ("/admin/v1/licenses/create").
type VendorTransport interface {
	PostVendorAdmin(ctx context.Context, path, ts, nonce, sig string, body []byte) (status int, answer []byte, err error)
}

// DirectVendorTransport posts straight to the license server at base (an https address). It is for tests and for a
// core that does have internet access; it follows no redirect.
func DirectVendorTransport(base string, hc *http.Client) VendorTransport {
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
	}
	c := *hc
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return directVendor{base: strings.TrimRight(base, "/"), hc: &c}
}

type directVendor struct {
	base string
	hc   *http.Client
}

func (d directVendor) PostVendorAdmin(ctx context.Context, path, ts, nonce, sig string, body []byte) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.base+path, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(license.AdminHeaderTS, ts)
	req.Header.Set(license.AdminHeaderNonce, nonce)
	req.Header.Set(license.AdminHeaderSig, sig)
	resp, err := d.hc.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close() //nolint:errcheck // nothing to do about a failed close
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxVendorAnswer))
	return resp.StatusCode, data, err
}

// NewVendorClient builds the signed client of the license server's admin API that reaches it through tr.
func NewVendorClient(tr VendorTransport, key ed25519.PrivateKey) VendorClient {
	return newVendorClient(tr, key, time.Now)
}

func newVendorClient(tr VendorTransport, key ed25519.PrivateKey, now func() time.Time) VendorClient {
	return &vendorHTTP{tr: tr, key: key, now: now}
}

type vendorHTTP struct {
	tr  VendorTransport
	key ed25519.PrivateKey
	now func() time.Time
}

type vendorBody struct {
	Ref      string `json:"ref,omitempty"`
	ID       string `json:"id,omitempty"`
	Customer string `json:"customer,omitempty"`
	Tier     string `json:"tier,omitempty"`
	Months   int    `json:"months,omitempty"`
	Reason   string `json:"reason,omitempty"`
	Status   string `json:"status,omitempty"`
	EndsAt   string `json:"ends_at,omitempty"`
	NoEnd    bool   `json:"no_end,omitempty"`
}

// maxVendorAnswer bounds what a license server's answer may be (a list of 500 licenses is far below it).
const maxVendorAnswer = 1 << 20

func (c *vendorHTTP) do(ctx context.Context, path string, body vendorBody, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	ts, nonce, sig, err := license.AdminSign(c.key, http.MethodPost, path, raw, c.now())
	if err != nil {
		return err
	}
	status, data, err := c.tr.PostVendorAdmin(ctx, path, ts, nonce, sig, raw)
	if err != nil {
		return fmt.Errorf("license server: %w", err)
	}
	if status != http.StatusOK {
		e := &VendorError{Status: status}
		var body struct{ Error, Message string }
		if json.Unmarshal(data, &body) == nil {
			e.Code, e.Message = body.Error, body.Message
		}
		return e
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("license server: the answer is not what was expected: %w", err)
	}
	return nil
}

func (c *vendorHTTP) one(ctx context.Context, path string, b vendorBody) (*VendorLicenseInfo, error) {
	var v VendorLicenseInfo
	if err := c.do(ctx, path, b, &v); err != nil {
		return nil, err
	}
	if v.ID == "" {
		return nil, errors.New("license server: the answer has no license id")
	}
	return &v, nil
}

func (c *vendorHTTP) Create(ctx context.Context, ref, customer, tier string, months int) (*VendorLicenseInfo, error) {
	return c.one(ctx, "/admin/v1/licenses/create", vendorBody{Ref: ref, Customer: customer, Tier: tier, Months: months})
}

func (c *vendorHTTP) CreateCustom(ctx context.Context, ref string, in VendorCreate) (*VendorLicenseInfo, error) {
	return c.one(ctx, "/admin/v1/licenses/create", vendorBody{Ref: ref, Customer: in.Customer, Tier: in.Tier, Months: in.Months, EndsAt: in.EndsAt, NoEnd: in.NoEnd})
}

func (c *vendorHTTP) Update(ctx context.Context, ref, id string, in VendorUpdate) (*VendorLicenseInfo, error) {
	return c.one(ctx, "/admin/v1/licenses/update", vendorBody{Ref: ref, ID: id, Customer: in.Customer, Tier: in.Tier, EndsAt: in.EndsAt, NoEnd: in.NoEnd})
}

func (c *vendorHTTP) Extend(ctx context.Context, ref, id string, months int) (*VendorLicenseInfo, error) {
	return c.one(ctx, "/admin/v1/licenses/extend", vendorBody{Ref: ref, ID: id, Months: months})
}

func (c *vendorHTTP) Rekey(ctx context.Context, ref, id string) (*VendorLicenseInfo, error) {
	return c.one(ctx, "/admin/v1/licenses/rekey", vendorBody{Ref: ref, ID: id})
}

func (c *vendorHTTP) Revoke(ctx context.Context, ref, id, reason string) (*VendorLicenseInfo, error) {
	return c.one(ctx, "/admin/v1/licenses/revoke", vendorBody{Ref: ref, ID: id, Reason: reason})
}

func (c *vendorHTTP) Get(ctx context.Context, id string) (*VendorLicenseInfo, error) {
	return c.one(ctx, "/admin/v1/licenses/get", vendorBody{ID: id})
}

func (c *vendorHTTP) List(ctx context.Context, status string) ([]VendorLicenseInfo, error) {
	var out struct {
		Licenses []VendorLicenseInfo `json:"licenses"`
	}
	if err := c.do(ctx, "/admin/v1/licenses/list", vendorBody{Status: status}, &out); err != nil {
		return nil, err
	}
	return out.Licenses, nil
}
