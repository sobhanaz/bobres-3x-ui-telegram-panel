package store

import (
	"context"
	"testing"
)

// A restore recreates every table (pg_restore), with the rows the backup
// had: the guard's row then names another table's OID, and the next call
// notices the restore, once.
func TestNoteRestoreNoticesARecreatedTable(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	exec := func(q string) {
		t.Helper()
		if _, err := s.DB().Exec(ctx, q); err != nil {
			t.Fatalf("%v\n%s", err, q)
		}
	}
	exec(`TRUNCATE payments.restore_guard`)
	for i := range 2 { // the first call writes the row; neither notices anything
		at, noticed, err := s.NoteRestore(ctx, nil)
		if err != nil || at != nil || noticed {
			t.Fatalf("call %d on a fresh install: %v %v %v", i, at, noticed, err)
		}
	}

	// What pg_restore does with the table: drop, create, copy the rows back.
	exec(`CREATE TEMP TABLE guard_copy AS SELECT * FROM payments.restore_guard`)
	exec(`DROP TABLE payments.restore_guard`)
	exec(`CREATE TABLE payments.restore_guard (id boolean PRIMARY KEY DEFAULT true CHECK (id),
		table_oid oid NOT NULL, restored_at timestamptz)`)
	exec(`INSERT INTO payments.restore_guard SELECT * FROM guard_copy`)

	at, noticed, err := s.NoteRestore(ctx, nil)
	if err != nil || at == nil || !noticed {
		t.Fatalf("after the restore: %v %v %v", at, noticed, err)
	}
	again, noticed, err := s.NoteRestore(ctx, nil)
	if err != nil || again == nil || !again.Equal(*at) || noticed {
		t.Fatalf("noticed twice: %v %v %v", again, noticed, err)
	}
	if got, err := s.RestoredAt(ctx, nil); err != nil || got == nil || !got.Equal(*at) {
		t.Fatalf("restored at: %v %v", got, err)
	}
}
