package i18n

import (
	"fmt"
	"html"
	"strings"
	"testing"
	"time"
)

// A system alert's values: numbers in the language's digits, a size, a
// date; names (and anything that is not a number) escaped.
func TestAlertArgs(t *testing.T) {
	args := map[string]string{
		"count": "1200", "minutes": "25", "free": fmt.Sprint(1536 << 20),
		"date":    fmt.Sprint(time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC).Unix()),
		"server":  "DE <1>",
		"service": "42", // a name made of digits stays as it is
		"days":    "soon",
	}
	for lang, want := range map[string]string{
		"en": "[count 1,200 date 2 Nov 2026 days soon free 1.5 GB minutes 25 server DE &lt;1&gt; service 42]",
		"fa": "[count ۱٬۲۰۰ date ۱۱ آبان ۱۴۰۵ days soon free ۱٫۵ گیگابایت minutes ۲۵ server DE &lt;1&gt; service 42]",
	} {
		if got := fmt.Sprint(AlertArgs(lang, args, html.EscapeString)); got != want {
			t.Errorf("%s:\n got %s\nwant %s", lang, got, want)
		}
	}
	if got := AlertArgs("en", nil, html.EscapeString); len(got) != 0 {
		t.Errorf("no args: %v", got)
	}
}

// The alert texts are staff texts, filed with the admin alerts in the
// texts editor; the headings take a rendered title.
func TestAlertTextsAreAdminTexts(t *testing.T) {
	c := MustLoad()
	for _, key := range []string{"alert.critical", "alert.server_down", "alert.duration_hours"} {
		info, ok := c.Info(key)
		if !ok || info.Group != "admin" || info.Context != CtxHTML {
			t.Errorf("%s: %+v %v", key, info, ok)
		}
	}
	if _, probs := c.ValidateOverride("en", "alert.critical", "<code>{title}</code>"); len(probs) == 0 {
		t.Error("a title inside <code> was accepted")
	}
}

// The limit alert names what is limited in words and writes the limit in the
// language's digits.
func TestLicenseLimitAlert(t *testing.T) {
	c := MustLoad()
	args := map[string]string{"limit": "25", "name": "active_services"}
	for lang, want := range map[string]string{
		"en": "A new purchase was refused: this install is at its limit of 25 active services. Buy or renew a license to sell more.",
		"fa": "یک خرید جدید رد شد: این نصب به سقف ۲۵ سرویس فعال رسیده است. برای فروش بیشتر، مجوز بخرید یا تمدید کنید.",
	} {
		if got := c.T(lang, "alert.license_limit", AlertArgs(lang, args, html.EscapeString)...); got != want {
			t.Errorf("%s:\n got %s\nwant %s", lang, got, want)
		}
	}
	// A name this build does not know is shown as it is (escaped).
	if got := fmt.Sprint(AlertArgs("en", map[string]string{"name": "<x>"}, html.EscapeString)); got != "[name &lt;x&gt;]" {
		t.Errorf("an unknown name: %s", got)
	}
	info, ok := c.Info("alert.license_limit")
	if !ok || info.Group != "admin" || fmt.Sprint(info.Placeholders) != "[limit name]" {
		t.Errorf("alert.license_limit: %+v %v", info, ok)
	}
}

// licensingWords are what a text says when it speaks of licensing, in
// either language: the English words and every Persian one a text could use
// for a license (مجوز, پروانه, لایسنس) or for the tiers (سطح رایگان, and the
// old بسته رایگان).
var licensingWords = []string{"licen", "free tier", "مجوز", "پروانه", "لایسنس", "سطح رایگان", "بسته رایگان"}

// sellsLicenses: the keys of the owner's own bot selling BOBRES licenses as a product (license sales are a feature of
// that one store, not a limit a customer meets): the menu button, the sales screens, their ledger lines and settings.
func sellsLicenses(key string) bool {
	for _, p := range []string{"lic.", "btn.lic", "ledger.license_", "setting.vendor."} {
		if strings.HasPrefix(key, p) {
			return true
		}
	}
	return false
}

// speaksOfLicensing: text mentions licensing (case aside).
func speaksOfLicensing(text string) bool {
	text = strings.ToLower(text)
	for _, w := range licensingWords {
		if strings.Contains(text, w) {
			return true
		}
	}
	return false
}

// A customer never sees licensing: only the staff alerts speak of a license,
// and the text a refused purchase gets does not.
func TestOnlyStaffAlertsMentionLicensing(t *testing.T) {
	c := MustLoad()
	// The detector sees every word a text could use, and the staff alerts
	// that speak of a license (so the test below is not vacuous).
	for _, w := range licensingWords {
		if !speaksOfLicensing("… " + strings.ToUpper(w) + " …") {
			t.Errorf("the detector misses %q", w)
		}
	}
	for _, lang := range Languages {
		if !speaksOfLicensing(c.T(lang, "alert.license_expired")) || !speaksOfLicensing(c.T(lang, "alert.trial_ended")) {
			t.Errorf("%s: the detector misses a staff alert about licensing", lang)
		}
		for _, key := range c.Keys(lang) {
			if speaksOfLicensing(c.T(lang, key)) && !strings.HasPrefix(key, "alert.") && !sellsLicenses(key) {
				t.Errorf("%s %s speaks of licensing to a customer: %s", lang, key, c.T(lang, key))
			}
		}
		if got := c.T(lang, "error.purchases_paused"); got == "error.purchases_paused" || got == "" {
			t.Errorf("%s: no text for a refused purchase", lang)
		}
	}
	info, ok := c.Info("error.purchases_paused")
	if !ok || info.Context != CtxToastHTML || info.Group != "general" {
		t.Errorf("error.purchases_paused: %+v %v", info, ok)
	}
}

// The staff alerts about the trial name their values and the tier in
// Persian as «سطح» (a package is «بسته», which is traffic).
func TestTrialAlerts(t *testing.T) {
	c := MustLoad()
	ending := map[string]string{"days": "5", "date": "1792000000"}
	for lang, want := range map[string]string{
		"en": "The free trial ends in 5 days, on ",
		"fa": "دورهٔ آزمایشی ۵ روز دیگر، در ",
	} {
		got := c.T(lang, "alert.trial_ending", AlertArgs(lang, ending, html.EscapeString)...)
		if !strings.HasPrefix(got, want) || strings.Contains(got, "{") {
			t.Errorf("%s alert.trial_ending: %s", lang, got)
		}
		if info, ok := c.Info("alert.trial_ending"); !ok || info.Group != "admin" || fmt.Sprint(info.Placeholders) != "[date days]" {
			t.Errorf("alert.trial_ending: %+v %v", info, ok)
		}
		if info, ok := c.Info("alert.trial_ended"); !ok || info.Group != "admin" || len(info.Placeholders) != 0 {
			t.Errorf("alert.trial_ended: %+v %v", info, ok)
		}
		for _, key := range []string{"alert.trial_ended", "alert.license_expired"} {
			if strings.Contains(c.T(lang, key), "بسته") {
				t.Errorf("%s %s says بسته for a tier: %s", lang, key, c.T(lang, key))
			}
		}
	}
}

// The monitoring alerts write a percent in the language's digits and name the mount, container or
// workflow as a name (escaped).
func TestMonitorAlertTexts(t *testing.T) {
	c := MustLoad()
	args := map[string]string{"mount": "/var/lib/docker", "percent": "91", "free": fmt.Sprint(3 << 30), "minutes": "10", "count": "4",
		"container": "bobres-bot-1", "workflow": "CI <main>"}
	for _, key := range []string{"alert.monitor_disk", "alert.monitor_memory", "alert.monitor_container_down", "alert.monitor_container_unhealthy",
		"alert.monitor_container_restarting", "alert.monitor_collector_stale", "alert.monitor_ci"} {
		for _, lang := range Languages {
			got := c.T(lang, key, AlertArgs(lang, args, html.EscapeString)...)
			if strings.Contains(got, "{") || strings.Contains(got, "<main>") || got == "" {
				t.Errorf("%s %s: %q", lang, key, got)
			}
		}
	}
	if got := c.T("en", "alert.monitor_disk", AlertArgs("en", args, html.EscapeString)...); got != "The disk at /var/lib/docker is 91% full (3 GB free)." {
		t.Errorf("en: %q", got)
	}
	if got := c.T("fa", "alert.monitor_memory", AlertArgs("fa", args, html.EscapeString)...); !strings.Contains(got, "۹۱") || !strings.Contains(got, "۱۰") {
		t.Errorf("fa: %q", got)
	}
}
