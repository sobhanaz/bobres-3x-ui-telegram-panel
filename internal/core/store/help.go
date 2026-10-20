package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	buuid "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
)

// HelpButton is a URL button of a tutorial: an address and its label by
// language.
type HelpButton struct {
	URL   string            `json:"url"`
	Label map[string]string `json:"label"`
}

// HelpEntry mirrors core.help_entries: a question of the FAQ or a tutorial,
// with its text by language.
type HelpEntry struct {
	ID   string
	Kind string // faq | tutorial
	// Title is the question or the tutorial's title and Body the answer or the
	// tutorial's text, each by language.
	Title   map[string]string
	Body    map[string]string
	Buttons []HelpButton // tutorials only
	Sort    int
	Enabled bool
	// Version counts the changes of the content (an edit with an older one is
	// refused with ErrStale); reordering does not change it.
	Version   int
	CreatedAt time.Time
	UpdatedAt time.Time
	UpdatedBy *string
}

const helpCols = `id, kind, title, body, buttons, sort, enabled, version, created_at, updated_at, updated_by`

// helpOrder is the order customers see: the owner's, then the order made in.
const helpOrder = `ORDER BY kind, sort, created_at, id`

func scanHelp(row pgx.Row) (*HelpEntry, error) {
	var (
		e                    HelpEntry
		title, body, buttons []byte
	)
	err := row.Scan(&e.ID, &e.Kind, &title, &body, &buttons, &e.Sort, &e.Enabled, &e.Version, &e.CreatedAt, &e.UpdatedAt, &e.UpdatedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	for _, d := range []struct {
		raw []byte
		to  any
	}{{title, &e.Title}, {body, &e.Body}, {buttons, &e.Buttons}} {
		if err := json.Unmarshal(d.raw, d.to); err != nil {
			return nil, fmt.Errorf("help entry %s: %w", e.ID, err)
		}
	}
	if e.Buttons == nil {
		e.Buttons = []HelpButton{}
	}
	return &e, nil
}

// helpJSON encodes an entry's three JSON columns.
func helpJSON(e *HelpEntry) (title, body, buttons []byte, err error) {
	if e.Buttons == nil {
		e.Buttons = []HelpButton{}
	}
	for _, d := range []struct {
		from any
		to   *[]byte
	}{{e.Title, &title}, {e.Body, &body}, {e.Buttons, &buttons}} {
		if *d.to, err = json.Marshal(d.from); err != nil {
			return nil, nil, nil, err
		}
	}
	return title, body, buttons, nil
}

// ListHelp lists the help entries of a kind ("" for both) in the order
// customers see them; enabledOnly leaves out the ones that are switched off.
func (s *Store) ListHelp(ctx context.Context, q querier, kind string, enabledOnly bool) ([]HelpEntry, error) {
	rows, err := q.Query(ctx, `SELECT `+helpCols+` FROM core.help_entries
		WHERE ($1 = '' OR kind = $1) AND (NOT $2 OR enabled) `+helpOrder, kind, enabledOnly)
	if err != nil {
		return nil, fmt.Errorf("list help: %w", err)
	}
	defer rows.Close()
	out := []HelpEntry{}
	for rows.Next() {
		e, err := scanHelp(rows)
		if err != nil {
			return nil, fmt.Errorf("list help: %w", err)
		}
		out = append(out, *e)
	}
	return out, rows.Err()
}

// GetHelp loads one entry; lock takes it FOR UPDATE.
func (s *Store) GetHelp(ctx context.Context, q querier, id string, lock bool) (*HelpEntry, error) {
	sql := `SELECT ` + helpCols + ` FROM core.help_entries WHERE id = $1`
	if lock {
		sql += ` FOR UPDATE`
	}
	e, err := scanHelp(q.QueryRow(ctx, sql, id))
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, fmt.Errorf("get help: %w", err)
	}
	return e, err
}

// CountHelp counts the entries of a kind.
func (s *Store) CountHelp(ctx context.Context, q querier, kind string) (int, error) {
	var n int
	if err := q.QueryRow(ctx, `SELECT count(*) FROM core.help_entries WHERE kind = $1`, kind).Scan(&n); err != nil {
		return 0, fmt.Errorf("count help: %w", err)
	}
	return n, nil
}

// HelpEnabledKinds reports whether there is an enabled FAQ entry and an
// enabled tutorial (an entry is always readable in some language: the table
// checks it).
func (s *Store) HelpEnabledKinds(ctx context.Context, q querier) (faq, tutorial bool, err error) {
	rows, err := q.Query(ctx, `SELECT DISTINCT kind FROM core.help_entries WHERE enabled`)
	if err != nil {
		return false, false, fmt.Errorf("help kinds: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return false, false, err
		}
		faq, tutorial = faq || k == "faq", tutorial || k == "tutorial"
	}
	return faq, tutorial, rows.Err()
}

// InsertHelp adds an entry after the last one of its kind and fills in its
// id, order, version and times.
func (s *Store) InsertHelp(ctx context.Context, q querier, e *HelpEntry) error {
	title, body, buttons, err := helpJSON(e)
	if err != nil {
		return err
	}
	e.ID = buuid.MustV7().String()
	err = q.QueryRow(ctx, `INSERT INTO core.help_entries (id, kind, title, body, buttons, sort, enabled, updated_by)
		VALUES ($1, $2, $3, $4, $5, (SELECT COALESCE(max(sort), 0) + 1 FROM core.help_entries WHERE kind = $2), $6, $7)
		RETURNING sort, version, created_at, updated_at`,
		e.ID, e.Kind, title, body, buttons, e.Enabled, e.UpdatedBy).Scan(&e.Sort, &e.Version, &e.CreatedAt, &e.UpdatedAt)
	if err != nil {
		return fmt.Errorf("insert help: %w", err)
	}
	return nil
}

// UpdateHelp saves the content of an entry (not its kind or place) read at
// version: ErrStale when it has changed since, ErrNotFound when it is gone.
func (s *Store) UpdateHelp(ctx context.Context, q querier, e *HelpEntry, version int) error {
	title, body, buttons, err := helpJSON(e)
	if err != nil {
		return err
	}
	err = q.QueryRow(ctx, `UPDATE core.help_entries SET title = $2, body = $3, buttons = $4, enabled = $5, updated_by = $6,
			version = version + 1, updated_at = now()
		WHERE id = $1 AND version = $7 RETURNING version, updated_at`,
		e.ID, title, body, buttons, e.Enabled, e.UpdatedBy, version).Scan(&e.Version, &e.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		if _, err := s.GetHelp(ctx, q, e.ID, false); err != nil {
			return err
		}
		return ErrStale
	}
	if err != nil {
		return fmt.Errorf("update help: %w", err)
	}
	return nil
}

// DeleteHelp removes an entry.
func (s *Store) DeleteHelp(ctx context.Context, q querier, id string) error {
	tag, err := q.Exec(ctx, `DELETE FROM core.help_entries WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete help: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetHelpOrder puts the entries of a kind in the order of ids, which must name
// every one of them once (ErrStale when they do not: an entry came or went
// meanwhile). It returns the order the entries had, and changes nothing when
// that is the one asked for. It runs in the caller's transaction, which holds
// the entries until it ends.
func (s *Store) SetHelpOrder(ctx context.Context, tx pgx.Tx, kind string, ids []string) (before []string, err error) {
	rows, err := tx.Query(ctx, `SELECT id FROM core.help_entries WHERE kind = $1 ORDER BY sort, created_at, id FOR UPDATE`, kind)
	if err != nil {
		return nil, fmt.Errorf("lock help: %w", err)
	}
	before = []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		before = append(before, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if slices.Equal(before, ids) {
		return before, nil
	}
	have, want := slices.Clone(before), slices.Clone(ids)
	slices.Sort(have)
	slices.Sort(want)
	if !slices.Equal(have, want) {
		return before, ErrStale
	}
	for i, id := range ids {
		if _, err := tx.Exec(ctx, `UPDATE core.help_entries SET sort = $2 WHERE id = $1`, id, i+1); err != nil {
			return before, fmt.Errorf("order help: %w", err)
		}
	}
	return before, nil
}
