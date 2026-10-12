package jsonl

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/session"
)

// halfWrite makes each write stop after half of its bytes, as a full
// disk does, until the test ends.
func halfWrite(t *testing.T) {
	t.Helper()
	write = func(f *os.File, b []byte) (int, error) {
		n, err := f.Write(b[:len(b)/2])
		if err != nil {
			return n, err
		}
		return n, syscall.ENOSPC
	}
	t.Cleanup(func() { write = (*os.File).Write })
}

func TestAppendFailureLeavesFile(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := New(dir)
	require.NoError(t, err)
	rec := session.Session{
		ID:        "s1",
		CreatedAt: time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC),
	}
	require.NoError(t, store.Create(ctx, rec))
	require.NoError(t, store.Append(ctx, "s1", session.Text("a")))
	path := filepath.Join(dir, "s1.jsonl")
	before, err := os.ReadFile(path)
	require.NoError(t, err)

	halfWrite(t)
	assert.Error(t, store.Append(ctx, "s1", session.Text("b")))
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after), "a failed append writes nothing")
}

func TestCreateFailureFreesTheID(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := New(dir)
	require.NoError(t, err)
	rec := session.Session{
		ID:        "s1",
		CreatedAt: time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC),
	}

	halfWrite(t)
	assert.Error(t, store.Create(ctx, rec))
	write = (*os.File).Write

	_, err = os.Stat(filepath.Join(dir, "s1.jsonl"))
	assert.ErrorIs(t, err, os.ErrNotExist, "a failed create leaves no file")
	assert.NoError(t, store.Create(ctx, rec))
}
