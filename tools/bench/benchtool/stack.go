package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tgfake"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/xuifake"
)

// The owner's Telegram id on the bench stack (BOBRES_ADMIN_TELEGRAM_ID).
const benchOwner = 777

const defaultBaseDSN = "postgres:///postgres?host=/tmp&port=5432&sslmode=disable"

// scenario is one load test run against the stack.
type scenario struct {
	Name     string        `json:"name"`
	Users    int           `json:"users"`
	Duration time.Duration `json:"-"`
	Think    time.Duration `json:"-"`
}

// parseScenarios reads "stress:200:30s,paced:500:60s:2s".
func parseScenarios(s string) ([]scenario, error) {
	var out []scenario
	for _, f := range strings.Split(s, ",") {
		p := strings.Split(strings.TrimSpace(f), ":")
		if len(p) < 3 {
			return nil, fmt.Errorf("scenario %q: want name:users:duration[:think]", f)
		}
		n, err := strconv.Atoi(p[1])
		if err != nil {
			return nil, fmt.Errorf("scenario %q: %w", f, err)
		}
		d, err := time.ParseDuration(p[2])
		if err != nil {
			return nil, fmt.Errorf("scenario %q: %w", f, err)
		}
		sc := scenario{Name: p[0], Users: n, Duration: d}
		if len(p) > 3 {
			if sc.Think, err = time.ParseDuration(p[3]); err != nil {
				return nil, fmt.Errorf("scenario %q: %w", f, err)
			}
		}
		out = append(out, sc)
	}
	return out, nil
}

// svcResult is what one service did on the stack.
type svcResult struct {
	FirstExecS float64               `json:"first_exec_s"` // the binary's first run (--version): macOS scans a new executable
	ColdReadyS float64               `json:"cold_ready_s"` // first start: an empty database, its migrations
	WarmReadyS float64               `json:"warm_ready_s"` // restart on the loaded database
	IdleRSSMB  float64               `json:"idle_rss_mb"`  // median while idle after the first start
	IdleCPUPct float64               `json:"idle_cpu_pct"` // % of one core while idle
	Phases     map[string]phaseUsage `json:"phases,omitempty"`
	FirstStop  stopped               `json:"first_stop"` // after the load: its rusage covers start, idle and load
	Stop       stopped               `json:"stop"`       // after the warm start
}

// phaseUsage is a service's RSS and CPU while a load phase (or its drain) ran.
type phaseUsage struct {
	Seconds   float64 `json:"seconds"`
	CPUS      float64 `json:"cpu_s"`
	CPUCores  float64 `json:"cpu_cores"` // cpu_s / seconds: 1.0 is one core busy
	RSSMaxMB  float64 `json:"rss_max_mb"`
	RSSMeanMB float64 `json:"rss_mean_mb"`
}

type scenarioResult struct {
	scenario
	DurationS    float64        `json:"duration_s"`
	ThinkS       float64        `json:"think_s"`
	LoadBefore   []float64      `json:"load_before"`
	LoadAfter    []float64      `json:"load_after"`
	Loadtest     map[string]any `json:"loadtest"`
	BacklogEnd   int            `json:"backlog_at_end"`
	DrainS       float64        `json:"drain_s"`
	StillWaiting int            `json:"still_waiting"`
}

type stackResult struct {
	Kind      string                `json:"kind"`
	Variant   string                `json:"variant"`
	BinDir    string                `json:"bin_dir"`
	At        string                `json:"at"`
	Order     []string              `json:"order"`
	Services  map[string]*svcResult `json:"services"`
	Scenarios []scenarioResult      `json:"scenarios"`
	DB        map[string]any        `json:"db"`
	Load      []float64             `json:"load_before"`
}

// stack runs the real service binaries as separate processes on one fresh
// database, with a fake 3x-ui panel and a fake Telegram API, measures each
// one's start, idle RSS and CPU, then drives core with the load test binary
// scenario after scenario (as on a live server, the database keeps what
// the earlier ones wrote) and measures RSS and CPU per service while each
// runs and while core's deliveries catch up. At the end each service is
// restarted once on the loaded database (the warm start) and stopped.
func stack(args []string) error {
	fs := flag.NewFlagSet("stack", flag.ContinueOnError)
	binDir := fs.String("bin-dir", "", "the variant's service binaries for this machine (core, payments, provisioner, bot, backup)")
	ltBin := fs.String("loadtest", "", "the load test binary (go build ./tools/loadtest), the same for every variant")
	out := fs.String("out", "", "JSON result")
	work := fs.String("work", "", "scratch directory for logs and the services' files")
	base := fs.String("db", envOr("BOBRES_TEST_DATABASE_URL", defaultBaseDSN), "a postgres:// URL of a role that may CREATE DATABASE")
	redis := fs.String("redis", "127.0.0.1:6391", "a throwaway Redis for the bot (its database 0 is flushed)")
	scen := fs.String("scenarios", "stress:200:30s,paced:500:60s:2s", "name:users:duration[:think], comma separated")
	idle := fs.Duration("idle", 20*time.Second, "how long the idle stack is measured")
	drainLimit := fs.Duration("drain-limit", 10*time.Minute, "longest wait for the delivery backlog after a scenario")
	variant := fs.String("variant", "plain", "the build variant's name")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *binDir == "" || *ltBin == "" || *out == "" || *work == "" {
		return errors.New("usage: stack -bin-dir D -loadtest BIN -work DIR -out F.json [-scenarios ...]")
	}
	scenarios, err := parseScenarios(*scen)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(*work, 0o700); err != nil {
		return err
	}
	ctx := context.Background()
	dsn, dropDB, err := benchDB(ctx, *base)
	if err != nil {
		return err
	}
	defer dropDB()
	if err := flushRedis(*redis); err != nil {
		return fmt.Errorf("redis %s: %w (start a throwaway one, see tools/bench/run.sh)", *redis, err)
	}

	panelToken := token(24)
	panel := xuifake.NewServer(panelToken)
	defer panel.Close()
	tgs := httptest.NewServer(tgfake.New().Handler())
	defer tgs.Close()

	procs, err := services(*binDir, *work, dsn, *redis, panel.URL, panelToken, tgs.URL)
	if err != nil {
		return err
	}
	res := stackResult{Kind: "stack", Variant: *variant, BinDir: *binDir, At: time.Now().UTC().Format(time.RFC3339),
		Services: map[string]*svcResult{}, Load: loadAvg()}
	for _, p := range procs {
		res.Order = append(res.Order, p.Name)
		res.Services[p.Name] = &svcResult{Phases: map[string]phaseUsage{}}
	}
	var running []*proc
	defer func() { // a failure halfway leaves nothing running
		for _, p := range running {
			select {
			case <-p.done:
			default:
				_ = p.cmd.Process.Kill()
			}
		}
	}()

	// 0. Each binary's first exec (`--version`, no config, no database): on
	// macOS the first run of a new executable waits for its security scan,
	// which is not the service's own start, so it is measured apart.
	firstExec := map[string]float64{}
	for _, p := range procs {
		if _, done := firstExec[p.Bin]; done {
			continue
		}
		t := time.Now()
		if out, err := exec.Command(p.Bin, "--version").CombinedOutput(); err != nil { //nolint:gosec // our binaries
			return fmt.Errorf("%s --version: %w: %s", p.Bin, err, out)
		}
		firstExec[p.Bin] = time.Since(t).Seconds()
		res.Services[p.Name].FirstExecS = firstExec[p.Bin]
	}

	// 1. Cold start, one service after the other.
	for _, p := range procs {
		if err := p.start(2 * time.Minute); err != nil {
			return err
		}
		running = append(running, p)
		res.Services[p.Name].ColdReadyS = p.ready.Seconds()
		fmt.Fprintf(os.Stderr, "  %-12s ready in %6.3fs (cold)\n", p.Name, p.ready.Seconds())
	}

	// 2. Idle.
	time.Sleep(5 * time.Second)
	cpu0 := cpuNow(procs)
	smp := startSampler(procs, time.Second)
	time.Sleep(*idle)
	rss := smp.finish()
	cpu1 := cpuNow(procs)
	for _, p := range procs {
		r := res.Services[p.Name]
		r.IdleRSSMB = summarize(rss[p.Name]).Median
		r.IdleCPUPct = 100 * (cpu1[p.Name] - cpu0[p.Name]) / idle.Seconds()
	}

	// 3. Load, scenario after scenario, each followed by the delivery drain.
	db, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return err
	}
	defer db.Close(ctx) //nolint:errcheck // end of the run
	core := procByName(procs, "core")
	for i, sc := range scenarios {
		sr := scenarioResult{scenario: sc, DurationS: sc.Duration.Seconds(), ThinkS: sc.Think.Seconds(), LoadBefore: loadAvg()}
		jsonPath := filepath.Join(*work, fmt.Sprintf("loadtest-%d-%s.json", i, sc.Name))
		ltArgs := []string{"-addr", core.Env["BOBRES_GRPC_ADDR"], "-owner", strconv.Itoa(benchOwner),
			"-users", strconv.Itoa(sc.Users), "-duration", sc.Duration.String(), "-json", jsonPath}
		if sc.Think > 0 {
			ltArgs = append(ltArgs, "-think", sc.Think.String())
		}
		lt := exec.Command(*ltBin, ltArgs...) //nolint:gosec // our own load test binary
		lt.Env = append(os.Environ(), "BOBRES_LOADTEST_TOKEN="+core.Env["BOBRES_PEER_BOT_TOKEN"])
		ltLog, err := os.Create(filepath.Join(*work, fmt.Sprintf("loadtest-%d-%s.log", i, sc.Name)))
		if err != nil {
			return err
		}
		lt.Stdout, lt.Stderr = ltLog, ltLog
		fmt.Fprintf(os.Stderr, "  load %-8s %d users, %s, think %s, load %v\n", sc.Name, sc.Users, sc.Duration, sc.Think, sr.LoadBefore)
		c0, t0 := cpuNow(procs), time.Now()
		smp := startSampler(procs, 500*time.Millisecond)
		err = lt.Run()
		_ = ltLog.Close()
		loadRSS, c1, t1 := smp.finish(), cpuNow(procs), time.Now()
		if err != nil {
			return fmt.Errorf("load test %s: %w (see %s)", sc.Name, err, ltLog.Name())
		}
		sr.LoadAfter = loadAvg()
		if err := readJSON(jsonPath, &sr.Loadtest); err != nil {
			return err
		}
		// The drain: core delivers what was paid while the load ran.
		sr.BacklogEnd, err = backlog(ctx, db)
		if err != nil {
			return err
		}
		smp = startSampler(procs, time.Second)
		dctx, cancel := context.WithTimeout(ctx, *drainLimit)
		_ = waitFor(dctx, 100*time.Millisecond, func() (bool, error) {
			n, err := backlog(ctx, db)
			sr.StillWaiting = n
			return n == 0, err
		})
		cancel()
		drainRSS, c2, t2 := smp.finish(), cpuNow(procs), time.Now()
		sr.DrainS = t2.Sub(t1).Seconds()
		fmt.Fprintf(os.Stderr, "    %.1f journeys/s, %d waiting at the end, drained in %.1fs\n",
			num(sr.Loadtest["journeys_per_s"]), sr.BacklogEnd, sr.DrainS)
		for _, p := range procs {
			r := res.Services[p.Name]
			r.Phases[sc.Name] = phase(t1.Sub(t0), c1[p.Name]-c0[p.Name], loadRSS[p.Name])
			r.Phases[sc.Name+"-drain"] = phase(t2.Sub(t1), c2[p.Name]-c1[p.Name], drainRSS[p.Name])
		}
		res.Scenarios = append(res.Scenarios, sr)
	}
	if res.DB, err = dbFacts(ctx, db); err != nil {
		return err
	}

	// 4. The warm start: each service again, on the loaded database.
	for _, p := range procs {
		res.Services[p.Name].FirstStop = p.stop()
	}
	running = nil
	for _, p := range procs {
		if err := p.start(2 * time.Minute); err != nil {
			return err
		}
		running = append(running, p)
		res.Services[p.Name].WarmReadyS = p.ready.Seconds()
		fmt.Fprintf(os.Stderr, "  %-12s ready in %6.3fs (warm)\n", p.Name, p.ready.Seconds())
	}
	time.Sleep(2 * time.Second)
	for i := len(procs) - 1; i >= 0; i-- {
		res.Services[procs[i].Name].Stop = procs[i].stop()
	}
	running = nil
	return writeJSON(*out, res)
}

func phase(d time.Duration, cpu float64, rss []float64) phaseUsage {
	u := phaseUsage{Seconds: d.Seconds(), CPUS: cpu}
	if d > 0 {
		u.CPUCores = cpu / d.Seconds()
	}
	if len(rss) > 0 {
		var sum float64
		for _, v := range rss {
			sum += v
			u.RSSMaxMB = max(u.RSSMaxMB, v)
		}
		u.RSSMeanMB = sum / float64(len(rss))
	}
	return u
}

func num(v any) float64 {
	f, _ := v.(float64)
	return f
}

func procByName(ps []*proc, name string) *proc {
	for _, p := range ps {
		if p.Name == name {
			return p
		}
	}
	return nil
}

// services is the stack: payments, provisioner, core, bot, backup and the
// tls-agent (core's binary), each on its own loopback ports, with a token
// of its own for each pair as docker-compose.yml wires them.
func services(binDir, work, dsn, redis, panelURL, panelToken, tgURL string) ([]*proc, error) {
	ports, err := freePorts(9)
	if err != nil {
		return nil, err
	}
	payHTTP, payGRPC, provHTTP, provGRPC, coreHTTP, coreGRPC, botHTTP, backupHTTP := ports[0], ports[1], ports[2], ports[3], ports[4], ports[5], ports[6], ports[7]
	tCore, tCoreProv, tCoreBot, tBot, tBackup := token(24), token(24), token(24), token(24), token(24)
	common := func(httpAddr, grpcAddr string) map[string]string {
		m := map[string]string{"BOBRES_ENV": "prod", "BOBRES_LOG_LEVEL": "info", "BOBRES_LOG_FORMAT": "json",
			"BOBRES_HTTP_ADDR": httpAddr, "BOBRES_TIMEZONE": "Asia/Tehran", "BOBRES_SHUTDOWN_WAIT": "15s"}
		if grpcAddr != "" {
			m["BOBRES_GRPC_ADDR"] = grpcAddr
		}
		return m
	}
	with := func(m map[string]string, kv ...string) map[string]string {
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return m
	}
	tlsDir := filepath.Join(work, "tls")
	backupDir := filepath.Join(work, "backups")
	for _, d := range []string{tlsDir, backupDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return nil, err
		}
	}
	// Far from now, so the backup service never dumps during a run.
	backupAt := time.Now().Add(12 * time.Hour).Format("15:04")
	bin := func(name string) string { return filepath.Join(binDir, name) }
	logf := func(name string) string { return filepath.Join(work, name+".log") }
	ps := []*proc{
		{Name: "payments", Bin: bin("payments"), HTTP: payHTTP, Log: logf("payments"), Env: with(common(payHTTP, payGRPC),
			"BOBRES_DATABASE_URL", dsn, "BOBRES_PEER_CORE_TOKEN", tCore, "BOBRES_MASTER_KEY", token(24))},
		{Name: "provisioner", Bin: bin("provisioner"), HTTP: provHTTP, Log: logf("provisioner"), Env: with(common(provHTTP, provGRPC),
			"BOBRES_DATABASE_URL", dsn, "BOBRES_PEER_CORE_TOKEN", tCoreProv, "BOBRES_MASTER_KEY", token(24),
			"BOBRES_XUI_URL", panelURL, "BOBRES_XUI_TOKEN", panelToken, "BOBRES_XUI_SUB_URL", "https://sub.example.test/sub/",
			"BOBRES_XUI_ALLOW_PRIVATE", "true")},
		{Name: "core", Bin: bin("core"), HTTP: coreHTTP, Log: logf("core"), Env: with(common(coreHTTP, coreGRPC),
			"BOBRES_DATABASE_URL", dsn, "BOBRES_SERVICE_TOKEN", tCore, "BOBRES_PROVISIONER_TOKEN", tCoreProv,
			"BOBRES_BOT_INTERNAL_TOKEN", tCoreBot, "BOBRES_PEER_BOT_TOKEN", tBot, "BOBRES_MASTER_KEY", token(24),
			"BOBRES_PAYMENTS_GRPC_ADDR", payGRPC, "BOBRES_PROVISIONER_GRPC_ADDR", provGRPC,
			"BOBRES_ADMIN_TELEGRAM_ID", strconv.Itoa(benchOwner), "BOBRES_BOT_URL", "http://"+botHTTP,
			"BOBRES_PAYMENTS_URL", "http://"+payHTTP, "BOBRES_PROVISIONER_URL", "http://"+provHTTP,
			"BOBRES_PUBLIC_URL", "http://"+coreHTTP,
			"BOBRES_TLS_REQUEST_DIR", filepath.Join(work, "no-tls-agent"), "BOBRES_TLS_RESULT_DIR", filepath.Join(work, "no-tls-agent"))},
		{Name: "bot", Bin: bin("bot"), HTTP: botHTTP, Log: logf("bot"), Env: with(common(botHTTP, ""),
			"BOBRES_REDIS_URL", "redis://"+redis+"/0", "BOBRES_SERVICE_TOKEN", tBot, "BOBRES_CORE_GRPC_ADDR", coreGRPC,
			"BOBRES_TELEGRAM_BOT_TOKEN", "123456789:bench-fake-telegram-token", "BOBRES_TELEGRAM_API_URL", tgURL,
			"BOBRES_PEER_CORE_TOKEN", tCoreBot, "BOBRES_ADMIN_TELEGRAM_ID", strconv.Itoa(benchOwner))},
		{Name: "backup", Bin: bin("backup"), HTTP: backupHTTP, Log: logf("backup"), Env: with(common(backupHTTP, ""),
			"BOBRES_BACKUP_DATABASE_URL", dsn, "BOBRES_BACKUP_KEY", token(24), "BOBRES_BACKUP_DIR", backupDir,
			"BOBRES_BACKUP_TIME", backupAt, "BOBRES_SERVICE_TOKEN", tBackup, "BOBRES_BOT_URL", "http://"+botHTTP)},
		{Name: "tls-agent", Bin: bin("core"), Log: logf("tls-agent"), Beat: filepath.Join(tlsDir, "heartbeat"),
			Args: []string{"tls-agent", "-request", filepath.Join(tlsDir, "request.json"), "-result", filepath.Join(tlsDir, "result.json"),
				"-tls-dir", filepath.Join(tlsDir, "files"), "-caddyfile", filepath.Join(tlsDir, "Caddyfile"),
				"-caddy-source", filepath.Join(tlsDir, "Caddyfile"), "-admin-socket", filepath.Join(tlsDir, "admin.sock"),
				"-heartbeat", filepath.Join(tlsDir, "heartbeat")},
			Env: map[string]string{"BOBRES_DOMAIN": "bench.example.test", "BOBRES_LOG_FORMAT": "json"}},
	}
	for _, p := range ps {
		if _, err := os.Stat(p.Bin); err != nil {
			return nil, fmt.Errorf("%s: %w", p.Name, err)
		}
	}
	return ps, nil
}

func token(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// benchDB creates a throwaway database with the three schemas (as
// deploy/postgres-init.sh does) and returns its DSN and how to drop it.
func benchDB(ctx context.Context, base string) (string, func(), error) {
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		return "", nil, fmt.Errorf("postgres: %w", err)
	}
	name := "bobres_bench_" + token(4)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		_ = admin.Close(ctx)
		return "", nil, err
	}
	u, err := url.Parse(base)
	if err != nil {
		_ = admin.Close(ctx)
		return "", nil, err
	}
	u.Path = "/" + name
	dsn := u.String()
	drop := func() {
		_, _ = admin.Exec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
		_ = admin.Close(context.Background())
	}
	c, err := pgx.Connect(ctx, dsn)
	if err != nil {
		drop()
		return "", nil, err
	}
	defer c.Close(ctx) //nolint:errcheck // setup connection
	if _, err := c.Exec(ctx, `CREATE SCHEMA core; CREATE SCHEMA payments; CREATE SCHEMA provisioner;`); err != nil {
		drop()
		return "", nil, err
	}
	return dsn, drop, nil
}

// backlog is the paid orders core has not delivered yet.
func backlog(ctx context.Context, db *pgx.Conn) (int, error) {
	var n int
	err := db.QueryRow(ctx, `SELECT count(*) FROM core.orders WHERE status IN ('paid', 'provisioning')`).Scan(&n)
	return n, err
}

// dbFacts is the database's size after the load, and its largest tables.
func dbFacts(ctx context.Context, db *pgx.Conn) (map[string]any, error) {
	var size, users, orders int64
	if err := db.QueryRow(ctx, `SELECT pg_database_size(current_database()), (SELECT count(*) FROM core.users),
		(SELECT count(*) FROM core.orders)`).Scan(&size, &users, &orders); err != nil {
		return nil, err
	}
	rows, err := db.Query(ctx, `SELECT n.nspname || '.' || c.relname, pg_total_relation_size(c.oid)
		FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE c.relkind = 'r' AND n.nspname IN ('core', 'payments', 'provisioner') ORDER BY 2 DESC LIMIT 8`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	top := map[string]int64{}
	for rows.Next() {
		var name string
		var n int64
		if err := rows.Scan(&name, &n); err != nil {
			return nil, err
		}
		top[name] = n
	}
	return map[string]any{"bytes": size, "users": users, "orders": orders, "top_tables": top}, rows.Err()
}

// flushRedis empties database 0 of the throwaway Redis, so each run's bot
// starts from nothing.
func flushRedis(addr string) error {
	out, err := exec.Command("redis-cli", "-h", strings.Split(addr, ":")[0], "-p", strings.Split(addr, ":")[1], "-n", "0", "FLUSHDB").CombinedOutput() //nolint:gosec // our throwaway Redis
	if err != nil {
		return fmt.Errorf("%w: %s", err, out)
	}
	if !strings.Contains(string(out), "OK") {
		return errors.New(strings.TrimSpace(string(out)))
	}
	return nil
}
