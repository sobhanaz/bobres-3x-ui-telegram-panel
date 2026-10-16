package branding

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestDefaultIsValid(t *testing.T) {
	if err := Default().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRejectsBadInput(t *testing.T) {
	b := Default()
	b.PrimaryColor = "red"
	b.LogoURL = "javascript:alert(1)"
	b.TermsURL = "ftp://x/y"
	b.Name = map[Lang]string{}
	b.Currency = " "
	err := b.Validate()
	if err == nil {
		t.Fatal("want error")
	}
	for _, want := range []string{"primary_color", "logo_url", "terms_url", "name is required", "currency"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in %v", want, err)
		}
	}
}

func TestDisplayNameFallbacks(t *testing.T) {
	b := Default()
	b.Name = map[Lang]string{FA: "فروشگاه"}
	if b.DisplayName(EN) != "فروشگاه" {
		t.Fatal("should fall back to default language")
	}
	b.Name = map[Lang]string{}
	if b.DisplayName(EN) != ProductName {
		t.Fatal("should fall back to product name")
	}
}

func TestPoweredBy(t *testing.T) {
	if PoweredBy(true) != "" {
		t.Fatal("white label must hide attribution")
	}
	if PoweredBy(false) != "Powered by BOBRES" {
		t.Fatal("attribution expected")
	}
}

func TestAboutWithCredit(t *testing.T) {
	const about = "Fast, secure VPN."
	if got := AboutWithCredit(about, true); got != about {
		t.Fatalf("white label must leave the about text alone, got %q", got)
	}
	if got := AboutWithCredit(about, false); got != about+"\nMade by TECSO-DEV" {
		t.Fatalf("credit expected, got %q", got)
	}
	// Telegram refuses more than 120 characters: with no room the text stays as it is.
	long := strings.Repeat("ا", 110)
	if got := AboutWithCredit(long, false); got != long {
		t.Fatalf("a text with no room for the credit must be kept, got %q", got)
	}
	exact := strings.Repeat("a", 120-len("\nMade by TECSO-DEV"))
	if got := AboutWithCredit(exact, false); utf8.RuneCountInString(got) != 120 {
		t.Fatalf("a text that fits exactly must get the credit, got %d characters", utf8.RuneCountInString(got))
	}
	if AboutWithCredit("", false) != "" {
		t.Fatal("an empty about text stays empty")
	}
}
