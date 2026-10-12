package config

import (
	"errors"
	"net/url"
	"strings"
)

// LicenseServerURL checks BOBRES_LICENSE_SERVER, the vendor's license
// server: an https URL without credentials, query or fragment (a path
// prefix is kept). It answers it without a trailing slash; "" when unset.
// The bot (for core) and `bobres license activate` call it.
func LicenseServerURL(raw string) (string, error) {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	if raw == "" {
		return "", nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		strings.HasSuffix(raw, "?") || strings.HasSuffix(raw, "#") {
		return "", errors.New("BOBRES_LICENSE_SERVER must be an https URL (https://license.example.com)")
	}
	return raw, nil
}
