package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// The daily sales summary's claims (docs/14-reports.md, migration 00019): one
// row for each store-local day that was summed up and sent.

// ClaimDailySummary takes the claim on the summary of day, the store-local date
// ("2026-10-13") read in zone, for recipients staff members. The caller does it
// in the transaction that writes the outbox events carrying the summary, so the
// claim and the events commit together or not at all. It reports false when the
// day is already claimed (by an earlier run or by a claimer that committed
// first): the caller then writes nothing. A claimer that finds the row taken by
// a transaction still open waits for it, and gets the claim after all when that
// one rolls back.
func (s *Store) ClaimDailySummary(ctx context.Context, tx pgx.Tx, day, zone string, recipients int) (bool, error) {
	tag, err := tx.Exec(ctx, `
		INSERT INTO core.daily_summary_claims (day, zone, recipients) VALUES ($1::date, $2, $3)
		ON CONFLICT (day) DO NOTHING`, day, zone, recipients)
	if err != nil {
		return false, fmt.Errorf("claim daily summary %s: %w", day, err)
	}
	return tag.RowsAffected() == 1, nil
}

// DailySummaryClaimed reports whether the summary of day ("2026-10-13") was
// claimed already.
func (s *Store) DailySummaryClaimed(ctx context.Context, q querier, day string) (bool, error) {
	var claimed bool
	if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM core.daily_summary_claims WHERE day = $1::date)`, day).Scan(&claimed); err != nil {
		return false, fmt.Errorf("daily summary claimed %s: %w", day, err)
	}
	return claimed, nil
}

// LastDailySummaryZone is the time zone the newest claimed summary was read in
// (ok=false: no summary was claimed yet). The worker compares it with the zone
// it is about to read a day in: when the store changed its zone the day edges
// moved, and a sale can fall in two summaries or in none.
func (s *Store) LastDailySummaryZone(ctx context.Context, q querier) (zone string, ok bool, err error) {
	err = q.QueryRow(ctx, `SELECT zone FROM core.daily_summary_claims ORDER BY day DESC LIMIT 1`).Scan(&zone)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("last daily summary zone: %w", err)
	}
	return zone, true, nil
}

// SettingChangedAt is when a setting last got its value (the time of the
// change that wrote it); ok=false when the setting has no row.
func (s *Store) SettingChangedAt(ctx context.Context, q querier, key string) (at time.Time, ok bool, err error) {
	err = q.QueryRow(ctx, `SELECT updated_at FROM core.settings WHERE key = $1`, key).Scan(&at)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("setting changed at %s: %w", key, err)
	}
	return at, true, nil
}
