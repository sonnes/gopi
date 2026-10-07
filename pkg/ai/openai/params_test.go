package openai

import (
	"encoding/json"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
)

var testModel = ai.Model{ID: "gpt-6-luna", Name: "GPT-6 Luna", MaxTokens: 128000}

// plainModel takes no system messages, like an unknown model ID.
var plainModel = ai.Model{ID: "plain-model", MaxTokens: 128000}

// breakpoint is the cache breakpoint of a content part.
var breakpoint = map[string]any{"mode": "explicit"}

type weatherInput struct {
	City string `json:"city"`
}

// wire builds the request for the messages and options, and decodes the
// JSON body into a map, so a test checks what the API would receive.
func wire(t *testing.T, model ai.Model, messages []ai.Message, opts ai.Options) map[string]any {
	t.Helper()

	params, err := buildParams(model, messages, opts)
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

func TestBuildParams_Stateless(t *testing.T) {
	body := wire(t, testModel, nil, nil)

	assert.Equal(t, false, body["store"])
	assert.Equal(t, []any{"reasoning.encrypted_content"}, body["include"])
	assert.Equal(t, "gpt-6-luna", body["model"])
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
			body := wire(t, tt.model, nil, tt.opts)
			assert.Equal(t, tt.want, body["max_output_tokens"])
		})
	}
}

func TestBuildParams_Thinking(t *testing.T) {
	tests := []struct {
		name string
		opts ai.Options
		want any
	}{
		{name: "unset", want: map[string]any{"summary": "auto"}},
		{name: "off", opts: ai.Thinking(ai.ThinkingOff), want: map[string]any{"effort": "none"}},
		{
			name: "low",
			opts: ai.Thinking(ai.ThinkingLow),
			want: map[string]any{"effort": "low", "summary": "auto"},
		},
		{
			name: "xhigh",
			opts: ai.Thinking(ai.ThinkingXHigh),
			want: map[string]any{"effort": "xhigh", "summary": "auto"},
		},
		{
			name: "max",
			opts: ai.Thinking(ai.ThinkingMax),
			want: map[string]any{"effort": "max", "summary": "auto"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := wire(t, testModel, nil, tt.opts)
			assert.Equal(t, tt.want, body["reasoning"])
		})
	}
}

func TestBuildParams_UnknownThinkingLevel(t *testing.T) {
	_, err := buildParams(testModel, nil, ai.Thinking("deep"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "deep")
}

func TestBuildParams_System(t *testing.T) {
	system := ai.SystemPrompt([]ai.Content{
		ai.Text{Text: "Be brief."},
		ai.Section{Name: "rules", Text: "<rules>Cite.</rules>"},
		ai.ToolRemoved{Name: "bash"},
	})
	body := wire(t, testModel, []ai.Message{ai.UserMessage("Hi")}, system)

	_, ok := body["instructions"]
	assert.False(t, ok)

	input := body["input"].([]any)
	require.Len(t, input, 2)
	assert.Equal(t, map[string]any{
		"role": "developer",
		"content": []any{
			map[string]any{"type": "input_text", "text": "Be brief."},
			map[string]any{
				"type":                    "input_text",
				"text":                    "<rules>Cite.</rules>",
				"prompt_cache_breakpoint": breakpoint,
			},
		},
	}, input[0], "one part for each Text and Section, the breakpoint on the last")
	assert.Equal(t, "user", input[1].(map[string]any)["role"])
}

func TestBuildParams_CacheRetention(t *testing.T) {
	system := ai.SystemPrompt([]ai.Content{ai.Text{Text: "Be brief."}})

	body := wire(t, testModel, nil, ai.JoinOptions(system, PromptCacheKey("conv-1")))
	_, ok := body["prompt_cache_retention"]
	assert.False(t, ok, "short by default")

	body = wire(t, testModel, nil, ai.JoinOptions(system, ai.CacheRetention(ai.CacheLong)))
	assert.Equal(t, "24h", body["prompt_cache_retention"])

	body = wire(t, testModel, nil, ai.JoinOptions(system, PromptCacheKey("conv-1"), ai.CacheRetention(ai.CacheNone)))
	_, ok = body["prompt_cache_key"]
	assert.False(t, ok, "no key without a cache")
	part := body["input"].([]any)[0].(map[string]any)["content"].([]any)[0].(map[string]any)
	assert.Nil(t, part["prompt_cache_breakpoint"], "no breakpoint without a cache")
}

func TestBuildParams_NoBreakpointOnOtherModels(t *testing.T) {
	system := ai.SystemPrompt([]ai.Content{ai.Text{Text: "Be brief."}})
	body := wire(t, plainModel, nil, system)

	part := body["input"].([]any)[0].(map[string]any)["content"].([]any)[0].(map[string]any)
	assert.Nil(t, part["prompt_cache_breakpoint"], "an older model rejects the breakpoint")
}

func TestBuildParams_Temperature(t *testing.T) {
	body := wire(t, testModel, nil, ai.Temperature(0.2))
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
		{choice: ai.ToolChoiceTool("read"), want: map[string]any{"type": "function", "name": "read"}},
	}

	for _, tt := range tests {
		t.Run(tt.choice.Type, func(t *testing.T) {
			body := wire(t, testModel, nil, ai.ToolChoice(tt.choice))
			assert.Equal(t, tt.want, body["tool_choice"])
		})
	}

	_, err := buildParams(testModel, nil, ai.ToolChoice(ai.ToolChoiceMode{Type: "some"}))
	require.Error(t, err)
}

func TestBuildParams_SystemMessagePlacement(t *testing.T) {
	call := ai.ToolCall{ID: "c1", Name: "weather", Input: json.RawMessage(`{}`)}
	french := ai.SystemMessage(ai.Text{Text: "Answer in French."})
	messages := []ai.Message{
		ai.UserMessage("Weather?"),
		ai.AssistantMessage(call),
		french,
		ai.ToolResultMessage("c1", ai.Text{Text: "sunny"}),
		ai.UserMessage("Thanks"),
	}
	body := wire(t, testModel, messages, nil)

	var kinds []string
	for _, item := range body["input"].([]any) {
		m := item.(map[string]any)
		if role, ok := m["role"].(string); ok {
			kinds = append(kinds, role)
		} else {
			kinds = append(kinds, m["type"].(string))
		}
	}
	assert.Equal(t, []string{"user", "function_call", "function_call_output", "user", "developer"}, kinds,
		"a system message waits for the next assistant message, or the end")
}

func TestBuildParams_SystemReminders(t *testing.T) {
	call := ai.ToolCall{ID: "c1", Name: "weather", Input: json.RawMessage(`{}`)}
	rules := ai.SystemMessage(ai.Section{Name: "rules", Text: "Cite."})
	reminder := map[string]any{
		"type": "input_text",
		"text": "<system-reminder>\nUpdated system prompt section \"rules\":\n\nCite.\n</system-reminder>",
	}
	text := func(s string) map[string]any { return map[string]any{"type": "input_text", "text": s} }
	user := func(parts ...any) map[string]any { return map[string]any{"role": "user", "content": parts} }

	tests := []struct {
		name     string
		messages []ai.Message
		want     []any
	}{
		{
			name:     "at the start of the next user message",
			messages: []ai.Message{ai.UserMessage("Hi"), rules, ai.UserMessage("Go")},
			want:     []any{user(text("Hi")), user(reminder, text("Go"))},
		},
		{
			name:     "at the end of the last user message",
			messages: []ai.Message{ai.UserMessage("Hi"), rules},
			want:     []any{user(text("Hi"), reminder)},
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
				user(text("Hi")),
				map[string]any{"type": "function_call", "call_id": "c1", "name": "weather", "arguments": "{}"},
				map[string]any{"type": "function_call_output", "call_id": "c1", "output": "sunny"},
				user(reminder),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := wire(t, plainModel, tt.messages, nil)
			assert.Equal(t, tt.want, body["input"])
		})
	}
}

func TestBuildParams_ToolChangesWithoutSystemMessages(t *testing.T) {
	bash := ai.ToolInfo{Name: "bash"}
	weather := ai.ToolInfo{Name: "weather"}
	messages := []ai.Message{
		ai.UserMessage("Hi"),
		ai.SystemMessage(ai.ToolAdded{Tool: weather}),
	}
	body := wire(t, plainModel, messages, ai.Tools([]ai.ToolInfo{bash}))

	var names []string
	for _, tool := range body["tools"].([]any) {
		names = append(names, tool.(map[string]any)["name"].(string))
	}
	assert.Equal(t, []string{"bash", "weather"}, names, "the current tools")
	assert.Len(t, body["input"], 1, "no additional_tools item")
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
	body := wire(t, testModel, []ai.Message{msg}, nil)

	assert.Equal(t, []any{
		map[string]any{"type": "input_text", "text": "Compare"},
		map[string]any{"type": "input_image", "detail": "auto", "image_url": "data:image/png;base64,cG5n"},
		map[string]any{"type": "input_file", "filename": "file.pdf", "file_data": "data:application/pdf;base64,cGRm"},
	}, body["input"].([]any)[0].(map[string]any)["content"])
}

func TestBuildParams_OrphanToolCalls(t *testing.T) {
	call := ai.ToolCall{ID: "c1", Name: "calc", Input: json.RawMessage(`{}`)}
	messages := []ai.Message{
		ai.UserMessage("Calc"),
		ai.AssistantMessage(call),
		ai.UserMessage("Never mind"),
	}
	body := wire(t, testModel, messages, nil)

	input := body["input"].([]any)
	require.Len(t, input, 4)
	assert.Equal(t, map[string]any{
		"type":    "function_call_output",
		"call_id": "c1",
		"output":  "No result provided",
	}, input[2])
}

func TestFeaturesOf(t *testing.T) {
	assert.Equal(t, gpt, featuresOf("gpt-6-luna"))
	assert.Equal(t, gpt, featuresOf("openai/gpt-6-luna"))
	assert.Equal(t, features{}, featuresOf("gpt-4o"))
}

func TestBuildParams_SystemMessages(t *testing.T) {
	messages := []ai.Message{
		ai.UserMessage("Hi"),
		ai.SystemMessage(ai.Text{Text: "Answer in French."}),
	}
	body := wire(t, testModel, messages, nil)

	input := body["input"].([]any)
	require.Len(t, input, 2)
	assert.Equal(t, map[string]any{
		"role":    "developer",
		"content": []any{map[string]any{"type": "input_text", "text": "Answer in French."}},
	}, input[1])
}

func TestBuildParams_Sections(t *testing.T) {
	messages := []ai.Message{
		ai.SystemMessage(ai.Section{Name: "rules", Text: "Be brief."}),
		ai.UserMessage("Hi"),
		ai.SystemMessage(ai.SectionRemoved{Name: "rules"}),
	}
	body := wire(t, testModel, messages, nil)

	input := body["input"].([]any)
	require.Len(t, input, 3)
	assert.Equal(t, "user", input[0].(map[string]any)["role"])
	assert.Equal(t, map[string]any{
		"role": "developer",
		"content": []any{map[string]any{
			"type": "input_text",
			"text": "Updated system prompt section \"rules\":\n\nBe brief.",
		}},
	}, input[1])
	assert.Equal(t, map[string]any{
		"role":    "developer",
		"content": []any{map[string]any{"type": "input_text", "text": "Removed system prompt section \"rules\"."}},
	}, input[2])
}

func TestBuildParams_ToolAdditions(t *testing.T) {
	bash := ai.ToolInfo{Name: "bash"}
	weather := ai.ToolInfo{Name: "weather"}
	messages := []ai.Message{
		ai.UserMessage("Hi"),
		ai.SystemMessage(ai.ToolAdded{Tool: weather}),
		ai.UserMessage("Weather?"),
	}
	body := wire(t, testModel, messages, ai.Tools([]ai.ToolInfo{bash}))

	tools := body["tools"].([]any)
	require.Len(t, tools, 1, "the tools of the first call stay the same")
	assert.Equal(t, "bash", tools[0].(map[string]any)["name"])

	input := body["input"].([]any)
	require.Len(t, input, 3)
	item := input[2].(map[string]any)
	assert.Equal(t, "additional_tools", item["type"])
	assert.Equal(t, "developer", item["role"])
	added := item["tools"].([]any)
	require.Len(t, added, 1)
	assert.Equal(t, "weather", added[0].(map[string]any)["name"])
}

// toolNames returns the names of the tools of a request body.
func toolNames(body map[string]any) []string {
	var names []string
	for _, tool := range body["tools"].([]any) {
		names = append(names, tool.(map[string]any)["name"].(string))
	}
	return names
}

// allowed is the tool_choice that limits the model to the named tools.
func allowed(mode string, names ...string) map[string]any {
	tools := make([]any, 0, len(names))
	for _, name := range names {
		tools = append(tools, map[string]any{"type": "function", "name": name})
	}
	return map[string]any{
		"type":  "allowed_tools",
		"mode":  mode,
		"tools": tools,
	}
}

func TestBuildParams_ToolRemovals(t *testing.T) {
	bash := ai.ToolInfo{Name: "bash"}
	read := ai.ToolInfo{Name: "read"}
	weather := ai.ToolInfo{Name: "weather"}
	messages := []ai.Message{
		ai.UserMessage("Hi"),
		ai.SystemMessage(ai.ToolRemoved{Name: "bash"}, ai.ToolAdded{Tool: weather}),
	}
	body := wire(t, testModel, messages, ai.Tools([]ai.ToolInfo{bash, read}))

	assert.Equal(t, []string{"bash", "read"}, toolNames(body), "the tools of the first call stay the same")
	input := body["input"].([]any)
	require.Len(t, input, 2)
	assert.Equal(t, "additional_tools", input[1].(map[string]any)["type"], "the addition goes in place")
	assert.Equal(t, allowed("auto", "read", "weather"), body["tool_choice"],
		"the tool choice limits the model to the current tools")
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
		{choice: ai.ToolChoiceTool("read"), want: map[string]any{"type": "function", "name": "read"}},
	}

	for _, tt := range tests {
		t.Run(tt.choice.Type, func(t *testing.T) {
			opts := ai.JoinOptions(
				ai.Tools([]ai.ToolInfo{bash, read}),
				ai.ToolChoice(tt.choice),
			)
			body := wire(t, testModel, messages, opts)
			assert.Equal(t, []string{"bash", "read"}, toolNames(body))
			assert.Equal(t, tt.want, body["tool_choice"])
		})
	}
}

func TestBuildParams_ToolReplacement(t *testing.T) {
	bash := ai.ToolInfo{Name: "bash"}
	read := ai.ToolInfo{Name: "read"}
	messages := []ai.Message{
		ai.UserMessage("Hi"),
		ai.SystemMessage(ai.ToolAdded{Tool: ai.ToolInfo{Name: "bash", Description: "Run"}}),
	}
	body := wire(t, testModel, messages, ai.Tools([]ai.ToolInfo{bash, read}))

	assert.Equal(t, []string{"bash", "read"}, toolNames(body))
	assert.Equal(t, "Run", body["tools"].([]any)[0].(map[string]any)["description"],
		"the API has no item that replaces a tool, so the list holds the current tools")
	assert.Len(t, body["input"], 1, "the change sends no item")
	assert.Nil(t, body["tool_choice"])
}

func TestBuildParams_NoSystem(t *testing.T) {
	body := wire(t, testModel, []ai.Message{ai.UserMessage("Hi")}, nil)

	input := body["input"].([]any)
	require.Len(t, input, 1)
	assert.Equal(t, "user", input[0].(map[string]any)["role"])
}

func TestBuildParams_PromptCacheKey(t *testing.T) {
	body := wire(t, testModel, nil, PromptCacheKey("conv-1"))
	assert.Equal(t, "conv-1", body["prompt_cache_key"])

	body = wire(t, testModel, nil, nil)
	_, ok := body["prompt_cache_key"]
	assert.False(t, ok)
}

func TestBuildParams_Tools(t *testing.T) {
	schema, err := jsonschema.For[weatherInput](nil)
	require.NoError(t, err)

	tools := []ai.ToolInfo{{
		Name:        "get_weather",
		Description: "Get the weather",
		InputSchema: schema,
	}}
	body := wire(t, testModel, nil, ai.Tools(tools))

	list := body["tools"].([]any)
	require.Len(t, list, 1)
	tool := list[0].(map[string]any)
	assert.Equal(t, "function", tool["type"])
	assert.Equal(t, "get_weather", tool["name"])
	assert.Equal(t, "Get the weather", tool["description"])
	assert.Equal(t, false, tool["strict"])

	parameters := tool["parameters"].(map[string]any)
	assert.Equal(t, "object", parameters["type"])
	assert.Contains(t, parameters["properties"], "city")
}

func TestBuildObjectParams(t *testing.T) {
	schema, err := jsonschema.For[weatherInput](nil)
	require.NoError(t, err)

	params, err := buildObjectParams(testModel, nil, schema, nil)
	require.NoError(t, err)
	body := toBody(t, params)

	format := body["text"].(map[string]any)["format"].(map[string]any)
	assert.Equal(t, "json_schema", format["type"])
	assert.Equal(t, true, format["strict"])
	assert.NotEmpty(t, format["name"])
	assert.Contains(t, format["schema"].(map[string]any)["properties"], "city")
}

func TestBuildCountTokensParams(t *testing.T) {
	params, err := buildCountTokensParams(
		testModel,
		[]ai.Message{ai.UserMessage("Hi")},
		ai.JoinOptions(
			ai.SystemPrompt([]ai.Content{ai.Text{Text: "Be brief."}}),
			ai.Thinking(ai.ThinkingHigh),
		),
	)
	require.NoError(t, err)
	body := toBody(t, params)

	assert.Equal(t, "gpt-6-luna", body["model"])
	assert.Len(t, body["input"], 2)
	assert.Equal(t, map[string]any{"effort": "high", "summary": "auto"}, body["reasoning"])
}
