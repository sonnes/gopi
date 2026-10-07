package openaichat

import (
	"encoding/json"
	"fmt"
	"slices"

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
	retention := cacheRetention(opts)
	f := featuresOf(model.ID)
	params := sdk.ChatCompletionNewParams{
		Model:    shared.ChatModel(model.ID),
		Messages: buildMessages(cfg, f, messages, opts),
	}

	// Every server accepts max_completion_tokens. OpenAI rejects
	// max_tokens for a reasoning model.
	if n, ok := maxTokens(model, opts); ok {
		params.MaxCompletionTokens = param.NewOpt(n)
	}

	if level, ok := ai.GetOption(opts, ai.Thinking); ok {
		effort, ok := efforts[level]
		if !ok {
			return params, fmt.Errorf("openaichat: unknown thinking level %q", level)
		}
		params.ReasoningEffort = effort
	}

	tools := requestTools(messages, opts, f)
	if len(tools.list) > 0 {
		params.Tools = convertTools(tools.list)
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

	choice, ok, err := requestToolChoice(opts, tools)
	if err != nil {
		return params, err
	}
	if ok {
		params.ToolChoice = choice
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
// changes of the messages, from [transcript.CurrentTools]. A model that
// takes allowed_tools gets every tool that the messages declared, and
// a removal limits the model to the current tools. The list then stays
// the same after a removal, so the cache holds. A replacement changes a
// definition, so the list then holds the current tools.
func requestTools(messages []ai.Message, opts ai.Options, f features) toolPlan {
	tools, _ := ai.GetOption(opts, ai.Tools)
	current := transcript.CurrentTools(tools, messages)
	if !f.allowedTools {
		return toolPlan{list: current}
	}

	declared := slices.Clone(tools)
	seen := make(map[string]bool, len(tools))
	for _, t := range tools {
		seen[t.Name] = true
	}
	removed := false
	replaced := false
	for _, m := range messages {
		if m.Role != ai.RoleSystem {
			continue
		}
		for _, c := range m.Content {
			switch v := c.(type) {
			case ai.ToolRemoved:
				removed = true
			case ai.ToolAdded:
				if seen[v.Tool.Name] {
					replaced = true
					continue
				}
				seen[v.Tool.Name] = true
				declared = append(declared, v.Tool)
			}
		}
	}

	if replaced {
		return toolPlan{list: current}
	}
	plan := toolPlan{list: declared}
	if removed {
		plan.allowed = make([]string, 0, len(current))
		for _, t := range current {
			plan.allowed = append(plan.allowed, t.Name)
		}
	}
	return plan
}

// requestToolChoice returns the tool choice of a request, and false if
// the request sends none. If the tool plan limits the tools, the choice
// limits the model to them with allowed_tools. A choice that names one
// tool, or none, is narrower than the limit, so it goes as it is.
func requestToolChoice(opts ai.Options, plan toolPlan) (sdk.ChatCompletionToolChoiceOptionUnionParam, bool, error) {
	c, set := ai.GetOption(opts, ai.ToolChoice)
	if plan.allowed == nil {
		if !set {
			return sdk.ChatCompletionToolChoiceOptionUnionParam{}, false, nil
		}
		choice, err := toolChoiceParam(c)
		return choice, true, err
	}

	narrower := set && (c.Type == ai.ToolChoiceNone.Type || c.Type == "tool")
	if narrower {
		choice, err := toolChoiceParam(c)
		return choice, true, err
	}
	if len(plan.allowed) == 0 {
		choice, err := toolChoiceParam(ai.ToolChoiceNone)
		return choice, true, err
	}

	mode := sdk.ChatCompletionAllowedToolsModeAuto
	if set {
		if _, err := toolChoiceParam(c); err != nil {
			return sdk.ChatCompletionToolChoiceOptionUnionParam{}, false, err
		}
		if c.Type == ai.ToolChoiceAny.Type {
			mode = sdk.ChatCompletionAllowedToolsModeRequired
		}
	}

	tools := make([]map[string]any, 0, len(plan.allowed))
	for _, name := range plan.allowed {
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
	}, true, nil
}

// cacheRetention is the [ai.CacheRetention] option, or [ai.CacheShort].
func cacheRetention(opts ai.Options) ai.Retention {
	if r, ok := ai.GetOption(opts, ai.CacheRetention); ok {
		return r
	}
	return ai.CacheShort
}

// toolChoiceParam maps a tool choice to the API. Any is "required" in
// this API. An unknown type is an error.
func toolChoiceParam(c ai.ToolChoiceMode) (sdk.ChatCompletionToolChoiceOptionUnionParam, error) {
	mode := func(m string) sdk.ChatCompletionToolChoiceOptionUnionParam {
		return sdk.ChatCompletionToolChoiceOptionUnionParam{OfAuto: param.NewOpt(m)}
	}

	switch c.Type {
	case ai.ToolChoiceAuto.Type:
		return mode(string(sdk.ChatCompletionToolChoiceOptionAutoAuto)), nil
	case ai.ToolChoiceAny.Type:
		return mode(string(sdk.ChatCompletionToolChoiceOptionAutoRequired)), nil
	case ai.ToolChoiceNone.Type:
		return mode(string(sdk.ChatCompletionToolChoiceOptionAutoNone)), nil
	case "tool":
		named := sdk.ChatCompletionNamedToolChoiceFunctionParam{Name: c.Name}
		return sdk.ToolChoiceOptionFunctionToolChoice(named), nil
	default:
		return sdk.ChatCompletionToolChoiceOptionUnionParam{}, fmt.Errorf("openaichat: unknown tool choice %q", c.Type)
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

	schemaMap, err := toMap(schema)
	if err != nil {
		return params, err
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
	system, _ := ai.GetOption(opts, ai.SystemPrompt)
	breakpoint := cfg.cacheBreakpoints && cacheRetention(opts) != ai.CacheNone
	if m, ok := systemMessage(system, breakpoint); ok {
		out = append(out, m)
	}
	return append(out, convertMessages(messages, convertConfig{systemMessages: f.systemMessages})...)
}

// systemMessage returns the [ai.SystemPrompt] option as a system
// message. Each [ai.Text] and [ai.Section] block is one text part, in
// order. If breakpoint is true, the last part carries a cache
// breakpoint. One part with no breakpoint goes as a string. The bool is
// false if the prompt has no text.
func systemMessage(system []ai.Content, breakpoint bool) (sdk.ChatCompletionMessageParamUnion, bool) {
	var parts []sdk.ChatCompletionContentPartTextParam
	for _, c := range system {
		switch v := c.(type) {
		case ai.Text:
			parts = append(parts, sdk.ChatCompletionContentPartTextParam{Text: v.Text})
		case ai.Section:
			parts = append(parts, sdk.ChatCompletionContentPartTextParam{Text: v.Text})
		}
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

// maxTokens is the option, else the cap of the model. If neither is set,
// the request omits the cap and the server uses its default. The
// default of Fireworks is 2048 tokens, so a model for Fireworks sets
// [ai.Model.MaxTokens].
func maxTokens(model ai.Model, opts ai.Options) (int64, bool) {
	if n, ok := ai.GetOption(opts, ai.MaxTokens); ok {
		return int64(n), true
	}
	if model.MaxTokens > 0 {
		return int64(model.MaxTokens), true
	}
	return 0, false
}

// efforts maps a thinking level to the reasoning effort of the API. Off
// is the effort none. A model that cannot turn reasoning off rejects it.
var efforts = map[ai.ThinkingLevel]shared.ReasoningEffort{
	ai.ThinkingOff:    shared.ReasoningEffortNone,
	ai.ThinkingLow:    shared.ReasoningEffortLow,
	ai.ThinkingMedium: shared.ReasoningEffortMedium,
	ai.ThinkingHigh:   shared.ReasoningEffortHigh,
	ai.ThinkingXHigh:  shared.ReasoningEffortXhigh,
	ai.ThinkingMax:    shared.ReasoningEffortMax,
}

// convertTools maps each tool to a function tool. The request omits
// strict, because strict mode needs every property to be required.
func convertTools(tools []ai.ToolInfo) []sdk.ChatCompletionToolUnionParam {
	out := make([]sdk.ChatCompletionToolUnionParam, 0, len(tools))
	for _, t := range tools {
		parameters, err := toMap(t.InputSchema)
		if err != nil || parameters == nil {
			parameters = map[string]any{"type": "object", "properties": map[string]any{}}
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
	return out
}

// toMap converts a schema to the map shape that the API takes.
func toMap(schema *jsonschema.Schema) (map[string]any, error) {
	if schema == nil {
		return nil, nil
	}

	data, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("openaichat: encode schema: %w", err)
	}

	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("openaichat: decode schema: %w", err)
	}
	return m, nil
}
