// Command sitelegal turns the legal drafts in docs/legal into the draft pages
// under site/legal (English and Persian), so the owner can read them in a
// browser and a lawyer can be pointed at an address.
//
// The pages are drafts on purpose: a banner says so, they carry
// <meta name="robots" content="noindex"> and the sitemap does not list them.
// Publishing them for real is a decision for after the lawyer's review (see
// docs/launch-checklist.md); this tool only makes sure the pages and the
// Markdown never drift apart (see main_test.go).
//
// Usage, from the repository root:
//
//	go run ./tools/sitelegal          write site/legal
//	go run ./tools/sitelegal -check   fail if site/legal is out of date
package main

import (
	"bytes"
	"flag"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const repoBlob = "https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/"

// doc is one legal text. The Persian text lives in <slug>.fa.md.
type doc struct{ slug, en, fa string }

var docs = []doc{
	{"terms-of-service", "Terms of Service", "شرایط استفاده"},
	{"privacy-policy", "Privacy Policy", "سیاست حریم خصوصی"},
	{"acceptable-use", "Acceptable Use Policy", "سیاست استفادهٔ مجاز"},
	{"refund-policy", "Refund policy for licenses", "سیاست بازپرداخت لایسنس"},
}

func main() {
	check := flag.Bool("check", false, "fail if the pages are out of date instead of writing them")
	src := flag.String("src", "docs/legal", "folder with the Markdown drafts")
	out := flag.String("out", "site/legal", "folder for the pages")
	flag.Parse()
	files, err := build(*src)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sitelegal:", err)
		os.Exit(1)
	}
	if *check {
		bad := 0
		for name, want := range files {
			got, err := os.ReadFile(filepath.Join(*out, name))
			if err != nil || !bytes.Equal(got, want) {
				fmt.Fprintf(os.Stderr, "sitelegal: %s is out of date; run: go run ./tools/sitelegal\n", filepath.Join(*out, name))
				bad++
			}
		}
		if bad > 0 {
			os.Exit(1)
		}
		return
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "sitelegal:", err)
		os.Exit(1)
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(*out, name), data, 0o644); err != nil { //nolint:gosec // static site pages, meant to be world-readable
			fmt.Fprintln(os.Stderr, "sitelegal:", err)
			os.Exit(1)
		}
	}
}

// build returns every page, keyed by file name.
func build(src string) (map[string][]byte, error) {
	files := map[string][]byte{}
	for _, d := range docs {
		for _, fa := range []bool{false, true} {
			name := d.slug + ".md"
			if fa {
				name = d.slug + ".fa.md"
			}
			md, err := os.ReadFile(filepath.Join(src, name))
			if err != nil {
				return nil, err
			}
			title := d.en
			if fa {
				title = d.fa
			}
			page := strings.TrimSuffix(name, ".md") + ".html"
			files[page] = []byte(pageHTML(title, render(string(md)), fa, d.slug))
		}
	}
	files["index.html"] = []byte(indexHTML(false))
	files["index.fa.html"] = []byte(indexHTML(true))
	return files, nil
}

const head = `<!DOCTYPE html>
<html lang="%s" dir="%s">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex">
<title>%s · BOBRES</title>
<link rel="icon" href="../assets/brand/bobres-icon.svg" type="image/svg+xml">
<link rel="stylesheet" href="../assets/site.css">
<link rel="stylesheet" href="../assets/legal.css">
</head>
<body>
<main class="legal">
<p class="draft-banner" role="note">%s</p>
`

func banner(fa bool) string {
	if fa {
		return "پیش\u200cنویس. این متن هنوز بازبینی حقوقی نشده است و مشورت حقوقی نیست. عبارت\u200cهای «[OWNER: …]» و «[LAWYER: …]» جای خالی\u200cاند، نه متن نهایی."
	}
	return "DRAFT. A lawyer has not reviewed this text, and it is not legal advice. The “[OWNER: …]” and “[LAWYER: …]” parts are blanks, not final wording."
}

func lang(fa bool) (l, dir string) {
	if fa {
		return "fa", "rtl"
	}
	return "en", "ltr"
}

func pageHTML(title, body string, fa bool, slug string) string {
	l, dir := lang(fa)
	other, otherName, back := slug+".fa.html", "فارسی", "All legal drafts"
	idx := "./"
	if fa {
		other, otherName, back = slug+".html", "English", "همهٔ پیش\u200cنویس\u200cهای حقوقی"
		idx = "index.fa.html"
	}
	var b strings.Builder
	fmt.Fprintf(&b, head, l, dir, html.EscapeString(title), html.EscapeString(banner(fa)))
	b.WriteString(body)
	fmt.Fprintf(&b, "<p class=\"legal-nav\"><a href=\"%s\">%s</a> · <a href=\"%s\" hreflang=\"%s\">%s</a></p>\n</main>\n</body>\n</html>\n",
		idx, back, other, map[bool]string{true: "en", false: "fa"}[fa], otherName)
	return b.String()
}

func indexHTML(fa bool) string {
	l, dir := lang(fa)
	title, intro, other, otherName := "Legal drafts", "Drafts of the texts BOBRES operators and the vendor agree to. A lawyer must review each before the public launch.", "index.fa.html", "فارسی"
	if fa {
		title, intro, other, otherName = "پیش\u200cنویس\u200cهای حقوقی", "پیش\u200cنویس متن\u200cهایی که اپراتورهای BOBRES و فروشنده می\u200cپذیرند. هر کدام باید پیش از انتشار عمومی از وکیل بازبینی بگیرد.", "./", "English"
	}
	var b strings.Builder
	fmt.Fprintf(&b, head, l, dir, html.EscapeString(title), html.EscapeString(banner(fa)))
	fmt.Fprintf(&b, "<h1>%s</h1>\n<p>%s</p>\n<ul>\n", html.EscapeString(title), html.EscapeString(intro))
	for _, d := range docs {
		href, name := d.slug+".html", d.en
		if fa {
			href, name = d.slug+".fa.html", d.fa
		}
		fmt.Fprintf(&b, "<li><a href=\"%s\">%s</a></li>\n", href, html.EscapeString(name))
	}
	b.WriteString("</ul>\n")
	fmt.Fprintf(&b, "<p class=\"legal-nav\"><a href=\"%s\" hreflang=\"%s\">%s</a></p>\n</main>\n</body>\n</html>\n",
		other, map[bool]string{true: "en", false: "fa"}[fa], otherName)
	return b.String()
}

var (
	reHeading = regexp.MustCompile(`^(#{1,6}) +(.*)$`)
	reOL      = regexp.MustCompile(`^\d+\. +(.*)$`)
	reUL      = regexp.MustCompile(`^[-*] +(.*)$`)
	reCode    = regexp.MustCompile("`([^`]+)`")
	reBold    = regexp.MustCompile(`\*\*(.+?)\*\*`)
	reLink    = regexp.MustCompile(`\[([^\]]+)\]\(([^)\s]+)\)`)
	reSep     = regexp.MustCompile(`^\|?[\s:|-]+\|?$`)
)

// render converts the Markdown subset the drafts use: headings, paragraphs,
// block quotes, bullet and numbered lists (an indented line continues the
// item above), tables, code spans, bold and links.
func render(md string) string {
	lines := strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n")
	var b strings.Builder
	for i := 0; i < len(lines); {
		line := lines[i]
		trim := strings.TrimSpace(line)
		switch {
		case trim == "":
			i++
		case reHeading.MatchString(line):
			m := reHeading.FindStringSubmatch(line)
			fmt.Fprintf(&b, "<h%d>%s</h%d>\n", len(m[1]), inline(m[2]), len(m[1]))
			i++
		case strings.HasPrefix(line, ">"):
			var parts []string
			for i < len(lines) && strings.HasPrefix(lines[i], ">") {
				parts = append(parts, strings.TrimSpace(strings.TrimPrefix(lines[i], ">")))
				i++
			}
			fmt.Fprintf(&b, "<blockquote><p>%s</p></blockquote>\n", inline(strings.Join(parts, " ")))
		case strings.HasPrefix(trim, "|") && i+1 < len(lines) && reSep.MatchString(strings.TrimSpace(lines[i+1])) && strings.Contains(lines[i+1], "-"):
			i = table(&b, lines, i)
		case reUL.MatchString(line) || reOL.MatchString(line):
			i = list(&b, lines, i)
		default:
			var parts []string
			for i < len(lines) && strings.TrimSpace(lines[i]) != "" && !reHeading.MatchString(lines[i]) &&
				!strings.HasPrefix(lines[i], ">") && !reUL.MatchString(lines[i]) && !reOL.MatchString(lines[i]) {
				parts = append(parts, strings.TrimSpace(lines[i]))
				i++
			}
			fmt.Fprintf(&b, "<p>%s</p>\n", inline(strings.Join(parts, " ")))
		}
	}
	return b.String()
}

func list(b *strings.Builder, lines []string, i int) int {
	tag, re := "ul", reUL
	if reOL.MatchString(lines[i]) {
		tag, re = "ol", reOL
	}
	fmt.Fprintf(b, "<%s>\n", tag)
	for i < len(lines) && re.MatchString(lines[i]) {
		text := []string{re.FindStringSubmatch(lines[i])[1]}
		i++
		for i < len(lines) && strings.TrimSpace(lines[i]) != "" && (strings.HasPrefix(lines[i], " ") || strings.HasPrefix(lines[i], "\t")) {
			text = append(text, strings.TrimSpace(lines[i]))
			i++
		}
		fmt.Fprintf(b, "<li>%s</li>\n", inline(strings.Join(text, " ")))
		// Blank lines between items keep the list going.
		for j := i; j < len(lines) && strings.TrimSpace(lines[j]) == ""; j++ {
			if j+1 < len(lines) && re.MatchString(lines[j+1]) {
				i = j + 1
				break
			}
		}
	}
	fmt.Fprintf(b, "</%s>\n", tag)
	return i
}

func cells(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "|")
	line = strings.TrimSuffix(line, "|")
	var out []string
	var cur strings.Builder
	inCode := false
	for _, r := range line {
		switch {
		case r == '`':
			inCode = !inCode
			cur.WriteRune(r)
		case r == '|' && !inCode:
			out = append(out, strings.TrimSpace(cur.String()))
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	return append(out, strings.TrimSpace(cur.String()))
}

func table(b *strings.Builder, lines []string, i int) int {
	b.WriteString("<div class=\"table-wrap\"><table>\n<thead><tr>")
	for _, c := range cells(lines[i]) {
		fmt.Fprintf(b, "<th>%s</th>", inline(c))
	}
	b.WriteString("</tr></thead>\n<tbody>\n")
	for i += 2; i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), "|"); i++ {
		b.WriteString("<tr>")
		for _, c := range cells(lines[i]) {
			fmt.Fprintf(b, "<td>%s</td>", inline(c))
		}
		b.WriteString("</tr>\n")
	}
	b.WriteString("</tbody></table></div>\n")
	return i
}

// inline escapes the text first, then marks it up, so no draft can inject
// markup. Code spans are lifted out so their content is left alone.
func inline(s string) string {
	var codes []string
	s = reCode.ReplaceAllStringFunc(s, func(m string) string {
		codes = append(codes, reCode.FindStringSubmatch(m)[1])
		return fmt.Sprintf("\x00%d\x00", len(codes)-1)
	})
	s = html.EscapeString(s)
	s = reLink.ReplaceAllStringFunc(s, func(m string) string {
		p := reLink.FindStringSubmatch(m)
		return fmt.Sprintf("<a href=\"%s\">%s</a>", href(p[2]), p[1])
	})
	s = reBold.ReplaceAllString(s, "<strong>$1</strong>")
	for i, c := range codes {
		s = strings.Replace(s, fmt.Sprintf("\x00%d\x00", i), "<code>"+html.EscapeString(c)+"</code>", 1)
	}
	return s
}

// href points a link of the Markdown to where it works on the site: a sibling
// draft to its page, anything else in the repository to GitHub.
func href(u string) string {
	if strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "#") || strings.HasPrefix(u, "mailto:") {
		return u
	}
	for _, d := range docs {
		switch u {
		case d.slug + ".md":
			return d.slug + ".html"
		case d.slug + ".fa.md":
			return d.slug + ".fa.html"
		}
	}
	// Relative to docs/legal: resolve against the repository.
	parts := []string{"docs", "legal"}
	for _, p := range strings.Split(u, "/") {
		switch p {
		case "..":
			if len(parts) > 0 {
				parts = parts[:len(parts)-1]
			}
		case ".", "":
		default:
			parts = append(parts, p)
		}
	}
	return repoBlob + strings.Join(parts, "/")
}
