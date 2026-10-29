package web

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/monitor"
)

// The public status page, GET /status (a page for a person) and GET /status.json (for a monitoring service), docs/04-dashboard.md
// ("The public status page"). It is off until the owner turns on status_page.enabled; off, both answer 404 like any address that
// does not exist.
//
// It says only whether the store works: one verdict, "all systems working" or "some systems are down", and the state of four
// generic parts: Dashboard, Bot, Payments and Panels. It never names a server, a version, an address, an error or a count (the
// answer is built from monitor.PublicStatus, which holds nothing else). It needs no session; it is rate limited per client
// address, and everything it shows is cached for 30 seconds, so a crowd of visitors costs one look at the stack.

//go:embed status.html
var statusHTML string

var statusTmpl = template.Must(template.New("status").Parse(statusHTML))

const (
	// statusRate is how many requests of the status page one client address may make a minute.
	statusRate = 60
	// statusTTL is how long a look at the stack is kept before the next request makes a new one.
	statusTTL = 30 * time.Second
	// statusRefresh is the seconds after which the page asks again by itself.
	statusRefresh = 60
)

// statusSnap is the rendered answers of one look at the stack, never changed once made.
type statusSnap struct {
	at   time.Time
	json []byte
	html map[string][]byte // by language
}

// statusState keeps the last look (mu guards it).
type statusState struct {
	mu   sync.Mutex
	snap *statusSnap
}

// statusText is the page's wording, per language; the part names are fixed and general.
type statusText struct {
	Title, AllOK, SomeDown, Updated, Working, Degraded, Down string
	Names                                                    map[string]string
}

var statusTexts = map[string]statusText{
	"en": {
		Title: "Service status", AllOK: "All systems working", SomeDown: "Some systems are down", Updated: "Updated {time}",
		Working: "Working", Degraded: "Degraded", Down: "Down",
		Names: map[string]string{"dashboard": "Dashboard", "bot": "Bot", "payments": "Payments", "panels": "Panels"},
	},
	"fa": {
		Title: "وضعیت سرویس", AllOK: "همهٔ سیستم\u200cها کار می\u200cکنند", SomeDown: "برخی سیستم\u200cها از کار افتاده\u200cاند",
		Updated: "به\u200cروزرسانی: {time}", Working: "کار می\u200cکند", Degraded: "عملکرد ناقص", Down: "از کار افتاده",
		Names: map[string]string{"dashboard": "داشبورد", "bot": "ربات", "payments": "پرداخت\u200cها", "panels": "پنل\u200cها"},
	},
}

type statusRow struct{ Name, Label, Class, Icon string }

type statusPageData struct {
	Lang, Dir, OtherLang, OtherLangName, OtherLangHref string
	Title, Brand, Color, Logo                          string
	Overall, OverallIcon, Headline                     string
	Rows                                               []statusRow
	Updated                                            string
	Support, SupportURL, SupportLabel                  string
	PoweredBy                                          bool
	Refresh                                            int
}

// statusIcons are shapes besides the colours, so the states can be told apart without telling the colours apart.
var statusIcons = map[string]string{monitor.PublicOK: "✓", monitor.PublicDegraded: "!", monitor.PublicDown: "✕"}

// statusPageLang is the language of a request: Persian unless ?l=en (as the Terms and Privacy pages).
func statusPageLang(r *http.Request) string {
	if r.URL.Query().Get("l") == "en" {
		return "en"
	}
	return "fa"
}

// renderStatus draws the page of a status in a language.
func (s *Server) renderStatus(ctx context.Context, settings map[string]string, st *monitor.PublicStatus, lang string) ([]byte, error) {
	t := statusTexts[lang]
	other, otherName, dir := "en", "English", "rtl"
	if lang == "en" {
		other, otherName, dir = "fa", "فارسی", "ltr"
	}
	color := strings.ToLower(settings["branding.color"])
	if !pageColorRe.MatchString(color) {
		color = defaultColor
	}
	otherHref := "/status"
	if other == "en" {
		otherHref += "?l=en"
	}
	d := &statusPageData{
		Lang: lang, Dir: dir, OtherLang: other, OtherLangName: otherName, OtherLangHref: otherHref,
		Title: t.Title, Brand: brandName(settings), Color: color, Logo: s.logoURL(ctx), PoweredBy: s.poweredBy(ctx),
		Overall: st.Status, OverallIcon: statusIcons[st.Status], Headline: t.AllOK, SupportLabel: pageTexts[lang].Support,
		Updated: strings.Replace(t.Updated, "{time}", i18n.DateTime(lang, time.Unix(st.UpdatedAt, 0)), 1), Refresh: statusRefresh,
	}
	if st.Status != monitor.PublicOK {
		d.Headline = t.SomeDown
	}
	d.Support, d.SupportURL = supportContact(settings["branding.support"])
	for _, c := range st.Components {
		label := map[string]string{monitor.PublicOK: t.Working, monitor.PublicDegraded: t.Degraded, monitor.PublicDown: t.Down}[c.Status]
		d.Rows = append(d.Rows, statusRow{Name: t.Names[c.ID], Label: label, Class: c.Status, Icon: statusIcons[c.Status]})
	}
	var buf bytes.Buffer
	if err := statusTmpl.Execute(&buf, d); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// statusSnapshot is the answers of the last look at the stack, made again when it is older than statusTTL.
func (s *Server) statusSnapshot(ctx context.Context, settings map[string]string) (*statusSnap, error) {
	s.status.mu.Lock()
	defer s.status.mu.Unlock()
	now := s.now()
	if c := s.status.snap; c != nil && !now.Before(c.at) && now.Sub(c.at) < statusTTL {
		return c, nil
	}
	st := s.cfg.Monitor.PublicStatus(ctx)
	raw, err := json.Marshal(st)
	if err != nil {
		return nil, err
	}
	pages := make(map[string][]byte, len(statusTexts))
	for lang := range statusTexts {
		page, err := s.renderStatus(ctx, settings, st, lang)
		if err != nil {
			return nil, err
		}
		pages[lang] = page
	}
	s.status.snap = &statusSnap{at: now, json: raw, html: pages}
	return s.status.snap, nil
}

func (s *Server) statusPage(w http.ResponseWriter, r *http.Request) { s.servePublicStatus(w, r, false) }
func (s *Server) statusJSON(w http.ResponseWriter, r *http.Request) { s.servePublicStatus(w, r, true) }

// servePublicStatus answers GET /status and GET /status.json.
func (s *Server) servePublicStatus(w http.ResponseWriter, r *http.Request, asJSON bool) {
	notFound := func() {
		if asJSON {
			http.NotFound(w, r)
			return
		}
		s.pageNotFound(w, r)
	}
	if s.cfg.Monitor == nil {
		notFound()
		return
	}
	if !s.statusPages.allow(clientKey(r), s.now()) {
		pageHeaders(w)
		w.Header().Set("Retry-After", "60")
		http.Error(w, "too many requests", http.StatusTooManyRequests)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), monitorTimeout)
	defer cancel()
	settings, err := s.cfg.Domain.Settings(ctx)
	if err != nil {
		s.cfg.Log.Error("status page settings", "err", err)
		pageHeaders(w)
		http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
		return
	}
	if !domain.StatusPageFrom(settings) {
		notFound()
		return
	}
	snap, err := s.statusSnapshot(ctx, settings)
	if err != nil {
		s.cfg.Log.Error("status page", "err", err)
		pageHeaders(w)
		http.Error(w, "temporarily unavailable", http.StatusInternalServerError)
		return
	}
	body := snap.json
	if asJSON {
		h := w.Header()
		h.Set("Content-Type", "application/json; charset=utf-8")
		h.Set("X-Content-Type-Options", "nosniff")
		// The payload is public and holds nothing of the store's: a page of another site may read it.
		h.Set("Access-Control-Allow-Origin", "*")
	} else {
		body = snap.html[statusPageLang(r)]
		pageHeaders(w)
	}
	w.Header().Set("Cache-Control", "public, max-age="+strconv.Itoa(int(statusTTL.Seconds())))
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	_, _ = w.Write(body)
}
