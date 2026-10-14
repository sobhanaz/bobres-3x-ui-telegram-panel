package main

import (
	"io"
	"os"
	"strconv"
	"strings"

	"golang.org/x/term"
)

// colorLevel is how many colors a terminal is trusted with.
type colorLevel int

const (
	colorNone colorLevel = iota
	color8
	color256
	colorTrue
)

// role is a color of the BOBRES palette (docs/08-brand.md: navy, teal, brown,
// cream) or a style.
type role int

const (
	roleNavy role = iota
	roleTeal
	roleBrown
	roleCream
	roleBold
	roleDim
	roleRed
)

// sgr is the escape parameter of each role at each level. Navy is lightened
// from the brand's #0f172a so that it can be read on a dark terminal.
var sgr = map[colorLevel]map[role]string{
	colorTrue: {
		roleNavy: "38;2;59;91;140", roleTeal: "38;2;20;184;166", roleBrown: "38;2;139;90;43",
		roleCream: "38;2;245;222;179", roleBold: "1", roleDim: "2", roleRed: "31",
	},
	color256: {
		roleNavy: "38;5;60", roleTeal: "38;5;37", roleBrown: "38;5;94",
		roleCream: "38;5;223", roleBold: "1", roleDim: "2", roleRed: "31",
	},
	color8: {
		roleNavy: "34", roleTeal: "36", roleBrown: "33",
		roleCream: "37", roleBold: "1", roleDim: "2", roleRed: "31",
	},
}

// uiOptions are the global flags (--plain, --no-banner), taken out of the
// command line by run.
type uiOptions struct{ plain, noBanner bool }

var uiOpts uiOptions

// stripUIFlags takes --plain and --no-banner out of args (up to a "--") and
// reports them: every command accepts them, and they change only how the
// output looks.
func stripUIFlags(args []string) ([]string, uiOptions) {
	var o uiOptions
	rest := make([]string, 0, len(args))
	for i, a := range args {
		switch a {
		case "--":
			return append(rest, args[i:]...), o
		case "--plain":
			o.plain = true
		case "--no-banner":
			o.noBanner = true
		default:
			rest = append(rest, a)
		}
	}
	return rest, o
}

// termUI is what a command may do on its output: draw in place and use color
// (fancy), use Unicode, and how wide a line may be.
type termUI struct {
	tty     bool // a person is watching (a terminal, whatever its looks)
	fancy   bool // a terminal that takes cursor control and color
	color   colorLevel
	unicode bool
	width   int
}

// minFancyWidth is the narrowest terminal that gets a bar and a banner with
// art; under it the output is plain, short lines.
const minFancyWidth = 60

// detectUI decides how out is shown. It is plain (no escape bytes, no
// carriage returns, ASCII only) when out is not a terminal, NO_COLOR is set,
// TERM is dumb or unset, BOBRES_PLAIN=1 or --plain says so, or the terminal
// is narrower than minFancyWidth. BOBRES_FORCE_TTY=1 makes out look like a
// terminal (tests and demos only).
func detectUI(out io.Writer, getenv func(string) string) termUI {
	tty, w := false, 0
	if f, ok := rawWriter(out).(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		tty = true
		if cols, _, err := term.GetSize(int(f.Fd())); err == nil {
			w = cols
		}
	}
	if getenv("BOBRES_FORCE_TTY") == "1" {
		tty = true
	}
	if w <= 0 {
		w, _ = strconv.Atoi(strings.TrimSpace(getenv("COLUMNS")))
	}
	if w <= 0 {
		w = 80
	}
	t := termUI{tty: tty, width: w}
	name := getenv("TERM")
	if !tty || uiOpts.plain || truthy(getenv("BOBRES_PLAIN")) || getenv("NO_COLOR") != "" ||
		name == "" || name == "dumb" || w < minFancyWidth {
		return t
	}
	t.fancy = true
	t.unicode = utf8Locale(getenv)
	switch ct := getenv("COLORTERM"); {
	case ct == "truecolor" || ct == "24bit":
		t.color = colorTrue
	case strings.Contains(name, "256color"):
		t.color = color256
	default:
		t.color = color8
	}
	return t
}

// truthy reads an on/off environment value.
func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// utf8Locale reports whether the locale says UTF-8: the first of LC_ALL,
// LC_CTYPE and LANG that is set decides, as in POSIX.
func utf8Locale(getenv func(string) string) bool {
	for _, k := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		if v := getenv(k); v != "" {
			v = strings.ToLower(v)
			return strings.Contains(v, "utf-8") || strings.Contains(v, "utf8")
		}
	}
	return false
}

// paint colors s with r, or leaves it as it is where color is off.
func (t termUI) paint(r role, s string) string {
	if t.color == colorNone || s == "" {
		return s
	}
	return "\033[" + sgr[t.color][r] + "m" + s + "\033[0m"
}

// beaver paints one row of the beaver: eyes and tooth cream, the rest brown.
func (t termUI) beaver(row string) string {
	if t.color == colorNone {
		return row
	}
	var b strings.Builder
	var cur role
	var run []rune
	flush := func() {
		if len(run) > 0 {
			b.WriteString(t.paint(cur, string(run)))
			run = run[:0]
		}
	}
	for _, c := range row {
		r := roleBrown
		switch c {
		case 'O', '#':
			r = roleCream
		case ' ':
			r = cur // a gap takes the color around it: fewer escapes
		}
		if r != cur {
			flush()
			cur = r
		}
		run = append(run, c)
	}
	flush()
	return b.String()
}

// rawWriter is the writer under a progress writer (out itself otherwise).
func rawWriter(w io.Writer) io.Writer {
	if pw, ok := w.(*progWriter); ok {
		return pw.w
	}
	return w
}

// bannerOff reports whether the banner is switched off (--no-banner, or
// BOBRES_NO_BANNER=1).
func bannerOff(getenv func(string) string) bool {
	return uiOpts.noBanner || truthy(getenv("BOBRES_NO_BANNER"))
}

// printBanner writes the full banner to out.
func printBanner(out io.Writer, t termUI) {
	for _, l := range bannerLines(t, currentBuild()) {
		_, _ = io.WriteString(out, l+"\n")
	}
	_, _ = io.WriteString(out, "\n")
}

// mark is the sign of an outcome: a check, a warning sign, a cross or a dash
// on a terminal (plain letters where Unicode is off), the words OK, WARN,
// FAIL and SKIP anywhere else.
func (t termUI) mark(k markKind) string {
	if !t.fancy {
		return [...]string{"OK", "WARN", "FAIL", "SKIP"}[k]
	}
	glyph := [...]string{"+", "!", "x", "-"}[k]
	if t.unicode {
		glyph = [...]string{"✔", "!", "✗", "-"}[k]
	}
	return t.paint([...]role{roleTeal, roleBrown, roleRed, roleDim}[k], glyph)
}

// statusMark is the mark of a doctor outcome.
func statusMark(st Status) markKind {
	switch st {
	case Warn:
		return markWarn
	case Fail:
		return markFail
	case Skip:
		return markSkip
	}
	return markOK
}

// paintUsage colors a help text on a terminal: the title bold, the section
// lines bold teal, the command names teal. Elsewhere it is the text as it is.
func paintUsage(t termUI, usage string) string {
	if t.color == colorNone {
		return usage
	}
	lines := strings.Split(usage, "\n")
	for i, l := range lines {
		switch {
		case i == 0 && strings.Contains(l, " - "):
			lines[i] = t.paint(roleBold, l)
		case l != "" && !strings.HasPrefix(l, " ") && (strings.HasSuffix(l, ":") || strings.HasPrefix(l, "Usage:")):
			lines[i] = t.paint(roleBold, t.paint(roleTeal, l))
		case strings.HasPrefix(l, "  ") && len(l) > 2 && l[2] != ' ':
			name, rest, found := strings.Cut(l[2:], " ")
			lines[i] = "  " + t.paint(roleTeal, name)
			if found {
				lines[i] += " " + rest
			}
		}
	}
	return strings.Join(lines, "\n")
}
