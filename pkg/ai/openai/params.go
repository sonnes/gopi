package openai

import (
	"encoding/json"
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"

	"github.com/sonnes/gopi/internal/transcript"
	"github.com/sonnes/gopi/pkg/ai"
)

// objectFormatName is the name of the JSON schema format of an object
// call. The API requires a name and shows it to the model.
const objectFormatName = "output"

// buildParams builds the request of a stream call. The request is
// stateless: the API stores nothing, and it returns the reasoning of the
// model encrypted, so the next call can send it back.
func buildParams(model ai.Model, messages []ai.Message, opts ai.Options) (responses.ResponseNewParams, error) {
	f := featuresOf(model.ID)
	tools := requestTools(messages, opts, f)
	params := responses.ResponseNewParams{
		Model:   shared.ResponsesModel(model.ID),
		Store:   param.NewOpt(false),
		Include: []responses.ResponseIncludable{responses.ResponseIncludableReasoningEncryptedContent},
		Input: responses.ResponseNewParamsInputUnion{
			OfInputItemList: buildInput(messages, opts, f, tools.addTools),
		},
	}

	if n, ok := maxTokens(model, opts); ok {
		params.MaxOutputTokens = param.NewOpt(n)
	}

	reasoning, err := reasoningConfig(opts)
	if err != nil {
		return params, err
	}
	params.Reasoning = reasoning

	if len(tools.list) > 0 {
		params.Tools = convertTools(tools.list)
	}

	retention := cacheRetention(opts)
	if key, ok := ai.GetOption(opts, PromptCacheKey); ok && key != "" && retention != ai.CacheNone {
		params.PromptCacheKey = param.NewOpt(key)
	}
	if retention == ai.CacheLong {
		params.PromptCacheRetention = responses.ResponseNewParamsPromptCacheRetention24h
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

// requestToolChoice returns the tool choice of a request, and false if
// the request sends none. If the tool plan limits the tools, the choice
// limits the model to them with allowed_tools. The tool list then stays
// the same, so the cache holds. A choice that names one tool, or none,
// is narrower than the limit, so it goes as it is.
func requestToolChoice(opts ai.Options, plan toolPlan) (responses.ResponseNewParamsToolChoiceUnion, bool, error) {
	c, set := ai.GetOption(opts, ai.ToolChoice)
	if plan.allowed == nil {
		if !set {
			return responses.ResponseNewParamsToolChoiceUnion{}, false, nil
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

	mode := responses.ToolChoiceAllowedModeAuto
	if set {
		if _, err := toolChoiceParam(c); err != nil {
			return responses.ResponseNewParamsToolChoiceUnion{}, false, err
		}
		if c.Type == ai.ToolChoiceAny.Type {
			mode = responses.ToolChoiceAllowedModeRequired
		}
	}

	tools := make([]map[string]any, 0, len(plan.allowed))
	for _, name := range plan.allowed {
		tools = append(tools, map[string]any{
			"type": "function",
			"name": name,
		})
	}
	return responses.ResponseNewParamsToolChoiceUnion{
		OfAllowedTools: &responses.ToolChoiceAllowedParam{
			Mode:  mode,
			Tools: tools,
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
func toolChoiceParam(c ai.ToolChoiceMode) (responses.ResponseNewParamsToolChoiceUnion, error) {
	mode := func(m responses.ToolChoiceOptions) responses.ResponseNewParamsToolChoiceUnion {
		return responses.ResponseNewParamsToolChoiceUnion{OfToolChoiceMode: param.NewOpt(m)}
	}

	switch c.Type {
	case ai.ToolChoiceAuto.Type:
		return mode(responses.ToolChoiceOptionsAuto), nil
	case ai.ToolChoiceAny.Type:
		return mode(responses.ToolChoiceOptionsRequired), nil
	case ai.ToolChoiceNone.Type:
		return mode(responses.ToolChoiceOptionsNone), nil
	case "tool":
		return responses.ResponseNewParamsToolChoiceUnion{
			OfFunctionTool: &responses.ToolChoiceFunctionParam{Name: c.Name},
		}, nil
	default:
		return responses.ResponseNewParamsToolChoiceUnion{}, fmt.Errorf("openai: unknown tool choice %q", c.Type)
	}
}

// buildObjectParams builds the request of an object call. It is the
// request of a stream call, with the schema as a strict output format.
func buildObjectParams(
	model ai.Model,
	messages []ai.Message,
	schema *jsonschema.Schema,
	opts ai.Options,
) (responses.ResponseNewParams, error) {
	params, err := buildParams(model, messages, opts)
	if err != nil {
		return params, err
	}

	schemaMap, err := toMap(schema)
	if err != nil {
		return params, err
	}
	params.Text.Format = responses.ResponseFormatTextConfigUnionParam{
		OfJSONSchema: &responses.ResponseFormatTextJSONSchemaConfigParam{
			Name:   objectFormatName,
			Schema: schemaMap,
			Strict: param.NewOpt(true),
		},
	}

	return params, nil
}

// buildCountTokensParams builds the request of a token count. It carries
// the same input, tools, and reasoning config as a stream request, so
// the count is correct.
func buildCountTokensParams(
	model ai.Model,
	messages []ai.Message,
	opts ai.Options,
) (responses.InputTokenCountParams, error) {
	f := featuresOf(model.ID)
	tools := requestTools(messages, opts, f)
	params := responses.InputTokenCountParams{
		Model: param.NewOpt(model.ID),
		Input: responses.InputTokenCountParamsInputUnion{
			OfResponseInputItemArray: buildInput(messages, opts, f, tools.addTools),
		},
	}

	reasoning, err := reasoningConfig(opts)
	if err != nil {
		return params, err
	}
	params.Reasoning = reasoning

	if len(tools.list) > 0 {
		params.Tools = convertTools(tools.list)
	}

	return params, nil
}

// buildInput puts the system prompt in front of the messages. See
// [convertMessages] for addTools.
func buildInput(
	messages []ai.Message,
	opts ai.Options,
	f features,
	addTools bool,
) responses.ResponseInputParam {
	var input responses.ResponseInputParam
	system, _ := ai.GetOption(opts, ai.SystemPrompt)
	breakpoint := f.cacheBreakpoint && cacheRetention(opts) != ai.CacheNone
	if item, ok := systemItem(system, breakpoint); ok {
		input = append(input, item)
	}

	cfg := convertConfig{
		systemMessages: f.systemMessages,
		addTools:       addTools,
	}
	return append(input, convertMessages(messages, cfg)...)
}

// toolPlan is where the tools of a request go.
type toolPlan struct {
	// list is the tools of the request.
	list []ai.ToolInfo
	// addTools is true if the input adds the tools of each
	// [ai.ToolAdded] block in its place.
	addTools bool
	// allowed names the tools that the model can call, if a removal
	// made them fewer than the tools of the request and the input. Nil
	// means no limit.
	allowed []string
}

// requestTools returns where the tools of a request go. The API adds
// tools in place with an additional_tools item, and the tools of the
// request stay the same, so the cache holds. The API has no item that
// removes a tool, so after a removal the request limits the model to
// the current tools with allowed_tools. The OpenAI function calling
// guide gives allowed_tools for this, because the tool list stays the
// same. The API also has no item that replaces a tool. So after a
// replacement, or for a model without the item, the request sends the
// current tools from [transcript.CurrentTools], and the input adds no
// tools.
func requestTools(messages []ai.Message, opts ai.Options, f features) toolPlan {
	tools, _ := ai.GetOption(opts, ai.Tools)
	current := transcript.CurrentTools(tools, messages)
	if !f.additionalTools {
		return toolPlan{list: current}
	}

	declared := make(map[string]bool, len(tools))
	for _, t := range tools {
		declared[t.Name] = true
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
				if declared[v.Tool.Name] {
					replaced = true
				}
				declared[v.Tool.Name] = true
			}
		}
	}

	if replaced {
		return toolPlan{list: current}
	}
	plan := toolPlan{
		list:     tools,
		addTools: true,
	}
	if removed {
		plan.allowed = make([]string, 0, len(current))
		for _, t := range current {
			plan.allowed = append(plan.allowed, t.Name)
		}
	}
	return plan
}

// systemItem returns the [ai.SystemPrompt] option as a developer
// message. Each [ai.Text] and [ai.Section] block is one text part, in
// order. If breakpoint is true, the last part carries a cache breakpoint.
// The implicit breakpoint of the API stays on, so the API also caches
// the history. The prompt is not sent as instructions, because a
// breakpoint needs a content part. The bool is false if the prompt has
// no text.
func systemItem(system []ai.Content, breakpoint bool) (responses.ResponseInputItemUnionParam, bool) {
	var parts responses.ResponseInputMessageContentListParam
	for _, c := range system {
		switch v := c.(type) {
		case ai.Text:
			parts = append(parts, inputText(v.Text))
		case ai.Section:
			parts = append(parts, inputText(v.Text))
		}
	}
	if len(parts) == 0 {
		return responses.ResponseInputItemUnionParam{}, false
	}

	if breakpoint {
		last := parts[len(parts)-1].OfInputText
		last.PromptCacheBreakpoint = responses.NewResponseInputTextPromptCacheBreakpointParam()
	}
	return responses.ResponseInputItemParamOfMessage(parts, responses.EasyInputMessageRoleDeveloper), true
}

// maxTokens is the option, else the cap of the model. If neither is set,
// the request omits the cap and the API uses the cap of the model.
func maxTokens(model ai.Model, opts ai.Options) (int64, bool) {
	if n, ok := ai.GetOption(opts, ai.MaxTokens); ok {
		return int64(n), true
	}
	if model.MaxTokens > 0 {
		return int64(model.MaxTokens), true
	}
	return 0, false
}

// reasoningConfig maps the thinking option to the request. If the
// option is unset, the request asks only for a summary, and the model
// uses its default effort. Off sends the effort none. Every other level
// sends its effort and asks for a summary, because the API sends no
// reasoning text without one.
func reasoningConfig(opts ai.Options) (shared.ReasoningParam, error) {
	level, ok := ai.GetOption(opts, ai.Thinking)
	if !ok {
		return shared.ReasoningParam{Summary: shared.ReasoningSummaryAuto}, nil
	}

	if level == ai.ThinkingOff {
		return shared.ReasoningParam{Effort: shared.ReasoningEffortNone}, nil
	}

	effort, ok := efforts[level]
	if !ok {
		return shared.ReasoningParam{}, fmt.Errorf("openai: unknown thinking level %q", level)
	}

	return shared.ReasoningParam{
		Effort:  effort,
		Summary: shared.ReasoningSummaryAuto,
	}, nil
}

// efforts maps a thinking level to the effort of the API.
var efforts = map[ai.ThinkingLevel]shared.ReasoningEffort{
	ai.ThinkingLow:    shared.ReasoningEffortLow,
	ai.ThinkingMedium: shared.ReasoningEffortMedium,
	ai.ThinkingHigh:   shared.ReasoningEffortHigh,
	ai.ThinkingXHigh:  shared.ReasoningEffortXhigh,
	ai.ThinkingMax:    shared.ReasoningEffortMax,
}

// convertTools maps each tool to a function tool. Strict mode is off,
// because it needs every property to be required, and a tool input can
// have optional fields.
func convertTools(tools []ai.ToolInfo) []responses.ToolUnionParam {
	out := make([]responses.ToolUnionParam, 0, len(tools))
	for _, t := range tools {
		parameters, err := toMap(t.InputSchema)
		if err != nil || parameters == nil {
			parameters = map[string]any{"type": "object", "properties": map[string]any{}}
		}

		tool := &responses.FunctionToolParam{
			Name:       t.Name,
			Parameters: parameters,
			Strict:     param.NewOpt(false),
		}
		if t.Description != "" {
			tool.Description = param.NewOpt(t.Description)
		}
		out = append(out, responses.ToolUnionParam{OfFunction: tool})
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
		return nil, fmt.Errorf("openai: encode schema: %w", err)
	}

	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("openai: decode schema: %w", err)
	}
	return m, nil
}
