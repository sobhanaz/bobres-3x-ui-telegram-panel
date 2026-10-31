package store

import (
	"context"
	"sync"
	"testing"
)

// Callers that find core.install empty at the same moment all get the one id that is made: one inserts it, the
// others' insert does nothing and their statement's snapshot does not see the new row yet (the v0.19.0
// benchmark saw most of 320 such calls fail with "no rows").
func TestInstallIDMadeConcurrently(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	for round := range 5 {
		if _, err := s.DB().Exec(ctx, `DELETE FROM core.install`); err != nil {
			t.Fatal(err)
		}
		const callers = 4 // the pool CI's race job allows
		ids := make([]string, callers)
		errs := make([]error, callers)
		var start, done sync.WaitGroup
		start.Add(1)
		for i := range callers {
			done.Add(1)
			go func() {
				defer done.Done()
				start.Wait()
				ids[i], errs[i] = s.InstallID(ctx, s.Conn())
			}()
		}
		start.Done()
		done.Wait()
		for i := range callers {
			if errs[i] != nil || ids[i] == "" || ids[i] != ids[0] {
				t.Fatalf("round %d, caller %d: %q %v (first %q)", round, i, ids[i], errs[i], ids[0])
			}
		}
	}
}
