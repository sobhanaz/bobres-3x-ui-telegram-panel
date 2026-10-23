package store

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
)

// What a person pastes to find a service: its subscription link (".../sub/<subscription id>"), the branded page's address
// (".../s/<token>") or a client config (vless://..., vmess://..., trojan://..., ss://...), whose name carries the client's email.
// The services lists use it, so a search by any of them finds the service.

// linkSecretRe is what the end of a link looks like: letters, digits, "." "_" "-", 8 to 64 of them.
var linkSecretRe = regexp.MustCompile(`^[A-Za-z0-9._-]{8,64}$`)

// ParseLinkSecret reads the secret out of what was sent: a whole link (a query or a fragment after it is ignored, a closing "/" too), the
// end of one, or the secret itself. false when it is none of them.
func ParseLinkSecret(input string) (string, bool) {
	s := strings.TrimSpace(input)
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil {
			return "", false
		}
		s = u.Path
	} else if i := strings.IndexAny(s, "?#"); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimRight(s, "/")
	if i := strings.LastIndex(s, "/"); i >= 0 {
		s = s[i+1:]
	}
	if !linkSecretRe.MatchString(s) {
		return "", false
	}
	return s, true
}

// configSchemes are the client config links of the proxy protocols the panels hand out.
var configSchemes = map[string]bool{"vless": true, "vmess": true, "trojan": true, "ss": true, "hysteria": true, "hysteria2": true, "tuic": true}

// configRemark is the name a client config carries, lower case: the part after "#" of a vless, trojan or ss link, the "ps" of a vmess one.
// "" when the input is not a config link or names nothing.
func configRemark(input string) string {
	s := strings.TrimSpace(input)
	scheme, rest, ok := strings.Cut(s, "://")
	if !ok || !configSchemes[strings.ToLower(scheme)] {
		return ""
	}
	if strings.EqualFold(scheme, "vmess") {
		payload, _, _ := strings.Cut(rest, "#")
		payload = strings.TrimRight(payload, "=")
		raw, err := base64.RawStdEncoding.DecodeString(payload)
		if err != nil {
			if raw, err = base64.RawURLEncoding.DecodeString(payload); err != nil {
				return ""
			}
		}
		var v struct {
			PS string `json:"ps"`
		}
		if json.Unmarshal(raw, &v) != nil {
			return ""
		}
		return strings.ToLower(strings.TrimSpace(v.PS))
	}
	_, frag, found := strings.Cut(rest, "#")
	if !found {
		return ""
	}
	if un, err := url.PathUnescape(frag); err == nil {
		frag = un
	}
	return strings.ToLower(strings.TrimSpace(frag))
}

// serviceLinkTerms is what a search says when it is a link: the secret of a subscription link or page address, or the name of a client
// config (which holds the client's email). Both are "" for an ordinary search (a name or a number).
func serviceLinkTerms(query string) (secret, remark string) {
	if remark = configRemark(query); remark != "" {
		return "", remark
	}
	if strings.Contains(query, "://") || strings.Contains(query, "/") {
		secret, _ = ParseLinkSecret(query)
	}
	return secret, ""
}
