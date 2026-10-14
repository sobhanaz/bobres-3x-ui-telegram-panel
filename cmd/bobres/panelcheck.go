package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/provisioner/adapter"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/xui"
)

// panelTokenPrompt asks for the 3x-ui API token (typed hidden).
const panelTokenPrompt = "3x-ui API token (panel Settings > Security): " //nolint:gosec // a prompt, not a credential

// panelAttempts is how many times the wizard asks again after a panel check
// fails.
const panelAttempts = 3

// panelInfo is what a look at a 3x-ui panel found.
type panelInfo struct {
	version  string
	inbounds int
}

// probePanel connects to a panel the way the provisioner does (the same client
// and adapter, its health check: the inbound listing, which needs the token,
// then the version) and reports what it found.
func probePanel(ctx context.Context, baseURL, token string, allowPrivate bool) (panelInfo, error) {
	var opts []xui.Option
	if allowPrivate {
		opts = append(opts, xui.AllowPrivateAddresses())
	}
	a, err := adapter.NewXUIv3(baseURL, token, opts...)
	if err != nil {
		return panelInfo{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ins, err := a.Inbounds(ctx)
	if err != nil {
		return panelInfo{}, err
	}
	return panelInfo{version: a.PanelVersion(ctx), inbounds: len(ins)}, nil
}

// panelOnThisServer settles what the owner could be asked and has one answer
// to: a plain-http panel on this server (localhost) is reached by the
// containers as host.docker.internal, and a plain-http panel on a private
// address is a private panel. A plain-http panel anywhere else is left as it
// is: validation refuses it and says to use https.
func (ins *installer) panelOnThisServer(env map[string]string) {
	u, err := url.Parse(env["BOBRES_XUI_URL"])
	if err != nil || u.Scheme != "http" || env["BOBRES_XUI_ALLOW_PRIVATE"] == "true" {
		return
	}
	switch ip := net.ParseIP(u.Hostname()); {
	case loopbackHost(u.Hostname()):
		host := "host.docker.internal"
		if p := u.Port(); p != "" {
			host += ":" + p
		}
		u.Host = host
		env["BOBRES_XUI_URL"], env["BOBRES_XUI_ALLOW_PRIVATE"] = u.String(), "true"
		fmt.Fprintf(ins.out, "The panel is on this server: the BOBRES containers reach it as %s.\n", host)
	case ip != nil && (ip.IsPrivate() || ip.IsLinkLocalUnicast()):
		env["BOBRES_XUI_ALLOW_PRIVATE"] = "true"
		fmt.Fprintf(ins.out, "The panel is on a private network (%s): it is used as a private panel.\n", ip)
	}
}

// panelCheckURL is the address to check a panel at from this server: the
// containers reach a panel on this server as host.docker.internal, which only
// resolves inside them, so here it is 127.0.0.1. onHost says it was changed.
func panelCheckURL(raw string) (checkURL string, onHost bool) {
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(u.Hostname(), "host.docker.internal") {
		return raw, false
	}
	host := "127.0.0.1"
	if p := u.Port(); p != "" {
		host += ":" + p
	}
	u.Host = host
	return u.String(), true
}

// panelProblem is why a panel cannot be used and what to do about it.
type panelProblem struct {
	// kind is what to ask for again: "token", or "url" (the address, the
	// panel's version).
	kind string
	msg  string
	fix  string
}

// panelTokenFix is what to do about a token the panel refuses.
const panelTokenFix = "In 3x-ui open Settings, then API tokens (Security), create a token and paste it here." //nolint:gosec // advice, not a credential

// panelProblemOf judges a look at a panel: nil when the panel can be used (it
// answered and took the token).
func panelProblemOf(info panelInfo, err error, host string) *panelProblem {
	if err == nil {
		// A version outside the tested range is not a problem here: the panel answered and took the
		// token, and the provisioner and the dashboard only warn about it. verifyPanel says so.
		return nil
	}
	var (
		ae  *xui.APIError
		dns *net.DNSError
		ua  x509.UnknownAuthorityError
		he  x509.HostnameError
		ci  x509.CertificateInvalidError
	)
	text := strings.TrimPrefix(err.Error(), "xui: ")
	switch {
	case xui.IsUnauthorized(err):
		return &panelProblem{"token", "the panel refused the API token", panelTokenFix}
	case errors.Is(err, xui.ErrInsecureURL), errors.Is(err, xui.ErrPublicPlaintext), errors.Is(err, xui.ErrBlockedAddress):
		return &panelProblem{"url", text, "Use the panel's https URL (plain http is only for a panel on this server or a private network)."}
	case errors.As(err, &ae):
		if ae.Status == 404 || ae.Status/100 == 3 || strings.Contains(ae.Msg, "non-JSON") || strings.Contains(ae.Msg, "redirect") {
			return &panelProblem{"url", fmt.Sprintf("%s answers, but not as the 3x-ui API (HTTP %d)", host, ae.Status),
				"Check the URL: it must carry the panel's web base path (3x-ui Settings > Panel > URL path), like https://host:2053/abc."}
		}
		return &panelProblem{"url", "the panel answered with an error: " + strings.TrimPrefix(ae.Error(), "xui: panel error "), "Check the URL and that this is a 3x-ui panel."}
	case errors.As(err, &dns):
		return &panelProblem{"url", fmt.Sprintf("the name %s does not resolve (DNS)", dns.Name), "Check the host name and its DNS record."}
	case errors.Is(err, syscall.ECONNREFUSED):
		return &panelProblem{"url", fmt.Sprintf("nothing listens on %s", host), "Check that 3x-ui runs (x-ui status), and the port and the http/https scheme."}
	case errors.Is(err, context.DeadlineExceeded) || os.IsTimeout(err) || isTimeout(err):
		return &panelProblem{"url", fmt.Sprintf("no answer from %s in time", host), "A firewall may block the port, or the port is wrong."}
	case errors.As(err, &ua), errors.As(err, &he), errors.As(err, &ci):
		return &panelProblem{"url", "the panel's HTTPS certificate is not trusted: " + text, "Give 3x-ui a certificate for its host name (Let's Encrypt in its settings), then use that name in the URL."}
	case strings.Contains(text, "first record does not look like a TLS handshake") || strings.Contains(text, "HTTP response to HTTPS client"):
		return &panelProblem{"url", "the panel speaks plain http, not https", "Use http:// (only for a panel on this server or a private network) or enable HTTPS in 3x-ui."}
	case errors.As(err, new(tls.RecordHeaderError)):
		return &panelProblem{"url", "the panel speaks plain http, not https", "Use http:// (only for a panel on this server or a private network) or enable HTTPS in 3x-ui."}
	}
	return &panelProblem{"url", "could not talk to the panel: " + text, "Check the URL, and that this server can reach the panel."}
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// askSecretOptional asks for a secret that may be left empty (Enter): eof is
// true when there is nobody to ask (no terminal, or the input ended).
func (ins *installer) askSecretOptional(prompt string) (v string, eof bool) {
	if ins.readHide != nil {
		v, err := ins.readHide(prompt)
		return strings.TrimSpace(v), err != nil
	}
	fmt.Fprint(ins.out, prompt)
	line, err := ins.in.ReadString('\n')
	return strings.TrimSpace(line), err != nil && line == ""
}

// verifyPanel looks at the panel in env before anything is written, the way
// the provisioner will: reachable, a supported version, and the API token
// accepted by an authenticated call. What fails is said with what to do, and
// the owner is asked again (up to panelAttempts times); Enter skips the panel
// (the server is added later in the dashboard). It returns an exit code.
func (ins *installer) verifyPanel(ctx context.Context, env map[string]string) int {
	if ins.panelProbe == nil {
		return 0
	}
	for attempt := 0; ; attempt++ {
		checkURL, onHost := panelCheckURL(env["BOBRES_XUI_URL"])
		host := "the panel"
		if u, err := url.Parse(checkURL); err == nil {
			host = u.Host
		}
		fmt.Fprintf(ins.out, "Checking the 3x-ui panel at %s ...\n", host)
		info, err := ins.panelProbe(ctx, checkURL, env["BOBRES_XUI_TOKEN"], env["BOBRES_XUI_ALLOW_PRIVATE"] == "true")
		prob := panelProblemOf(info, err, host)
		if prob == nil {
			where := "checked from this server; the containers reach it the same way"
			if onHost {
				where = "checked from this server at " + host + "; the containers reach it as host.docker.internal, " +
					"which the firewall rule that hardening adds allows from Docker's networks"
			}
			fmt.Fprintf(ins.out, "3x-ui panel OK: %s, API token accepted, %d inbound(s) (%s).\n", orDash(info.version), info.inbounds, where)
			if adapter.Untested(info.version) {
				fmt.Fprintf(ins.out, "Note: 3x-ui %s is outside the versions BOBRES was tested with (%s to %s). It is used anyway, and the dashboard shows a warning.\n",
					info.version, adapter.TestedMinVersion, adapter.TestedMaxVersion)
			}
			return 0
		}
		fmt.Fprintf(ins.errOut, "3x-ui panel check failed: %s.\n  %s\n", prob.msg, prob.fix)
		if onHost {
			fmt.Fprintln(ins.errOut, "  (This is checked from this server; the provisioner runs in a container and reaches the panel as host.docker.internal.)")
		}
		if attempt >= panelAttempts {
			fmt.Fprintf(ins.errOut, "error: the 3x-ui panel was refused %d times, so nothing was written. Fix it and run the install again, "+
				"or press Enter at the panel question to add the server later in the dashboard.\n", attempt+1)
			return 2
		}
		var eof bool
		if prob.kind == "token" {
			var v string
			if v, eof = ins.askSecretOptional("New 3x-ui API token (Enter to skip and add the server later in the dashboard): "); v != "" {
				env["BOBRES_XUI_TOKEN"] = v
				continue
			} else if !eof {
				return ins.skipPanel(env)
			}
		} else {
			var v string
			fmt.Fprint(ins.out, "3x-ui panel URL (Enter to skip and add the server later in the dashboard): ")
			line, err := ins.in.ReadString('\n')
			v, eof = strings.TrimSpace(line), err != nil && line == ""
			if v != "" {
				env["BOBRES_XUI_URL"], env["BOBRES_XUI_ALLOW_PRIVATE"] = v, "false"
				ins.panelOnThisServer(env)
				continue
			} else if !eof {
				return ins.skipPanel(env)
			}
		}
		fmt.Fprintln(ins.errOut, "error: the 3x-ui panel was refused and there is no one to ask again, so nothing was written. "+
			"Fix the panel URL or token and run the install again.")
		return 2
	}
}

// skipPanel drops the panel from this install: the server is added in the
// dashboard.
func (ins *installer) skipPanel(env map[string]string) int {
	env["BOBRES_XUI_URL"], env["BOBRES_XUI_TOKEN"] = "", ""
	fmt.Fprintln(ins.out, "Skipped: the install goes on without a 3x-ui panel. Add the server in the dashboard (Servers).")
	return 0
}
