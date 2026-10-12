package session_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/session"
)

func texts(entries []session.Entry) []string {
	var out []string
	for _, e := range entries {
		out = append(out, e.Message.Text())
	}
	return out
}

func TestOpenCreatesThenResumes(t *testing.T) {
	ctx := context.Background()
	store := session.NewMemoryStore()

	l, err := session.Open(ctx, store, "s1")
	require.NoError(t, err)
	assert.Equal(t, "s1", l.Session().ID)
	assert.False(t, l.Session().CreatedAt.IsZero())
	assert.Empty(t, l.Entries())

	_, err = l.Append(ctx, session.Text("a"), session.Text("b"))
	require.NoError(t, err)

	again, err := session.Open(ctx, store, "s1")
	require.NoError(t, err)
	assert.Equal(t, l.Session(), again.Session())
	assert.Equal(t, []string{"a", "b"}, texts(again.Entries()))
}

func TestOpenRequiresID(t *testing.T) {
	_, err := session.Open(context.Background(), session.NewMemoryStore(), "")
	assert.Error(t, err)
}

func TestNewIsUnsaved(t *testing.T) {
	l := session.New()
	_, err := l.Append(context.Background(), session.Text("a"))
	require.NoError(t, err)
	assert.Equal(t, []string{"a"}, texts(l.Entries()))
}

func TestAppendSetsIDAndTime(t *testing.T) {
	ctx := context.Background()
	l := session.New()

	got, err := l.Append(ctx, session.Text("a"), session.Text("b"))
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.NotEmpty(t, got[0].ID)
	assert.NotEqual(t, got[0].ID, got[1].ID)
	assert.False(t, got[1].CreatedAt.IsZero())
	assert.Equal(t, got, l.Entries())
}

func TestAppendSkipsEphemeral(t *testing.T) {
	ctx := context.Background()
	store := session.NewMemoryStore()
	l, err := session.Open(ctx, store, "s1")
	require.NoError(t, err)

	got, err := l.Append(ctx,
		session.Text("reminder", session.Ephemeral()),
		session.Text("hi"),
		session.Text("skill"),
	)
	require.NoError(t, err)
	require.Len(t, got, 3, "Append returns every entry, ephemeral included")
	assert.NotEmpty(t, got[0].ID)

	assert.Equal(t, []string{"hi", "skill"}, texts(l.Entries()))

	snap, err := store.Load(ctx, "s1")
	require.NoError(t, err)
	assert.Equal(t, []string{"hi", "skill"}, texts(snap.Entries))
}

func TestMessagesRepairsDanglingToolCall(t *testing.T) {
	ctx := context.Background()
	l := session.New()

	call := ai.ToolCall{ID: "c1", Name: "read", Input: json.RawMessage(`{}`)}
	_, err := l.Append(ctx,
		session.Text("hi"),
		session.NewEntry(ai.AssistantMessage(call)),
	)
	require.NoError(t, err)

	msgs := l.Messages()
	require.Len(t, msgs, 3)
	assert.Equal(t, ai.RoleToolResult, msgs[2].Role)
	assert.Equal(t, "c1", msgs[2].ToolCallID)
	assert.True(t, msgs[2].IsError)

	result := ai.ToolResultMessage("c1", ai.Text{Text: "ok"})
	_, err = l.Append(ctx, session.NewEntry(result))
	require.NoError(t, err)
	msgs = l.Messages()
	require.Len(t, msgs, 3)
	assert.False(t, msgs[2].IsError, "a real result needs no repair")
}

func TestMessagesSkipsCustomEntries(t *testing.T) {
	ctx := context.Background()
	l := session.New()

	marker, err := session.Custom("compaction", map[string]string{"summary": "s"})
	require.NoError(t, err)
	_, err = l.Append(ctx,
		session.Text("a"),
		marker,
		session.Text("b"),
	)
	require.NoError(t, err)

	assert.Len(t, l.Entries(), 3, "the log keeps the custom entry")
	msgs := l.Messages()
	require.Len(t, msgs, 2)
	assert.Equal(t, "a", msgs[0].Text())
	assert.Equal(t, "b", msgs[1].Text())
}

// lateStore is a store that another caller fills between the Load and
// the Create of [session.Open].
type lateStore struct {
	*session.MemoryStore
	loads int
}

func (s *lateStore) Load(ctx context.Context, id string) (session.Snapshot, error) {
	s.loads++
	if s.loads == 1 {
		return session.Snapshot{}, session.ErrNotFound
	}
	return s.MemoryStore.Load(ctx, id)
}

func TestOpenResumesASessionCreatedFirst(t *testing.T) {
	ctx := context.Background()
	mem := session.NewMemoryStore()
	first, err := session.Open(ctx, mem, "s1")
	require.NoError(t, err)
	_, err = first.Append(ctx, session.Text("a"))
	require.NoError(t, err)

	l, err := session.Open(ctx, &lateStore{MemoryStore: mem}, "s1")
	require.NoError(t, err)
	assert.Equal(t, first.Session(), l.Session())
	assert.Equal(t, []string{"a"}, texts(l.Entries()))
}
