package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	buuid "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
)

// PlanGroup mirrors core.plan_groups: a named group of plans with a text the
// customer reads before choosing one of them.
type PlanGroup struct {
	ID              string
	NameI18n        map[string]string
	DescriptionI18n map[string]string
	Sort            int32
	// Plans is how many plans are in it (filled by the list and by Get).
	Plans int
}

const planGroupCols = `id, name_i18n, description_i18n, sort,
	(SELECT count(*) FROM core.plans p WHERE p.group_id = plan_groups.id)`

func scanPlanGroup(row pgx.Row) (*PlanGroup, error) {
	var (
		g          PlanGroup
		name, desc []byte
	)
	err := row.Scan(&g.ID, &name, &desc, &g.Sort, &g.Plans)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(name, &g.NameI18n); err != nil {
		return nil, fmt.Errorf("plan group name_i18n: %w", err)
	}
	if err := json.Unmarshal(desc, &g.DescriptionI18n); err != nil {
		return nil, fmt.Errorf("plan group description_i18n: %w", err)
	}
	return &g, nil
}

// ListPlanGroups returns every group, in the order customers see them.
func (s *Store) ListPlanGroups(ctx context.Context, q querier) ([]PlanGroup, error) {
	rows, err := q.Query(ctx, `SELECT `+planGroupCols+` FROM core.plan_groups ORDER BY sort, created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("list plan groups: %w", err)
	}
	defer rows.Close()
	var out []PlanGroup
	for rows.Next() {
		g, err := scanPlanGroup(rows)
		if err != nil {
			return nil, fmt.Errorf("list plan groups: %w", err)
		}
		out = append(out, *g)
	}
	return out, rows.Err()
}

// GetPlanGroup returns one group by id (ErrNotFound when there is none).
func (s *Store) GetPlanGroup(ctx context.Context, q querier, id string) (*PlanGroup, error) {
	g, err := scanPlanGroup(q.QueryRow(ctx, `SELECT `+planGroupCols+` FROM core.plan_groups WHERE id = $1`, id))
	if err != nil {
		return nil, fmt.Errorf("get plan group: %w", err)
	}
	return g, nil
}

// UpsertPlanGroup inserts (empty ID: a new id is made) or updates a group.
func (s *Store) UpsertPlanGroup(ctx context.Context, q querier, g *PlanGroup) error {
	if g.ID == "" {
		g.ID = buuid.MustV7().String()
	}
	name, err := json.Marshal(g.NameI18n)
	if err != nil {
		return fmt.Errorf("plan group name_i18n: %w", err)
	}
	desc, err := json.Marshal(g.DescriptionI18n)
	if err != nil {
		return fmt.Errorf("plan group description_i18n: %w", err)
	}
	_, err = q.Exec(ctx, `
		INSERT INTO core.plan_groups (id, name_i18n, description_i18n, sort) VALUES ($1, $2, $3, $4)
		ON CONFLICT (id) DO UPDATE SET name_i18n = EXCLUDED.name_i18n, description_i18n = EXCLUDED.description_i18n,
			sort = EXCLUDED.sort, updated_at = now()`, g.ID, name, desc, g.Sort)
	if err != nil {
		return fmt.Errorf("upsert plan group: %w", err)
	}
	return nil
}

// DeletePlanGroup deletes a group; its plans stay on sale, ungrouped. It
// returns how many plans were in it (ErrNotFound when there is no such group).
func (s *Store) DeletePlanGroup(ctx context.Context, q querier, id string) (int, error) {
	var n int
	err := q.QueryRow(ctx, `
		WITH freed AS (SELECT count(*) AS n FROM core.plans WHERE group_id = $1),
		gone AS (DELETE FROM core.plan_groups WHERE id = $1 RETURNING id)
		SELECT freed.n FROM freed, gone`, id).Scan(&n)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("delete plan group: %w", err)
	}
	return n, nil
}
