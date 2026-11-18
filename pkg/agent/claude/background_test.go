package claude_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/agent/claude"
	"github.com/sonnes/gopi/pkg/agent/internal/emittest"
)

// wakeupDelay is the delaySeconds of the ScheduleWakeup call of
// background_command.trace.
const wakeupDelay = 1800 * time.Second

// lastBackground returns the tasks of the last [agent.EventBackground]
// of events, and false if it has none.
func lastBackground(events []agent.Event) ([]agent.Task, bool) {
	for i := len(events) - 1; i >= 0; i-- {
		if e, ok := events[i].(agent.EventBackground); ok {
			return e.Tasks, true
		}
	}
	return nil, false
}

// startBackground runs background_command.trace to the wake of the CLI,
// and returns the agent, the idle events, the events of the run, and
// the time of the start of the run.
func startBackground(t *testing.T) (*claude.Agent, *idleSink, []agent.Event, time.Time) {
	t.Helper()
	claude.Replay(t, nil, "testdata/background_command.trace")
	idle := &idleSink{}
	woke := newWakeSink()
	a := claude.New(agent.Config{
		Idle: idle.add,
		Wake: woke.signal,
	})
	start := time.Now()
	events, _, err := run(context.Background(), a, "Build in the background.")
	require.NoError(t, err)
	emittest.CheckEvents(t, events)
	<-woke
	return a, idle, events, start
}

func TestBackgroundTasksOfARun(t *testing.T) {
	a, idle, events, start := startBackground(t)
	defer closeAgent(t, a)

	tasks, ok := lastBackground(events)
	require.True(t, ok, "the run sends its background tasks")
	require.Len(t, tasks, 3)
	assert.Equal(t, "b1", tasks[0].ID)
	assert.Equal(t, agent.TaskCommand, tasks[0].Kind)
	assert.Equal(t, "Build the app", tasks[0].Title)
	assert.Equal(t, "m1", tasks[1].ID)
	assert.Equal(t, agent.TaskMonitor, tasks[1].Kind, "a local_bash task of a Monitor call is a monitor")
	assert.Equal(t, "toolu_w", tasks[2].ID)
	assert.Equal(t, agent.TaskWakeup, tasks[2].Kind)
	assert.Equal(t, "Check the build", tasks[2].Title)
	assert.WithinDuration(t, start.Add(wakeupDelay), tasks[2].Due, time.Minute)

	left, ok := lastBackground(idle.all())
	require.True(t, ok, "the end of the command reaches the idle events")
	var ids []string
	for _, task := range left {
		ids = append(ids, task.ID)
	}
	assert.Equal(t, []string{"m1", "toolu_w"}, ids, "the wake of a notice keeps a wakeup that is not due")

	_, msgs, err := collectRun(a.Continue(context.Background()))
	require.NoError(t, err)
	require.NotEmpty(t, msgs)
	assert.Equal(t, "The build passed.", msgs[len(msgs)-1].Text())
}

func TestCloseNotesTheOpenTasks(t *testing.T) {
	a, idle, _, _ := startBackground(t)
	_, _, err := collectRun(a.Continue(context.Background()))
	require.NoError(t, err)

	require.NoError(t, a.Close())

	left, ok := lastBackground(idle.all())
	require.True(t, ok)
	assert.Empty(t, left, "a close ends each task")

	var stopped *agent.Stopped
	for _, e := range a.Log().Entries() {
		if e.Kind != agent.KindStopped {
			continue
		}
		stopped = &agent.Stopped{}
		require.NoError(t, json.Unmarshal(e.Data, stopped))
	}
	require.NotNil(t, stopped, "the log names the tasks that the close stopped")
	require.Len(t, stopped.Tasks, 2)
	assert.Equal(t, "Watch the log", stopped.Tasks[0].Title)
	assert.Equal(t, "Check the build", stopped.Tasks[1].Title)
}
