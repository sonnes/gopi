package session_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/session"
)

func TestPreloaded(t *testing.T) {
	ctx := context.Background()
	store := session.NewMemoryStore()
	require.NoError(t, store.Create(ctx, session.Session{ID: "s1"}))
	snap, err := store.Load(ctx, "s1")
	require.NoError(t, err)
	require.NoError(t, store.Append(ctx, "s1", session.Text("later")))

	pre := session.Preloaded(store, "s1", snap)

	first, err := pre.Load(ctx, "s1")
	require.NoError(t, err)
	assert.Empty(t, first.Entries, "the first load gives the snapshot")
	second, err := pre.Load(ctx, "s1")
	require.NoError(t, err)
	assert.Len(t, second.Entries, 1, "a later load reads the store")
}
