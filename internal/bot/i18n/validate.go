package i18n

import (
	"html"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

// Problem is one reason an override cannot be used. Arg names the
// placeholder, tag, character or limit concerned.
type Problem struct {
	Code string
	Arg  string
}

// Problem codes.
const (
	ProblemUnknownKey  = "unknown_key"
	ProblemLangUnused  = "lang_unused"
	ProblemBadUTF8     = "bad_utf8"
	ProblemControlChar = "control_char"
	ProblemMissing     = "placeholder_missing"
	ProblemUnknown     = "placeholder_unknown"
	ProblemTagNotAllow = "tag_not_allowed"
	ProblemTagUnclosed = "tag_unclosed"
	ProblemTagMismatch = "tag_mismatch"
	ProblemTagInCode   = "tag_in_code"
	ProblemBadAttr     = "bad_attr"
	ProblemBadLink     = "bad_link"
	ProblemBadEntity   = "bad_entity"
	ProblemMarkup      = "markup_not_allowed"
	ProblemTooLong     = "too_long"
)

// allowedTags are the Telegram HTML tags an override may use.
var allowedTags = map[string]bool{
	"b": true, "strong": true, "i": true, "em": true, "u": true, "ins": true,
	"s": true, "strike": true, "del": true, "code": true, "pre": true,
	"blockquote": true, "tg-spoiler": true, "span": true, "a": true,
}

var (
	// looseBraceRe finds {...} that is not a well-formed placeholder.
	looseBraceRe = regexp.MustCompile(`\{[^{}\n]{0,40}\}`)
	tagRe        = regexp.MustCompile(`^<(/?)([a-zA-Z][a-zA-Z0-9-]*)((?:\s+[a-zA-Z-]+(?:\s*=\s*"[^"<>]*")?)*)\s*>`)
	attrRe       = regexp.MustCompile(`([a-zA-Z-]+)(?:\s*=\s*"([^"]*)")?`)
	entityRe     = regexp.MustCompile(`^&(?:lt|gt|amp|quot|#[0-9]{1,7}|#x[0-9a-fA-F]{1,6});`)
)

// NormalizeText is how an override is stored: line breaks as \n, no
// surrounding space.
func NormalizeText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.TrimSpace(s)
}

// ValidateOverride checks a replacement for the text key in lang and
// returns it normalised. An empty value means "back to the default" and is
// always fine. Placeholders must be exactly those of the default text, and
// the text must be safe where Telegram shows it.
func (c *Catalog) ValidateOverride(lang, key, value string) (string, []Problem) {
	info, ok := c.Info(key)
	if !ok {
		return "", []Problem{{ProblemUnknownKey, key}}
	}
	if !slices.Contains(info.Langs, lang) {
		return "", []Problem{{ProblemLangUnused, lang}}
	}
	if !utf8.ValidString(value) {
		return "", []Problem{{ProblemBadUTF8, ""}}
	}
	value = NormalizeText(value)
	if value == "" {
		return "", nil
	}
	var probs []Problem
	add := func(code, arg string) {
		p := Problem{code, arg}
		if !slices.Contains(probs, p) {
			probs = append(probs, p)
		}
	}
	for _, r := range value {
		if r != '\n' && r != '\t' && (unicode.IsControl(r) || r == ' ' || r == ' ') {
			add(ProblemControlChar, strconv.QuoteRune(r))
		}
	}

	have := placeholders(value)
	for _, p := range info.Placeholders {
		if !slices.Contains(have, p) {
			add(ProblemMissing, p)
		}
	}
	for _, p := range have {
		if !slices.Contains(info.Placeholders, p) {
			add(ProblemUnknown, p)
		}
	}
	for _, m := range looseBraceRe.FindAllString(value, -1) {
		if placeholderRe.FindString(m) != m {
			add(ProblemUnknown, m)
		}
	}

	if info.Context.html() {
		for _, p := range checkHTML(render(key, value)) {
			add(p.Code, p.Arg)
		}
	} else {
		for _, ch := range []string{"<", ">", "&"} {
			if strings.Contains(value, ch) {
				add(ProblemMarkup, ch)
			}
		}
	}

	if n := TextLength(value); n > info.Max {
		add(ProblemTooLong, strconv.Itoa(info.Max))
	}
	return value, probs
}

// render fills the placeholders with samples: values that carry tags get a
// tag, so a text cannot put them inside <code> or an attribute.
func render(key, s string) string {
	markup := markupArgs[key]
	return placeholderRe.ReplaceAllStringFunc(s, func(m string) string {
		if slices.Contains(markup, m[1:len(m)-1]) {
			return "<b>x</b>"
		}
		return "x"
	})
}

// checkHTML checks text against Telegram's HTML rules: only the allowed
// tags, properly nested, nothing inside code or pre (but pre > code), only
// the four named entities, links to https or tg only.
func checkHTML(s string) []Problem {
	var probs []Problem
	var stack []string
	inCode := func() bool {
		for _, t := range stack {
			if t == "code" || t == "pre" {
				return true
			}
		}
		return false
	}
	for i := 0; i < len(s); {
		switch s[i] {
		case '<':
			m := tagRe.FindStringSubmatch(s[i:])
			if m == nil {
				probs = append(probs, Problem{ProblemBadEntity, "<"})
				i++
				continue
			}
			closing, name, attrs := m[1] == "/", strings.ToLower(m[2]), strings.TrimSpace(m[3])
			i += len(m[0])
			if !allowedTags[name] {
				probs = append(probs, Problem{ProblemTagNotAllow, name})
				continue
			}
			if closing {
				if len(stack) == 0 || stack[len(stack)-1] != name {
					probs = append(probs, Problem{ProblemTagMismatch, name})
					continue
				}
				stack = stack[:len(stack)-1]
				continue
			}
			preCode := name == "code" && len(stack) > 0 && stack[len(stack)-1] == "pre" // <pre><code> is allowed
			if inCode() && !preCode {
				probs = append(probs, Problem{ProblemTagInCode, name})
			}
			if p := checkAttrs(name, attrs); p != nil {
				probs = append(probs, *p)
			}
			stack = append(stack, name)
		case '>':
			probs = append(probs, Problem{ProblemBadEntity, ">"})
			i++
		case '&':
			m := entityRe.FindString(s[i:])
			if m == "" {
				probs = append(probs, Problem{ProblemBadEntity, "&"})
				i++
				continue
			}
			i += len(m)
		default:
			i++
		}
	}
	for _, t := range stack {
		probs = append(probs, Problem{ProblemTagUnclosed, t})
	}
	return probs
}

func checkAttrs(tag, attrs string) *Problem {
	got := map[string]string{}
	for _, m := range attrRe.FindAllStringSubmatch(attrs, -1) {
		got[strings.ToLower(m[1])] = m[2]
	}
	switch tag {
	case "a":
		href, ok := got["href"]
		if !ok || len(got) != 1 {
			return &Problem{ProblemBadAttr, tag}
		}
		if !strings.HasPrefix(href, "https://") && !strings.HasPrefix(href, "tg://") || len(href) < 9 {
			return &Problem{ProblemBadLink, href}
		}
	case "span":
		if len(got) != 1 || got["class"] != "tg-spoiler" {
			return &Problem{ProblemBadAttr, tag}
		}
	case "blockquote":
		if _, ok := got["expandable"]; len(got) > 1 || len(got) == 1 && !ok {
			return &Problem{ProblemBadAttr, tag}
		}
	default:
		if len(got) > 0 {
			return &Problem{ProblemBadAttr, tag}
		}
	}
	return nil
}

var stripTagsRe = regexp.MustCompile(`<[^<>]*>`)

// TextLength counts a text the way Telegram limits it: UTF-16 units of the
// text without tags, with each entity as the character it stands for.
func TextLength(s string) int {
	return len(utf16.Encode([]rune(html.UnescapeString(stripTagsRe.ReplaceAllString(s, "")))))
}

// StripHTML turns an HTML message into the plain text Telegram would show:
// the fallback when Telegram refuses the markup.
func StripHTML(s string) string {
	s = stripTagsRe.ReplaceAllString(s, "")
	return strings.NewReplacer("&lt;", "<", "&gt;", ">", "&quot;", `"`, "&amp;", "&").Replace(s)
}
