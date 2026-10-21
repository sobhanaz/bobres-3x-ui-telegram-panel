package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	buuid "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
)

const planCols = `id, name_i18n, kind, duration_days, traffic_bytes, price, currency, enabled, is_trial, sort, is_topup,
	ARRAY(SELECT ps.server_id::text FROM core.plan_servers ps WHERE ps.plan_id = plans.id ORDER BY ps.server_id),
	COALESCE((SELECT json_agg(json_build_object('server_id', g.server_id, 'inbound_ids', g.ids) ORDER BY g.server_id)
		FROM (SELECT pi.server_id::text AS server_id, array_agg(pi.inbound_id ORDER BY pi.inbound_id) AS ids
			FROM core.plan_inbounds pi WHERE pi.plan_id = plans.id GROUP BY pi.server_id) g), '[]'::json),
	group_id::text`

func scanPlan(row pgx.Row) (*Plan, error) {
	var (
		p        Plan
		name     []byte
		inbounds []byte
	)
	err := row.Scan(&p.ID, &name, &p.Kind, &p.DurationDays, &p.TrafficBytes,
		&p.Price, &p.Currency, &p.Enabled, &p.IsTrial, &p.Sort, &p.IsTopup, &p.ServerIDs, &inbounds, &p.GroupID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if p.ServerIDs == nil {
		p.ServerIDs = []string{}
	}
	if err := json.Unmarshal(name, &p.NameI18n); err != nil {
		return nil, fmt.Errorf("plan name_i18n: %w", err)
	}
	if err := json.Unmarshal(inbounds, &p.Inbounds); err != nil {
		return nil, fmt.Errorf("plan inbounds: %w", err)
	}
	if p.Inbounds == nil {
		p.Inbounds = []PlanInbound{}
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

// UpsertPlan inserts or updates a plan by id, and replaces its servers when
// p.ServerIDs is not nil (an empty list: any server) and its inbound choice
// when p.Inbounds is not nil (an empty list: the default).
func (s *Store) UpsertPlan(ctx context.Context, q querier, p *Plan) error {
	if p.ID == "" {
		p.ID = buuid.MustV7().String()
	}
	name, err := json.Marshal(p.NameI18n)
	if err != nil {
		return fmt.Errorf("plan name_i18n: %w", err)
	}
	_, err = q.Exec(ctx, `
		INSERT INTO core.plans (id, name_i18n, kind, duration_days, traffic_bytes, price, currency, enabled, is_trial, sort, is_topup, group_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12::uuid)
		ON CONFLICT (id) DO UPDATE SET
			name_i18n = EXCLUDED.name_i18n, kind = EXCLUDED.kind,
			duration_days = EXCLUDED.duration_days, traffic_bytes = EXCLUDED.traffic_bytes,
			price = EXCLUDED.price, currency = EXCLUDED.currency, enabled = EXCLUDED.enabled,
			is_trial = EXCLUDED.is_trial, sort = EXCLUDED.sort, is_topup = EXCLUDED.is_topup, group_id = EXCLUDED.group_id,
			updated_at = now()`,
		p.ID, name, p.Kind, p.DurationDays, p.TrafficBytes,
		p.Price, p.Currency, p.Enabled, p.IsTrial, p.Sort, p.IsTopup, p.GroupID)
	if err != nil {
		return fmt.Errorf("upsert plan: %w", err)
	}
	if p.ServerIDs != nil {
		// The servers no longer named go, the new ones come (one statement:
		// both see the rows as they were).
		_, err = q.Exec(ctx, `
			WITH gone AS (DELETE FROM core.plan_servers WHERE plan_id = $1 AND server_id <> ALL($2::uuid[]))
			INSERT INTO core.plan_servers (plan_id, server_id) SELECT $1, unnest($2::uuid[])
			ON CONFLICT DO NOTHING`, p.ID, p.ServerIDs)
		if err != nil {
			return fmt.Errorf("plan servers: %w", err)
		}
	}
	if p.Inbounds == nil {
		return nil
	}
	// The same for the inbound choice: rows are (server, inbound) pairs.
	var servers []string
	var inbounds []int32
	for _, in := range p.Inbounds {
		for _, id := range in.InboundIDs {
			servers, inbounds = append(servers, in.ServerID), append(inbounds, id)
		}
	}
	_, err = q.Exec(ctx, `
		WITH want AS (SELECT * FROM unnest($2::uuid[], $3::int[]) AS w(server_id, inbound_id)),
		gone AS (DELETE FROM core.plan_inbounds pi WHERE pi.plan_id = $1
			AND NOT EXISTS (SELECT 1 FROM want w WHERE w.server_id = pi.server_id AND w.inbound_id = pi.inbound_id))
		INSERT INTO core.plan_inbounds (plan_id, server_id, inbound_id) SELECT $1, server_id, inbound_id FROM want
		ON CONFLICT DO NOTHING`, p.ID, servers, inbounds)
	if err != nil {
		return fmt.Errorf("plan inbounds: %w", err)
	}
	return nil
}

// DeletePlanInboundsOfServer drops every plan's inbound choice for a server
// that was deleted (plan_inbounds keeps the provisioner's server id with no
// foreign key). It returns how many plans lost a choice.
func (s *Store) DeletePlanInboundsOfServer(ctx context.Context, q querier, serverID string) (int, error) {
	var n int
	err := q.QueryRow(ctx, `
		WITH gone AS (DELETE FROM core.plan_inbounds WHERE server_id = $1::uuid RETURNING plan_id)
		SELECT count(DISTINCT plan_id) FROM gone`, serverID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("delete plan inbounds: %w", err)
	}
	return n, nil
}
