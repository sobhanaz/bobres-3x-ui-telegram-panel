// Package hosted holds what the owner's tools for hosted stores share (docs/18-hosted-and-affiliate-plans.md, section 1): the registration
// code that tools/hosted prints after it installed a customer's store and the owner pastes into the dashboard's Hosted stores page, and the
// reading of a store's /metrics into the few figures that page shows.
//
// A registration carries the store's domain, the customer's name, the version installed, the update channel, the license id (when one was
// made) and the token of the store's /metrics. It never carries the customer's bot token or backup key: those stay with the customer.
package hosted

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// CodePrefix starts every registration code.
const CodePrefix = "bobres-hosted:v1:"

// maxCode bounds a registration code (a token is at most 128 characters; the rest is short).
const maxCode = 4096

var (
	// DomainRe is a store's domain as the installer takes it: lower case, at least two labels, no port and no IP address.
	DomainRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`)
	// TokenRe is a metrics token (core's own rule: 24 to 128 characters of a bearer token).
	TokenRe = regexp.MustCompile(`^[A-Za-z0-9._~+/=-]{24,128}$`)
	// LicenseIDRe is a license server id.
	LicenseIDRe = regexp.MustCompile(`^lic_[a-z0-9_-]{1,60}$`)
	versionRe   = regexp.MustCompile(`^[0-9A-Za-z.+_-]{1,64}$`)
	allDigits   = regexp.MustCompile(`^[0-9]+$`)
)

// Registration is what the dashboard needs to put a store in the register.
type Registration struct {
	Customer     string `json:"customer"`
	Domain       string `json:"domain"`
	Version      string `json:"version,omitempty"`
	Channel      string `json:"channel,omitempty"`
	LicenseID    string `json:"license_id,omitempty"`
	MetricsToken string `json:"metrics_token,omitempty"`
	Created      int64  `json:"created,omitempty"`
}

// Check reports the first field that is not usable, or nil.
func (r *Registration) Check() error {
	r.Customer = strings.TrimSpace(r.Customer)
	r.Domain = strings.ToLower(strings.TrimSpace(r.Domain))
	switch {
	case r.Customer == "" || len(r.Customer) > 200 || strings.IndexFunc(r.Customer, unicode.IsControl) >= 0:
		return errors.New("customer: required, at most 200 characters")
	case !ValidDomain(r.Domain):
		return errors.New("domain: not a domain name (lower case, no port, not an IP address)")
	case r.Version != "" && !versionRe.MatchString(r.Version):
		return errors.New("version: not a version")
	case r.Channel != "" && r.Channel != "stable" && r.Channel != "beta":
		return errors.New("channel: stable or beta")
	case r.LicenseID != "" && !LicenseIDRe.MatchString(r.LicenseID):
		return errors.New("license_id: not a license id")
	case r.MetricsToken != "" && !TokenRe.MatchString(r.MetricsToken):
		return errors.New("metrics_token: 24 to 128 characters of letters, digits and . _ ~ + / = -")
	}
	return nil
}

// ValidDomain reports whether d is a domain name a store can have (and not an IP address written as one).
func ValidDomain(d string) bool {
	if len(d) > 253 || !DomainRe.MatchString(d) {
		return false
	}
	labels := strings.Split(d, ".")
	return !allDigits.MatchString(labels[len(labels)-1]) // 10.0.0.1 is an address, not a name
}

// Encode is the registration as one line to paste.
func (r Registration) Encode() (string, error) {
	if err := r.Check(); err != nil {
		return "", err
	}
	b, err := json.Marshal(r)
	if err != nil {
		return "", err
	}
	return CodePrefix + base64.RawURLEncoding.EncodeToString(b), nil
}

// Decode reads a registration code (spaces and line breaks around and inside it are ignored, as a terminal may wrap it).
func Decode(code string) (*Registration, error) {
	code = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, code)
	if len(code) > maxCode {
		return nil, errors.New("the registration code is too long")
	}
	rest, ok := strings.CutPrefix(code, CodePrefix)
	if !ok {
		return nil, errors.New("not a registration code (it starts with " + CodePrefix + ")")
	}
	raw, err := base64.RawURLEncoding.DecodeString(rest)
	if err != nil {
		return nil, errors.New("the registration code is damaged")
	}
	var r Registration
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return nil, errors.New("the registration code is damaged")
	}
	if err := r.Check(); err != nil {
		return nil, err
	}
	return &r, nil
}

// Figures are the few things the Hosted stores page shows of a store, read from its /metrics.
type Figures struct {
	Version          string   `json:"version,omitempty"`
	ServicesUp       int      `json:"services_up"`
	ServicesTotal    int      `json:"services_total"`
	DatabaseUp       *bool    `json:"database_up,omitempty"`
	OrdersPaid24h    *int64   `json:"orders_paid_24h,omitempty"`
	AlertsCritical   int64    `json:"alerts_critical"`
	AlertsWarning    int64    `json:"alerts_warning"`
	LicenseDaysLeft  *int64   `json:"license_days_left,omitempty"`
	LicenseBasis     string   `json:"license_basis,omitempty"`
	BackupAgeSeconds *int64   `json:"backup_age_seconds,omitempty"`
	BackupLastOK     *bool    `json:"backup_last_ok,omitempty"`
	PanelsUp         int      `json:"panels_up"`
	PanelsTotal      int      `json:"panels_total"`
	DiskUsedRatio    *float64 `json:"disk_used_ratio,omitempty"`
	QueuePending     *int64   `json:"pending_payments,omitempty"`
}

// Healthy is the store's state at a glance: every service answers, the database answers and no critical alert is open.
func (f *Figures) Healthy() bool {
	return f.ServicesTotal > 0 && f.ServicesUp == f.ServicesTotal && (f.DatabaseUp == nil || *f.DatabaseUp) && f.AlertsCritical == 0
}

// maxMetrics bounds what is read of an exposition (a store's is a few kilobytes).
const maxMetrics = 1 << 20

var sampleRe = regexp.MustCompile(`^([a-zA-Z_:][a-zA-Z0-9_:]*)(\{([^}]*)\})?\s+(\S+)`)
var labelRe = regexp.MustCompile(`([a-zA-Z_][a-zA-Z0-9_]*)="((?:[^"\\]|\\.)*)"`)

// ParseMetrics reads a BOBRES /metrics exposition (the text format 0.0.4) into Figures. It fails when the text is not a BOBRES exposition at
// all (no bobres_build_info), which tells a wrong address from a store that answers.
func ParseMetrics(body []byte) (*Figures, error) {
	if len(body) > maxMetrics {
		return nil, errors.New("the metrics are too long")
	}
	f := &Figures{}
	found := false
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 64<<10), 64<<10)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		m := sampleRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		name, labels := m[1], map[string]string{}
		for _, l := range labelRe.FindAllStringSubmatch(m[3], -1) {
			labels[l[1]] = l[2]
		}
		v, err := strconv.ParseFloat(m[4], 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
			continue
		}
		switch name {
		case "bobres_build_info":
			found = true
			if ver := labels["version"]; versionRe.MatchString(ver) {
				f.Version = ver
			}
		case "bobres_service_up":
			f.ServicesTotal++
			if v == 1 {
				f.ServicesUp++
			}
		case "bobres_database_up":
			up := v == 1
			f.DatabaseUp = &up
		case "bobres_orders_paid":
			if labels["window"] == "24h" {
				n := int64(v)
				f.OrdersPaid24h = &n
			}
		case "bobres_alerts_open":
			switch labels["severity"] {
			case "critical":
				f.AlertsCritical = int64(v)
			case "warning":
				f.AlertsWarning = int64(v)
			}
		case "bobres_license_days_left":
			n := int64(v)
			if f.LicenseDaysLeft == nil || labels["basis"] == "license" {
				f.LicenseDaysLeft, f.LicenseBasis = &n, labels["basis"]
			}
		case "bobres_backup_age_seconds":
			n := int64(v)
			f.BackupAgeSeconds = &n
		case "bobres_backup_last_run_ok":
			ok := v == 1
			f.BackupLastOK = &ok
		case "bobres_panel_up":
			f.PanelsTotal++
			if v == 1 {
				f.PanelsUp++
			}
		case "bobres_host_disk_used_ratio":
			if f.DiskUsedRatio == nil || v > *f.DiskUsedRatio {
				r := v
				f.DiskUsedRatio = &r
			}
		case "bobres_queue_size":
			if labels["queue"] == "pending_payments" {
				n := int64(v)
				f.QueuePending = &n
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("the metrics are unreadable: %w", err)
	}
	if !found {
		return nil, errors.New("the answer is not a BOBRES store's metrics")
	}
	return f, nil
}
