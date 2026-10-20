package migrate

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"testing"
)

// 00026 on a store from before: a ledger that already has entries keeps them, gift codes and their redemptions
// get the checks the application relies on as a last guard (a code is never stored, a redemption is counted
// once per customer and per request, a code is never over-redeemed), the ledger takes the gift_code kind, and
// Down drops the three tables but leaves the entries a redemption wrote: the ledger is append-only.
func TestGiftCodesMigration(t *testing.T) {
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
		u1     = "00000000-0000-0000-0000-000000000701"
		u2     = "00000000-0000-0000-0000-000000000702"
		ledger = "00000000-0000-0000-0000-000000000711"
		code1  = "00000000-0000-0000-0000-0000000007a1"
		code2  = "00000000-0000-0000-0000-0000000007a2"
	)
	for _, q := range []string{
		`INSERT INTO core.users (id, telegram_id) VALUES ('` + u1 + `', 701), ('` + u2 + `', 702)`,
		`INSERT INTO core.ledger_entries (id, user_id, currency, amount, kind, idempotency_key, balance_after)
			VALUES ('` + ledger + `', '` + u1 + `', 'IRT', 500, 'topup', 'k-before-gift', 500)`,
	} {
		if err := exec(q); err != nil {
			t.Fatalf("seed: %v\n%s", err, q)
		}
	}
	// Before: a gift code entry is refused, the old kinds are all fine.
	if err := exec(`INSERT INTO core.ledger_entries (id, user_id, currency, amount, kind, idempotency_key, balance_after)
		VALUES (gen_random_uuid(), $1, 'IRT', 10, 'gift_code', 'k-early', 510)`, u1); err == nil {
		t.Fatal("before 00026 the ledger took a gift_code entry")
	}
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up: %v", err)
	}
	var kept int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM core.ledger_entries WHERE id = $1`, ledger).Scan(&kept); err != nil || kept != 1 {
		t.Fatalf("the old ledger entry after Up: %d %v", kept, err)
	}

	hash := func(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }
	insertCode := func(id string, h []byte, amount int64, max, per int) error {
		return exec(`INSERT INTO core.gift_codes (id, code_hash, code_tail, generated, amount, currency, max_redemptions, per_customer_limit)
			VALUES ($1, $2, 'ABCD', true, $3, 'IRT', $4, $5)`, id, h, amount, max, per)
	}
	if err := insertCode(code1, hash(1), 1000, 5, 1); err != nil {
		t.Fatalf("a code: %v", err)
	}
	// The terms and the hash are checked.
	for name, f := range map[string]func() error{
		"a hash that is not 32 bytes": func() error { return insertCode("00000000-0000-0000-0000-0000000007b1", []byte("short"), 1, 1, 1) },
		"the same hash twice":         func() error { return insertCode("00000000-0000-0000-0000-0000000007b2", hash(1), 1, 1, 1) },
		"amount 0":                    func() error { return insertCode("00000000-0000-0000-0000-0000000007b3", hash(3), 0, 1, 1) },
		"a negative amount":           func() error { return insertCode("00000000-0000-0000-0000-0000000007b4", hash(4), -5, 1, 1) },
		"max redemptions 0":           func() error { return insertCode("00000000-0000-0000-0000-0000000007b5", hash(5), 1, 0, 1) },
		"per customer 0":              func() error { return insertCode("00000000-0000-0000-0000-0000000007b6", hash(6), 1, 1, 0) },
		"per customer above the max":  func() error { return insertCode("00000000-0000-0000-0000-0000000007b7", hash(7), 1, 2, 3) },
		"a note over 200 characters": func() error {
			return exec(`INSERT INTO core.gift_codes (id, code_hash, code_tail, generated, amount, currency, max_redemptions, note)
				VALUES ('00000000-0000-0000-0000-0000000007b8', $1, 'ABCD', true, 1, 'IRT', 1, $2)`, hash(8), strings.Repeat("x", 201))
		},
		"a currency that is not a code": func() error {
			return exec(`INSERT INTO core.gift_codes (id, code_hash, code_tail, generated, amount, currency, max_redemptions)
				VALUES ('00000000-0000-0000-0000-0000000007b9', $1, 'ABCD', true, 1, 'irt', 1)`, hash(9))
		},
		"an empty tail": func() error {
			return exec(`INSERT INTO core.gift_codes (id, code_hash, code_tail, generated, amount, currency, max_redemptions)
				VALUES ('00000000-0000-0000-0000-0000000007ba', $1, '', true, 1, 'IRT', 1)`, hash(10))
		},
	} {
		if err := f(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// A code is never redeemed more often than its limit, whatever the application does.
	if err := exec(`UPDATE core.gift_codes SET redeemed_count = 6 WHERE id = $1`, code1); err == nil {
		t.Error("redeemed_count above max_redemptions was accepted")
	}
	if err := exec(`UPDATE core.gift_codes SET redeemed_count = -1 WHERE id = $1`, code1); err == nil {
		t.Error("a negative redeemed_count was accepted")
	}
	if err := exec(`UPDATE core.gift_codes SET redeemed_count = 5 WHERE id = $1`, code1); err != nil {
		t.Errorf("redeemed_count at the limit: %v", err)
	}
	if err := exec(`UPDATE core.gift_codes SET redeemed_count = 0 WHERE id = $1`, code1); err != nil {
		t.Fatal(err)
	}

	// The ledger takes the new kind; a redemption names the one entry it wrote.
	const entry = "00000000-0000-0000-0000-000000000712"
	if err := exec(`INSERT INTO core.ledger_entries (id, user_id, currency, amount, kind, ref_type, ref_id, idempotency_key, balance_after)
		VALUES ($1, $2, 'IRT', 1000, 'gift_code', 'gift_code', $3, 'gift:k1', 1500)`, entry, u1, code1); err != nil {
		t.Fatalf("a gift_code ledger entry: %v", err)
	}
	redeem := func(id, code, user string, seq int, key, ledgerEntry string) error {
		return exec(`INSERT INTO core.gift_code_redemptions (id, gift_code_id, user_id, seq, request_key, amount, currency, ledger_entry_id, balance_after)
			VALUES ($1, $2, $3, $4, $5, 1000, 'IRT', $6, 1500)`, id, code, user, seq, key, ledgerEntry)
	}
	if err := redeem("00000000-0000-0000-0000-0000000007c1", code1, u1, 1, "req-1", entry); err != nil {
		t.Fatalf("a redemption: %v", err)
	}
	const entry2 = "00000000-0000-0000-0000-000000000713"
	if err := exec(`INSERT INTO core.ledger_entries (id, user_id, currency, amount, kind, ref_type, ref_id, idempotency_key, balance_after)
		VALUES ($1, $2, 'IRT', 1000, 'gift_code', 'gift_code', $3, 'gift:k2', 2500)`, entry2, u1, code1); err != nil {
		t.Fatal(err)
	}
	for name, f := range map[string]func() error{
		"a second redemption with the same number (one per customer when the limit is 1)": func() error {
			return redeem("00000000-0000-0000-0000-0000000007c2", code1, u1, 1, "req-2", entry2)
		},
		"the same request key again": func() error {
			return redeem("00000000-0000-0000-0000-0000000007c3", code1, u1, 2, "req-1", entry2)
		},
		"a number of 0": func() error {
			return redeem("00000000-0000-0000-0000-0000000007c4", code1, u1, 0, "req-3", entry2)
		},
		"the same ledger entry twice": func() error {
			return redeem("00000000-0000-0000-0000-0000000007c5", code1, u1, 2, "req-4", entry)
		},
		"a ledger entry that does not exist": func() error {
			return redeem("00000000-0000-0000-0000-0000000007c6", code1, u1, 2, "req-5", "00000000-0000-0000-0000-0000000007ff")
		},
		"a code that does not exist": func() error {
			return redeem("00000000-0000-0000-0000-0000000007c7", "00000000-0000-0000-0000-0000000007fe", u1, 2, "req-6", entry2)
		},
		"an empty request key": func() error {
			return redeem("00000000-0000-0000-0000-0000000007c8", code1, u1, 2, "", entry2)
		},
	} {
		if err := f(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// Another customer's redemption of the same code is its own row, and the same request key may be theirs.
	const entry3 = "00000000-0000-0000-0000-000000000714"
	if err := exec(`INSERT INTO core.ledger_entries (id, user_id, currency, amount, kind, idempotency_key, balance_after)
		VALUES ($1, $2, 'IRT', 1000, 'gift_code', 'gift:k3', 1000)`, entry3, u2); err != nil {
		t.Fatal(err)
	}
	if err := redeem("00000000-0000-0000-0000-0000000007c9", code1, u2, 1, "req-1", entry3); err != nil {
		t.Fatalf("another customer: %v", err)
	}

	// The failure counter takes no negative counts and one row per customer.
	if err := exec(`INSERT INTO core.gift_code_failures (user_id, failures, window_start) VALUES ($1, 1, now())`, u1); err != nil {
		t.Fatal(err)
	}
	if err := exec(`INSERT INTO core.gift_code_failures (user_id, failures, window_start) VALUES ($1, 1, now())`, u1); err == nil {
		t.Error("two failure rows for one customer")
	}
	if err := exec(`UPDATE core.gift_code_failures SET failures = -1 WHERE user_id = $1`, u1); err == nil {
		t.Error("a negative failure count")
	}

	// Down: the three tables go; the ledger keeps what was credited, and Up brings the tables back empty.
	if err := downTo(ctx, dsn, "core", 25); err != nil {
		t.Fatalf("Down: %v", err)
	}
	for _, table := range []string{"gift_codes", "gift_code_redemptions", "gift_code_failures"} {
		var has bool
		if err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.tables
			WHERE table_schema = 'core' AND table_name = $1)`, table).Scan(&has); err != nil || has {
			t.Fatalf("after Down: %s is still there (%v)", table, err)
		}
	}
	var credited int64
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(sum(amount), 0) FROM core.ledger_entries WHERE kind = 'gift_code'`).Scan(&credited); err != nil || credited != 3000 {
		t.Fatalf("after Down the ledger's gift credits: %d %v", credited, err)
	}
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up again: %v", err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM core.gift_codes) + (SELECT count(*) FROM core.gift_code_redemptions)
		+ (SELECT count(*) FROM core.gift_code_failures)`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("after Up again: %d rows, %v", n, err)
	}
}
