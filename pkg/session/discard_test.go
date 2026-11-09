package session_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/session"
)

func TestDiscard(t *testing.T) {
	ctx := t.Context()
	var store session.Discard

	require.NoError(t, store.Create(ctx, session.Session{ID: "s1"}))
	require.NoError(t, store.Append(ctx, "s1", session.Text("a")))
	require.NoError(t, store.SetValue(ctx, "s1", session.StoredValue{Namespace: "n", Key: "k"}))
	require.NoError(t, store.DeleteValue(ctx, "s1", "n", "k"))

	_, err := store.Load(ctx, "s1")
	assert.ErrorIs(t, err, session.ErrNotFound, "the store keeps no record")
}

func TestDiscardLogKeepsItsEntriesInMemory(t *testing.T) {
	ctx := t.Context()

	log, err := session.Open(ctx, session.Discard{}, "s1")
	require.NoError(t, err)
	_, err = log.Append(ctx, session.Text("a"))
	require.NoError(t, err)

	msgs := log.Messages()
	require.Len(t, msgs, 1)
	assert.Equal(t, "a", msgs[0].Text())

	again, err := session.Open(ctx, session.Discard{}, "s1")
	require.NoError(t, err)
	assert.Empty(t, again.Messages(), "a new log of the store has no entry")
}
