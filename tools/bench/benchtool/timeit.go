package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"time"
)

// timing is one timed command: a line of timings.jsonl.
type timing struct {
	Label    string    `json:"label"`
	Rep      int       `json:"rep"`
	Command  string    `json:"command"`
	WallS    float64   `json:"wall_s"`
	UserS    float64   `json:"user_s"`
	SysS     float64   `json:"sys_s"`
	MaxRSSMB float64   `json:"max_rss_mb"`
	Exit     int       `json:"exit"`
	LoadBef  []float64 `json:"load_before"`
	LoadAft  []float64 `json:"load_after"`
	At       string    `json:"at"`
}

// timeit runs a command, appends how long it took (wall and CPU, the
// load average around it) to -out, and exits with the command's status.
func timeit(args []string) error {
	fs := flag.NewFlagSet("timeit", flag.ContinueOnError)
	out := fs.String("out", "", "timings.jsonl to append to")
	label := fs.String("label", "", "what is measured, e.g. build/linux-amd64/core/clean")
	rep := fs.Int("rep", 1, "repetition number")
	logPath := fs.String("log", "", "write the command's output here (default: pass it through)")
	dir := fs.String("dir", "", "run the command in this directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cmdArgs := fs.Args()
	if *out == "" || *label == "" || len(cmdArgs) == 0 {
		return errors.New("usage: timeit -out F.jsonl -label L [-rep N] [-log F] [-dir D] -- cmd args...")
	}
	var w io.Writer = os.Stdout
	var ew io.Writer = os.Stderr
	if *logPath != "" {
		f, err := os.Create(*logPath)
		if err != nil {
			return err
		}
		defer f.Close() //nolint:errcheck // a log file
		w, ew = f, f
	}
	c := exec.Command(cmdArgs[0], cmdArgs[1:]...) //nolint:gosec // the caller's own command
	c.Stdout, c.Stderr, c.Stdin, c.Dir = w, ew, os.Stdin, *dir
	t := timing{Label: *label, Rep: *rep, Command: strings.Join(cmdArgs, " "), LoadBef: loadAvg(),
		At: time.Now().UTC().Format(time.RFC3339)}
	begin := time.Now()
	err := c.Run()
	t.WallS = time.Since(begin).Seconds()
	t.LoadAft = loadAvg()
	if ps := c.ProcessState; ps != nil {
		t.UserS, t.SysS = ps.UserTime().Seconds(), ps.SystemTime().Seconds()
		t.Exit = ps.ExitCode()
		if ru, ok := ps.SysUsage().(*syscall.Rusage); ok {
			t.MaxRSSMB = maxRSSMB(ru.Maxrss)
		}
	} else if err != nil {
		t.Exit = 127
	}
	if werr := appendJSONL(*out, t); werr != nil {
		return werr
	}
	fmt.Fprintf(os.Stderr, "  %-48s %8.2fs  exit %d  load %v\n", t.Label+fmt.Sprintf(" #%d", t.Rep), t.WallS, t.Exit, t.LoadBef)
	if t.Exit != 0 {
		return exitCode(t.Exit)
	}
	return nil
}

// maxRSSMB converts getrusage's ru_maxrss: bytes on macOS, KiB on Linux.
func maxRSSMB(v int64) float64 {
	if runtime.GOOS == "darwin" {
		return float64(v) / (1 << 20)
	}
	return float64(v) / (1 << 10)
}
