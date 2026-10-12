package session_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/session"
)

// countingStore counts the value writes that reach the store, and
// fails them if fail is set.
type countingStore struct {
	*session.MemoryStore
	writes int
	fail   bool
}

var errStore = errors.New("store failed")

func (s *countingStore) SetValue(ctx context.Context, id string, v session.StoredValue) error {
	s.writes++
	if s.fail {
		return errStore
	}
	return s.MemoryStore.SetValue(ctx, id, v)
}

func (s *countingStore) DeleteValue(ctx context.Context, id, namespace, key string) error {
	s.writes++
	if s.fail {
		return errStore
	}
	return s.MemoryStore.DeleteValue(ctx, id, namespace, key)
}

var title = session.Value[string]{Namespace: "app.title"}

func TestValueSetGetResume(t *testing.T) {
	ctx := context.Background()
	store := session.NewMemoryStore()
	l, err := session.Open(ctx, store, "s1")
	require.NoError(t, err)

	_, ok, err := title.Get(l)
	require.NoError(t, err)
	assert.False(t, ok, "a value that is not set")

	require.NoError(t, title.Set(ctx, l, "fix login"))
	got, ok, err := title.Get(l)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "fix login", got)

	again, err := session.Open(ctx, store, "s1")
	require.NoError(t, err)
	got, ok, err = title.Get(again)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "fix login", got)

	assert.Empty(t, again.Entries(), "a value is not an entry")
	assert.Empty(t, again.Messages(), "the model never reads a value")
}

func TestValueKeys(t *testing.T) {
	ctx := context.Background()
	l := session.New()
	label := func(id string) session.Value[string] {
		return session.Value[string]{
			Namespace: "app.label",
			Key:       id,
		}
	}

	require.NoError(t, label("e1").Set(ctx, l, "one"))
	require.NoError(t, label("e2").Set(ctx, l, "two"))

	got, _, err := label("e1").Get(l)
	require.NoError(t, err)
	assert.Equal(t, "one", got)
	got, _, err = label("e2").Get(l)
	require.NoError(t, err)
	assert.Equal(t, "two", got)
}

func TestValueDelete(t *testing.T) {
	ctx := context.Background()
	store := session.NewMemoryStore()
	l, err := session.Open(ctx, store, "s1")
	require.NoError(t, err)

	require.NoError(t, title.Delete(ctx, l), "a delete of a value that is not set does nothing")
	require.NoError(t, title.Set(ctx, l, "x"))
	require.NoError(t, title.Delete(ctx, l))

	_, ok, err := title.Get(l)
	require.NoError(t, err)
	assert.False(t, ok)

	again, err := session.Open(ctx, store, "s1")
	require.NoError(t, err)
	_, ok, err = title.Get(again)
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestValueNullIsSet(t *testing.T) {
	ctx := context.Background()
	l := session.New()
	model := session.Value[*string]{Namespace: "app.model"}

	require.NoError(t, model.Set(ctx, l, nil))
	got, ok, err := model.Get(l)
	require.NoError(t, err)
	assert.True(t, ok, "JSON null is a set value")
	assert.Nil(t, got)
}

func TestValueWrongType(t *testing.T) {
	ctx := context.Background()
	l := session.New()
	require.NoError(t, title.Set(ctx, l, "x"))

	count := session.Value[int]{Namespace: "app.title"}
	_, _, err := count.Get(l)
	assert.Error(t, err)
}

func TestValueRequiresNamespace(t *testing.T) {
	ctx := context.Background()
	store := &countingStore{MemoryStore: session.NewMemoryStore()}
	l, err := session.Open(ctx, store, "s1")
	require.NoError(t, err)

	bad := session.Value[string]{Key: "k"}
	assert.Error(t, bad.Set(ctx, l, "x"))
	assert.Error(t, bad.Delete(ctx, l))
	_, _, err = bad.Get(l)
	assert.Error(t, err)
	assert.Zero(t, store.writes, "a bad address makes no store call")
}

func TestValueSetFailureKeepsValue(t *testing.T) {
	ctx := context.Background()
	store := &countingStore{MemoryStore: session.NewMemoryStore()}
	l, err := session.Open(ctx, store, "s1")
	require.NoError(t, err)
	require.NoError(t, title.Set(ctx, l, "old"))

	store.fail = true
	assert.ErrorIs(t, title.Set(ctx, l, "new"), errStore)
	assert.ErrorIs(t, title.Delete(ctx, l), errStore)

	got, ok, err := title.Get(l)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "old", got)
}

func TestValueEncodeFailure(t *testing.T) {
	ctx := context.Background()
	store := &countingStore{MemoryStore: session.NewMemoryStore()}
	l, err := session.Open(ctx, store, "s1")
	require.NoError(t, err)

	fn := session.Value[func()]{Namespace: "app.fn"}
	assert.Error(t, fn.Set(ctx, l, func() {}))
	assert.Zero(t, store.writes, "a value that does not encode makes no store call")
}

func TestReadReturnsTheValueOfASnapshot(t *testing.T) {
	ctx := context.Background()
	store := session.NewMemoryStore()
	log, err := session.Open(ctx, store, "s1")
	require.NoError(t, err)
	require.NoError(t, title.Set(ctx, log, "Fix the build"))

	snap, err := store.Load(ctx, "s1")
	require.NoError(t, err)
	got, ok, err := title.Read(snap)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "Fix the build", got)

	other := session.Value[string]{Namespace: "app.label"}
	_, ok, err = other.Read(snap)
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestWriterSetsValues(t *testing.T) {
	ctx := context.Background()
	store := session.NewMemoryStore()
	l, err := session.Open(ctx, store, "s1")
	require.NoError(t, err)

	w := l.Writer()
	require.NoError(t, title.Set(ctx, w, "plan"))

	got, ok, err := title.Get(l)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "plan", got, "the log sees a write of its writer")

	again, err := session.Open(ctx, store, "s1")
	require.NoError(t, err)
	got, _, err = title.Get(again)
	require.NoError(t, err)
	assert.Equal(t, "plan", got, "the store has the write")

	require.NoError(t, title.Delete(ctx, w))
	_, ok, err = title.Get(w)
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestWriterCannotAppend(t *testing.T) {
	var w any = session.New().Writer()

	_, ok := w.(interface {
		Append(context.Context, ...session.Entry) ([]session.Entry, error)
	})
	assert.False(t, ok, "each entry of a run goes through the agent")
}
