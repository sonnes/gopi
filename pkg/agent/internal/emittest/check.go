// Package emittest checks the event order of a run in a test. It is a
// separate package, so that the agent package does not link testify.
package emittest

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
)

// CheckEvents fails t if events break the event order of a run:
//
//   - agent_start is first, and agent_end is last. A run that starts
//     always ends, also when it fails.
//   - turn_start and turn_end are balanced and do not nest.
//   - message_start and message_end are balanced, do not nest, and are
//     in a turn. A user or system message that the loop adds between
//     turns is the exception. A message_update is in an open message and has an
//     AssistantEvent.
//   - Each tool_execution_start is in a turn, outside a message, and
//     has one tool_execution_end.
//   - Every event of a run that ends is closed: no open message, tool
//     call, or turn.
//
// A test of the agent loop runs CheckEvents on the events of its runs.
func CheckEvents(t testing.TB, events []agent.Event) {
	t.Helper()
	if !assert.NotEmpty(t, events, "a run sends events") {
		return
	}
	assert.IsType(t, agent.EventAgentStart{}, events[0], "agent_start is first")
	assert.IsType(t, agent.EventAgentEnd{}, events[len(events)-1], "agent_end is last")

	var (
		inTurn bool
		inMsg  bool
		tools  = map[string]bool{}
	)
	for i, ev := range events {
		switch ev := ev.(type) {
		case agent.EventAgentStart:
			assert.Zero(t, i, "event %d: agent_start is first", i)
		case agent.EventAgentEnd:
			assert.Equal(t, len(events)-1, i, "event %d: agent_end is last", i)
		case agent.EventTurnStart:
			assert.False(t, inTurn, "event %d: turn_start in an open turn", i)
			inTurn = true
		case agent.EventTurnEnd:
			assert.True(t, inTurn, "event %d: turn_end with no turn", i)
			assert.False(t, inMsg, "event %d: turn_end in an open message", i)
			assert.Empty(t, tools, "event %d: turn_end with open tool calls", i)
			if ev.Message != nil {
				assert.Equal(t, ai.RoleAssistant, ev.Message.Role, "event %d: turn_end message", i)
			}
			inTurn = false
		case agent.EventMessageStart:
			assert.False(t, inMsg, "event %d: message_start in an open message", i)
			betweenTurns := ev.Message.Role == ai.RoleUser || ev.Message.Role == ai.RoleSystem
			assert.True(t, inTurn || betweenTurns, "event %d: message_start outside a turn", i)
			inMsg = true
		case agent.EventMessageUpdate:
			assert.True(t, inMsg, "event %d: message_update outside a message", i)
			assert.NotZero(t, ev.AssistantEvent, "event %d: message_update has a model event", i)
		case agent.EventMessageEnd:
			assert.True(t, inMsg, "event %d: message_end with no message", i)
			inMsg = false
		case agent.EventToolExecutionStart:
			assert.True(t, inTurn, "event %d: tool_execution_start outside a turn", i)
			assert.False(t, inMsg, "event %d: tool_execution_start in a message", i)
			assert.NotEmpty(t, ev.ToolCallID, "event %d: tool_execution_start has a call ID", i)
			assert.False(t, tools[ev.ToolCallID], "event %d: tool call %q started twice", i, ev.ToolCallID)
			tools[ev.ToolCallID] = true
		case agent.EventToolExecutionEnd:
			assert.True(t, tools[ev.ToolCallID], "event %d: tool call %q ended with no start", i, ev.ToolCallID)
			delete(tools, ev.ToolCallID)
		default:
			assert.Fail(t, "unknown event type", "event %d: %T", i, ev)
		}
	}
	assert.False(t, inTurn, "the run left a turn open")
	assert.False(t, inMsg, "the run left a message open")
	assert.Empty(t, tools, "the run left tool calls open")
}

// Types returns the JSON type name of each event, such as
// "message_start", so that a test compares the order of a run in one
// assert.
func Types(events []agent.Event) []string {
	out := make([]string, 0, len(events))
	for _, ev := range events {
		data, err := json.Marshal(ev)
		if err != nil {
			panic(err)
		}
		var v struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(data, &v); err != nil {
			panic(err)
		}
		out = append(out, v.Type)
	}
	return out
}
