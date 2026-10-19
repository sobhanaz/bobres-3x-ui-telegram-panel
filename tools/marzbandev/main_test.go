package main

import (
	"encoding/json"
	"slices"
	"testing"
)

// defaultConfig is Marzban's own xray_config.json (v0.8.4): one Shadowsocks inbound.
const defaultConfig = `{
  "log": {"loglevel": "warning"},
  "routing": {"rules": [{"ip": ["geoip:private"], "outboundTag": "BLOCK", "type": "field"}]},
  "inbounds": [{"tag": "Shadowsocks TCP", "listen": "0.0.0.0", "port": 1080, "protocol": "shadowsocks",
                "settings": {"clients": [], "network": "tcp,udp"}}],
  "outbounds": [{"protocol": "freedom", "tag": "DIRECT"}, {"protocol": "blackhole", "tag": "BLOCK"}]
}`

func tagsOf(t *testing.T, cfg map[string]any) []string {
	t.Helper()
	var tags []string
	for _, in := range cfg["inbounds"].([]any) {
		tags = append(tags, in.(map[string]any)["tag"].(string))
	}
	return tags
}

func TestWithInboundsAddsWhatIsMissing(t *testing.T) {
	var cfg map[string]any
	if err := json.Unmarshal([]byte(defaultConfig), &cfg); err != nil {
		t.Fatal(err)
	}
	if !withInbounds(cfg) {
		t.Fatal("nothing added to a configuration with one inbound")
	}
	want := []string{"Shadowsocks TCP", "VLESS TCP", "VMess TCP", "Trojan TCP"}
	if got := tagsOf(t, cfg); !slices.Equal(got, want) {
		t.Fatalf("inbounds = %v, want %v", got, want)
	}
	if withInbounds(cfg) {
		t.Fatal("a second pass changed the configuration")
	}
	// The rest of the configuration is untouched, and the whole thing is what
	// the panel's PUT accepts: JSON.
	for _, k := range []string{"log", "routing", "outbounds"} {
		if _, ok := cfg[k]; !ok {
			t.Errorf("%q lost", k)
		}
	}
	if _, err := json.Marshal(cfg); err != nil {
		t.Fatal(err)
	}
}

func TestWithInboundsRespectsWhatIsThere(t *testing.T) {
	var cfg map[string]any
	if err := json.Unmarshal([]byte(`{"inbounds":[
	  {"tag":"my vless","protocol":"vless","port":443},
	  {"tag":"Trojan TCP","protocol":"vmess","port":8443}]}`), &cfg); err != nil {
		t.Fatal(err)
	}
	// VLESS and VMess are served already (under other tags) and the tag
	// "Trojan TCP" is in use, so nothing is added.
	if withInbounds(cfg) {
		t.Fatalf("changed a configuration that serves the protocols: %v", cfg)
	}
	got := tagsOf(t, cfg)
	want := []string{"my vless", "Trojan TCP"}
	if !slices.Equal(got, want) {
		t.Fatalf("inbounds = %v, want %v", got, want)
	}
}

func TestWithInboundsOnAnEmptyConfiguration(t *testing.T) {
	cfg := map[string]any{}
	if !withInbounds(cfg) || len(cfg["inbounds"].([]any)) != 3 {
		t.Fatalf("config = %v", cfg)
	}
}

func TestWithoutPrivateBlock(t *testing.T) {
	var cfg map[string]any
	if err := json.Unmarshal([]byte(defaultConfig), &cfg); err != nil {
		t.Fatal(err)
	}
	if !withoutPrivateBlock(cfg) {
		t.Fatal("Marzban's default rule to block private addresses was not found")
	}
	if rules := cfg["routing"].(map[string]any)["rules"].([]any); len(rules) != 0 {
		t.Fatalf("rules = %v", rules)
	}
	if withoutPrivateBlock(cfg) {
		t.Fatal("a second pass changed the configuration")
	}
	// Other rules stay, and so does a configuration with no routing at all.
	if err := json.Unmarshal([]byte(`{"routing":{"rules":[
	  {"ip":["geoip:private"],"outboundTag":"BLOCK","type":"field"},
	  {"domain":["geosite:category-ads"],"outboundTag":"BLOCK","type":"field"},
	  {"ip":["geoip:private"],"outboundTag":"DIRECT","type":"field"}]}}`), &cfg); err != nil {
		t.Fatal(err)
	}
	if !withoutPrivateBlock(cfg) {
		t.Fatal("rule not found")
	}
	if rules := cfg["routing"].(map[string]any)["rules"].([]any); len(rules) != 2 {
		t.Fatalf("rules = %v, want the other two", rules)
	}
	if withoutPrivateBlock(map[string]any{}) {
		t.Fatal("changed an empty configuration")
	}
}

func TestThrowawayRefusesAPublicAddress(t *testing.T) {
	if err := throwaway("http://127.0.0.1:8000"); err != nil {
		t.Fatal(err)
	}
	if err := throwaway("http://[::1]:8000/"); err != nil {
		t.Fatal(err)
	}
	if err := throwaway("http://8.8.8.8:8000"); err == nil {
		t.Fatal("a public address was accepted")
	}
	if err := throwaway("not a url"); err == nil {
		t.Fatal("garbage was accepted")
	}
}
