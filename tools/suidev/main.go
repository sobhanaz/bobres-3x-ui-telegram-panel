// Command suidev prepares a fresh S-UI panel for development and CI: it waits
// for the panel, signs in with the admin's username and password (a fresh
// panel's is admin/admin), makes an API token that does not expire, and adds the
// inbounds the tests use (VLESS, Trojan and a mixed HTTP/SOCKS one, all without
// TLS) unless the panel has them. It prints the token on stdout and the panel's
// version on stderr.
//
//	suidev -url http://127.0.0.1:2095/app
//
// It is a test fixture: it talks to throwaway panels only (a loopback or
// private address unless -force), so it accepts plain HTTP. The CI job "S-UI
// integration" runs it against the alireza7/s-ui image.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
	"time"
)

// The inbounds the integration tests use, by tag. The mixed one carries real
// traffic in the usage test.
var inbounds = []map[string]any{
	{"type": "vless", "tag": "bobres-vless", "listen": "::", "listen_port": 2443},
	{"type": "trojan", "tag": "bobres-trojan", "listen": "::", "listen_port": 2444},
	{"type": "mixed", "tag": "bobres-mixed", "listen": "::", "listen_port": 2445},
}

func main() {
	base := flag.String("url", "http://127.0.0.1:2095/app", "panel address with its web path")
	user := flag.String("user", "admin", "admin username")
	pass := flag.String("pass", "admin", "admin password")
	wait := flag.Duration("wait", 3*time.Minute, "how long to wait for the panel to answer")
	noInbounds := flag.Bool("no-inbounds", false, "add no inbounds")
	force := flag.Bool("force", false, "allow a panel that is not on a loopback or private address")
	flag.Parse()

	if err := throwaway(*base); err != nil && !*force {
		fmt.Fprintln(os.Stderr, "suidev:", err, "(pass -force to use it anyway)")
		os.Exit(2)
	}
	jar, _ := cookiejar.New(nil)
	p := &panel{base: strings.TrimRight(*base, "/"), hc: &http.Client{Timeout: 30 * time.Second, Jar: jar}}
	ctx, cancel := context.WithTimeout(context.Background(), *wait+2*time.Minute)
	defer cancel()
	token, version, err := run(ctx, p, *user, *pass, *wait, !*noInbounds)
	if err != nil {
		fmt.Fprintln(os.Stderr, "suidev:", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, "S-UI", version)
	fmt.Println(token)
}

func throwaway(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return fmt.Errorf("invalid panel URL %q", raw)
	}
	ips, err := net.LookupIP(u.Hostname())
	if err != nil {
		return fmt.Errorf("resolve %s: %w", u.Hostname(), err)
	}
	for _, ip := range ips {
		if !ip.IsLoopback() && !ip.IsPrivate() {
			return fmt.Errorf("%s is not a throwaway panel address (%s)", u.Hostname(), ip)
		}
	}
	return nil
}

type panel struct {
	base  string
	hc    *http.Client
	token string
}

type msg struct {
	Success bool            `json:"success"`
	Msg     string          `json:"msg"`
	Obj     json.RawMessage `json:"obj"`
}

// call sends a form to <base>/<path> (the session API, "api/...", or the token
// API, "apiv2/...") and reads the panel's answer.
func (p *panel) call(ctx context.Context, method, path string, form url.Values) (json.RawMessage, error) {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, p.base+"/"+path, body)
	if err != nil {
		return nil, err
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	// The session API refuses a write without this (or an Origin of its own host).
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	if p.token != "" {
		req.Header.Set("Token", p.token)
	}
	resp, err := p.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s %s: http %d", method, path, resp.StatusCode)
	}
	var m msg
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%s %s: not a panel answer", method, path)
	}
	if !m.Success {
		return nil, fmt.Errorf("%s %s: %s", method, path, m.Msg)
	}
	return m.Obj, nil
}

func run(ctx context.Context, p *panel, user, pass string, wait time.Duration, addInbounds bool) (string, string, error) {
	deadline := time.Now().Add(wait)
	for {
		_, err := p.call(ctx, http.MethodPost, "api/login", url.Values{"user": {user}, "pass": {pass}})
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			return "", "", fmt.Errorf("the panel did not let the admin in within %s: %w", wait, err)
		}
		select {
		case <-ctx.Done():
			return "", "", ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	obj, err := p.call(ctx, http.MethodPost, "api/addToken", url.Values{"expiry": {"0"}, "desc": {"bobres-dev"}})
	if err != nil {
		return "", "", fmt.Errorf("make an API token: %w", err)
	}
	if err := json.Unmarshal(obj, &p.token); err != nil || p.token == "" {
		return "", "", errors.New("make an API token: the panel gave none")
	}
	if addInbounds {
		obj, err := p.call(ctx, http.MethodGet, "apiv2/inbounds", nil)
		if err != nil {
			return "", "", err
		}
		var have struct {
			Inbounds []struct {
				Tag string `json:"tag"`
			} `json:"inbounds"`
		}
		_ = json.Unmarshal(obj, &have)
		tags := map[string]bool{}
		for _, in := range have.Inbounds {
			tags[in.Tag] = true
		}
		for _, in := range inbounds {
			if tags[in["tag"].(string)] {
				continue
			}
			full := map[string]any{"tls_id": 0, "addrs": []any{}, "out_json": map[string]any{}}
			for k, v := range in {
				full[k] = v
			}
			data, _ := json.Marshal(full)
			if _, err := p.call(ctx, http.MethodPost, "apiv2/save", url.Values{"object": {"inbounds"}, "action": {"new"}, "data": {string(data)}, "initUsers": {""}}); err != nil {
				return "", "", fmt.Errorf("add inbound %s: %w", in["tag"], err)
			}
		}
	}
	obj, err = p.call(ctx, http.MethodGet, "apiv2/status?r=sys", nil)
	if err != nil {
		return "", "", err
	}
	var st struct {
		Sys struct {
			AppVersion string `json:"appVersion"`
		} `json:"sys"`
	}
	_ = json.Unmarshal(obj, &st)
	return p.token, st.Sys.AppVersion, nil
}
