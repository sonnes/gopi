package resume_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent/internal/resume"
	"github.com/sonnes/gopi/pkg/session"
)

func TestSaveThenGet(t *testing.T) {
	ctx := context.Background()
	store := session.NewMemoryStore()
	log, err := session.Open(ctx, store, "s1")
	require.NoError(t, err)

	id := resume.New("claude")
	got, err := id.Get(log)
	require.NoError(t, err)
	assert.Empty(t, got, "a new session has no ID")

	require.NoError(t, id.Save(ctx, log, "sess-1"))
	got, err = id.Get(log)
	require.NoError(t, err)
	assert.Equal(t, "sess-1", got)

	again, err := session.Open(ctx, store, "s1")
	require.NoError(t, err)
	got, err = id.Get(again)
	require.NoError(t, err)
	assert.Equal(t, "sess-1", got, "the store keeps the ID")
}

func TestSaveIgnoresEmptyID(t *testing.T) {
	ctx := context.Background()
	log := session.New()
	id := resume.New("codex")
	require.NoError(t, id.Save(ctx, log, "th1"))

	require.NoError(t, id.Save(ctx, log, ""))
	got, err := id.Get(log)
	require.NoError(t, err)
	assert.Equal(t, "th1", got, "an empty ID does not clear the stored one")
}

func TestSaveOutlivesCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	log := session.New()
	id := resume.New("claude")

	require.NoError(t, id.Save(ctx, log, "sess-1"))
	got, err := id.Get(log)
	require.NoError(t, err)
	assert.Equal(t, "sess-1", got)
}

func TestEachCLIHasItsOwnID(t *testing.T) {
	ctx := context.Background()
	log := session.New()
	require.NoError(t, resume.New("claude").Save(ctx, log, "sess-1"))

	got, err := resume.New("codex").Get(log)
	require.NoError(t, err)
	assert.Empty(t, got)
}
