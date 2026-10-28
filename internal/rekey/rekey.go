// Package rekey re-encrypts what a service keeps encrypted in its database when
// its master key changes (`bobres rotate-key`). Each service runs it on its own
// schema, with a database role that already owns those tables: core for the
// core schema, payments for payments, provisioner for provisioner.
//
// The work is made safe in three ways. Every value is read with whichever key
// fits (the envelope holds the new master key and the previous one), sealed with
// the new key, and read back with the new key alone before the old ciphertext is
// replaced. Each table changes in one transaction, so a table is old or new,
// never half; and because the envelope reads both keys, a run cut off between
// two tables, or killed in the middle, leaves everything readable and can simply
// be run again: it only touches what the new key does not read yet. And before
// anything is written, every value of the service is checked to be readable at
// all: with the wrong key nothing is written.
package rekey

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/crypto"
)

// Table is a table with encrypted columns.
type Table struct {
	Schema, Name string
	// Key is the primary key column. It is read as text, and only used to find the row again.
	Key string
	// Cols are the encrypted (bytea) columns. NULL and empty values are not encrypted values and are left alone.
	Cols []string
}

func (t Table) String() string { return t.Schema + "." + t.Name }

func (t Table) qualified() string { return pgx.Identifier{t.Schema, t.Name}.Sanitize() }

// Services are the services that keep encrypted values, in the order a rotation handles them.
var Services = []string{"core", "payments", "provisioner"}

// Tables lists every encrypted column of each service. A test fails when a
// migration adds a bytea column that is neither here nor in the test's list of
// columns that are not encrypted, so a new secret cannot be forgotten.
var Tables = map[string][]Table{
	"core": {
		{"core", "staff_credentials", "user_id", []string{"totp_secret_enc"}},
		{"core", "tls_config", "id", []string{"key_sealed"}},
		{"core", "vendor_config", "id", []string{"admin_key_enc"}},
		{"core", "vendor_licenses", "id", []string{"key_enc"}},
		{"core", "supplier_config", "id", []string{"api_key_enc"}},
		{"core", "supplier_orders", "id", []string{"delivery_enc"}},
	},
	"payments": {
		{"payments", "gateway_configs", "provider", []string{"merchant_enc", "proxy_enc", "address_enc", "api_key_enc"}},
	},
	"provisioner": {
		{"provisioner", "xui_servers", "id", []string{"api_token_enc"}},
	},
}

// DB starts transactions (a *pgxpool.Pool does).
type DB interface {
	BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error)
}

// Options tune a run. The hooks are for tests that cut a run short.
type Options struct {
	// DryRun reads and counts and writes nothing: the transactions are read-only.
	DryRun bool
	// BeforeCommit runs once a table is changed and checked, before its transaction commits; an error rolls it back.
	BeforeCommit func(Table) error
	// AfterTable runs after a table's transaction committed; an error stops the run.
	AfterTable func(Table) error
}

// TableReport is what one table held and what was done.
type TableReport struct {
	Table Table
	// Rows is how many rows the table has; Values how many encrypted values they hold.
	Rows, Values int
	// OnCurrent is how many values the current key already reads.
	OnCurrent int
	// Sealed is how many values were sealed again with the current key (in a dry run: would be).
	Sealed int
}

// Report is the answer of a run.
type Report struct {
	DryRun bool
	Tables []TableReport
}

// Values is the number of encrypted values in all the tables.
func (r Report) Values() (n int) {
	for _, t := range r.Tables {
		n += t.Values
	}
	return n
}

// Sealed is the number of values sealed again (in a dry run: that would be).
func (r Report) Sealed() (n int) {
	for _, t := range r.Tables {
		n += t.Sealed
	}
	return n
}

// UnreadableError names a value that no key reads. It carries where the value
// is, never the value.
type UnreadableError struct {
	Table  Table
	Column string
	Key    string
}

func (e *UnreadableError) Error() string {
	return fmt.Sprintf("the value of %s.%s in the row %s=%s is not readable with the key this service has (or its previous key): wrong key, or a damaged value",
		e.Table, e.Column, e.Table.Key, e.Key)
}

// Plan reads every encrypted value, without a lock and without a write, and
// reports what a run would do. It fails with *UnreadableError when a value is
// readable with no key.
func Plan(ctx context.Context, db DB, env *crypto.Envelope, tables []Table) (Report, error) {
	rep := Report{DryRun: true}
	for _, t := range tables {
		tr, err := inTx(ctx, db, true, func(tx pgx.Tx) (TableReport, error) { return scan(ctx, tx, env, t, false) })
		if err != nil {
			return rep, err
		}
		rep.Tables = append(rep.Tables, tr)
	}
	return rep, nil
}

// Run seals every value that the current key does not read with the current
// key. With Options.DryRun it only plans. It first checks that every value can
// be read (nothing is written otherwise), then changes the tables one by one,
// each in its own transaction.
func Run(ctx context.Context, db DB, env *crypto.Envelope, tables []Table, opts Options) (Report, error) {
	plan, err := Plan(ctx, db, env, tables)
	if err != nil || opts.DryRun {
		return plan, err
	}
	rep := Report{}
	for _, t := range tables {
		tr, err := inTx(ctx, db, false, func(tx pgx.Tx) (TableReport, error) {
			tr, err := scan(ctx, tx, env, t, true)
			if err != nil {
				return tr, err
			}
			if err := verify(ctx, tx, env.CurrentOnly(), t); err != nil {
				return tr, err
			}
			if opts.BeforeCommit != nil {
				if err := opts.BeforeCommit(t); err != nil {
					return tr, err
				}
			}
			return tr, nil
		})
		if err != nil {
			return rep, fmt.Errorf("%s: %w (this table was left as it was)", t, err)
		}
		rep.Tables = append(rep.Tables, tr)
		if opts.AfterTable != nil {
			if err := opts.AfterTable(t); err != nil {
				return rep, err
			}
		}
	}
	return rep, nil
}

// inTx runs f in a transaction and commits it when f succeeds.
func inTx(ctx context.Context, db DB, readOnly bool, f func(pgx.Tx) (TableReport, error)) (TableReport, error) {
	mode := pgx.ReadWrite
	if readOnly {
		mode = pgx.ReadOnly
	}
	tx, err := db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: mode})
	if err != nil {
		return TableReport{}, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // a no-op once committed
	tr, err := f(tx)
	if err != nil {
		return tr, err
	}
	if err := tx.Commit(ctx); err != nil {
		return tr, fmt.Errorf("commit: %w", err)
	}
	return tr, nil
}

// row is one row's key and its encrypted values, in the order of Table.Cols.
type row struct {
	key  string
	vals [][]byte
}

// load reads the rows of t; lock takes the row locks a write needs.
func load(ctx context.Context, tx pgx.Tx, t Table, lock bool) ([]row, error) {
	cols := make([]string, len(t.Cols))
	for i, c := range t.Cols {
		cols[i] = pgx.Identifier{c}.Sanitize()
	}
	key := pgx.Identifier{t.Key}.Sanitize()
	q := "SELECT " + key + "::text, " + strings.Join(cols, ", ") + " FROM " + t.qualified() + " ORDER BY " + key
	if lock {
		q += " FOR UPDATE"
	}
	rows, err := tx.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", t, err)
	}
	defer rows.Close()
	var out []row
	for rows.Next() {
		r := row{vals: make([][]byte, len(t.Cols))}
		dest := make([]any, 0, 1+len(t.Cols))
		dest = append(dest, &r.key)
		for i := range r.vals {
			dest = append(dest, &r.vals[i])
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, fmt.Errorf("read %s: %w", t, err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", t, err)
	}
	return out, nil
}

// scan counts the table's values and, with apply, seals those the current key does not read.
func scan(ctx context.Context, tx pgx.Tx, env *crypto.Envelope, t Table, apply bool) (TableReport, error) {
	tr := TableReport{Table: t}
	rows, err := load(ctx, tx, t, apply)
	if err != nil {
		return tr, err
	}
	tr.Rows = len(rows)
	for _, r := range rows {
		for i, v := range r.vals {
			if len(v) == 0 {
				continue
			}
			tr.Values++
			switch env.Check(v) {
			case crypto.Current:
				tr.OnCurrent++
				continue
			case crypto.Unreadable:
				return tr, &UnreadableError{Table: t, Column: t.Cols[i], Key: r.key}
			}
			tr.Sealed++
			if !apply {
				continue
			}
			sealed, err := env.Reseal(v)
			if err != nil {
				return tr, fmt.Errorf("seal %s.%s (%s=%s) again: %w", t, t.Cols[i], t.Key, r.key, err)
			}
			col := pgx.Identifier{t.Cols[i]}.Sanitize()
			// The row is locked; the old value in the condition is a second guard that nothing else changed it.
			tag, err := tx.Exec(ctx, "UPDATE "+t.qualified()+" SET "+col+" = $1 WHERE "+pgx.Identifier{t.Key}.Sanitize()+"::text = $2 AND "+col+" = $3",
				sealed, r.key, v)
			if err != nil {
				return tr, fmt.Errorf("write %s.%s (%s=%s): %w", t, t.Cols[i], t.Key, r.key, err)
			}
			if tag.RowsAffected() != 1 {
				return tr, errors.New("a row changed while it was being re-encrypted")
			}
		}
	}
	return tr, nil
}

// verify reads the table once more, inside the transaction that changed it, and
// requires every value to be readable with the current key alone: after the
// commit nothing in the table needs the previous key.
func verify(ctx context.Context, tx pgx.Tx, current *crypto.Envelope, t Table) error {
	rows, err := load(ctx, tx, t, false)
	if err != nil {
		return err
	}
	for _, r := range rows {
		for i, v := range r.vals {
			if len(v) > 0 && current.Check(v) != crypto.Current {
				return fmt.Errorf("after the change: %w", &UnreadableError{Table: t, Column: t.Cols[i], Key: r.key})
			}
		}
	}
	return nil
}
