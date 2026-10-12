package jsonl_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/session"
	"github.com/sonnes/gopi/pkg/session/jsonl"
)

func record(id string) session.Session {
	return session.Session{
		ID:        id,
		CreatedAt: time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC),
	}
}

func entry(id, text string) session.Entry {
	e := session.Text(text)
	e.ID = id
	e.CreatedAt = time.Date(2026, 9, 23, 12, 0, 1, 0, time.UTC)
	return e
}

func TestStoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := jsonl.New(filepath.Join(dir, "sessions"))
	require.NoError(t, err, "New creates the directory")

	rec := record("s1")
	require.NoError(t, store.Create(ctx, rec))
	assert.ErrorIs(t, store.Create(ctx, rec), session.ErrExists)

	require.NoError(t, store.Append(ctx, "s1", entry("e1", "a")))
	require.NoError(t, store.Append(ctx, "s1", entry("e2", "b")))

	reopened, err := jsonl.New(filepath.Join(dir, "sessions"))
	require.NoError(t, err)
	snap, err := reopened.Load(ctx, "s1")
	require.NoError(t, err)
	assert.Equal(t, rec, snap.Session)
	require.Len(t, snap.Entries, 2)
	assert.Equal(t, "e1", snap.Entries[0].ID)
	assert.Equal(t, "e2", snap.Entries[1].ID)
	assert.Equal(t, "b", snap.Entries[1].Message.Text())

	data, err := os.ReadFile(filepath.Join(dir, "sessions", "s1.jsonl"))
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	assert.Len(t, lines, 3, "one line for the record, one per entry")
	assert.Contains(t, lines[0], `{"type":"session_init","id":"s1",`)
	assert.Contains(t, lines[1], `{"type":"message","id":"e1",`)
}

func TestStoreLoadsMinimalFile(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	file := strings.Join([]string{
		`{"type":"session_init","id":"s1","parent_id":"p0","created_at":"2026-09-23T12:00:00Z"}`,
		`{"type":"message","id":"e1","created_at":"2026-09-23T12:00:01Z","message":{"role":"user","content":[{"type":"text","text":"hi"}],"origin":"harness/x"}}`,
		`{"type":"message","id":"e2","parent_id":"e1","created_at":"2026-09-23T12:00:02Z","message":{"role":"assistant","content":[{"type":"text","text":"hello"}],"provider":"anthropic"}}`,
	}, "\n") + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "s1.jsonl"), []byte(file), 0o644))

	store, err := jsonl.New(dir)
	require.NoError(t, err)
	snap, err := store.Load(ctx, "s1")
	require.NoError(t, err, "the old parent_id fields do not stop the load")

	assert.Equal(t, "s1", snap.Session.ID)
	require.Len(t, snap.Entries, 2)
	assert.Equal(t, "hi", snap.Entries[0].Message.Text())
	assert.Equal(t, "hello", snap.Entries[1].Message.Text())
}

func TestStoreValues(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := jsonl.New(dir)
	require.NoError(t, err)
	require.NoError(t, store.Create(ctx, record("s1")))

	title := session.StoredValue{
		Namespace: "app.title",
		Data:      json.RawMessage(`"fix login"`),
	}
	label := session.StoredValue{
		Namespace: "app.label",
		Key:       "e1",
		Data:      json.RawMessage(`null`),
	}
	require.NoError(t, store.SetValue(ctx, "s1", label))
	require.NoError(t, store.SetValue(ctx, "s1", session.StoredValue{
		Namespace: "app.title",
		Data:      json.RawMessage(`"old"`),
	}))
	require.NoError(t, store.SetValue(ctx, "s1", title))
	require.NoError(t, store.Append(ctx, "s1", entry("e1", "a")))
	require.NoError(t, store.SetValue(ctx, "s1", session.StoredValue{
		Namespace: "app.gone",
		Data:      json.RawMessage(`1`),
	}))
	require.NoError(t, store.DeleteValue(ctx, "s1", "app.gone", ""))

	snap, err := store.Load(ctx, "s1")
	require.NoError(t, err)
	assert.ElementsMatch(t, []session.StoredValue{title, label}, snap.Values)
	require.Len(t, snap.Entries, 1)

	data, err := os.ReadFile(filepath.Join(dir, "s1.jsonl"))
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	assert.Equal(t, `{"type":"value","op":"set","namespace":"app.label","key":"e1","value":null}`, lines[1])
	assert.Equal(t, `{"type":"value","op":"delete","namespace":"app.gone","key":""}`, lines[6])

	assert.ErrorIs(t, store.SetValue(ctx, "nope", title), session.ErrNotFound)
	assert.ErrorIs(t, store.DeleteValue(ctx, "nope", "app", ""), session.ErrNotFound)
}

func TestStoreIgnoresTornValueLine(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := jsonl.New(dir)
	require.NoError(t, err)
	require.NoError(t, store.Create(ctx, record("s1")))

	old := session.StoredValue{
		Namespace: "app.title",
		Data:      json.RawMessage(`"old"`),
	}
	require.NoError(t, store.SetValue(ctx, "s1", old))

	f, err := os.OpenFile(filepath.Join(dir, "s1.jsonl"), os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, err = f.WriteString(`{"type":"value","op":"set","namespace":"app.title","key":"","value":"ne`)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	snap, err := store.Load(ctx, "s1")
	require.NoError(t, err)
	assert.Equal(t, []session.StoredValue{old}, snap.Values, "the value keeps its earlier state")
}

func TestStoreNotFound(t *testing.T) {
	ctx := context.Background()
	store, err := jsonl.New(t.TempDir())
	require.NoError(t, err)

	_, err = store.Load(ctx, "nope")
	assert.ErrorIs(t, err, session.ErrNotFound)
	assert.ErrorIs(t, store.Append(ctx, "nope", entry("e1", "a")), session.ErrNotFound)
}

func TestStoreRejectsUnsafeID(t *testing.T) {
	ctx := context.Background()
	store, err := jsonl.New(t.TempDir())
	require.NoError(t, err)

	for _, id := range []string{"", ".", "..", "a/b", `a\b`} {
		assert.Error(t, store.Create(ctx, record(id)), id)
	}
}

func TestStoreIgnoresTornLastLine(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := jsonl.New(dir)
	require.NoError(t, err)

	require.NoError(t, store.Create(ctx, record("s1")))
	require.NoError(t, store.Append(ctx, "s1", entry("e1", "a")))

	f, err := os.OpenFile(filepath.Join(dir, "s1.jsonl"), os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, err = f.WriteString(`{"type":"message","id":"e2","mess`)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	snap, err := store.Load(ctx, "s1")
	require.NoError(t, err)
	assert.Len(t, snap.Entries, 1, "a torn write is not an error")
}

func TestStoreRejectsBadLine(t *testing.T) {
	tests := []struct {
		name string
		line string
	}{
		{
			name: "a complete line that does not decode is corruption",
			line: "not json\n",
		},
		{
			name: "an entry type that gopi does not keep is an error, not a lost line",
			line: `{"type":"compaction","id":"c1","summary":"s"}` + "\n",
		},
		{
			name: "a value line with an unknown op is an error",
			line: `{"type":"value","op":"merge","namespace":"app","key":""}` + "\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			dir := t.TempDir()
			store, err := jsonl.New(dir)
			require.NoError(t, err)
			require.NoError(t, store.Create(ctx, record("s1")))

			f, err := os.OpenFile(filepath.Join(dir, "s1.jsonl"), os.O_APPEND|os.O_WRONLY, 0o644)
			require.NoError(t, err)
			_, err = f.WriteString(tt.line)
			require.NoError(t, err)
			require.NoError(t, f.Close())

			_, err = store.Load(ctx, "s1")
			assert.Error(t, err)
		})
	}
}

func TestStoreAppendsAfterTornLine(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := jsonl.New(dir)
	require.NoError(t, err)

	require.NoError(t, store.Create(ctx, record("s1")))
	require.NoError(t, store.Append(ctx, "s1", entry("e1", "a")))

	f, err := os.OpenFile(filepath.Join(dir, "s1.jsonl"), os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, err = f.WriteString(`{"type":"message","id":"e2","mess`)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	_, err = store.Load(ctx, "s1")
	require.NoError(t, err)
	require.NoError(t, store.Append(ctx, "s1", entry("e3", "c")))

	snap, err := store.Load(ctx, "s1")
	require.NoError(t, err, "the append after a torn line starts on a new line")
	require.Len(t, snap.Entries, 2)
	assert.Equal(t, "e1", snap.Entries[0].ID)
	assert.Equal(t, "e3", snap.Entries[1].ID)
}

func TestStoreRoundTripsCustomEntry(t *testing.T) {
	ctx := context.Background()
	store, err := jsonl.New(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, store.Create(ctx, record("s1")))

	e, err := session.Custom("compaction", map[string]string{"summary": "s"})
	require.NoError(t, err)
	e.ID = "c1"
	require.NoError(t, store.Append(ctx, "s1", e))

	snap, err := store.Load(ctx, "s1")
	require.NoError(t, err)
	require.Len(t, snap.Entries, 1)
	assert.Equal(t, "compaction", snap.Entries[0].Kind)
	assert.JSONEq(t, `{"summary":"s"}`, string(snap.Entries[0].Data))
}

func TestStoreAppendsAfterLineWithNoNewline(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	file := `{"type":"session_init","id":"s1","created_at":"2026-09-23T12:00:00Z"}` + "\n" +
		`{"type":"message","id":"e1","created_at":"2026-09-23T12:00:01Z","message":{"role":"user","content":[{"type":"text","text":"a"}]}}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "s1.jsonl"), []byte(file), 0o644))
	store, err := jsonl.New(dir)
	require.NoError(t, err)

	_, err = store.Load(ctx, "s1")
	require.NoError(t, err)
	require.NoError(t, store.Append(ctx, "s1", entry("e2", "b")))

	snap, err := store.Load(ctx, "s1")
	require.NoError(t, err)
	require.Len(t, snap.Entries, 2, "a whole last line stays")
	assert.Equal(t, "e1", snap.Entries[0].ID)
	assert.Equal(t, "e2", snap.Entries[1].ID)
}

func TestStoreAppendsAfterRecordWithNoNewline(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	file := `{"type":"session_init","id":"s1","created_at":"2026-09-23T12:00:00Z"}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "s1.jsonl"), []byte(file), 0o644))
	store, err := jsonl.New(dir)
	require.NoError(t, err)

	_, err = store.Load(ctx, "s1")
	require.NoError(t, err)
	require.NoError(t, store.Append(ctx, "s1", entry("e1", "a")))

	snap, err := store.Load(ctx, "s1")
	require.NoError(t, err, "the append starts on a new line")
	require.Len(t, snap.Entries, 1)
}

func TestStoreTornRecordIsNotFound(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := jsonl.New(dir)
	require.NoError(t, err)
	path := filepath.Join(dir, "s1.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(`{"type":"session_init","id":"s`), 0o644))

	_, err = store.Load(ctx, "s1")
	assert.ErrorIs(t, err, session.ErrNotFound, "a crash in Create leaves nothing to load")

	require.NoError(t, store.Create(ctx, record("s1")), "Create replaces a torn record")
	snap, err := store.Load(ctx, "s1")
	require.NoError(t, err)
	assert.Equal(t, record("s1"), snap.Session)
}

func TestStoreCreateKeepsAWholeRecord(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := jsonl.New(dir)
	require.NoError(t, err)
	require.NoError(t, store.Create(ctx, record("s1")))
	require.NoError(t, store.Append(ctx, "s1", entry("e1", "a")))

	assert.ErrorIs(t, store.Create(ctx, record("s1")), session.ErrExists)
	snap, err := store.Load(ctx, "s1")
	require.NoError(t, err)
	assert.Len(t, snap.Entries, 1)
}
