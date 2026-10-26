package host_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/host"
	"github.com/sonnes/gopi/pkg/session"
)

func TestTheSessionValue(t *testing.T) {
	f := newFixture(t)
	created := f.create(t, host.Config{ID: "s1"})
	stored := session.Value[host.Session]{
		Namespace: "gopi.host",
		Key:       "session",
	}

	snap, err := f.store.Load(context.Background(), "s1")
	require.NoError(t, err)

	got, ok, err := stored.Read(snap)
	require.NoError(t, err)
	require.True(t, ok, "the store keeps the session in gopi.host/session")
	assert.Equal(t, created, got)
}

func TestTheStatusOfARun(t *testing.T) {
	f := newFixture(t)
	f.create(t, host.Config{ID: "s1"})
	_, events := f.watch(t)

	f.send(t, "s1", "a")

	next(t, events, status(host.StatusRunning))
	end := runEnd(t, events)
	assert.Equal(t, host.StopEndTurn, end.StopReason)
	assert.Empty(t, end.Error)
	assert.Equal(t, 10, end.Usage.Input)
	got, _ := f.h.Get("s1")
	assert.Equal(t, end, got)
}

func TestSendReturnsWithTheSessionRunning(t *testing.T) {
	f, gate := gated(t)
	f.create(t, host.Config{ID: "s1"})

	f.send(t, "s1", "a")

	got, _ := f.h.Get("s1")
	assert.Equal(t, host.StatusRunning, got.Status, "a caller never sees idle between a message and its run")
	close(gate)
}

func TestARunThatFails(t *testing.T) {
	f := newFixture(t)
	f.model.fail = errors.New("model down")
	f.create(t, host.Config{ID: "s1"})
	_, events := f.watch(t)
	_, agentEvents := f.follow(t, "s1")

	f.send(t, "s1", "a")

	end := runEnd(t, events)
	assert.Equal(t, host.StatusIdle, end.Status, "a failed run is idle")
	assert.Equal(t, host.StopError, end.StopReason)
	assert.Contains(t, end.Error, "model down")
	agentEnd := next(t, agentEvents, is[agent.EventAgentEnd]).(agent.EventAgentEnd)
	assert.Contains(t, agentEnd.Error, "model down")
}

func TestInterrupt(t *testing.T) {
	f, gate := gated(t)
	f.create(t, host.Config{ID: "s1"})
	_, events := f.watch(t)
	f.send(t, "s1", "a")
	f.model.waitCalls(t, 1)

	require.NoError(t, f.h.Interrupt(context.Background(), "s1"))

	end := runEnd(t, events)
	assert.Equal(t, host.StopInterrupted, end.StopReason)
	assert.Empty(t, end.Error, "a stop is not a failure")
	close(gate)
	require.NoError(t, f.h.Interrupt(context.Background(), "s1"), "with no run, Interrupt does nothing")
}

func TestTheStatusSurvivesARestart(t *testing.T) {
	f, gate := gated(t)
	f.create(t, host.Config{ID: "s1"})
	f.send(t, "s1", "a")
	f.model.waitCalls(t, 1)

	// A second host on the store reads it as a program does after a
	// crash: the first host did not end the run.
	h := f.open(t)
	close(gate)

	got, ok := h.Get("s1")
	require.True(t, ok)
	assert.Equal(t, host.StatusIdle, got.Status)
	assert.Equal(t, host.StopRestarted, got.StopReason)
	assert.Equal(t, "fake/m", got.Model)
}

func TestCloseCutsARun(t *testing.T) {
	f, _ := gated(t)
	f.create(t, host.Config{ID: "s1"})
	_, events := f.watch(t)
	f.send(t, "s1", "a")
	f.model.waitCalls(t, 1)

	f = f.restart(t)

	assert.Equal(t, host.StopRestarted, runEnd(t, events).StopReason)
	got, _ := f.h.Get("s1")
	assert.Equal(t, host.StatusIdle, got.Status)
	assert.Equal(t, host.StopRestarted, got.StopReason)
}

func TestOpenReadsASessionWithNoStatus(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	log, err := session.Open(ctx, f.store, "s1")
	require.NoError(t, err)
	_, err = log.Append(ctx, session.Text("hello"))
	require.NoError(t, err)

	f = f.restart(t)

	got, ok := f.h.Get("s1")
	require.True(t, ok)
	assert.Equal(t, host.StatusIdle, got.Status)
	assert.Empty(t, got.StopReason)
	err = f.h.Send(ctx, "s1", host.Text("a"))
	assert.ErrorIs(t, err, host.ErrInvalid, "a session with no config cannot start an agent")
	got, _ = f.h.Get("s1")
	assert.Equal(t, host.StopError, got.StopReason)
}

func TestInterruptRightAfterSend(t *testing.T) {
	f, gate := gated(t)
	defer close(gate)
	f.create(t, host.Config{ID: "s1"})
	_, events := f.watch(t)

	f.send(t, "s1", "a")
	require.NoError(t, f.h.Interrupt(context.Background(), "s1"))

	assert.Equal(t, host.StopInterrupted, runEnd(t, events).StopReason, "a run is active as soon as Send returns")
}
