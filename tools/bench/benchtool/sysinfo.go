package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type sysInfo struct {
	Kind       string            `json:"kind"`
	Variant    string            `json:"variant"`
	Build      string            `json:"build"` // the variant's go build command
	At         string            `json:"at"`
	CPU        string            `json:"cpu"`
	CPUs       int               `json:"cpus"`
	MemoryGB   float64           `json:"memory_gb"`
	OS         string            `json:"os"`
	Kernel     string            `json:"kernel"`
	Go         string            `json:"go"`
	Node       string            `json:"node"`
	Npm        string            `json:"npm"`
	Postgres   string            `json:"postgres"`
	PGSettings map[string]string `json:"postgres_settings"`
	Commit     string            `json:"commit"`
	Describe   string            `json:"describe"`
	Load       []float64         `json:"load"`
}

// sysinfo records the machine, the toolchains and the code being measured.
func sysinfo(args []string) error {
	fs := flag.NewFlagSet("sysinfo", flag.ContinueOnError)
	out := fs.String("out", "", "JSON result")
	variant := fs.String("variant", "plain", "the build variant")
	build := fs.String("build", "go build", "the variant's build command")
	base := fs.String("db", envOr("BOBRES_TEST_DATABASE_URL", defaultBaseDSN), "the test Postgres")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out == "" {
		return errors.New("usage: sysinfo -out F.json")
	}
	s := sysInfo{Kind: "sysinfo", Variant: *variant, Build: *build, At: time.Now().UTC().Format(time.RFC3339),
		CPUs: runtime.NumCPU(), Load: loadAvg(), PGSettings: map[string]string{}}
	if runtime.GOOS == "darwin" {
		s.CPU = run("sysctl", "-n", "machdep.cpu.brand_string")
		if b, err := strconv.ParseFloat(run("sysctl", "-n", "hw.memsize"), 64); err == nil {
			s.MemoryGB = b / (1 << 30)
		}
		s.OS = "macOS " + run("sw_vers", "-productVersion")
	} else {
		s.CPU = procField("/proc/cpuinfo", "model name")
		if kb, err := strconv.ParseFloat(strings.Fields(procField("/proc/meminfo", "MemTotal") + " 0")[0], 64); err == nil {
			s.MemoryGB = kb / (1 << 20)
		}
		s.OS = strings.Trim(procField("/etc/os-release", "PRETTY_NAME"), `"`)
	}
	s.Kernel = run("uname", "-sr")
	s.Go = run("go", "version")
	s.Node, s.Npm = run("node", "--version"), run("npm", "--version")
	s.Commit, s.Describe = run("git", "rev-parse", "HEAD"), run("git", "describe", "--tags", "--always", "--dirty")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if c, err := pgx.Connect(ctx, *base); err == nil {
		_ = c.QueryRow(ctx, `SELECT version()`).Scan(&s.Postgres)
		for _, k := range []string{"server_version", "max_connections", "shared_buffers", "synchronous_commit", "fsync", "wal_sync_method"} {
			var v string
			if c.QueryRow(ctx, "SELECT current_setting($1)", k).Scan(&v) == nil {
				s.PGSettings[k] = v
			}
		}
		_ = c.Close(ctx)
	}
	return writeJSON(*out, s)
}

func run(name string, args ...string) string {
	out, err := exec.Command(name, args...).Output() //nolint:gosec // fixed tools
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// procField is the value of the first "key: value" or key=value line.
func procField(path, key string) string {
	f, err := os.Open(path) //nolint:gosec // a fixed system file
	if err != nil {
		return ""
	}
	defer f.Close() //nolint:errcheck // read only
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, key) {
			continue
		}
		if _, v, ok := strings.Cut(line, ":"); ok {
			return strings.TrimSpace(v)
		}
		if _, v, ok := strings.Cut(line, "="); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
