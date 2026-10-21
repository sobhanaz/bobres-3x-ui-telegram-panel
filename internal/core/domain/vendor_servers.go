package domain

import (
	"context"
	"regexp"
	"strings"
	"unicode"

	"github.com/jackc/pgx/v5"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// The license server's own licenses, all of them (the store's own, the ones made by hand with `licensesrv key`, and the
// ones the bot sold), managed from the dashboard through the same signed admin API: look at them with where each
// runs, make one by hand (a gift, a trial, a customer who paid outside the bot), change its customer, tier and end,
// renew it, revoke it, and issue its key again while nobody has activated it. Every change carries a ref made from the
// dashboard's request id, so a double click is one change; a license the store sold is kept in step (tier, end, status).

// serverLicenseIDRe is a license server id; real ones are "lic_" and 16 letters, and the bound keeps a ref under 100.
var serverLicenseIDRe = regexp.MustCompile(`^lic_[a-z0-9_-]{1,36}$`)

func (s *Service) serverClient(ctx context.Context, id string) (VendorClient, error) {
	if !serverLicenseIDRe.MatchString(id) {
		return nil, invalid("not a license id")
	}
	return s.vendorClient(ctx)
}

func checkRequestID(id string) error {
	if !adminRequestRe.MatchString(id) {
		return invalid("request_id is required (8 to 40 letters, digits, . _ -)")
	}
	return nil
}

func cleanCustomer(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 200 || strings.IndexFunc(s, unicode.IsControl) >= 0 {
		return "", &FieldError{"customer", "required, at most 200 characters, no control characters"}
	}
	return s, nil
}

// ListServerLicenses lists the license server's licenses, newest first (status "", active or revoked).
func (s *Service) ListServerLicenses(ctx context.Context, status string) ([]VendorLicenseInfo, error) {
	if status != "" && status != "active" && status != "revoked" {
		return nil, invalid("status is active or revoked")
	}
	cl, err := s.vendorClient(ctx)
	if err != nil {
		return nil, err
	}
	return cl.List(ctx, status)
}

// GetServerLicense is one license with what it grants, where it ran and its audit trail.
func (s *Service) GetServerLicense(ctx context.Context, id string) (*VendorLicenseInfo, error) {
	cl, err := s.serverClient(ctx, id)
	if err != nil {
		return nil, err
	}
	return cl.Get(ctx, id)
}

// ServerLicenseCreate is a license made by hand: the end is Months, EndsAt (YYYY-MM-DD) or NoEnd; a trial may give none.
type ServerLicenseCreate struct {
	Customer  string
	Tier      string
	Months    int
	EndsAt    string
	NoEnd     bool
	RequestID string
}

// CreateServerLicense makes a license by hand. The answer carries the activation key: the only time it is shown.
func (s *Service) CreateServerLicense(ctx context.Context, actor *store.User, in ServerLicenseCreate) (*VendorLicenseInfo, error) {
	customer, err := cleanCustomer(in.Customer)
	if err != nil {
		return nil, err
	}
	if err := checkRequestID(in.RequestID); err != nil {
		return nil, err
	}
	cl, err := s.vendorClient(ctx)
	if err != nil {
		return nil, err
	}
	info, err := cl.CreateCustom(ctx, "dash-create-"+in.RequestID, VendorCreate{Customer: customer, Tier: in.Tier, Months: in.Months, EndsAt: in.EndsAt, NoEnd: in.NoEnd})
	if err != nil {
		return nil, err
	}
	if err := s.auditServer(ctx, actor, "vendor.license.create", info.ID, map[string]any{"customer": customer, "tier": info.Tier, "ends_at": info.EndsAt}, ""); err != nil {
		return nil, err
	}
	return info, nil
}

// ServerLicenseUpdate changes a license: ExtendMonths renews it (adds months to its end, or to now); otherwise any of
// the customer, tier, EndsAt (a date) and NoEnd.
type ServerLicenseUpdate struct {
	Customer     string
	Tier         string
	EndsAt       string
	NoEnd        bool
	ExtendMonths int
	RequestID    string
}

// UpdateServerLicense changes a license on the license server and keeps the store's own sold copy in step.
func (s *Service) UpdateServerLicense(ctx context.Context, actor *store.User, id string, in ServerLicenseUpdate) (*VendorLicenseInfo, error) {
	if err := checkRequestID(in.RequestID); err != nil {
		return nil, err
	}
	cl, err := s.serverClient(ctx, id)
	if err != nil {
		return nil, err
	}
	var info *VendorLicenseInfo
	switch {
	case in.ExtendMonths != 0:
		if in.Customer != "" || in.Tier != "" || in.EndsAt != "" || in.NoEnd {
			return nil, invalid("extend months alone, or the other fields without it")
		}
		info, err = cl.Extend(ctx, "dash-extend-"+id+"-"+in.RequestID, id, in.ExtendMonths)
	default:
		u := VendorUpdate{Tier: in.Tier, EndsAt: in.EndsAt, NoEnd: in.NoEnd}
		if in.Customer != "" {
			if u.Customer, err = cleanCustomer(in.Customer); err != nil {
				return nil, err
			}
		}
		info, err = cl.Update(ctx, "dash-update-"+id+"-"+in.RequestID, id, u)
	}
	if err != nil {
		return nil, err
	}
	if err := s.afterServerChange(ctx, actor, "vendor.license.update", info, map[string]any{"customer": in.Customer, "tier": in.Tier,
		"ends_at": in.EndsAt, "no_end": in.NoEnd, "extend_months": in.ExtendMonths}, ""); err != nil {
		return nil, err
	}
	return info, nil
}

// RevokeServerLicense revokes a license on the license server (a repeat is fine) and in the store's sold copy.
func (s *Service) RevokeServerLicense(ctx context.Context, actor *store.User, id, reason string) (*VendorLicenseInfo, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" || len(reason) > 500 {
		return nil, invalid("a reason is required (at most 500 characters)")
	}
	cl, err := s.serverClient(ctx, id)
	if err != nil {
		return nil, err
	}
	info, err := cl.Revoke(ctx, "dash-revoke-"+id, id, reason)
	if err != nil {
		var ve *VendorError
		if !asVendorError(err, &ve) || ve.Status != 409 || ve.Code != "revoked" {
			return nil, err
		}
		if info, err = cl.Get(ctx, id); err != nil { // already revoked: the same end state
			return nil, err
		}
	}
	if err := s.afterServerChange(ctx, actor, "vendor.license.revoke", info, nil, reason); err != nil {
		return nil, err
	}
	return info, nil
}

// RekeyServerLicense issues a new activation key for a license nobody has activated (the answer carries it once).
func (s *Service) RekeyServerLicense(ctx context.Context, actor *store.User, id, requestID string) (*VendorLicenseInfo, error) {
	if err := checkRequestID(requestID); err != nil {
		return nil, err
	}
	cl, err := s.serverClient(ctx, id)
	if err != nil {
		return nil, err
	}
	info, err := cl.Rekey(ctx, "dash-rekey-"+id+"-"+requestID, id)
	if err != nil {
		return nil, err
	}
	if err := s.auditServer(ctx, actor, "vendor.license.rekey", info.ID, nil, ""); err != nil {
		return nil, err
	}
	return info, nil
}

func (s *Service) auditServer(ctx context.Context, actor *store.User, action, id string, after any, reason string) error {
	return s.st.WithTx(ctx, func(tx pgx.Tx) error {
		return s.audit(ctx, tx, actor, action, "vendor_license", id, after, reason)
	})
}

// afterServerChange mirrors the change into the store's sold copy and audits it.
func (s *Service) afterServerChange(ctx context.Context, actor *store.User, action string, info *VendorLicenseInfo, after any, reason string) error {
	return s.st.WithTx(ctx, func(tx pgx.Tx) error {
		if err := s.st.SyncVendorLicense(ctx, tx, info.ID, info.Tier, info.EndsAt, info.Status); err != nil {
			return err
		}
		return s.audit(ctx, tx, actor, action, "vendor_license", info.ID, after, reason)
	})
}
