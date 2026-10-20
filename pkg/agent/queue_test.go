package agent_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/agent/internal/emittest"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/session"
	"github.com/sonnes/gopi/pkg/tool"
)

func TestSteerDuringToolCall(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	wait := tool.Func("wait", func(context.Context, ai.ToolCall) (ai.ToolResult, error) {
		close(started)
		<-release
		return textResult("waited"), nil
	})
	p := &script{responses: []ai.Message{
		toolCalls("wait", "wait2"),
		text("done"),
	}}
	ran := false
	wait2 := tool.Func("wait2", func(context.Context, ai.ToolCall) (ai.ToolResult, error) {
		ran = true
		return textResult("second"), nil
	}, tool.Sequential())
	a := agent.New(model(p), agent.Toolset(wait, wait2))

	s := a.Run(context.Background(), session.Text("hi"))
	go func() {
		<-started
		a.Steer(session.Text("use the other file"))
		close(release)
	}()
	events, _, err := collect(t, s)
	require.NoError(t, err)
	emittest.CheckEvents(t, events)

	assert.True(t, ran, "every call of the turn runs")
	calls := p.received()
	require.Len(t, calls, 2)
	assert.Equal(t, []string{"hi", "", "waited", "second", "use the other file"}, msgTexts(calls[1]))
	assert.Equal(t,
		[]string{"hi", "", "waited", "second", "use the other file", "done"},
		historyTexts(a),
	)
}

func TestSteerBeforeRun(t *testing.T) {
	p := &script{responses: []ai.Message{text("ok")}}
	a := agent.New(model(p))
	a.Steer(session.Text("first"))

	events, msgs, err := collect(t, a.Run(context.Background(), session.Text("hi")))
	require.NoError(t, err)
	emittest.CheckEvents(t, events)

	assert.Equal(t, types([]agent.Event{
		agent.EventAgentStart{},
		agent.EventTurnStart{},
		agent.EventMessageStart{},
		agent.EventMessageEnd{},
		agent.EventMessageStart{},
		agent.EventMessageEnd{},
		agent.EventMessageStart{},
	}), types(events[:7]), "the steer is a message event after the input")
	assert.Equal(t, "hi", as[agent.EventMessageEnd](t, events[3]).Message.Text())
	assert.Equal(t, "first", as[agent.EventMessageEnd](t, events[5]).Message.Text())
	assert.Equal(t, []string{"hi", "first"}, msgTexts(p.received()[0]))
	assert.Equal(t, []string{"first", "ok"}, msgTexts(msgs))
}

func TestQueueOrder(t *testing.T) {
	tests := []struct {
		name      string
		responses []ai.Message
		// setup returns the options of the test. a is set before the
		// run starts.
		setup func(a **agent.Agent) []agent.Option
		// before runs after New and before Run.
		before  func(a *agent.Agent)
		history []string
	}{
		{
			name:      "a steer continues a turn with no tool calls",
			responses: []ai.Message{text("one"), text("two")},
			setup: func(a **agent.Agent) []agent.Option {
				return []agent.Option{steerOnTurn(a, 1, "more")}
			},
			history: []string{"hi", "one", "more", "two"},
		},
		{
			name: "a follow-up waits until the run would stop",
			responses: []ai.Message{
				toolCall("c1", "echo", `{"s":"x"}`),
				text("one"),
				text("two"),
			},
			before:  func(a *agent.Agent) { a.FollowUp(session.Text("next")) },
			history: []string{"hi", "", "echo:x", "one", "next", "two"},
		},
		{
			name:      "the steer queue comes before the follow-up queue",
			responses: []ai.Message{text("one"), text("two"), text("three")},
			setup: func(a **agent.Agent) []agent.Option {
				return []agent.Option{steerOnTurn(a, 1, "steer")}
			},
			before:  func(a *agent.Agent) { a.FollowUp(session.Text("follow")) },
			history: []string{"hi", "one", "steer", "two", "follow", "three"},
		},
		{
			name:      "the follow-up queue comes before BeforeStop",
			responses: []ai.Message{text("one"), text("two"), text("three")},
			setup: func(**agent.Agent) []agent.Option {
				return []agent.Option{agent.BeforeStop(once("hook"))}
			},
			before: func(a *agent.Agent) {
				a.FollowUp(session.Text("follow"), session.Text("up"))
			},
			history: []string{"hi", "one", "follow", "up", "two", "hook", "three"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &script{responses: tt.responses}
			var a *agent.Agent
			opts := []agent.Option{agent.Toolset(echo)}
			if tt.setup != nil {
				opts = append(opts, tt.setup(&a)...)
			}
			a = agent.New(model(p), opts...)
			if tt.before != nil {
				tt.before(a)
			}

			events, _, err := collect(t, a.Run(context.Background(), session.Text("hi")))
			require.NoError(t, err)
			emittest.CheckEvents(t, events)

			assert.Equal(t, tt.history, historyTexts(a))
		})
	}
}

// steerOnTurn returns a BeforeCall hook that steers the agent with s on
// the given turn. The first turn is 1.
func steerOnTurn(a **agent.Agent, turn int, s string) agent.Option {
	return agent.BeforeCall(func(_ context.Context, st agent.State, r agent.Request) (agent.Request, error) {
		if st.Turn() == turn {
			(*a).Steer(session.Text(s))
		}
		return r, nil
	})
}

func TestQueueKeptAfterFailure(t *testing.T) {
	p := &script{responses: []ai.Message{
		text("one"),
		text("two"),
		text("three"),
	}}
	var a *agent.Agent
	failed := false
	a = agent.New(model(p),
		agent.AfterTurn(func(context.Context, agent.State) ([]session.Entry, error) {
			if failed {
				return nil, nil
			}
			failed = true
			a.Steer(session.Text("steer"))
			return nil, errors.New("stop")
		}),
	)
	a.FollowUp(session.Text("follow"))

	_, _, err := collect(t, a.Run(context.Background(), session.Text("hi")))
	require.Error(t, err)
	assert.Equal(t, []string{"hi", "one"}, historyTexts(a), "the failure stops the run before a drain")

	_, _, err = collect(t, a.Run(context.Background()))
	require.NoError(t, err)
	assert.Equal(t,
		[]string{"hi", "one", "steer", "two", "follow", "three"},
		historyTexts(a),
		"the next run reads the queues",
	)
}

func TestQueueConcurrent(t *testing.T) {
	const (
		writers = 8
		each    = 10
	)
	responses := make([]ai.Message, 0, 4*writers*each)
	for range cap(responses) {
		responses = append(responses, text("ok"))
	}
	p := &script{responses: responses}
	a := agent.New(model(p))

	s := a.Run(context.Background(), session.Text("hi"))
	var wg sync.WaitGroup
	for range writers {
		wg.Go(func() {
			for range each {
				a.Steer(session.Text("steer"))
				a.FollowUp(session.Text("follow"))
			}
		})
	}
	wg.Wait()
	_, err := s.Wait()
	require.NoError(t, err)
	_, err = a.Run(context.Background()).Wait()
	require.NoError(t, err)

	got := strings.Join(historyTexts(a), " ")
	assert.Equal(t, writers*each, strings.Count(got, "steer"), "every steer reaches the history")
	assert.Equal(t, writers*each, strings.Count(got, "follow"), "every follow-up reaches the history")
}

func TestHookSteersThroughTheState(t *testing.T) {
	p := &script{responses: []ai.Message{
		toolCall("c1", "echo", `{"s":"x"}`),
		text("done"),
	}}
	a := agent.New(model(p),
		agent.Toolset(echo),
		agent.AfterTool(func(_ context.Context, s agent.State, _ ai.ToolCall, r ai.ToolResult) (ai.ToolResult, error) {
			s.Steer(session.Text("check the output"))
			return r, nil
		}),
	)

	events, _, err := collect(t, a.Run(context.Background(), session.Text("hi")))
	require.NoError(t, err)
	emittest.CheckEvents(t, events)

	calls := p.received()
	require.Len(t, calls, 2)
	assert.Equal(t, []string{"hi", "", "echo:x", "check the output"}, msgTexts(calls[1]),
		"the next model call reads the steer")
}

func TestHookFollowsUpThroughTheState(t *testing.T) {
	p := &script{responses: []ai.Message{text("one"), text("two")}}
	done := false
	a := agent.New(model(p),
		agent.AfterTurn(func(_ context.Context, s agent.State) ([]session.Entry, error) {
			if !done {
				done = true
				s.FollowUp(session.Text("go on"))
			}
			return nil, nil
		}),
	)

	_, _, err := collect(t, a.Run(context.Background(), session.Text("hi")))
	require.NoError(t, err)
	assert.Len(t, p.received(), 2, "the follow-up continues the run")
	assert.Equal(t, []string{"hi", "one", "go on", "two"}, historyTexts(a))
}

func TestDecodedStateCannotQueue(t *testing.T) {
	var s agent.State
	require.NoError(t, s.UnmarshalJSON([]byte(`{}`)))

	assert.NotPanics(t, func() {
		s.Steer(session.Text("x"))
		s.FollowUp(session.Text("y"))
	}, "a viewer has no queues, so the calls do nothing")
}

func TestTakeSteer(t *testing.T) {
	a := agent.New(model(&script{}))
	a.Steer(session.Text("one"), session.Text("two"))

	taken := a.TakeSteer()
	require.Len(t, taken, 2)
	assert.Equal(t, "one", taken[0].Message.Text())
	assert.Empty(t, a.TakeSteer(), "TakeSteer empties the queue")
}
