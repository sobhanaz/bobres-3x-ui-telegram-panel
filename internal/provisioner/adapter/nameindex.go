package adapter

import (
	"context"
	"sync"
	"time"
)

// relistEvery bounds how often a nameIndex lists the whole panel because of a
// name it does not know: a client the panel does not have (core asks about one
// now and then) must not cost a full listing each time.
const relistEvery = 10 * time.Second

// nameIndex remembers which panel id a client name has, for a panel whose API
// addresses a client by an id of its own (S-UI: a number; Hiddify: a UUID) and
// can only find one by name in a listing of all of them. A hit is checked by
// the caller (the record it reads must carry the name); a miss, or a hit that
// turned out stale, lists the panel again, at most once per relistEvery.
type nameIndex[ID comparable] struct {
	mu     sync.Mutex
	ids    map[string]ID
	listed time.Time // when the last full listing finished
	now    func() time.Time
}

func newNameIndex[ID comparable]() *nameIndex[ID] {
	return &nameIndex[ID]{ids: map[string]ID{}, now: time.Now}
}

// get is the id remembered for name.
func (x *nameIndex[ID]) get(name string) (ID, bool) {
	x.mu.Lock()
	defer x.mu.Unlock()
	id, ok := x.ids[name]
	return id, ok
}

func (x *nameIndex[ID]) put(name string, id ID) {
	x.mu.Lock()
	x.ids[name] = id
	x.mu.Unlock()
}

func (x *nameIndex[ID]) forget(name string) {
	x.mu.Lock()
	delete(x.ids, name)
	x.mu.Unlock()
}

// stale drops what a full listing found, so the next lookup of a name that
// is not remembered lists the panel again (a create that failed may have met a
// client made since that listing).
func (x *nameIndex[ID]) stale() {
	x.mu.Lock()
	x.listed = time.Time{}
	x.mu.Unlock()
}

// refresh finds name by listing the panel (list returns every name with its
// id), unless a listing finished after since and less than relistEvery ago:
// then its answer stands. A plain miss passes the zero time; a caller that found
// a remembered id stale passes the time it looked, so only a listing made after
// that counts.
func (x *nameIndex[ID]) refresh(ctx context.Context, name string, since time.Time, list func(context.Context) (map[string]ID, error)) (ID, bool, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if !x.listed.IsZero() && x.listed.After(since) && x.now().Sub(x.listed) < relistEvery {
		id, ok := x.ids[name]
		return id, ok, nil
	}
	all, err := list(ctx)
	if err != nil {
		var zero ID
		return zero, false, err
	}
	x.ids = all
	x.listed = x.now()
	id, ok := all[name]
	return id, ok, nil
}
