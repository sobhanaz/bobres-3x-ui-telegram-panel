// Command bobres is the BOBRES operator CLI (install, update, doctor, ...).
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/version"
)

// notImplemented lists commands from docs/06-installer.md that land in later phases.
var notImplemented = map[string]string{
	"update": "phase 6", "rollback": "phase 6", "backup": "phase 7", "restore": "phase 7",
	"config": "phase 1", "secrets": "phase 7", "license": "phase 6",
	"support-bundle": "phase 7",
}

// stdin feeds interactive prompts (replaced in tests).
var stdin io.Reader = os.Stdin

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

// stdout is where the menu decides whether a person is watching (replaced in tests).
var stdout io.Writer = os.Stdout

func run(args []string, out, errOut io.Writer) int {
	if len(args) == 0 && interactive(stdin, stdout) {
		return menuCmd(nil, stdin, out, errOut)
	}
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		usage(out)
		return 0
	}
	switch cmd := args[0]; cmd {
	case "version", "--version":
		fmt.Fprintln(out, "bobres", version.Version)
		return 0
	case "menu":
		return menuCmd(args[1:], stdin, out, errOut)
	case "doctor":
		return doctorCmd(args[1:], out, errOut)
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
	default:
		if phase, ok := notImplemented[cmd]; ok {
			fmt.Fprintf(errOut, "bobres %s: not implemented yet (planned for %s)\n", cmd, phase)
			return 2
		}
		fmt.Fprintf(errOut, "bobres: unknown command %q\n\n", cmd)
		usage(errOut)
		return 2
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `bobres - install and operate your BOBRES store

Usage: bobres [command] [flags]

Run bobres without a command in a terminal to open the management menu.

Available now:
  menu       The management menu (status, logs, start/stop, change settings...)
  install    Set up BOBRES on this server (asks for what it needs; see install -h)
  status     Show the services, version and disk (exit 1 if something is down)
  logs       Show service logs: bobres logs [-f] [--tail N] [service...]
  start      Start the store and wait until it is healthy
  stop       Stop the store (nothing is deleted)
  restart    Restart the store and wait until it is healthy
  uninstall  Remove the containers (data kept unless --purge)
  admin link Print a one-time dashboard login link (for when the bot is down)
  doctor     Check this server (RAM, disk, ports, Docker, DNS)
  version    Print the version

Commands that work on an install take --dir (default: $BOBRES_DIR, else /opt/bobres).

Coming later:
  config                                       (phase 1)
  update rollback license                      (phase 6)
  backup restore secrets support-bundle        (phase 7)
`)
}

func doctorCmd(args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(errOut)
	domain := fs.String("domain", "", "check that this domain points to this server")
	offline := fs.Bool("offline", false, "skip network checks")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	results := runDoctor(*domain, *offline)
	for _, r := range results {
		fmt.Fprintf(out, "%-5s %-18s %s\n", r.Status, r.Name, r.Detail)
	}
	pass, warn, fail, failed := Summarize(results)
	fmt.Fprintf(out, "\n%d passed, %d warnings, %d failed\n", pass, warn, fail)
	if failed {
		return 1
	}
	return 0
}

// publicIP asks a small set of services for this machine's public IPv4.
func publicIP() string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, u := range []string{"https://api.ipify.org", "https://ifconfig.me/ip"} {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			continue
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			continue
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 64))
		_ = resp.Body.Close()
		if ip := strings.TrimSpace(string(b)); resp.StatusCode == http.StatusOK && len(ip) >= 7 && len(ip) <= 45 {
			return ip
		}
	}
	return ""
}
