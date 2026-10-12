package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestPercentileIsNearestRank(t *testing.T) {
	var sorted []time.Duration
	for i := 1; i <= 100; i++ {
		sorted = append(sorted, time.Duration(i)*time.Millisecond)
	}
	for _, c := range []struct {
		p    float64
		want time.Duration
	}{{50, 50 * time.Millisecond}, {95, 95 * time.Millisecond}, {99, 99 * time.Millisecond}, {100, 100 * time.Millisecond}, {0.1, time.Millisecond}} {
		if got := percentile(sorted, c.p); got != c.want {
			t.Errorf("p%v = %v, want %v", c.p, got, c.want)
		}
	}
	if got := percentile([]time.Duration{7}, 99); got != 7 {
		t.Errorf("one sample: p99 = %v", got)
	}
	if got := percentile(nil, 50); got != 0 {
		t.Errorf("no samples: p50 = %v", got)
	}
}

func TestRecorderMergesWorkersAndCountsErrorsApart(t *testing.T) {
	a, b := newRecorder(), newRecorder()
	a.add("GetUser", 3*time.Millisecond, nil)
	a.add("CreateOrder", 9*time.Millisecond, nil)
	b.add("GetUser", time.Millisecond, nil)
	b.add("GetUser", time.Second, status.Error(codes.Unavailable, "down")) // a failure's latency is not a sample
	b.add("GetUser", 2*time.Millisecond, status.Error(codes.Unavailable, "down"))
	b.add("ListPlans", 5*time.Millisecond, errors.New("not a status"))
	b.add(journeyName, 20*time.Millisecond, nil)

	total := newRecorder()
	total.merge(a)
	total.merge(b)
	rows := total.rows(2 * time.Second)
	var names []string
	for _, r := range rows {
		names = append(names, r.Name)
	}
	if got := strings.Join(names, ","); got != "GetUser,CreateOrder,ListPlans,journey" {
		t.Fatalf("order = %s, want the order first seen", got)
	}
	g := rows[0]
	if g.Calls != 4 || g.Errors != 2 || g.Codes["Unavailable"] != 2 || g.PerSec != 2 {
		t.Fatalf("GetUser row = %+v", g)
	}
	if g.P50 != time.Millisecond || g.P99 != 3*time.Millisecond || g.Max != 3*time.Millisecond {
		t.Fatalf("GetUser latencies = %v %v %v, want successes only", g.P50, g.P99, g.Max)
	}
	if rows[2].Codes["Unknown"] != 1 {
		t.Fatalf("a plain error = %+v, want code Unknown", rows[2])
	}
}

func TestReport(t *testing.T) {
	rec := newRecorder()
	for range 3 {
		rec.add("GetUser", 1500*time.Microsecond, nil)
	}
	rec.add("GetUser", 0, status.Error(codes.DeadlineExceeded, "slow"))
	rec.add("GetUser", 0, status.Error(codes.Unavailable, "down"))
	rec.add("GetUser", 0, status.Error(codes.Unavailable, "down"))
	rec.add(journeyName, 250*time.Millisecond, nil)
	rec.add(journeyName, 0, errors.New("GetUser: down"))
	var out bytes.Buffer
	writeReport(&out, result{Users: 50, Duration: 30 * time.Second, Elapsed: 2 * time.Second, Rec: rec, Notes: []string{"Postgres 16.15"}})
	for _, want := range []string{
		"### 50 users, 30s",
		"- 2 journeys in 2.0s (1.0/s), 1 failed",
		"- 6 calls (3/s), 3 errors",
		"- Postgres 16.15",
		"| GetUser | 6 | 3.0 | 1.5 | 1.5 | 1.5 | 1.5 | 3 (Unavailable 2, DeadlineExceeded 1) |",
		"| **whole journey** | 2 | 1.0 | 250 | 250 | 250 | 250 | 1 (Unknown 1) |",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report lacks %q:\n%s", want, out.String())
		}
	}
}
