package notify

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strings"
)

// pickConfig chooses the config that goes to a customer on its own (the direct config of a delivery). match is the owner's
// setting delivery.config_match: words (separated by commas or line breaks) to look for in a config's name; the first config
// whose name has any of them is the one. With no words, or when no config's name has one, it is the service's first config.
// configs is not empty.
func pickConfig(configs []string, match string) string {
	words := matchWords(match)
	if len(words) == 0 {
		return configs[0]
	}
	for _, c := range configs {
		name := strings.ToLower(configName(c))
		for _, w := range words {
			if strings.Contains(name, w) {
				return c
			}
		}
	}
	return configs[0]
}

// matchWords are the lower-case words of the setting: separated by commas (Persian ones too), semicolons or line breaks.
func matchWords(match string) []string {
	var out []string
	for _, w := range strings.FieldsFunc(match, func(r rune) bool { return r == ',' || r == '،' || r == ';' || r == '\n' }) {
		if w = strings.ToLower(strings.TrimSpace(w)); w != "" {
			out = append(out, w)
		}
	}
	return out
}

// configName is the name a share link carries, as the panel shows it (the inbound's remark and the client's name): the text after
// the "#" of a vless, trojan, ss or hysteria link, or the "ps" of a vmess link (base64 of a JSON object). Empty when there is none.
func configName(link string) string {
	if rest, ok := strings.CutPrefix(link, "vmess://"); ok {
		return vmessName(rest)
	}
	_, frag, ok := strings.Cut(link, "#")
	if !ok {
		return ""
	}
	if name, err := url.PathUnescape(frag); err == nil {
		return name
	}
	return frag
}

// vmessName is the "ps" field of a vmess link's payload; the base64 comes padded or not, standard or URL-safe.
func vmessName(payload string) string {
	if i := strings.IndexAny(payload, "#?"); i >= 0 { // a fragment some panels add after the payload
		if payload[i] == '#' {
			if name, err := url.PathUnescape(payload[i+1:]); err == nil && name != "" {
				return name
			}
		}
		payload = payload[:i]
	}
	payload = strings.TrimRight(payload, "=")
	var raw []byte
	for _, enc := range []*base64.Encoding{base64.RawStdEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(payload); err == nil {
			raw = b
			break
		}
	}
	var v struct {
		PS string `json:"ps"`
	}
	if raw == nil || json.Unmarshal(raw, &v) != nil {
		return ""
	}
	return v.PS
}
