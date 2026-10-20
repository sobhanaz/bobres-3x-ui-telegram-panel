package collect

import (
	"regexp"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/monitor/snapshot"
)

// The patterns of what must not stay in a journal line. Lines are
// Docker's, Caddy's and BOBRES's own: they can carry a bot token inside a
// URL, a bearer token, a password in a connection string, a subscription link
// (its path is the secret), a client's address or an e-mail address.
var scrubbers = []struct {
	re   *regexp.Regexp
	with string
}{
	// A Telegram bot token: the bot's numeric id, a colon and 30+ characters.
	{regexp.MustCompile(`\d{6,}:[A-Za-z0-9_-]{30,}`), "[token]"},
	// "Authorization: Bearer xyz", "bearer xyz", "Basic xyz".
	{regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=-]{8,}`), "$1 [redacted]"},
	// secret=..., "password": "...", token: ..., apikey=... (the value up to a space, comma or quote).
	{regexp.MustCompile(`(?i)\b([a-z0-9_.-]*(?:password|passwd|secret|token|api[_-]?key|authorization|cookie|dsn|credential)s?[a-z0-9_.-]*["']?\s*[=:]\s*["']?)[^\s,;"'&]+`), "$1[redacted]"},
	// scheme://user:password@host
	{regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://)[^/\s:@]*:[^/\s@]*@`), "$1[redacted]@"},
	// A URL's query string and fragment can hold a token.
	{regexp.MustCompile(`(https?://[^\s?#"']+)[?#][^\s"']*`), "$1?[...]"},
	// The subscription page's secret path, and the Telegram Bot API's.
	{regexp.MustCompile(`/sub/[A-Za-z0-9_-]{6,}`), "/sub/[...]"},
	{regexp.MustCompile(`/bot[A-Za-z0-9:_-]{10,}`), "/bot[...]"},
	// An e-mail address.
	{regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)*\.[A-Za-z]{2,}`), "[email]"},
	// A long run of hex or base64 (a key, a digest, a session id).
	{regexp.MustCompile(`[A-Za-z0-9+/_=-]{32,}`), "[...]"},
	// IPv4 addresses (with a port), then IPv6 ones (colon-separated hex groups with at least one "::" or 3+ groups).
	{regexp.MustCompile(`\b\d{1,3}(?:\.\d{1,3}){3}(?::\d{1,5})?\b`), "[ip]"},
	{regexp.MustCompile(`(?i)\b(?:[0-9a-f]{1,4}:){3,7}[0-9a-f]{1,4}\b|\b(?:[0-9a-f]{1,4}:){1,6}:(?:[0-9a-f]{1,4}(?::[0-9a-f]{1,4}){0,5})?`), "[ip]"},
}

// Scrub makes a log line safe to keep and show: tokens, passwords, URLs'
// queries and userinfo, secret paths, e-mail addresses, long keys and IP
// addresses are replaced by a marker, and the rest is cleaned
// (snapshot.Clean) and cut to the snapshot's line length. It prefers losing a
// useful detail to keeping a secret.
func Scrub(line string) string {
	s := snapshot.Clean(line, 4*snapshot.MaxLine)
	for _, sc := range scrubbers {
		s = sc.re.ReplaceAllString(s, sc.with)
	}
	return snapshot.Clean(s, snapshot.MaxLine)
}
