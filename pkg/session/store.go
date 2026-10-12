package session

import (
	"context"
	"encoding/json"
	"errors"
)

// ErrNotFound marks an unknown session ID.
var ErrNotFound = errors.New("session: not found")

// ErrExists marks a session ID that is taken. [Storer.Create] returns it.
var ErrExists = errors.New("session: already exists")

// Storer keeps session records, their append-only entry logs, and their
// values. It is the whole contract that a [Log] needs.
//
// The methods act on sessions, so their names do not repeat the word.
type Storer interface {
	// Create writes the record of s. If s.ID is taken, Create returns
	// [ErrExists].
	Create(ctx context.Context, s Session) error

	// Load returns the session id as the store holds it. If id is
	// unknown, Load returns [ErrNotFound]. One call returns everything
	// because [Open] needs everything.
	Load(ctx context.Context, id string) (Snapshot, error)

	// Append adds entries to the log of id. If id is unknown, Append
	// returns [ErrNotFound].
	Append(ctx context.Context, id string, entries ...Entry) error

	// SetValue replaces the value at the address of v in the session
	// id. If id is unknown, SetValue returns [ErrNotFound].
	SetValue(ctx context.Context, id string, v StoredValue) error

	// DeleteValue removes the value at namespace and key in the session
	// id. A delete of a value that is not set does nothing. If id is
	// unknown, DeleteValue returns [ErrNotFound].
	DeleteValue(ctx context.Context, id, namespace, key string) error
}

// Snapshot is a session as [Storer.Load] finds it.
type Snapshot struct {
	Session Session
	// Entries holds the log in append order.
	Entries []Entry
	// Values holds the current values, in no order.
	Values []StoredValue
}

// StoredValue is one value as a store keeps it. A store keeps Data as
// bytes and never decodes it. The type belongs to the [Value] in the
// code, so a store needs no schema.
type StoredValue struct {
	Namespace string
	Key       string
	Data      json.RawMessage
}
