package host_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/host"
	"github.com/sonnes/gopi/pkg/tool"
)

// wakeFixture returns a fixture whose runners wake, and the session s1
// after one run.
func wakeFixture(t *testing.T, opts ...host.Option) (fixture, <-chan host.Event) {
	t.Helper()
	f := newFixture(t, opts...)
	f.driver.wakes = true
	f.create(t, host.Config{ID: "s1"})
	_, events := f.watch(t)
	f.send(t, "s1", "go")
	runEnd(t, events)
	return f, events
}

func TestWakeRunsTheTurnOfTheAgent(t *testing.T) {
	f, events := wakeFixture(t)

	f.driver.waker(t).wakes <- struct{}{}

	next(t, events, func(e host.Event) bool {
		return e.Session.ID == "s1" && e.Session.Status == host.StatusRunning
	})
	end := runEnd(t, events)
	assert.Equal(t, host.StopEndTurn, end.StopReason)
	assert.Equal(t, []string{"go", "continue"}, f.model.texts())
}

func TestWakeDuringARunRunsAfterIt(t *testing.T) {
	f, events := wakeFixture(t)
	gate := make(chan struct{})
	f.model.mu.Lock()
	f.model.gate = gate
	f.model.mu.Unlock()

	f.send(t, "s1", "again")
	f.model.waitCalls(t, 2)
	f.driver.waker(t).wakes <- struct{}{}
	close(gate)

	runEnd(t, events)
	runEnd(t, events)
	assert.Equal(t, []string{"go", "again", "continue"}, f.model.texts(), "a wake during a run runs after it")
}

func TestIdleEventsReachTheChild(t *testing.T) {
	f, events := wakeFixture(t)
	idle := f.driver.waker(t).idle
	require.NotNil(t, idle, "the host gives the agent an Idle function")
	_, parent := f.follow(t, "s1")

	idle(agent.EventSubagentStart{
		ToolCallID: "c1",
		SessionID:  childID,
		Name:       "helper",
	})
	next(t, events, func(e host.Event) bool {
		return e.Session.ID == childID && e.Session.Status == host.StatusRunning
	})
	_, child := f.follow(t, childID)
	idle(agent.EventSubagent{
		ToolCallID: "c1",
		SessionID:  childID,
		Event:      agent.EventAgentStart{},
	})
	idle(agent.EventSubagentEnd{
		ToolCallID: "c1",
		SessionID:  childID,
		Status:     agent.SubagentCompleted,
	})

	first := next(t, child, func(agent.Event) bool { return true })
	assert.IsType(t, agent.EventAgentStart{}, first, "the child stream gets the events between runs")
	ended := next(t, events, func(e host.Event) bool {
		return e.Session.ID == childID && e.Session.Status == host.StatusIdle
	})
	assert.Equal(t, host.StopEndTurn, ended.Session.StopReason)
	assert.Equal(t, &agent.ParentRef{SessionID: "s1", ToolCallID: "c1"}, ended.Session.Parent)
	next(t, parent, is[agent.EventSubagentEnd, agent.Event])
}

// background returns a tool that starts the child childID, and ends it
// in the run with the status background, as a driver does for a child
// that goes on after the run.
func background() tool.Tool {
	return tool.Func("background", func(ctx context.Context, call ai.ToolCall) (ai.ToolResult, error) {
		agent.EmitSubagent(ctx, agent.EventSubagentStart{
			SessionID: childID,
			Name:      "helper",
		})
		agent.EmitSubagent(ctx, agent.EventSubagentEnd{
			SessionID: childID,
			Status:    agent.SubagentBackground,
		})
		return ai.ToolResult{Content: []ai.Content{ai.Text{Text: "started"}}}, nil
	})
}

func TestBackgroundChildRunsAfterTheRun(t *testing.T) {
	f := newFixture(t)
	f.driver.wakes = true
	f.h = f.open(t, host.Tools(background()))
	f.model.replies = []ai.Message{
		callTool("c1", "background", `{}`),
		reply("ok"),
	}
	f.create(t, host.Config{ID: "s1"})
	_, events := f.watch(t)

	f.send(t, "s1", "go")
	runEnd(t, events)

	got, ok := f.h.Get(childID)
	require.True(t, ok)
	assert.Equal(t, host.StatusRunning, got.Status, "a child in the background runs after the run")
	assert.True(t, f.h.RunsChildren("s1"))

	f.driver.waker(t).idle(agent.EventSubagentEnd{
		ToolCallID: "c1",
		SessionID:  childID,
		Status:     agent.SubagentCompleted,
	})
	next(t, events, func(e host.Event) bool {
		return e.Session.ID == childID && e.Session.Status == host.StatusIdle
	})
	assert.False(t, f.h.RunsChildren("s1"))
}

func TestCloseEndsABackgroundChild(t *testing.T) {
	f := newFixture(t)
	f.driver.wakes = true
	f.h = f.open(t, host.Tools(background()))
	f.model.replies = []ai.Message{
		callTool("c1", "background", `{}`),
		reply("ok"),
	}
	f.create(t, host.Config{ID: "s1"})
	_, events := f.watch(t)
	f.send(t, "s1", "go")
	runEnd(t, events)

	f = f.restart(t)

	got, ok := f.h.Get(childID)
	require.True(t, ok)
	assert.Equal(t, host.StatusIdle, got.Status)
	assert.Equal(t, host.StopRestarted, got.StopReason, "no child outlives the agent that runs it")
}
