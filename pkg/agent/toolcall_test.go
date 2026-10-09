package agent_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/agent/internal/emittest"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/tool"
)

func TestRunUnknownTool(t *testing.T) {
	p := &script{responses: []ai.Message{
		toolCall("c1", "nope", `{}`),
		text("ok"),
	}}
	a := agent.New(model(p), agent.Toolset(echo))

	_, msgs, err := collect(t, a.Run(context.Background(), ai.UserMessage("hi")))
	require.NoError(t, err)
	msgs = afterBase(t, msgs)

	require.Len(t, msgs, 3)
	assert.True(t, msgs[1].IsError, "an unknown tool is an error the model can correct")
	assert.Contains(t, msgs[1].Text(), "nope")
}

func TestRunToolFailure(t *testing.T) {
	broken := tool.Func("broken", func(context.Context, ai.ToolCall) (ai.ToolResult, error) {
		return ai.ToolResult{}, errors.New("boom")
	})
	p := &script{responses: []ai.Message{
		toolCall("c1", "broken", `{}`),
		text("ok"),
	}}
	a := agent.New(model(p), agent.Toolset(broken))

	events, msgs, err := collect(t, a.Run(context.Background(), ai.UserMessage("hi")))
	require.NoError(t, err, "a Go error from a tool does not fail the run")
	emittest.CheckEvents(t, events)
	msgs = afterBase(t, msgs)

	require.Len(t, msgs, 3)
	assert.True(t, msgs[1].IsError, "the error becomes an error result")
	assert.Equal(t, "boom", msgs[1].Text())
	assert.Equal(t, "ok", msgs[2].Text(), "the model reads the error and goes on")
}

func TestRunToolCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop := tool.Func("stop", func(ctx context.Context, _ ai.ToolCall) (ai.ToolResult, error) {
		cancel()
		return ai.ToolResult{}, ctx.Err()
	})
	p := &script{responses: []ai.Message{toolCall("c1", "stop", `{}`)}}
	a := agent.New(model(p), agent.Toolset(stop))

	events, msgs, err := collect(t, a.Run(ctx, ai.UserMessage("hi")))
	assert.ErrorIs(t, err, context.Canceled, "a canceled run fails")
	assert.Len(t, afterBase(t, msgs), 1, "the assistant message is kept")
	emittest.CheckEvents(t, events)
	agentEnd := as[agent.EventAgentEnd](t, events[len(events)-1])
	assert.Equal(t, context.Canceled.Error(), agentEnd.Error, "a failed run ends with agent_end")
	assert.IsType(t, agent.EventTurnEnd{}, events[len(events)-2], "turn_start and turn_end stay paired")
	assert.IsType(t, agent.EventToolExecutionEnd{}, events[len(events)-3], "the tool call stays paired")
	assert.Equal(t, []string{"hi", ""}, msgTexts(afterBase(t, history(a))), "the result of a canceled call is not added")
}

func TestRunToolCallsWithAnotherStopReason(t *testing.T) {
	call := toolCall("c1", "echo", `{"s":"x"}`)
	call.StopReason = ai.StopReasonLength
	p := &script{responses: []ai.Message{call, text("done")}}
	a := agent.New(model(p), agent.Toolset(echo))

	events, _, err := collect(t, a.Run(context.Background(), ai.UserMessage("hi")))
	require.NoError(t, err)
	emittest.CheckEvents(t, events)
	assert.Equal(t,
		[]string{"hi", "", "echo:x", "done"},
		historyTexts(a),
		"a finished call runs, whatever the stop reason",
	)
}

// counter is a tool that records the most calls that ran at one time.
type counter struct {
	running atomic.Int32
	most    atomic.Int32
}

func (c *counter) tool(name string, opts ...tool.Option) tool.Tool {
	return tool.Func(name, func(context.Context, ai.ToolCall) (ai.ToolResult, error) {
		n := c.running.Add(1)
		defer c.running.Add(-1)
		for {
			most := c.most.Load()
			if n <= most || c.most.CompareAndSwap(most, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		return textResult(name), nil
	}, opts...)
}

func TestRunToolsInParallel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	first, second := meeting()
	p := &script{responses: []ai.Message{
		toolCalls("first", "second"),
		text("done"),
	}}
	a := agent.New(model(p), agent.Toolset(first, second))

	events, msgs, err := collect(t, a.Run(ctx, ai.UserMessage("hi")))
	require.NoError(t, err, "the calls of one message run at the same time")
	emittest.CheckEvents(t, events)
	msgs = afterBase(t, msgs)

	var order []string
	for _, ev := range events {
		switch ev := ev.(type) {
		case agent.EventToolExecutionStart:
			order = append(order, "start "+ev.ToolCallID)
		case agent.EventToolExecutionEnd:
			order = append(order, "end "+ev.ToolCallID)
		case agent.EventMessageEnd:
			if ev.Message.Role == ai.RoleToolResult {
				order = append(order, "result "+ev.Message.ToolCallID)
			}
		}
	}
	assert.Equal(t, []string{
		"start c1", "start c2",
		"end c2", "end c1",
		"result c1", "result c2",
	}, order, "starts and results in call order, ends as each call finishes")

	require.Len(t, msgs, 4)
	assert.Equal(t, "first", msgs[1].Text())
	assert.Equal(t, "second", msgs[2].Text())
	assert.Equal(t, []string{"hi", "", "first", "second", "done"}, historyTexts(a),
		"the history keeps the results in call order")
}

func TestRunToolsSequential(t *testing.T) {
	var c counter
	p := &script{responses: []ai.Message{
		toolCalls("one", "two", "three"),
		text("done"),
	}}
	a := agent.New(model(p), agent.Toolset(
		c.tool("one"),
		c.tool("two", tool.Sequential()),
		c.tool("three"),
	))

	events, msgs, err := collect(t, a.Run(context.Background(), ai.UserMessage("hi")))
	require.NoError(t, err)
	emittest.CheckEvents(t, events)
	msgs = afterBase(t, msgs)

	assert.Equal(t, int32(1), c.most.Load(), "a sequential tool runs every call of its message in order")
	require.Len(t, msgs, 5)
	assert.Equal(t, "one", msgs[1].Text())
	assert.Equal(t, "three", msgs[3].Text())
}

func TestRunToolsCanceledInParallel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wait := tool.Func("wait", func(ctx context.Context, _ ai.ToolCall) (ai.ToolResult, error) {
		<-ctx.Done()
		return ai.ToolResult{}, ctx.Err()
	})
	stop := tool.Func("stop", func(context.Context, ai.ToolCall) (ai.ToolResult, error) {
		cancel()
		return textResult("stopped"), nil
	})
	p := &script{responses: []ai.Message{toolCalls("wait", "stop")}}
	a := agent.New(model(p), agent.Toolset(wait, stop))

	events, msgs, err := collect(t, a.Run(ctx, ai.UserMessage("hi")))
	assert.ErrorIs(t, err, context.Canceled)
	assert.Len(t, afterBase(t, msgs), 1, "a canceled batch adds no result")
	assert.Equal(t, []string{"hi", ""}, historyTexts(a))
	emittest.CheckEvents(t, events)
	assert.IsType(t, agent.EventTurnEnd{}, events[len(events)-2])
	assert.Empty(t, a.State().Pending(), "agent_end clears the pending calls")

	ends := 0
	for _, ev := range events {
		if _, ok := ev.(agent.EventToolExecutionEnd); ok {
			ends++
		}
	}
	assert.Equal(t, 2, ends, "every call gets its end")
}
