package domain

import "testing"

func TestMiniAppOn(t *testing.T) {
	for _, c := range []struct {
		settings map[string]string
		want     bool
	}{
		{nil, true}, // never set: on
		{map[string]string{"miniapp.enabled": "true"}, true},
		{map[string]string{"miniapp.enabled": "false"}, false},
		{map[string]string{"miniapp.enabled": ""}, true},
	} {
		if got := MiniAppOn(c.settings); got != c.want {
			t.Errorf("%v: %v, want %v", c.settings, got, c.want)
		}
	}
}

func TestMiniAppURL(t *testing.T) {
	for origin, want := range map[string]string{
		"https://shop.example.com":         "https://shop.example.com/app/",
		"https://shop.example.com:8443":    "https://shop.example.com:8443/app/",
		"https://203.0.113.9":              "https://203.0.113.9/app/",
		"https://[2001:db8::1]":            "https://[2001:db8::1]/app/",
		"https://panel.sub.domain.ir/":     "https://panel.sub.domain.ir/app/",
		"http://shop.example.com":          "", // Telegram needs https
		"":                                 "",
		"https://":                         "",
		"https://localhost":                "",
		"https://localhost:8443":           "",
		"https://bobres.localhost:8443":    "",
		"https://shop.local":               "",
		"https://nas.lan":                  "",
		"https://app.test":                 "",
		"https://shop":                     "", // a single label
		"https://127.0.0.1":                "",
		"https://10.0.0.5":                 "",
		"https://192.168.1.10":             "",
		"https://172.16.4.4":               "",
		"https://169.254.1.1":              "",
		"https://100.64.1.1":               "",
		"https://[::1]":                    "",
		"https://[fd00::1]":                "",
		"https://user:pw@shop.example.com": "",
		"ftp://shop.example.com":           "",
		"not a url":                        "",
	} {
		if got := MiniAppURL(origin); got != want {
			t.Errorf("MiniAppURL(%q) = %q, want %q", origin, got, want)
		}
	}
}
