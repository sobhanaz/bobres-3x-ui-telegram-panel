package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	buuid "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
)

const planCols = `id, name_i18n, kind, duration_days, traffic_bytes, price, currency, enabled, is_trial, sort, is_topup`

func scanPlan(row pgx.Row) (*Plan, error) {
	var (
		p    Plan
		name []byte
	)
	err := row.Scan(&p.ID, &name, &p.Kind, &p.DurationDays, &p.TrafficBytes,
		&p.Price, &p.Currency, &p.Enabled, &p.IsTrial, &p.Sort, &p.IsTopup)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(name, &p.NameI18n); err != nil {
		return nil, fmt.Errorf("plan name_i18n: %w", err)
	}
	return &p, nil
}

// ListPlans returns enabled plans (or all when includeDisabled), sorted.
func (s *Store) ListPlans(ctx context.Context, q querier, includeDisabled bool) ([]Plan, error) {
	where := ""
	if !includeDisabled {
		where = "WHERE enabled"
	}
	rows, err := q.Query(ctx,
		`SELECT `+planCols+` FROM core.plans `+where+` ORDER BY sort, price`)
	if err != nil {
		return nil, fmt.Errorf("list plans: %w", err)
	}
	defer rows.Close()
	var out []Plan
	for rows.Next() {
		p, err := scanPlan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// GetPlan returns one plan by id.
func (s *Store) GetPlan(ctx context.Context, q querier, id string) (*Plan, error) {
	row := q.QueryRow(ctx, `SELECT `+planCols+` FROM core.plans WHERE id = $1`, id)
	p, err := scanPlan(row)
	if err != nil {
		return nil, fmt.Errorf("get plan: %w", err)
	}
	return p, nil
}

// UpsertPlan inserts or updates a plan by id.
func (s *Store) UpsertPlan(ctx context.Context, q querier, p *Plan) error {
	if p.ID == "" {
		p.ID = buuid.MustV7().String()
	}
	name, err := json.Marshal(p.NameI18n)
	if err != nil {
		return fmt.Errorf("plan name_i18n: %w", err)
	}
	_, err = q.Exec(ctx, `
		INSERT INTO core.plans (id, name_i18n, kind, duration_days, traffic_bytes, price, currency, enabled, is_trial, sort, is_topup)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (id) DO UPDATE SET
			name_i18n = EXCLUDED.name_i18n, kind = EXCLUDED.kind,
			duration_days = EXCLUDED.duration_days, traffic_bytes = EXCLUDED.traffic_bytes,
			price = EXCLUDED.price, currency = EXCLUDED.currency, enabled = EXCLUDED.enabled,
			is_trial = EXCLUDED.is_trial, sort = EXCLUDED.sort, is_topup = EXCLUDED.is_topup, updated_at = now()`,
		p.ID, name, p.Kind, p.DurationDays, p.TrafficBytes,
		p.Price, p.Currency, p.Enabled, p.IsTrial, p.Sort, p.IsTopup)
	if err != nil {
		return fmt.Errorf("upsert plan: %w", err)
	}
	return nil
}
