package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// jsonRun is one run as JSON, for tools/bench (docs/21-benchmark.md): the
// same numbers as the Markdown report, with latencies in milliseconds.
type jsonRun struct {
	Users     int     `json:"users"`
	ThinkS    float64 `json:"think_s"`
	DurationS float64 `json:"duration_s"`
	ElapsedS  float64 `json:"elapsed_s"`

	Journeys       int     `json:"journeys"`
	JourneysFailed int     `json:"journeys_failed"`
	JourneysPerS   float64 `json:"journeys_per_s"`
	Calls          int     `json:"calls"`
	CallsPerS      float64 `json:"calls_per_s"`
	Errors         int     `json:"errors"`

	Journey jsonRow   `json:"journey"`
	Rows    []jsonRow `json:"rows"`
	Notes   []string  `json:"notes,omitempty"`
	// Facts are the stack's numbers when the tool runs it in-process
	// (deliveries, core's pool, the database): see TestLocalStack.
	Facts map[string]any `json:"facts,omitempty"`

	GoVersion  string `json:"go_version"`
	GOOS       string `json:"goos"`
	GOARCH     string `json:"goarch"`
	NumCPU     int    `json:"num_cpu"`
	GOMAXPROCS int    `json:"gomaxprocs"`
	At         string `json:"at"`
}

type jsonRow struct {
	Name   string         `json:"name"`
	Calls  int            `json:"calls"`
	PerS   float64        `json:"per_s"`
	P50ms  float64        `json:"p50_ms"`
	P95ms  float64        `json:"p95_ms"`
	P99ms  float64        `json:"p99_ms"`
	MaxMs  float64        `json:"max_ms"`
	Errors int            `json:"errors"`
	Codes  map[string]int `json:"codes,omitempty"`
}

func msf(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

// toJSON summarizes res as writeReport does.
func toJSON(res result) jsonRun {
	out := jsonRun{Users: res.Users, DurationS: res.Duration.Seconds(), ElapsedS: res.Elapsed.Seconds(),
		Notes: res.Notes, Facts: res.Facts, GoVersion: runtime.Version(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		NumCPU: runtime.NumCPU(), GOMAXPROCS: runtime.GOMAXPROCS(0), At: time.Now().UTC().Format(time.RFC3339)}
	out.ThinkS = res.Think.Seconds()
	for _, x := range res.Rec.rows(res.Elapsed) {
		r := jsonRow{Name: x.Name, Calls: x.Calls, PerS: x.PerSec, P50ms: msf(x.P50), P95ms: msf(x.P95),
			P99ms: msf(x.P99), MaxMs: msf(x.Max), Errors: x.Errors}
		if len(x.Codes) > 0 {
			r.Codes = x.Codes
		}
		if x.Name == journeyName {
			out.Journey = r
			out.Journeys, out.JourneysFailed, out.JourneysPerS = x.Calls, x.Errors, x.PerSec
			continue
		}
		out.Rows = append(out.Rows, r)
		out.Calls += x.Calls
		out.Errors += x.Errors
	}
	if s := res.Elapsed.Seconds(); s > 0 {
		out.CallsPerS = float64(out.Calls) / s
	}
	return out
}

// writeJSON writes res to path. With several runs to one -json path
// (TestLocalStack with several user counts) each gets the user count
// before the extension: load.json becomes load-200users.json.
func writeJSON(path string, res result, several bool) error {
	if path == "" {
		return nil
	}
	if several {
		ext := filepath.Ext(path)
		path = fmt.Sprintf("%s-%dusers%s", strings.TrimSuffix(path, ext), res.Users, ext)
	}
	b, err := json.MarshalIndent(toJSON(res), "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o600)
}
