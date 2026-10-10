package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Asset is a store file (the logo), kept in core.assets.
type Asset struct {
	Name        string
	ContentType string
	Data        []byte // empty from AssetInfo
	Size        int64
	UpdatedAt   time.Time
}

// GetAsset loads a store file with its bytes.
func (s *Store) GetAsset(ctx context.Context, q querier, name string) (*Asset, error) {
	a := &Asset{Name: name}
	err := q.QueryRow(ctx, `SELECT content_type, data, octet_length(data), updated_at FROM core.assets WHERE name = $1`, name).
		Scan(&a.ContentType, &a.Data, &a.Size, &a.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get asset: %w", err)
	}
	return a, nil
}

// AssetInfo is GetAsset without the bytes.
func (s *Store) AssetInfo(ctx context.Context, q querier, name string) (*Asset, error) {
	a := &Asset{Name: name}
	err := q.QueryRow(ctx, `SELECT content_type, octet_length(data), updated_at FROM core.assets WHERE name = $1`, name).
		Scan(&a.ContentType, &a.Size, &a.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("asset info: %w", err)
	}
	return a, nil
}

// PutAsset stores (or replaces) a store file.
func (s *Store) PutAsset(ctx context.Context, q querier, name, contentType string, data []byte) error {
	_, err := q.Exec(ctx, `
		INSERT INTO core.assets (name, content_type, data) VALUES ($1, $2, $3)
		ON CONFLICT (name) DO UPDATE SET content_type = EXCLUDED.content_type, data = EXCLUDED.data, updated_at = now()`,
		name, contentType, data)
	if err != nil {
		return fmt.Errorf("put asset: %w", err)
	}
	return nil
}

// DeleteAsset removes a store file; false when there was none.
func (s *Store) DeleteAsset(ctx context.Context, q querier, name string) (bool, error) {
	tag, err := q.Exec(ctx, `DELETE FROM core.assets WHERE name = $1`, name)
	if err != nil {
		return false, fmt.Errorf("delete asset: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}
