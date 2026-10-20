package snapshot

import (
	"regexp"
	"strings"
	"unicode"
)

// Clean makes untrusted text fit to show: invalid UTF-8, control characters,
// the invisible and direction-changing characters that make text read other
// than it is (bidirectional overrides, zero-width marks) and line breaks are
// gone, runs of white space become one space, < and > become the single angle
// quotes ‹ and › (so no client that forgets to escape can be handed markup),
// and what is longer than max characters is cut with an ellipsis.
func Clean(s string, max int) string {
	if s == "" || max <= 0 {
		return ""
	}
	// Anything past what could ever be kept is not looked at.
	if len(s) > 8*max+16 {
		s = s[:8*max+16]
	}
	var b strings.Builder
	n, cut, pendingSpace := 0, false, false
	for _, r := range strings.ToValidUTF8(s, "?") {
		switch {
		case r == '<':
			r = '‹'
		case r == '>':
			r = '›'
		case isInvisible(r):
			continue
		case unicode.IsSpace(r) || unicode.IsControl(r):
			pendingSpace = n > 0 // leading white space is dropped; the rest is one space, written before the next character
			continue
		}
		extra := 1
		if pendingSpace {
			extra = 2
		}
		if n+extra > max {
			cut = true
			break
		}
		if pendingSpace {
			b.WriteByte(' ')
			n++
			pendingSpace = false
		}
		b.WriteRune(r)
		n++
	}
	if cut {
		return b.String() + "…"
	}
	return b.String()
}

// isInvisible: characters that change how text is drawn or ordered without
// showing anything.
func isInvisible(r rune) bool {
	return unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Co, r) || r == 0x034f || r == 0xfffc || r == 0xfffd
}

// The shapes names must have to be kept. A value that does not match is
// dropped (or replaced by a placeholder where the field is needed), never
// repaired: what a name looks like is what the engine, systemd or the
// kernel call it.
var (
	reMount   = regexp.MustCompile(`^/[A-Za-z0-9_./+@=-]{0,127}$`)
	reName    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
	reImage   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:/+-]{0,255}$`)
	reID12    = regexp.MustCompile(`^[0-9a-f]{12}$`)
	reIface   = regexp.MustCompile(`^[A-Za-z0-9_.:@-]{1,32}$`)
	reUnit    = regexp.MustCompile(`^[A-Za-z0-9:_.@\\-]{1,100}$`)
	reWord    = regexp.MustCompile(`^[A-Za-z0-9_.-]{0,40}$`)
	reDNS     = regexp.MustCompile(`^[a-z0-9]([-a-z0-9.]{0,251}[a-z0-9])?$`)
	reVersion = regexp.MustCompile(`^[0-9A-Za-z.+~_-]{0,64}$`)
	reRoles   = regexp.MustCompile(`^[a-z0-9,._/-]*$`)
)

// ValidMount reports whether s can be a mount point the snapshot keeps.
func ValidMount(s string) bool { return reMount.MatchString(s) && !strings.Contains(s, "..") }

// ValidName reports whether s is shaped like a container, service or project
// name.
func ValidName(s string) bool { return reName.MatchString(s) }

// ValidLabel reports whether s can be the label of a metric (a Compose
// service name or a mount point): short, plain and with no separator that
// could be read as another label.
func ValidLabel(s string) bool {
	return len(s) > 0 && len(s) <= 64 && (ValidName(s) || (ValidMount(s) && !strings.ContainsAny(s, ":@=+")))
}
