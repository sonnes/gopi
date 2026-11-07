package host_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/harness/permission"
	"github.com/sonnes/gopi/pkg/host"
	"github.com/sonnes/gopi/pkg/session"
)

// grants is the session value with the rules of the Always decisions.
var grants = session.Value[[]string]{Namespace: "gopi.permission", Key: "grants"}

// rulesReader gives the rules of each session, and counts its calls.
type rulesReader struct {
	mu    sync.Mutex
	rules permission.Rules
	err   error
	calls int
}

func (r *rulesReader) Rules(context.Context, string) (permission.Rules, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	return r.rules, r.err
}

func (r *rulesReader) set(rules permission.Rules) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rules = rules
}

func (r *rulesReader) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

// echoTwice is a script that calls echo twice with the same input.
var echoTwice = []ai.Message{
	callTool("call_1", "echo", `{"s":"hi"}`),
	callTool("call_2", "echo", `{"s":"hi"}`),
	reply("done"),
}

func TestAnAllowRuleNeedsNoPerson(t *testing.T) {
	f := asking(t)
	f.create(t, host.Config{ID: "s1", Allow: []string{"echo"}})
	_, events := f.watch(t)

	f.send(t, "s1", "a")

	end := runEnd(t, events)
	assert.Equal(t, host.StopEndTurn, end.StopReason)
	assert.Equal(t, []string{"hi"}, f.toolResults(t, "s1"))
}

func TestAnAskRuleWaitsForAPersonOnce(t *testing.T) {
	tests := []struct {
		name string
		// ask makes the agent ask for each call too, so the rule and the
		// agent ask about one call.
		ask bool
	}{
		{name: "the agent runs the call"},
		{name: "the agent also asks", ask: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := asking(t)
			f.driver.ask = tt.ask
			f.create(t, host.Config{
				ID:    "s1",
				Allow: []string{"echo"},
				Ask:   []string{"echo"},
			})
			_, events := f.watch(t)

			f.send(t, "s1", "a")
			waiting := next(t, events, status(host.StatusRequiresAction)).Session
			require.NoError(t, f.h.Confirm(context.Background(), "s1", "call_1", agent.Decision{Allow: true}))

			require.Len(t, waiting.Waiting, 1)
			assert.Contains(t, waiting.Waiting[0].Reason, "echo")
			end := runEnd(t, events)
			assert.Equal(t, host.StopEndTurn, end.StopReason, "one answer runs the call")
			assert.Equal(t, []string{"hi"}, f.toolResults(t, "s1"))
		})
	}
}

func TestADenyRuleBlocksTheCall(t *testing.T) {
	f := asking(t)
	f.driver.ask = false
	f.create(t, host.Config{
		ID:    "s1",
		Allow: []string{"echo"},
		Deny:  []string{"echo"},
	})
	_, events := f.watch(t)

	f.send(t, "s1", "a")
	runEnd(t, events)

	results := f.toolResults(t, "s1")
	require.Len(t, results, 1)
	assert.Contains(t, results[0], `"echo"`)
	assert.Equal(t, []string{"echo"}, f.lastConfig(t).Deny, "the driver gets the Deny rules")
}

func TestTheHostReadsTheRulesAtEachCall(t *testing.T) {
	reader := &rulesReader{rules: permission.Rules{Allow: []string{"echo"}}}
	f := newFixture(t, host.Rules(reader))
	f.driver.ask = true
	f.model.replies = echoTwice
	f.create(t, host.Config{ID: "s1"})
	_, events := f.watch(t)

	f.send(t, "s1", "a")
	runEnd(t, events)
	assert.Equal(t, []string{"hi", "hi"}, f.toolResults(t, "s1"), "an Allow rule of the reader runs each call")
	before := reader.count()
	assert.GreaterOrEqual(t, before, 2, "the host reads the rules for each call")

	reader.set(permission.Rules{Deny: []string{"echo"}})
	f.model.replies = echoTwice[1:]
	f.send(t, "s1", "b")
	runEnd(t, events)

	results := f.toolResults(t, "s1")
	require.Len(t, results, 3)
	assert.Contains(t, results[2], `"echo"`, "the host reads a new Deny rule at the next call")
	assert.Greater(t, reader.count(), before)
}

func TestAReaderErrorBlocksTheCall(t *testing.T) {
	reader := &rulesReader{err: errors.New("the settings do not load")}
	f := newFixture(t, host.Rules(reader))
	f.model.replies = []ai.Message{
		callTool("call_1", "echo", `{"s":"hi"}`),
		reply("done"),
	}
	f.create(t, host.Config{ID: "s1"})
	_, events := f.watch(t)

	f.send(t, "s1", "a")
	runEnd(t, events)

	results := f.toolResults(t, "s1")
	require.Len(t, results, 1)
	assert.Contains(t, results[0], "the settings do not load")
}

func TestAlwaysKeepsAGrant(t *testing.T) {
	f := asking(t)
	f.model.replies = echoTwice
	f.create(t, host.Config{ID: "s1"})
	_, events := f.watch(t)

	f.send(t, "s1", "a")
	waiting := next(t, events, status(host.StatusRequiresAction)).Session
	require.Len(t, waiting.Waiting, 1)
	assert.Equal(t, "echo", waiting.Waiting[0].Rule, "the request has the exact rule")
	require.NoError(t, f.h.Confirm(context.Background(), "s1", "call_1", agent.Decision{Allow: true, Always: true}))

	end := runEnd(t, events)
	assert.Equal(t, host.StopEndTurn, end.StopReason, "the grant allows the second call with no person")
	assert.Equal(t, []string{"hi", "hi"}, f.toolResults(t, "s1"))
	snap, err := f.store.Load(context.Background(), "s1")
	require.NoError(t, err)
	got, ok, err := grants.Read(snap)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, []string{"echo"}, got)
}

func TestAGrantSurvivesARestart(t *testing.T) {
	f := asking(t)
	f.create(t, host.Config{ID: "s1"})
	_, events := f.watch(t)
	f.send(t, "s1", "a")
	next(t, events, status(host.StatusRequiresAction))
	require.NoError(t, f.h.Confirm(context.Background(), "s1", "call_1", agent.Decision{Allow: true, Always: true}))
	runEnd(t, events)

	f = f.restart(t)
	f.model.replies = []ai.Message{
		callTool("call_2", "echo", `{"s":"hi"}`),
		reply("done"),
	}
	_, events = f.watch(t)
	f.send(t, "s1", "b")

	end := runEnd(t, events)
	assert.Equal(t, host.StopEndTurn, end.StopReason)
	assert.Equal(t, []string{"hi", "hi"}, f.toolResults(t, "s1"))
}

func TestTheAgentGetsNoAlways(t *testing.T) {
	f, gate := gated(t)
	ctx := context.Background()
	f.create(t, host.Config{ID: "s1"})
	_, events := f.watch(t)
	f.send(t, "s1", "a")
	f.model.waitCalls(t, 1)

	approve := f.lastConfig(t).Approve
	decision := make(chan agent.Decision, 1)
	go func() {
		d, err := approve(ctx, agent.ApprovalRequest{Call: ai.ToolCall{ID: "call_1", Name: "echo"}})
		assert.NoError(t, err)
		decision <- d
	}()
	next(t, events, status(host.StatusRequiresAction))
	require.NoError(t, f.h.Confirm(ctx, "s1", "call_1", agent.Decision{Allow: true, Always: true}))

	d := <-decision
	assert.True(t, d.Allow)
	assert.False(t, d.Always, "the host keeps the grant, and the agent keeps none")
	close(gate)
	runEnd(t, events)
}

func TestASafetyCallAlwaysWaits(t *testing.T) {
	f := asking(t)
	f.driver.safety = true
	f.model.replies = echoTwice
	f.create(t, host.Config{ID: "s1", Allow: []string{"echo"}})
	_, events := f.watch(t)

	f.send(t, "s1", "a")
	first := next(t, events, status(host.StatusRequiresAction)).Session
	require.NoError(t, f.h.Confirm(context.Background(), "s1", "call_1", agent.Decision{Allow: true, Always: true}))
	next(t, events, status(host.StatusRequiresAction))
	require.NoError(t, f.h.Confirm(context.Background(), "s1", "call_2", agent.Decision{Allow: true}))
	runEnd(t, events)

	require.Len(t, first.Waiting, 1)
	assert.True(t, first.Waiting[0].Safety)
	assert.Empty(t, first.Waiting[0].Rule, "a safety check has no rule to keep")
	snap, err := f.store.Load(context.Background(), "s1")
	require.NoError(t, err)
	_, ok, err := grants.Read(snap)
	require.NoError(t, err)
	assert.False(t, ok, "Always on a safety check keeps no grant")
}
