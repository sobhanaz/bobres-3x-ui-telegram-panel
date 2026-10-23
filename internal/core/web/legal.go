package web

import (
	"bytes"
	"embed"
	"html/template"
	"net/http"
	"strconv"
	"strings"
)

// The store's own Terms and Privacy pages, GET /terms and GET /privacy: public pages every store serves from its own domain, so that the
// two links the bot, the subscription page and the support screen carry work from the first day (the bot is told these addresses when the
// owner has set none). The text is a general default, in Persian and English, with the store's name and support contact in it; the owner
// edits either text per language in the dashboard (Branding), and the page then shows theirs. The text is a small markdown that is
// escaped before it is shown (legalmd.go).

//go:embed legal/*.md
var legalFiles embed.FS

//go:embed legal.html
var legalHTML string

var legalTmpl = template.Must(template.New("legal").Parse(legalHTML))

// legalKinds are the two pages, in the order the navigation shows them.
var legalKinds = []string{"terms", "privacy"}

// legalKeys are the settings that hold an owner's own text: legal.<kind>.<lang>.
func legalKey(kind, lang string) string { return "legal." + kind + "." + lang }

// legalDefault is the default text of a page in a language, as written (with {store} and {contact} still in it).
func legalDefault(kind, lang string) string {
	b, err := legalFiles.ReadFile("legal/" + kind + "." + lang + ".md")
	if err != nil {
		return ""
	}
	return string(b)
}

// legalContact is how the page names the way to reach the store.
func legalContact(support, lang string) string {
	if s := strings.TrimSpace(support); s != "" {
		return s
	}
	if lang == "fa" {
		return "دکمهٔ پشتیبانی ربات"
	}
	return "the Support button in the bot"
}

// legalText is the text a page shows: the owner's own when they wrote one, else the default, with the store's name and contact put in.
func legalText(settings map[string]string, kind, lang string) string {
	text := strings.TrimSpace(settings[legalKey(kind, lang)])
	if text == "" {
		text = legalDefault(kind, lang)
	}
	return strings.NewReplacer("{store}", brandName(settings), "{contact}", legalContact(settings["branding.support"], lang)).Replace(text)
}

type legalNav struct {
	Href, Label string
	Current     bool
}

type legalData struct {
	Lang, Dir, OtherLang, OtherLangName, OtherLangHref string
	Title, Brand, Color, Logo                          string
	Support, SupportURL, SupportLabel                  string
	PoweredBy                                          bool
	Body                                               template.HTML
	Nav                                                []legalNav
}

var legalTitles = map[string]map[string]string{
	"fa": {"terms": "قوانین", "privacy": "حریم خصوصی"},
	"en": {"terms": "Terms", "privacy": "Privacy"},
}

func (s *Server) termsPage(w http.ResponseWriter, r *http.Request)   { s.legalPage(w, r, "terms") }
func (s *Server) privacyPage(w http.ResponseWriter, r *http.Request) { s.legalPage(w, r, "privacy") }

// legalPage serves a page. It is public and has no session; it is rate limited per client address like the subscription page.
func (s *Server) legalPage(w http.ResponseWriter, r *http.Request, kind string) {
	ctx := r.Context()
	if !s.subPages.allow(clientKey(r), s.now()) {
		pageHeaders(w)
		w.Header().Set("Retry-After", "60")
		http.Error(w, "too many requests", http.StatusTooManyRequests)
		return
	}
	settings, err := s.cfg.Domain.Settings(ctx)
	if err != nil {
		s.cfg.Log.Error("legal page settings", "err", err)
		pageHeaders(w)
		http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
		return
	}
	lang := "fa"
	if r.URL.Query().Get("l") == "en" {
		lang = "en"
	}
	other, otherName, dir := "en", "English", "rtl"
	if lang == "en" {
		other, otherName, dir = "fa", "فارسی", "ltr"
	}
	color := strings.ToLower(settings["branding.color"])
	if !pageColorRe.MatchString(color) {
		color = defaultColor
	}
	d := &legalData{
		Lang: lang, Dir: dir, OtherLang: other, OtherLangName: otherName, OtherLangHref: "/" + kind + "?l=" + other,
		Title: legalTitles[lang][kind], Brand: brandName(settings), Color: color, Logo: s.logoURL(ctx), PoweredBy: s.poweredBy(ctx),
		Body: renderLegal(legalText(settings, kind, lang)), SupportLabel: pageTexts[lang].Support,
	}
	d.Support, d.SupportURL = supportContact(settings["branding.support"])
	for _, k := range legalKinds {
		href := "/" + k
		if lang == "en" {
			href += "?l=en"
		}
		d.Nav = append(d.Nav, legalNav{Href: href, Label: legalTitles[lang][k], Current: k == kind})
	}
	var buf bytes.Buffer
	if err := legalTmpl.Execute(&buf, d); err != nil {
		s.cfg.Log.Error("legal page render", "err", err)
		pageHeaders(w)
		http.Error(w, "temporarily unavailable", http.StatusInternalServerError)
		return
	}
	pageHeaders(w)
	w.Header().Set("Content-Length", strconv.Itoa(buf.Len()))
	_, _ = w.Write(buf.Bytes())
}
