package host_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/host"
	"github.com/sonnes/gopi/pkg/session"
)

func TestCreate(t *testing.T) {
	f := newFixture(t)
	_, events := f.watch(t)

	sess := f.create(t, host.Config{
		ID:   "s1",
		Dir:  "/tmp",
		Mode: agent.ModePlan,
	})

	assert.Equal(t, "s1", sess.ID)
	assert.Equal(t, host.StatusIdle, sess.Status)
	assert.Equal(t, "fake/m", sess.Model)
	assert.Equal(t, agent.ModePlan, sess.Mode)
	assert.Empty(t, sess.StopReason, "a session with no run has no stop reason")
	got := next(t, events, func(host.Event) bool { return true })
	assert.Equal(t, sess, got.Session)

	found, ok := f.h.Get("s1")
	require.True(t, ok)
	assert.Equal(t, sess, found)
	assert.Equal(t, []host.Session{sess}, f.h.List())

	c := f.lastConfig(t)
	assert.Equal(t, "s1", c.SessionID)
	assert.Equal(t, "/tmp", c.Dir)
	assert.Equal(t, agent.ModePlan, c.Mode)
	assert.NotNil(t, c.Approve, "the host answers each call that asks")
}

func TestCreatedAt(t *testing.T) {
	f := newFixture(t)
	created := f.create(t, host.Config{ID: "s1"})
	_, events := f.watch(t)
	f.send(t, "s1", "a")
	ended := runEnd(t, events)

	f = f.restart(t)

	got, _ := f.h.Get("s1")
	snap, err := f.store.Load(context.Background(), "s1")
	require.NoError(t, err)
	assert.False(t, created.CreatedAt.IsZero())
	assert.Equal(t, created.CreatedAt, ended.CreatedAt, "a run changes only UpdatedAt")
	assert.True(t, created.CreatedAt.Equal(got.CreatedAt), "the time survives a restart")
	assert.True(t, created.CreatedAt.Equal(snap.Session.CreatedAt), "the record has the same time")
}

func TestCreateMakesAnID(t *testing.T) {
	f := newFixture(t)

	sess := f.create(t, host.Config{})

	assert.True(t, strings.HasPrefix(sess.ID, "s_"), sess.ID)
}

func TestCreateATakenID(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.create(t, host.Config{ID: "s1"})
	require.NoError(t, f.store.Create(ctx, session.Session{ID: "s2"}))

	for _, id := range []string{"s1", "s2"} {
		_, err := f.h.Create(ctx, host.Config{ID: id, Model: "fake/m"})
		assert.ErrorIs(t, err, host.ErrExists, id)
	}
}

func TestCreateAnUnknownModel(t *testing.T) {
	f := newFixture(t)

	_, err := f.h.Create(context.Background(), host.Config{ID: "s1", Model: "nope/m"})

	require.Error(t, err)
	_, ok := f.h.Get("s1")
	assert.False(t, ok, "a config that no driver takes makes no session")
}

func TestCreateWhenTheDriverFails(t *testing.T) {
	f := newFixture(t)
	f.driver.setErr(errors.New("no agent"))

	sess, err := f.h.Create(context.Background(), host.Config{ID: "s1", Model: "fake/m"})

	assert.ErrorContains(t, err, "no agent")
	assert.Equal(t, host.StatusIdle, sess.Status)
	assert.Equal(t, host.StopError, sess.StopReason)
	assert.Contains(t, sess.Error, "no agent")

	f.driver.setErr(nil)
	_, events := f.watch(t)
	f.send(t, "s1", "a")
	assert.Equal(t, host.StopEndTurn, runEnd(t, events).StopReason, "the next message starts the agent again")
}

func TestAnUnknownSession(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	_, ok := f.h.Get("s1")
	assert.False(t, ok)
	_, err := f.h.Log(ctx, "s1")
	assert.ErrorIs(t, err, host.ErrNotFound)
	_, _, err = f.h.Stream(ctx, "s1")
	assert.ErrorIs(t, err, host.ErrNotFound)
	assert.ErrorIs(t, f.h.Send(ctx, "s1", host.Text("a")), host.ErrNotFound)
	assert.ErrorIs(t, f.h.Interrupt(ctx, "s1"), host.ErrNotFound)
	assert.ErrorIs(t, f.h.Confirm(ctx, "s1", "call_1", agent.Decision{}), host.ErrNotFound)
}

func TestEventsStateIsACopy(t *testing.T) {
	f := newFixture(t)
	f.create(t, host.Config{ID: "s1"})
	state, _ := f.h.Events(context.Background())

	f.create(t, host.Config{ID: "s2"})

	assert.Contains(t, state.Sessions, "s1")
	assert.NotContains(t, state.Sessions, "s2", "a later event does not change the state of a reader")
}

func TestHostClose(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.create(t, host.Config{ID: "s1"})
	_, events := f.watch(t)

	require.NoError(t, f.h.Close())
	require.NoError(t, f.h.Close(), "a second Close does nothing")

	assert.Empty(t, drain(t, events))
	_, err := f.h.Create(ctx, host.Config{ID: "s2", Model: "fake/m"})
	assert.ErrorIs(t, err, host.ErrClosed)
	assert.ErrorIs(t, f.h.Send(ctx, "s1", host.Text("a")), host.ErrClosed)
}
