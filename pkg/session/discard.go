package session

import "context"

// Discard is a [Storer] that keeps nothing. Each write succeeds and
// does nothing. Load returns [ErrNotFound], so [Open] starts a new log,
// and the log keeps its entries in memory.
type Discard struct{}

var _ Storer = Discard{}

// Create does nothing.
func (Discard) Create(context.Context, Session) error { return nil }

// Load returns [ErrNotFound].
func (Discard) Load(context.Context, string) (Snapshot, error) { return Snapshot{}, ErrNotFound }

// Append does nothing.
func (Discard) Append(context.Context, string, ...Entry) error { return nil }

// SetValue does nothing.
func (Discard) SetValue(context.Context, string, StoredValue) error { return nil }

// DeleteValue does nothing.
func (Discard) DeleteValue(context.Context, string, string, string) error { return nil }
