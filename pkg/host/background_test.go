package host_test

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/host"
)

// openTask sends one open command task of the session s1 between runs,
// and waits until the session has it.
func openTask(t *testing.T, f fixture, events <-chan host.Event) {
	t.Helper()
	f.driver.waker(t).idle(agent.EventBackground{
		Tasks: []agent.Task{
			{
				ID:        "b1",
				Kind:      agent.TaskCommand,
				Title:     "make build",
				StartedAt: time.Now(),
			},
		},
	})
	next(t, events, func(e host.Event) bool {
		return e.Session.ID == "s1" && len(e.Session.Background) == 1
	})
}

func TestBackgroundTaskHoldsTheAgent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const idle = time.Minute
		f, events := wakeFixture(t, host.IdleTimeout(idle))
		openTask(t, f, events)

		err := f.h.Release(context.Background(), "s1")
		require.ErrorIs(t, err, host.ErrBusy, "an open task keeps the agent")
		time.Sleep(3 * idle)
		synctest.Wait()
		assert.Equal(t, 0, f.driver.closes(), "the idle time waits for the task")
		got, ok := f.h.Get("s1")
		require.True(t, ok)
		require.Len(t, got.Background, 1)
		assert.Equal(t, "make build", got.Background[0].Title)

		f.driver.waker(t).idle(agent.EventBackground{Tasks: []agent.Task{}})
		time.Sleep(idle)
		synctest.Wait()
		assert.Equal(t, 1, f.driver.closes(), "the agent stops an idle time after the task ends")
	})
}

func TestHoldLimitReleasesTheAgent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const idle = time.Minute
		f, events := wakeFixture(t, host.IdleTimeout(idle), host.HoldLimit(3*idle))
		openTask(t, f, events)

		time.Sleep(4 * idle)
		synctest.Wait()

		assert.Equal(t, 1, f.driver.closes(), "a task older than the hold limit does not keep the agent")
		got, ok := f.h.Get("s1")
		require.True(t, ok)
		assert.Empty(t, got.Background, "the close ends the tasks")
	})
}

func TestEndBackgroundClosesTheAgent(t *testing.T) {
	f, events := wakeFixture(t)
	openTask(t, f, events)

	require.NoError(t, f.h.EndBackground(context.Background(), "s1"))

	assert.Equal(t, 1, f.driver.closes())
	got, ok := f.h.Get("s1")
	require.True(t, ok)
	assert.Empty(t, got.Background)
}

func TestEndBackgroundOfARun(t *testing.T) {
	f, gate := gated(t)
	f.create(t, host.Config{ID: "s1"})
	_, events := f.watch(t)
	f.send(t, "s1", "a")

	err := f.h.EndBackground(context.Background(), "s1")

	require.ErrorIs(t, err, host.ErrBusy, "a run takes an interrupt, not an end of its background")
	close(gate)
	runEnd(t, events)
}
