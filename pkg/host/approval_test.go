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

// asking returns a fixture whose agent asks before each tool call, and
// whose model calls echo with "hi" and then says "done".
func asking(t *testing.T) fixture {
	t.Helper()
	f := newFixture(t)
	f.driver.ask = true
	f.model.replies = []ai.Message{
		callTool("call_1", "echo", `{"s":"hi"}`),
		reply("done"),
	}
	return f
}

func TestConfirmAllowsACall(t *testing.T) {
	f := asking(t)
	f.create(t, host.Config{ID: "s1"})
	_, events := f.watch(t)

	f.send(t, "s1", "a")
	waiting := next(t, events, status(host.StatusRequiresAction)).Session
	require.NoError(t, f.h.Confirm(context.Background(), "s1", "call_1", agent.Decision{Allow: true}))

	require.Len(t, waiting.Waiting, 1)
	assert.Equal(t, "call_1", waiting.Waiting[0].Call.ID)
	assert.Empty(t, waiting.Waiting[0].Reason, "an agent that says nothing gives no reason")
	running := next(t, events, func(host.Event) bool { return true }).Session
	assert.Equal(t, host.StatusRunning, running.Status)
	assert.Empty(t, running.Waiting)
	assert.Equal(t, host.StopEndTurn, runEnd(t, events).StopReason)
	assert.Equal(t, []string{"hi"}, f.toolResults(t, "s1"))
}

func TestConfirmDeniesACall(t *testing.T) {
	f := asking(t)
	f.create(t, host.Config{ID: "s1"})
	_, events := f.watch(t)

	f.send(t, "s1", "a")
	next(t, events, status(host.StatusRequiresAction))
	require.NoError(t, f.h.Confirm(context.Background(), "s1", "call_1", agent.Decision{Message: "not now"}))

	runEnd(t, events)
	results := f.toolResults(t, "s1")
	require.Len(t, results, 1)
	assert.Contains(t, results[0], "not now")
}

func TestTheReasonOfACall(t *testing.T) {
	f := asking(t)
	f.driver.reason = "the command writes"
	f.create(t, host.Config{ID: "s1"})
	_, events := f.watch(t)

	f.send(t, "s1", "a")
	waiting := next(t, events, status(host.StatusRequiresAction)).Session
	require.NoError(t, f.h.Confirm(context.Background(), "s1", "call_1", agent.Decision{Allow: true}))

	assert.Equal(t, "the command writes", waiting.Waiting[0].Reason)
	runEnd(t, events)
}

func TestTwoCallsWaitTogether(t *testing.T) {
	f, gate := gated(t)
	ctx := context.Background()
	f.create(t, host.Config{ID: "s1"})
	_, events := f.watch(t)
	f.send(t, "s1", "a")
	f.model.waitCalls(t, 1)

	// The loop of the fake agent asks one call at a time, so the test
	// calls the approve function twice, as a CLI agent does for two
	// subagents.
	approve := f.lastConfig(t).Approve
	decisions := make(map[string]chan agent.Decision)
	for _, id := range []string{"call_1", "call_2"} {
		ch := make(chan agent.Decision, 1)
		decisions[id] = ch
		go func() {
			d, err := approve(ctx, agent.ApprovalRequest{Call: ai.ToolCall{ID: id, Name: "echo"}})
			assert.NoError(t, err)
			ch <- d
		}()
	}
	next(t, events, func(e host.Event) bool { return len(e.Session.Waiting) == 2 })

	require.NoError(t, f.h.Confirm(ctx, "s1", "call_2", agent.Decision{Allow: true}))
	one := next(t, events, func(host.Event) bool { return true }).Session
	assert.Equal(t, host.StatusRequiresAction, one.Status, "the first call still waits")
	require.Len(t, one.Waiting, 1)
	assert.Equal(t, "call_1", one.Waiting[0].Call.ID)
	assert.True(t, (<-decisions["call_2"]).Allow)

	require.NoError(t, f.h.Confirm(ctx, "s1", "call_1", agent.Decision{Message: "no"}))
	none := next(t, events, func(host.Event) bool { return true }).Session
	assert.Equal(t, host.StatusRunning, none.Status)
	assert.Equal(t, "no", (<-decisions["call_1"]).Message)
	close(gate)
	runEnd(t, events)
}

func TestConfirmACallThatDoesNotWait(t *testing.T) {
	f := newFixture(t)
	f.create(t, host.Config{ID: "s1"})

	err := f.h.Confirm(context.Background(), "s1", "call_1", agent.Decision{Allow: true})

	assert.ErrorIs(t, err, host.ErrNotFound)
}

func TestInterruptEndsEachCallThatWaits(t *testing.T) {
	tests := []struct {
		name string
		// detached calls the approve function with a context that the
		// run does not end, and abandons the wait, as the Codex agent
		// does.
		detached bool
	}{
		{name: "harness"},
		{name: "detached", detached: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := asking(t)
			f.driver.detached = tt.detached
			f.driver.abandon = tt.detached
			ctx := context.Background()
			f.create(t, host.Config{ID: "s1"})
			_, events := f.watch(t)
			f.send(t, "s1", "a")
			next(t, events, status(host.StatusRequiresAction))

			require.NoError(t, f.h.Interrupt(ctx, "s1"))

			end := runEnd(t, events)
			assert.Equal(t, host.StopInterrupted, end.StopReason)
			assert.Empty(t, end.Waiting)
			assert.ErrorIs(t, f.h.Confirm(ctx, "s1", "call_1", agent.Decision{Allow: true}), host.ErrNotFound)
			assert.NotContains(t, f.toolResults(t, "s1"), "hi")
		})
	}
}

func TestAnApprovalWithNoRun(t *testing.T) {
	f := newFixture(t)
	f.create(t, host.Config{ID: "s1"})

	_, err := f.lastConfig(t).Approve(context.Background(), agent.ApprovalRequest{Call: ai.ToolCall{ID: "call_1"}})

	assert.ErrorIs(t, err, host.ErrNoRun)
	got, _ := f.h.Get("s1")
	assert.Equal(t, host.StatusIdle, got.Status)
	assert.Empty(t, got.Waiting)
}
