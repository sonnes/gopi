package host_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/host"
)

// approver is an approve function that sends each call to calls, and
// returns the next decision of decisions.
type approver struct {
	calls     chan ai.ToolCall
	decisions chan agent.Decision
}

func newApprover() approver {
	return approver{
		calls:     make(chan ai.ToolCall, 1),
		decisions: make(chan agent.Decision, 1),
	}
}

func (a approver) approve(ctx context.Context, req agent.ApprovalRequest) (agent.Decision, error) {
	a.calls <- req.Call
	select {
	case d := <-a.decisions:
		return d, nil
	case <-ctx.Done():
		return agent.Decision{}, ctx.Err()
	}
}

// approveLate is an approve function that ignores the end of its
// context.
func (a approver) approveLate(_ context.Context, req agent.ApprovalRequest) (agent.Decision, error) {
	a.calls <- req.Call
	return <-a.decisions, nil
}

func TestApproval(t *testing.T) {
	f := newFixture(t)
	f.model.replies = []ai.Message{
		callTool("call_1", "echo", `{"s":"hi"}`),
		reply("done"),
	}
	a := newApprover()
	s := f.start(t, host.Spec{
		ID:      "s1",
		Approve: a.approve,
	})
	_, events := f.watch(t)

	send(t, s, "a", host.Queue)
	call := <-a.calls
	start := next(t, events, is[host.EventApprovalStart]).(host.EventApprovalStart)
	waiting, _ := f.h.Events(context.Background())
	a.decisions <- agent.Decision{Allow: true}
	end := next(t, events, is[host.EventApprovalEnd]).(host.EventApprovalEnd)

	assert.Equal(t, "call_1", call.ID)
	assert.Equal(t, "call_1", start.Request.Call.ID)
	assert.Equal(t, host.EventApprovalEnd{
		Session:    "s1",
		ToolCallID: "call_1",
		Decision:   agent.Decision{Allow: true},
	}, end)
	assert.Empty(t, runEnd(t, events).Error)
	assert.Equal(t, []string{"hi"}, toolResults(s))
	assert.Equal(t, host.StatusRequiresAction, waiting.Sessions["s1"].Status())
	assert.Empty(t, start.Request.Reason, "an agent that says nothing gives no reason")
	idle, _ := f.h.Events(context.Background())
	assert.Equal(t, host.StatusIdle, idle.Sessions["s1"].Status())
}

func TestApprovalReason(t *testing.T) {
	f := newFixture(t)
	f.driver.reason = "the command writes"
	f.model.replies = []ai.Message{
		callTool("call_1", "echo", `{"s":"hi"}`),
		reply("done"),
	}
	a := newApprover()
	s := f.start(t, host.Spec{
		ID:      "s1",
		Approve: a.approve,
	})
	_, events := f.watch(t)

	send(t, s, "a", host.Queue)
	<-a.calls
	start := next(t, events, is[host.EventApprovalStart]).(host.EventApprovalStart)
	a.decisions <- agent.Decision{Allow: true}

	assert.Equal(t, "the command writes", start.Request.Reason, "the event has the reason of the agent")
	assert.Empty(t, runEnd(t, events).Error)
}

func TestApprovalInterrupt(t *testing.T) {
	f := newFixture(t)
	f.model.replies = []ai.Message{
		callTool("call_1", "echo", `{"s":"hi"}`),
		reply("done"),
	}
	a := newApprover()
	s := f.start(t, host.Spec{
		ID:      "s1",
		Approve: a.approve,
	})
	_, events := f.watch(t)

	send(t, s, "a", host.Queue)
	<-a.calls
	next(t, events, is[host.EventApprovalStart])
	s.Interrupt()

	end := next(t, events, is[host.EventApprovalEnd]).(host.EventApprovalEnd)
	assert.NotEmpty(t, end.Error)
	assert.Equal(t, host.ErrInterrupted.Error(), runEnd(t, events).Error)
	assert.NotContains(t, toolResults(s), "hi")
	state, _ := f.h.Events(context.Background())
	assert.Empty(t, state.Sessions["s1"].Waiting)
}

func TestNoApprove(t *testing.T) {
	f := newFixture(t)
	f.model.replies = []ai.Message{
		callTool("call_1", "echo", `{"s":"hi"}`),
		reply("done"),
	}
	s := f.start(t, host.Spec{ID: "s1"})
	_, events := f.watch(t)

	send(t, s, "a", host.Queue)
	runEnd(t, events)
	require.NoError(t, s.Close())

	assert.Nil(t, f.driver.configs[0].Approve)
	for _, e := range drainUntilEnd(t, events) {
		assert.False(t, is[host.EventApprovalStart](e))
	}
}

func TestApprovalEndsWithItsRun(t *testing.T) {
	f := newFixture(t)
	f.driver.detached = true
	f.driver.abandon = true
	f.model.replies = []ai.Message{
		callTool("call_1", "echo", `{"s":"hi"}`),
		reply("done"),
	}
	a := newApprover()
	s := f.start(t, host.Spec{
		ID:      "s1",
		Approve: a.approveLate,
	})
	_, events := f.watch(t)

	send(t, s, "a", host.Queue)
	<-a.calls
	next(t, events, is[host.EventApprovalStart])
	s.Interrupt()

	end := next(t, events, is[host.EventApprovalEnd]).(host.EventApprovalEnd)
	assert.Equal(t, host.ErrInterrupted.Error(), end.Error)
	assert.Equal(t, host.ErrInterrupted.Error(), runEnd(t, events).Error)
	state, _ := f.h.Events(context.Background())
	assert.Equal(t, host.StatusIdle, state.Sessions["s1"].Status())

	a.decisions <- agent.Decision{Allow: true}
	require.NoError(t, s.Close())
	for _, e := range drainUntilEnd(t, events) {
		assert.False(t, is[host.EventApprovalEnd](e), "a late answer has no event")
	}
}

func TestApprovalWithNoRun(t *testing.T) {
	f := newFixture(t)
	a := newApprover()
	s := f.start(t, host.Spec{
		ID:      "s1",
		Approve: a.approve,
	})
	_, events := f.watch(t)

	_, err := f.driver.configs[0].Approve(context.Background(), agent.ApprovalRequest{Call: testCall})

	assert.ErrorIs(t, err, host.ErrNoRun)
	assert.Empty(t, a.calls, "the approve function of the spec did not run")
	require.NoError(t, s.Close())
	for _, e := range drainUntilEnd(t, events) {
		assert.False(t, is[host.EventApprovalStart](e))
	}
}
