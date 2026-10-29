// Command bobres is the BOBRES operator CLI (install, update, doctor, ...).
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tlsconf"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/version"
)

// stdin feeds interactive prompts (replaced in tests).
var stdin io.Reader = os.Stdin

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

// stdout is where the menu decides whether a person is watching (replaced in tests).
var stdout io.Writer = os.Stdout

func run(args []string, out, errOut io.Writer) int {
	// --plain and --no-banner are the same for every command.
	args, uiOpts = stripUIFlags(args)
	if len(args) == 0 && interactive(stdin, stdout) {
		return menuCmd(nil, stdin, out, errOut)
	}
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		if line := compactBanner(out); line != "" {
			fmt.Fprintln(out, line)
			fmt.Fprintln(out)
		}
		usage(out)
		return 0
	}
	switch cmd := args[0]; cmd {
	case "version", "--version":
		if len(args) > 1 && args[1] == "--images" {
			return versionImages(out, errOut)
		}
		if line := compactBanner(out); line != "" {
			fmt.Fprintln(out, line)
			return 0
		}
		fmt.Fprintln(out, "bobres", version.Version)
		return 0
	case "about":
		return aboutCmd(args[1:], out, errOut)
	case "menu":
		return menuCmd(args[1:], stdin, out, errOut)
	case "doctor":
		return doctorCmd(args[1:], out, errOut)
	case "check":
		return newOps(stdin, out, errOut).checkCmd(args[1:])
	case "install":
		return installCmd(args[1:], stdin, out, errOut)
	case "status":
		return newOps(stdin, out, errOut).statusCmd(args[1:])
	case "logs":
		return newOps(stdin, out, errOut).logsCmd(args[1:])
	case "start":
		return newOps(stdin, out, errOut).startCmd(args[1:])
	case "stop":
		return newOps(stdin, out, errOut).stopCmd(args[1:])
	case "restart":
		return newOps(stdin, out, errOut).restartCmd(args[1:])
	case "uninstall":
		return newOps(stdin, out, errOut).uninstallCmd(args[1:])
	case "admin":
		return newOps(stdin, out, errOut).adminCmd(args[1:])
	case "license":
		return newOps(stdin, out, errOut).licenseCmd(args[1:])
	case "backup":
		return newOps(stdin, out, errOut).backupCmd(args[1:])
	case "restore":
		return newOps(stdin, out, errOut).restoreCmd(args[1:])
	case "update":
		// bobres-updater.service runs this one for the dashboard's request; it is not in the help.
		if len(args) > 1 && args[1] == "--from-request" {
			return helperRequestCmd(args[2:], stdin, out, errOut)
		}
		return newUpdater(newOps(stdin, out, errOut)).updateCmd(args[1:])
	case "rollback":
		return newUpdater(newOps(stdin, out, errOut)).rollbackCmd(args[1:])
	case "support-bundle":
		return newBundler(newOps(stdin, out, errOut)).supportBundleCmd(args[1:])
	case "harden":
		return hardenCmd(args[1:], stdin, out, errOut)
	case "config":
		return newOps(stdin, out, errOut).configCmd(args[1:])
	case "ssl":
		return newOps(stdin, out, errOut).sslCmd(args[1:])
	case "secrets":
		return newRotator(newOps(stdin, out, errOut)).secretsCmd(args[1:])
	case "rotate-key":
		return newRotator(newOps(stdin, out, errOut)).rotateKeyCmd(args[1:])
	case "monitor":
		return monitorCmd(args[1:], stdin, out, errOut)
	default:
		fmt.Fprintf(errOut, "bobres: unknown command %q\n\n", cmd)
		usage(errOut)
		return 2
	}
}

// compactBanner is the one-line banner (the version and the copyright) for a
// person at a terminal; "" when output goes anywhere else or the banner is off,
// where the version and the help text keep their plain form.
func compactBanner(out io.Writer) string {
	t := detectUI(out, os.Getenv)
	if !t.tty || bannerOff(os.Getenv) {
		return ""
	}
	return compactLine(t, currentBuild())
}

func usage(w io.Writer) {
	fmt.Fprint(w, paintUsage(detectUI(w, os.Getenv), usageText))
}

const usageText = `bobres - install and operate your BOBRES store

Usage: bobres [command] [flags]

Run bobres without a command in a terminal to open the management menu.

Available now:
  menu       The management menu (status, logs, start/stop, change settings...)
  install    Set up BOBRES on this server (asks only what has no safe default; see install -h)
  status     Show the services, version and disk (exit 1 if something is down); --json for scripts
  logs       Show service logs: bobres logs [-f] [--tail N] [service...]
  start      Start the store and wait until it is healthy
  stop       Stop the store (nothing is deleted)
  restart    Restart the store and wait until it is healthy
  uninstall  Remove the containers (data kept); --purge removes everything BOBRES put on this server, as on
             day one (asks first; --dry-run shows the plan; --verify scans for leftovers)
  admin link Print a one-time dashboard login link (for when the bot is down)
  admin password  Reset the owner's dashboard password and show it once (no Telegram needed)
  license    Show the license (license status), install one (license import < token.txt)
             or activate a license key with the vendor's license server (license activate)
  backup     Back up the database now (encrypted; kept in <dir>/backups and sent to the owner)
  restore    Replace the data with a backup's: bobres restore <file> [--key-file F] [--yes]
  update     Update to the latest release (backup first, automatic rollback); --check only looks
  rollback   Undo the last update (previous version, and the database from before it)
  config     Show or change a setting: config list | get <key> | set <key> <value>
  ssl        The HTTPS certificate: ssl status | auto | ip | manual --cert FILE --key FILE
  secrets    Rotate a secret: secrets rotate bot-token|db-passwords|service-tokens|redis-password
  rotate-key Replace the master keys and seal everything they protect again (backup first;
             --dry-run changes nothing; the old keys come back if anything fails)
  harden     Firewall (ufw), fail2ban, automatic security updates and swap, each asked first
  monitor    Host monitoring for the dashboard: monitor status | enable | disable | snapshot
  doctor     Check this server (RAM, disk, ports, Docker, DNS, HTTPS certificate, hardening)
  check      Check the whole install (settings, services, disk, backups, bot token, release
             signature, license, servers): PASS/WARN/FAIL lines, exit 1 on a FAIL; --json
  support-bundle
             Write a redacted tar.gz for support into <dir> (versions, status, logs; no secrets)
  version    Print the version (--images: the image digests this release pins)
  about      The version, license, copyright, vendor license key fingerprint and links

Every command takes --plain (no colors, no redrawing; the same as BOBRES_PLAIN=1, NO_COLOR or
a pipe) and --no-banner (BOBRES_NO_BANNER=1).

Commands that work on an install take --dir (default: $BOBRES_DIR, else /opt/bobres).
`

func doctorCmd(args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(errOut)
	dir := fs.String("dir", defaultDir(), dirUsage)
	domain := fs.String("domain", "", "check that this domain points to this server (default: the install's BOBRES_DOMAIN)")
	offline := fs.Bool("offline", false, "skip network checks")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *domain == "" {
		*domain = domainOfInstall(*dir)
	}
	results := doctorResults(*dir, *domain, *offline)
	t := detectUI(out, os.Getenv)
	for _, r := range results {
		if t.fancy {
			fmt.Fprintf(out, "  %s %-18s %s\n", t.mark(statusMark(r.Status)), r.Name, r.Detail)
			continue
		}
		fmt.Fprintf(out, "%-5s %-18s %s\n", r.Status, r.Name, r.Detail)
	}
	pass, warn, fail, failed := Summarize(results)
	sum := fmt.Sprintf("%d passed, %d warnings, %d failed", pass, warn, fail)
	switch {
	case failed:
		sum = t.paint(roleRed, sum)
	case warn > 0:
		sum = t.paint(roleBrown, sum)
	default:
		sum = t.paint(roleTeal, sum)
	}
	fmt.Fprintf(out, "\n%s\n", sum)
	if failed {
		return 1
	}
	return 0
}

// domainOfInstall is BOBRES_DOMAIN of the install in dir: "" when there is
// none, or when this user cannot read its .env (it is root's).
func domainOfInstall(dir string) string {
	if env, ok, err := readEnv(filepath.Join(dir, ".env")); err == nil && ok {
		return env["BOBRES_DOMAIN"]
	}
	return ""
}

// ipServices answer with the caller's public address as plain text.
var ipServices = []string{"https://api.ipify.org", "https://ifconfig.me/ip"}

// maxIPAnswer bounds an address service's answer: an IPv6 address is 45
// characters at most.
const maxIPAnswer = 64

// publicIP asks a small set of services for this machine's public IP
// address. The answer can end up in a certificate request, so only a plain 200
// of at most maxIPAnswer bytes that reads as a public address counts (and no
// redirect is followed); anything else is no answer, and the next service is
// asked. "" when none answers.
func publicIP() string {
	hc := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, u := range ipServices {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		text, ok := fetchIPText(ctx, hc, u)
		cancel()
		if !ok {
			continue
		}
		if ip, err := tlsconf.PublicIP(text); err == nil {
			return ip
		}
	}
	return ""
}

// fetchIPText reads the answer of one address service: the body, trimmed,
// when the status is 200, the body is at most maxIPAnswer bytes and it is an IP
// address (net.ParseIP).
func fetchIPText(ctx context.Context, hc *http.Client, url string) (string, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", false
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close() //nolint:errcheck // read-only
	if resp.StatusCode != http.StatusOK {
		return "", false
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxIPAnswer+1))
	if err != nil || len(b) > maxIPAnswer {
		return "", false
	}
	text := strings.TrimSpace(string(b))
	if net.ParseIP(text) == nil {
		return "", false
	}
	return text, true
}
