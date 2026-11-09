package openaichat

import (
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
	sdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/shared"

	"github.com/sonnes/gopi/internal/transcript"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/ai/openai"
)

// objectFormatName is the name of the JSON schema format of an object
// call. The API requires a name and shows it to the model.
const objectFormatName = "output"

// buildParams builds the request of a call. It holds only the fields
// that OpenAI, OpenRouter, and Fireworks all accept, because Fireworks
// rejects a field that it does not know. The stream fields are added by
// the stream call.
func buildParams(
	cfg config,
	model ai.Model,
	messages []ai.Message,
	opts ai.Options,
) (sdk.ChatCompletionNewParams, error) {
	retention := ai.RetentionOf(opts)
	f := featuresOf(model.ID)
	params := sdk.ChatCompletionNewParams{
		Model:    shared.ChatModel(model.ID),
		Messages: buildMessages(cfg, f, messages, opts),
	}

	// Every server accepts max_completion_tokens. OpenAI rejects
	// max_tokens for a reasoning model.
	if n, ok := ai.MaxTokensOf(model, opts); ok {
		params.MaxCompletionTokens = param.NewOpt(int64(n))
	}

	// Off is the effort none. A model that cannot turn reasoning off
	// rejects it.
	level, ok, err := ai.ThinkingOf(opts)
	if err != nil {
		return params, err
	}
	if ok {
		params.ReasoningEffort = shared.ReasoningEffort(level.Effort())
	}

	tools := requestTools(messages, opts, f)
	params.Tools, err = convertTools(tools.list)
	if err != nil {
		return params, err
	}

	if key, ok := ai.GetOption(opts, openai.PromptCacheKey); ok && key != "" && retention != ai.CacheNone {
		params.PromptCacheKey = param.NewOpt(key)
	}
	// A caller that asks for the long cache sends the field. Fireworks
	// rejects it, so the request has no field by default.
	if retention == ai.CacheLong {
		params.PromptCacheRetention = sdk.ChatCompletionNewParamsPromptCacheRetention24h
	}

	if t, ok := ai.GetOption(opts, ai.Temperature); ok {
		params.Temperature = param.NewOpt(t)
	}

	c, set, err := ai.ToolChoiceOf(opts)
	if err != nil {
		return params, err
	}
	choice, send, limit := transcript.LimitChoice(c, set, tools.allowed)
	switch {
	case limit:
		params.ToolChoice = allowedTools(choice, tools.allowed)
	case send:
		params.ToolChoice = toolChoiceParam(choice)
	}

	return params, nil
}

// toolPlan is the tool list of a request, and the tools of the list
// that the model can call.
type toolPlan struct {
	// list is the tools of the request.
	list []ai.ToolInfo
	// allowed names the tools that the model can call, if a removal
	// made them fewer than list. Nil means no limit.
	allowed []string
}

// requestTools returns the tools of a request. The API has no way to
// change the tools in place, so the list holds the tools after the
// changes of the messages. A model that takes allowed_tools gets every
// tool that the messages declared, and a removal limits the model to
// the current tools. The list then stays the same after a removal, so
// the cache holds. A replacement changes a definition, so the list then
// holds the current tools.
func requestTools(messages []ai.Message, opts ai.Options, f features) toolPlan {
	tools, _ := ai.GetOption(opts, ai.Tools)
	changes := transcript.ChangesOf(tools, messages)
	if !f.allowedTools || changes.Replaced {
		return toolPlan{list: changes.Current}
	}
	return toolPlan{
		list:    changes.Declared,
		allowed: changes.Allowed(),
	}
}

// allowedTools limits the model to the allowed tools with an
// allowed_tools choice. Any is "required" in this API.
func allowedTools(c ai.ToolChoiceMode, allowed []string) sdk.ChatCompletionToolChoiceOptionUnionParam {
	mode := sdk.ChatCompletionAllowedToolsModeAuto
	if c.Type == ai.ToolChoiceAny.Type {
		mode = sdk.ChatCompletionAllowedToolsModeRequired
	}

	tools := make([]map[string]any, 0, len(allowed))
	for _, name := range allowed {
		tools = append(tools, map[string]any{
			"type":     "function",
			"function": map[string]any{"name": name},
		})
	}
	return sdk.ChatCompletionToolChoiceOptionUnionParam{
		OfAllowedTools: &sdk.ChatCompletionAllowedToolChoiceParam{
			AllowedTools: sdk.ChatCompletionAllowedToolsParam{
				Mode:  mode,
				Tools: tools,
			},
		},
	}
}

// toolChoiceParam maps a tool choice that [ai.ToolChoiceOf] returned to
// the API. Any is "required" in this API.
func toolChoiceParam(c ai.ToolChoiceMode) sdk.ChatCompletionToolChoiceOptionUnionParam {
	mode := func(m string) sdk.ChatCompletionToolChoiceOptionUnionParam {
		return sdk.ChatCompletionToolChoiceOptionUnionParam{OfAuto: param.NewOpt(m)}
	}

	switch c.Type {
	case ai.ToolChoiceAuto.Type:
		return mode(string(sdk.ChatCompletionToolChoiceOptionAutoAuto))
	case ai.ToolChoiceAny.Type:
		return mode(string(sdk.ChatCompletionToolChoiceOptionAutoRequired))
	case ai.ToolChoiceNone.Type:
		return mode(string(sdk.ChatCompletionToolChoiceOptionAutoNone))
	default:
		named := sdk.ChatCompletionNamedToolChoiceFunctionParam{Name: c.Name}
		return sdk.ToolChoiceOptionFunctionToolChoice(named)
	}
}

// buildObjectParams builds the request of an object call, with the
// schema as a strict response format.
func buildObjectParams(
	cfg config,
	model ai.Model,
	messages []ai.Message,
	schema *jsonschema.Schema,
	opts ai.Options,
) (sdk.ChatCompletionNewParams, error) {
	params, err := buildParams(cfg, model, messages, opts)
	if err != nil {
		return params, err
	}

	schemaMap, err := transcript.Schema(schema)
	if err != nil {
		return params, fmt.Errorf("openaichat: %w", err)
	}
	params.ResponseFormat = sdk.ChatCompletionNewParamsResponseFormatUnion{
		OfJSONSchema: &shared.ResponseFormatJSONSchemaParam{
			JSONSchema: shared.ResponseFormatJSONSchemaJSONSchemaParam{
				Name:   objectFormatName,
				Schema: schemaMap,
				Strict: param.NewOpt(true),
			},
		},
	}

	return params, nil
}

// buildMessages puts the system prompt in front of the messages. The
// role is system, because not every server knows the developer role,
// and OpenAI treats system as developer for a reasoning model.
func buildMessages(
	cfg config,
	f features,
	messages []ai.Message,
	opts ai.Options,
) []sdk.ChatCompletionMessageParamUnion {
	var out []sdk.ChatCompletionMessageParamUnion
	breakpoint := cfg.cacheBreakpoints && ai.RetentionOf(opts) != ai.CacheNone
	if m, ok := systemMessage(ai.SystemPromptOf(opts), breakpoint); ok {
		out = append(out, m)
	}
	return append(out, convertMessages(messages, convertConfig{systemMessages: f.systemMessages})...)
}

// systemMessage returns the system prompt as a system message. Each
// text is one text part, in order. If breakpoint is true, the last part
// carries a cache breakpoint. One part with no breakpoint goes as a
// string. The bool is false if the prompt has no text.
func systemMessage(system []string, breakpoint bool) (sdk.ChatCompletionMessageParamUnion, bool) {
	var parts []sdk.ChatCompletionContentPartTextParam
	for _, text := range system {
		parts = append(parts, sdk.ChatCompletionContentPartTextParam{Text: text})
	}

	switch {
	case len(parts) == 0:
		return sdk.ChatCompletionMessageParamUnion{}, false
	case len(parts) == 1 && !breakpoint:
		return sdk.SystemMessage(parts[0].Text), true
	}

	if breakpoint {
		parts[len(parts)-1].PromptCacheBreakpoint = sdk.NewChatCompletionContentPartTextPromptCacheBreakpointParam()
	}
	return sdk.SystemMessage(parts), true
}

// emptyParameters is the parameters of a tool with no schema. The API
// needs an object schema for a function tool.
var emptyParameters = map[string]any{"type": "object", "properties": map[string]any{}}

// convertTools maps each tool to a function tool. The request omits
// strict, because strict mode needs every property to be required.
func convertTools(tools []ai.ToolInfo) ([]sdk.ChatCompletionToolUnionParam, error) {
	var out []sdk.ChatCompletionToolUnionParam
	for _, t := range tools {
		parameters, err := transcript.Schema(t.InputSchema)
		if err != nil {
			return nil, fmt.Errorf("openaichat: tool %q: %w", t.Name, err)
		}
		if parameters == nil {
			parameters = emptyParameters
		}

		fn := shared.FunctionDefinitionParam{
			Name:       t.Name,
			Parameters: parameters,
		}
		if t.Description != "" {
			fn.Description = param.NewOpt(t.Description)
		}
		out = append(out, sdk.ChatCompletionFunctionTool(fn))
	}
	return out, nil
}
