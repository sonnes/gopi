package session

import (
	"context"
	"slices"
	"sync"
)

// MemoryStore is a [Storer] in process memory. It is safe for concurrent
// use. It is enough for tests and for an agent that ends with the
// process.
type MemoryStore struct {
	mu       sync.Mutex
	sessions map[string]Session
	entries  map[string][]Entry
	values   map[string]map[address]StoredValue
}

var _ Storer = (*MemoryStore)(nil)

// address names one value in a session.
type address struct {
	namespace string
	key       string
}

// NewMemoryStore returns an empty [MemoryStore].
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		sessions: make(map[string]Session),
		entries:  make(map[string][]Entry),
		values:   make(map[string]map[address]StoredValue),
	}
}

// Create implements [Storer].
func (s *MemoryStore) Create(_ context.Context, rec Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.sessions[rec.ID]; ok {
		return ErrExists
	}
	s.sessions[rec.ID] = rec
	s.values[rec.ID] = make(map[address]StoredValue)
	return nil
}

// Load implements [Storer]. The returned slices are copies.
func (s *MemoryStore) Load(_ context.Context, id string) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, ok := s.sessions[id]
	if !ok {
		return Snapshot{}, ErrNotFound
	}

	var values []StoredValue
	for _, v := range s.values[id] {
		values = append(values, cloneValue(v))
	}
	snap := Snapshot{
		Session: rec,
		Entries: slices.Clone(s.entries[id]),
		Values:  values,
	}
	return snap, nil
}

// Append implements [Storer].
func (s *MemoryStore) Append(_ context.Context, id string, entries ...Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.sessions[id]; !ok {
		return ErrNotFound
	}
	s.entries[id] = append(s.entries[id], entries...)
	return nil
}

// SetValue implements [Storer]. It keeps a copy of v.Data.
func (s *MemoryStore) SetValue(_ context.Context, id string, v StoredValue) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.sessions[id]; !ok {
		return ErrNotFound
	}
	addr := address{
		namespace: v.Namespace,
		key:       v.Key,
	}
	s.values[id][addr] = cloneValue(v)
	return nil
}

// DeleteValue implements [Storer].
func (s *MemoryStore) DeleteValue(_ context.Context, id, namespace, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.sessions[id]; !ok {
		return ErrNotFound
	}
	addr := address{
		namespace: namespace,
		key:       key,
	}
	delete(s.values[id], addr)
	return nil
}

// cloneValue returns v with its own copy of Data, so a caller that
// changes the bytes cannot change a stored value.
func cloneValue(v StoredValue) StoredValue {
	v.Data = slices.Clone(v.Data)
	return v
}
