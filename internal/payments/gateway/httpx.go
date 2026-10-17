package gateway

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Helpers shared by the redirect gateways (IDPay, NextPay): an HTTP client with
// the timeouts and the optional proxy, and the number and text handling their
// JSON needs.

// Timeouts for every call to a gateway: a gateway that stops answering must
// not hold a customer's request or the reconciler.
const (
	clientTimeout         = 25 * time.Second
	handshakeTimeout      = 10 * time.Second
	responseHeaderTimeout = 20 * time.Second
)

// NewHTTPClient makes the client a gateway calls its API with. proxy ("" =
// none) is an http(s) URL every call is routed through (an egress IP the
// gateway has registered); a redirect is never followed, so a key header or a
// body cannot follow one to another host. name prefixes the errors.
func NewHTTPClient(name, proxy string) (*http.Client, error) {
	tr := &http.Transport{Proxy: nil, TLSHandshakeTimeout: handshakeTimeout, ResponseHeaderTimeout: responseHeaderTimeout,
		IdleConnTimeout: 90 * time.Second} // a replaced or tested client lets its connections go
	if proxy != "" {
		pu, err := url.Parse(proxy)
		if err != nil || (pu.Scheme != "http" && pu.Scheme != "https") || pu.Host == "" {
			return nil, errors.New(name + ": the proxy must be an http(s) URL")
		}
		tr.Proxy = http.ProxyURL(pu)
	}
	return &http.Client{Timeout: clientTimeout, Transport: tr,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, nil
}

// FlexInt reads 100, "100", 100.0 and null (the gateways' samples mix them).
type FlexInt int64

// UnmarshalJSON implements json.Unmarshaler.
func (f *FlexInt) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(bytes.TrimSpace(b)), `"`)
	if s == "" || s == "null" {
		*f = 0
		return nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		d, ferr := strconv.ParseFloat(s, 64)
		if ferr != nil || d != float64(int64(d)) {
			return fmt.Errorf("gateway: not a whole number: %q", s)
		}
		n = int64(d)
	}
	*f = FlexInt(n)
	return nil
}

// Sanitize makes a gateway's message safe for logs and the dashboard: control
// characters become spaces and the text is cut to 200 characters.
func Sanitize(m string) string {
	m = strings.Map(func(r rune) rune {
		if r < 0x20 {
			return ' '
		}
		return r
	}, m)
	if r := []rune(m); len(r) > 200 {
		m = string(r[:200])
	}
	return m
}
