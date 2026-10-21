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
	Key          string     `json:"key,omitempty"`
	Replayed     bool       `json:"replayed,omitempty"`
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

// Final reports whether retrying the same request can never succeed.
func (e *VendorError) Final() bool {
	return e.Status >= 400 && e.Status < 500 && e.Status != http.StatusRequestTimeout && e.Status != http.StatusTooManyRequests
}

// VendorClient is what core needs from the license server.
type VendorClient interface {
	Create(ctx context.Context, ref, customer, tier string, months int) (*VendorLicenseInfo, error)
	Extend(ctx context.Context, ref, id string, months int) (*VendorLicenseInfo, error)
	Rekey(ctx context.Context, ref, id string) (*VendorLicenseInfo, error)
	Revoke(ctx context.Context, ref, id, reason string) (*VendorLicenseInfo, error)
	Get(ctx context.Context, id string) (*VendorLicenseInfo, error)
	List(ctx context.Context, status string) ([]VendorLicenseInfo, error)
}

type vendorHTTP struct {
	base string
	key  ed25519.PrivateKey
	hc   *http.Client
	now  func() time.Time
}

// NewVendorClient builds the signed client for the license server at base (an https address).
func NewVendorClient(base string, key ed25519.PrivateKey, hc *http.Client) VendorClient {
	return newVendorClient(base, key, hc, time.Now)
}

func newVendorClient(base string, key ed25519.PrivateKey, hc *http.Client, now func() time.Time) VendorClient {
	if hc == nil {
		hc = &http.Client{
			Timeout: 20 * time.Second,
			// A redirect would carry a signed request somewhere the owner did not name.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
	}
	return &vendorHTTP{base: strings.TrimRight(base, "/"), key: key, hc: hc, now: now}
}

type vendorBody struct {
	Ref      string `json:"ref,omitempty"`
	ID       string `json:"id,omitempty"`
	Customer string `json:"customer,omitempty"`
	Tier     string `json:"tier,omitempty"`
	Months   int    `json:"months,omitempty"`
	Reason   string `json:"reason,omitempty"`
	Status   string `json:"status,omitempty"`
}

// maxVendorAnswer bounds what a license server's answer may be (a list of 500 licenses is far below it).
const maxVendorAnswer = 1 << 20

func (c *vendorHTTP) do(ctx context.Context, path string, body vendorBody, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	ts, nonce, sig, err := license.AdminSign(c.key, http.MethodPost, req.URL.RequestURI(), raw, c.now())
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(license.AdminHeaderTS, ts)
	req.Header.Set(license.AdminHeaderNonce, nonce)
	req.Header.Set(license.AdminHeaderSig, sig)
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("license server: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // nothing to do about a failed close
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxVendorAnswer))
	if err != nil {
		return fmt.Errorf("license server: reading the answer: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		e := &VendorError{Status: resp.StatusCode}
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
