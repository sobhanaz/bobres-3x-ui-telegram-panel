// Command hosted is the owner's tool for hosted stores (docs/18-hosted-and-affiliate-plans.md, section 1): it installs a customer's BOBRES
// store on a fresh server over SSH, with the normal installer and no question asked, and prints the registration code to paste into the
// dashboard's Hosted stores page (the register, and the store's health read from its /metrics).
//
//	go run ./tools/hosted install --host root@203.0.113.7 --domain shop.example.com --customer "Acme VPN" \
//	    --admin-id 123456789 --bot-token-file ~/hosted/acme.token [--channel stable|beta] [--port 22] [--identity ~/.ssh/id_ed25519] \
//	    [--license-server https://license.example.com --license-key-file ~/hosted/acme.key] [--out ~/hosted]
//	go run ./tools/hosted check --host root@203.0.113.7 [--port 22] [--identity FILE]
//
// It runs from the owner's computer with the system's ssh (key authentication only: BatchMode, so it never asks for a password) as root, or
// as a user with password-less sudo. Every secret goes to the server in the script ssh reads on its standard input, never on a command line
// (where ps and the shell history keep it). What it does on the server:
//
//  1. checks the server is fresh (no /opt/bobres/.env; --rerun allows a re-run, which keeps every secret as the installer does);
//  2. downloads install/install.sh and runs it with --yes, the domain, the owner's Telegram id and the bot token from the file;
//  3. sets the update channel, hardens the server (keeping the SSH port open), and activates the license when a key file is given;
//  4. makes the token of the store's /metrics (bobres admin metrics-token generate).
//
// Then it prints the store's backup key ONCE, for the customer to keep (it is not written anywhere here: the customer keeps
// BOBRES_BACKUP_KEY, the owner does not), and the registration code, which it also saves in --out as hosted-<domain>.txt (mode 600). The
// code carries the domain, the customer, the version, the channel, the license id and the metrics token, never the bot token or the backup
// key. The bot token never leaves the customer's server after the install, and the register never holds it.
package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/hosted"
)

// DefaultInstaller is the bootstrap installer the customers' stores are installed with.
const DefaultInstaller = "https://raw.githubusercontent.com/sobhanaz/bobres-3x-ui-telegram-panel/main/install/install.sh"

const usage = `usage: hosted install --host USER@HOST --domain DOMAIN --customer NAME --admin-id ID --bot-token-file FILE
                     [--channel stable|beta] [--port 22] [--identity FILE] [--license-server URL --license-key-file FILE]
                     [--license-id lic_...] [--out DIR] [--installer URL] [--rerun]
       hosted check --host USER@HOST [--port 22] [--identity FILE]`

var (
	botTokenRe   = regexp.MustCompile(`^\d{5,15}:[A-Za-z0-9_-]{30,64}$`)
	adminIDsRe   = regexp.MustCompile(`^[1-9][0-9]{0,15}(,[1-9][0-9]{0,15}){0,9}$`)
	hostRe       = regexp.MustCompile(`^([a-z_][a-z0-9_-]{0,31}@)?[A-Za-z0-9.:\[\]-]{1,255}$`)
	licenseKeyRe = regexp.MustCompile(`^BOBRES(-[A-Z2-7]{4}){8}$`)
	versionRe    = regexp.MustCompile(`^[0-9A-Za-z.+_-]{1,64}$`)
)

// Runner runs a shell script on the server as root (the script is ssh's standard input) and returns what it printed on stdout. What it
// prints on stderr goes to the operator's terminal.
type Runner interface {
	Run(ctx context.Context, script string) (string, error)
}

// sshRunner is the real one: the system's ssh with key authentication.
type sshRunner struct {
	host, port, identity string
	stderr               io.Writer
}

func (r sshRunner) args() []string {
	a := []string{"-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=accept-new", "-o", "ServerAliveInterval=30", "-p", r.port}
	if r.identity != "" {
		a = append(a, "-i", r.identity, "-o", "IdentitiesOnly=yes")
	}
	shell := "bash -s"
	if user, _, ok := strings.Cut(r.host, "@"); ok && user != "root" {
		shell = "sudo -n bash -s"
	}
	return append(a, "--", r.host, shell)
}

func (r sshRunner) Run(ctx context.Context, script string) (string, error) {
	cmd := exec.CommandContext(ctx, "ssh", r.args()...) //nolint:gosec // the operator's own host and flags
	cmd.Stdin = strings.NewReader(script)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = r.stderr
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("ssh %s: %w", r.host, err)
	}
	return out.String(), nil
}

// app is the tool with its outside world, so a test runs it against a fake server.
type app struct {
	out, errOut io.Writer
	newRunner   func(host, port, identity string) Runner
	readFile    func(string) ([]byte, error)
	stat        func(string) (os.FileInfo, error)
	writeFile   func(string, []byte, os.FileMode) error
	now         func() time.Time
}

func main() {
	a := &app{out: os.Stdout, errOut: os.Stderr, readFile: os.ReadFile, stat: os.Stat, writeFile: os.WriteFile, now: time.Now}
	a.newRunner = func(host, port, identity string) Runner {
		return sshRunner{host: host, port: port, identity: identity, stderr: os.Stderr}
	}
	os.Exit(a.run(os.Args[1:]))
}

func (a *app) run(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(a.errOut, usage)
		return 2
	}
	switch args[0] {
	case "install":
		return a.install(args[1:])
	case "check":
		return a.check(args[1:])
	case "-h", "--help", "help":
		fmt.Fprintln(a.out, usage)
		return 0
	}
	fmt.Fprintln(a.errOut, usage)
	return 2
}

// conn is the flags every command takes to reach the server.
type conn struct {
	host, identity *string
	port           *int
}

func connFlags(fs *flag.FlagSet) conn {
	return conn{
		host:     fs.String("host", "", "the server, as USER@HOST (root, or a user with password-less sudo); key authentication only"),
		port:     fs.Int("port", 22, "the server's SSH port (the firewall keeps it open)"),
		identity: fs.String("identity", "", "the SSH key to use (default: ssh's own)"),
	}
}

func (c conn) check() error {
	switch {
	case !hostRe.MatchString(*c.host):
		return errors.New("--host is USER@HOST (for example root@203.0.113.7)")
	case strings.HasPrefix(*c.host, "-"):
		return errors.New("--host may not start with -")
	case *c.port < 1 || *c.port > 65535:
		return errors.New("--port is 1 to 65535")
	}
	return nil
}

// probe is the script that says what the server is: who runs it, its system, and whether BOBRES is installed.
const probe = `set -eu
echo "uid=$(id -u)"
if [ -r /etc/os-release ]; then . /etc/os-release; echo "os=${ID:-unknown} ${VERSION_ID:-}"; fi
echo "arch=$(uname -m)"
if [ -e /opt/bobres/.env ]; then echo "bobres=installed"; else echo "bobres=none"; fi
`

func parseLines(out string) map[string]string {
	m := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		if k, v, ok := strings.Cut(strings.TrimSpace(sc.Text()), "="); ok {
			m[k] = v
		}
	}
	return m
}

func (a *app) check(args []string) int {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(a.errOut)
	c := connFlags(fs)
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		fmt.Fprintln(a.errOut, usage)
		return 2
	}
	if err := c.check(); err != nil {
		fmt.Fprintln(a.errOut, "error:", err)
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	out, err := a.newRunner(*c.host, strconv.Itoa(*c.port), *c.identity).Run(ctx, probe)
	if err != nil {
		fmt.Fprintln(a.errOut, "error: the server could not be reached:", err)
		return 1
	}
	m := parseLines(out)
	if m["uid"] != "0" {
		fmt.Fprintln(a.errOut, "error: the script does not run as root on the server (use root, or a user with password-less sudo)")
		return 1
	}
	fmt.Fprintf(a.out, "reachable as root; system %s (%s); BOBRES %s\n", m["os"], m["arch"], m["bobres"])
	return 0
}

// installOpts are the values of one install, checked.
type installOpts struct {
	host, port, identity      string
	domain, customer, adminID string
	channel, installer        string
	botToken                  string
	licenseServer, licenseKey string
	licenseID                 string
	outDir                    string
	rerun                     bool
}

// readSecretFile reads a one-line secret from a file the operator made, and warns when others may read it.
func (a *app) readSecretFile(flagName, path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("%s is required", flagName)
	}
	fi, err := a.stat(path)
	if err != nil {
		return "", fmt.Errorf("%s: %w", flagName, err)
	}
	if !fi.Mode().IsRegular() {
		return "", fmt.Errorf("%s: not a regular file", flagName)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		fmt.Fprintf(a.errOut, "warning: %s %s can be read by other users of this computer: chmod 600 it\n", flagName, path)
	}
	raw, err := a.readFile(path)
	if err != nil {
		return "", fmt.Errorf("%s: %w", flagName, err)
	}
	return strings.TrimSpace(string(raw)), nil
}

func (a *app) installOptions(args []string) (*installOpts, int) {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	fs.SetOutput(a.errOut)
	c := connFlags(fs)
	domain := fs.String("domain", "", "the store's domain (its DNS must point at the server)")
	customer := fs.String("customer", "", "the customer's name, for the register")
	adminID := fs.String("admin-id", "", "the customer's Telegram id (the store's owner); several: separated by commas, the main owner first")
	tokenFile := fs.String("bot-token-file", "", "a file holding the customer's bot token (from @BotFather); never put the token on a command line")
	channel := fs.String("channel", "stable", "the store's update channel: stable, or beta for pre-releases too")
	licenseServer := fs.String("license-server", "", "the license server's address (https), to activate the store's license")
	licenseKeyFile := fs.String("license-key-file", "", "a file holding the store's license key (the Hosted license made on the Hosted stores page)")
	licenseID := fs.String("license-id", "", "the license server's id of that license (lic_...), for the register")
	outDir := fs.String("out", ".", "where the registration code is saved (hosted-<domain>.txt, mode 600)")
	installer := fs.String("installer", DefaultInstaller, "the bootstrap installer's address (https)")
	rerun := fs.Bool("rerun", false, "run the installer again on a server that has BOBRES (it keeps every secret)")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		fmt.Fprintln(a.errOut, usage)
		return nil, 2
	}
	fail := func(format string, v ...any) (*installOpts, int) {
		fmt.Fprintf(a.errOut, "error: "+format+"\n", v...)
		return nil, 2
	}
	if err := c.check(); err != nil {
		return fail("%v", err)
	}
	o := &installOpts{host: *c.host, port: strconv.Itoa(*c.port), identity: *c.identity, domain: strings.ToLower(strings.TrimSpace(*domain)),
		customer: strings.TrimSpace(*customer), adminID: strings.ReplaceAll(*adminID, " ", ""), channel: *channel, installer: *installer,
		licenseServer: strings.TrimRight(strings.TrimSpace(*licenseServer), "/"), licenseID: strings.TrimSpace(*licenseID), outDir: *outDir,
		rerun: *rerun}
	reg := hosted.Registration{Customer: o.customer, Domain: o.domain, Channel: o.channel, LicenseID: o.licenseID}
	if err := reg.Check(); err != nil {
		return fail("%v", err)
	}
	if !adminIDsRe.MatchString(o.adminID) {
		return fail("--admin-id is the customer's numeric Telegram id (several: separated by commas)")
	}
	if !strings.HasPrefix(o.installer, "https://") {
		return fail("--installer must be an https:// address")
	}
	tok, err := a.readSecretFile("--bot-token-file", *tokenFile)
	if err != nil {
		return fail("%v", err)
	}
	if !botTokenRe.MatchString(tok) {
		return fail("--bot-token-file does not hold a bot token (digits, a colon, then 30 to 64 letters, digits, _ or -)")
	}
	o.botToken = tok
	if *licenseKeyFile != "" {
		if !strings.HasPrefix(o.licenseServer, "https://") || strings.ContainsAny(o.licenseServer, " '\"\\$`") {
			return fail("--license-key-file needs --license-server, an https:// address")
		}
		key, err := a.readSecretFile("--license-key-file", *licenseKeyFile)
		if err != nil {
			return fail("%v", err)
		}
		key = strings.ToUpper(strings.ReplaceAll(key, " ", ""))
		if !licenseKeyRe.MatchString(key) {
			return fail("--license-key-file does not hold a license key (BOBRES- and 8 groups of 4 letters)")
		}
		o.licenseKey = key
	} else if o.licenseServer != "" {
		return fail("--license-server is used with --license-key-file")
	}
	return o, 0
}

// sq quotes a value for the server's shell (every value is checked before; this is the second line of defence).
func sq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'" }

// installScript is the script that installs the store. The bot token is in it, so it only ever goes to ssh's standard input.
func installScript(o *installOpts) string {
	var b strings.Builder
	b.WriteString("set -euo pipefail\n")
	b.WriteString("export BOBRES_TELEGRAM_BOT_TOKEN=" + sq(o.botToken) + "\n")
	b.WriteString("export BOBRES_UPDATE_CHANNEL=" + sq(o.channel) + "\n")
	b.WriteString("export BOBRES_PLAIN=1 DEBIAN_FRONTEND=noninteractive\n")
	b.WriteString(`command -v curl >/dev/null 2>&1 || { apt-get update -qq && apt-get install -y -qq curl ca-certificates; } >&2` + "\n")
	b.WriteString(`f=$(mktemp); trap 'rm -f "$f"' EXIT` + "\n")
	b.WriteString("curl -fsSL --proto '=https' " + sq(o.installer) + ` -o "$f"` + "\n")
	b.WriteString(`bash "$f" --plain --no-banner -- --yes --domain ` + sq(o.domain) + " --admin-id " + sq(o.adminID) + " >&2\n")
	return b.String()
}

// afterScript finishes the store (channel, hardening, license, metrics token) and prints, as KEY=value lines, what the tool needs back.
func afterScript(o *installOpts) string {
	var b strings.Builder
	b.WriteString("set -euo pipefail\n")
	b.WriteString("bobres config set BOBRES_UPDATE_CHANNEL " + sq(o.channel) + " >&2\n")
	b.WriteString("bobres harden --yes --ssh-port " + sq(o.port) + " >&2 || echo 'warning: a hardening step did not finish: run bobres harden on the server' >&2\n")
	if o.licenseKey != "" {
		b.WriteString("bobres config set BOBRES_LICENSE_SERVER " + sq(o.licenseServer) + " >&2\n")
		b.WriteString("printf '%s\\n' " + sq(o.licenseKey) + " | bobres license activate >&2\n")
	}
	b.WriteString(`tok=$(bobres admin metrics-token generate | tail -n 1)` + "\n")
	b.WriteString(`echo "metrics_token=$tok"` + "\n")
	b.WriteString(`echo "version=$(sed -n 's/^BOBRES_VERSION=//p' /opt/bobres/.env | tr -d '"' | head -n 1)"` + "\n")
	b.WriteString(`echo "backup_key=$(sed -n 's/^BOBRES_BACKUP_KEY=//p' /opt/bobres/.env | tr -d '"' | head -n 1)"` + "\n")
	return b.String()
}

func (a *app) install(args []string) int {
	o, code := a.installOptions(args)
	if o == nil {
		return code
	}
	r := a.newRunner(o.host, o.port, o.identity)
	ctx := context.Background()
	say := func(format string, v ...any) { fmt.Fprintf(a.out, "==> "+format+"\n", v...) }

	say("Checking %s", o.host)
	pctx, cancel := context.WithTimeout(ctx, time.Minute)
	out, err := r.Run(pctx, probe)
	cancel()
	if err != nil {
		fmt.Fprintln(a.errOut, "error: the server could not be reached (key authentication, root or password-less sudo):", err)
		return 1
	}
	m := parseLines(out)
	if m["uid"] != "0" {
		fmt.Fprintln(a.errOut, "error: the script does not run as root on the server (use root, or a user with password-less sudo)")
		return 1
	}
	if m["bobres"] == "installed" && !o.rerun {
		fmt.Fprintln(a.errOut, "error: BOBRES is installed on this server already; a hosted store goes on a fresh server (--rerun runs the installer again)")
		return 1
	}
	say("Installing BOBRES for %s on %s (%s): this takes a few minutes", o.customer, o.domain, m["os"])
	ictx, cancel := context.WithTimeout(ctx, 45*time.Minute)
	_, err = r.Run(ictx, installScript(o))
	cancel()
	if err != nil {
		fmt.Fprintln(a.errOut, "error: the installer failed (its log on the server: /var/log/bobres-install.log):", err)
		return 1
	}
	say("Finishing: update channel, hardening%s, metrics token", map[bool]string{true: ", license", false: ""}[o.licenseKey != ""])
	actx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	out, err = r.Run(actx, afterScript(o))
	cancel()
	if err != nil {
		fmt.Fprintln(a.errOut, "error: the store is installed, but finishing it failed:", err)
		return 1
	}
	got := parseLines(out)
	token, version, backupKey := got["metrics_token"], got["version"], got["backup_key"]
	if !hosted.TokenRe.MatchString(token) {
		fmt.Fprintln(a.errOut, "error: the store did not give a metrics token; run  sudo bobres admin metrics-token generate  on it and add it on the Hosted stores page")
		return 1
	}
	if !versionRe.MatchString(version) {
		version = ""
	}
	reg := hosted.Registration{Customer: o.customer, Domain: o.domain, Version: version, Channel: o.channel, LicenseID: o.licenseID,
		MetricsToken: token, Created: a.now().Unix()}
	codeLine, err := reg.Encode()
	if err != nil {
		fmt.Fprintln(a.errOut, "error:", err)
		return 1
	}
	path := filepath.Join(o.outDir, "hosted-"+o.domain+".txt")
	if err := a.writeFile(path, []byte(codeLine+"\n"), 0o600); err != nil {
		fmt.Fprintln(a.errOut, "warning: the registration code could not be saved:", err)
		path = ""
	}

	fmt.Fprintf(a.out, "\nThe store is installed: https://%s (version %s, channel %s).\n", o.domain, orUnknown(version), o.channel)
	if backupKey != "" {
		fmt.Fprintln(a.out, "\nTHE CUSTOMER'S BACKUP KEY, shown once. Give it to the customer and tell them to save it somewhere safe:")
		fmt.Fprintln(a.out, "without it their backups cannot be restored. It is not kept here, and you should not keep it either.")
		fmt.Fprintf(a.out, "\n    %s\n\n", backupKey)
	} else {
		fmt.Fprintln(a.out, "\nThe backup key could not be read: the customer sees it with  sudo bobres menu  (Backup key) on their server.")
	}
	fmt.Fprintln(a.out, "Register the store: on the dashboard's Hosted stores page, Add, paste this code:")
	fmt.Fprintf(a.out, "\n    %s\n\n", codeLine)
	if path != "" {
		fmt.Fprintf(a.out, "(saved in %s, mode 600: it holds the store's metrics token; delete it once the store is registered)\n", path)
	}
	fmt.Fprintln(a.out, "Next: the customer sends /start to their bot, then signs in with /dashboard.")
	return 0
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}
