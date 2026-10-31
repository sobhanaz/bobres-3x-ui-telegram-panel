package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// metric is one headline number of a run, as summary.json keeps it for
// compare.
type metric struct {
	Key    string  `json:"metric"`
	Value  float64 `json:"value"`
	Unit   string  `json:"unit"`
	Spread float64 `json:"spread,omitempty"`
	N      int     `json:"n,omitempty"`
}

type summaryFile struct {
	Kind    string   `json:"kind"`
	Variant string   `json:"variant"`
	Dir     string   `json:"dir"`
	Metrics []metric `json:"metrics"`
}

// md collects the Markdown summary and the metrics.
type md struct {
	b       strings.Builder
	metrics []metric
	notes   []string
}

func (m *md) p(format string, a ...any) { fmt.Fprintf(&m.b, format+"\n", a...) }

func (m *md) add(key, unit string, s stats) {
	if s.N == 0 {
		return
	}
	m.metrics = append(m.metrics, metric{Key: key, Value: s.Median, Unit: unit, Spread: s.Spread, N: s.N})
}

func (m *md) one(key, unit string, v float64) {
	m.metrics = append(m.metrics, metric{Key: key, Value: v, Unit: unit, N: 1})
}

// flag notes a spread above 15% (re-run it) in the summary.
func (m *md) check(what string, s stats) {
	if s.N > 1 && s.Spread > 0.15 {
		m.notes = append(m.notes, fmt.Sprintf("%s: the %d runs spread %.0f%% (%s to %s): re-run it if the load average was high", what, s.N, 100*s.Spread, g(s.Min), g(s.Max)))
	}
}

// med is "median (±spread)".
func med(s stats, f func(float64) string) string {
	if s.N == 0 {
		return "–"
	}
	if s.N == 1 {
		return f(s.Median)
	}
	return fmt.Sprintf("%s (±%.0f%%)", f(s.Median), 100*s.Spread/2)
}

// rng is "median (min–max)": for throughput, where a busy machine pulls
// single runs far down and the best run is worth seeing.
func rng(s stats, f func(float64) string) string {
	if s.N < 2 {
		return med(s, f)
	}
	return fmt.Sprintf("%s (%s–%s)", f(s.Median), f(s.Min), f(s.Max))
}

func g(v float64) string { return strconv.FormatFloat(v, 'g', 4, 64) }
func f1(v float64) string {
	return strconv.FormatFloat(v, 'f', 1, 64)
}
func f2(v float64) string { return strconv.FormatFloat(v, 'f', 2, 64) }
func f3(v float64) string { return strconv.FormatFloat(v, 'f', 3, 64) }
func f0(v float64) string { return strconv.FormatFloat(v, 'f', 0, 64) }
func mb(v float64) string { return f1(v / 1e6) }
func ms3(v float64) string {
	if v >= 100 {
		return f0(v)
	}
	return f1(v)
}

// report reads a run's raw results and writes summary.md and summary.json.
func report(args []string) error {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	dir := fs.String("dir", "", "a run's results: docs/bench/DATE/VARIANT")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dir == "" {
		return errors.New("usage: report -dir docs/bench/DATE/VARIANT")
	}
	m := &md{}
	var sys sysInfo
	_ = readJSON(filepath.Join(*dir, "system.json"), &sys)
	timings, err := readTimings(filepath.Join(*dir, "timings.jsonl"))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	m.p("# BOBRES benchmark: %s, variant %s", sys.Describe, sys.Variant)
	m.p("")
	m.p("Raw results are the JSON files next to this one; `tools/bench/benchtool report` wrote this summary and `compare` puts two variants side by side (docs/21-benchmark.md). Each timed number is the median of the runs, ± half their spread.")
	m.p("")
	m.p("- Machine: %s, %d CPUs, %.0f GB, %s (%s)", sys.CPU, sys.CPUs, sys.MemoryGB, sys.OS, sys.Kernel)
	m.p("- Toolchains: %s; Node %s, npm %s", sys.Go, sys.Node, sys.Npm)
	m.p("- Postgres: %s (%s)", sys.PGSettings["server_version"], settingsLine(sys.PGSettings))
	m.p("- Code: %s (%s); build: `%s`", sys.Describe, short(sys.Commit), sys.Build)
	m.p("- Load average when the run started: %v", sys.Load)
	m.p("")

	reportLoadtest(m, *dir, timings)
	reportStack(m, *dir)
	reportBinaries(m, *dir, timings)
	reportMigrate(m, *dir)
	reportTests(m, *dir, timings)
	reportWeb(m, *dir, timings)
	reportRelease(m, *dir)
	reportLoc(m, *dir)
	reportGoBench(m, *dir)
	if len(m.notes) > 0 {
		m.p("## Notes")
		m.p("")
		for _, n := range m.notes {
			m.p("- %s", n)
		}
		m.p("")
	}
	if err := os.WriteFile(filepath.Join(*dir, "summary.md"), []byte(m.b.String()), 0o644); err != nil { //nolint:gosec // meant to be read
		return err
	}
	return writeJSON(filepath.Join(*dir, "summary.json"), summaryFile{Kind: "summary", Variant: sys.Variant, Dir: *dir, Metrics: m.metrics})
}

func settingsLine(s map[string]string) string {
	var parts []string
	for _, k := range []string{"max_connections", "shared_buffers", "synchronous_commit", "fsync", "wal_sync_method"} {
		if v, ok := s[k]; ok {
			parts = append(parts, k+" "+v)
		}
	}
	return strings.Join(parts, ", ")
}

func short(c string) string {
	if len(c) > 12 {
		return c[:12]
	}
	return c
}

// readTimings reads timings.jsonl; a later line for the same label and
// repetition replaces an earlier one (a re-run of an outlier).
func readTimings(path string) (map[string][]timing, error) {
	f, err := os.Open(path) //nolint:gosec // a results file
	if err != nil {
		return nil, err
	}
	defer f.Close() //nolint:errcheck // read only
	last := map[string]map[int]timing{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		var t timing
		if err := json.Unmarshal(sc.Bytes(), &t); err != nil {
			continue
		}
		if last[t.Label] == nil {
			last[t.Label] = map[int]timing{}
		}
		last[t.Label][t.Rep] = t
	}
	out := map[string][]timing{}
	for label, reps := range last {
		keys := make([]int, 0, len(reps))
		for r := range reps {
			keys = append(keys, r)
		}
		sort.Ints(keys)
		for _, r := range keys {
			out[label] = append(out[label], reps[r])
		}
	}
	return out, sc.Err()
}

func wall(ts []timing) stats {
	var v []float64
	for _, t := range ts {
		if t.Exit == 0 {
			v = append(v, t.WallS)
		}
	}
	return summarize(v)
}

func failed(ts []timing) int {
	n := 0
	for _, t := range ts {
		if t.Exit != 0 {
			n++
		}
	}
	return n
}

func loads(ts []timing) string {
	var parts []string
	for _, t := range ts {
		if len(t.LoadBef) > 0 {
			parts = append(parts, f1(t.LoadBef[0]))
		}
	}
	return strings.Join(parts, ", ")
}

// --- load test, in-process -------------------------------------------------

var inprocName = regexp.MustCompile(`^inproc-([a-z]+)-(\d+)-r(\d+)\.json$`)

type ltGroup struct {
	name  string
	users int
	runs  []jsonRunR
	quiet []jsonRunR // the runs that started with the 1-minute load average below quietLoad
	label string
}

// quietLoad: a run that starts with a 1-minute load average below this had
// the machine (and its Postgres) mostly to itself. On a shared machine other
// work pulls the load test far down, so those runs are shown apart.
const quietLoad = 4.0

// jsonRunR is the load test's -json output, as read back.
type jsonRunR struct {
	Users          int            `json:"users"`
	ThinkS         float64        `json:"think_s"`
	ElapsedS       float64        `json:"elapsed_s"`
	Journeys       int            `json:"journeys"`
	JourneysFailed int            `json:"journeys_failed"`
	JourneysPerS   float64        `json:"journeys_per_s"`
	Calls          int            `json:"calls"`
	CallsPerS      float64        `json:"calls_per_s"`
	Errors         int            `json:"errors"`
	Journey        jsonRowR       `json:"journey"`
	Rows           []jsonRowR     `json:"rows"`
	Facts          map[string]any `json:"facts"`
}

type jsonRowR struct {
	Name   string         `json:"name"`
	Calls  int            `json:"calls"`
	PerS   float64        `json:"per_s"`
	P50    float64        `json:"p50_ms"`
	P95    float64        `json:"p95_ms"`
	P99    float64        `json:"p99_ms"`
	Max    float64        `json:"max_ms"`
	Errors int            `json:"errors"`
	Codes  map[string]int `json:"codes"`
}

func collect(runs []jsonRunR, f func(jsonRunR) (float64, bool)) stats {
	var v []float64
	for _, r := range runs {
		if x, ok := f(r); ok {
			v = append(v, x)
		}
	}
	return summarize(v)
}

func fact(r jsonRunR, k string) (float64, bool) {
	v, ok := r.Facts[k].(float64)
	return v, ok
}

func rowStat(runs []jsonRunR, name string, f func(jsonRowR) float64) stats {
	return collect(runs, func(r jsonRunR) (float64, bool) {
		for _, x := range r.Rows {
			if x.Name == name {
				return f(x), true
			}
		}
		return 0, false
	})
}

func reportLoadtest(m *md, dir string, timings map[string][]timing) {
	entries, _ := os.ReadDir(filepath.Join(dir, "loadtest"))
	groups := map[string]*ltGroup{}
	for _, e := range entries {
		sm := inprocName.FindStringSubmatch(e.Name())
		if sm == nil {
			continue
		}
		var r jsonRunR
		if readJSON(filepath.Join(dir, "loadtest", e.Name()), &r) != nil {
			continue
		}
		key := sm[1] + "-" + sm[2]
		gr, ok := groups[key]
		if !ok {
			n, _ := strconv.Atoi(sm[2])
			gr = &ltGroup{name: sm[1], users: n, label: "loadtest/inproc/" + key}
			groups[key] = gr
		}
		gr.runs = append(gr.runs, r)
		rep, _ := strconv.Atoi(sm[3])
		for _, t := range timings[gr.label] {
			if t.Rep == rep && len(t.LoadBef) > 0 && t.LoadBef[0] < quietLoad {
				gr.quiet = append(gr.quiet, r)
			}
		}
	}
	if len(groups) == 0 {
		return
	}
	var keys []string
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := groups[keys[i]], groups[keys[j]]
		if a.name != b.name {
			return a.name > b.name // stress before paced
		}
		return a.users < b.users
	})
	m.p("## Load test, in-process (as in docs/12-load-test.md)")
	m.p("")
	m.p("| Run | Users | Journeys/s | Calls/s | Journey p50 / p95 / p99 ms | Slowest call at p99 | Errors (all runs) | Pool acquires that waited | Waiting at the end, drained in | DB after | Load avg before |")
	m.p("|---|---:|---:|---:|---|---|---:|---|---|---:|---|")
	for _, k := range keys {
		gr := groups[k]
		js := collect(gr.runs, func(r jsonRunR) (float64, bool) { return r.JourneysPerS, true })
		cs := collect(gr.runs, func(r jsonRunR) (float64, bool) { return r.CallsPerS, true })
		p50 := collect(gr.runs, func(r jsonRunR) (float64, bool) { return r.Journey.P50, true })
		p95 := collect(gr.runs, func(r jsonRunR) (float64, bool) { return r.Journey.P95, true })
		p99 := collect(gr.runs, func(r jsonRunR) (float64, bool) { return r.Journey.P99, true })
		share := collect(gr.runs, func(r jsonRunR) (float64, bool) { return fact(r, "pool_waited_share") })
		wait := collect(gr.runs, func(r jsonRunR) (float64, bool) { return fact(r, "pool_avg_wait_ms") })
		backlog := collect(gr.runs, func(r jsonRunR) (float64, bool) { return fact(r, "backlog_at_end") })
		drain := collect(gr.runs, func(r jsonRunR) (float64, bool) { return fact(r, "backlog_drain_s") })
		dbb := collect(gr.runs, func(r jsonRunR) (float64, bool) { return fact(r, "db_bytes") })
		errs := 0
		codes := map[string]int{}
		for _, r := range gr.runs {
			errs += r.Errors
			for _, x := range r.Rows {
				for c, n := range x.Codes {
					codes[x.Name+" "+c] += n
				}
			}
		}
		slow, slowV := "", 0.0
		for _, x := range gr.runs[0].Rows {
			if v := rowStat(gr.runs, x.Name, func(r jsonRowR) float64 { return r.P99 }).Median; v > slowV {
				slow, slowV = x.Name, v
			}
		}
		errText := strconv.Itoa(errs)
		if len(codes) > 0 {
			errText += " (" + joinCodes(codes) + ")"
		}
		poolText := "–"
		if share.N > 0 {
			poolText = fmt.Sprintf("%.0f%%, %s ms average", 100*share.Median, f1(wait.Median))
		}
		drainText := "none"
		if backlog.Median > 0 {
			drainText = fmt.Sprintf("%s in %s s", f0(backlog.Median), f0(drain.Median))
		}
		m.p("| %s | %d | %s | %s | %s / %s / %s | %s %s ms | %s | %s | %s | %s MB | %s |", gr.name, gr.users, rng(js, f0), rng(cs, f0),
			ms3(p50.Median), ms3(p95.Median), ms3(p99.Median), slow, ms3(slowV), errText, poolText, drainText, mb(dbb.Median), loads(timings[gr.label]))
		key := "loadtest/inproc/" + k
		m.add(key+"/journeys_per_s", "1/s", js)
		m.add(key+"/calls_per_s", "1/s", cs)
		m.add(key+"/journey_p95_ms", "ms", p95)
		m.add(key+"/journey_p99_ms", "ms", p99)
		m.check("in-process "+k+" journeys/s", js)
	}
	m.p("")
	m.p("Quiet runs only (1-minute load average below %s when the run started):", g(quietLoad))
	m.p("")
	m.p("| Run | Users | Quiet runs | Journeys/s | Calls/s | Journey p50 / p95 / p99 ms |")
	m.p("|---|---:|---:|---:|---:|---|")
	for _, k := range keys {
		gr := groups[k]
		if len(gr.quiet) == 0 {
			m.p("| %s | %d | 0 of %d | – | – | – |", gr.name, gr.users, len(gr.runs))
			continue
		}
		js := collect(gr.quiet, func(r jsonRunR) (float64, bool) { return r.JourneysPerS, true })
		cs := collect(gr.quiet, func(r jsonRunR) (float64, bool) { return r.CallsPerS, true })
		p50 := collect(gr.quiet, func(r jsonRunR) (float64, bool) { return r.Journey.P50, true })
		p95 := collect(gr.quiet, func(r jsonRunR) (float64, bool) { return r.Journey.P95, true })
		p99 := collect(gr.quiet, func(r jsonRunR) (float64, bool) { return r.Journey.P99, true })
		m.p("| %s | %d | %d of %d | %s | %s | %s / %s / %s |", gr.name, gr.users, len(gr.quiet), len(gr.runs), rng(js, f0), rng(cs, f0),
			ms3(p50.Median), ms3(p95.Median), ms3(p99.Median))
		m.add("loadtest/inproc/"+k+"/quiet_journeys_per_s", "1/s", js)
	}
	m.p("")
	for _, k := range keys {
		gr := groups[k]
		m.p("<details><summary>Per call: %s, %d users (median of %d runs)</summary>", gr.name, gr.users, len(gr.runs))
		m.p("")
		m.p("| call | calls/s | p50 ms | p95 ms | p99 ms | max ms | errors |")
		m.p("|---|---:|---:|---:|---:|---:|---:|")
		for _, x := range gr.runs[0].Rows {
			per := rowStat(gr.runs, x.Name, func(r jsonRowR) float64 { return r.PerS })
			p50 := rowStat(gr.runs, x.Name, func(r jsonRowR) float64 { return r.P50 })
			p95 := rowStat(gr.runs, x.Name, func(r jsonRowR) float64 { return r.P95 })
			p99 := rowStat(gr.runs, x.Name, func(r jsonRowR) float64 { return r.P99 })
			mx := rowStat(gr.runs, x.Name, func(r jsonRowR) float64 { return r.Max })
			errs := rowStat(gr.runs, x.Name, func(r jsonRowR) float64 { return float64(r.Errors) })
			m.p("| %s | %s | %s | %s | %s | %s | %s |", x.Name, f1(per.Median), ms3(p50.Median), ms3(p95.Median), ms3(p99.Median), ms3(mx.Median), f0(errs.Max))
			m.add("loadtest/inproc/"+k+"/"+x.Name+"/p95_ms", "ms", p95)
		}
		m.p("")
		m.p("</details>")
		m.p("")
	}
}

func joinCodes(c map[string]int) string {
	keys := make([]string, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s %d", k, c[k])
	}
	return strings.Join(parts, ", ")
}

// --- the stack of real binaries ---------------------------------------------

type stackResultR struct {
	Order     []string              `json:"order"`
	Services  map[string]*svcResult `json:"services"`
	Scenarios []struct {
		Name       string    `json:"name"`
		Users      int       `json:"users"`
		ThinkS     float64   `json:"think_s"`
		DurationS  float64   `json:"duration_s"`
		LoadBefore []float64 `json:"load_before"`
		Loadtest   jsonRunR  `json:"loadtest"`
		BacklogEnd int       `json:"backlog_at_end"`
		DrainS     float64   `json:"drain_s"`
	} `json:"scenarios"`
	DB   map[string]any `json:"db"`
	Load []float64      `json:"load_before"`
}

func reportStack(m *md, dir string) {
	files, _ := filepath.Glob(filepath.Join(dir, "stack", "stack-r*.json"))
	var runs []stackResultR
	for _, f := range files {
		var r stackResultR
		if readJSON(f, &r) == nil && len(r.Services) > 0 {
			runs = append(runs, r)
		}
	}
	if len(runs) == 0 {
		return
	}
	order := runs[0].Order
	svc := func(name string, f func(*svcResult) float64) stats {
		var v []float64
		for _, r := range runs {
			if s, ok := r.Services[name]; ok {
				v = append(v, f(s))
			}
		}
		return summarize(v)
	}
	m.p("## The service binaries as separate processes (%d runs)", len(runs))
	m.p("")
	m.p("Each run: a fresh database, the services started one after the other (cold: the database is empty, so each runs its migrations), an idle period (RSS idle is its median, CPU idle its average), then the load scenarios below one after the other on the same stack, each followed by the delivery drain, then every service restarted once on the loaded database (warm) and stopped. Ready is the time from exec to `/readyz` answering 200 (the tls-agent: its first heartbeat). RSS is sampled with ps; peak RSS and shutdown are from the stop after the load.")
	m.p("")
	m.p("First exec is the binary's first `--version` in run 1, just after the build (macOS scans a new executable then; later runs are much faster), so it is not counted in the starts.")
	m.p("")
	m.p("| Service | First exec | Ready, cold | Ready, warm | RSS idle | CPU idle | Shutdown | Peak RSS (rusage) |")
	m.p("|---|---:|---:|---:|---:|---:|---:|---:|")
	for _, name := range order {
		fe := "–"
		if s := runs[0].Services[name]; s != nil && s.FirstExecS > 0 {
			fe = f0(s.FirstExecS*1000) + " ms"
		}
		cold := svc(name, func(s *svcResult) float64 { return s.ColdReadyS * 1000 })
		warm := svc(name, func(s *svcResult) float64 { return s.WarmReadyS * 1000 })
		rss := svc(name, func(s *svcResult) float64 { return s.IdleRSSMB })
		cpu := svc(name, func(s *svcResult) float64 { return s.IdleCPUPct })
		sd := svc(name, func(s *svcResult) float64 { return s.FirstStop.ShutdownS * 1000 })
		peak := svc(name, func(s *svcResult) float64 { return s.FirstStop.MaxRSSMB })
		m.p("| %s | %s | %s ms | %s ms | %s MB | %s | %s ms | %s MB |", name, fe, med(cold, f0), med(warm, f0), med(rss, f1), med(cpu, func(v float64) string { return f2(v) + "%" }), med(sd, f0), med(peak, f1))
		m.add("stack/"+name+"/ready_cold_ms", "ms", cold)
		m.add("stack/"+name+"/ready_warm_ms", "ms", warm)
		m.add("stack/"+name+"/rss_idle_mb", "MB", rss)
		m.add("stack/"+name+"/rss_peak_mb", "MB", peak)
		m.check("ready (cold) of "+name, cold)
	}
	m.p("")
	for i, sc := range runs[0].Scenarios {
		get := func(f func(jsonRunR) float64) stats {
			var v []float64
			for _, r := range runs {
				if i < len(r.Scenarios) {
					v = append(v, f(r.Scenarios[i].Loadtest))
				}
			}
			return summarize(v)
		}
		js := get(func(r jsonRunR) float64 { return r.JourneysPerS })
		cs := get(func(r jsonRunR) float64 { return r.CallsPerS })
		p95 := get(func(r jsonRunR) float64 { return r.Journey.P95 })
		errs := get(func(r jsonRunR) float64 { return float64(r.Errors) })
		var bl, dr []float64
		for _, r := range runs {
			if i < len(r.Scenarios) {
				bl, dr = append(bl, float64(r.Scenarios[i].BacklogEnd)), append(dr, r.Scenarios[i].DrainS)
			}
		}
		think := ""
		if sc.ThinkS > 0 {
			think = fmt.Sprintf(", %s s between updates", g(sc.ThinkS))
		}
		m.p("### %s: %d users for %.0f s%s", sc.Name, sc.Users, sc.DurationS, think)
		m.p("")
		m.p("%s journeys/s, %s calls/s, journey p95 %s ms, errors %s (worst run); %s orders waiting at the end, delivered in %s s.",
			rng(js, f0), rng(cs, f0), ms3(p95.Median), f0(errs.Max), f0(summarize(bl).Median), f0(summarize(dr).Median))
		m.p("")
		m.p("| Service | CPU under load (cores) | RSS under load, mean / max | CPU in the drain (cores) | RSS in the drain, max |")
		m.p("|---|---:|---:|---:|---:|")
		for _, name := range order {
			ph := func(p string, f func(phaseUsage) float64) stats {
				return svc(name, func(s *svcResult) float64 { return f(s.Phases[p]) })
			}
			cores := ph(sc.Name, func(u phaseUsage) float64 { return u.CPUCores })
			mean := ph(sc.Name, func(u phaseUsage) float64 { return u.RSSMeanMB })
			mx := ph(sc.Name, func(u phaseUsage) float64 { return u.RSSMaxMB })
			dc := ph(sc.Name+"-drain", func(u phaseUsage) float64 { return u.CPUCores })
			dm := ph(sc.Name+"-drain", func(u phaseUsage) float64 { return u.RSSMaxMB })
			m.p("| %s | %s | %s / %s MB | %s | %s MB |", name, med(cores, f2), f1(mean.Median), med(mx, f1), med(dc, f2), f1(dm.Median))
			m.add("stack/"+sc.Name+"/"+name+"/cpu_cores", "cores", cores)
			m.add("stack/"+sc.Name+"/"+name+"/rss_max_mb", "MB", mx)
		}
		m.p("")
		m.add("stack/"+sc.Name+"/journeys_per_s", "1/s", js)
		m.add("stack/"+sc.Name+"/journey_p95_ms", "ms", p95)
		m.check("stack "+sc.Name+" journeys/s", js)
	}
	var dbb, users []float64
	for _, r := range runs {
		dbb = append(dbb, num(r.DB["bytes"]))
		users = append(users, num(r.DB["users"]))
	}
	m.p("Database after all scenarios: %s MB for %s customers.", mb(summarize(dbb).Median), f0(summarize(users).Median))
	m.p("")
	m.add("stack/db_bytes", "B", summarize(dbb))
}

// --- binaries ---------------------------------------------------------------

func reportBinaries(m *md, dir string, timings map[string][]timing) {
	files, _ := filepath.Glob(filepath.Join(dir, "sizes", "bin-*.json"))
	if len(files) == 0 {
		return
	}
	m.p("## Binaries")
	m.p("")
	m.p("Built as release.yml builds them: `CGO_ENABLED=0 <build> -trimpath -ldflags \"-s -w -X ...version...\"`, with the dashboard and the Mini App built first (core embeds them). Clean: an empty GOCACHE; warm: the same build again with that cache.")
	m.p("")
	m.p("| Target | Binary | Size | Build, clean | Build, warm |")
	m.p("|---|---|---:|---:|---:|")
	for _, f := range files {
		var set sizeSet
		if readJSON(f, &set) != nil {
			continue
		}
		target := strings.TrimPrefix(set.Label, "bin/")
		sort.Slice(set.Files, func(i, j int) bool { return set.Files[i].Path < set.Files[j].Path })
		for _, fl := range set.Files {
			name := filepath.Base(fl.Path)
			clean := wall(timings["build/"+target+"/"+name+"/clean"])
			warm := wall(timings["build/"+target+"/"+name+"/warm"])
			m.p("| %s | %s | %s MB | %s | %s |", target, name, f2(float64(fl.Raw)/1e6), med(clean, func(v float64) string { return f1(v) + " s" }),
				med(warm, func(v float64) string { return f1(v) + " s" }))
			m.one("bin/"+target+"/"+name+"/bytes", "B", float64(fl.Raw))
			m.add("build/"+target+"/"+name+"/clean_s", "s", clean)
			m.add("build/"+target+"/"+name+"/warm_s", "s", warm)
			m.check("clean build of "+target+"/"+name, clean)
		}
	}
	m.p("")
}

func reportMigrate(m *md, dir string) {
	var r migrateResult
	if readJSON(filepath.Join(dir, "migrate.json"), &r) != nil {
		return
	}
	m.p("## Database migrations")
	m.p("")
	m.p("All migrations on an empty database, %d runs, each on a fresh database: %s s (core %s s, payments %s s, provisioner %s s); running them again on the migrated database (what every restart does): %s s. Migration files: core %d, payments %d, provisioner %d.",
		r.Total.N, med(r.Total, f2), f2(r.Schema["core"].Median), f2(r.Schema["payments"].Median), f2(r.Schema["provisioner"].Median), med(r.Noop, f3),
		r.Files["core"], r.Files["payments"], r.Files["provisioner"])
	m.p("")
	m.add("migrate/total_s", "s", r.Total)
	m.add("migrate/noop_s", "s", r.Noop)
}

// --- tests ------------------------------------------------------------------

type vitestJSON struct {
	NumTotalTests       int  `json:"numTotalTests"`
	NumPassedTests      int  `json:"numPassedTests"`
	NumFailedTests      int  `json:"numFailedTests"`
	NumPendingTests     int  `json:"numPendingTests"`
	Files               int  `json:"files"`
	Success             bool `json:"success"`
	NumFailedTestSuites int  `json:"numFailedTestSuites"`
}

func reportTests(m *md, dir string, timings map[string][]timing) {
	rows := []struct{ label, what string }{
		{"test/go/default", "`go test -count=1 ./...` (default -p)"},
		{"test/go/p2", "`go test -count=1 -p 2 ./...`"},
		{"test/go/race", "`go test -race -count=1 -timeout 20m ./...`, as CI's Test (race) job (without its coverage profile)"},
		{"test/go/race-pool4", "CI's concurrency step: `-race` with BOBRES_TEST_POOL_MAX_CONNS=4 on internal/core/domain and internal/core/store"},
		{"test/vitest/dashboard", "dashboard `vitest run`"},
		{"test/vitest/miniapp", "Mini App `vitest run`"},
	}
	found := false
	for _, r := range rows {
		if len(timings[r.label]) > 0 {
			found = true
		}
	}
	if !found {
		return
	}
	m.p("## Tests")
	m.p("")
	m.p("| Suite | Wall time | CPU (user+sys) | Failed runs | Load avg before |")
	m.p("|---|---:|---:|---:|---|")
	for _, r := range rows {
		ts := timings[r.label]
		if len(ts) == 0 {
			continue
		}
		w := wall(ts)
		var cpu []float64
		for _, t := range ts {
			cpu = append(cpu, t.UserS+t.SysS)
		}
		m.p("| %s | %s | %s | %d of %d | %s |", r.what, med(w, func(v float64) string { return f1(v) + " s" }),
			f0(summarize(cpu).Median)+" s", failed(ts), len(ts), loads(ts))
		m.add(r.label+"/wall_s", "s", w)
		m.check(r.what, w)
	}
	m.p("")
	var loc locResult
	if readJSON(filepath.Join(dir, "loc.json"), &loc) == nil {
		m.p("Go test functions (top level): %d Test, %d Benchmark, %d Fuzz, %d Example.", loc.GoFuncs["Test"], loc.GoFuncs["Benchmark"], loc.GoFuncs["Fuzz"], loc.GoFuncs["Example"])
		m.one("tests/go_test_funcs", "", float64(loc.GoFuncs["Test"]))
	}
	for _, app := range []string{"dashboard", "miniapp"} {
		files, _ := filepath.Glob(filepath.Join(dir, "vitest", app+"-r*.json"))
		if len(files) == 0 {
			continue
		}
		var v vitestJSON
		if readJSON(files[len(files)-1], &v) == nil {
			m.p("%s vitest: %d tests in %d files, %d passed, %d failed, %d skipped.", app, v.NumTotalTests, v.Files, v.NumPassedTests, v.NumFailedTests, v.NumPendingTests)
			m.one("tests/vitest/"+app+"/tests", "", float64(v.NumTotalTests))
		}
	}
	m.p("")
}

// --- frontend ---------------------------------------------------------------

func reportWeb(m *md, dir string, timings map[string][]timing) {
	first := true
	for _, app := range []string{"dashboard", "miniapp"} {
		var set sizeSet
		if readJSON(filepath.Join(dir, "sizes", "web-"+app+".json"), &set) != nil {
			continue
		}
		if first {
			m.p("## Frontend bundles")
			m.p("")
			first = false
		}
		build := wall(timings["web/"+app+"/build"])
		m.p("### %s: %s MB raw, %s MB gzip, %d files; `npm run build` %s", app, mb(float64(set.TotalRaw)), mb(float64(set.TotalGzip)), len(set.Files),
			med(build, func(v float64) string { return f1(v) + " s" }))
		m.p("")
		m.p("<details><summary>Per file (largest first)</summary>")
		m.p("")
		m.p("| File | Raw KB | Gzip KB |")
		m.p("|---|---:|---:|")
		for _, f := range set.Files {
			m.p("| %s | %s | %s |", f.Path, f1(float64(f.Raw)/1e3), f1(float64(f.Gzip)/1e3))
		}
		m.p("")
		m.p("</details>")
		m.p("")
		m.one("web/"+app+"/raw_bytes", "B", float64(set.TotalRaw))
		m.one("web/"+app+"/gzip_bytes", "B", float64(set.TotalGzip))
		m.add("web/"+app+"/build_s", "s", build)
	}
}

// --- release artifacts ------------------------------------------------------

type ciJobs struct {
	Runs []struct {
		Workflow string `json:"workflow"`
		ID       int64  `json:"id"`
		Ref      string `json:"ref"`
		Jobs     []struct {
			Name     string  `json:"name"`
			Result   string  `json:"conclusion"`
			Seconds  float64 `json:"seconds"`
			Started  string  `json:"startedAt"`
			Complete string  `json:"completedAt"`
		} `json:"jobs"`
		WallS float64 `json:"wall_s"`
	} `json:"runs"`
}

func reportRelease(m *md, dir string) {
	var gh ghcrResult
	haveImages := readJSON(filepath.Join(dir, "ghcr.json"), &gh) == nil
	var ci ciJobs
	haveCI := readJSON(filepath.Join(dir, "ci.json"), &ci) == nil
	if !haveImages && !haveCI {
		return
	}
	m.p("## Release artifacts")
	m.p("")
	if haveImages {
		m.p("Images of %s on ghcr.io (compressed: the download), read %s:", gh.Version, gh.At)
		m.p("")
		m.p("| Image | linux/amd64 | linux/arm64 | Layers | Note |")
		m.p("|---|---:|---:|---:|---|")
		for _, im := range gh.Images {
			m.p("| %s | %s MB | %s MB | %d | %s |", im.Service, mb(float64(im.Platform["linux/amd64"])), mb(float64(im.Platform["linux/arm64"])), im.Layers["linux/amd64"], im.Error)
			if im.Error == "" {
				m.one("image/"+im.Service+"/amd64_bytes", "B", float64(im.Platform["linux/amd64"]))
			}
		}
		m.p("")
	}
	if haveCI {
		for _, r := range ci.Runs {
			m.p("%s run %d (%s): %s min from the first job's start to the last one's end.", r.Workflow, r.ID, r.Ref, f1(r.WallS/60))
			m.p("")
			m.p("| Job | Result | Minutes |")
			m.p("|---|---|---:|")
			for _, j := range r.Jobs {
				m.p("| %s | %s | %s |", j.Name, j.Result, f1(j.Seconds/60))
				m.one("ci/"+r.Workflow+"/"+j.Name+"/s", "s", j.Seconds)
			}
			m.p("")
		}
	}
}

func reportLoc(m *md, dir string) {
	var loc locResult
	if readJSON(filepath.Join(dir, "loc.json"), &loc) != nil {
		return
	}
	m.p("## Code size")
	m.p("")
	m.p("| Kind | Files | Lines | Non-blank |")
	m.p("|---|---:|---:|---:|")
	kinds := []string{"go", "go_test", "go_generated", "proto", "ts", "ts_test", "vue", "sql_migrations", "shell", "docs", "docs_wiki"}
	for _, k := range kinds {
		c, ok := loc.Count[k]
		if !ok {
			continue
		}
		m.p("| %s | %d | %d | %d |", k, c.Files, c.Lines, c.NonBlank)
		m.one("loc/"+k, "lines", float64(c.Lines))
	}
	m.p("")
}

func reportGoBench(m *md, dir string) {
	var r goBenchResult
	if readJSON(filepath.Join(dir, "gobench.json"), &r) != nil {
		return
	}
	m.p("## Go micro-benchmarks")
	m.p("")
	m.p("`go test -run '^$' -bench . -benchmem -count N` on the packages that have them; ns/op is the median of the N runs.")
	m.p("")
	m.p("| Package | Benchmark | ns/op | B/op | allocs/op |")
	m.p("|---|---|---:|---:|---:|")
	for _, b := range r.Benches {
		pkg := strings.TrimPrefix(b.Pkg, "github.com/sobhanaz/bobres-3x-ui-telegram-panel/")
		m.p("| %s | %s | %s | %s | %s |", pkg, strings.TrimPrefix(b.Name, "Benchmark"), med(b.Ns, g), f0(b.BytesOp), f0(b.AllocsOp))
		m.add("gobench/"+pkg+"/"+b.Name+"/ns_op", "ns", b.Ns)
		m.one("gobench/"+pkg+"/"+b.Name+"/allocs_op", "", b.AllocsOp)
	}
	m.p("")
}
