package migrate

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

// 00026 on a store from before: every existing order belongs to no bulk; a
// bulk holds 2 to 1000 services whose total is the unit price times the
// quantity and has one request key; an order is in a bulk with its place in it
// (both or neither, each place once, the bulk must exist); Down drops what ties
// the orders together and leaves the orders themselves, and Up brings it back.
func TestBulkOrdersMigration(t *testing.T) {
	dsn := testDSN(t)
	dropSchemas(t, dsn)
	ctx := context.Background()
	if err := upTo(ctx, dsn, "core", 25); err != nil {
		t.Fatalf("up to 25: %v", err)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close() //nolint:errcheck // test helper
	exec := func(q string, args ...any) error {
		_, err := db.ExecContext(ctx, q, args...)
		return err
	}
	const (
		uid   = "00000000-0000-0000-0000-000000000701"
		plan  = "00000000-0000-0000-0000-000000000702"
		old   = "00000000-0000-0000-0001-000000000703"
		bulk  = "00000000-0000-0000-0002-000000000704"
		bulk2 = "00000000-0000-0000-0002-000000000705"
	)
	for _, q := range []string{
		`INSERT INTO core.users (id, telegram_id) VALUES ('` + uid + `', 701)`,
		`INSERT INTO core.plans (id, name_i18n, kind, duration_days, price, currency) VALUES ('` + plan + `', '{"en":"P"}', 'time', 30, 1000, 'IRT')`,
		`INSERT INTO core.orders (id, user_id, plan_id, type, status, amount, currency, idempotency_key)
			VALUES ('` + old + `', '` + uid + `', '` + plan + `', 'new', 'active', 1000, 'IRT', 'k-before')`,
	} {
		if err := exec(q); err != nil {
			t.Fatalf("seed: %v\n%s", err, q)
		}
	}
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up: %v", err)
	}

	var bid sql.NullString
	var idx sql.NullInt32
	if err := db.QueryRowContext(ctx, `SELECT bulk_id, bulk_index FROM core.orders WHERE id = $1`, old).Scan(&bid, &idx); err != nil || bid.Valid || idx.Valid {
		t.Fatalf("an order from before: bulk %v index %v (%v)", bid, idx, err)
	}
	insertBulk := func(id string, quantity int, unit, amount int64, key string) error {
		return exec(`INSERT INTO core.bulk_orders (id, user_id, plan_id, quantity, unit_amount, amount, currency, idempotency_key)
			VALUES ($1, $2, $3, $4, $5, $6, 'IRT', $7)`, id, uid, plan, quantity, unit, amount, key)
	}
	if err := insertBulk(bulk, 3, 1000, 3000, "bulk-key"); err != nil {
		t.Fatalf("a bulk of three: %v", err)
	}
	if err := exec(`UPDATE core.bulk_orders SET delivered_at = now() WHERE id = $1`, bulk); err != nil {
		t.Fatalf("announcing a bulk: %v", err)
	}
	for name, c := range map[string]struct {
		quantity      int
		unit, amount  int64
		key           string
		wantRefusedBy string
	}{
		"one service":        {1, 1000, 1000, "k1", "quantity"},
		"a thousand and one": {1001, 1, 1001, "k2", "quantity"},
		"a total that is not the unit price times the quantity": {3, 1000, 2999, "k3", "bulk_orders_total_check"},
		"a free service":     {2, 0, 0, "k4", "unit_amount"},
		"the same key twice": {2, 1000, 2000, "bulk-key", "idempotency_key"},
	} {
		if err := insertBulk(bulk2, c.quantity, c.unit, c.amount, c.key); err == nil || !strings.Contains(err.Error(), c.wantRefusedBy) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := insertBulk(bulk2, 1000, 1, 1000, "k5"); err != nil {
		t.Fatalf("a bulk of a thousand: %v", err)
	}
	if err := exec(`UPDATE core.bulk_orders SET label_prefix = $2 WHERE id = $1`, bulk2, strings.Repeat("ب", 65)); err == nil {
		t.Error("a 65-character name was accepted")
	}

	order := func(key string, bulkID any, index any) error {
		return exec(`INSERT INTO core.orders (id, user_id, plan_id, type, status, amount, currency, idempotency_key, bulk_id, bulk_index)
			VALUES (gen_random_uuid(), $1, $2, 'new', 'paid', 1000, 'IRT', $3, $4, $5)`, uid, plan, key, bulkID, index)
	}
	if err := order("o-1", bulk, 1); err != nil {
		t.Fatalf("the first order of a bulk: %v", err)
	}
	if err := order("o-2", bulk, 2); err != nil {
		t.Fatalf("the second order of a bulk: %v", err)
	}
	if err := order("o-plain", nil, nil); err != nil {
		t.Fatalf("an ordinary order: %v", err)
	}
	if err := order("o-dup", bulk, 2); err == nil || !strings.Contains(err.Error(), "orders_bulk_index_idx") {
		t.Errorf("the same place twice: %v", err)
	}
	if err := order("o-0", bulk, 0); err == nil {
		t.Error("place 0 was accepted")
	}
	if err := order("o-half-1", bulk, nil); err == nil || !strings.Contains(err.Error(), "orders_bulk_pair") {
		t.Errorf("a bulk without a place: %v", err)
	}
	if err := order("o-half-2", nil, 1); err == nil || !strings.Contains(err.Error(), "orders_bulk_pair") {
		t.Errorf("a place without a bulk: %v", err)
	}
	if err := order("o-ghost", "00000000-0000-0000-0002-0000000007ff", 1); err == nil {
		t.Error("an order of a bulk that does not exist")
	}
	if err := exec(`DELETE FROM core.bulk_orders WHERE id = $1`, bulk); err == nil {
		t.Error("a bulk with orders was deleted")
	}

	if err := downTo(ctx, dsn, "core", 25); err != nil {
		t.Fatalf("Down: %v", err)
	}
	var orders int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM core.orders`).Scan(&orders); err != nil || orders != 4 {
		t.Fatalf("after Down: %d orders (%v), want the 4 that were made", orders, err)
	}
	var tables, columns int
	if err := db.QueryRowContext(ctx, `SELECT
		(SELECT count(*) FROM information_schema.tables WHERE table_schema = 'core' AND table_name = 'bulk_orders'),
		(SELECT count(*) FROM information_schema.columns WHERE table_schema = 'core' AND table_name = 'orders' AND column_name IN ('bulk_id', 'bulk_index'))`).
		Scan(&tables, &columns); err != nil || tables != 0 || columns != 0 {
		t.Fatalf("after Down: %d tables, %d columns (%v)", tables, columns, err)
	}
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up again: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT bulk_id, bulk_index FROM core.orders WHERE idempotency_key = 'o-1'`).Scan(&bid, &idx); err != nil || bid.Valid || idx.Valid {
		t.Fatalf("after Up again: bulk %v index %v (%v)", bid, idx, err)
	}
}
