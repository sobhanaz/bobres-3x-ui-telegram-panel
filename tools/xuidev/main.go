// Command xuidev prepares a fresh 3x-ui v3 panel for development and CI: it
// logs in with the admin credentials, mints an admin API token and makes sure
// at least one enabled inbound exists, then prints the token on stdout.
//
//	xuidev -url http://127.0.0.1:2053 -user admin -pass admin
//
// It is a test fixture: it talks to throwaway panels only (default image
// credentials), so it accepts plain HTTP and private addresses.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"strings"
	"time"
)

type envelope struct {
	Success bool            `json:"success"`
	Msg     string          `json:"msg"`
	Obj     json.RawMessage `json:"obj"`
}

type panel struct {
	base  string
	hc    *http.Client
	csrf  string
	token string
}

func main() {
	base := flag.String("url", "http://127.0.0.1:2053", "panel URL including the web base path")
	user := flag.String("user", "admin", "panel admin username")
	pass := flag.String("pass", "admin", "panel admin password")
	port := flag.Int("inbound-port", 8443, "port of the inbound created when none is enabled")
	wait := flag.Duration("wait", 3*time.Minute, "how long to wait for the panel to answer")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), *wait+time.Minute)
	defer cancel()
	jar, _ := cookiejar.New(nil)
	p := &panel{base: strings.TrimRight(*base, "/"), hc: &http.Client{Jar: jar, Timeout: 20 * time.Second}}
	if err := run(ctx, p, *user, *pass, *port, *wait); err != nil {
		fmt.Fprintln(os.Stderr, "xuidev:", err)
		os.Exit(1)
	}
	fmt.Println(p.token)
}

func run(ctx context.Context, p *panel, user, pass string, port int, wait time.Duration) error {
	if err := p.waitUp(ctx, wait); err != nil {
		return err
	}
	if err := p.refreshCSRF(ctx); err != nil {
		return err
	}
	if _, err := p.call(ctx, http.MethodPost, "/login", map[string]string{"username": user, "password": pass}, false); err != nil {
		return fmt.Errorf("login: %w", err)
	}
	if err := p.refreshCSRF(ctx); err != nil { // the session may bind a new one
		return err
	}
	obj, err := p.call(ctx, http.MethodPost, "/panel/api/setting/apiTokens/create",
		map[string]any{"name": fmt.Sprintf("bobres-dev-%d", time.Now().Unix()), "scope": "admin", "expiresAt": 0}, false)
	if err != nil {
		return fmt.Errorf("create API token: %w", err)
	}
	var tok struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(obj, &tok); err != nil || tok.Token == "" {
		return fmt.Errorf("create API token: no token in response")
	}
	p.token = tok.Token
	fmt.Fprintln(os.Stderr, "xuidev: API token created")

	obj, err = p.call(ctx, http.MethodGet, "/panel/api/inbounds/options", nil, true)
	if err != nil {
		return fmt.Errorf("list inbounds with the new token: %w", err)
	}
	var ins []struct {
		ID     int  `json:"id"`
		Enable bool `json:"enable"`
	}
	_ = json.Unmarshal(obj, &ins)
	for _, in := range ins {
		if in.Enable {
			fmt.Fprintf(os.Stderr, "xuidev: enabled inbound %d already exists\n", in.ID)
			return nil
		}
	}
	inbound := map[string]any{
		"enable": true, "remark": "bobres-dev", "listen": "", "port": port, "protocol": "vless",
		"expiryTime": 0, "total": 0,
		"settings":       map[string]any{"clients": []any{}, "decryption": "none", "fallbacks": []any{}},
		"streamSettings": map[string]any{"network": "tcp", "security": "none", "tcpSettings": map[string]any{"header": map[string]any{"type": "none"}}},
		"sniffing":       map[string]any{"enabled": false, "destOverride": []string{"http", "tls"}},
	}
	if _, err := p.call(ctx, http.MethodPost, "/panel/api/inbounds/add", inbound, true); err != nil {
		return fmt.Errorf("create inbound: %w", err)
	}
	fmt.Fprintf(os.Stderr, "xuidev: created vless inbound on port %d\n", port)
	return nil
}

func (p *panel) waitUp(ctx context.Context, wait time.Duration) error {
	deadline := time.Now().Add(wait)
	for {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, p.base+"/", nil)
		if resp, err := p.hc.Do(req); err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode < 500 {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("panel at %s did not answer within %s", p.base, wait)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func (p *panel) refreshCSRF(ctx context.Context) error {
	obj, err := p.call(ctx, http.MethodGet, "/csrf-token", nil, false)
	if err != nil {
		return fmt.Errorf("csrf token: %w", err)
	}
	var s string
	if json.Unmarshal(obj, &s) == nil {
		p.csrf = s
		return nil
	}
	var o struct {
		Token string `json:"token"`
	}
	if json.Unmarshal(obj, &o) == nil {
		p.csrf = o.Token
	}
	return nil
}

// call sends a request with the session cookie and CSRF header, or with the
// API token when bearer is true, and returns the envelope's obj.
func (p *panel) call(ctx context.Context, method, path string, body any, bearer bool) (json.RawMessage, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, p.base+path, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer {
		req.Header.Set("Authorization", "Bearer "+p.token)
	} else if p.csrf != "" {
		req.Header.Set("X-CSRF-Token", p.csrf)
	}
	resp, err := p.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("%s %s: HTTP %d, non-JSON response", method, path, resp.StatusCode)
	}
	if resp.StatusCode >= 400 || !env.Success {
		if env.Msg == "" {
			env.Msg = http.StatusText(resp.StatusCode)
		}
		return nil, errors.New(env.Msg)
	}
	return env.Obj, nil
}
