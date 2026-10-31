// Package i18n holds the bot's texts (Persian and English, and Arabic,
// Russian and Turkish in beta) and the formatting rules each language
// expects: Persian and Arabic-Indic digits and separators, Toman amounts,
// Jalali dates for Persian and Gregorian ones for the others.
//
// Texts live in locales/<lang>.json with {name} placeholders and plural
// selectors ({n|form|form…}, see plural.go). An install can override any
// Persian or English text through core settings (key texts.<lang>.<text key>).
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

//go:embed locales/*.json
var localeFS embed.FS

// Languages are the languages a store writes its own content in (plan names,
// broadcasts, help pages, legal pages) and edits the bot's texts in; the
// first is the default.
var Languages = []string{"fa", "en"}

// All are the languages of the built-in catalog: Languages, then the ones in
// Beta. A customer may pick any of them the store enabled (languages.enabled).
var All = []string{"fa", "en", "ar", "ru", "tr"}

// Beta are the machine-translated languages, awaiting a native speaker's review.
var Beta = []string{"ar", "ru", "tr"}

// DefaultEnabled is languages.enabled when the store has not set it: nothing
// changes for a store that had only Persian and English.
const DefaultEnabled = "fa,en"

// RTL reports whether the language is written right to left.
func RTL(lang string) bool {
	l := Normalize(lang)
	return l == "fa" || l == "ar"
}

// enabled are the languages customers may pick, the store's default first;
// nil (a process that never set it, such as core) accepts every language.
var enabled atomic.Pointer[[]string]

// ParseEnabled reads languages.enabled: codes separated by commas or spaces,
// the store's default first. Unknown and repeated codes are left out; when
// nothing is left it is DefaultEnabled.
func ParseEnabled(v string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(strings.ToLower(v), func(r rune) bool { return r == ',' || r == ' ' || r == ';' }) {
		if slices.Contains(All, f) && !slices.Contains(out, f) {
			out = append(out, f)
		}
	}
	if len(out) == 0 {
		return []string{"fa", "en"}
	}
	return out
}

// SetEnabled sets the languages customers may pick (ParseEnabled's list):
// Normalize then maps any other language to the first of them.
func SetEnabled(langs []string) {
	l := slices.Clone(langs)
	if len(l) == 0 {
		l = ParseEnabled(DefaultEnabled)
	}
	enabled.Store(&l)
}

// Enabled are the languages customers may pick, the store's default first.
func Enabled() []string {
	if l := enabled.Load(); l != nil {
		return slices.Clone(*l)
	}
	return slices.Clone(All)
}

// Default is the store's default language.
func Default() string {
	if l := enabled.Load(); l != nil && len(*l) > 0 {
		return (*l)[0]
	}
	return Languages[0]
}

// Choose maps a language code (a Telegram language_code such as "ru" or
// "en-US", or a stored one) to one of enabled; anything else is the first of
// enabled (the store's default), or Persian when enabled is empty.
func Choose(lang string, enabled []string) string {
	lang = strings.ToLower(strings.TrimSpace(lang))
	for _, l := range enabled {
		if lang == l || strings.HasPrefix(lang, l+"-") {
			return l
		}
	}
	if len(enabled) > 0 {
		return enabled[0]
	}
	return Languages[0]
}

// ContentOrder is the order a store's own texts (written in Languages) are
// looked up in for a reader of lang: their language, then English, then
// Persian, so a reader of a beta language gets the English text before the
// Persian one, and a text written in one language only is still shown.
func ContentOrder(lang string) []string {
	out := []string{lang}
	if lang == "fa" {
		return append(out, "en")
	}
	for _, l := range []string{"en", "fa"} {
		if l != lang {
			out = append(out, l)
		}
	}
	return out
}

// Catalog resolves text keys per language, with per-install overrides.
type Catalog struct {
	base map[string]map[string]string

	mu        sync.RWMutex
	overrides map[string]map[string]string
}

// Load reads the embedded catalogs.
func Load() (*Catalog, error) {
	c := &Catalog{base: map[string]map[string]string{}, overrides: map[string]map[string]string{}}
	for _, lang := range All {
		raw, err := localeFS.ReadFile("locales/" + lang + ".json")
		if err != nil {
			return nil, err
		}
		m := map[string]string{}
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, fmt.Errorf("i18n: %s.json: %w", lang, err)
		}
		c.base[lang] = m
	}
	return c, nil
}

// MustLoad is Load for program start-up.
func MustLoad() *Catalog {
	c, err := Load()
	if err != nil {
		panic(err)
	}
	return c
}

// Normalize maps any language code to one the bot speaks to customers: an
// enabled language (SetEnabled), else the store's default. In a process that
// never set the enabled languages, every language of the catalog is one.
func Normalize(lang string) string {
	if l := enabled.Load(); l != nil {
		return Choose(lang, *l)
	}
	return Choose(lang, All)
}

// SetOverrides replaces the per-install overrides from core settings
// (texts.<lang>.<key> -> value). Core checks overrides when they are saved;
// they are checked again here (a newer catalog, a row written by hand), and
// any that would break a message is left out and returned in dropped, so
// the bot keeps working with the default text.
func (c *Catalog) SetOverrides(settings map[string]string) (dropped []string) {
	o := map[string]map[string]string{}
	for k, v := range settings {
		lang, key, ok := SplitTextKey(k)
		if !ok || v == "" {
			continue
		}
		v, probs := c.ValidateOverride(lang, key, v)
		if len(probs) > 0 || v == "" {
			dropped = append(dropped, k)
			continue
		}
		if o[lang] == nil {
			o[lang] = map[string]string{}
		}
		o[lang][key] = v
	}
	c.mu.Lock()
	c.overrides = o
	c.mu.Unlock()
	sort.Strings(dropped)
	return dropped
}

// SplitTextKey reads a settings key texts.<lang>.<text key>.
func SplitTextKey(k string) (lang, key string, ok bool) {
	rest, ok := strings.CutPrefix(k, "texts.")
	if !ok {
		return "", "", false
	}
	lang, key, ok = strings.Cut(rest, ".")
	return lang, key, ok && lang != "" && key != ""
}

// T renders key in lang, replacing {name} with the matching value from
// args (name, value, name, value...) and a plural selector {name|form|…}
// with the form the value's number takes in the language. A missing key
// renders as the key, so a gap is visible instead of silently blank.
func (c *Catalog) T(lang, key string, args ...any) string {
	lang = Normalize(lang)
	c.mu.RLock()
	s, ok := c.overrides[lang][key]
	c.mu.RUnlock()
	if !ok {
		if s, ok = c.base[lang][key]; !ok {
			if s, ok = c.base["en"][key]; !ok {
				return key
			}
		}
	}
	if len(args) == 0 && !strings.Contains(s, "|") {
		return s
	}
	values := make(map[string]string, len(args)/2)
	pairs := make([]string, 0, len(args))
	for i := 0; i+1 < len(args); i += 2 {
		name, value := fmt.Sprint(args[i]), fmt.Sprint(args[i+1])
		values[name] = value
		pairs = append(pairs, "{"+name+"}", value)
	}
	// Plural selectors first: a form holds no placeholder, and a value the
	// replacer puts in is never read as a selector.
	return strings.NewReplacer(pairs...).Replace(selectPlurals(lang, s, values))
}

// Keys returns the keys of a base catalog (tests compare languages).
func (c *Catalog) Keys(lang string) []string {
	out := make([]string, 0, len(c.base[lang]))
	for k := range c.base[lang] {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
