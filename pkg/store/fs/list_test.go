package fs_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/session"
	storefs "github.com/sonnes/gopi/pkg/store/fs"
)

func TestListGivesEachSessionWithItsValues(t *testing.T) {
	ctx := context.Background()
	store, err := storefs.Sessions(t.TempDir())
	require.NoError(t, err)

	later := session.Session{
		ID:        "b",
		CreatedAt: time.Date(2026, 9, 23, 13, 0, 0, 0, time.UTC),
	}
	require.NoError(t, store.Create(ctx, later))
	require.NoError(t, store.Create(ctx, record("a")))
	require.NoError(t, store.Append(ctx, "a", entry("e1", "hi")))
	title := session.StoredValue{
		Namespace: "app",
		Key:       "title",
		Data:      json.RawMessage(`"Fix the build"`),
	}
	require.NoError(t, store.SetValue(ctx, "a", title))

	got, err := store.List(ctx)
	require.NoError(t, err)

	want := []session.Snapshot{
		{
			Session: record("a"),
			Values:  []session.StoredValue{title},
		},
		{
			Session: later,
		},
	}
	assert.Equal(t, want, got)
}

func TestListOfAnEmptyStoreIsEmpty(t *testing.T) {
	store, err := storefs.Sessions(t.TempDir())
	require.NoError(t, err)

	got, err := store.List(context.Background())
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestListSkipsFilesThatAreNotSessions(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := storefs.Sessions(dir)
	require.NoError(t, err)
	require.NoError(t, store.Create(ctx, record("a")))

	notes := filepath.Join(dir, "notes.txt")
	require.NoError(t, os.WriteFile(notes, []byte("hello\n"), 0o644))
	torn := filepath.Join(dir, "torn.jsonl")
	require.NoError(t, os.WriteFile(torn, []byte(`{"type":"session_init","id":"to`), 0o644))
	require.NoError(t, os.Mkdir(filepath.Join(dir, "sub.jsonl"), 0o755))
	export := filepath.Join(dir, "export.jsonl")
	require.NoError(t, os.WriteFile(export, []byte(`{"type":"message","id":"e1"}`+"\n"), 0o644))
	require.NoError(t, store.Create(ctx, record("corrupt")))
	corrupt, err := os.OpenFile(filepath.Join(dir, "corrupt.jsonl"), os.O_APPEND|os.O_WRONLY, 0)
	require.NoError(t, err)
	_, err = corrupt.WriteString("not json\n")
	require.NoError(t, err)
	require.NoError(t, corrupt.Close())

	got, err := store.List(ctx)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "a", got[0].Session.ID)
}

func TestListDoesNotRepairATornLine(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := storefs.Sessions(dir)
	require.NoError(t, err)
	require.NoError(t, store.Create(ctx, record("a")))

	path := filepath.Join(dir, "a.jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	require.NoError(t, err)
	_, err = f.WriteString(`{"type":"mess`)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	before, err := os.ReadFile(path)
	require.NoError(t, err)

	got, err := store.List(ctx)
	require.NoError(t, err)
	require.Len(t, got, 1)

	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, before, after)
}
