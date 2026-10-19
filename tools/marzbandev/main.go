// Command marzbandev prepares a fresh Marzban panel for development and CI: it
// waits for the panel, logs in with the account the container was started with
// (SUDO_USERNAME and SUDO_PASSWORD), creates a database sudo admin the way
// `marzban cli admin create --sudo` does, and makes sure the panel serves the
// four protocols the tests use (VLESS, VMess, Trojan and Shadowsocks, all
// without TLS) by adding the missing inbounds to its Xray configuration, and
// lets clients reach private addresses (the tests send traffic to the panel's
// own address). It prints the panel's version on stdout.
//
//	marzbandev -url http://127.0.0.1:8000 -bootstrap-user boot -bootstrap-pass ... -user bobres-ci -pass ...
//
// Why a database admin and not the SUDO_ account itself: users made through
// that account have no owner, and Marzban v0.8 answers 500 to deleting them
// (after removing the row, before removing the account from the running core).
// The adapter works around it, which the tests check with that account too.
//
// It is a test fixture: it talks to throwaway panels only, so it accepts plain
// HTTP and private addresses. local.sh in this directory starts a panel in
// Docker, runs this and the integration tests; the CI job "Marzban
// integration" does the same steps.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"
)

func main() {
	base := flag.String("url", "http://127.0.0.1:8000", "panel URL including the base path, if any")
	bootUser := flag.String("bootstrap-user", "", "the SUDO_USERNAME the panel was started with")
	bootPass := flag.String("bootstrap-pass", "", "the SUDO_PASSWORD the panel was started with")
	user := flag.String("user", "bobres-ci", "username of the admin to create")
	pass := flag.String("pass", "", "password of the admin to create")
	wait := flag.Duration("wait", 3*time.Minute, "how long to wait for the panel to answer")
	noInbounds := flag.Bool("no-inbounds", false, "leave the Xray configuration alone")
	force := flag.Bool("force", false, "allow a panel that is not on a loopback or private address")
	flag.Parse()

	if *bootUser == "" || *bootPass == "" || *user == "" || *pass == "" {
		fmt.Fprintln(os.Stderr, "marzbandev: -bootstrap-user, -bootstrap-pass, -user and -pass are required")
		os.Exit(2)
	}
	// It creates an admin and rewrites the Xray configuration: only for
	// throwaway panels unless explicitly forced.
	if err := throwaway(*base); err != nil && !*force {
		fmt.Fprintln(os.Stderr, "marzbandev:", err, "(pass -force to use it anyway)")
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *wait+3*time.Minute)
	defer cancel()
	p := &panel{base: strings.TrimRight(*base, "/"), hc: &http.Client{Timeout: 30 * time.Second}}
	version, err := run(ctx, p, *bootUser, *bootPass, *user, *pass, *wait, !*noInbounds)
	if err != nil {
		fmt.Fprintln(os.Stderr, "marzbandev:", err)
		os.Exit(1)
	}
	fmt.Println(version)
}

func run(ctx context.Context, p *panel, bootUser, bootPass, user, pass string, wait time.Duration, inbounds bool) (string, error) {
	if err := p.waitUp(ctx, wait); err != nil {
		return "", err
	}
	boot, err := p.login(ctx, bootUser, bootPass)
	if err != nil {
		return "", fmt.Errorf("log in as the bootstrap account: %w", err)
	}
	status, err := p.call(ctx, http.MethodPost, "/api/admin", boot, map[string]any{"username": user, "password": pass, "is_sudo": true}, nil)
	switch {
	case err == nil:
		fmt.Fprintf(os.Stderr, "marzbandev: admin %s created\n", user)
	case status == http.StatusConflict:
		fmt.Fprintf(os.Stderr, "marzbandev: admin %s exists already\n", user)
	default:
		return "", fmt.Errorf("create admin: %w", err)
	}
	tok, err := p.login(ctx, user, pass)
	if err != nil {
		return "", fmt.Errorf("log in as the new admin: %w", err)
	}
	if inbounds {
		if err := p.ensureInbounds(ctx, tok); err != nil {
			return "", err
		}
	}
	var sys struct {
		Version string `json:"version"`
	}
	if _, err := p.call(ctx, http.MethodGet, "/api/system", tok, nil, &sys); err != nil {
		return "", fmt.Errorf("read the version: %w", err)
	}
	return "v" + strings.TrimPrefix(sys.Version, "v"), nil
}

type panel struct {
	base string
	hc   *http.Client
}

// throwaway accepts panels on loopback or private addresses only.
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

// waitUp waits until the panel answers anything but a server error: Marzban
// starts listening once its startup (database migrations, the Xray core) is
// done.
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

// login signs in with the OAuth2 password form and returns the bearer token.
func (p *panel) login(ctx context.Context, user, pass string) (string, error) {
	form := url.Values{"username": {user}, "password": {pass}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.base+"/api/admin/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := p.hc.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var out struct {
		AccessToken string `json:"access_token"`
	}
	if resp.StatusCode != http.StatusOK || json.Unmarshal(raw, &out) != nil || out.AccessToken == "" {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, detail(raw))
	}
	return out.AccessToken, nil
}

// call sends a JSON request as the admin whose token is given and decodes the
// answer into out when it is not nil. It returns the HTTP status too.
func (p *panel) call(ctx context.Context, method, path, token string, body, out any) (int, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, p.base+path, rd)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := p.hc.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return resp.StatusCode, fmt.Errorf("%s %s: HTTP %d: %s", method, path, resp.StatusCode, detail(raw))
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return resp.StatusCode, fmt.Errorf("%s %s: unexpected response", method, path)
		}
	}
	return resp.StatusCode, nil
}

// detail reads the message of an error answer, bounded.
func detail(raw []byte) string {
	var d struct {
		Detail json.RawMessage `json:"detail"`
	}
	msg := strings.TrimSpace(string(raw))
	if json.Unmarshal(raw, &d) == nil && len(d.Detail) > 0 {
		msg = string(d.Detail)
	}
	if len(msg) > 200 {
		msg = msg[:200]
	}
	return msg
}

// wanted are the inbounds the tests need beside Marzban's default Shadowsocks
// one, by protocol. None uses TLS, so a fresh panel can serve them.
var wanted = []map[string]any{
	{"tag": "VLESS TCP", "listen": "0.0.0.0", "port": 2443, "protocol": "vless",
		"settings": map[string]any{"clients": []any{}, "decryption": "none"}, "streamSettings": plainTCP()},
	{"tag": "VMess TCP", "listen": "0.0.0.0", "port": 2444, "protocol": "vmess",
		"settings": map[string]any{"clients": []any{}}, "streamSettings": plainTCP()},
	{"tag": "Trojan TCP", "listen": "0.0.0.0", "port": 2445, "protocol": "trojan",
		"settings": map[string]any{"clients": []any{}}, "streamSettings": plainTCP()},
}

func plainTCP() map[string]any { return map[string]any{"network": "tcp", "security": "none"} }

// withInbounds adds the wanted inbounds a configuration lacks, by tag and by
// protocol, and says whether it changed anything. The rest of the
// configuration is kept as it is.
func withInbounds(cfg map[string]any) bool {
	have, _ := cfg["inbounds"].([]any)
	tags, protocols := map[string]bool{}, map[string]bool{}
	for _, in := range have {
		m, _ := in.(map[string]any)
		tag, _ := m["tag"].(string)
		proto, _ := m["protocol"].(string)
		tags[tag], protocols[proto] = true, true
	}
	changed := false
	for _, w := range wanted {
		if tags[w["tag"].(string)] || protocols[w["protocol"].(string)] {
			continue
		}
		have = append(have, w)
		changed = true
	}
	cfg["inbounds"] = have
	return changed
}

// withoutPrivateBlock drops the routing rule that sends traffic to private
// addresses to the blackhole (Marzban's default configuration has it), so a
// client of the panel can reach a server on the panel's own host: the tests
// push traffic to the panel's own address. It says whether it changed
// anything.
func withoutPrivateBlock(cfg map[string]any) bool {
	routing, _ := cfg["routing"].(map[string]any)
	rules, _ := routing["rules"].([]any)
	kept := make([]any, 0, len(rules))
	for _, r := range rules {
		m, _ := r.(map[string]any)
		ips, _ := m["ip"].([]any)
		if m["outboundTag"] == "BLOCK" && slices.Contains(ips, any("geoip:private")) {
			continue
		}
		kept = append(kept, r)
	}
	if len(kept) == len(rules) {
		return false
	}
	routing["rules"] = kept
	return true
}

// ensureInbounds makes the panel serve the wanted protocols and let clients
// reach private addresses, through its own API (PUT /api/core/config restarts
// the core), and waits until it lists the inbounds.
func (p *panel) ensureInbounds(ctx context.Context, token string) error {
	var cfg map[string]any
	if _, err := p.call(ctx, http.MethodGet, "/api/core/config", token, nil, &cfg); err != nil {
		return fmt.Errorf("read the Xray configuration: %w", err)
	}
	added, opened := withInbounds(cfg), withoutPrivateBlock(cfg)
	if added || opened {
		if _, err := p.call(ctx, http.MethodPut, "/api/core/config", token, cfg, nil); err != nil {
			return fmt.Errorf("write the Xray configuration: %w", err)
		}
		fmt.Fprintf(os.Stderr, "marzbandev: Xray configuration written (inbounds added: %v, private addresses opened: %v)\n", added, opened)
	}
	deadline := time.Now().Add(time.Minute)
	for {
		var by map[string][]json.RawMessage
		if _, err := p.call(ctx, http.MethodGet, "/api/inbounds", token, nil, &by); err == nil {
			ok := true
			for _, w := range wanted {
				if len(by[w["protocol"].(string)]) == 0 {
					ok = false
				}
			}
			if ok {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return errors.New("the panel does not list the inbounds that were added")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}
