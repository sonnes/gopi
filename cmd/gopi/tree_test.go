package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
)

func update(typ ai.EventType, index int, delta string) agent.Event {
	return agent.EventMessageUpdate{
		AssistantEvent: ai.Event{
			Type:         typ,
			ContentIndex: index,
			Delta:        delta,
		},
	}
}

func message(m ai.Message) []agent.Event {
	return []agent.Event{
		agent.EventMessageStart{Message: m},
		agent.EventMessageEnd{Message: m},
	}
}

func toolEnd(id, name, text string, isError bool) agent.Event {
	return agent.EventToolExecutionEnd{
		ToolCallID: id,
		ToolName:   name,
		Result: ai.ToolResult{
			Content: []ai.Content{ai.Text{Text: text}},
			IsError: isError,
		},
	}
}

func events(groups ...[]agent.Event) []agent.Event {
	var all []agent.Event
	for _, g := range groups {
		all = append(all, g...)
	}
	return all
}

func one(e ...agent.Event) []agent.Event { return e }

func TestTree(t *testing.T) {
	streamed := ai.AssistantMessage(ai.Text{Text: "streamed"})

	tests := []struct {
		name   string
		events []agent.Event
		want   string
	}{
		{
			name: "a run with thinking, text, and tools",
			events: events(
				one(
					agent.EventAgentStart{},
					agent.EventTurnStart{Turn: 1},
				),
				message(ai.UserMessage("Is Lisbon warmer than Oslo?")),
				one(
					agent.EventMessageStart{Message: ai.AssistantMessage()},
					update(ai.EventThinkStart, 0, ""),
					update(ai.EventThinkDelta, 0, "I need both\n"),
					update(ai.EventThinkDelta, 0, "cities."),
					update(ai.EventThinkEnd, 0, ""),
					update(ai.EventTextStart, 1, ""),
					update(ai.EventTextDelta, 1, "\n\nI will check."),
					update(ai.EventTextEnd, 1, ""),
					update(ai.EventToolStart, 2, ""),
					update(ai.EventToolDelta, 2, `{"city":`),
					update(ai.EventToolEnd, 2, ""),
					agent.EventMessageEnd{Message: streamed},
					agent.EventToolExecutionStart{
						ToolCallID: "call_a",
						ToolName:   "weather",
						Args:       json.RawMessage(`{ "city": "Lisbon" }`),
					},
					agent.EventToolExecutionStart{
						ToolCallID: "call_b",
						ToolName:   "weather",
						Args:       json.RawMessage(`{ "city": "Oslo" }`),
					},
					toolEnd("call_b", "weather", "no data\nfor Oslo", true),
					toolEnd("call_a", "weather", `{"temp_c":21}`, false),
				),
				message(ai.ToolResultMessage("call_1", ai.Text{Text: "21"})),
				one(
					agent.EventTurnEnd{},
					agent.EventTurnStart{Turn: 2},
					agent.EventToolExecutionStart{
						ToolCallID: "call_c",
						ToolName:   "weather",
						Args:       json.RawMessage(`{"city":"Rome"}`),
					},
					toolEnd("call_c", "weather", "ok", false),
					update(ai.EventTextDelta, 0, "Lisbon is"),
					update(ai.EventTextDelta, 0, " warmer.\n"),
					update(ai.EventTextDelta, 0, "\nIt is 21 degrees.\n"),
					agent.EventMessageEnd{Message: streamed},
					agent.EventTurnEnd{},
					agent.EventAgentEnd{
						Usage: ai.Usage{
							Input:     300,
							Output:    42,
							CacheRead: 10,
							Cost:      ai.Cost{Input: 0.0031},
						},
					},
				),
			),
			want: `
agent  fake/model
├─ turn 1
│  ├─ thinking  I need both
│  │  cities.
│  ├─ I will check.
│  ├─ ● 1 weather {"city":"Lisbon"}
│  ├─ ● 2 weather {"city":"Oslo"}
│  ├─ ✗ 2 weather no data for Oslo
│  ├─ ✓ 1 weather {"temp_c":21}
├─ turn 2
│  ├─ ● 1 weather {"city":"Rome"}
│  ├─ ✓ 1 weather ok
│  ├─ Lisbon is warmer.
│  │
│  │  It is 21 degrees.
└─ usage  in=300 out=42 cache_read=10 $0.0031
`,
		},
		{
			name: "a message with no stream events",
			events: events(
				one(
					agent.EventAgentStart{},
					agent.EventTurnStart{Turn: 1},
				),
				message(ai.AssistantMessage(
					ai.ThinkingBlock{Thinking: "Short."},
					ai.Text{Text: "Hi there."},
				)),
				one(agent.EventAgentEnd{}),
			),
			want: `
agent  fake/model
├─ turn 1
│  ├─ thinking  Short.
│  ├─ Hi there.
└─ usage  in=0 out=0
`,
		},
		{
			name: "a stop at the output cap",
			events: events(
				one(
					agent.EventAgentStart{},
					agent.EventTurnStart{Turn: 1},
					update(ai.EventTextDelta, 0, "Part"),
					agent.EventMessageEnd{Message: ai.Message{
						Role:       ai.RoleAssistant,
						Content:    []ai.Content{ai.Text{Text: "Part"}},
						StopReason: ai.StopReasonLength,
					}},
					agent.EventAgentEnd{},
				),
			),
			want: `
agent  fake/model
├─ turn 1
│  ├─ Part
│  ├─ ✗ stopped: length
└─ usage  in=0 out=0
`,
		},
		{
			name: "a failed run",
			events: events(
				one(
					agent.EventAgentStart{},
					agent.EventTurnStart{Turn: 1},
					update(ai.EventTextDelta, 0, "par"),
					agent.EventMessageEnd{Message: ai.Message{
						Role:       ai.RoleAssistant,
						Content:    []ai.Content{ai.Text{Text: "par"}},
						StopReason: ai.StopReasonError,
						StopDetail: "boom",
					}},
					agent.EventTurnEnd{},
					agent.EventAgentEnd{
						Usage: ai.Usage{Input: 5},
						Error: "boom",
					},
				),
			),
			want: `
agent  fake/model
├─ turn 1
│  ├─ par
├─ ✗ error: boom
└─ usage  in=5 out=0
`,
		},
		{
			name: "control codes and a multi-line error",
			events: events(
				one(
					agent.EventAgentStart{},
					agent.EventTurnStart{Turn: 1},
					update(ai.EventTextDelta, 0, "\x1b[2Jred\x07"),
					agent.EventToolExecutionStart{
						ToolCallID: "call_a",
						ToolName:   "bash",
						Args:       json.RawMessage(`{}`),
					},
					toolEnd("call_a", "bash", "\x1b]0;title\x07done", false),
					agent.EventAgentEnd{Error: "status 400:\n{\n  \"error\": \"bad\"\n}"},
				),
			),
			want: `
agent  fake/model
├─ turn 1
│  ├─ [2Jred
│  ├─ ● 1 bash {}
│  ├─ ✓ 1 bash ]0;titledone
├─ ✗ error: status 400: { "error": "bad" }
└─ usage  in=0 out=0
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out strings.Builder
			tr := newTree(&out, "fake/model", false)
			for _, e := range tt.events {
				tr.print(e)
			}
			assert.Equal(t, strings.TrimPrefix(tt.want, "\n"), out.String())
		})
	}
}
