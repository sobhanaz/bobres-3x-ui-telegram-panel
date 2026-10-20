package store

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
)

// The monitoring time series (docs/04-dashboard.md, migration 00030). A row of
// core.monitor_series is a block of one metric: FullSlots slots of FullStep
// seconds (one hour) at full resolution, or RolledSlots slots of RolledStep
// seconds (one day) once rolled up. A slot nobody sampled is NULL.
const (
	FullStep    = 30
	FullSlots   = 120
	FullSpan    = FullStep * FullSlots // 3600 s
	RolledStep  = 300
	RolledSlots = 288
	RolledSpan  = RolledStep * RolledSlots // 86400 s

	// FullKeep is how long samples stay at full resolution, RolledKeep how long
	// the 5-minute rows stay (counted from the sample, so 30 days of history).
	FullKeep   = 7 * 24 * time.Hour
	RolledKeep = 30 * 24 * time.Hour

	// MaxMetrics bounds the number of distinct metrics, so a hostile or
	// buggy source cannot grow the table without limit.
	MaxMetrics = 400
)

// MonitorSample is one reading of a metric.
type MonitorSample struct {
	Name  string
	Value float64
}

// MonitorPoint is one point of a series: unix seconds and the value.
type MonitorPoint struct {
	T int64
	V float64
}

func floorTo(t, span int64) int64 { return t - ((t%span)+span)%span }

// RecordSamples writes samples taken at at, each into its 30-second slot.
// A name seen for the first time is registered (up to MaxMetrics names;
// further new names are dropped). Writing the same slot again overwrites it,
// so a restart in the middle of a slot changes nothing. NaN and infinite
// values are skipped (a missing value is no value, never a zero). It
// returns how many samples were written.
func (s *Store) RecordSamples(ctx context.Context, at time.Time, samples []MonitorSample) (int, error) {
	t := at.Unix()
	bucket := floorTo(t, FullSpan)
	slot := int((t-bucket)/FullStep) + 1 // 1-based
	wrote := 0
	err := s.WithTx(ctx, func(tx pgx.Tx) error {
		var known int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM core.monitor_metric`).Scan(&known); err != nil {
			return fmt.Errorf("count metrics: %w", err)
		}
		for _, sm := range samples {
			if math.IsNaN(sm.Value) || math.IsInf(sm.Value, 0) || sm.Name == "" || len(sm.Name) > 120 {
				continue
			}
			var id int
			err := tx.QueryRow(ctx, `SELECT id FROM core.monitor_metric WHERE name = $1`, sm.Name).Scan(&id)
			if err == pgx.ErrNoRows {
				if known >= MaxMetrics {
					continue
				}
				if err = tx.QueryRow(ctx, `INSERT INTO core.monitor_metric (name) VALUES ($1)
					ON CONFLICT (name) DO UPDATE SET name = EXCLUDED.name RETURNING id`, sm.Name).Scan(&id); err != nil {
					return fmt.Errorf("register metric %s: %w", sm.Name, err)
				}
				known++
			} else if err != nil {
				return fmt.Errorf("find metric %s: %w", sm.Name, err)
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO core.monitor_series (metric_id, step, bucket, vals)
				VALUES ($1, $2, $3, array_fill(NULL::real, ARRAY[$4::int]))
				ON CONFLICT (metric_id, step, bucket) DO NOTHING`, id, FullStep, bucket, FullSlots); err != nil {
				return fmt.Errorf("open block %s: %w", sm.Name, err)
			}
			if _, err := tx.Exec(ctx, `UPDATE core.monitor_series SET vals[$4] = $5::real
				WHERE metric_id = $1 AND step = $2 AND bucket = $3`, id, FullStep, bucket, slot, float32(sm.Value)); err != nil {
				return fmt.Errorf("write slot %s: %w", sm.Name, err)
			}
			wrote++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return wrote, nil
}

// MonitorMetrics lists the metric names that have any block, sorted.
func (s *Store) MonitorMetrics(ctx context.Context, q querier) ([]string, error) {
	rows, err := q.Query(ctx, `SELECT name FROM core.monitor_metric ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("monitor metrics: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// MonitorSeries reads the points of a metric in [from, to], oldest first,
// from both resolutions (the 5-minute points cover only what is older than
// the full-resolution ones). Empty slots give no point. A metric nobody
// recorded gives nil.
func (s *Store) MonitorSeries(ctx context.Context, q querier, metric string, from, to time.Time) ([]MonitorPoint, error) {
	f, t := from.Unix(), to.Unix()
	rows, err := q.Query(ctx, `
		SELECT ms.step, ms.bucket, ms.vals
		FROM core.monitor_series ms JOIN core.monitor_metric m ON m.id = ms.metric_id
		WHERE m.name = $1 AND ((ms.step = $2 AND ms.bucket + $3 > $5 AND ms.bucket <= $6)
			OR (ms.step = $4 AND ms.bucket + $7 > $5 AND ms.bucket <= $6))
		ORDER BY ms.bucket, ms.step`,
		metric, FullStep, FullSpan, RolledStep, f, t, RolledSpan)
	if err != nil {
		return nil, fmt.Errorf("monitor series: %w", err)
	}
	defer rows.Close()
	var out []MonitorPoint
	for rows.Next() {
		var (
			step   int16
			bucket int64
			vals   []*float32
		)
		if err := rows.Scan(&step, &bucket, &vals); err != nil {
			return nil, err
		}
		for i, v := range vals {
			if v == nil {
				continue
			}
			at := bucket + int64(i)*int64(step)
			if at < f || at > t {
				continue
			}
			out = append(out, MonitorPoint{T: at, V: float64(*v)})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sortPoints(out)
	return out, nil
}

// sortPoints keeps the order guaranteed even if the two resolutions overlap
// (a restored copy of the database can leave a day in both).
func sortPoints(p []MonitorPoint) {
	sort.SliceStable(p, func(i, j int) bool { return p[i].T < p[j].T })
}

// RollupMonitor rolls whole days of full-resolution blocks older than
// FullKeep into 5-minute blocks: per metric and day, the average of the
// samples in each 5-minute slot, written and the full blocks dropped in one
// transaction (so the two resolutions never overlap and a crash loses
// nothing). It rolls at most maxDays (metric, day) groups per call and returns
// how many it did; call again while it returns maxDays.
func (s *Store) RollupMonitor(ctx context.Context, now time.Time, maxDays int) (int, error) {
	cutoff := floorTo(now.Add(-FullKeep).Unix(), RolledSpan)
	rows, err := s.db.Query(ctx, `
		SELECT DISTINCT metric_id, bucket - (bucket % $2) AS day
		FROM core.monitor_series WHERE step = $3 AND bucket < $1
		ORDER BY day, metric_id LIMIT $4`, cutoff, RolledSpan, FullStep, maxDays)
	if err != nil {
		return 0, fmt.Errorf("rollup list: %w", err)
	}
	type group struct {
		metric int
		day    int64
	}
	var groups []group
	for rows.Next() {
		var g group
		if err := rows.Scan(&g.metric, &g.day); err != nil {
			rows.Close()
			return 0, err
		}
		groups = append(groups, g)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, g := range groups {
		if err := s.rollGroup(ctx, g.metric, g.day); err != nil {
			return 0, err
		}
	}
	return len(groups), nil
}

func (s *Store) rollGroup(ctx context.Context, metric int, day int64) error {
	return s.WithTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT bucket, vals FROM core.monitor_series
			WHERE metric_id = $1 AND step = $2 AND bucket >= $3 AND bucket < $4 FOR UPDATE`,
			metric, FullStep, day, day+RolledSpan)
		if err != nil {
			return fmt.Errorf("roll read: %w", err)
		}
		var sum [RolledSlots]float64
		var n [RolledSlots]int
		for rows.Next() {
			var (
				bucket int64
				vals   []*float32
			)
			if err := rows.Scan(&bucket, &vals); err != nil {
				rows.Close()
				return err
			}
			for i, v := range vals {
				if v == nil {
					continue
				}
				slot := int((bucket - day + int64(i)*FullStep) / RolledStep)
				if slot < 0 || slot >= RolledSlots {
					continue
				}
				sum[slot] += float64(*v)
				n[slot]++
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		out := make([]*float32, RolledSlots)
		any := false
		for i := range out {
			if n[i] > 0 {
				v := float32(sum[i] / float64(n[i]))
				out[i] = &v
				any = true
			}
		}
		if any {
			// A day rolled before (a crash cannot leave one, but a restored
			// copy might) keeps its values where this run has none.
			if _, err := tx.Exec(ctx, `
				INSERT INTO core.monitor_series (metric_id, step, bucket, vals) VALUES ($1, $2, $3, $4)
				ON CONFLICT (metric_id, step, bucket) DO UPDATE SET vals = EXCLUDED.vals`,
				metric, RolledStep, day, out); err != nil {
				return fmt.Errorf("roll write: %w", err)
			}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM core.monitor_series
			WHERE metric_id = $1 AND step = $2 AND bucket >= $3 AND bucket < $4`,
			metric, FullStep, day, day+RolledSpan); err != nil {
			return fmt.Errorf("roll drop: %w", err)
		}
		return nil
	})
}

// PruneMonitor drops the 5-minute blocks whose last slot is older than
// RolledKeep and the metrics left with no block at all. It returns how many
// blocks went.
func (s *Store) PruneMonitor(ctx context.Context, now time.Time) (int64, error) {
	old := now.Add(-RolledKeep).Unix()
	tag, err := s.db.Exec(ctx, `DELETE FROM core.monitor_series WHERE step = $1 AND bucket + $2 <= $3`,
		RolledStep, RolledSpan, old)
	if err != nil {
		return 0, fmt.Errorf("prune monitor: %w", err)
	}
	// Full-resolution blocks past their keep that a rollup did not take
	// (it ran out of time) wait for the rollup; they are never dropped unrolled.
	// A metric whose last block went is forgotten (a removed service).
	if _, err := s.db.Exec(ctx, `DELETE FROM core.monitor_metric m
		WHERE NOT EXISTS (SELECT 1 FROM core.monitor_series s WHERE s.metric_id = m.id)`); err != nil {
		return tag.RowsAffected(), fmt.Errorf("prune metrics: %w", err)
	}
	return tag.RowsAffected(), nil
}

// MonitorCounters are the figures the Monitoring page and the sampler read
// from the database in one query.
type MonitorCounters struct {
	ProvisionQueue int64 // orders paid and waiting for the panel, or being delivered
	OutboxBacklog  int64 // core events the slowest consumer has not read
	OrdersPending  int64 // orders created or awaiting payment
	TicketsOpen    int64
	ServicesActive int64
	CustomersTotal int64
	DBBytes        int64
}

// MonitorCounters reads them.
func (s *Store) MonitorCounters(ctx context.Context, q querier) (*MonitorCounters, error) {
	var c MonitorCounters
	err := q.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM core.orders WHERE status IN ('paid', 'provisioning')),
		(SELECT count(*) FROM core.outbox_core WHERE id > (SELECT min(last_id) FROM core.outbox_core_cursors)),
		(SELECT count(*) FROM core.orders WHERE status IN ('created', 'awaiting_payment')),
		(SELECT count(*) FROM core.tickets WHERE status = 'open'),
		(SELECT count(*) FROM core.subscriptions WHERE status IN ('active', 'expiring_soon')),
		(SELECT count(*) FROM core.users),
		pg_database_size(current_database())`).
		Scan(&c.ProvisionQueue, &c.OutboxBacklog, &c.OrdersPending, &c.TicketsOpen, &c.ServicesActive, &c.CustomersTotal, &c.DBBytes)
	if err != nil {
		return nil, fmt.Errorf("monitor counters: %w", err)
	}
	return &c, nil
}

// PoolStats are the connection counts of the pool.
func (s *Store) PoolStats() (inUse, total, limit int32) {
	st := s.db.Stat()
	return st.AcquiredConns(), st.TotalConns(), st.MaxConns()
}

// AlertEventRow is one watchdog alert event (system.alert.v1) as the outbox keeps it.
type AlertEventRow struct {
	ID        int64
	CreatedAt time.Time
	Payload   []byte
}

// RecentAlertEvents reads the newest alert events, newest first (the outbox is kept for ever; the
// partial index of migration 00030 keeps this a short walk). The topic is written out in the query so
// that the index applies whatever plan the server picks.
func (s *Store) RecentAlertEvents(ctx context.Context, q querier, limit int) ([]AlertEventRow, error) {
	rows, err := q.Query(ctx, `SELECT id, created_at, payload FROM core.outbox_core
		WHERE topic = 'system.alert.v1' ORDER BY id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("recent alert events: %w", err)
	}
	defer rows.Close()
	var out []AlertEventRow
	for rows.Next() {
		var r AlertEventRow
		if err := rows.Scan(&r.ID, &r.CreatedAt, &r.Payload); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
