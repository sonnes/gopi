package host_test

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
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
	synctest.Test(t, func(t *testing.T) {
		const idle = time.Minute
		f := newFixture(t, host.IdleTimeout(idle))
		f.driver.counts = true
		f.create(t, host.Config{ID: "s1"})
		_, events := f.watch(t)
		f.send(t, "s1", "a")
		runEnd(t, events)

		time.Sleep(idle - time.Nanosecond)
		synctest.Wait()
		assert.Equal(t, 0, f.driver.closes(), "the agent runs until the idle time ends")

		time.Sleep(time.Nanosecond)
		synctest.Wait()
		assert.Equal(t, 1, f.driver.closes(), "the idle agent stops")

		f.send(t, "s1", "b")
		runEnd(t, events)
		assert.Equal(t, 2, f.driver.starts(), "the next message starts the agent again")
	})
}

func TestIdleTimeoutKeepsTheAgentOfARun(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const idle = time.Minute
		f, gate := gated(t, host.IdleTimeout(idle))
		f.driver.counts = true
		f.create(t, host.Config{ID: "s1"})
		_, events := f.watch(t)
		f.send(t, "s1", "a")

		time.Sleep(5 * idle)
		synctest.Wait()
		assert.Equal(t, 0, f.driver.closes(), "a run keeps its agent")
		close(gate)
		runEnd(t, events)
	})
}

// releaseAtStart makes the next start of an agent in Send or Compact run
// Release on the session id, and returns the result of Release. The
// start sleeps for a real time, so Release can take the agent if the
// start lets it. synctest cannot fake this sleep, because Release waits
// on a mutex.
func releaseAtStart(t *testing.T, f fixture, id string) <-chan error {
	t.Helper()
	const raceWindow = 20 * time.Millisecond
	released := make(chan error, 1)
	var once sync.Once
	restore := host.SetAfterStart(func() {
		once.Do(func() {
			go func() { released <- f.h.Release(context.Background(), id) }()
			time.Sleep(raceWindow)
		})
	})
	t.Cleanup(restore)
	return released
}

func TestReleaseDuringTheStartOfASend(t *testing.T) {
	f, gate := gated(t)
	f.driver.counts = true
	f.create(t, host.Config{ID: "s1"})
	_, events := f.watch(t)
	released := releaseAtStart(t, f, "s1")

	f.send(t, "s1", "a")

	require.ErrorIs(t, <-released, host.ErrBusy, "the release waits for the run")
	close(gate)
	sess := runEnd(t, events)
	assert.Equal(t, host.StopEndTurn, sess.StopReason)
	assert.Equal(t, 0, f.driver.closes(), "the run keeps the agent that it started")
	assert.Equal(t, 1, f.driver.starts())
}

// gatedCompactor is a strategy that waits for a value from its channel,
// or for the end of its context, and makes no edits.
type gatedCompactor chan struct{}

func (gatedCompactor) Limit(ai.Model) int { return 0 }

func (g gatedCompactor) Compact(ctx context.Context, _ agent.CompactInput) (agent.CompactResult, error) {
	select {
	case <-g:
		return agent.CompactResult{}, nil
	case <-ctx.Done():
		return agent.CompactResult{}, ctx.Err()
	}
}

func TestReleaseDuringTheStartOfACompact(t *testing.T) {
	gate := make(chan struct{})
	f := newFixture(t, host.Compaction(gatedCompactor(gate)))
	f.driver.counts = true
	f.create(t, host.Config{ID: "s1"})
	_, events := f.watch(t)
	f.send(t, "s1", "a")
	runEnd(t, events)
	require.NoError(t, f.h.Release(context.Background(), "s1"))
	released := releaseAtStart(t, f, "s1")

	require.NoError(t, f.h.Compact(context.Background(), "s1", ""))

	require.ErrorIs(t, <-released, host.ErrBusy, "the release waits for the compaction")
	close(gate)
	runEnd(t, events)
	assert.Equal(t, 1, f.driver.closes(), "the compaction keeps the agent that it started")
	assert.Equal(t, 2, f.driver.starts())
}
