package agent_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
)

func TestEventJSON(t *testing.T) {
	msg := ai.Message{
		Role:    ai.RoleAssistant,
		Content: []ai.Content{ai.Text{Text: "hi"}},
	}
	tests := []struct {
		name  string
		event agent.Event
		want  string
	}{
		{
			name:  "agent_start has only a type",
			event: agent.EventAgentStart{},
			want:  `{"type":"agent_start"}`,
		},
		{
			name:  "agent_end carries usage and the error",
			event: agent.EventAgentEnd{Usage: ai.Usage{Input: 2}, Error: "boom"},
			want:  `{"type":"agent_end","usage":{"input":2},"error":"boom"}`,
		},
		{
			name:  "turn_start carries the turn",
			event: agent.EventTurnStart{Turn: 1},
			want:  `{"type":"turn_start","turn":1}`,
		},
		{
			name:  "turn_end with no message",
			event: agent.EventTurnEnd{},
			want:  `{"type":"turn_end"}`,
		},
		{
			name: "turn_end carries the message and the tool results",
			event: agent.EventTurnEnd{
				Message:     &msg,
				ToolResults: []ai.Message{ai.ToolResultMessage("c1", ai.Text{Text: "ok"})},
			},
		},
		{
			name:  "message_start carries the message",
			event: agent.EventMessageStart{Message: msg},
		},
		{
			name: "message_update carries the snapshot and the model event",
			event: agent.EventMessageUpdate{
				Message:        msg,
				AssistantEvent: ai.Event{Type: ai.EventTextDelta, Delta: "hi"},
			},
		},
		{
			name:  "message_end carries the message",
			event: agent.EventMessageEnd{Message: msg},
			want:  `{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"hi"}]}}`,
		},
		{
			name: "tool_execution_start carries the call",
			event: agent.EventToolExecutionStart{
				ToolCallID: "c1",
				ToolName:   "echo",
				Args:       json.RawMessage(`{"s":"x"}`),
			},
			want: `{"type":"tool_execution_start","tool_call_id":"c1","tool_name":"echo","args":{"s":"x"}}`,
		},
		{
			name: "tool_execution_end carries the result",
			event: agent.EventToolExecutionEnd{
				ToolCallID: "c1",
				ToolName:   "echo",
				Result:     ai.ToolResult{Content: []ai.Content{ai.Text{Text: "ok"}}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.event)
			require.NoError(t, err)
			if tt.want != "" {
				assert.JSONEq(t, tt.want, string(data))
			}

			back, err := agent.UnmarshalEvent(data)
			require.NoError(t, err)
			assert.Equal(t, tt.event, back)
		})
	}
}

func TestUnmarshalEventUnknownType(t *testing.T) {
	_, err := agent.UnmarshalEvent([]byte(`{"type":"nope"}`))
	assert.ErrorContains(t, err, `unknown event type "nope"`)
}
