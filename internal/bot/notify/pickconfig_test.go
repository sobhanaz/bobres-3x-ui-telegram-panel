package notify

import (
	"encoding/base64"
	"strings"
	"testing"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
)

func vmessLink(ps string) string {
	return "vmess://" + base64.StdEncoding.EncodeToString([]byte(`{"v":"2","ps":"`+ps+`","add":"h.example","port":"443","id":"u"}`))
}

// A config's name is what the panel shows after the "#" of the link (percent-encoded), or the ps of a vmess link.
func TestConfigName(t *testing.T) {
	for _, c := range []struct{ link, want string }{
		{"vless://u@h.example:443?type=tcp#Turkey-abc", "Turkey-abc"},
		{"vless://u@h.example:443?type=tcp#%F0%9F%87%B9%F0%9F%87%B7%20Turkey-abc", "🇹🇷 Turkey-abc"},
		{"trojan://p@h.example:443#Germany%201", "Germany 1"},
		{"vless://u@h.example:443?type=tcp", ""},
		{"vless://u@h.example:443#%zz", "%zz"}, // not valid percent-encoding: as it is
		{vmessLink("Turkey-vmess"), "Turkey-vmess"},
		{strings.TrimRight(vmessLink("Turkey-vmess"), "="), "Turkey-vmess"}, // unpadded
		{"vmess://not-base64!", ""},
	} {
		if got := configName(c.link); got != c.want {
			t.Errorf("configName(%q) = %q, want %q", c.link, got, c.want)
		}
	}
}

// The config sent on its own is the first whose name has one of the owner's words, and the first config when there are none or nothing matches.
func TestPickConfig(t *testing.T) {
	de := "vless://u@de.example:443#Germany-abc"
	tr := "vless://u@tr.example:443#%F0%9F%87%B9%F0%9F%87%B7%20Turkey-abc"
	tr2 := "vless://u@tr2.example:443#Turkey-2-abc"
	nl := vmessLink("Netherlands-abc")
	all := []string{de, tr, tr2, nl}

	for _, c := range []struct {
		match   string
		configs []string
		want    string
	}{
		{"", all, de},                    // no words: the first, as before
		{" , ;\n", all, de},              // only separators
		{"turkey", all, tr},              // the first Turkey config
		{"TURKEY", all, tr},              // any case
		{"turkey-2", all, tr2},           // a longer word picks a given one
		{"🇹🇷", all, tr},                  // a flag
		{"france, netherlands", all, nl}, // the first word that matches nothing is skipped, the next one finds a config
		{"france", all, de},              // nothing matches: the first
		{"turkey", []string{de}, de},     // a service with no Turkey config
		{"،turkey،", all, tr},            // Persian commas
	} {
		if got := pickConfig(c.configs, c.match); got != c.want {
			t.Errorf("pickConfig(match %q) = %q, want %q", c.match, got, c.want)
		}
	}
}

// With delivery.config_match set, the customer gets the subscription link in the ready message and, on its own, the Turkey config, not the first one.
func TestDeliverySendsTheMatchingConfig(t *testing.T) {
	n, core, tgf, set := bundleNotifier(t)
	core.links = &corev1.SubscriptionLinks{ConfigLinks: []string{
		"vless://u@de.example:443?security=tls#Germany-abc",
		"vless://u@tr.example:443?security=tls#Turkey-abc",
	}}
	set.Replace(map[string]string{"delivery.config_match": "turkey"})
	deliver(t, n, purchase())
	got := tgf.take()
	if len(got) != 2 {
		t.Fatalf("%d messages: %+v", len(got), got)
	}
	if !strings.Contains(got[0].text, "https://sub.example.com/sub/x") {
		t.Errorf("the ready message carries the subscription link: %q", got[0].text)
	}
	if !strings.Contains(got[1].text, "Turkey-abc") || strings.Contains(got[1].text, "Germany") {
		t.Errorf("the direct config is the Turkey one: %q", got[1].text)
	}

	// Without the setting, the first config, as before.
	set.Replace(map[string]string{})
	deliver(t, n, purchase())
	if got := tgf.take(); len(got) != 2 || !strings.Contains(got[1].text, "Germany-abc") {
		t.Errorf("no setting: %+v", got)
	}
}
