package main

import (
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/config"
)

func TestStoreDomain(t *testing.T) {
	for name, tc := range map[string]struct {
		cfg  config.Core
		want string
	}{
		"the domain":                {config.Core{Domain: "Panel.Example.com"}, "panel.example.com"},
		"the domain wins":           {config.Core{Domain: "panel.example.com", PublicURLEnv: "https://other.example.org"}, "panel.example.com"},
		"a local test setup":        {config.Core{PublicURLEnv: "https://bobres.localhost:8443"}, "bobres.localhost"},
		"neither":                   {config.Core{}, ""},
		"an address that is no URL": {config.Core{PublicURLEnv: "::"}, ""},
	} {
		if got := storeDomain(tc.cfg); got != tc.want {
			t.Errorf("%s: %q, want %q", name, got, tc.want)
		}
	}
}
