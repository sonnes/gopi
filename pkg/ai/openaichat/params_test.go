package openaichat

import (
	"encoding/json"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/ai/openai"
)

var testModel = ai.Model{ID: "gpt-6-luna", Name: "GPT-6 Luna", MaxTokens: 128000}

// plainModel takes no system messages, like a model on Fireworks.
var plainModel = ai.Model{ID: "accounts/fireworks/models/kimi-k2-thinking", MaxTokens: 128000}

type weatherInput struct {
	City string `json:"city"`
}

// wire builds the request for the messages and options, and decodes the
// JSON body into a map, so a test checks what the API would receive.
func wire(t *testing.T, cfg config, model ai.Model, messages []ai.Message, opts ai.Options) map[string]any {
	t.Helper()

	params, err := buildParams(cfg, model, messages, opts)
	require.NoError(t, err)

	return toBody(t, params)
}

func toBody(t *testing.T, v any) map[string]any {
	t.Helper()

	data, err := json.Marshal(v)
	require.NoError(t, err)

	var body map[string]any
	require.NoError(t, json.Unmarshal(data, &body))
	return body
}

// Fireworks rejects a field that it does not know, so the request holds
// only the fields that every compatible server accepts.
func TestBuildParams_CompatibleFields(t *testing.T) {
	body := wire(t, config{}, testModel, []ai.Message{ai.UserMessage("Hi")}, nil)

	keys := make([]string, 0, len(body))
	for k := range body {
		keys = append(keys, k)
	}
	assert.ElementsMatch(t, []string{"model", "messages", "max_completion_tokens"}, keys)
}

func TestBuildParams_MaxTokens(t *testing.T) {
	tests := []struct {
		name  string
		model ai.Model
		opts  ai.Options
		want  any
	}{
		{name: "option", model: testModel, opts: ai.MaxTokens(100), want: float64(100)},
		{name: "model", model: testModel, want: float64(128000)},
		{name: "unset", model: ai.Model{ID: "x"}, want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := wire(t, config{}, tt.model, nil, tt.opts)
			assert.Equal(t, tt.want, body["max_completion_tokens"])
			_, ok := body["max_tokens"]
			assert.False(t, ok)
		})
	}
}

func TestBuildParams_Thinking(t *testing.T) {
	tests := []struct {
		name string
		opts ai.Options
		want any
	}{
		{name: "unset", want: nil},
		{name: "off", opts: ai.Thinking(ai.ThinkingOff), want: "none"},
		{name: "low", opts: ai.Thinking(ai.ThinkingLow), want: "low"},
		{name: "xhigh", opts: ai.Thinking(ai.ThinkingXHigh), want: "xhigh"},
		{name: "max", opts: ai.Thinking(ai.ThinkingMax), want: "max"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := wire(t, config{}, testModel, nil, tt.opts)
			assert.Equal(t, tt.want, body["reasoning_effort"])
		})
	}
}

func TestBuildParams_UnknownThinkingLevel(t *testing.T) {
	_, err := buildParams(config{}, testModel, nil, ai.Thinking("deep"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "deep")
}

func TestBuildParams_System(t *testing.T) {
	body := wire(t, config{}, testModel, []ai.Message{ai.UserMessage("Hi")}, ai.SystemPrompt([]ai.Content{ai.Text{Text: "Be brief."}}))

	messages := body["messages"].([]any)
	require.Len(t, messages, 2)
	assert.Equal(t, map[string]any{"role": "system", "content": "Be brief."}, messages[0])
}

func TestBuildParams_SystemMessages(t *testing.T) {
	messages := []ai.Message{
		ai.UserMessage("Hi"),
		ai.SystemMessage(ai.Text{Text: "Answer in French."}),
	}
	body := wire(t, config{}, testModel, messages, nil)

	sent := body["messages"].([]any)
	require.Len(t, sent, 2)
	assert.Equal(t, map[string]any{"role": "system", "content": "Answer in French."}, sent[1])
}

func TestBuildParams_Sections(t *testing.T) {
	messages := []ai.Message{
		ai.SystemMessage(ai.Section{Name: "rules", Text: "Be brief."}),
		ai.UserMessage("Hi"),
		ai.SystemMessage(ai.Section{Name: "rules", Text: "Be thorough."}),
	}
	body := wire(t, config{}, testModel, messages, nil)

	sent := body["messages"].([]any)
	require.Len(t, sent, 3)
	assert.Equal(t, "user", sent[0].(map[string]any)["role"])
	assert.Equal(t, map[string]any{
		"role":    "system",
		"content": "Updated system prompt section \"rules\":\n\nBe brief.",
	}, sent[1])
	assert.Equal(t, map[string]any{
		"role":    "system",
		"content": "Updated system prompt section \"rules\":\n\nBe thorough.",
	}, sent[2])
}

func TestBuildParams_SystemPrompt(t *testing.T) {
	system := ai.SystemPrompt([]ai.Content{
		ai.Text{Text: "Be brief."},
		ai.Section{Name: "rules", Text: "<rules>Cite.</rules>"},
	})
	body := wire(t, config{}, testModel, nil, system)

	assert.Equal(t, map[string]any{
		"role": "system",
		"content": []any{
			map[string]any{"type": "text", "text": "Be brief."},
			map[string]any{"type": "text", "text": "<rules>Cite.</rules>"},
		},
	}, body["messages"].([]any)[0], "one part for each Text and Section")
}

func TestBuildParams_SystemMessagePlacement(t *testing.T) {
	call := ai.ToolCall{ID: "c1", Name: "weather", Input: json.RawMessage(`{}`)}
	messages := []ai.Message{
		ai.UserMessage("Weather?"),
		ai.AssistantMessage(call),
		ai.SystemMessage(ai.Text{Text: "Answer in French."}),
		ai.ToolResultMessage("c1", ai.Text{Text: "sunny"}),
	}
	body := wire(t, config{}, testModel, messages, nil)

	var roles []string
	for _, m := range body["messages"].([]any) {
		roles = append(roles, m.(map[string]any)["role"].(string))
	}
	assert.Equal(t, []string{"user", "assistant", "tool", "system"}, roles,
		"a system message waits for the next assistant message, or the end")
}

func TestBuildParams_SystemReminders(t *testing.T) {
	call := ai.ToolCall{ID: "c1", Name: "weather", Input: json.RawMessage(`{}`)}
	rules := ai.SystemMessage(ai.Section{Name: "rules", Text: "Cite."})
	reminder := map[string]any{
		"type": "text",
		"text": "<system-reminder>\nUpdated system prompt section \"rules\":\n\nCite.\n</system-reminder>",
	}
	text := func(s string) map[string]any { return map[string]any{"type": "text", "text": s} }

	tests := []struct {
		name     string
		messages []ai.Message
		want     []any
	}{
		{
			name:     "at the start of the next user message",
			messages: []ai.Message{ai.UserMessage("Hi"), rules, ai.UserMessage("Go")},
			want: []any{
				map[string]any{"role": "user", "content": "Hi"},
				map[string]any{"role": "user", "content": []any{reminder, text("Go")}},
			},
		},
		{
			name:     "at the end of the last user message",
			messages: []ai.Message{ai.UserMessage("Hi"), rules},
			want: []any{
				map[string]any{"role": "user", "content": []any{text("Hi"), reminder}},
			},
		},
		{
			name: "in a user message after the tool results",
			messages: []ai.Message{
				ai.UserMessage("Hi"),
				ai.AssistantMessage(call),
				rules,
				ai.ToolResultMessage("c1", ai.Text{Text: "sunny"}),
			},
			want: []any{
				map[string]any{"role": "user", "content": "Hi"},
				map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{
					"id":       "c1",
					"type":     "function",
					"function": map[string]any{"name": "weather", "arguments": "{}"},
				}}},
				map[string]any{"role": "tool", "tool_call_id": "c1", "content": "sunny"},
				map[string]any{"role": "user", "content": "<system-reminder>\nUpdated system prompt section \"rules\":\n\nCite.\n</system-reminder>"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := wire(t, config{}, plainModel, tt.messages, nil)
			assert.Equal(t, tt.want, body["messages"])
		})
	}
}

func TestBuildParams_UserFiles(t *testing.T) {
	msg := ai.Message{
		Role: ai.RoleUser,
		Content: []ai.Content{
			ai.Text{Text: "Compare"},
			ai.File{MediaType: "image/png", Data: []byte("png")},
			ai.File{MediaType: "application/pdf", Data: []byte("pdf")},
		},
	}
	body := wire(t, config{}, testModel, []ai.Message{msg}, nil)

	assert.Equal(t, []any{
		map[string]any{"type": "text", "text": "Compare"},
		map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,cG5n"}},
		map[string]any{"type": "file", "file": map[string]any{
			"filename":  "file.pdf",
			"file_data": "data:application/pdf;base64,cGRm",
		}},
	}, body["messages"].([]any)[0].(map[string]any)["content"])
}

func TestBuildParams_OrphanToolCalls(t *testing.T) {
	call := ai.ToolCall{ID: "c1", Name: "calc", Input: json.RawMessage(`{}`)}
	messages := []ai.Message{
		ai.UserMessage("Calc"),
		ai.AssistantMessage(call),
		ai.UserMessage("Never mind"),
	}
	body := wire(t, config{}, testModel, messages, nil)

	sent := body["messages"].([]any)
	require.Len(t, sent, 4)
	assert.Equal(t, map[string]any{
		"role":         "tool",
		"tool_call_id": "c1",
		"content":      "No result provided",
	}, sent[2])
}

func TestBuildParams_Temperature(t *testing.T) {
	body := wire(t, config{}, testModel, nil, ai.Temperature(0.2))
	assert.Equal(t, 0.2, body["temperature"])
}

func TestBuildParams_ToolChoice(t *testing.T) {
	tests := []struct {
		choice ai.ToolChoiceMode
		want   any
	}{
		{choice: ai.ToolChoiceAuto, want: "auto"},
		{choice: ai.ToolChoiceAny, want: "required"},
		{choice: ai.ToolChoiceNone, want: "none"},
		{
			choice: ai.ToolChoiceTool("read"),
			want:   map[string]any{"type": "function", "function": map[string]any{"name": "read"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.choice.Type, func(t *testing.T) {
			body := wire(t, config{}, testModel, nil, ai.ToolChoice(tt.choice))
			assert.Equal(t, tt.want, body["tool_choice"])
		})
	}

	_, err := buildParams(config{}, testModel, nil, ai.ToolChoice(ai.ToolChoiceMode{Type: "some"}))
	require.Error(t, err)
}

func TestBuildParams_CacheRetention(t *testing.T) {
	body := wire(t, config{}, testModel, nil, ai.CacheRetention(ai.CacheLong))
	assert.Equal(t, "24h", body["prompt_cache_retention"])

	cfg := config{cacheBreakpoints: true}
	opts := ai.JoinOptions(
		ai.SystemPrompt([]ai.Content{ai.Text{Text: "Be brief."}}),
		openai.PromptCacheKey("conv-1"),
		ai.CacheRetention(ai.CacheNone),
	)
	body = wire(t, cfg, testModel, nil, opts)
	_, ok := body["prompt_cache_key"]
	assert.False(t, ok, "no key without a cache")
	assert.Equal(t, map[string]any{"role": "system", "content": "Be brief."}, body["messages"].([]any)[0],
		"no breakpoint without a cache")
}

func TestFeaturesOf(t *testing.T) {
	assert.Equal(t, openAIModel, featuresOf("gpt-6-luna"))
	assert.Equal(t, openAIModel, featuresOf("openai/gpt-6-luna"))
	assert.Equal(t, features{}, featuresOf("deepseek/deepseek-v4-flash"))
}

// toolNames returns the names of the tools of a request body.
func toolNames(body map[string]any) []string {
	var names []string
	for _, tool := range body["tools"].([]any) {
		function := tool.(map[string]any)["function"].(map[string]any)
		names = append(names, function["name"].(string))
	}
	return names
}

// allowed is the tool_choice that limits the model to the named tools.
func allowed(mode string, names ...string) map[string]any {
	tools := make([]any, 0, len(names))
	for _, name := range names {
		tools = append(tools, map[string]any{
			"type":     "function",
			"function": map[string]any{"name": name},
		})
	}
	return map[string]any{
		"type": "allowed_tools",
		"allowed_tools": map[string]any{
			"mode":  mode,
			"tools": tools,
		},
	}
}

func TestBuildParams_ToolChanges(t *testing.T) {
	bash := ai.ToolInfo{Name: "bash"}
	read := ai.ToolInfo{Name: "read"}
	weather := ai.ToolInfo{Name: "weather"}
	change := ai.SystemMessage(ai.ToolRemoved{Name: "bash"}, ai.ToolAdded{Tool: weather})
	replace := ai.SystemMessage(ai.ToolAdded{Tool: ai.ToolInfo{Name: "bash", Description: "Run"}})

	tests := []struct {
		name       string
		model      ai.Model
		change     ai.Message
		wantTools  []string
		wantChoice any
	}{
		{
			name:       "a removal keeps the declared tools and limits the model",
			model:      testModel,
			change:     change,
			wantTools:  []string{"bash", "read", "weather"},
			wantChoice: allowed("auto", "read", "weather"),
		},
		{
			name:      "a replacement sends the current tools",
			model:     testModel,
			change:    replace,
			wantTools: []string{"bash", "read"},
		},
		{
			name:      "a server without allowed_tools gets the current tools",
			model:     plainModel,
			change:    change,
			wantTools: []string{"read", "weather"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			messages := []ai.Message{
				ai.UserMessage("Hi"),
				tt.change,
			}
			body := wire(t, config{}, tt.model, messages, ai.Tools([]ai.ToolInfo{bash, read}))

			assert.Equal(t, tt.wantTools, toolNames(body), "the API has no tool change in place")
			assert.Equal(t, tt.wantChoice, body["tool_choice"])
			assert.Len(t, body["messages"], 1, "the change sends no message")
		})
	}
}

func TestBuildParams_ToolRemovalsWithToolChoice(t *testing.T) {
	bash := ai.ToolInfo{Name: "bash"}
	read := ai.ToolInfo{Name: "read"}
	messages := []ai.Message{
		ai.UserMessage("Hi"),
		ai.SystemMessage(ai.ToolRemoved{Name: "bash"}),
	}

	tests := []struct {
		choice ai.ToolChoiceMode
		want   any
	}{
		{choice: ai.ToolChoiceAuto, want: allowed("auto", "read")},
		{choice: ai.ToolChoiceAny, want: allowed("required", "read")},
		{choice: ai.ToolChoiceNone, want: "none"},
		{
			choice: ai.ToolChoiceTool("read"),
			want:   map[string]any{"type": "function", "function": map[string]any{"name": "read"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.choice.Type, func(t *testing.T) {
			opts := ai.JoinOptions(
				ai.Tools([]ai.ToolInfo{bash, read}),
				ai.ToolChoice(tt.choice),
			)
			body := wire(t, config{}, testModel, messages, opts)
			assert.Equal(t, []string{"bash", "read"}, toolNames(body))
			assert.Equal(t, tt.want, body["tool_choice"])
		})
	}
}

func TestBuildParams_CacheBreakpoints(t *testing.T) {
	cfg := config{cacheBreakpoints: true}
	body := wire(t, cfg, testModel, []ai.Message{ai.UserMessage("Hi")}, ai.SystemPrompt([]ai.Content{ai.Text{Text: "Be brief."}}))

	messages := body["messages"].([]any)
	assert.Equal(t, map[string]any{
		"role": "system",
		"content": []any{map[string]any{
			"type":                    "text",
			"text":                    "Be brief.",
			"prompt_cache_breakpoint": map[string]any{"mode": "explicit"},
		}},
	}, messages[0])
}

func TestBuildParams_PromptCacheKey(t *testing.T) {
	body := wire(t, config{}, testModel, nil, openai.PromptCacheKey("conv-1"))
	assert.Equal(t, "conv-1", body["prompt_cache_key"])
}

func TestBuildParams_Tools(t *testing.T) {
	schema, err := jsonschema.For[weatherInput](nil)
	require.NoError(t, err)

	tools := []ai.ToolInfo{{
		Name:        "get_weather",
		Description: "Get the weather",
		InputSchema: schema,
	}}
	body := wire(t, config{}, testModel, nil, ai.Tools(tools))

	list := body["tools"].([]any)
	require.Len(t, list, 1)
	tool := list[0].(map[string]any)
	assert.Equal(t, "function", tool["type"])

	fn := tool["function"].(map[string]any)
	assert.Equal(t, "get_weather", fn["name"])
	assert.Equal(t, "Get the weather", fn["description"])
	_, strict := fn["strict"]
	assert.False(t, strict)
	assert.Contains(t, fn["parameters"].(map[string]any)["properties"], "city")
}

func TestBuildObjectParams(t *testing.T) {
	schema, err := jsonschema.For[weatherInput](nil)
	require.NoError(t, err)

	params, err := buildObjectParams(config{}, testModel, nil, schema, nil)
	require.NoError(t, err)
	body := toBody(t, params)

	format := body["response_format"].(map[string]any)
	assert.Equal(t, "json_schema", format["type"])

	jsonSchema := format["json_schema"].(map[string]any)
	assert.Equal(t, true, jsonSchema["strict"])
	assert.NotEmpty(t, jsonSchema["name"])
	assert.Contains(t, jsonSchema["schema"].(map[string]any)["properties"], "city")
}
