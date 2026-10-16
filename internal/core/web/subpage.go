package web

import (
	"bytes"
	_ "embed"
	"encoding/base64"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// The branded subscription page, GET /sub/{token} (docs/15-subscription-page.md):
// server-rendered HTML for a person, one inline stylesheet, no script. It shows
// the service's existing sub link; it never proxies a configuration.

//go:embed subpage.html
var subpageHTML string

var subpageTmpl = template.Must(template.New("subpage").Parse(subpageHTML))

const (
	// subPageRate is how many page requests one client address may make a minute.
	subPageRate = 30
	// expiringDays: a service with fewer days left than this is "expiring soon".
	expiringDays = 3
	// subPageCSP lets the page load its own logo and the QR code (a data: URI),
	// apply its inline stylesheet and nothing else.
	subPageCSP = "default-src 'none'; style-src 'unsafe-inline'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'; form-action 'none'"
)

var (
	pageColorRe = regexp.MustCompile(`^#[0-9a-f]{6}$`)
	pageBotRe   = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{4,31}$`)
)

// pageText is the page's own wording (the app guides are catalog texts the
// store can edit; the rest is fixed).
type pageText struct {
	Title, Expires, DaysLeftLabel, Traffic, Renew, LinkTitle, LinkHint, QRAlt,
	AppsTitle, AddTo, AppsHint, Support, Terms, Privacy, Unlimited, NoExpiry,
	UsedOf, UsedOnly, DayOne, DayMany string
	Status map[string]string
}

var pageTexts = map[string]pageText{
	"en": {
		Title: "Your service", Expires: "Expires", DaysLeftLabel: "Time left", Traffic: "Traffic",
		Renew: "Renew or top up", LinkTitle: "Subscription link",
		LinkHint: "Copy this link into your VPN app, or scan the QR code with it. Keep it private: anyone with it can use your service.",
		QRAlt:    "QR code of the subscription link", AppsTitle: "Add it to an app", AddTo: "Add to",
		AppsHint: "If a button does nothing, install the app first, or copy the link above and add it in the app by hand.",
		Support:  "Support", Terms: "Terms", Privacy: "Privacy", Unlimited: "Unlimited", NoExpiry: "No expiry",
		UsedOf: "{used} used of {total}", UsedOnly: "{used} used", DayOne: "{n} day", DayMany: "{n} days",
		Status: map[string]string{"active": "Active", "expiring_soon": "Expiring soon", "expired": "Expired",
			"depleted": "Traffic used up", "disabled": "Disabled"},
	},
	"fa": {
		Title: "سرویس شما", Expires: "پایان", DaysLeftLabel: "زمان باقی‌مانده", Traffic: "ترافیک",
		Renew: "تمدید یا افزایش حجم", LinkTitle: "لینک اشتراک",
		LinkHint: "این لینک را در برنامه VPN خود وارد کنید یا کد QR را اسکن کنید. آن را محرمانه نگه دارید: هر کسی با این لینک می‌تواند از سرویس شما استفاده کند.",
		QRAlt:    "کد QR لینک اشتراک", AppsTitle: "افزودن به برنامه", AddTo: "افزودن به",
		AppsHint: "اگر دکمه‌ای کاری نکرد، ابتدا برنامه را نصب کنید یا لینک بالا را کپی کرده و در برنامه دستی اضافه کنید.",
		Support:  "پشتیبانی", Terms: "قوانین", Privacy: "حریم خصوصی", Unlimited: "نامحدود", NoExpiry: "بدون انقضا",
		UsedOf: "{used} از {total} مصرف شده", UsedOnly: "{used} مصرف شده", DayOne: "{n} روز", DayMany: "{n} روز",
		Status: map[string]string{"active": "فعال", "expiring_soon": "رو به پایان", "expired": "منقضی شده",
			"depleted": "حجم تمام شده", "disabled": "غیرفعال"},
	},
}

// pageApp is one app button: a deep link and its guide.
type pageApp struct {
	Name  string
	URL   template.URL
	Guide string
}

type pageData struct {
	Lang, Dir, OtherLang, OtherLangName string
	Title, Brand, Color, OnColor, Logo  string
	T                                   pageText

	StatusClass, StatusText, Expires, DaysLeft, TrafficText, UsageText string
	HasBar, BarFull, NeedsRenew                                        bool
	Percent                                                            int

	Link string
	QR   template.URL
	Apps []pageApp

	Renew                string
	Support, SupportURL  string
	TermsURL, PrivacyURL string
	PoweredBy            bool
}

// pageNotFound is the one answer for every request that does not lead to a
// page: unknown, malformed, deleted or unfinished services, and the page
// switched off. It is fixed bytes (no store details, no language switch), so
// the cases cannot be told apart by the body.
var pageNotFound = []byte(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex, nofollow"><title>404</title>
<style>body{font:16px/1.6 system-ui,sans-serif;margin:0;display:grid;place-items:center;min-height:100vh;background:#f5f7f8;color:#14202b;text-align:center}
@media (prefers-color-scheme:dark){body{background:#0f1519;color:#e8eef2}}main{padding:1rem}h1{font-size:3rem;margin:0}</style></head>
<body><main><h1>404</h1><p>This page does not exist.</p><p dir="rtl">این صفحه وجود ندارد.</p></main></body></html>
`)

func pageHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store, private")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Robots-Tag", "noindex, nofollow")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Content-Security-Policy", subPageCSP)
}

func (s *Server) pageNotFound(w http.ResponseWriter, _ *http.Request) {
	pageHeaders(w)
	w.Header().Set("Content-Length", strconv.Itoa(len(pageNotFound)))
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write(pageNotFound)
}

// subPage: GET /sub/{token}. Public (the token is the credential), so it has
// no session and no CSRF check; it is rate limited per client address.
func (s *Server) subPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if !s.subPages.allow(clientKey(r), s.now()) {
		pageHeaders(w)
		w.Header().Set("Retry-After", "60")
		http.Error(w, "too many requests", http.StatusTooManyRequests)
		return
	}
	settings, err := s.cfg.Domain.Settings(ctx)
	if err != nil {
		s.cfg.Log.Error("sub page settings", "err", err)
		pageHeaders(w)
		http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
		return
	}
	token := r.PathValue("token")
	conf := domain.SubPageFrom(settings)
	if !store.ValidPageToken(token) {
		s.pageNotFound(w, r)
		return
	}
	sub, err := s.cfg.Store.SubscriptionByPageToken(ctx, s.cfg.Store.Conn(), token)
	switch {
	case errors.Is(err, store.ErrNotFound):
		s.pageNotFound(w, r)
		return
	case err != nil:
		s.cfg.Log.Error("sub page lookup", "err", err)
		pageHeaders(w)
		http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
		return
	}
	if !conf.Enabled || sub.Status == "pending" || sub.Status == "deleted" || sub.SubLink == "" {
		s.pageNotFound(w, r)
		return
	}
	data, err := s.pageData(r, settings, conf, sub)
	if err != nil {
		s.cfg.Log.Error("sub page build", "err", err)
		pageHeaders(w)
		http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
		return
	}
	var buf bytes.Buffer
	if err := subpageTmpl.Execute(&buf, data); err != nil {
		s.cfg.Log.Error("sub page render", "err", err)
		pageHeaders(w)
		http.Error(w, "temporarily unavailable", http.StatusInternalServerError)
		return
	}
	pageHeaders(w)
	w.Header().Set("Content-Length", strconv.Itoa(buf.Len()))
	_, _ = w.Write(buf.Bytes())
}

// pageData turns a service and the store's branding into what the template shows.
func (s *Server) pageData(r *http.Request, settings map[string]string, conf domain.SubPageSettings, sub *store.Subscription) (*pageData, error) {
	ctx := r.Context()
	lang := "fa"
	if r.URL.Query().Get("l") == "en" {
		lang = "en"
	}
	other, otherName := "en", "English"
	dir := "rtl"
	if lang == "en" {
		other, otherName, dir = "fa", "فارسی", "ltr"
	}
	t := pageTexts[lang]
	color := strings.ToLower(settings["branding.color"])
	if !pageColorRe.MatchString(color) {
		color = defaultColor
	}
	d := &pageData{
		Lang: lang, Dir: dir, OtherLang: other, OtherLangName: otherName, T: t,
		Brand: brandName(settings), Color: color, OnColor: onColor(color), Logo: s.logoURL(ctx),
		Link: sub.SubLink, PoweredBy: s.poweredBy(ctx),
		TermsURL: settings["branding.terms_url"], PrivacyURL: settings["branding.privacy_url"],
	}
	d.Title = t.Title
	d.Support, d.SupportURL = supportContact(settings["branding.support"])
	if pageBotRe.MatchString(conf.BotUsername) {
		d.Renew = "https://t.me/" + conf.BotUsername
	}

	now := s.now()
	status := sub.Status
	days := 0
	if sub.ExpiresAt != nil {
		d.Expires = i18n.Date(lang, *sub.ExpiresAt)
		if left := sub.ExpiresAt.Sub(now); left > 0 {
			days = int((left + 24*time.Hour - 1) / (24 * time.Hour))
		} else if status == "active" || status == "expiring_soon" {
			status = "expired"
		}
		if days > 0 && days <= expiringDays && status == "active" {
			status = "expiring_soon"
		}
	} else {
		d.Expires = t.NoExpiry
	}
	if days > 0 {
		d.DaysLeft = strings.NewReplacer("{n}", i18n.Digits(lang, strconv.Itoa(days))).Replace(map[bool]string{true: t.DayOne, false: t.DayMany}[days == 1])
	}
	d.StatusText = t.Status[status]
	if d.StatusText == "" {
		d.StatusText = status
	}
	switch status {
	case "active":
		d.StatusClass = "s-ok"
	case "expiring_soon":
		d.StatusClass, d.NeedsRenew = "s-warn", true
	case "expired", "depleted":
		d.StatusClass, d.NeedsRenew = "s-bad", true
	default:
		d.StatusClass = "s-muted"
	}

	used := sub.TrafficUsed
	if sub.TrafficTotal != nil && *sub.TrafficTotal > 0 {
		total := *sub.TrafficTotal
		d.TrafficText = i18n.Bytes(lang, total)
		d.HasBar = true
		pct := int(float64(used) / float64(total) * 100)
		d.Percent = min(max(pct, 0), 100)
		d.BarFull = d.Percent >= 100
		d.UsageText = strings.NewReplacer("{used}", i18n.Bytes(lang, used), "{total}", i18n.Bytes(lang, total)).Replace(t.UsedOf)
	} else {
		d.TrafficText = t.Unlimited
		d.UsageText = strings.NewReplacer("{used}", i18n.Bytes(lang, used)).Replace(t.UsedOnly)
	}

	png, err := qrcode.Encode(sub.SubLink, qrcode.Medium, 256)
	if err != nil {
		return nil, err
	}
	d.QR = template.URL("data:image/png;base64," + base64.StdEncoding.EncodeToString(png))

	cat := domain.Texts()
	guide := func(key string) string {
		if v := settings["texts."+lang+"."+key]; v != "" {
			if v, probs := cat.ValidateOverride(lang, key, v); len(probs) == 0 && v != "" {
				return v
			}
		}
		return cat.T(lang, key)
	}
	enc := url.QueryEscape(sub.SubLink)
	// The schemes are the ones the apps document; they are not verified on
	// real devices (docs/15-subscription-page.md).
	d.Apps = []pageApp{
		{"v2rayNG", template.URL("v2rayng://install-config?url=" + enc), guide("subpage.guide.v2rayng")},
		{"Hiddify", template.URL("hiddify://import/" + sub.SubLink), guide("subpage.guide.hiddify")},
		{"Streisand", template.URL("streisand://import/" + sub.SubLink), guide("subpage.guide.streisand")},
		{"V2Box", template.URL("v2box://install-sub?url=" + enc), guide("subpage.guide.v2box")},
	}
	return d, nil
}

// supportContact reads the support setting: "@name" is a Telegram link, an
// https address is a link, anything else is shown as text.
func supportContact(v string) (text, link string) {
	v = strings.TrimSpace(v)
	switch {
	case v == "":
		return "", ""
	case strings.HasPrefix(v, "@") && pageBotRe.MatchString(v[1:]):
		return v, "https://t.me/" + v[1:]
	}
	if u, err := url.Parse(v); err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil {
		return v, v
	}
	return v, ""
}

// onColor picks black or white text for a button of the brand colour.
func onColor(hex string) string {
	n, err := strconv.ParseUint(strings.TrimPrefix(hex, "#"), 16, 32)
	if err != nil {
		return "#ffffff"
	}
	r, g, b := float64(n>>16&0xff), float64(n>>8&0xff), float64(n&0xff)
	if 0.299*r+0.587*g+0.114*b > 150 {
		return "#0b1116"
	}
	return "#ffffff"
}

// pageBase is the address service pages are reached at: the store's public
// URL, or this request's own host when none is configured.
func (s *Server) pageBase(r *http.Request) string {
	if s.cfg.TLS != nil {
		if u := s.cfg.TLS.PublicURL(r.Context()); u != "" {
			return u
		}
	}
	return "https://" + r.Host
}

// pageURL is a service's page address, "" while the page is off or not ready.
func (s *Server) pageURL(r *http.Request, sub *store.Subscription) string {
	if sub.Status == "pending" || sub.Status == "deleted" || sub.PageToken == "" {
		return ""
	}
	if set, err := s.cfg.Domain.SubPage(r.Context()); err != nil || !set.Enabled {
		return ""
	}
	return domain.SubPageURL(s.pageBase(r), sub.PageToken)
}

// regeneratePage: POST /api/v1/services/{id}/page-token/regenerate {reason}.
// The old page address stops working at once.
func (s *Server) regeneratePage(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if _, err := s.cfg.Domain.RegenerateSubscriptionPage(r.Context(), actor(r), id, req.Reason); err != nil {
		s.fail(w, "regenerate page", err)
		return
	}
	s.serviceResult(w, r, id, nil)
}

// subBaseCollides reports whether a server's sub_base_url would put the
// panel's subscription links on BOBRES's own public address under /sub/,
// where core serves the branded pages instead (so the links would answer
// 404). Only the address core can tell is its own is checked.
func (s *Server) subBaseCollides(r *http.Request, subBase string) bool {
	if s.cfg.TLS == nil || strings.TrimSpace(subBase) == "" {
		return false
	}
	return subBaseCollidesWith(s.cfg.TLS.PublicURL(r.Context()), subBase)
}

func subBaseCollidesWith(public, subBase string) bool {
	p, err := url.Parse(public)
	if err != nil || p.Host == "" {
		return false
	}
	u, err := url.Parse(strings.TrimSpace(subBase))
	if err != nil || u.Host == "" {
		return false
	}
	if !strings.EqualFold(u.Hostname(), p.Hostname()) || portOf(u) != portOf(p) {
		return false
	}
	path := "/" + strings.Trim(u.Path, "/") + "/"
	return strings.HasPrefix(path, "/sub/")
}

// portOf is a URL's port, with the scheme's default when it has none.
func portOf(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	if u.Scheme == "http" {
		return "80"
	}
	return "443"
}
