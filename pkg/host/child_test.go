package host_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/host"
	"github.com/sonnes/gopi/pkg/session"
	"github.com/sonnes/gopi/pkg/tool"
)

// childID is the session of the subagent of the delegate tool.
const childID = "s1.helper-01"

// delegate returns a tool that runs one subagent in the child session
// childID of store, as a driver does. If gate is set, the child waits on
// it after its start. If end is false, the tool returns with the child
// open.
func delegate(store session.Storer, gate chan struct{}, end bool) tool.Tool {
	return tool.Func("delegate", func(ctx context.Context, call ai.ToolCall) (ai.ToolResult, error) {
		log, err := session.Open(ctx, store, childID)
		if err != nil {
			return ai.ToolResult{}, err
		}
		ref := agent.ParentRef{
			SessionID:  "s1",
			ToolCallID: call.ID,
		}
		if err := agent.Parent.Set(ctx, log.Writer(), ref); err != nil {
			return ai.ToolResult{}, err
		}
		agent.EmitSubagent(ctx, agent.EventSubagentStart{
			SessionID: childID,
			Name:      "helper",
			Model:     "fake/m",
		})
		if gate != nil {
			<-gate
		}
		agent.EmitSubagent(ctx, agent.EventSubagent{
			SessionID: childID,
			Event:     agent.EventAgentStart{},
		})
		agent.EmitSubagent(ctx, agent.EventSubagent{
			SessionID: childID,
			Event:     agent.EventAgentEnd{},
		})
		if end {
			agent.EmitSubagent(ctx, agent.EventSubagentEnd{
				SessionID: childID,
				Status:    agent.SubagentCompleted,
				Text:      "done",
			})
		}
		return ai.ToolResult{Content: []ai.Content{ai.Text{Text: "done"}}}, nil
	})
}

// childFixture returns a fixture whose sessions have the delegate tool,
// and the session s1 with a model that calls it once.
func childFixture(t *testing.T, gate chan struct{}, end bool) fixture {
	t.Helper()
	f := newFixture(t)
	f.h = f.open(t, host.Tools(delegate(f.store, gate, end)))
	f.model.replies = []ai.Message{
		callTool("c1", "delegate", `{}`),
		reply("ok"),
	}
	f.create(t, host.Config{ID: "s1"})
	return f
}

func TestChildIsASessionWithItsParent(t *testing.T) {
	f := childFixture(t, nil, true)
	_, events := f.watch(t)

	f.send(t, "s1", "go")
	next(t, events, func(e host.Event) bool {
		return e.Session.ID == childID && e.Session.Status == host.StatusRunning
	})
	ended := next(t, events, func(e host.Event) bool {
		return e.Session.ID == childID && e.Session.Status == host.StatusIdle
	})

	assert.Equal(t, &agent.ParentRef{SessionID: "s1", ToolCallID: "c1"}, ended.Session.Parent)
	assert.Equal(t, host.StopEndTurn, ended.Session.StopReason)
	assert.Equal(t, "fake/m", ended.Session.Model)
	got, ok := f.h.Get(childID)
	require.True(t, ok)
	assert.Equal(t, ended.Session.Parent, got.Parent)
}

func TestStreamOfAChildHasItsOwnEvents(t *testing.T) {
	gate := make(chan struct{})
	f := childFixture(t, gate, true)

	f.send(t, "s1", "go")
	require.Eventually(t, func() bool {
		_, ok := f.h.Get(childID)
		return ok
	}, waitTimeout, time.Millisecond)
	_, events := f.follow(t, childID)
	close(gate)

	first := next(t, events, func(agent.Event) bool { return true })
	assert.IsType(t, agent.EventAgentStart{}, first, "the stream of a child has the events of the child, unwrapped")
	next(t, events, func(e agent.Event) bool {
		_, ok := e.(agent.EventAgentEnd)
		return ok
	})
}

func TestChildIsReadOnly(t *testing.T) {
	f := childFixture(t, nil, true)
	_, events := f.watch(t)
	f.send(t, "s1", "go")
	next(t, events, func(e host.Event) bool {
		return e.Session.ID == childID && e.Session.Status == host.StatusIdle
	})
	ctx := context.Background()

	err := f.h.Send(ctx, childID, host.Text("hi"))
	assert.ErrorIs(t, err, host.ErrChild)
	err = f.h.Interrupt(ctx, childID)
	assert.ErrorIs(t, err, host.ErrChild)
	_, err = f.h.Update(ctx, childID, func(c *host.Config) { c.Model = "fake/m" })
	assert.ErrorIs(t, err, host.ErrChild)

	log, err := f.h.Log(ctx, childID)
	require.NoError(t, err, "a child has a log to read")
	parent, ok, err := agent.Parent.Get(log)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "s1", parent.SessionID)
}

func TestChildOfAnEndedRunEndsInterrupted(t *testing.T) {
	f := childFixture(t, nil, false)
	_, events := f.watch(t)

	f.send(t, "s1", "go")
	ended := next(t, events, func(e host.Event) bool {
		return e.Session.ID == childID && e.Session.Status == host.StatusIdle
	})
	assert.Equal(t, host.StopInterrupted, ended.Session.StopReason, "a run that ends ends each child that it left open")
}

func TestChildSurvivesARestart(t *testing.T) {
	f := childFixture(t, nil, true)
	_, events := f.watch(t)
	f.send(t, "s1", "go")
	next(t, events, func(e host.Event) bool {
		return e.Session.ID == childID && e.Session.Status == host.StatusIdle
	})

	f = f.restart(t)

	got, ok := f.h.Get(childID)
	require.True(t, ok)
	assert.Equal(t, &agent.ParentRef{SessionID: "s1", ToolCallID: "c1"}, got.Parent)
	assert.Equal(t, "fake/m", got.Model, "a child keeps its model, though it has no config")
	assert.ErrorIs(t, f.h.Send(context.Background(), childID, host.Text("hi")), host.ErrChild)
}

func TestChildExistsWhenItsStartIsOnTheParentStream(t *testing.T) {
	f := childFixture(t, nil, true)
	_, events := f.follow(t, "s1")

	f.send(t, "s1", "go")
	next(t, events, func(e agent.Event) bool {
		_, ok := e.(agent.EventSubagentStart)
		return ok
	})
	_, ok := f.h.Get(childID)
	assert.True(t, ok, "a reader can open the child as soon as the parent shows its start")
}

func TestChildOfAClosedHostIsNotKept(t *testing.T) {
	f := newFixture(t)
	started := make(chan struct{})
	late := tool.Func("late", func(ctx context.Context, call ai.ToolCall) (ai.ToolResult, error) {
		close(started)
		<-ctx.Done()
		agent.EmitSubagent(ctx, agent.EventSubagentStart{SessionID: childID})
		return ai.ToolResult{}, nil
	})
	f.h = f.open(t, host.Tools(late))
	f.model.replies = []ai.Message{callTool("c1", "late", `{}`)}
	f.create(t, host.Config{ID: "s1"})

	f.send(t, "s1", "go")
	<-started
	require.NoError(t, f.h.Close())

	_, ok := f.h.Get(childID)
	assert.False(t, ok, "a host that closed takes no new child")
}
