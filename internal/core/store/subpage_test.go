package store

import "testing"

func TestNewPageToken(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		tok := NewPageToken()
		if !ValidPageToken(tok) {
			t.Fatalf("a made token is not valid: %q", tok)
		}
		if seen[tok] {
			t.Fatalf("token repeated: %q", tok)
		}
		seen[tok] = true
	}
}

func TestValidPageToken(t *testing.T) {
	ok := "0123456789abcdef0123456789abcdef"
	if !ValidPageToken(ok) {
		t.Fatal("a good token was refused")
	}
	for name, s := range map[string]string{
		"empty": "", "short": ok[:31], "long": ok + "0", "upper": "0123456789ABCDEF0123456789ABCDEF",
		"not hex": "0123456789abcdeg0123456789abcdef", "dash": "0123456789abcdef-123456789abcdef",
		"space": " 123456789abcdef0123456789abcdef", "slash": "0123456789abcdef0123456789abcde/", "nul": "0123456789abcdef0123456789abcde\x00",
		"unicode": "0123456789abcdef0123456789abcdé",
	} {
		if ValidPageToken(s) {
			t.Errorf("%s: accepted %q", name, s)
		}
	}
}
