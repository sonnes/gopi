package gemini_test

import (
	"encoding/json"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/internal/transcript"
	"github.com/sonnes/gopi/pkg/ai"
)

// request runs one call with messages and opts, and returns the body
// that the provider sent.
func request(t *testing.T, messages []ai.Message, opts ...ai.Options) string {
	t.Helper()

	p, body := fakeProvider(t, completed)
	_, err := p.StreamText(t.Context(), fakeModel, messages, ai.JoinOptions(opts...)).Wait()
	require.NoError(t, err)
	return string(body())
}

var weatherTool = ai.ToolInfo{
	Name:        "get_weather",
	Description: "Get the weather of a city",
	InputSchema: &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"city": {Type: "string"},
		},
	},
}

func TestRequest_Conversation(t *testing.T) {
	messages := []ai.Message{
		ai.UserMessage("Weather in Paris?"),
		ai.AssistantMessage(
			ai.ThinkingBlock{Thinking: "Look it up.", Signature: `{"gemini":"sig-1"}`},
			ai.Text{Text: "Checking."},
			ai.ToolCall{ID: "call-1", Name: "get_weather", Input: json.RawMessage(`{"city":"Paris"}`)},
		),
		ai.ToolResultMessage("call-1", ai.Text{Text: "Sunny"}),
	}

	body := request(t, messages,
		ai.SystemPrompt([]ai.Content{ai.Text{Text: "Be brief."}}),
		ai.Tools([]ai.ToolInfo{weatherTool}),
		ai.MaxTokens(500),
		ai.Thinking(ai.ThinkingHigh),
	)

	assert.JSONEq(t, `{
		"model": "gemini-3.8-flash",
		"store": false,
		"stream": true,
		"system_instruction": "Be brief.",
		"input": [
			{"type": "user_input", "content": [{"type": "text", "text": "Weather in Paris?"}]},
			{"type": "thought", "summary": [{"type": "text", "text": "Look it up."}], "signature": "sig-1"},
			{"type": "model_output", "content": [{"type": "text", "text": "Checking."}]},
			{"type": "function_call", "id": "call-1", "name": "get_weather", "arguments": {"city": "Paris"}},
			{"type": "function_result", "call_id": "call-1", "name": "get_weather", "result": [{"type": "text", "text": "Sunny"}]}
		],
		"tools": [{
			"type": "function",
			"name": "get_weather",
			"description": "Get the weather of a city",
			"parameters": {"type": "object", "properties": {"city": {"type": "string"}}}
		}],
		"generation_config": {
			"max_output_tokens": 500,
			"thinking_level": "high",
			"thinking_summaries": "auto"
		}
	}`, body)
}

func TestRequest_Files(t *testing.T) {
	png := ai.File{MediaType: "image/png", Data: []byte("png")}
	pdf := ai.File{MediaType: "application/pdf", Data: []byte("pdf")}
	messages := []ai.Message{
		{Role: ai.RoleUser, Content: []ai.Content{ai.Text{Text: "Read these."}, png, pdf}},
		ai.AssistantMessage(ai.ToolCall{ID: "call-1", Name: "screenshot", Input: json.RawMessage(`{}`)}),
		{Role: ai.RoleToolResult, ToolCallID: "call-1", IsError: true, Content: []ai.Content{ai.Text{Text: "failed"}, png}},
	}

	body := request(t, messages)

	var got struct {
		Input []json.RawMessage `json:"input"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &got))
	require.Len(t, got.Input, 4)
	assert.JSONEq(t, `{"type": "user_input", "content": [
		{"type": "text", "text": "Read these."},
		{"type": "image", "mime_type": "image/png", "data": "cG5n"},
		{"type": "document", "mime_type": "application/pdf", "data": "cGRm"}
	]}`, string(got.Input[0]))
	assert.JSONEq(t, `{"type": "function_result", "call_id": "call-1", "name": "screenshot", "is_error": true, "result": [
		{"type": "text", "text": "failed"},
		{"type": "image", "mime_type": "image/png", "data": "cG5n"}
	]}`, string(got.Input[3]))
}

func TestRequest_ForeignThinking(t *testing.T) {
	messages := []ai.Message{
		ai.UserMessage("Hi"),
		ai.AssistantMessage(
			ai.ThinkingBlock{Thinking: "From Claude.", Signature: "anthropic-signature"},
			ai.Text{Text: "Hello."},
		),
		ai.UserMessage("Again"),
	}

	body := request(t, messages)

	var got struct {
		Input []map[string]any `json:"input"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &got))
	var types []any
	for _, step := range got.Input {
		types = append(types, step["type"])
	}
	assert.Equal(t, []any{"user_input", "model_output", "user_input"}, types)
}

func TestRequest_ForeignToolCall(t *testing.T) {
	messages := []ai.Message{
		ai.UserMessage("Weather in Paris?"),
		ai.AssistantMessage(
			ai.ThinkingBlock{Thinking: "From Claude.", Signature: "anthropic-signature"},
			ai.Text{Text: "Checking."},
			ai.ToolCall{ID: "toolu_01", Name: "get_weather", Input: json.RawMessage(`{"city":"Paris"}`)},
		),
		ai.ToolResultMessage("toolu_01", ai.Text{Text: "Sunny"}),
	}

	body := request(t, messages)

	var got struct {
		Input []json.RawMessage `json:"input"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &got))
	require.Len(t, got.Input, 5)
	assert.JSONEq(t, `{"type": "thought", "signature": "skip_thought_signature_validator"}`, string(got.Input[1]))
	assert.JSONEq(t, `{"type": "model_output", "content": [{"type": "text", "text": "Checking."}]}`, string(got.Input[2]))
	assert.JSONEq(t, `{"type": "function_call", "id": "toolu_01", "name": "get_weather", "arguments": {"city": "Paris"}}`, string(got.Input[3]))
}

func TestRequest_SystemMessage(t *testing.T) {
	messages := []ai.Message{
		ai.UserMessage("Hi"),
		ai.SystemMessage(ai.Text{Text: "Answer in French."}),
		ai.UserMessage("Again"),
	}

	body := request(t, messages)

	var got struct {
		Input             []json.RawMessage `json:"input"`
		SystemInstruction *string           `json:"system_instruction"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &got))
	assert.Nil(t, got.SystemInstruction)
	require.Len(t, got.Input, 2)
	assert.JSONEq(t, `{"type": "user_input", "content": [{"type": "text", "text": "Hi"}]}`, string(got.Input[0]))

	reminder, err := json.Marshal(transcript.Reminder("Answer in French."))
	require.NoError(t, err)
	assert.JSONEq(t, `{"type": "user_input", "content": [
		{"type": "text", "text": `+string(reminder)+`},
		{"type": "text", "text": "Again"}
	]}`, string(got.Input[1]))
}

func TestRequest_ThinkingLevels(t *testing.T) {
	tests := []struct {
		level ai.ThinkingLevel
		want  string
	}{
		{ai.ThinkingOff, "minimal"},
		{ai.ThinkingLow, "low"},
		{ai.ThinkingMedium, "medium"},
		{ai.ThinkingHigh, "high"},
		{ai.ThinkingXHigh, "high"},
		{ai.ThinkingMax, "high"},
	}
	for _, tt := range tests {
		t.Run(string(tt.level), func(t *testing.T) {
			body := request(t, []ai.Message{ai.UserMessage("Hi")}, ai.Thinking(tt.level))

			var got struct {
				Config struct {
					Level string `json:"thinking_level"`
				} `json:"generation_config"`
			}
			require.NoError(t, json.Unmarshal([]byte(body), &got))
			assert.Equal(t, tt.want, got.Config.Level)
		})
	}
}

func TestRequest_ToolChoice(t *testing.T) {
	tests := []struct {
		name   string
		choice ai.ToolChoiceMode
		want   string
	}{
		{"auto", ai.ToolChoiceAuto, `"auto"`},
		{"any", ai.ToolChoiceAny, `"any"`},
		{"none", ai.ToolChoiceNone, `"none"`},
		{"tool", ai.ToolChoiceTool("get_weather"), `{"allowed_tools": {"mode": "any", "tools": ["get_weather"]}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := request(t, []ai.Message{ai.UserMessage("Hi")},
				ai.Tools([]ai.ToolInfo{weatherTool}),
				ai.ToolChoice(tt.choice),
			)

			var got struct {
				Config struct {
					ToolChoice json.RawMessage `json:"tool_choice"`
				} `json:"generation_config"`
			}
			require.NoError(t, json.Unmarshal([]byte(body), &got))
			assert.JSONEq(t, tt.want, string(got.Config.ToolChoice))
		})
	}
}
