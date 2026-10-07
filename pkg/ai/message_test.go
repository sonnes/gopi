package ai_test

import (
	"encoding/json"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
)

func TestMessage_JSONRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		msg  ai.Message
	}{
		{
			name: "user",
			msg: ai.Message{
				Role:    ai.RoleUser,
				Content: []ai.Content{ai.Text{Text: "hi"}},
			},
		},
		{
			name: "assistant",
			msg: ai.Message{
				Role: ai.RoleAssistant,
				Content: []ai.Content{
					ai.ThinkingBlock{Thinking: "plan", Signature: "sig"},
					ai.Text{Text: "calling"},
					ai.ToolCall{
						ID:    "call_1",
						Name:  "weather",
						Input: json.RawMessage(`{"city":"Paris"}`),
					},
				},
				Model:      "claude-opus-5",
				Usage:      ai.Usage{Input: 10, Output: 5, CacheRead: 3, CacheWrite: 2},
				StopReason: ai.StopReasonToolUse,
			},
		},
		{
			name: "tool result",
			msg: ai.Message{
				Role:       ai.RoleToolResult,
				Content:    []ai.Content{ai.Text{Text: "boom"}},
				ToolCallID: "call_1",
				IsError:    true,
			},
		},
		{
			name: "tool result with a file",
			msg: ai.Message{
				Role: ai.RoleToolResult,
				Content: []ai.Content{
					ai.Text{Text: "cat.png"},
					ai.File{MediaType: "image/png", Data: []byte{0x89, 'P', 'N', 'G'}},
				},
				ToolCallID: "call_1",
			},
		},
		{
			name: "tool change",
			msg: ai.Message{
				Role: ai.RoleSystem,
				Content: []ai.Content{
					ai.ToolRemoved{Name: "bash"},
					ai.ToolAdded{Tool: ai.ToolInfo{
						Name:        "weather",
						Description: "Get the weather",
						InputSchema: &jsonschema.Schema{Type: "object"},
					}},
				},
			},
		},
		{
			name: "section change",
			msg: ai.Message{
				Role: ai.RoleSystem,
				Content: []ai.Content{
					ai.SectionRemoved{Name: "todos"},
					ai.Section{Name: "rules", Text: "<rules>Be brief.</rules>"},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.msg)
			require.NoError(t, err)

			var got ai.Message
			require.NoError(t, json.Unmarshal(data, &got))
			assert.Equal(t, tt.msg, got)
		})
	}
}

func TestMessage_WireShape(t *testing.T) {
	msg := ai.Message{
		Role: ai.RoleAssistant,
		Content: []ai.Content{
			ai.ToolCall{ID: "c1", Name: "echo", Input: json.RawMessage(`{"a":1}`)},
		},
	}

	data, err := json.Marshal(msg)
	require.NoError(t, err)

	want := `{
		"role": "assistant",
		"content": [
			{"type": "tool_call", "id": "c1", "name": "echo", "input": {"a": 1}}
		]
	}`
	assert.JSONEq(t, want, string(data))
}

func TestMessage_UnmarshalUnknownContent(t *testing.T) {
	var msg ai.Message
	err := json.Unmarshal([]byte(`{"role":"user","content":[{"type":"video"}]}`), &msg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "video")
}

func TestMessage_Text(t *testing.T) {
	msg := ai.AssistantMessage(
		ai.ThinkingBlock{Thinking: "hmm"},
		ai.Text{Text: "one"},
		ai.ToolCall{ID: "c", Name: "n"},
		ai.Text{Text: "two"},
	)
	assert.Equal(t, "onetwo", msg.Text())
	assert.Equal(t, "", ai.AssistantMessage().Text())
}

func TestMessage_ToolCalls(t *testing.T) {
	call := ai.ToolCall{ID: "c", Name: "n", Input: json.RawMessage(`{}`)}
	msg := ai.AssistantMessage(ai.Text{Text: "x"}, call)
	assert.Equal(t, []ai.ToolCall{call}, msg.ToolCalls())
	assert.Nil(t, ai.UserMessage("x").ToolCalls())
}

func TestConstructors(t *testing.T) {
	user := ai.UserMessage("hi")
	assert.Equal(t, ai.RoleUser, user.Role)
	assert.Equal(t, "hi", user.Text())

	result := ai.ToolResultMessage("call_1", ai.Text{Text: "ok"})
	assert.Equal(t, ai.RoleToolResult, result.Role)
	assert.Equal(t, "call_1", result.ToolCallID)
	assert.Equal(t, "ok", result.Text())
}

func TestSystemMessage(t *testing.T) {
	weather := ai.ToolInfo{Name: "weather"}
	msg := ai.SystemMessage(
		ai.Text{Text: "Be brief."},
		ai.Section{Name: "repo", Text: "gopi"},
		ai.ToolRemoved{Name: "bash"},
		ai.ToolAdded{Tool: weather},
	)

	assert.Equal(t, ai.RoleSystem, msg.Role)
	assert.Equal(t, []ai.Content{
		ai.Text{Text: "Be brief."},
		ai.Section{Name: "repo", Text: "gopi"},
		ai.ToolRemoved{Name: "bash"},
		ai.ToolAdded{Tool: weather},
	}, msg.Content, "the blocks keep their order")
}

func TestCurrentTools(t *testing.T) {
	read := ai.ToolInfo{Name: "read"}
	bash := ai.ToolInfo{Name: "bash"}
	weather := ai.ToolInfo{Name: "weather"}
	newRead := ai.ToolInfo{Name: "read", Description: "Read a file"}

	tests := []struct {
		name     string
		messages []ai.Message
		want     []ai.ToolInfo
	}{
		{
			name: "no changes",
			messages: []ai.Message{
				ai.UserMessage("hi"),
			},
			want: []ai.ToolInfo{read, bash},
		},
		{
			name: "add and remove",
			messages: []ai.Message{
				ai.SystemMessage(ai.ToolRemoved{Name: "bash"}, ai.ToolAdded{Tool: weather}),
			},
			want: []ai.ToolInfo{read, weather},
		},
		{
			name: "replace in place",
			messages: []ai.Message{
				ai.SystemMessage(ai.ToolAdded{Tool: newRead}),
			},
			want: []ai.ToolInfo{newRead, bash},
		},
		{
			name: "remove then add again",
			messages: []ai.Message{
				ai.SystemMessage(ai.ToolRemoved{Name: "read"}),
				ai.UserMessage("hi"),
				ai.SystemMessage(ai.ToolAdded{Tool: read}),
			},
			want: []ai.ToolInfo{bash, read},
		},
		{
			name: "blocks outside a system message",
			messages: []ai.Message{
				{
					Role:    ai.RoleUser,
					Content: []ai.Content{ai.ToolRemoved{Name: "bash"}},
				},
			},
			want: []ai.ToolInfo{read, bash},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ai.CurrentTools([]ai.ToolInfo{read, bash}, tt.messages)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestMessage_SectionWireShape(t *testing.T) {
	msg := ai.SystemMessage(ai.SectionRemoved{Name: "todos"}, ai.Section{Name: "rules", Text: "Be brief."})

	data, err := json.Marshal(msg)
	require.NoError(t, err)

	want := `{
		"role": "system",
		"content": [
			{"type": "section_removed", "name": "todos"},
			{"type": "section", "name": "rules", "text": "Be brief."}
		]
	}`
	assert.JSONEq(t, want, string(data))
}

func TestToolResult_JSONRoundTrip(t *testing.T) {
	res := ai.ToolResult{
		Content: []ai.Content{ai.Text{Text: "out"}},
		IsError: true,
	}

	data, err := json.Marshal(res)
	require.NoError(t, err)
	assert.JSONEq(t, `{"content":[{"type":"text","text":"out"}],"is_error":true}`, string(data))

	var got ai.ToolResult
	require.NoError(t, json.Unmarshal(data, &got))
	assert.Equal(t, res, got)
}
