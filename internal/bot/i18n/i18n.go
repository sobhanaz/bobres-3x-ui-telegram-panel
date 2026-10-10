// Package i18n holds the bot's texts (Persian and English) and the
// formatting rules each language expects: Persian digits and separators,
// Toman amounts, Jalali dates.
//
// Texts live in locales/<lang>.json with {name} placeholders. An install can
// override any text through core settings (key texts.<lang>.<text key>).
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
)

//go:embed locales/*.json
var localeFS embed.FS

// Languages the bot speaks; the first is the default.
var Languages = []string{"fa", "en"}

// Catalog resolves text keys per language, with per-install overrides.
type Catalog struct {
	base map[string]map[string]string

	mu        sync.RWMutex
	overrides map[string]map[string]string
}

// Load reads the embedded catalogs.
func Load() (*Catalog, error) {
	c := &Catalog{base: map[string]map[string]string{}, overrides: map[string]map[string]string{}}
	for _, lang := range Languages {
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

// Normalize maps any language code to a supported one.
func Normalize(lang string) string {
	lang = strings.ToLower(strings.TrimSpace(lang))
	for _, l := range Languages {
		if lang == l || strings.HasPrefix(lang, l+"-") {
			return l
		}
	}
	return Languages[0]
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
// args (name, value, name, value...). A missing key renders as the key, so a
// gap is visible instead of silently blank.
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
	if len(args) == 0 {
		return s
	}
	pairs := make([]string, 0, len(args))
	for i := 0; i+1 < len(args); i += 2 {
		pairs = append(pairs, "{"+fmt.Sprint(args[i])+"}", fmt.Sprint(args[i+1]))
	}
	return strings.NewReplacer(pairs...).Replace(s)
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
