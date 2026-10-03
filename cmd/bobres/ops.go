package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// composeProject is the Compose project name every bobres command uses, so
// status/logs/uninstall find what install started.
const composeProject = "bobres"

func composeArgs(extra ...string) []string {
	return append([]string{"compose", "--project-name", composeProject, "-f", "docker-compose.yml", "--env-file", ".env"}, extra...)
}

// ops holds the side effects of status/logs/uninstall, replaceable in tests.
type ops struct {
	in     io.Reader
	out    io.Writer
	errOut io.Writer
	// run captures a docker command's output.
	run func(ctx context.Context, dir string, args ...string) ([]byte, error)
	// stream runs a docker command with its output going straight to the user.
	stream func(ctx context.Context, dir string, args ...string) error
	freeGB func(path string) float64
}

func newOps(in io.Reader, out, errOut io.Writer) *ops {
	return &ops{
		in: in, out: out, errOut: errOut,
		run: func(ctx context.Context, dir string, args ...string) ([]byte, error) {
			cmd := exec.CommandContext(ctx, "docker", args...) //nolint:gosec // fixed program, arguments built here
			cmd.Dir = dir
			return cmd.CombinedOutput()
		},
		stream: func(ctx context.Context, dir string, args ...string) error {
			cmd := exec.CommandContext(ctx, "docker", args...) //nolint:gosec // fixed program, arguments built here
			cmd.Dir, cmd.Stdout, cmd.Stderr, cmd.Stdin = dir, out, errOut, os.Stdin
			return cmd.Run()
		},
		freeGB: freeDiskGB,
	}
}

// requireInstall checks that dir holds an install and returns its .env.
func (o *ops) requireInstall(dir string) (map[string]string, bool) {
	env, ok, err := readEnv(filepath.Join(dir, ".env"))
	if err != nil {
		fmt.Fprintf(o.errOut, "error: read %s: %v\n", filepath.Join(dir, ".env"), err)
		return nil, false
	}
	if !ok {
		fmt.Fprintf(o.errOut, "error: no install found in %s (run `bobres install`, or pass --dir)\n", dir)
		return nil, false
	}
	return env, true
}

// statusCmd shows the services, versions and disk; exit 1 when something is
// not healthy, so it doubles as a monitoring probe.
func (o *ops) statusCmd(args []string) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(o.errOut)
	dir := fs.String("dir", "/opt/bobres", "install directory")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	env, ok := o.requireInstall(*dir)
	if !ok {
		return 2
	}
	fmt.Fprintf(o.out, "Install:  %s\nVersion:  %s\nDomain:   %s\n", *dir, orDash(env["BOBRES_VERSION"]), orDash(env["BOBRES_DOMAIN"]))
	if gb := o.freeGB(*dir); gb >= 0 {
		fmt.Fprintf(o.out, "Disk:     %.1f GB free\n", gb)
	}
	out, err := o.run(context.Background(), *dir, composeArgs("ps", "--all", "--format", "json")...)
	if err != nil {
		fmt.Fprintf(o.errOut, "error: docker compose ps failed: %v\n%s\n", err, strings.TrimSpace(string(out)))
		return 1
	}
	rows := parseServices(out)
	want := []string{"caddy", "postgres", "redis", "core", "payments", "provisioner", "bot"}
	fmt.Fprintln(o.out, "\nSERVICE       STATE        HEALTH")
	problems := 0
	seen := map[string]bool{}
	for _, name := range want {
		r, found := rows[name]
		seen[name] = true
		if !found {
			fmt.Fprintf(o.out, "%-13s %-12s %s\n", name, "missing", "-")
			problems++
			continue
		}
		health := r.Health
		if health == "" {
			health = "-"
		}
		fmt.Fprintf(o.out, "%-13s %-12s %s\n", name, r.State, health)
		if r.State != "running" || (r.Health != "" && r.Health != "healthy") {
			problems++
		}
	}
	var extra []string
	for name := range rows {
		if !seen[name] {
			extra = append(extra, name)
		}
	}
	sort.Strings(extra)
	for _, name := range extra {
		r := rows[name]
		fmt.Fprintf(o.out, "%-13s %-12s %s\n", name, r.State, orDash(r.Health))
	}
	if problems > 0 {
		fmt.Fprintf(o.out, "\n%d service(s) need attention. See: bobres logs <service>\n", problems)
		return 1
	}
	fmt.Fprintln(o.out, "\nAll services are running.")
	return 0
}

type serviceRow struct {
	State  string
	Health string
}

// parseServices reads `docker compose ps --format json` (an array, or one
// object per line depending on the Compose version) into service -> row.
func parseServices(out []byte) map[string]serviceRow {
	type row struct {
		Service string `json:"Service"`
		State   string `json:"State"`
		Health  string `json:"Health"`
	}
	var list []row
	trimmed := bytes.TrimSpace(out)
	if bytes.HasPrefix(trimmed, []byte("[")) {
		_ = json.Unmarshal(trimmed, &list)
	} else {
		for _, line := range bytes.Split(trimmed, []byte("\n")) {
			var r row
			if json.Unmarshal(line, &r) == nil && r.Service != "" {
				list = append(list, r)
			}
		}
	}
	rows := make(map[string]serviceRow, len(list))
	for _, r := range list {
		rows[r.Service] = serviceRow{State: r.State, Health: r.Health}
	}
	return rows
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// logsCmd streams service logs.
func (o *ops) logsCmd(args []string) int {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	fs.SetOutput(o.errOut)
	dir := fs.String("dir", "/opt/bobres", "install directory")
	follow := fs.Bool("f", false, "follow")
	tail := fs.String("tail", "200", "number of lines to show from the end of each log")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if _, ok := o.requireInstall(*dir); !ok {
		return 2
	}
	cargs := composeArgs("logs", "--tail", *tail, "--no-color")
	if *follow {
		cargs = append(cargs, "-f")
	}
	cargs = append(cargs, fs.Args()...)
	if err := o.stream(context.Background(), *dir, cargs...); err != nil {
		fmt.Fprintf(o.errOut, "error: docker compose logs: %v\n", err)
		return 1
	}
	return 0
}

// uninstallCmd stops and removes the containers. Data (database, Redis,
// certificates) and the install directory stay unless --purge is given.
func (o *ops) uninstallCmd(args []string) int {
	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	fs.SetOutput(o.errOut)
	dir := fs.String("dir", "/opt/bobres", "install directory")
	purge := fs.Bool("purge", false, "also delete the database, Redis data, certificates and the install directory (irreversible)")
	yes := fs.Bool("yes", false, "do not ask for confirmation")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	env, ok := o.requireInstall(*dir)
	if !ok {
		return 2
	}
	if !*yes {
		if *purge {
			fmt.Fprintf(o.out, "This deletes ALL data of %s (users, orders, ledger, panel token) and %s.\nType the domain (%s) to confirm: ",
				orDash(env["BOBRES_DOMAIN"]), *dir, env["BOBRES_DOMAIN"])
		} else {
			fmt.Fprintf(o.out, "Stop and remove the BOBRES containers in %s? Data is kept. [y/N]: ", *dir)
		}
		var answer string
		fmt.Fscanln(o.in, &answer) //nolint:errcheck // an empty answer means no
		answer = strings.TrimSpace(answer)
		confirmed := (*purge && answer == env["BOBRES_DOMAIN"] && answer != "") || (!*purge && strings.EqualFold(answer, "y"))
		if !confirmed {
			fmt.Fprintln(o.out, "Cancelled.")
			return 1
		}
	}
	cargs := composeArgs("down", "--remove-orphans")
	if *purge {
		cargs = append(cargs, "--volumes")
	}
	if out, err := o.run(context.Background(), *dir, cargs...); err != nil {
		fmt.Fprintf(o.errOut, "error: docker compose down failed: %v\n%s\n", err, strings.TrimSpace(string(out)))
		return 1
	}
	if !*purge {
		fmt.Fprintf(o.out, "Containers removed. Kept: the database and Redis volumes (%s_*), certificates, and %s (run `bobres install` to start again, or `bobres uninstall --purge` to delete everything).\n",
			composeProject, *dir)
		return 0
	}
	if err := os.RemoveAll(*dir); err != nil {
		fmt.Fprintf(o.errOut, "error: remove %s: %v\n", *dir, err)
		return 1
	}
	fmt.Fprintf(o.out, "Removed containers, volumes and %s. Nothing of BOBRES remains on this server except the images (docker image prune to drop them).\n", *dir)
	return 0
}
