package collect

import (
	"strings"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/monitor/snapshot"
)

func TestScrub(t *testing.T) {
	type c struct{ in, notWant, want string }
	for _, tc := range []c{
		{"bot token 123456789:AAEhBOweik6ad6PsWRpYgM2RUBXPoX0jEl8x here", "AAEhBOweik6", "[token]"},
		{"GET https://api.telegram.org/bot123456789:AAEhBOweik6ad6PsWRpYgM2RUBXPoX0jEl8x/getMe failed", "123456789", "/bot[token]/getMe"},
		{"Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.payload.sig", "eyJhbGci", "[redacted]"},
		{"password=hunter2 and more", "hunter2", "password=[redacted]"},
		{`{"api_key":"abc123def456","other":1}`, "abc123def456", "[redacted]"},
		{"BOBRES_TOKEN_BOT=0123456789abcdef", "0123456789abcdef", "[redacted]"},
		{"DB_PASSWORD: s3cret!", "s3cret", "[redacted]"},
		{"connect postgres://svc_core:pa55w0rd@postgres:5432/bobres failed", "pa55w0rd", "[redacted]@"},
		{"callback https://example.com/pay?token=abcdef&x=1#frag done", "abcdef", "https://example.com/pay?[...]"},
		{"page /sub/0a1b2c3d4e5f60718293a4b5c6d7e8f9 not found", "0a1b2c3d4e5f", "/sub/[...]"},
		{"mail from admin@example.com bounced", "admin@", "[email]"},
		{"key " + strings.Repeat("ab12", 12) + " rejected", strings.Repeat("ab12", 12), "[...]"},
		{"dial tcp 172.19.0.5:8080: refused", "172.19.0.5", "[ip]"},
		{"from 203.0.113.9 and 2001:db8:85a3:0:0:8a2e:370:7334 and fe80::1", "2001:db8", "[ip]"},
		{"a <b>tag</b>", "<b>", "‹b›"},
	} {
		got := Scrub(tc.in)
		if strings.Contains(got, tc.notWant) {
			t.Errorf("Scrub(%q) = %q still has %q", tc.in, got, tc.notWant)
		}
		if !strings.Contains(got, tc.want) {
			t.Errorf("Scrub(%q) = %q, want it to contain %q", tc.in, got, tc.want)
		}
	}
	// Plain lines stay readable.
	for _, plain := range []string{"failed to start container: exit status 1", "time 10:20:30 passed", "version 1.2 of docker", "no space left on device"} {
		if got := Scrub(plain); got != plain {
			t.Errorf("Scrub(%q) = %q", plain, got)
		}
	}
	if got := Scrub(strings.Repeat("word ", 400)); len([]rune(got)) > snapshot.MaxLine+1 {
		t.Errorf("a long line: %d characters", len([]rune(got)))
	}
	if Scrub("") != "" || Scrub("\x00\x01") != "" {
		t.Error("empty lines")
	}
}
