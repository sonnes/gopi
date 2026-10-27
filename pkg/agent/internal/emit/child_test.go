package emit_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/agent/internal/emit"
	"github.com/sonnes/gopi/pkg/session"
)

func TestOpenChildWritesTheParentOnce(t *testing.T) {
	ctx := context.Background()
	store := session.NewMemoryStore()
	ref := agent.ParentRef{
		SessionID:  "s1",
		ToolCallID: "c1",
	}

	log, err := emit.OpenChild(ctx, store, "s1.helper-01", ref)
	require.NoError(t, err)
	entries := len(log.Entries())
	again, err := emit.OpenChild(ctx, store, "s1.helper-01", ref)
	require.NoError(t, err)

	assert.Len(t, again.Entries(), entries, "a child that exists keeps its entries")
	snap, err := store.Load(ctx, "s1.helper-01")
	require.NoError(t, err)
	got, ok, err := agent.Parent.Read(snap)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, ref, got)
}

func TestOpenChildOfAnUnsavedParentIsUnsaved(t *testing.T) {
	ctx := context.Background()
	store := session.NewMemoryStore()

	log, err := emit.OpenChild(ctx, store, "", agent.ParentRef{})
	require.NoError(t, err)

	require.NotNil(t, log)
	list, err := store.List(ctx)
	require.NoError(t, err)
	assert.Empty(t, list)
}
