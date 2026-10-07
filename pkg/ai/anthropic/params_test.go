package anthropic

import (
	"encoding/json"
	"testing"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
)

// testModel takes system messages and tool changes, and does not
// manage its effort. No real model has these features, so init adds it
// to the map.
var testModel = ai.Model{ID: "claude-test", Name: "Claude Test", MaxTokens: 128000}

// plainModel takes no system messages, like Sonnet 5 and any unknown ID.
var plainModel = ai.Model{ID: "claude-plain", MaxTokens: 128000}

func init() {
	modelFeatures[testModel.ID] = features{systemMessages: true, toolChanges: true}
}

// ephemeral is the cache marker of the short cache.
var ephemeral = map[string]any{"type": "ephemeral"}

type weatherInput struct {
	City string `json:"city"`
}

// wire builds the request for the messages and options, and decodes the
// JSON body into a map, so a test checks what the API would receive.
func wire(t *testing.T, model ai.Model, messages []ai.Message, opts ai.Options) map[string]any {
	t.Helper()

	req, err := buildRequest(model, messages, opts)
	require.NoError(t, err)

	data, err := json.Marshal(req.params)
	require.NoError(t, err)

	var body map[string]any
	require.NoError(t, json.Unmarshal(data, &body))
	return body
}

func TestBuildParams_MaxTokens(t *testing.T) {
	tests := []struct {
		name  string
		model ai.Model
		opts  ai.Options
		want  float64
	}{
		{name: "option", model: testModel, opts: ai.MaxTokens(100), want: 100},
		{name: "model", model: testModel, want: 128000},
		{name: "default", model: ai.Model{ID: "x"}, want: defaultMaxTokens},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := wire(t, tt.model, nil, tt.opts)
			assert.Equal(t, tt.want, body["max_tokens"])
		})
	}
}

func TestBuildParams_Thinking(t *testing.T) {
	tests := []struct {
		name         string
		opts         ai.Options
		wantThinking any
		wantEffort   any
	}{
		{name: "unset", opts: nil, wantThinking: nil, wantEffort: nil},
		{
			name:         "off",
			opts:         ai.Thinking(ai.ThinkingOff),
			wantThinking: map[string]any{"type": "disabled"},
		},
		{
			name:         "high",
			opts:         ai.Thinking(ai.ThinkingHigh),
			wantThinking: map[string]any{"type": "adaptive", "display": "summarized"},
			wantEffort:   "high",
		},
		{
			name:         "max",
			opts:         ai.Thinking(ai.ThinkingMax),
			wantThinking: map[string]any{"type": "adaptive", "display": "summarized"},
			wantEffort:   "max",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := wire(t, testModel, nil, tt.opts)
			assert.Equal(t, tt.wantThinking, body["thinking"])

			var effort any
			if cfg, ok := body["output_config"].(map[string]any); ok {
				effort = cfg["effort"]
			}
			assert.Equal(t, tt.wantEffort, effort)
		})
	}
}

func TestBuildParams_UnknownThinkingLevel(t *testing.T) {
	_, err := buildRequest(testModel, nil, ai.Thinking("deep"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "deep")
}

func TestBuildParams_Caching(t *testing.T) {
	weather := ai.ToolInfo{Name: "weather", InputSchema: &jsonschema.Schema{Type: "object"}}
	bash := ai.ToolInfo{Name: "bash", InputSchema: &jsonschema.Schema{Type: "object"}}
	messages := []ai.Message{
		ai.UserMessage("Hi"),
		ai.AssistantMessage(ai.Text{Text: "Hello"}),
		ai.UserMessage("Weather?"),
	}

	tests := []struct {
		name   string
		opts   ai.Options
		marker any
	}{
		{name: "short by default", opts: nil, marker: ephemeral},
		{
			name:   "long",
			opts:   ai.CacheRetention(ai.CacheLong),
			marker: map[string]any{"type": "ephemeral", "ttl": "1h"},
		},
		{name: "none", opts: ai.CacheRetention(ai.CacheNone), marker: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := ai.JoinOptions(
				ai.SystemPrompt([]ai.Content{ai.Text{Text: "Be brief."}}),
				ai.Tools([]ai.ToolInfo{weather, bash}),
				tt.opts,
			)
			body := wire(t, plainModel, messages, opts)

			_, ok := body["cache_control"]
			assert.False(t, ok, "no top-level marker")

			system := body["system"].([]any)
			assert.Equal(t, tt.marker, system[0].(map[string]any)["cache_control"], "the system prompt")

			tools := body["tools"].([]any)
			assert.Nil(t, tools[0].(map[string]any)["cache_control"])
			assert.Equal(t, tt.marker, tools[1].(map[string]any)["cache_control"], "the last tool")

			sent := body["messages"].([]any)
			first := sent[0].(map[string]any)["content"].([]any)[0].(map[string]any)
			assert.Nil(t, first["cache_control"])
			last := sent[2].(map[string]any)["content"].([]any)[0].(map[string]any)
			assert.Equal(t, tt.marker, last["cache_control"], "the last user block")
		})
	}
}

func TestBuildParams_CachingLastSystemMessage(t *testing.T) {
	messages := []ai.Message{
		ai.UserMessage("Hi"),
		ai.SystemMessage(ai.Text{Text: "Answer in French."}),
	}
	body := wire(t, testModel, messages, nil)

	sent := body["messages"].([]any)
	require.Len(t, sent, 2)
	block := sent[1].(map[string]any)["content"].([]any)[0].(map[string]any)
	assert.Equal(t, ephemeral, block["cache_control"], "the last block of a system message")
}

func TestBuildParams_SystemBlocks(t *testing.T) {
	system := ai.SystemPrompt([]ai.Content{
		ai.Text{Text: "Be brief."},
		ai.Section{Name: "rules", Text: "<rules>Cite.</rules>"},
		ai.ToolRemoved{Name: "bash"},
	})
	body := wire(t, testModel, nil, system)

	assert.Equal(t, []any{
		map[string]any{"type": "text", "text": "Be brief."},
		map[string]any{
			"type":          "text",
			"text":          "<rules>Cite.</rules>",
			"cache_control": ephemeral,
		},
	}, body["system"], "one block for each Text and Section, the marker on the last")
}

func TestBuildParams_SystemMessageBlocks(t *testing.T) {
	messages := []ai.Message{
		ai.UserMessage("Hi"),
		ai.SystemMessage(
			ai.Text{Text: "Be careful."},
			ai.Section{Name: "rules", Text: "<rules>Be thorough.</rules>"},
			ai.SectionRemoved{Name: "todos"},
		),
		ai.UserMessage("Go"),
	}
	body := wire(t, testModel, messages, ai.CacheRetention(ai.CacheNone))

	sent := body["messages"].([]any)
	require.Len(t, sent, 3)
	assert.Equal(t, map[string]any{
		"role": "system",
		"content": []any{map[string]any{
			"type": "text",
			"text": "Be careful.\n\n" +
				"Updated system prompt section \"rules\":\n\n<rules>Be thorough.</rules>\n\n" +
				"Removed system prompt section \"todos\".",
		}},
	}, sent[2], "the provider frames each section by its name")
}

func TestBuildParams_EmptySystemMessage(t *testing.T) {
	messages := []ai.Message{
		ai.UserMessage("Hi"),
		ai.SystemMessage(ai.Text{Text: ""}),
	}
	body := wire(t, testModel, messages, nil)

	assert.Len(t, body["messages"], 1, "a system message with no text is not sent")
}
func TestBuildParams_NoSystem(t *testing.T) {
	body := wire(t, testModel, nil, nil)
	_, ok := body["system"]
	assert.False(t, ok)
}

func TestBuildParams_SystemMessages(t *testing.T) {
	messages := []ai.Message{
		ai.UserMessage("Hi"),
		ai.SystemMessage(ai.Text{Text: "Answer in French."}),
		ai.UserMessage("Weather?"),
	}
	body := wire(t, testModel, messages, ai.SystemPrompt([]ai.Content{ai.Text{Text: "Be brief."}}))

	system := body["system"].([]any)
	assert.Equal(t, "Be brief.", system[0].(map[string]any)["text"])

	sent := body["messages"].([]any)
	require.Len(t, sent, 3)
	assert.Equal(t, "user", sent[1].(map[string]any)["role"])
	assert.Equal(t, map[string]any{
		"role": "system",
		"content": []any{map[string]any{
			"type":          "text",
			"text":          "Answer in French.",
			"cache_control": ephemeral,
		}},
	}, sent[2], "a system message waits for the end")
}

func TestBuildParams_SystemMessagePlacement(t *testing.T) {
	call := ai.ToolCall{ID: "c1", Name: "weather", Input: json.RawMessage(`{}`)}
	french := ai.SystemMessage(ai.Text{Text: "Answer in French."})

	tests := []struct {
		name     string
		messages []ai.Message
		want     []string
	}{
		{
			name: "before an assistant message",
			messages: []ai.Message{
				ai.UserMessage("Hi"),
				french,
				ai.AssistantMessage(ai.Text{Text: "Bonjour"}),
				ai.UserMessage("Weather?"),
			},
			want: []string{"user", "system", "assistant", "user"},
		},
		{
			name: "between a tool call and its result",
			messages: []ai.Message{
				ai.UserMessage("Weather?"),
				ai.AssistantMessage(call),
				french,
				ai.ToolResultMessage("c1", ai.Text{Text: "sunny"}),
				ai.AssistantMessage(ai.Text{Text: "Il fait beau"}),
			},
			want: []string{"user", "assistant", "user", "system", "assistant"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := wire(t, testModel, tt.messages, nil)

			var roles []string
			for _, m := range body["messages"].([]any) {
				roles = append(roles, m.(map[string]any)["role"].(string))
			}
			assert.Equal(t, tt.want, roles)
		})
	}
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
				map[string]any{"role": "user", "content": []any{text("Hi")}},
				map[string]any{"role": "user", "content": []any{reminder, text("Go")}},
			},
		},
		{
			name: "after the tool results",
			messages: []ai.Message{
				ai.UserMessage("Hi"),
				ai.AssistantMessage(call),
				rules,
				ai.ToolResultMessage("c1", ai.Text{Text: "sunny"}),
			},
			want: []any{
				map[string]any{"role": "user", "content": []any{text("Hi")}},
				map[string]any{"role": "assistant", "content": []any{map[string]any{
					"type":  "tool_use",
					"id":    "c1",
					"name":  "weather",
					"input": map[string]any{},
				}}},
				map[string]any{"role": "user", "content": []any{
					map[string]any{
						"type":        "tool_result",
						"tool_use_id": "c1",
						"content":     []any{text("sunny")},
					},
					reminder,
				}},
			},
		},
		{
			name: "at the end of the last user message",
			messages: []ai.Message{
				ai.UserMessage("Hi"),
				rules,
			},
			want: []any{
				map[string]any{"role": "user", "content": []any{text("Hi"), reminder}},
			},
		},
		{
			name: "in a new user message after an assistant message",
			messages: []ai.Message{
				ai.UserMessage("Hi"),
				ai.AssistantMessage(ai.Text{Text: "Hello"}),
				rules,
			},
			want: []any{
				map[string]any{"role": "user", "content": []any{text("Hi")}},
				map[string]any{"role": "assistant", "content": []any{text("Hello")}},
				map[string]any{"role": "user", "content": []any{reminder}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := ai.CacheRetention(ai.CacheNone)
			body := wire(t, plainModel, tt.messages, opts)
			assert.Equal(t, tt.want, body["messages"])
		})
	}
}

func TestBuildParams_ToolChangesWithoutSystemMessages(t *testing.T) {
	weather := ai.ToolInfo{Name: "weather", InputSchema: &jsonschema.Schema{Type: "object"}}
	bash := ai.ToolInfo{Name: "bash", InputSchema: &jsonschema.Schema{Type: "object"}}
	messages := []ai.Message{
		ai.UserMessage("Hi"),
		ai.SystemMessage(ai.ToolRemoved{Name: "bash"}, ai.ToolAdded{Tool: weather}),
		ai.UserMessage("Weather?"),
	}
	req, err := buildRequest(plainModel, messages, ai.Tools([]ai.ToolInfo{bash}))
	require.NoError(t, err)
	body := wire(t, plainModel, messages, ai.Tools([]ai.ToolInfo{bash}))

	tools := body["tools"].([]any)
	require.Len(t, tools, 1, "the current tools")
	assert.Equal(t, "weather", tools[0].(map[string]any)["name"])
	assert.Len(t, body["messages"], 2, "a tool change sends no text")
	assert.NotContains(t, req.betas, inlineToolsBeta)
}

func TestBuildParams_Sections(t *testing.T) {
	rules := ai.Section{Name: "rules", Text: "<rules>Be brief.</rules>"}
	thorough := ai.Section{Name: "rules", Text: "<rules>Be thorough.</rules>"}
	messages := []ai.Message{
		ai.SystemMessage(rules),
		ai.UserMessage("Hi"),
		ai.SystemMessage(thorough),
	}
	body := wire(t, testModel, messages, nil)

	sent := body["messages"].([]any)
	require.Len(t, sent, 3)
	assert.Equal(t, "user", sent[0].(map[string]any)["role"])
	first := sent[1].(map[string]any)["content"].([]any)[0].(map[string]any)
	assert.Equal(t, "Updated system prompt section \"rules\":\n\n<rules>Be brief.</rules>", first["text"])
	second := sent[2].(map[string]any)["content"].([]any)[0].(map[string]any)
	assert.Equal(t, "Updated system prompt section \"rules\":\n\n<rules>Be thorough.</rules>", second["text"])
}

func TestBuildParams_SectionsWithToolChanges(t *testing.T) {
	bash := ai.ToolInfo{Name: "bash", InputSchema: &jsonschema.Schema{Type: "object"}}
	msg := ai.SystemMessage(ai.Section{Name: "rules", Text: "Be brief."})
	msg.Content = append(msg.Content, ai.ToolRemoved{Name: "bash"})
	body := wire(t, testModel, []ai.Message{msg}, ai.Tools([]ai.ToolInfo{bash}))

	sent := body["messages"].([]any)
	require.Len(t, sent, 1)
	blocks := sent[0].(map[string]any)["content"].([]any)
	require.Len(t, blocks, 2)
	assert.Equal(t, map[string]any{
		"type": "text",
		"text": "Updated system prompt section \"rules\":\n\nBe brief.",
	}, blocks[0])
	assert.Equal(t, "tool_removal", blocks[1].(map[string]any)["type"])
}

func TestBuildParams_ToolChanges(t *testing.T) {
	weather := ai.ToolInfo{
		Name:        "weather",
		Description: "Get the weather",
		InputSchema: &jsonschema.Schema{Type: "object"},
	}
	bash := ai.ToolInfo{Name: "bash", InputSchema: &jsonschema.Schema{Type: "object"}}
	messages := []ai.Message{
		ai.UserMessage("Hi"),
		ai.SystemMessage(ai.ToolRemoved{Name: "bash"}, ai.ToolAdded{Tool: weather}),
		ai.UserMessage("Weather?"),
	}
	body := wire(t, testModel, messages, ai.Tools([]ai.ToolInfo{bash}))

	tools := body["tools"].([]any)
	require.Len(t, tools, 2, "the first tools stay, with the placeholder")
	assert.Equal(t, "bash", tools[0].(map[string]any)["name"])
	assert.Equal(t, map[string]any{
		"name":          placeholderName,
		"description":   "Reserved placeholder. Never available. Never call this.",
		"input_schema":  map[string]any{"type": "object", "properties": map[string]any{}},
		"defer_loading": true,
	}, tools[1])

	sent := body["messages"].([]any)
	require.Len(t, sent, 3)
	change := sent[2].(map[string]any)
	assert.Equal(t, "system", change["role"])

	blocks := change["content"].([]any)
	require.Len(t, blocks, 2)
	assert.Equal(t, map[string]any{
		"type": "tool_removal",
		"tool": map[string]any{"type": "tool_reference", "name": "bash"},
	}, blocks[0])

	addition := blocks[1].(map[string]any)
	assert.Equal(t, "tool_addition", addition["type"])
	tool := addition["tool"].(map[string]any)
	assert.Equal(t, "tool_definition", tool["type"])
	definition := tool["definition"].(map[string]any)
	assert.Equal(t, "weather", definition["name"])
	assert.Equal(t, "Get the weather", definition["description"])
	assert.Equal(t, true, definition["eager_input_streaming"])
}

func TestBuildParams_ToolRedefinition(t *testing.T) {
	read := ai.ToolInfo{Name: "read", InputSchema: &jsonschema.Schema{Type: "object"}}
	change := ai.SystemMessage(ai.ToolRemoved{Name: "read"}, ai.ToolAdded{Tool: read})
	body := wire(t, testModel, []ai.Message{change}, ai.Tools([]ai.ToolInfo{read}))

	sent := body["messages"].([]any)
	require.Len(t, sent, 1)
	blocks := sent[0].(map[string]any)["content"].([]any)
	require.Len(t, blocks, 1, "an addition replaces a tool with the same name")
	assert.Equal(t, "tool_addition", blocks[0].(map[string]any)["type"])
}

func TestBuildRequest_InlineToolsBeta(t *testing.T) {
	bash := ai.ToolInfo{Name: "bash", InputSchema: &jsonschema.Schema{Type: "object"}}
	messages := []ai.Message{ai.UserMessage("Hi")}

	req, err := buildRequest(testModel, messages, ai.Tools([]ai.ToolInfo{bash}))
	require.NoError(t, err)
	assert.Contains(t, req.betas, inlineToolsBeta, "from the first request, so the prefix stays")

	req, err = buildRequest(testModel, messages, nil)
	require.NoError(t, err)
	assert.Empty(t, req.betas, "the API rejects a list with only deferred tools")
}

func TestBuildParams_ToolChangesWithNoFirstTools(t *testing.T) {
	read := ai.ToolInfo{Name: "read", InputSchema: &jsonschema.Schema{Type: "object"}}
	messages := []ai.Message{
		ai.UserMessage("Hi"),
		ai.SystemMessage(ai.ToolAdded{Tool: read}),
	}
	body := wire(t, testModel, messages, nil)

	tools := body["tools"].([]any)
	require.Len(t, tools, 1, "the current tools, with no placeholder")
	assert.Equal(t, "read", tools[0].(map[string]any)["name"])
	assert.Len(t, body["messages"], 1)
}

func TestBuildParams_Tools(t *testing.T) {
	schema, err := jsonschema.For[weatherInput](nil)
	require.NoError(t, err)

	tools := []ai.ToolInfo{{
		Name:        "weather",
		Description: "Get the weather",
		InputSchema: schema,
	}}
	body := wire(t, plainModel, nil, ai.Tools(tools))

	sent := body["tools"].([]any)
	require.Len(t, sent, 1)
	tool := sent[0].(map[string]any)
	assert.Equal(t, "weather", tool["name"])
	assert.Equal(t, "Get the weather", tool["description"])
	assert.Equal(t, true, tool["eager_input_streaming"])

	input := tool["input_schema"].(map[string]any)
	assert.Equal(t, "object", input["type"])
	assert.Contains(t, input["properties"], "city")
	assert.Equal(t, []any{"city"}, input["required"])
	assert.Equal(t, false, input["additionalProperties"])
}

func TestBuildParams_Messages(t *testing.T) {
	messages := []ai.Message{
		ai.UserMessage("What is the weather in Paris?"),
		ai.AssistantMessage(
			ai.ThinkingBlock{Thinking: "look it up", Signature: "sig"},
			ai.ThinkingBlock{Thinking: "unsigned"},
			ai.Text{Text: ""},
			ai.ToolCall{ID: "c1", Name: "weather", Input: json.RawMessage(`{"city":"Paris"}`)},
			ai.ToolCall{ID: "c2", Name: "weather", Input: json.RawMessage(`{"city":"Lyon"}`)},
		),
		{Role: ai.RoleToolResult, ToolCallID: "c1", Content: []ai.Content{ai.Text{Text: "sunny"}}},
		{
			Role:       ai.RoleToolResult,
			ToolCallID: "c2",
			Content:    []ai.Content{ai.Text{Text: "down"}},
			IsError:    true,
		},
	}
	body := wire(t, testModel, messages, nil)

	sent := body["messages"].([]any)
	require.Len(t, sent, 3, "consecutive tool results group into one user message")

	assistant := sent[1].(map[string]any)
	assert.Equal(t, "assistant", assistant["role"])
	blocks := assistant["content"].([]any)
	require.Len(t, blocks, 3, "unsigned thinking and empty text are dropped")
	assert.Equal(t, map[string]any{
		"type":      "thinking",
		"thinking":  "look it up",
		"signature": "sig",
	}, blocks[0])
	assert.Equal(t, map[string]any{
		"type":  "tool_use",
		"id":    "c1",
		"name":  "weather",
		"input": map[string]any{"city": "Paris"},
	}, blocks[1])

	results := sent[2].(map[string]any)
	assert.Equal(t, "user", results["role"])
	resultBlocks := results["content"].([]any)
	require.Len(t, resultBlocks, 2)
	assert.Equal(t, map[string]any{
		"type":        "tool_result",
		"tool_use_id": "c1",
		"content":     []any{map[string]any{"type": "text", "text": "sunny"}},
	}, resultBlocks[0])
	assert.Equal(t, true, resultBlocks[1].(map[string]any)["is_error"])
	assert.Equal(t, ephemeral, resultBlocks[1].(map[string]any)["cache_control"])
}

func TestBuildParams_ToolResultFiles(t *testing.T) {
	messages := []ai.Message{
		ai.UserMessage("Look"),
		ai.AssistantMessage(ai.ToolCall{ID: "c1", Name: "read_file", Input: json.RawMessage(`{}`)}),
		ai.ToolResultMessage(
			"c1",
			ai.Text{Text: "two files"},
			ai.File{MediaType: "image/png", Data: []byte("png")},
			ai.File{MediaType: "application/pdf", Data: []byte("pdf")},
		),
	}
	body := wire(t, testModel, messages, nil)

	results := body["messages"].([]any)[2].(map[string]any)
	block := results["content"].([]any)[0].(map[string]any)
	assert.Equal(t, ephemeral, block["cache_control"])
	assert.Equal(t, []any{
		map[string]any{"type": "text", "text": "two files"},
		map[string]any{
			"type": "image",
			"source": map[string]any{
				"type":       "base64",
				"media_type": "image/png",
				"data":       "cG5n",
			},
		},
		map[string]any{
			"type": "document",
			"source": map[string]any{
				"type":       "base64",
				"media_type": "application/pdf",
				"data":       "cGRm",
			},
		},
	}, block["content"])
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
	body := wire(t, testModel, []ai.Message{msg}, ai.CacheRetention(ai.CacheNone))

	user := body["messages"].([]any)[0].(map[string]any)
	assert.Equal(t, []any{
		map[string]any{"type": "text", "text": "Compare"},
		map[string]any{
			"type": "image",
			"source": map[string]any{
				"type":       "base64",
				"media_type": "image/png",
				"data":       "cG5n",
			},
		},
		map[string]any{
			"type": "document",
			"source": map[string]any{
				"type":       "base64",
				"media_type": "application/pdf",
				"data":       "cGRm",
			},
		},
	}, user["content"])
}

func TestBuildParams_Temperature(t *testing.T) {
	tests := []struct {
		name  string
		model ai.Model
		opts  ai.Options
		want  any
	}{
		{name: "sent", model: plainModel, opts: ai.Temperature(0.2), want: 0.2},
		{
			name:  "with thinking off",
			model: plainModel,
			opts:  ai.JoinOptions(ai.Temperature(0.2), ai.Thinking(ai.ThinkingOff)),
			want:  0.2,
		},
		{
			name:  "not with thinking on",
			model: plainModel,
			opts:  ai.JoinOptions(ai.Temperature(0.2), ai.Thinking(ai.ThinkingLow)),
		},
		{name: "not to an effort model", model: Opus5_5, opts: ai.Temperature(0.2)},
		{name: "not to Sonnet 5", model: Sonnet5, opts: ai.Temperature(0.2)},
		{name: "to Haiku 4.5", model: Haiku4_5, opts: ai.Temperature(0.2), want: 0.2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := wire(t, tt.model, nil, tt.opts)
			assert.Equal(t, tt.want, body["temperature"])
		})
	}
}

func TestBuildParams_ToolChoice(t *testing.T) {
	tests := []struct {
		choice ai.ToolChoiceMode
		want   map[string]any
	}{
		{choice: ai.ToolChoiceAuto, want: map[string]any{"type": "auto"}},
		{choice: ai.ToolChoiceAny, want: map[string]any{"type": "any"}},
		{choice: ai.ToolChoiceNone, want: map[string]any{"type": "none"}},
		{
			choice: ai.ToolChoiceTool("read"),
			want:   map[string]any{"type": "tool", "name": "read"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.choice.Type, func(t *testing.T) {
			body := wire(t, testModel, nil, ai.ToolChoice(tt.choice))
			assert.Equal(t, tt.want, body["tool_choice"])
		})
	}

	_, err := buildRequest(testModel, nil, ai.ToolChoice(ai.ToolChoiceMode{Type: "some"}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "some")
}

func TestBuildRequest_Effort(t *testing.T) {
	messages := []ai.Message{
		ai.UserMessage("Hi"),
		{
			Role:    ai.RoleAssistant,
			Content: []ai.Content{ai.Text{Text: "Hello"}},
			Effort:  "low",
		},
		{
			Role:    ai.RoleAssistant,
			Content: []ai.Content{ai.Text{Text: "Again"}},
			Effort:  "minimal",
		},
		ai.UserMessage("Think hard"),
	}
	effortMessage := func(effort string) map[string]any {
		return map[string]any{
			"role":          "system",
			"content":       []any{},
			"output_config": map[string]any{"effort": effort},
		}
	}

	req, err := buildRequest(Opus5_5, messages, ai.Thinking(ai.ThinkingXHigh))
	require.NoError(t, err)
	assert.Equal(t, "xhigh", req.effort)
	assert.Contains(t, req.betas, midConversationOutputConfigBeta)
	assert.Contains(t, req.betas, thinkingBindingControlsBeta)

	body := wire(t, Opus5_5, messages, ai.Thinking(ai.ThinkingXHigh))
	assert.Equal(t, map[string]any{
		"type":    "adaptive",
		"display": "summarized",
		"block_binding": map[string]any{
			"prefix_mismatch_behavior": "drop_block",
		},
	}, body["thinking"])
	assert.Equal(t, map[string]any{"effort": "high"}, body["output_config"], "the prefix never changes")

	sent := body["messages"].([]any)
	require.Len(t, sent, 6)
	assert.Equal(t, effortMessage("low"), sent[1], "the effort of the old turn")
	assert.Equal(t, "assistant", sent[2].(map[string]any)["role"])
	assert.Equal(t, "assistant", sent[3].(map[string]any)["role"], "an effort of another API is not sent")
	assert.Equal(t, effortMessage("xhigh"), sent[5], "the effort of this call")

	body = wire(t, Opus5_5, messages[:1], nil)
	sent = body["messages"].([]any)
	assert.Equal(t, effortMessage("high"), sent[1], "high by default")
}

func TestBuildRequest_EffortModelRejectsOff(t *testing.T) {
	_, err := buildRequest(Opus5_5, nil, ai.Thinking(ai.ThinkingOff))
	require.Error(t, err)
}

func TestBuildRequest_NoEffortOnOtherModels(t *testing.T) {
	req, err := buildRequest(testModel, nil, ai.Thinking(ai.ThinkingHigh))
	require.NoError(t, err)
	assert.Empty(t, req.effort)
	assert.Empty(t, req.betas)
}

func TestBuildObjectParams_Format(t *testing.T) {
	schema, err := jsonschema.For[weatherInput](nil)
	require.NoError(t, err)

	req, err := buildObjectRequest(testModel, nil, schema, ai.Thinking(ai.ThinkingLow))
	require.NoError(t, err)

	data, err := json.Marshal(req.params)
	require.NoError(t, err)
	var body map[string]any
	require.NoError(t, json.Unmarshal(data, &body))

	cfg := body["output_config"].(map[string]any)
	assert.Equal(t, "low", cfg["effort"])
	format := cfg["format"].(map[string]any)
	assert.Equal(t, "json_schema", format["type"])
	assert.Contains(t, format["schema"].(map[string]any)["properties"], "city")
}

func TestBuildCountTokensParams(t *testing.T) {
	schema, err := jsonschema.For[weatherInput](nil)
	require.NoError(t, err)

	messages := []ai.Message{ai.UserMessage("hi")}
	opts := ai.JoinOptions(
		ai.SystemPrompt([]ai.Content{ai.Text{Text: "Be brief."}}),
		ai.Tools([]ai.ToolInfo{{Name: "weather", InputSchema: schema}}),
		ai.Thinking(ai.ThinkingHigh),
	)
	params, betas, err := buildCountTokensParams(testModel, messages, opts)
	require.NoError(t, err)
	assert.Contains(t, betas, inlineToolsBeta)

	data, err := json.Marshal(params)
	require.NoError(t, err)
	var body map[string]any
	require.NoError(t, json.Unmarshal(data, &body))

	assert.Equal(t, "claude-test", body["model"])
	assert.Len(t, body["messages"], 1)
	assert.Len(t, body["system"], 1)
	assert.Len(t, body["tools"], 2, "the tool and the placeholder, as in a call")
	assert.Equal(t, map[string]any{"type": "adaptive", "display": "summarized"}, body["thinking"])
	assert.Equal(t, "high", body["output_config"].(map[string]any)["effort"])
	_, ok := body["max_tokens"]
	assert.False(t, ok)
}

func TestMapStopReason(t *testing.T) {
	tests := []struct {
		reason sdk.StopReason
		want   ai.StopReason
	}{
		{sdk.StopReasonEndTurn, ai.StopReasonStop},
		{sdk.StopReasonStopSequence, ai.StopReasonStop},
		{sdk.StopReasonPauseTurn, ai.StopReasonStop},
		{sdk.StopReasonMaxTokens, ai.StopReasonLength},
		{sdk.StopReasonModelContextWindowExceeded, ai.StopReasonLength},
		{sdk.StopReasonToolUse, ai.StopReasonToolUse},
		{sdk.StopReasonRefusal, ai.StopReasonRefusal},
		{"sensitive", ai.StopReasonRefusal},
	}

	for _, tt := range tests {
		t.Run(string(tt.reason), func(t *testing.T) {
			got, err := mapStopReason(tt.reason)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestMapStopReason_Errors(t *testing.T) {
	_, err := mapStopReason("")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no stop reason")

	_, err = mapStopReason("some_reason")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "some_reason")
}

func TestBuildMessage_Refusal(t *testing.T) {
	var acc sdk.Message
	require.NoError(t, json.Unmarshal([]byte(`{
		"content": [{"type": "text", "text": "I"}],
		"stop_reason": "refusal",
		"stop_details": {"type": "refusal", "category": "cyber", "explanation": "Harmful."},
		"usage": {"input_tokens": 1, "output_tokens": 1}
	}`), &acc))

	msg, err := buildMessage(testModel, acc, "")
	require.NoError(t, err)
	assert.Equal(t, ai.StopReasonRefusal, msg.StopReason)
	assert.Equal(t, "Harmful.", msg.StopDetail)

	acc.StopReason = "sensitive"
	msg, err = buildMessage(testModel, acc, "")
	require.NoError(t, err)
	assert.Equal(t, "sensitive", msg.StopDetail)
}

func TestBuildMessage_Effort(t *testing.T) {
	acc := sdk.Message{StopReason: sdk.StopReasonEndTurn}
	msg, err := buildMessage(Opus5_5, acc, "xhigh")
	require.NoError(t, err)
	assert.Equal(t, "xhigh", msg.Effort)
}

func TestMapUsage(t *testing.T) {
	usage := mapUsage(sdk.Usage{
		InputTokens:              10,
		OutputTokens:             20,
		CacheReadInputTokens:     30,
		CacheCreationInputTokens: 40,
		CacheCreation:            sdk.CacheCreation{Ephemeral1hInputTokens: 15},
	})
	assert.Equal(t, ai.Usage{
		Input:          10,
		Output:         20,
		CacheRead:      30,
		CacheWrite:     40,
		CacheWriteLong: 15,
	}, usage)
}

func TestFeaturesOf(t *testing.T) {
	tests := []struct {
		id   string
		want features
	}{
		{id: "claude-opus-5-5", want: claude5},
		{id: "claude-sonnet-5", want: features{noTemperature: true}},
		{id: "anthropic/claude-opus-5.5", want: features{systemMessages: true, effort: true, noTemperature: true}},
		{id: "anthropic/claude-sonnet-5", want: features{noTemperature: true}},
		{id: "vendor/model", want: features{}},
	}

	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			assert.Equal(t, tt.want, featuresOf(tt.id))
		})
	}
}

func TestBuildParams_OrphanToolCalls(t *testing.T) {
	c1 := ai.ToolCall{ID: "c1", Name: "calc", Input: json.RawMessage(`{}`)}
	c2 := ai.ToolCall{ID: "c2", Name: "calc", Input: json.RawMessage(`{}`)}
	noResult := map[string]any{
		"type":        "tool_result",
		"tool_use_id": "c2",
		"is_error":    true,
		"content":     []any{map[string]any{"type": "text", "text": "No result provided"}},
	}

	tests := []struct {
		name     string
		messages []ai.Message
		want     []any
	}{
		{
			name: "a user message interrupts the calls",
			messages: []ai.Message{
				ai.UserMessage("Calc"),
				ai.AssistantMessage(c1, c2),
				ai.ToolResultMessage("c1", ai.Text{Text: "4"}),
				ai.UserMessage("Never mind"),
			},
			want: []any{
				"user",
				"assistant",
				[]any{"c1", noResult},
				"user",
			},
		},
		{
			name: "the messages end with the calls",
			messages: []ai.Message{
				ai.UserMessage("Calc"),
				ai.AssistantMessage(c2),
			},
			want: []any{
				"user",
				"assistant",
				[]any{noResult},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := wire(t, testModel, tt.messages, ai.CacheRetention(ai.CacheNone))

			var got []any
			for _, m := range body["messages"].([]any) {
				msg := m.(map[string]any)
				blocks := msg["content"].([]any)
				first := blocks[0].(map[string]any)
				if first["type"] != "tool_result" {
					got = append(got, msg["role"])
					continue
				}
				var results []any
				for _, b := range blocks {
					block := b.(map[string]any)
					if block["tool_use_id"] == "c1" {
						results = append(results, "c1")
					} else {
						results = append(results, block)
					}
				}
				got = append(got, results)
			}
			assert.Equal(t, tt.want, got)
		})
	}
}
