package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The pages in site/legal are what this tool writes from docs/legal now: a
// change to a draft without running the tool fails here.
func TestPagesMatchTheDrafts(t *testing.T) {
	files, err := build(filepath.Join("..", "..", "docs", "legal"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2*len(docs)+2 {
		t.Fatalf("%d pages for %d drafts", len(files), len(docs))
	}
	for name, want := range files {
		got, err := os.ReadFile(filepath.Join("..", "..", "site", "legal", name))
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("site/legal/%s is missing or out of date (%v); run: go run ./tools/sitelegal", name, err)
		}
	}
}

var relMD = regexp.MustCompile(`href="[^":]*\.md"`)

// Every page says it is a draft, stays out of search results, and the site's
// sitemap does not list it, until the owner publishes the reviewed texts.
func TestPagesAreMarkedAsDrafts(t *testing.T) {
	files, err := build(filepath.Join("..", "..", "docs", "legal"))
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range files {
		s := string(data)
		if !strings.Contains(s, `<meta name="robots" content="noindex">`) || !strings.Contains(s, `class="draft-banner"`) {
			t.Errorf("%s: no noindex or draft banner", name)
		}
		if relMD.MatchString(s) || strings.Contains(s, "href=\"../../") {
			t.Errorf("%s: a link still points at a Markdown file or outside the site", name)
		}
	}
	sm, err := os.ReadFile(filepath.Join("..", "..", "site", "sitemap.xml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(sm), "legal") {
		t.Error("the sitemap lists the draft legal pages")
	}
}

func TestRender(t *testing.T) {
	md := "# T <x>\n\n> a\n> b\n\nText **bold** `a<b` [l](../pricing.md) [s](terms-of-service.fa.md).\nnext\n\n- one\n  more\n- two\n\n1. x\n2. y\n\n| A | B `|` |\n|---|---|\n| 1 | **2** |\n"
	got := render(md)
	for _, want := range []string{
		"<h1>T &lt;x&gt;</h1>", "<blockquote><p>a b</p></blockquote>", "<strong>bold</strong>", "<code>a&lt;b</code>",
		`href="` + repoBlob + `docs/pricing.md"`, `href="terms-of-service.fa.html"`, "next</p>",
		"<li>one more</li>", "<li>two</li>", "<ol>", "<th>B <code>|</code></th>", "<td><strong>2</strong></td>",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if strings.Contains(render("<script>alert(1)</script>"), "<script>") {
		t.Error("markup in a draft is not escaped")
	}
}
