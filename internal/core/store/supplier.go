package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	buuid "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
)

// Reselling a supplier's digital accounts (migration 00033): the API key, the catalog and what the owner makes of each
// product, and the purchases. Only the owner's install has rows here.

// SupplierProduct is a product of the supplier's catalog with the owner's choices.
type SupplierProduct struct {
	ID           string
	Name         string
	Description  string
	CostMicro    *int64 // the wholesale cost in millionths of CostCurrency; nil: not read
	CostCurrency string
	Stock        *int32 // nil: the supplier does not say
	Listed       bool   // the supplier still lists it
	Raw          json.RawMessage
	Enabled      bool
	SellPrice    *int64 // Toman; nil until the owner sets it
	Title        map[string]string
	Desc         map[string]string
	Sort         int32
	SyncedAt     *time.Time
	UpdatedAt    time.Time
}

// SupplierOrder is one purchase of a supplier product.
type SupplierOrder struct {
	ID             string
	UserID         string
	ProductID      string
	Quantity       int
	Amount         int64
	Currency       string
	Status         string // paid, done, refunded
	IdempotencyKey string
	DeliveryEnc    []byte
	Attempts       int
	NextAttemptAt  time.Time
	LastError      string
	NotifiedAt     *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
	DeliveredAt    *time.Time
}

// GetSupplierKey returns the encrypted API key, or ErrNotFound when none is set.
func (s *Store) GetSupplierKey(ctx context.Context, q querier) ([]byte, error) {
	var enc []byte
	err := q.QueryRow(ctx, `SELECT api_key_enc FROM core.supplier_config WHERE id`).Scan(&enc)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get supplier key: %w", err)
	}
	return enc, nil
}

// SetSupplierKey stores the encrypted API key (replacing any before).
func (s *Store) SetSupplierKey(ctx context.Context, q querier, enc []byte) error {
	_, err := q.Exec(ctx, `
		INSERT INTO core.supplier_config (id, api_key_enc) VALUES (true, $1)
		ON CONFLICT (id) DO UPDATE SET api_key_enc = EXCLUDED.api_key_enc, updated_at = now()`, enc)
	if err != nil {
		return fmt.Errorf("set supplier key: %w", err)
	}
	return nil
}

const supplierProductCols = `id, name, description, cost_micro, cost_currency, stock, listed, raw, enabled, sell_price, title_i18n, desc_i18n, sort, synced_at, updated_at`

func scanSupplierProduct(row pgx.Row) (*SupplierProduct, error) {
	var p SupplierProduct
	var title, desc []byte
	err := row.Scan(&p.ID, &p.Name, &p.Description, &p.CostMicro, &p.CostCurrency, &p.Stock, &p.Listed, &p.Raw, &p.Enabled, &p.SellPrice,
		&title, &desc, &p.Sort, &p.SyncedAt, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(title, &p.Title); err != nil {
		return nil, fmt.Errorf("supplier product %s: %w", p.ID, err)
	}
	if err := json.Unmarshal(desc, &p.Desc); err != nil {
		return nil, fmt.Errorf("supplier product %s: %w", p.ID, err)
	}
	return &p, nil
}

// SyncSupplierCatalog writes the catalog as the supplier lists it: a new product is imported OFF, a known one keeps what
// the owner set and takes the supplier's fields, and a product the supplier no longer lists is marked unlisted.
func (s *Store) SyncSupplierCatalog(ctx context.Context, tx pgx.Tx, products []SupplierProduct) error {
	ids := make([]string, 0, len(products))
	for _, p := range products {
		raw := p.Raw
		if len(raw) == 0 {
			raw = json.RawMessage(`{}`)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO core.supplier_products (id, name, description, cost_micro, cost_currency, stock, listed, raw, synced_at)
			VALUES ($1, $2, $3, $4, $5, $6, true, $7, now())
			ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, description = EXCLUDED.description, cost_micro = EXCLUDED.cost_micro,
				cost_currency = EXCLUDED.cost_currency, stock = EXCLUDED.stock, listed = true, raw = EXCLUDED.raw, synced_at = now(), updated_at = now()`,
			p.ID, p.Name, p.Description, p.CostMicro, p.CostCurrency, p.Stock, raw); err != nil {
			return fmt.Errorf("sync supplier catalog: %w", err)
		}
		ids = append(ids, p.ID)
	}
	if _, err := tx.Exec(ctx, `UPDATE core.supplier_products SET listed = false, updated_at = now() WHERE listed AND NOT (id = ANY($1))`, ids); err != nil {
		return fmt.Errorf("sync supplier catalog: %w", err)
	}
	return nil
}

// ListSupplierProducts lists every product, the owner's order first.
func (s *Store) ListSupplierProducts(ctx context.Context, q querier) ([]SupplierProduct, error) {
	return s.supplierProducts(ctx, q, ``)
}

// ListSupplierOffers lists what customers may buy now: switched on, still listed, in stock (or stock unknown), priced.
func (s *Store) ListSupplierOffers(ctx context.Context, q querier) ([]SupplierProduct, error) {
	return s.supplierProducts(ctx, q, `WHERE enabled AND listed AND sell_price IS NOT NULL AND (stock IS NULL OR stock > 0)`)
}

func (s *Store) supplierProducts(ctx context.Context, q querier, where string) ([]SupplierProduct, error) {
	rows, err := q.Query(ctx, `SELECT `+supplierProductCols+` FROM core.supplier_products `+where+` ORDER BY sort, name, id`)
	if err != nil {
		return nil, fmt.Errorf("list supplier products: %w", err)
	}
	defer rows.Close()
	var out []SupplierProduct
	for rows.Next() {
		p, err := scanSupplierProduct(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// GetSupplierProduct returns a product; forUpdate locks it.
func (s *Store) GetSupplierProduct(ctx context.Context, q querier, id string, forUpdate bool) (*SupplierProduct, error) {
	lock := ""
	if forUpdate {
		lock = " FOR UPDATE"
	}
	return scanSupplierProduct(q.QueryRow(ctx, `SELECT `+supplierProductCols+` FROM core.supplier_products WHERE id = $1`+lock, id))
}

// SupplierProductEdit is what the owner may change of a product (nil: keep).
type SupplierProductEdit struct {
	Enabled   *bool
	SellPrice *int64 // 0 clears the price
	Title     map[string]string
	Desc      map[string]string
	Sort      *int32
}

// UpdateSupplierProduct applies the owner's edit.
func (s *Store) UpdateSupplierProduct(ctx context.Context, q querier, id string, e SupplierProductEdit) (*SupplierProduct, error) {
	cur, err := s.GetSupplierProduct(ctx, q, id, true)
	if err != nil {
		return nil, err
	}
	if e.Enabled != nil {
		cur.Enabled = *e.Enabled
	}
	if e.SellPrice != nil {
		if *e.SellPrice == 0 {
			cur.SellPrice = nil
		} else {
			cur.SellPrice = e.SellPrice
		}
	}
	if e.Title != nil {
		cur.Title = e.Title
	}
	if e.Desc != nil {
		cur.Desc = e.Desc
	}
	if e.Sort != nil {
		cur.Sort = *e.Sort
	}
	title, err := json.Marshal(nonNilMap(cur.Title))
	if err != nil {
		return nil, err
	}
	desc, err := json.Marshal(nonNilMap(cur.Desc))
	if err != nil {
		return nil, err
	}
	if _, err := q.Exec(ctx, `
		UPDATE core.supplier_products SET enabled = $2, sell_price = $3, title_i18n = $4, desc_i18n = $5, sort = $6, updated_at = now() WHERE id = $1`,
		id, cur.Enabled, cur.SellPrice, title, desc, cur.Sort); err != nil {
		return nil, fmt.Errorf("update supplier product: %w", err)
	}
	return s.GetSupplierProduct(ctx, q, id, false)
}

func nonNilMap(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

const supplierOrderCols = `id, user_id, product_id, quantity, amount, currency, status, idempotency_key, delivery_enc, attempts, next_attempt_at,
	last_error, notified_at, created_at, updated_at, delivered_at`

func scanSupplierOrder(row pgx.Row) (*SupplierOrder, error) {
	var o SupplierOrder
	err := row.Scan(&o.ID, &o.UserID, &o.ProductID, &o.Quantity, &o.Amount, &o.Currency, &o.Status, &o.IdempotencyKey, &o.DeliveryEnc,
		&o.Attempts, &o.NextAttemptAt, &o.LastError, &o.NotifiedAt, &o.CreatedAt, &o.UpdatedAt, &o.DeliveredAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &o, err
}

// CreateSupplierOrder inserts an order and reports whether it is new; a retry with the same idempotency key returns the
// existing one, and the same key for other terms is ErrIdempotencyConflict.
func (s *Store) CreateSupplierOrder(ctx context.Context, q querier, o *SupplierOrder) (*SupplierOrder, bool, error) {
	o.ID = buuid.MustV7().String()
	var inserted bool
	var got SupplierOrder
	err := q.QueryRow(ctx, `
		INSERT INTO core.supplier_orders (id, user_id, product_id, quantity, amount, currency, idempotency_key)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (idempotency_key) DO UPDATE SET idempotency_key = EXCLUDED.idempotency_key
		RETURNING `+supplierOrderCols+`, (xmax = 0)`,
		o.ID, o.UserID, o.ProductID, o.Quantity, o.Amount, o.Currency, o.IdempotencyKey).
		Scan(&got.ID, &got.UserID, &got.ProductID, &got.Quantity, &got.Amount, &got.Currency, &got.Status, &got.IdempotencyKey, &got.DeliveryEnc,
			&got.Attempts, &got.NextAttemptAt, &got.LastError, &got.NotifiedAt, &got.CreatedAt, &got.UpdatedAt, &got.DeliveredAt, &inserted)
	if err != nil {
		return nil, false, fmt.Errorf("create supplier order: %w", err)
	}
	if !inserted && (got.UserID != o.UserID || got.ProductID != o.ProductID || got.Quantity != o.Quantity) {
		return nil, false, ErrIdempotencyConflict
	}
	return &got, inserted, nil
}

// GetSupplierOrder returns an order; forUpdate locks it.
func (s *Store) GetSupplierOrder(ctx context.Context, q querier, id string, forUpdate bool) (*SupplierOrder, error) {
	lock := ""
	if forUpdate {
		lock = " FOR UPDATE"
	}
	return scanSupplierOrder(q.QueryRow(ctx, `SELECT `+supplierOrderCols+` FROM core.supplier_orders WHERE id = $1`+lock, id))
}

// DueSupplierOrders lists the ids of paid orders whose next attempt is due, oldest first.
func (s *Store) DueSupplierOrders(ctx context.Context, q querier, now time.Time, limit int) ([]string, error) {
	rows, err := q.Query(ctx, `SELECT id FROM core.supplier_orders WHERE status = 'paid' AND next_attempt_at <= $1 ORDER BY next_attempt_at, id LIMIT $2`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("due supplier orders: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// FinishSupplierOrder marks a paid order done (with its delivery, encrypted) or refunded.
func (s *Store) FinishSupplierOrder(ctx context.Context, q querier, id, status string, deliveryEnc []byte, lastError string) error {
	tag, err := q.Exec(ctx, `
		UPDATE core.supplier_orders SET status = $2, delivery_enc = coalesce($3, delivery_enc), last_error = $4, updated_at = now(),
			delivered_at = CASE WHEN $2 = 'done' THEN now() ELSE delivered_at END
		WHERE id = $1 AND status = 'paid'`, id, status, deliveryEnc, lastError)
	if err != nil {
		return fmt.Errorf("finish supplier order: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeferSupplierOrder records a failed attempt and when the next one is due.
func (s *Store) DeferSupplierOrder(ctx context.Context, q querier, id string, next time.Time, lastError string) error {
	_, err := q.Exec(ctx, `
		UPDATE core.supplier_orders SET attempts = attempts + 1, next_attempt_at = $2, last_error = $3, updated_at = now()
		WHERE id = $1 AND status = 'paid'`, id, next, lastError)
	if err != nil {
		return fmt.Errorf("defer supplier order: %w", err)
	}
	return nil
}

// UserSupplierOrders lists a customer's orders, newest first.
func (s *Store) UserSupplierOrders(ctx context.Context, q querier, userID string) ([]SupplierOrder, error) {
	rows, err := q.Query(ctx, `SELECT `+supplierOrderCols+` FROM core.supplier_orders WHERE user_id = $1 ORDER BY created_at DESC, id DESC LIMIT 30`, userID)
	if err != nil {
		return nil, fmt.Errorf("list supplier orders: %w", err)
	}
	defer rows.Close()
	var out []SupplierOrder
	for rows.Next() {
		o, err := scanSupplierOrder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *o)
	}
	return out, rows.Err()
}

// SupplierOrderRow is an order with its buyer and product name, for the dashboard.
type SupplierOrderRow struct {
	SupplierOrder
	TelegramID  int64
	Username    string
	ProductName string
}

// ListSupplierOrders lists every order, newest first, with the number of matches; status filters when not "".
func (s *Store) ListSupplierOrders(ctx context.Context, q querier, status string, p Page) ([]SupplierOrderRow, int64, error) {
	p = p.clamp()
	rows, err := q.Query(ctx, `
		SELECT o.id, o.user_id, o.product_id, o.quantity, o.amount, o.currency, o.status, o.idempotency_key, NULL::bytea, o.attempts, o.next_attempt_at,
		       o.last_error, o.notified_at, o.created_at, o.updated_at, o.delivered_at,
		       u.telegram_id, coalesce(u.username, ''), p.name, count(*) OVER ()
		FROM core.supplier_orders o JOIN core.users u ON u.id = o.user_id JOIN core.supplier_products p ON p.id = o.product_id
		WHERE $1 = '' OR o.status = $1
		ORDER BY o.created_at DESC, o.id DESC LIMIT $2 OFFSET $3`, status, p.Limit, p.Offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list supplier orders: %w", err)
	}
	defer rows.Close()
	var out []SupplierOrderRow
	var total int64
	for rows.Next() {
		var r SupplierOrderRow
		o := &r.SupplierOrder
		if err := rows.Scan(&o.ID, &o.UserID, &o.ProductID, &o.Quantity, &o.Amount, &o.Currency, &o.Status, &o.IdempotencyKey, &o.DeliveryEnc,
			&o.Attempts, &o.NextAttemptAt, &o.LastError, &o.NotifiedAt, &o.CreatedAt, &o.UpdatedAt, &o.DeliveredAt,
			&r.TelegramID, &r.Username, &r.ProductName, &total); err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}

// MarkSupplierOrderNotified records that the owner was told about an order (once).
func (s *Store) MarkSupplierOrderNotified(ctx context.Context, q querier, id string) (bool, error) {
	tag, err := q.Exec(ctx, `UPDATE core.supplier_orders SET notified_at = now() WHERE id = $1 AND notified_at IS NULL`, id)
	if err != nil {
		return false, fmt.Errorf("mark supplier order notified: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}
