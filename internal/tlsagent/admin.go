package tlsagent

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// admin talks to Caddy's admin endpoint on its unix socket. The socket's
// permissions are the only guard (Caddy checks neither Host nor Origin on a
// unix socket); a request must not carry an Origin header, or Caddy runs
// its origin check and answers 403.
type admin struct {
	hc *http.Client
}

func newAdmin(socket string, timeout time.Duration) *admin {
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		},
		DisableKeepAlives: true,
	}
	return &admin{hc: &http.Client{Transport: tr, Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

// RefusedError is Caddy's answer when it does not load a config (it keeps
// running the one it had).
type RefusedError struct {
	Status  int
	Message string
}

func (e *RefusedError) Error() string {
	return "caddy answered " + strconv.Itoa(e.Status) + ": " + e.Message
}

// load makes Caddy adapt and load the main Caddyfile again, with its
// imports, even when nothing in it changed (Cache-Control: must-revalidate:
// certificate files replaced under the same names need a forced load). The
// source headers keep Caddy's record of its config file, so SIGUSR1 still
// reloads it. Any other error means Caddy did not answer.
func (a *admin) load(ctx context.Context, caddyfile []byte, source string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://caddy/load", bytes.NewReader(caddyfile))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "text/caddyfile")
	req.Header.Set("Cache-Control", "must-revalidate")
	req.Header.Set("Caddy-Config-Source-File", source)
	req.Header.Set("Caddy-Config-Source-Adapter", "caddyfile")
	resp, err := a.hc.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	return &RefusedError{Status: resp.StatusCode, Message: caddyMessage(body, resp.StatusCode)}
}

// caddyMessage is the error in Caddy's answer: {"error": "..."} since 2.11.6,
// plain text before.
func caddyMessage(body []byte, status int) string {
	var out struct {
		Error string `json:"error"`
	}
	msg := strings.TrimSpace(string(body))
	if err := json.Unmarshal(body, &out); err == nil && out.Error != "" {
		msg = out.Error
	}
	msg = strings.Map(func(r rune) rune {
		if r < ' ' && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, msg)
	if msg == "" {
		msg = http.StatusText(status)
	}
	return clip(msg, 1500)
}
