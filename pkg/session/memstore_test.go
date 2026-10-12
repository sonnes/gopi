package session_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/session"
)

func TestMemoryStore(t *testing.T) {
	ctx := context.Background()
	store := session.NewMemoryStore()
	rec := session.Session{
		ID:        "s1",
		CreatedAt: time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC),
	}

	require.NoError(t, store.Create(ctx, rec))
	assert.ErrorIs(t, store.Create(ctx, rec), session.ErrExists)

	snap, err := store.Load(ctx, "s1")
	require.NoError(t, err)
	assert.Equal(t, rec, snap.Session, "the store writes the record as it is")
	assert.Empty(t, snap.Entries)
	assert.Empty(t, snap.Values)

	e1 := session.Text("a")
	e1.ID = "e1"
	e2 := session.Text("b")
	e2.ID = "e2"
	require.NoError(t, store.Append(ctx, "s1", e1, e2))

	snap, err = store.Load(ctx, "s1")
	require.NoError(t, err)
	require.Len(t, snap.Entries, 2)
	assert.Equal(t, "e1", snap.Entries[0].ID)
	assert.Equal(t, "e2", snap.Entries[1].ID)

	_, err = store.Load(ctx, "nope")
	assert.ErrorIs(t, err, session.ErrNotFound)
	assert.ErrorIs(t, store.Append(ctx, "nope", e1), session.ErrNotFound)
}

func TestMemoryStoreValues(t *testing.T) {
	ctx := context.Background()
	store := session.NewMemoryStore()
	require.NoError(t, store.Create(ctx, session.Session{ID: "s1"}))

	a := session.StoredValue{
		Namespace: "app",
		Key:       "a",
		Data:      json.RawMessage(`"one"`),
	}
	b := session.StoredValue{
		Namespace: "app",
		Key:       "b",
		Data:      json.RawMessage(`2`),
	}
	require.NoError(t, store.SetValue(ctx, "s1", a))
	require.NoError(t, store.SetValue(ctx, "s1", b))

	a.Data = json.RawMessage(`"two"`)
	require.NoError(t, store.SetValue(ctx, "s1", a), "a later set replaces the value")
	require.NoError(t, store.DeleteValue(ctx, "s1", "app", "b"))
	require.NoError(t, store.DeleteValue(ctx, "s1", "app", "none"), "a delete of an unset value does nothing")

	snap, err := store.Load(ctx, "s1")
	require.NoError(t, err)
	assert.Equal(t, []session.StoredValue{a}, snap.Values)

	assert.ErrorIs(t, store.SetValue(ctx, "nope", a), session.ErrNotFound)
	assert.ErrorIs(t, store.DeleteValue(ctx, "nope", "app", "a"), session.ErrNotFound)
}
