package session

import (
	"context"
	"sync/atomic"
)

// Preloaded returns a store that answers the first Load of the session
// id with snap, and passes each other call to s. A caller that read the
// session already gives the snapshot to the next reader, so the store
// does not read the session a second time. A later Load reads s,
// because the session can change after snap.
func Preloaded(s Storer, id string, snap Snapshot) Storer {
	return &preloaded{
		Storer: s,
		id:     id,
		snap:   snap,
	}
}

type preloaded struct {
	Storer
	id   string
	snap Snapshot
	used atomic.Bool
}

func (s *preloaded) Load(ctx context.Context, id string) (Snapshot, error) {
	if id == s.id && s.used.CompareAndSwap(false, true) {
		return s.snap, nil
	}
	return s.Storer.Load(ctx, id)
}
