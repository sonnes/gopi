package host_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/host"
)

func TestReleaseStopsTheAgentOfAnIdleSession(t *testing.T) {
	f := newFixture(t)
	f.driver.counts = true
	f.create(t, host.Config{ID: "s1"})
	_, events := f.watch(t)
	f.send(t, "s1", "a")
	runEnd(t, events)

	require.NoError(t, f.h.Release(context.Background(), "s1"))
	assert.Equal(t, 1, f.driver.closes(), "the agent stops")
	require.NoError(t, f.h.Release(context.Background(), "s1"), "a session with no agent has nothing to release")

	f.send(t, "s1", "b")
	runEnd(t, events)
	assert.Equal(t, 2, f.driver.starts(), "the next message starts the agent again")
	assert.Subset(t, f.model.texts(), []string{"a", "b"})
}

func TestReleaseOfABusySession(t *testing.T) {
	f, gate := gated(t)
	f.driver.counts = true
	f.create(t, host.Config{ID: "s1"})
	_, events := f.watch(t)
	f.send(t, "s1", "a")

	err := f.h.Release(context.Background(), "s1")

	require.ErrorIs(t, err, host.ErrBusy)
	assert.Equal(t, 0, f.driver.closes(), "the run keeps its agent")
	close(gate)
	runEnd(t, events)
}

func TestReleaseOfAnUnknownSession(t *testing.T) {
	f := newFixture(t)

	err := f.h.Release(context.Background(), "nope")

	require.ErrorIs(t, err, host.ErrNotFound)
}

func TestIdleTimeoutReleasesTheAgent(t *testing.T) {
	const idle = 20 * time.Millisecond
	f := newFixture(t, host.IdleTimeout(idle))
	f.driver.counts = true
	f.create(t, host.Config{ID: "s1"})
	_, events := f.watch(t)
	f.send(t, "s1", "a")
	runEnd(t, events)

	require.Eventually(t, func() bool {
		return f.driver.closes() == 1
	}, waitTimeout, time.Millisecond, "the idle agent stops")

	f.send(t, "s1", "b")
	runEnd(t, events)
	assert.Equal(t, 2, f.driver.starts(), "the next message starts the agent again")
}

func TestIdleTimeoutKeepsTheAgentOfARun(t *testing.T) {
	const idle = 20 * time.Millisecond
	f := newFixture(t, host.IdleTimeout(idle))
	gate := make(chan struct{})
	f.model.gate = gate
	f.driver.counts = true
	f.create(t, host.Config{ID: "s1"})
	_, events := f.watch(t)
	f.send(t, "s1", "a")

	time.Sleep(5 * idle)
	assert.Equal(t, 0, f.driver.closes(), "a run keeps its agent")
	close(gate)
	runEnd(t, events)
}
