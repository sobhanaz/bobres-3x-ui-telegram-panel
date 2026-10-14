package license

import (
	"bytes"
	_ "embed" // tiers.json
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// tiersJSON is the vendor's tier presets, tiers.json: the one file the vendor
// edits (docs/pricing.md). It is built into the vendor tools (tools/license,
// licensesrv); installs never read it.
//
//go:embed tiers.json
var tiersJSON []byte

// PricePlaceholder is what an unset price says in tiers.json.
const PricePlaceholder = "SET ME"

// Price is what a tier costs per install. The values are text so that the
// vendor can write them as they will print them; "SET ME" is a placeholder.
// The vendor tools never read a price.
type Price struct {
	Currency string `json:"currency"`
	Monthly  string `json:"monthly"`
	Yearly   string `json:"yearly"`
}

// IsSet reports whether the vendor has filled in all three values.
func (p Price) IsSet() bool {
	for _, v := range []string{p.Currency, p.Monthly, p.Yearly} {
		if v = strings.TrimSpace(v); v == "" || strings.EqualFold(v, PricePlaceholder) {
			return false
		}
	}
	return true
}

// Preset is one tier: the entitlements a license of it carries, and how long
// it lasts by default.
type Preset struct {
	Description string `json:"description"`
	// Price is nil for a tier that is not sold (the trial).
	Price *Price `json:"price,omitempty"`
	// Days, when set, is the fixed length of a license of this tier (the
	// trial: 30). A license of another tier has no fixed length.
	Days int `json:"days,omitempty"`
	// GraceDays is the default grace of a license of this tier.
	GraceDays    int          `json:"grace_days"`
	Entitlements Entitlements `json:"entitlements"`
}

// Presets are the paid tiers by name (a Token.Tier).
type Presets struct {
	// Readme is the file's header: prices are placeholders, the free tier's
	// numbers and how to edit it. It is documentation only.
	Readme []string          `json:"_readme"`
	Tiers  map[string]Preset `json:"tiers"`
}

// TierNames is the order tiers are listed in.
var TierNames = []string{TierStandard, TierPro, TierTrial}

// LoadPresets reads the presets built into this binary (tiers.json).
func LoadPresets() (Presets, error) { return ParsePresets(bytes.NewReader(tiersJSON)) }

// ParsePresets reads a presets file strictly (a misspelt field fails instead
// of granting nothing) and checks that every preset would make a token that
// core accepts.
func ParsePresets(r io.Reader) (Presets, error) {
	var p Presets
	dec := json.NewDecoder(io.LimitReader(r, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return Presets{}, fmt.Errorf("tier presets: %w", err)
	}
	if len(p.Tiers) == 0 {
		return Presets{}, errors.New("tier presets: no tiers")
	}
	var errs []error
	for name, t := range p.Tiers {
		if err := t.check(name); err != nil {
			errs = append(errs, fmt.Errorf("tier %s: %w", name, err))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return Presets{}, fmt.Errorf("tier presets: %w", err)
	}
	return p, nil
}

func (t Preset) check(name string) error {
	var errs []error
	switch name {
	case TierTrial, TierStandard, TierPro:
	default:
		errs = append(errs, fmt.Errorf("%q is not a tier a token can carry (trial, standard, pro)", name))
	}
	if t.Days < 0 || t.Days > 3650 {
		errs = append(errs, errors.New("days must be 0..3650"))
	}
	if t.GraceDays < 0 || t.GraceDays > maxGraceDays {
		errs = append(errs, fmt.Errorf("grace_days must be 0..%d", maxGraceDays))
	}
	if t.Entitlements.UpdatesUntil != "" {
		errs = append(errs, errors.New("updates_until is set per license, not in a preset"))
	}
	// The same structural checks as a token's, on a token made from it.
	now := time.Now().UTC()
	probe := Token{LicenseID: "lic_probe", Tier: TierStandard, IssuedAt: now, ExpiresAt: now.Add(time.Hour),
		GraceDays: t.GraceDays, Entitlements: t.Entitlements}
	if err := probe.Validate(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// Get returns the preset of a tier.
func (p Presets) Get(name string) (Preset, bool) {
	t, ok := p.Tiers[name]
	return t, ok
}

// Names lists the tiers in the file in TierNames order.
func (p Presets) Names() []string {
	var out []string
	for _, n := range TierNames {
		if _, ok := p.Tiers[n]; ok {
			out = append(out, n)
		}
	}
	return out
}

// AddMonths is t plus n calendar months, clamped to the end of a shorter
// month (31 January plus one month is the last day of February), so that a
// monthly subscription renewed on the 31st never skips into March.
func AddMonths(t time.Time, n int) time.Time {
	y, m, d := t.Date()
	first := time.Date(y, m+time.Month(n), 1, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location())
	last := time.Date(first.Year(), first.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
	if d > last {
		d = last
	}
	return time.Date(first.Year(), first.Month(), d, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location())
}
