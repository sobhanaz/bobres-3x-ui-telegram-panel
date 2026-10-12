package main

import (
	"cmp"
	"fmt"
	"io"
	"math"
	"slices"
	"strings"
	"time"

	"google.golang.org/grpc/status"
)

// recorder collects one worker's samples, so workers never share a lock;
// merge combines them at the end.
type recorder struct {
	names []string // in the order first seen: the journey's order
	calls map[string]*series
}

// series is one call's samples: the latency of each success and the number
// of failures by gRPC code.
type series struct {
	ok   []time.Duration
	errs map[string]int
}

func newRecorder() *recorder { return &recorder{calls: map[string]*series{}} }

func (r *recorder) series(name string) *series {
	s, ok := r.calls[name]
	if !ok {
		s = &series{errs: map[string]int{}}
		r.calls[name] = s
		r.names = append(r.names, name)
	}
	return s
}

// add records one call: its latency when it succeeded, its code when not.
// Percentiles are over successes only, so a fast failure never makes the
// latency look better.
func (r *recorder) add(name string, d time.Duration, err error) {
	s := r.series(name)
	if err != nil {
		s.errs[status.Code(err).String()]++
		return
	}
	s.ok = append(s.ok, d)
}

func (r *recorder) merge(o *recorder) {
	for _, name := range o.names {
		s, from := r.series(name), o.calls[name]
		s.ok = append(s.ok, from.ok...)
		for code, n := range from.errs {
			s.errs[code] += n
		}
	}
}

// percentile is the nearest-rank p-th percentile (0 < p <= 100) of sorted.
func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	rank := int(math.Ceil(p / 100 * float64(len(sorted))))
	return sorted[min(max(rank, 1), len(sorted))-1]
}

// row is one call's line in the report.
type row struct {
	Name               string
	Calls, Errors      int
	PerSec             float64
	P50, P95, P99, Max time.Duration
	Codes              map[string]int
}

// rows summarizes every call over elapsed, in the journey's order.
func (r *recorder) rows(elapsed time.Duration) []row {
	out := make([]row, 0, len(r.names))
	for _, name := range r.names {
		s := r.calls[name]
		sorted := slices.Clone(s.ok)
		slices.Sort(sorted)
		errs := 0
		for _, n := range s.errs {
			errs += n
		}
		w := row{Name: name, Calls: len(sorted) + errs, Errors: errs, Codes: s.errs,
			P50: percentile(sorted, 50), P95: percentile(sorted, 95), P99: percentile(sorted, 99)}
		if len(sorted) > 0 {
			w.Max = sorted[len(sorted)-1]
		}
		if elapsed > 0 {
			w.PerSec = float64(w.Calls) / elapsed.Seconds()
		}
		out = append(out, w)
	}
	return out
}

// result is one load run.
type result struct {
	Users    int
	Duration time.Duration // how long new journeys were started
	Elapsed  time.Duration // until the last journey ended
	Rec      *recorder
	Notes    []string // facts about the stack, printed under the table
}

// journeyName is the row of whole journeys, kept apart from the calls.
const journeyName = "journey"

// writeReport prints a run as a Markdown table, readable as it is in a
// terminal and ready to paste into docs/12-load-test.md.
func writeReport(w io.Writer, res result) {
	rows := res.Rec.rows(res.Elapsed)
	var calls, errs int
	var journey row
	for _, x := range rows {
		if x.Name == journeyName {
			journey = x
			continue
		}
		calls += x.Calls
		errs += x.Errors
	}
	secs := res.Elapsed.Seconds()
	fmt.Fprintf(w, "\n### %d users, %s\n\n", res.Users, res.Duration)
	fmt.Fprintf(w, "- %d journeys in %.1fs (%.1f/s), %d failed\n", journey.Calls, secs, journey.PerSec, journey.Errors)
	fmt.Fprintf(w, "- %d calls (%.0f/s), %d errors\n", calls, float64(calls)/secs, errs)
	for _, n := range res.Notes {
		fmt.Fprintf(w, "- %s\n", n)
	}
	fmt.Fprintf(w, "\n| call | calls | calls/s | p50 ms | p95 ms | p99 ms | max ms | errors |\n")
	fmt.Fprintf(w, "|---|---:|---:|---:|---:|---:|---:|---:|\n")
	for _, x := range rows {
		name := x.Name
		if name == journeyName {
			name = "**whole journey**"
		}
		fmt.Fprintf(w, "| %s | %d | %.1f | %s | %s | %s | %s | %s |\n", name, x.Calls, x.PerSec,
			ms(x.P50), ms(x.P95), ms(x.P99), ms(x.Max), errText(x))
	}
}

// ms prints a latency in milliseconds, with one decimal below 100 ms.
func ms(d time.Duration) string {
	v := float64(d) / float64(time.Millisecond)
	if v < 100 {
		return fmt.Sprintf("%.1f", v)
	}
	return fmt.Sprintf("%.0f", v)
}

// errText is "0", or the count with its codes: "3 (Unavailable 2, DeadlineExceeded 1)".
func errText(x row) string {
	if x.Errors == 0 {
		return "0"
	}
	codes := make([]string, 0, len(x.Codes))
	for c := range x.Codes {
		codes = append(codes, c)
	}
	slices.SortFunc(codes, func(a, b string) int { // most frequent first
		if n := cmp.Compare(x.Codes[b], x.Codes[a]); n != 0 {
			return n
		}
		return strings.Compare(a, b)
	})
	parts := make([]string, len(codes))
	for i, c := range codes {
		parts[i] = fmt.Sprintf("%s %d", c, x.Codes[c])
	}
	return fmt.Sprintf("%d (%s)", x.Errors, strings.Join(parts, ", "))
}
