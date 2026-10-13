package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tlsconf"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tlsprobe"
)

const sslUsage = `usage: bobres ssl [status] [--dir D]                  the HTTPS setting, and what this server serves now
       bobres ssl auto [--dir D]                     an automatic certificate for the domain
       bobres ssl ip [--ip ADDR] [--yes] [--dir D]   a Let's Encrypt certificate for the server's IP address
                                                     (about 6 days, renewed automatically); ADDR defaults to the detected one
       bobres ssl manual --cert FILE --key FILE [--dir D]
                                                     your own certificate chain and private key (PEM files)
The dashboard (System > HTTPS) does the same.`

// sslCmd is `bobres ssl`: it shows or changes the store's HTTPS setting by
// running core's own `ssl` command in its container, so the CLI and the
// dashboard change the same state. The mode lives in the database; the
// tls-agent applies it to Caddy. An install without the tls-agent (images from
// before the HTTPS manager) is told so and nothing runs in core: its `/app`
// has no `ssl` command, and would start a second core process instead.
func (o *ops) sslCmd(args []string) int {
	sub := "status"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub, args = args[0], args[1:]
	}
	if sub == "help" || (len(args) > 0 && (args[0] == "-h" || args[0] == "--help")) {
		fmt.Fprintln(o.out, sslUsage)
		return 0
	}
	fs := flag.NewFlagSet("ssl "+sub, flag.ContinueOnError)
	fs.SetOutput(o.errOut)
	dir := fs.String("dir", defaultDir(), dirUsage)
	var ip, cert, key string
	var yes bool
	switch sub {
	case "status", "auto":
	case "ip":
		fs.StringVar(&ip, "ip", "", "the server's public IP address (default: the detected one)")
		fs.BoolVar(&yes, "yes", false, "use --ip even though it is not the address this server reports")
	case "manual":
		fs.StringVar(&cert, "cert", "", "the certificate chain, a PEM file (the server's certificate first)")
		fs.StringVar(&key, "key", "", "its private key, a PEM file without a passphrase")
	default:
		fmt.Fprintf(o.errOut, "bobres ssl: unknown command %q\n%s\n", sub, sslUsage)
		return 2
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(o.errOut, "bobres ssl %s: unexpected argument %q\n", sub, fs.Arg(0))
		return 2
	}
	env, ok := o.requireInstall(*dir)
	if !ok {
		return 2
	}
	if msg := predatesHTTPS(*dir, env); msg != "" {
		fmt.Fprintf(o.errOut, "error: %s\n", msg)
		return 1
	}
	ctx := context.Background()
	switch sub {
	case "auto":
		return o.sslExec(ctx, *dir, "auto")
	case "ip":
		return o.sslIP(ctx, *dir, ip, yes)
	case "manual":
		return o.sslManual(ctx, *dir, cert, key)
	}
	code := o.sslExec(ctx, *dir, "status")
	o.sslHostView(ctx, *dir, env)
	return code
}

// sslExec runs core's `ssl` command; its own messages go to the user.
func (o *ops) sslExec(ctx context.Context, dir string, args ...string) int {
	return o.sslRun(ctx, dir, nil, args...)
}

// sslRun is sslExec with stdin (the certificate and key, for manual): when in
// is nil, the user's stdin is attached as for any other exec.
func (o *ops) sslRun(ctx context.Context, dir string, in io.Reader, args ...string) int {
	cargs := composeArgs(append([]string{"exec", "-T", "core", "/app", "ssl"}, args...)...)
	var err error
	if in != nil {
		err = o.pipe(ctx, dir, in, o.out, cargs...)
	} else {
		err = o.stream(ctx, dir, cargs...)
	}
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if !errors.As(err, &ee) { // docker itself failed; core's refusals already said why
		fmt.Fprintf(o.errOut, "error: docker compose exec failed: %v\n", err)
	}
	return 1
}

// sslIP asks for ip mode. The address is the detected one unless --ip names
// another; an address this server does not report as its own needs a yes,
// because Let's Encrypt validates the certificate by reaching that address:
// a wrong one leaves the store without HTTPS on it for nothing.
func (o *ops) sslIP(ctx context.Context, dir, ip string, yes bool) int {
	detect := o.detectIP
	if detect == nil {
		detect = publicIP
	}
	detected := detect()
	if ip == "" {
		if detected == "" {
			fmt.Fprintln(o.errOut, "error: this server's public IP address could not be detected (is there internet access?): give it with --ip ADDR")
			return 1
		}
		ip = detected
	}
	canon, err := tlsconf.PublicIP(ip)
	if err != nil {
		fmt.Fprintf(o.errOut, "error: %s: %v. Let's Encrypt issues IP certificates only for public addresses.\n", ip, err)
		return 2
	}
	if detected != "" && canon != detected && !yes {
		if !o.confirm(fmt.Sprintf("%s is not the address this server reports (%s). Let's Encrypt issues the certificate only if %s leads here.\n"+
			"Use %s anyway? [y/N]: ", canon, detected, canon, canon)) {
			fmt.Fprintln(o.out, "Cancelled. Nothing was changed.")
			return 1
		}
		yes = true
	}
	args := []string{"ip", canon}
	if detected != "" {
		args = append(args, "--detected", detected)
	}
	if yes {
		args = append(args, "--yes")
	}
	return o.sslExec(ctx, dir, args...)
}

// sslManual reads the certificate chain and the key on this host and gives
// them to core on stdin (never on a command line). Core checks them; the
// files stay where they are.
func (o *ops) sslManual(ctx context.Context, dir, certFile, keyFile string) int {
	if certFile == "" || keyFile == "" {
		fmt.Fprintf(o.errOut, "error: bobres ssl manual needs --cert FILE and --key FILE\n%s\n", sslUsage)
		return 2
	}
	var bundle []byte
	for _, f := range [...]struct{ what, path string }{{"certificate", certFile}, {"key", keyFile}} {
		b, err := readPEMFile(f.path)
		if err != nil {
			fmt.Fprintf(o.errOut, "error: the %s file: %v\n", f.what, err)
			return 1
		}
		bundle = append(bundle, bytes.TrimSpace(b)...)
		bundle = append(bundle, '\n')
	}
	return o.sslRun(ctx, dir, bytes.NewReader(bundle), "manual")
}

// readPEMFile reads a certificate or key file, at most tlsconf.MaxPEM bytes.
// It follows links (Let's Encrypt's live/ files are links) but wants a
// regular file at the end. Its text never appears in a message.
func readPEMFile(path string) ([]byte, error) {
	f, err := os.Open(path) //nolint:gosec // the operator names the file
	if err != nil {
		return nil, err
	}
	defer f.Close() //nolint:errcheck // read-only
	if fi, err := f.Stat(); err != nil {
		return nil, err
	} else if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	b, err := io.ReadAll(io.LimitReader(f, tlsconf.MaxPEM+1))
	if err != nil {
		return nil, err
	}
	if len(b) > tlsconf.MaxPEM {
		return nil, fmt.Errorf("%s is larger than %d KiB: that is not a certificate or a key", path, tlsconf.MaxPEM>>10)
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return nil, fmt.Errorf("%s is empty", path)
	}
	return b, nil
}

// served is an address the store is served on.
type served struct {
	Host string
	// Role is domain, ip or zarinpal.
	Role string
}

// servedAddresses are the addresses Caddy serves: core's own list when core
// answers (it knows ip mode's address), else the domain and the Zarinpal
// domain from .env. Core is not asked on an install without the tls-agent:
// its image has no `ssl` command.
func (o *ops) servedAddresses(ctx context.Context, dir string, env map[string]string) []served {
	if !tlsAgentOn(dir, env) {
		return envAddresses(env)
	}
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if out, err := o.run(cctx, dir, composeArgs("exec", "-T", "core", "/app", "ssl", "status", "--json")...); err == nil {
		// Compose may print a warning before the JSON.
		if i, j := bytes.IndexByte(out, '{'), bytes.LastIndexByte(out, '}'); i >= 0 && j > i {
			var v struct {
				Status struct {
					Addresses []struct {
						Address string `json:"address"`
						Role    string `json:"role"`
					} `json:"addresses"`
				} `json:"status"`
			}
			if json.Unmarshal(out[i:j+1], &v) == nil && len(v.Status.Addresses) > 0 {
				var list []served
				for _, a := range v.Status.Addresses {
					list = append(list, served{a.Address, a.Role})
				}
				return list
			}
		}
	}
	return envAddresses(env)
}

// envAddresses are the domain and the Zarinpal domain of an install.
func envAddresses(env map[string]string) []served {
	var list []served
	if d := strings.TrimSpace(env["BOBRES_DOMAIN"]); d != "" {
		list = append(list, served{d, tlsconf.RoleDomain})
	}
	if z := strings.TrimSpace(env["BOBRES_ZARINPAL_HOST"]); z != "" {
		list = append(list, served{z, tlsconf.RoleZarinpal})
	}
	return list
}

// probeLocal reads the certificate this host's own port 443 serves for
// address: what a visitor who reaches this server gets.
func (o *ops) probeLocal(ctx context.Context, address string) tlsprobe.Result {
	if o.probeTLS != nil {
		return o.probeTLS(ctx, address)
	}
	return tlsprobe.Probe(ctx, "127.0.0.1:443", address, tlsprobe.Options{})
}

// sslHostView prints, under core's status, what port 443 of this host serves
// for each served address: core looks from inside the Docker network, this
// is what the outside sees.
func (o *ops) sslHostView(ctx context.Context, dir string, env map[string]string) {
	addrs := o.servedAddresses(ctx, dir, env)
	if len(addrs) == 0 {
		return
	}
	fmt.Fprintln(o.out, "\nWhat this server answers on 127.0.0.1:443 (as a visitor would see it):")
	now := time.Now()
	if o.now != nil {
		now = o.now()
	}
	for _, a := range addrs {
		st, detail := tlsVerdict(o.probeLocal(ctx, a.Host), a.Host, now)
		fmt.Fprintf(o.out, "  %-30s %-5s %s\n", a.Host, st, detail)
	}
}

// tlsVerdict judges what an address serves, for doctor and `ssl status`:
// Pass for a publicly trusted certificate that is not about to expire; Warn
// for one that is expiring (renewal is failing) or not publicly trusted
// (self-signed, or from Caddy's own CA); Fail for no handshake, a
// certificate for other names, or an expired one.
func tlsVerdict(r tlsprobe.Result, address string, now time.Time) (Status, string) {
	if !r.Served {
		reason := r.Error
		if reason == "" {
			reason = "no answer"
		}
		return Fail, "no certificate is served for " + address + ": " + reason
	}
	left := r.Info.NotAfter.Sub(now)
	until := fmt.Sprintf("by %s, until %s (%s)", orDash(r.Info.Issuer), r.Info.NotAfter.Format(time.DateOnly), leftText(left))
	switch {
	case !r.NameOK:
		return Fail, "the certificate does not cover " + address + " (it is for " + orDash(r.Info.Subject) + "), " + until
	case left <= 0:
		return Fail, "the certificate has expired, " + until
	case tlsconf.ExpiringSoon(r.Info.NotBefore, r.Info.NotAfter, now):
		return Warn, "the certificate is about to expire and was not renewed: " + until + "; see: bobres logs caddy"
	case r.Trusted != nil && !*r.Trusted:
		why := ""
		if r.Error != "" {
			why = ": " + r.Error
		}
		return Warn, "not publicly trusted (self-signed, or from Caddy's own CA), " + until + why
	case r.Trusted == nil:
		return Pass, until + ", trust not checked"
	}
	return Pass, until + ", publicly trusted"
}

// leftText says how long a certificate has left.
func leftText(d time.Duration) string {
	switch {
	case d <= 0:
		return "expired"
	case d < 24*time.Hour:
		return fmt.Sprintf("%d hours left", int(d.Hours()))
	case d < 48*time.Hour:
		return "1 day left"
	}
	return fmt.Sprintf("%d days left", int(d.Hours()/24))
}
