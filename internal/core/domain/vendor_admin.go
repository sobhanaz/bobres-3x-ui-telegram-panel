package domain

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// What the owner does with the licenses the store sold, from the dashboard: look at them, give more months for
// nothing, and revoke one. The changes go to the license server first (with refs, so a retry changes nothing twice)
// and are then written here.

var adminRequestRe = regexp.MustCompile(`^[A-Za-z0-9._-]{8,40}$`)

// VendorStatus is what the dashboard shows of the sales: whether they can and do run, and where the license server is.
type VendorStatus struct {
	Available  bool
	On         bool
	Configured bool
	AdminURL   string
	Tiers      []VendorTier
}

// VendorStatus describes the license sales now.
func (s *Service) VendorStatus(ctx context.Context) (*VendorStatus, error) {
	st := &VendorStatus{Available: s.VendorAvailable()}
	tiers, err := s.VendorTiers()
	if err != nil {
		return nil, err
	}
	st.Tiers = tiers
	if c, err := s.st.GetVendorConfig(ctx, s.st.Conn()); err == nil {
		st.Configured, st.AdminURL = true, c.AdminURL
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	st.On = s.VendorOn(ctx)
	return st, nil
}

// ListSoldLicenses lists the licenses sold, newest first (never their keys).
func (s *Service) ListSoldLicenses(ctx context.Context, status string, p store.Page) ([]store.VendorLicenseRow, int64, error) {
	if status != "" && status != "active" && status != "revoked" {
		return nil, 0, invalid("status is active or revoked")
	}
	return s.st.ListVendorLicenses(ctx, s.st.Conn(), status, p)
}

func (s *Service) soldLicense(ctx context.Context, id string) (*store.VendorLicense, VendorClient, error) {
	l, err := s.st.GetVendorLicense(ctx, s.st.Conn(), id, false)
	if err != nil {
		return nil, nil, err
	}
	cl, err := s.vendorClient(ctx)
	if err != nil {
		return nil, nil, err
	}
	return l, cl, nil
}

// GrantVendorMonths adds months to a sold license for nothing (a gift, a compensation). requestID makes a retry of the
// same click one grant.
func (s *Service) GrantVendorMonths(ctx context.Context, actor *store.User, id string, months int, requestID string) (*store.VendorLicense, error) {
	if months < 1 || months > 36 {
		return nil, invalid("months is 1 to 36")
	}
	if !adminRequestRe.MatchString(requestID) {
		return nil, invalid("request_id is required (8 to 40 letters, digits, . _ -)")
	}
	l, cl, err := s.soldLicense(ctx, id)
	if err != nil {
		return nil, err
	}
	if l.Status != "active" {
		return nil, stateErr("a revoked license does not change")
	}
	info, err := cl.Extend(ctx, "grant-"+l.ID+"-"+requestID, l.LicenseID, months)
	if err != nil {
		return nil, err
	}
	err = s.st.WithTx(ctx, func(tx pgx.Tx) error {
		if err := s.st.SetVendorLicenseEnd(ctx, tx, l.ID, info.EndsAt); err != nil {
			return err
		}
		return s.audit(ctx, tx, actor, "vendor.grant", "vendor_license", l.ID, map[string]any{"months": months, "license_id": l.LicenseID}, "")
	})
	if err != nil {
		return nil, err
	}
	l.EndsAt = info.EndsAt
	return l, nil
}

// RevokeVendorLicense revokes a sold license on the license server (its install keeps its last token until that ends,
// then new actions are gated) and marks it here. The buyer's money is not touched: a refund is the owner's call.
func (s *Service) RevokeVendorLicense(ctx context.Context, actor *store.User, id, reason string) (*store.VendorLicense, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" || len(reason) > 500 {
		return nil, invalid("a reason is required (at most 500 characters)")
	}
	l, cl, err := s.soldLicense(ctx, id)
	if err != nil {
		return nil, err
	}
	if l.Status == "revoked" {
		return l, nil
	}
	if _, err := cl.Revoke(ctx, "revoke-"+l.ID, l.LicenseID, reason); err != nil {
		var ve *VendorError
		// The server already has it revoked (a retry after a lost answer): the same end state.
		if !errors.As(err, &ve) || ve.Status != 409 || ve.Code != "revoked" {
			return nil, err
		}
	}
	err = s.st.WithTx(ctx, func(tx pgx.Tx) error {
		if err := s.st.SetVendorLicenseStatus(ctx, tx, l.ID, "revoked"); err != nil {
			return err
		}
		return s.audit(ctx, tx, actor, "vendor.revoke", "vendor_license", l.ID, map[string]any{"license_id": l.LicenseID}, reason)
	})
	if err != nil {
		return nil, err
	}
	l.Status = "revoked"
	return l, nil
}
