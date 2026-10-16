package domain

import "testing"

func TestSubPageURL(t *testing.T) {
	for _, c := range []struct{ base, token, want string }{
		{"https://shop.example.com", "abc", "https://shop.example.com/sub/abc"},
		{"https://shop.example.com/", "abc", "https://shop.example.com/sub/abc"},
		{"https://shop.example.com:8443//", "abc", "https://shop.example.com:8443/sub/abc"},
		{"", "abc", ""},
		{"https://shop.example.com", "", ""},
	} {
		if got := SubPageURL(c.base, c.token); got != c.want {
			t.Errorf("SubPageURL(%q, %q) = %q, want %q", c.base, c.token, got, c.want)
		}
	}
}

func TestSubPageFrom(t *testing.T) {
	// On unless switched off.
	if got := SubPageFrom(nil); !got.Enabled || got.BotUsername != "" {
		t.Errorf("empty settings: %+v", got)
	}
	got := SubPageFrom(map[string]string{SettingSubPageEnabled: "false", SettingBotUsername: "acme_bot"})
	if got.Enabled || got.BotUsername != "acme_bot" {
		t.Errorf("off: %+v", got)
	}
	if got := SubPageFrom(map[string]string{SettingSubPageEnabled: "true"}); !got.Enabled {
		t.Errorf("on: %+v", got)
	}
}
