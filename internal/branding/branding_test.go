package branding

import (
	"strings"
	"testing"
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
