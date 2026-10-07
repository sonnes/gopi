package openaichat

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
)

// messages converts the messages and decodes them, so a test checks
// what the API would receive.
func messages(t *testing.T, in []ai.Message) []any {
	t.Helper()

	data, err := json.Marshal(convertMessages(in, convertConfig{systemMessages: true}))
	require.NoError(t, err)

	var out []any
	require.NoError(t, json.Unmarshal(data, &out))
	return out
}

func TestConvertMessages_User(t *testing.T) {
	out := messages(t, []ai.Message{ai.UserMessage("Hello")})

	require.Len(t, out, 1)
	assert.Equal(t, map[string]any{"role": "user", "content": "Hello"}, out[0])
}

func TestConvertMessages_UserWithManyBlocks(t *testing.T) {
	msg := ai.Message{
		Role:    ai.RoleUser,
		Content: []ai.Content{ai.Text{Text: "One."}, ai.Text{Text: "Two."}},
	}

	out := messages(t, []ai.Message{msg})

	require.Len(t, out, 1)
	assert.Equal(t, []any{
		map[string]any{"type": "text", "text": "One."},
		map[string]any{"type": "text", "text": "Two."},
	}, out[0].(map[string]any)["content"])
}

func TestConvertMessages_Assistant(t *testing.T) {
	msg := ai.AssistantMessage(
		ai.ThinkingBlock{Thinking: "Look up the weather."},
		ai.Text{Text: "Checking."},
		ai.ToolCall{ID: "call_1", Name: "get_weather", Input: json.RawMessage(`{"city":"Paris"}`)},
	)

	out := messages(t, []ai.Message{msg})

	require.Len(t, out, 2, "the call gets a result")
	assert.Equal(t, "tool", out[1].(map[string]any)["role"])
	assert.Equal(t, map[string]any{
		"role":              "assistant",
		"content":           "Checking.",
		"reasoning_content": "Look up the weather.",
		"tool_calls": []any{map[string]any{
			"id":   "call_1",
			"type": "function",
			"function": map[string]any{
				"name":      "get_weather",
				"arguments": `{"city":"Paris"}`,
			},
		}},
	}, out[0])
}

func TestConvertMessages_AssistantToolCallOnly(t *testing.T) {
	msg := ai.AssistantMessage(
		ai.ToolCall{ID: "call_1", Name: "get_weather", Input: json.RawMessage(`{}`)},
	)

	out := messages(t, []ai.Message{msg})

	require.Len(t, out, 2, "the call gets a result")
	assert.Equal(t, "tool", out[1].(map[string]any)["role"])
	item := out[0].(map[string]any)
	_, hasContent := item["content"]
	assert.False(t, hasContent)
	_, hasReasoning := item["reasoning_content"]
	assert.False(t, hasReasoning)
}

func TestConvertMessages_ToolResult(t *testing.T) {
	result := ai.ToolResultMessage("call_1", ai.Text{Text: "Sunny, "}, ai.Text{Text: "22C"})

	out := messages(t, []ai.Message{result})

	require.Len(t, out, 1)
	assert.Equal(t, map[string]any{
		"role":         "tool",
		"tool_call_id": "call_1",
		"content":      "Sunny, 22C",
	}, out[0])
}

func TestConvertMessages_ToolResultFile(t *testing.T) {
	result := ai.ToolResultMessage(
		"call_1",
		ai.Text{Text: "cat.png"},
		ai.File{MediaType: "image/png", Data: []byte("png")},
	)

	out := messages(t, []ai.Message{result})

	require.Len(t, out, 2)
	assert.Equal(t, "cat.png", out[0].(map[string]any)["content"])
	assert.Equal(t, map[string]any{
		"role": "user",
		"content": []any{
			map[string]any{"type": "text", "text": "Attached image(s) from tool result:"},
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,cG5n"}},
		},
	}, out[1], "the tool message takes only text, so the image follows in a user message")
}

func TestConvertMessages_DropsEmpty(t *testing.T) {
	out := messages(t, []ai.Message{
		{Role: ai.RoleUser, Content: []ai.Content{ai.Text{}}},
		ai.AssistantMessage(ai.Text{}),
	})

	assert.Empty(t, out)
}
