package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testdb"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testenv"
)

func TestMain(m *testing.M) { testdb.Main(m) }

var (
	userCounts = flag.String("users", "", "user counts to run against an in-process stack, e.g. 50,200,500 (none: the test skips)")
	shared     = flags(flag.CommandLine)
)

const (
	localOwner = 777
	// drainLimit bounds the wait for the provisioning backlog after a run.
	drainLimit = 10 * time.Minute
)

// TestLocalStack is the load test against the whole backend in-process
// (internal/testenv), one fresh stack for each user count. It runs only when
// asked, and without -race, whose overhead would be measured instead:
//
//	go test ./tools/loadtest -run TestLocalStack -count=1 -timeout 60m -v -args -users 50,200,500 -duration 30s
func TestLocalStack(t *testing.T) {
	if *userCounts == "" {
		t.Skip("load test: pass -args -users 50,200,500 to run it (docs/12-load-test.md)")
	}
	var counts []int
	for f := range strings.SplitSeq(*userCounts, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(f))
		if err != nil || n < 1 {
			t.Fatalf("-users %q: want user counts such as 50,200,500", *userCounts)
		}
		counts = append(counts, n)
	}
	fmt.Printf("\n## %s/%s, %d CPUs, GOMAXPROCS %d, %s, think %s\n", runtime.GOOS, runtime.GOARCH,
		runtime.NumCPU(), runtime.GOMAXPROCS(0), runtime.Version(), shared.Think)
	for _, n := range counts {
		t.Run(fmt.Sprintf("users=%d", n), func(t *testing.T) {
			s := testenv.Start(t, localOwner)
			db := s.Store.DB()
			var version string
			if err := db.QueryRow(context.Background(), `SHOW server_version`).Scan(&version); err != nil {
				t.Fatal(err)
			}
			cfg := *shared
			cfg.Users, cfg.Owner, cfg.TGBase = n, localOwner, 1_000_000
			res, err := load(context.Background(), s.Core, cfg)
			if err != nil {
				t.Fatal(err)
			}
			res.Facts = map[string]any{}
			res.Notes = append(res.Notes, afterLoad(db, func() string { return provisioning(t, db, res.Elapsed, res.Facts) }, res.Facts)...)
			res.Notes = append(res.Notes, "Postgres "+strings.Fields(version)[0], dbSize(t, db, res.Facts))
			res.Facts["postgres"] = strings.Fields(version)[0]
			writeReport(os.Stdout, res)
			if err := writeJSON(cfg.JSON, res, len(counts) > 1); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// afterLoad reports on core once the load has stopped: how deliveries kept
// up (drain waits for the backlog to clear, for minutes after a stress run)
// and core's pool. The pool is read before the drain, so its waits are the
// load's: the drain's polls and deliveries find idle connections and would
// dilute them.
func afterLoad(db *pgxpool.Pool, drain func() string, facts map[string]any) []string {
	st := db.Stat()
	return []string{drain(), pool(st, facts)}
}

// provisioning says how far core's provision worker kept up: the orders it
// delivered while the load ran, the backlog left when it stopped and how
// long that took to clear. The worker delivers one order at a time.
func provisioning(t *testing.T, db *pgxpool.Pool, elapsed time.Duration, facts map[string]any) string {
	t.Helper()
	count := func() (active, waiting, failed int) {
		err := db.QueryRow(context.Background(), `SELECT count(*) FILTER (WHERE status = 'active'),
			count(*) FILTER (WHERE status IN ('paid', 'provisioning')),
			count(*) FILTER (WHERE status = 'provision_failed') FROM core.orders`).Scan(&active, &waiting, &failed)
		if err != nil {
			t.Fatal(err)
		}
		return active, waiting, failed
	}
	active, backlog, _ := count()
	begin := time.Now()
	waiting, failed := backlog, 0
	for waiting > 0 && time.Since(begin) < drainLimit {
		time.Sleep(100 * time.Millisecond)
		_, waiting, failed = count()
	}
	drain := time.Since(begin)
	note := fmt.Sprintf("provisioning: %d orders delivered during the run (%.1f/s), %d waiting when it ended",
		active, float64(active)/elapsed.Seconds(), backlog)
	switch {
	case waiting > 0:
		note += fmt.Sprintf(", %d still waiting after %s", waiting, drainLimit)
	case backlog > 0:
		note += fmt.Sprintf(", delivered in %.1fs more (%.1f/s)", drain.Seconds(), float64(backlog)/drain.Seconds())
	}
	if failed > 0 {
		note += fmt.Sprintf(", %d failed", failed)
	}
	facts["delivered_during_run"] = active
	facts["delivered_per_s_during_run"] = float64(active) / elapsed.Seconds()
	facts["backlog_at_end"] = backlog
	facts["backlog_still_waiting"] = waiting
	facts["backlog_drain_s"] = drain.Seconds()
	if backlog > 0 && waiting == 0 {
		facts["backlog_drain_per_s"] = float64(backlog) / drain.Seconds()
	}
	facts["provision_failed"] = failed
	return note
}

// dbSize says how big the stack's database is after the run and its drain,
// and which tables are the largest.
func dbSize(t *testing.T, db *pgxpool.Pool, facts map[string]any) string {
	t.Helper()
	ctx := context.Background()
	var size int64
	if err := db.QueryRow(ctx, `SELECT pg_database_size(current_database())`).Scan(&size); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query(ctx, `SELECT n.nspname || '.' || c.relname, pg_total_relation_size(c.oid)
		FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE c.relkind = 'r' AND n.nspname IN ('core', 'payments', 'provisioner')
		ORDER BY 2 DESC LIMIT 5`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	top := map[string]int64{}
	var names []string
	for rows.Next() {
		var name string
		var n int64
		if err := rows.Scan(&name, &n); err != nil {
			t.Fatal(err)
		}
		top[name] = n
		names = append(names, fmt.Sprintf("%s %.1f MB", name, float64(n)/1e6))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	var users int64
	if err := db.QueryRow(ctx, `SELECT count(*) FROM core.users`).Scan(&users); err != nil {
		t.Fatal(err)
	}
	facts["db_bytes"], facts["db_top_tables"], facts["db_users"] = size, top, users
	return fmt.Sprintf("database: %.1f MB for %d users (largest: %s)", float64(size)/1e6, users, strings.Join(names, ", "))
}

// pool says how often a core query had to wait for a free connection.
func pool(s *pgxpool.Stat, facts map[string]any) string {
	note := fmt.Sprintf("core's DB pool: at most %d connections, %d acquires, %d waited", s.MaxConns(), s.AcquireCount(), s.EmptyAcquireCount())
	facts["pool_max_conns"], facts["pool_acquires"], facts["pool_waited"] = s.MaxConns(), s.AcquireCount(), s.EmptyAcquireCount()
	if n := s.EmptyAcquireCount(); n > 0 {
		facts["pool_waited_share"] = float64(n) / float64(s.AcquireCount())
		facts["pool_avg_wait_ms"] = float64((s.EmptyAcquireWaitTime() / time.Duration(n)).Microseconds()) / 1000
		note += fmt.Sprintf(" (%.0f%%, %s on average)", 100*float64(n)/float64(s.AcquireCount()),
			(s.EmptyAcquireWaitTime() / time.Duration(n)).Round(10*time.Microsecond))
	}
	return note
}

// The drain polls the database for minutes after a stress run. Reading the
// pool after it counted those quiet acquires with the load's and made the
// share that waited look smaller.
func TestPoolIsReadBeforeTheDrain(t *testing.T) {
	db, err := pgxpool.New(context.Background(), testdb.DSN(t))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	query := func(n int) {
		for range n {
			if _, err := db.Exec(context.Background(), `SELECT 1`); err != nil {
				t.Fatal(err)
			}
		}
	}
	query(3) // the load
	notes := afterLoad(db, func() string { query(5); return "drained" }, map[string]any{})
	if len(notes) != 2 || notes[0] != "drained" || !strings.Contains(notes[1], ", 3 acquires,") {
		t.Fatalf("notes = %q, want the drain's note, then the pool with the load's 3 acquires only", notes)
	}
}
