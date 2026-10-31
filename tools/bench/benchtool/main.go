// Command benchtool is the measuring half of tools/bench/run.sh
// (docs/21-benchmark.md): it times commands, measures files, runs the real
// service binaries as a local stack under the load test, and turns the raw
// JSON results into a summary and a comparison between two build variants.
//
//	benchtool timeit -out F.jsonl -label L [-rep N] [-log F] -- cmd args...
//	benchtool sizes -out F.json -label L [-gzip] [-strip-prefix P] paths...
//	benchtool stack -bin-dir D -loadtest BIN -out F.json [-scenarios ...]
//	benchtool migrate -out F.json [-repeat 3]
//	benchtool loc -root . -out F.json
//	benchtool gobench -in bench.txt -out F.json
//	benchtool ghcr -owner sobhanaz -version 0.19.0 -out F.json
//	benchtool sysinfo -out F.json -variant plain
//	benchtool report -dir docs/bench/DATE/VARIANT
//	benchtool compare -a DIR -b DIR
//	benchtool describe
//
// Everything it starts listens on 127.0.0.1 only and uses a database it
// creates and drops itself; it never talks to a real server or account.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	cmds := map[string]func([]string) error{
		"timeit": timeit, "sizes": sizes, "stack": stack, "migrate": migrateCmd, "loc": loc,
		"gobench": gobench, "ghcr": ghcr, "sysinfo": sysinfo, "report": report, "compare": compare,
		"describe": describe,
	}
	f, ok := cmds[os.Args[1]]
	if !ok {
		usage()
	}
	if err := f(os.Args[2:]); err != nil {
		var ec exitCode
		if asExit(err, &ec) {
			os.Exit(int(ec))
		}
		fmt.Fprintln(os.Stderr, "benchtool "+os.Args[1]+":", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: benchtool timeit|sizes|stack|migrate|loc|gobench|ghcr|sysinfo|report|compare ... (see tools/bench/run.sh)")
	os.Exit(2)
}

// exitCode passes a timed command's exit status through benchtool.
type exitCode int

func (e exitCode) Error() string { return "exit status " + strconv.Itoa(int(e)) }

func asExit(err error, ec *exitCode) bool {
	e, ok := err.(exitCode) //nolint:errorlint // only ever returned directly
	if ok {
		*ec = e
	}
	return ok
}

// writeJSON writes v to path, indented, creating the directory.
func writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644) //nolint:gosec // results are meant to be read
}

// appendJSONL appends v as one line to path.
func appendJSONL(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644) //nolint:gosec // results are meant to be read
	if err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path) //nolint:gosec // a results file named by the caller
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// loadAvg is the 1, 5 and 15 minute load average (uptime works on macOS
// and Linux alike).
func loadAvg() []float64 {
	if b, err := os.ReadFile("/proc/loadavg"); err == nil {
		return parseFloats(strings.Fields(string(b)), 3)
	}
	out, err := exec.Command("uptime").Output()
	if err != nil {
		return nil
	}
	s := string(out)
	i := strings.LastIndex(s, "load average")
	if i < 0 {
		return nil
	}
	s = s[i:]
	s = s[strings.Index(s, ":")+1:]
	return parseFloats(strings.Fields(strings.ReplaceAll(s, ",", " ")), 3)
}

func parseFloats(f []string, n int) []float64 {
	var out []float64
	for _, x := range f {
		if len(out) == n {
			break
		}
		v, err := strconv.ParseFloat(x, 64)
		if err != nil {
			continue
		}
		out = append(out, v)
	}
	return out
}

// stats are the median and spread of repeated measurements.
type stats struct {
	N      int     `json:"n"`
	Median float64 `json:"median"`
	Min    float64 `json:"min"`
	Max    float64 `json:"max"`
	// Spread is (max-min)/median: 0.1 means the runs were within 10%.
	Spread float64 `json:"spread"`
}

func summarize(v []float64) stats {
	if len(v) == 0 {
		return stats{}
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	m := s[len(s)/2]
	if len(s)%2 == 0 {
		m = (s[len(s)/2-1] + s[len(s)/2]) / 2
	}
	st := stats{N: len(s), Median: m, Min: s[0], Max: s[len(s)-1]}
	if m != 0 {
		st.Spread = (st.Max - st.Min) / m
	}
	return st
}

// describe prints the tag that names this commit, as release.yml stamps it
// into the binaries (the tag itself on a release commit).
func describe([]string) error {
	v := run("git", "describe", "--tags", "--always")
	if v == "" {
		v = "dev"
	}
	fmt.Println(v)
	return nil
}
