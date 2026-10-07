package openai

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
)

// input converts the messages and decodes the input items, so a test
// checks what the API would receive.
func input(t *testing.T, messages []ai.Message) []any {
	t.Helper()

	data, err := json.Marshal(convertMessages(messages, convertConfig{systemMessages: true, addTools: true}))
	require.NoError(t, err)

	var items []any
	require.NoError(t, json.Unmarshal(data, &items))
	return items
}

func TestConvertMessages_User(t *testing.T) {
	items := input(t, []ai.Message{ai.UserMessage("Hello")})

	require.Len(t, items, 1)
	assert.Equal(t, map[string]any{
		"role": "user",
		"content": []any{map[string]any{
			"type": "input_text",
			"text": "Hello",
		}},
	}, items[0])
}

func TestConvertMessages_AssistantKeepsOrder(t *testing.T) {
	sig := signature{ID: "rs_1", EncryptedContent: "enc"}.String()
	msg := ai.AssistantMessage(
		ai.ThinkingBlock{Thinking: "Look up the weather.", Signature: sig},
		ai.Text{Text: "Checking."},
		ai.ToolCall{ID: "call_1", Name: "get_weather", Input: json.RawMessage(`{"city":"Paris"}`)},
		ai.ToolCall{ID: "call_2", Name: "get_weather", Input: json.RawMessage(`{"city":"Rome"}`)},
	)

	results := []ai.Message{
		ai.ToolResultMessage("call_1", ai.Text{Text: "sunny"}),
		ai.ToolResultMessage("call_2", ai.Text{Text: "rain"}),
	}
	items := input(t, append([]ai.Message{msg}, results...))

	require.Len(t, items, 6)
	assert.Equal(t, map[string]any{
		"type":              "reasoning",
		"id":                "rs_1",
		"encrypted_content": "enc",
		"summary": []any{map[string]any{
			"type": "summary_text",
			"text": "Look up the weather.",
		}},
	}, items[0])
	assert.Equal(t, map[string]any{
		"role":    "assistant",
		"content": "Checking.",
	}, items[1])
	assert.Equal(t, map[string]any{
		"type":      "function_call",
		"call_id":   "call_1",
		"name":      "get_weather",
		"arguments": `{"city":"Paris"}`,
	}, items[2])
	assert.Equal(t, "call_2", items[3].(map[string]any)["call_id"])
}

func TestConvertMessages_ReasoningWithoutSummary(t *testing.T) {
	sig := signature{ID: "rs_1", EncryptedContent: "enc"}.String()
	msg := ai.AssistantMessage(ai.ThinkingBlock{Signature: sig})

	items := input(t, []ai.Message{msg})

	require.Len(t, items, 1)
	assert.Equal(t, []any{}, items[0].(map[string]any)["summary"])
}

func TestConvertMessages_DropsForeignThinking(t *testing.T) {
	msg := ai.AssistantMessage(
		ai.ThinkingBlock{Thinking: "Hmm.", Signature: "an-anthropic-signature"},
		ai.ThinkingBlock{Thinking: "Unsigned."},
		ai.Text{Text: "Hi."},
	)

	items := input(t, []ai.Message{msg})

	require.Len(t, items, 1)
	assert.Equal(t, "assistant", items[0].(map[string]any)["role"])
}

func TestConvertMessages_ToolResult(t *testing.T) {
	result := ai.ToolResultMessage("call_1", ai.Text{Text: "Sunny, "}, ai.Text{Text: "22C"})

	items := input(t, []ai.Message{result})

	require.Len(t, items, 1)
	assert.Equal(t, map[string]any{
		"type":    "function_call_output",
		"call_id": "call_1",
		"output":  "Sunny, 22C",
	}, items[0])
}

func TestConvertMessages_ToolResultFiles(t *testing.T) {
	result := ai.ToolResultMessage(
		"call_1",
		ai.Text{Text: "two files"},
		ai.File{MediaType: "image/png", Data: []byte("png")},
		ai.File{MediaType: "application/pdf", Data: []byte("pdf")},
	)

	items := input(t, []ai.Message{result})

	require.Len(t, items, 1)
	assert.Equal(t, map[string]any{
		"type":    "function_call_output",
		"call_id": "call_1",
		"output": []any{
			map[string]any{"type": "input_text", "text": "two files"},
			map[string]any{"type": "input_image", "image_url": "data:image/png;base64,cG5n"},
			map[string]any{
				"type":      "input_file",
				"file_data": "data:application/pdf;base64,cGRm",
				"filename":  "file.pdf",
			},
		},
	}, items[0])
}

func TestConvertMessages_DropsEmptyText(t *testing.T) {
	items := input(t, []ai.Message{
		{Role: ai.RoleUser, Content: []ai.Content{ai.Text{}}},
		ai.AssistantMessage(ai.Text{}),
	})

	assert.Empty(t, items)
}
