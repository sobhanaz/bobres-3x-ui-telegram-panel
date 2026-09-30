// Package branding holds the white-label settings of one install. Brand is
// data, never hardcoded in user-facing strings: each operator sets their own
// name, logo, colors and texts. "BOBRES" only appears when the license does
// not include the white_label entitlement.
package branding

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// Lang is a supported UI language.
type Lang string

// Supported languages.
const (
	FA Lang = "fa"
	EN Lang = "en"
)

// ProductName is the vendor product name shown only without white_label.
const ProductName = "BOBRES"

var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// Brand is the operator-facing identity of one install.
type Brand struct {
	Name           map[Lang]string `json:"name"`
	Tagline        map[Lang]string `json:"tagline,omitempty"`
	LogoURL        string          `json:"logo_url,omitempty"`
	PrimaryColor   string          `json:"primary_color"`
	AccentColor    string          `json:"accent_color"`
	SupportContact string          `json:"support_contact,omitempty"` // @username or URL
	TermsURL       string          `json:"terms_url,omitempty"`
	PrivacyURL     string          `json:"privacy_url,omitempty"`
	Currency       string          `json:"currency"` // display currency code, e.g. IRT
	DefaultLang    Lang            `json:"default_lang"`
}

// Default returns a neutral starting brand.
func Default() Brand {
	return Brand{
		Name:         map[Lang]string{EN: "My VPN Store", FA: "فروشگاه VPN من"},
		PrimaryColor: "#0F172A",
		AccentColor:  "#14B8A6",
		Currency:     "IRT",
		DefaultLang:  FA,
	}
}

// Validate checks the brand is safe to render and store.
func (b Brand) Validate() error {
	var errs []error
	if strings.TrimSpace(b.Name[b.DefaultLang]) == "" {
		errs = append(errs, fmt.Errorf("name is required for default language %q", b.DefaultLang))
	}
	switch b.DefaultLang {
	case FA, EN:
	default:
		errs = append(errs, fmt.Errorf("default_lang must be fa or en, got %q", b.DefaultLang))
	}
	if !hexColor.MatchString(b.PrimaryColor) {
		errs = append(errs, errors.New("primary_color must be #RRGGBB"))
	}
	if !hexColor.MatchString(b.AccentColor) {
		errs = append(errs, errors.New("accent_color must be #RRGGBB"))
	}
	if strings.TrimSpace(b.Currency) == "" {
		errs = append(errs, errors.New("currency is required"))
	}
	for field, raw := range map[string]string{"logo_url": b.LogoURL, "terms_url": b.TermsURL, "privacy_url": b.PrivacyURL} {
		if raw == "" {
			continue
		}
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			errs = append(errs, fmt.Errorf("%s must be an http(s) URL", field))
		}
	}
	return errors.Join(errs...)
}

// DisplayName returns the brand name for lang, falling back to the default
// language and finally to the product name.
func (b Brand) DisplayName(lang Lang) string {
	if n := strings.TrimSpace(b.Name[lang]); n != "" {
		return n
	}
	if n := strings.TrimSpace(b.Name[b.DefaultLang]); n != "" {
		return n
	}
	return ProductName
}

// PoweredBy returns the attribution line, or "" when white-label is licensed.
func PoweredBy(whiteLabel bool) string {
	if whiteLabel {
		return ""
	}
	return "Powered by " + ProductName
}
