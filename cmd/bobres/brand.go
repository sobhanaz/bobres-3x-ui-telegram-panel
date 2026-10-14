package main

import (
	"fmt"
	"runtime/debug"
	"strings"
	"unicode/utf8"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/version"
)

// The words and links of the banner. install/install.sh keeps the same ones in
// the block at its top (TestInstallShBrandMatchesGo fails when they differ).
const (
	brandCopyright = "© 2026 BOBRES, all rights reserved"
	brandMadeBy    = "Made by TECSO-DEV"
	brandTagline   = "Your VPN store on Telegram"
	brandLicense   = "Source available, all rights reserved. See LICENSE."
	brandSite      = "https://sobhanaz.github.io/bobres-3x-ui-telegram-panel/"
	brandRepo      = "https://github.com/sobhanaz/bobres-3x-ui-telegram-panel"
	brandIssues    = brandRepo + "/issues"
	brandSecurity  = brandRepo + "/security"
)

// bannerMaxWidth is the widest line any banner may have.
const bannerMaxWidth = 78

// buildInfo is what the running bobres knows about its own build.
type buildInfo struct {
	version string
	commit  string // short revision, "" when unknown
	built   string // date of the commit, "" when unknown
}

// currentBuild reads the version the release build stamped in and, from the Go
// toolchain, the commit it was built from.
func currentBuild() buildInfo {
	b := buildInfo{version: version.Version}
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				b.commit = s.Value
				if len(b.commit) > 7 {
					b.commit = b.commit[:7]
				}
			case "vcs.time":
				b.built = s.Value
				if i := strings.IndexByte(b.built, 'T'); i > 0 {
					b.built = b.built[:i]
				}
			}
		}
	}
	return b
}

// line is the version, then the commit and date when they are known.
func (b buildInfo) line() string { return b.fit(1 << 20) }

// fit is line, leaving out the date and then the commit until it takes at most
// max columns.
func (b buildInfo) fit(max int) string {
	for _, c := range []buildInfo{b, {version: b.version, commit: b.commit}, {version: b.version}} {
		s := "bobres " + c.version
		var more []string
		if c.commit != "" {
			more = append(more, "commit "+c.commit)
		}
		if c.built != "" {
			more = append(more, c.built)
		}
		if len(more) > 0 {
			s += " (" + strings.Join(more, ", ") + ")"
		}
		if width(s) <= max {
			return s
		}
	}
	return "bobres " + b.version
}

// beaverArt is the beaver, the whole brand, in plain ASCII: ears, eyes (O), a
// big front tooth (##) and the flat tail at the lower right. The colored banner
// paints O and # cream and the rest brown.
var beaverArt = [...]string{
	`   _          _`,
	`  (_)'------'(_)`,
	`  /    O  O    \`,
	` |       __      |`,
	` |      |##|     |   .------.`,
	`  \     |##|    /    |//////|`,
	`   '-.________.-'    '------'`,
}

// beaverRows is beaverArt with every row as wide as the widest.
func beaverRows() []string {
	w := 0
	for _, r := range beaverArt {
		w = max(w, width(r))
	}
	rows := make([]string, len(beaverArt))
	for i, r := range beaverArt {
		rows[i] = r + strings.Repeat(" ", w+2-width(r))
	}
	return rows
}

// letters is the wordmark's alphabet: 5 rows each, # is ink.
var letters = map[rune][5]string{
	'B': {"####.", "#...#", "####.", "#...#", "####."},
	'O': {".###.", "#...#", "#...#", "#...#", ".###."},
	'R': {"####.", "#...#", "####.", "#..#.", "#...#"},
	'E': {"#####", "#....", "####.", "#....", "#####"},
	'S': {".####", "#....", ".###.", "....#", "####."},
}

// wordmark is BOBRES in block letters (ink = # in ASCII, a full block in
// Unicode), 5 rows of 40 columns.
func wordmark(unicode bool) []string {
	ink := "#"
	if unicode {
		ink = "█"
	}
	rows := make([]string, 5)
	for i, ch := range "BOBRES" {
		for r := range rows {
			if i > 0 {
				rows[r] += "  "
			}
			rows[r] += strings.NewReplacer("#", ink, ".", " ").Replace(letters[ch][r])
		}
	}
	return rows
}

// width is the number of columns s takes (every glyph the banner uses is one).
func width(s string) int { return utf8.RuneCountInString(s) }

// bannerLines is the banner for terminal t: the beaver and the wordmark with
// the version and tagline, then the copyright, the license and the links.
// With t.fancy the lines carry color; without it they are pure ASCII.
func bannerLines(t termUI, b buildInfo) []string {
	copyright := brandCopyright
	if !t.unicode {
		copyright = strings.ReplaceAll(copyright, "©", "(c)")
	}
	var lines []string
	if t.width >= 64 {
		left := beaverRows()
		lw := width(left[0])
		room := bannerMaxWidth - lw
		right := append([]string{b.fit(room)}, wordmark(t.unicode)...)
		right = append(right, brandTagline)
		for i := range left {
			r := right[i]
			switch i {
			case 0:
				r = t.paint(roleBold, r)
			case len(left) - 1:
				r = t.paint(roleCream, r)
			default:
				r = t.paint(roleTeal, r)
			}
			lines = append(lines, t.beaver(left[i])+r)
		}
		lines = append(lines, "")
	} else {
		lines = append(lines, t.paint(roleBold, b.line()), t.paint(roleTeal, brandTagline))
	}
	lines = append(lines, t.paint(roleCream, copyright), t.paint(roleCream, brandMadeBy), brandLicense)
	for _, l := range []struct{ label, url string }{
		{"Website", brandSite}, {"Docs", brandRepo}, {"Issues", brandIssues}, {"Security", brandSecurity},
	} {
		lines = append(lines, fmt.Sprintf("%s %s", t.paint(roleTeal, fmt.Sprintf("%-8s", l.label)), l.url))
	}
	return lines
}

// compactLine is the one-line form of the banner, for version and help.
func compactLine(t termUI, b buildInfo) string {
	copyright := brandCopyright
	if !t.unicode {
		copyright = strings.ReplaceAll(copyright, "©", "(c)")
	}
	sep := "  |  "
	if t.unicode {
		sep = "  ·  "
	}
	line := t.paint(roleBold, "bobres "+b.version) + sep + copyright
	// The credit joins it only where the whole line still fits on one line.
	if full := "bobres " + b.version + sep + copyright + sep + brandMadeBy; width(full) <= min(bannerMaxWidth, max(t.width-1, 0)) {
		line += sep + brandMadeBy
	}
	return line
}
