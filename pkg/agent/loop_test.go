package agent_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/agent/internal/emittest"
	"github.com/sonnes/gopi/pkg/ai"
)

func TestRunText(t *testing.T) {
	p := &script{responses: []ai.Message{text("hello")}}
	a := agent.New(model(p))

	events, msgs, err := collect(t, a.Run(context.Background(), ai.UserMessage("hi")))
	require.NoError(t, err)

	emittest.CheckEvents(t, events)
	assert.Equal(t, types([]agent.Event{
		agent.EventAgentStart{},
		agent.EventTurnStart{},
		agent.EventMessageStart{},
		agent.EventMessageEnd{},
		agent.EventMessageStart{},
		agent.EventMessageUpdate{},
		agent.EventMessageUpdate{},
		agent.EventMessageEnd{},
		agent.EventTurnEnd{},
		agent.EventAgentEnd{},
	}), types(events))

	require.Len(t, msgs, 1)
	assert.Equal(t, "hello", msgs[0].Text())

	require.Len(t, p.received(), 1)
	assert.Equal(t, []string{"hi"}, msgTexts(p.received()[0]))
	assert.Equal(t, []string{"hi", "hello"}, msgTexts(history(a)))

	assert.Equal(t, 1, as[agent.EventTurnStart](t, events[1]).Turn, "the first turn is 1")
	assert.Equal(t, "hi", as[agent.EventMessageEnd](t, events[3]).Message.Text(), "the input arrives as message events in the turn")

	turnEnd := as[agent.EventTurnEnd](t, events[8])
	require.NotNil(t, turnEnd.Message)
	assert.Equal(t, "hello", turnEnd.Message.Text())
	assert.Empty(t, turnEnd.ToolResults)

	agentEnd := as[agent.EventAgentEnd](t, events[9])
	assert.Equal(t, ai.Usage{Input: 1, Output: 1}, agentEnd.Usage)
	assert.Empty(t, agentEnd.Error)
}

func TestRunToolLoop(t *testing.T) {
	p := &script{responses: []ai.Message{
		toolCall("c1", "echo", `{"s":"x"}`),
		text("done"),
	}}
	a := agent.New(model(p), agent.Toolset(echo))

	events, msgs, err := collect(t, a.Run(context.Background(), ai.UserMessage("hi")))
	require.NoError(t, err)
	emittest.CheckEvents(t, events)
	msgs = afterBase(t, msgs)

	assert.Equal(t, types([]agent.Event{
		agent.EventAgentStart{},
		agent.EventTurnStart{},
		agent.EventMessageStart{},
		agent.EventMessageEnd{},
		agent.EventMessageStart{},
		agent.EventMessageEnd{},
		agent.EventMessageStart{},
		agent.EventMessageUpdate{},
		agent.EventMessageUpdate{},
		agent.EventMessageEnd{},
		agent.EventToolExecutionStart{},
		agent.EventToolExecutionEnd{},
		agent.EventMessageStart{},
		agent.EventMessageEnd{},
		agent.EventTurnEnd{},
		agent.EventTurnStart{},
		agent.EventMessageStart{},
		agent.EventMessageUpdate{},
		agent.EventMessageUpdate{},
		agent.EventMessageEnd{},
		agent.EventTurnEnd{},
		agent.EventAgentEnd{},
	}), types(events))

	require.Len(t, msgs, 3)
	assert.Equal(t, ai.RoleToolResult, msgs[1].Role)
	assert.Equal(t, "c1", msgs[1].ToolCallID)
	assert.Equal(t, "echo:x", msgs[1].Text())
	assert.Equal(t, "done", msgs[2].Text())

	toolStart := as[agent.EventToolExecutionStart](t, events[10])
	assert.Equal(t, "c1", toolStart.ToolCallID)
	assert.Equal(t, "echo", toolStart.ToolName)
	assert.JSONEq(t, `{"s":"x"}`, string(toolStart.Args))

	toolEnd := as[agent.EventToolExecutionEnd](t, events[11])
	assert.Equal(t, "c1", toolEnd.ToolCallID)
	assert.Equal(t, "echo", toolEnd.ToolName)
	assert.Equal(t, "echo:x", toolEnd.Result.Content[0].(ai.Text).Text)

	turnEnd := as[agent.EventTurnEnd](t, events[14])
	require.NotNil(t, turnEnd.Message)
	assert.Equal(t, msgs[0], *turnEnd.Message)
	assert.Equal(t, msgs[1:2], turnEnd.ToolResults)

	calls := p.received()
	require.Len(t, calls, 2)
	assert.Len(t, calls[1], 3, "the second call sees the tool result")
	toolset, _ := ai.GetOption(p.opts[0], ai.Tools)
	require.Len(t, toolset, 1)
	assert.Equal(t, "echo", toolset[0].Name)

	assert.Equal(t, []string{"hi", "", "echo:x", "done"}, historyTexts(a))

	agentEnd := as[agent.EventAgentEnd](t, events[len(events)-1])
	assert.Equal(t, ai.Usage{Input: 2, Output: 2}, agentEnd.Usage)
}

func TestRunContinuesWithoutInput(t *testing.T) {
	p := &script{responses: []ai.Message{text("one"), text("two")}}
	a := agent.New(model(p))

	_, _, err := collect(t, a.Run(context.Background(), ai.UserMessage("hi")))
	require.NoError(t, err)
	_, msgs, err := collect(t, a.Run(context.Background()))
	require.NoError(t, err)

	assert.Equal(t, "two", msgs[0].Text())
	assert.Equal(t, []string{"hi", "one"}, msgTexts(p.received()[1]))
}

func TestMessageEndCarriesTheHistory(t *testing.T) {
	p := &script{responses: []ai.Message{
		toolCall("c1", "echo", `{"s":"x"}`),
		text("done"),
	}}
	a := agent.New(model(p), agent.Toolset(echo))

	events, _, err := collect(t, a.Run(context.Background(), ai.UserMessage("hi")))
	require.NoError(t, err)

	var saved []ai.Message
	for _, e := range events {
		if end, ok := e.(agent.EventMessageEnd); ok {
			saved = append(saved, end.Message)
		}
	}
	assert.Equal(t, history(a), saved, "a caller that saves each message_end saves the history")
}
