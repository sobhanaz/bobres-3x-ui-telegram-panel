package web

import (
	"html"
	"html/template"
	"regexp"
	"strings"
)

// renderLegal turns the small markdown a legal page is written in into HTML. It knows headings (# ## ###), paragraphs, bullet lists (- ),
// **bold**, *italic* and [text](https://...) links, and nothing else: every character of the text is escaped first, so a page can never carry
// a tag, a script or a link that is not https.
func renderLegal(src string) template.HTML {
	var out strings.Builder
	var para []string
	inList := false
	flushPara := func() {
		if len(para) > 0 {
			out.WriteString("<p>" + inlineLegal(strings.Join(para, " ")) + "</p>\n")
			para = nil
		}
	}
	closeList := func() {
		if inList {
			out.WriteString("</ul>\n")
			inList = false
		}
	}
	for _, line := range strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "":
			flushPara()
			closeList()
		case strings.HasPrefix(trimmed, "### "):
			flushPara()
			closeList()
			out.WriteString("<h3>" + inlineLegal(trimmed[4:]) + "</h3>\n")
		case strings.HasPrefix(trimmed, "## "):
			flushPara()
			closeList()
			out.WriteString("<h2>" + inlineLegal(trimmed[3:]) + "</h2>\n")
		case strings.HasPrefix(trimmed, "# "):
			flushPara()
			closeList()
			out.WriteString("<h1>" + inlineLegal(trimmed[2:]) + "</h1>\n")
		case strings.HasPrefix(trimmed, "- "):
			flushPara()
			if !inList {
				out.WriteString("<ul>\n")
				inList = true
			}
			out.WriteString("<li>" + inlineLegal(trimmed[2:]) + "</li>\n")
		default:
			closeList()
			para = append(para, trimmed)
		}
	}
	flushPara()
	closeList()
	return template.HTML(out.String()) //nolint:gosec // every character was escaped by inlineLegal; only the tags above are made here
}

var (
	legalBoldRe   = regexp.MustCompile(`\*\*([^*\n]+)\*\*`)
	legalItalicRe = regexp.MustCompile(`\*([^*\n]+)\*`)
	// the text was escaped first, so a link's address has no quote or angle bracket left in it
	legalLinkRe = regexp.MustCompile(`\[([^\]\n]+)\]\((https://[^\s)"<>]+)\)`)
)

// inlineLegal escapes a line of text and then marks up bold, italic and https links.
func inlineLegal(s string) string {
	s = html.EscapeString(s)
	s = legalLinkRe.ReplaceAllString(s, `<a href="$2" rel="noopener noreferrer">$1</a>`)
	s = legalBoldRe.ReplaceAllString(s, "<strong>$1</strong>")
	s = legalItalicRe.ReplaceAllString(s, "<em>$1</em>")
	return s
}
