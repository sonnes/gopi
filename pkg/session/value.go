package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// Value names one value of type T in a session. Two Values with the
// same Namespace and Key name the same value. Bind the address and the
// type once, in a package variable or a function, and use it for every
// read and write:
//
//	var title = session.Value[string]{Namespace: "app.title"}
//
// A value is application state that lives with the session: a title, a
// model choice, or a label on an entry. The model never reads a value.
// To give the model a value, append it as an entry.
//
// Get, Set, and Delete are methods on Value and not on [Log], because a
// Go method cannot take its own type parameter. On Value, they keep T
// bound to the address.
type Value[T any] struct {
	// Namespace groups values. It must not be empty. The namespace
	// "gopi" and every namespace that starts with "gopi." are reserved
	// for this module.
	Namespace string
	// Key names one value in the namespace. An empty key names one value
	// for the whole session.
	Key string
}

// Get returns the value in r. If the value is not set, ok is false. A
// JSON null is a set value. If the stored JSON does not decode into T,
// Get returns an error.
func (v Value[T]) Get(r Reader) (x T, ok bool, err error) {
	addr, err := v.address()
	if err != nil {
		return x, false, err
	}
	data, ok := r.value(addr)
	if !ok {
		return x, false, nil
	}
	return v.decode(data)
}

// Read returns the value in snap, as [Value.Get] does for a log. A
// caller reads a snapshot from [Storer.Load] to learn a value of a
// session before it opens the session. A store can repair the session
// in Load, so call Load only when no other process writes the session.
func (v Value[T]) Read(snap Snapshot) (x T, ok bool, err error) {
	if _, err := v.address(); err != nil {
		return x, false, err
	}
	for _, sv := range snap.Values {
		if sv.Namespace == v.Namespace && sv.Key == v.Key {
			return v.decode(sv.Data)
		}
	}
	return x, false, nil
}

// decode returns data as a T.
func (v Value[T]) decode(data json.RawMessage) (x T, ok bool, err error) {
	if err := json.Unmarshal(data, &x); err != nil {
		return x, false, fmt.Errorf("session: decode value %q/%q: %w", v.Namespace, v.Key, err)
	}
	return x, true, nil
}

// Set encodes x as JSON and writes it through w. The store has the
// value when Set returns. If the encode or the store fails, the value
// does not change.
func (v Value[T]) Set(ctx context.Context, w Writer, x T) error {
	addr, err := v.address()
	if err != nil {
		return err
	}
	data, err := json.Marshal(x)
	if err != nil {
		return fmt.Errorf("session: encode value %q/%q: %w", v.Namespace, v.Key, err)
	}
	return w.setValue(ctx, addr, data)
}

// Delete removes the value through w. A delete of a value that is not
// set does nothing. If the store fails, the value does not change.
func (v Value[T]) Delete(ctx context.Context, w Writer) error {
	addr, err := v.address()
	if err != nil {
		return err
	}
	return w.deleteValue(ctx, addr)
}

// address returns the address of v, or an error if the namespace is
// empty.
func (v Value[T]) address() (address, error) {
	if v.Namespace == "" {
		return address{}, errors.New("session: value namespace is required")
	}
	addr := address{
		namespace: v.Namespace,
		key:       v.Key,
	}
	return addr, nil
}
