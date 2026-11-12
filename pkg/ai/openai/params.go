package openai

import (
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
	input, err := buildInput(messages, opts, f, tools.addTools)
	if err != nil {
		return responses.ResponseNewParams{}, err
	}
	params := responses.ResponseNewParams{
		Model:   shared.ResponsesModel(model.ID),
		Store:   param.NewOpt(false),
		Include: []responses.ResponseIncludable{responses.ResponseIncludableReasoningEncryptedContent},
		Input: responses.ResponseNewParamsInputUnion{
			OfInputItemList: input,
		},
	}

	if n, ok := ai.MaxTokensOf(model, opts); ok {
		params.MaxOutputTokens = param.NewOpt(int64(n))
	}

	params.Reasoning, err = reasoningConfig(opts)
	if err != nil {
		return params, err
	}

	params.Tools, err = convertTools(tools.list)
	if err != nil {
		return params, err
	}

	retention := ai.RetentionOf(opts)
	if key, ok := ai.GetOption(opts, PromptCacheKey); ok && key != "" && retention != ai.CacheNone {
		params.PromptCacheKey = param.NewOpt(key)
	}
	if retention == ai.CacheLong {
		params.PromptCacheRetention = responses.ResponseNewParamsPromptCacheRetention24h
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

// allowedTools limits the model to the allowed tools with an
// allowed_tools choice. The tool list then stays the same, so the cache
// holds. Any is "required" in this API.
func allowedTools(c ai.ToolChoiceMode, allowed []string) responses.ResponseNewParamsToolChoiceUnion {
	mode := responses.ToolChoiceAllowedModeAuto
	if c.Type == ai.ToolChoiceAny.Type {
		mode = responses.ToolChoiceAllowedModeRequired
	}

	tools := make([]map[string]any, 0, len(allowed))
	for _, name := range allowed {
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
	}
}

// toolChoiceParam maps a tool choice that [ai.ToolChoiceOf] returned to
// the API. Any is "required" in this API.
func toolChoiceParam(c ai.ToolChoiceMode) responses.ResponseNewParamsToolChoiceUnion {
	mode := func(m responses.ToolChoiceOptions) responses.ResponseNewParamsToolChoiceUnion {
		return responses.ResponseNewParamsToolChoiceUnion{OfToolChoiceMode: param.NewOpt(m)}
	}

	switch c.Type {
	case ai.ToolChoiceAuto.Type:
		return mode(responses.ToolChoiceOptionsAuto)
	case ai.ToolChoiceAny.Type:
		return mode(responses.ToolChoiceOptionsRequired)
	case ai.ToolChoiceNone.Type:
		return mode(responses.ToolChoiceOptionsNone)
	default:
		return responses.ResponseNewParamsToolChoiceUnion{
			OfFunctionTool: &responses.ToolChoiceFunctionParam{Name: c.Name},
		}
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

	schemaMap, err := transcript.Schema(schema)
	if err != nil {
		return params, fmt.Errorf("openai: %w", err)
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
	input, err := buildInput(messages, opts, f, tools.addTools)
	if err != nil {
		return responses.InputTokenCountParams{}, err
	}
	params := responses.InputTokenCountParams{
		Model: param.NewOpt(model.ID),
		Input: responses.InputTokenCountParamsInputUnion{
			OfResponseInputItemArray: input,
		},
	}

	params.Reasoning, err = reasoningConfig(opts)
	if err != nil {
		return params, err
	}

	params.Tools, err = convertTools(tools.list)
	if err != nil {
		return params, err
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
) (responses.ResponseInputParam, error) {
	var input responses.ResponseInputParam
	breakpoint := f.cacheBreakpoint && ai.RetentionOf(opts) != ai.CacheNone
	if item, ok := systemItem(ai.SystemPromptOf(opts), breakpoint); ok {
		input = append(input, item)
	}

	cfg := convertConfig{
		systemMessages: f.systemMessages,
		addTools:       addTools,
	}
	converted, err := convertMessages(messages, cfg)
	if err != nil {
		return nil, err
	}
	return append(input, converted...), nil
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
// current tools, and the input adds no tools.
func requestTools(messages []ai.Message, opts ai.Options, f features) toolPlan {
	tools, _ := ai.GetOption(opts, ai.Tools)
	changes := transcript.ChangesOf(tools, messages)
	if !f.additionalTools || changes.Replaced {
		return toolPlan{list: changes.Current}
	}
	return toolPlan{
		list:     tools,
		addTools: true,
		allowed:  changes.Allowed(),
	}
}

// systemItem returns the system prompt as a developer message. Each
// text is one text part, in order. If breakpoint is true, the last part
// carries a cache breakpoint. The implicit breakpoint of the API stays
// on, so the API also caches the history. The prompt is not sent as
// instructions, because a breakpoint needs a content part. The bool is
// false if the prompt has no text.
func systemItem(system []string, breakpoint bool) (responses.ResponseInputItemUnionParam, bool) {
	var parts responses.ResponseInputMessageContentListParam
	for _, text := range system {
		parts = append(parts, inputText(text))
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

// reasoningConfig maps the thinking option to the request. If the
// option is unset, the request asks only for a summary, and the model
// uses its default effort. Off sends the effort none. Every other level
// sends its effort and asks for a summary, because the API sends no
// reasoning text without one.
func reasoningConfig(opts ai.Options) (shared.ReasoningParam, error) {
	level, ok, err := ai.ThinkingOf(opts)
	switch {
	case err != nil:
		return shared.ReasoningParam{}, err
	case !ok:
		return shared.ReasoningParam{Summary: shared.ReasoningSummaryAuto}, nil
	case level == ai.ThinkingOff:
		return shared.ReasoningParam{Effort: shared.ReasoningEffortNone}, nil
	default:
		return shared.ReasoningParam{
			Effort:  shared.ReasoningEffort(level.Effort()),
			Summary: shared.ReasoningSummaryAuto,
		}, nil
	}
}

// convertTools maps each tool to a function tool. Strict mode is off,
// because it needs every property to be required, and a tool input can
// have optional fields.
func convertTools(tools []ai.ToolInfo) ([]responses.ToolUnionParam, error) {
	var out []responses.ToolUnionParam
	for _, t := range tools {
		parameters, err := transcript.Parameters(t.InputSchema)
		if err != nil {
			return nil, fmt.Errorf("openai: tool %q: %w", t.Name, err)
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
	return out, nil
}
