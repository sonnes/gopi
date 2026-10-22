package host_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/host"
	"github.com/sonnes/gopi/pkg/session"
)

func TestStartNewSession(t *testing.T) {
	f := newFixture(t)
	_, events := f.watch(t)

	s := f.start(t, host.Spec{
		Dir:  "/tmp",
		Mode: agent.ModePlan,
	})

	assert.NotEmpty(t, s.ID())
	got := next(t, events, is[host.EventSessionStart]).(host.EventSessionStart)
	assert.Equal(t, host.EventSessionStart{
		Session: s.ID(),
		Model:   "fake/m",
		Mode:    agent.ModePlan,
		Dir:     "/tmp",
	}, got)
	found, ok := f.h.Session(s.ID())
	require.True(t, ok)
	assert.Same(t, s, found)

	state, _ := f.h.Events(context.Background())
	assert.Equal(t, host.StatusIdle, state.Sessions[s.ID()].Status())
	assert.Equal(t, agent.ModePlan, state.Sessions[s.ID()].Mode)
	assert.Equal(t, "/tmp", f.driver.configs[0].Dir)
	assert.Equal(t, agent.ModePlan, f.driver.configs[0].Mode)
	assert.Equal(t, s.ID(), f.driver.configs[0].Session)
}

func TestEventsStateIsACopy(t *testing.T) {
	f := newFixture(t)
	f.start(t, host.Spec{ID: "s1"})
	state, _ := f.h.Events(context.Background())

	f.start(t, host.Spec{ID: "s2"})

	assert.Contains(t, state.Sessions, "s1")
	assert.NotContains(t, state.Sessions, "s2", "a later event does not change the state of a reader")
}

func TestStartResumes(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	log, err := session.Open(ctx, f.store, "s1")
	require.NoError(t, err)
	_, err = log.Append(ctx, session.Text("hello"))
	require.NoError(t, err)
	_, events := f.watch(t)

	s := f.start(t, host.Spec{ID: "s1"})

	got := next(t, events, is[host.EventSessionStart]).(host.EventSessionStart)
	assert.True(t, got.Resumed)
	require.Len(t, s.Log().Messages(), 1)
	assert.Equal(t, "hello", s.Log().Messages()[0].Text())
}

// countingStore counts each Load.
type countingStore struct {
	session.Storer
	mu    sync.Mutex
	loads int
}

func (s *countingStore) Load(ctx context.Context, id string) (session.Snapshot, error) {
	s.mu.Lock()
	s.loads++
	s.mu.Unlock()
	return s.Storer.Load(ctx, id)
}

func TestStartResumeLoadsOnce(t *testing.T) {
	ctx := context.Background()
	store := &countingStore{Storer: session.NewMemoryStore()}
	log, err := session.Open(ctx, store, "s1")
	require.NoError(t, err)
	_, err = log.Append(ctx, session.Text("hello"))
	require.NoError(t, err)
	store.loads = 0
	f := newFixture(t, host.Store(store))

	s := f.start(t, host.Spec{ID: "s1"})

	assert.Len(t, s.Log().Messages(), 1)
	assert.Equal(t, 1, store.loads, "the driver gets the snapshot of the host")
}

func TestStartLiveID(t *testing.T) {
	f := newFixture(t)
	f.start(t, host.Spec{ID: "s1"})

	_, err := f.h.Start(context.Background(), host.Spec{
		ID:    "s1",
		Model: "fake/m",
	})
	assert.ErrorIs(t, err, host.ErrExists)
}

func TestStartDriverFails(t *testing.T) {
	f := newFixture(t)
	f.driver.err = errors.New("no agent")
	_, events := f.watch(t)

	_, err := f.h.Start(context.Background(), host.Spec{
		ID:    "s1",
		Model: "fake/m",
	})

	assert.ErrorContains(t, err, "no agent")
	_, ok := f.h.Session("s1")
	assert.False(t, ok)
	f.driver.err = nil
	s := f.start(t, host.Spec{ID: "s1"})
	got := next(t, events, is[host.EventSessionStart])
	assert.Equal(t, s.ID(), got.SessionID())
}

func TestStartUnknownModel(t *testing.T) {
	f := newFixture(t)

	_, err := f.h.Start(context.Background(), host.Spec{Model: "nope/m"})

	assert.Error(t, err)
}

func TestSessionClose(t *testing.T) {
	f := newFixture(t)
	s := f.start(t, host.Spec{ID: "s1"})
	_, events := f.watch(t)

	require.NoError(t, s.Close())
	require.NoError(t, s.Close())

	got := next(t, events, is[host.EventSessionEnd])
	assert.Equal(t, "s1", got.SessionID())
	_, ok := f.h.Session("s1")
	assert.False(t, ok)
	state, _ := f.h.Events(context.Background())
	assert.NotContains(t, state.Sessions, "s1")
	again := f.start(t, host.Spec{ID: "s1"})
	assert.NotSame(t, s, again)
}

func TestHostClose(t *testing.T) {
	f := newFixture(t)
	f.start(t, host.Spec{ID: "s1"})
	_, events := f.watch(t)

	require.NoError(t, f.h.Close())

	got := drain(t, events)
	require.Len(t, got, 1)
	assert.Equal(t, host.EventSessionEnd{Session: "s1"}, got[0])
	_, err := f.h.Start(context.Background(), host.Spec{Model: "fake/m"})
	assert.ErrorIs(t, err, host.ErrClosed)
}
