package anthropic

import (
	"encoding/json"
	"fmt"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/google/jsonschema-go/jsonschema"

	"github.com/sonnes/gopi/pkg/ai"
)

// defaultMaxTokens is the output cap when neither the call nor the model
// sets one. The SDK streams, so a large cap does not cause a timeout.
const defaultMaxTokens = 64000

// The betas of the API that a request can need.
const (
	// midConversationOutputConfigBeta reads an effort in a system
	// message.
	midConversationOutputConfigBeta = "mid-conversation-output-config-2026-07-01"
	// thinkingBindingControlsBeta reads the drop_block control.
	thinkingBindingControlsBeta = "thinking-binding-controls-2026-08-01"
)

// defaultEffort is the effort of a model that manages its effort when
// the call sets no thinking level.
const defaultEffort = "high"

// placeholderName is the name of the deferred tool that a request with
// tool changes declares. The model never sees it.
const placeholderName = "__gopi_deferred_placeholder__"

// request is a stream request and the betas that it needs.
type request struct {
	params sdk.MessageNewParams
	betas  []string
	// effort is the effort of the call for a model that manages its
	// effort. The final message records it.
	effort string
}

// buildRequest builds the request of a stream call. The system prompt,
// the tools, and the settings come from opts.
func buildRequest(model ai.Model, messages []ai.Message, opts ai.Options) (request, error) {
	f := featuresOf(model.ID)
	cache := cacheMarker(opts)
	system, _ := ai.GetOption(opts, ai.SystemPrompt)
	firstTools, _ := ai.GetOption(opts, ai.Tools)

	// The API rejects a tool list where every tool is deferred, so
	// native tool changes need a first tool.
	nativeTools := f.systemMessages && f.toolChanges && len(firstTools) > 0

	cfg := convertConfig{
		systemMessages: f.systemMessages,
		toolChanges:    nativeTools,
		effort:         f.effort,
		cache:          cache,
	}
	req := request{
		params: sdk.MessageNewParams{
			Model:     sdk.Model(model.ID),
			MaxTokens: maxTokens(model, opts),
			Messages:  convertMessages(messages, cfg),
			System:    systemPromptBlocks(system, cache),
		},
	}

	if nativeTools {
		// The list never changes. A later tool goes in a tool_addition
		// block, so the cached prefix stays.
		req.params.Tools = append(convertTools(firstTools), placeholderTool())
		markLastTool(req.params.Tools[:len(firstTools)], cache)
		req.betas = append(req.betas, inlineToolsBeta)
	} else if tools := ai.CurrentTools(firstTools, messages); len(tools) > 0 {
		req.params.Tools = convertTools(tools)
		markLastTool(req.params.Tools, cache)
	}

	if f.effort {
		effort, err := managedEffort(model, opts)
		if err != nil {
			return req, err
		}
		req.effort = effort
		req.params.Thinking = bindingThinking()
		// The effort of the request stays the same, so the prefix
		// stays. A system message at the end sets the effort of the
		// call.
		req.params.OutputConfig.Effort = defaultEffort
		req.params.Messages = append(req.params.Messages, effortMessage(effort))
		req.betas = append(req.betas, midConversationOutputConfigBeta, thinkingBindingControlsBeta)
	} else {
		thinking, effort, err := thinkingConfig(opts)
		if err != nil {
			return req, err
		}
		req.params.Thinking = thinking
		req.params.OutputConfig.Effort = effort
	}

	sendTemperature := !f.noTemperature && !thinkingOn(opts)
	if t, ok := ai.GetOption(opts, ai.Temperature); ok && sendTemperature {
		req.params.Temperature = param.NewOpt(t)
	}

	if c, ok := ai.GetOption(opts, ai.ToolChoice); ok {
		choice, err := toolChoiceParam(c)
		if err != nil {
			return req, err
		}
		req.params.ToolChoice = choice
	}

	return req, nil
}

// buildObjectRequest builds the request of an object call. It is a
// stream request with the schema as the output format.
func buildObjectRequest(
	model ai.Model,
	messages []ai.Message,
	schema *jsonschema.Schema,
	opts ai.Options,
) (request, error) {
	req, err := buildRequest(model, messages, opts)
	if err != nil {
		return req, err
	}

	schemaMap, err := toMap(schema)
	if err != nil {
		return req, err
	}
	req.params.OutputConfig.Format = sdk.JSONOutputFormatParam{Schema: schemaMap}

	return req, nil
}

// buildCountTokensParams builds the request of a token count from the
// stream request, so the count is correct. It returns the betas of the
// stream request too.
func buildCountTokensParams(
	model ai.Model,
	messages []ai.Message,
	opts ai.Options,
) (sdk.MessageCountTokensParams, []string, error) {
	req, err := buildRequest(model, messages, opts)
	if err != nil {
		return sdk.MessageCountTokensParams{}, nil, err
	}

	params := sdk.MessageCountTokensParams{
		Model:        req.params.Model,
		Messages:     req.params.Messages,
		Thinking:     req.params.Thinking,
		OutputConfig: req.params.OutputConfig,
		ToolChoice:   req.params.ToolChoice,
	}
	if len(req.params.System) > 0 {
		params.System = sdk.MessageCountTokensParamsSystemUnion{
			OfTextBlockArray: req.params.System,
		}
	}
	for _, tool := range req.params.Tools {
		params.Tools = append(
			params.Tools,
			sdk.MessageCountTokensToolUnionParam{OfTool: tool.OfTool},
		)
	}

	return params, req.betas, nil
}

// maxTokens is the option, else the cap of the model, else the default.
func maxTokens(model ai.Model, opts ai.Options) int64 {
	if n, ok := ai.GetOption(opts, ai.MaxTokens); ok {
		return int64(n)
	}
	if model.MaxTokens > 0 {
		return int64(model.MaxTokens)
	}
	return defaultMaxTokens
}

// cacheMarker returns the cache marker of the retention in opts, or nil
// for [ai.CacheNone]. The short cache sends no TTL, because 5 minutes
// is the default of the API.
func cacheMarker(opts ai.Options) *sdk.CacheControlEphemeralParam {
	retention, ok := ai.GetOption(opts, ai.CacheRetention)
	if !ok {
		retention = ai.CacheShort
	}

	if retention == ai.CacheNone {
		return nil
	}
	marker := sdk.NewCacheControlEphemeralParam()
	if retention == ai.CacheLong {
		marker.TTL = sdk.CacheControlEphemeralTTLTTL1h
	}
	return &marker
}

// systemPromptBlocks maps the blocks of the [ai.SystemPrompt] option
// 1:1. Each [ai.Text] and [ai.Section] block is one text block, in
// order. Every other block type is skipped. The last block carries the
// cache marker.
func systemPromptBlocks(system []ai.Content, cache *sdk.CacheControlEphemeralParam) []sdk.TextBlockParam {
	var out []sdk.TextBlockParam
	for _, c := range system {
		switch v := c.(type) {
		case ai.Text:
			out = append(out, sdk.TextBlockParam{Text: v.Text})
		case ai.Section:
			out = append(out, sdk.TextBlockParam{Text: v.Text})
		}
	}
	if n := len(out); n > 0 && cache != nil {
		out[n-1].CacheControl = *cache
	}
	return out
}

// markLastTool puts the cache marker on the last tool.
func markLastTool(tools []sdk.ToolUnionParam, cache *sdk.CacheControlEphemeralParam) {
	if len(tools) == 0 || cache == nil {
		return
	}
	if marker := tools[len(tools)-1].GetCacheControl(); marker != nil {
		*marker = *cache
	}
}

// placeholderTool is a deferred tool that a request with tool changes
// declares from the first request. The API adds a hidden scaffold for
// tool changes. With the placeholder, the scaffold is in the cached
// prefix, so the first tool change does not cause a cache miss.
// pi-mono measured a full miss without it.
func placeholderTool() sdk.ToolUnionParam {
	return sdk.ToolUnionParam{OfTool: &sdk.ToolParam{
		Name:         placeholderName,
		Description:  param.NewOpt("Reserved placeholder. Never available. Never call this."),
		InputSchema:  sdk.ToolInputSchemaParam{Properties: map[string]any{}},
		DeferLoading: param.NewOpt(true),
	}}
}

// thinkingOn reports if the call sets a thinking level other than off.
func thinkingOn(opts ai.Options) bool {
	level, ok := ai.GetOption(opts, ai.Thinking)
	return ok && level != ai.ThinkingOff
}

// thinkingConfig maps the thinking option to the request. If the option
// is unset, the request omits the parameter and the model uses its
// default. Off sends the disabled config. Every other level sends
// adaptive thinking with the level as the effort. The display is
// explicit, because the API default omits the thinking text.
func thinkingConfig(
	opts ai.Options,
) (sdk.ThinkingConfigParamUnion, sdk.OutputConfigEffort, error) {
	level, ok := ai.GetOption(opts, ai.Thinking)
	if !ok {
		return sdk.ThinkingConfigParamUnion{}, "", nil
	}

	if level == ai.ThinkingOff {
		disabled := sdk.ThinkingConfigParamUnion{OfDisabled: &sdk.ThinkingConfigDisabledParam{}}
		return disabled, "", nil
	}

	effort, ok := efforts[level]
	if !ok {
		err := fmt.Errorf("anthropic: unknown thinking level %q", level)
		return sdk.ThinkingConfigParamUnion{}, "", err
	}

	adaptive := sdk.ThinkingConfigParamUnion{
		OfAdaptive: &sdk.ThinkingConfigAdaptiveParam{
			Display: sdk.ThinkingConfigAdaptiveDisplaySummarized,
		},
	}
	return adaptive, effort, nil
}

// managedEffort returns the effort of a call to a model that manages
// its effort. The thinking level sets it, and high is the default. The
// model always thinks, so off is an error.
func managedEffort(model ai.Model, opts ai.Options) (string, error) {
	level, ok := ai.GetOption(opts, ai.Thinking)
	if !ok {
		return defaultEffort, nil
	}
	if level == ai.ThinkingOff {
		return "", fmt.Errorf("anthropic: %s always thinks, so thinking cannot be off", model.ID)
	}

	effort, ok := efforts[level]
	if !ok {
		return "", fmt.Errorf("anthropic: unknown thinking level %q", level)
	}
	return string(effort), nil
}

// bindingThinking is adaptive thinking with the drop_block control. If
// an earlier thinking block does not match its prefix, the API drops
// the block instead of an error.
func bindingThinking() sdk.ThinkingConfigParamUnion {
	adaptive := &sdk.ThinkingConfigAdaptiveParam{
		Display: sdk.ThinkingConfigAdaptiveDisplaySummarized,
	}
	adaptive.SetExtraFields(map[string]any{
		"block_binding": map[string]any{"prefix_mismatch_behavior": "drop_block"},
	})
	return sdk.ThinkingConfigParamUnion{OfAdaptive: adaptive}
}

// efforts maps a thinking level to the effort of the API.
var efforts = map[ai.ThinkingLevel]sdk.OutputConfigEffort{
	ai.ThinkingLow:    sdk.OutputConfigEffortLow,
	ai.ThinkingMedium: sdk.OutputConfigEffortMedium,
	ai.ThinkingHigh:   sdk.OutputConfigEffortHigh,
	ai.ThinkingXHigh:  sdk.OutputConfigEffortXhigh,
	ai.ThinkingMax:    sdk.OutputConfigEffortMax,
}

// toolChoiceParam maps a tool choice to the API. An unknown type is an
// error.
func toolChoiceParam(c ai.ToolChoiceMode) (sdk.ToolChoiceUnionParam, error) {
	switch c.Type {
	case ai.ToolChoiceAuto.Type:
		return sdk.ToolChoiceUnionParam{OfAuto: &sdk.ToolChoiceAutoParam{}}, nil
	case ai.ToolChoiceAny.Type:
		return sdk.ToolChoiceUnionParam{OfAny: &sdk.ToolChoiceAnyParam{}}, nil
	case ai.ToolChoiceNone.Type:
		return sdk.ToolChoiceUnionParam{OfNone: &sdk.ToolChoiceNoneParam{}}, nil
	case "tool":
		return sdk.ToolChoiceParamOfTool(c.Name), nil
	default:
		return sdk.ToolChoiceUnionParam{}, fmt.Errorf("anthropic: unknown tool choice %q", c.Type)
	}
}

// convertTools maps each tool to a function tool with eager input
// streaming, so the input streams as the model writes it.
func convertTools(tools []ai.ToolInfo) []sdk.ToolUnionParam {
	out := make([]sdk.ToolUnionParam, 0, len(tools))
	for _, t := range tools {
		tool := &sdk.ToolParam{
			Name:                t.Name,
			InputSchema:         convertSchema(t.InputSchema),
			EagerInputStreaming: param.NewOpt(true),
		}
		if t.Description != "" {
			tool.Description = param.NewOpt(t.Description)
		}
		out = append(out, sdk.ToolUnionParam{OfTool: tool})
	}
	return out
}

// convertSchema maps a JSON schema to the input schema param. The param
// has fields for properties and required. Every other keyword goes in
// an extra field.
func convertSchema(schema *jsonschema.Schema) sdk.ToolInputSchemaParam {
	var out sdk.ToolInputSchemaParam

	m, err := toMap(schema)
	if err != nil || m == nil {
		return out
	}

	for key, value := range m {
		switch key {
		case "type":
			// The param writes "object" itself.
		case "properties":
			out.Properties = value
		case "required":
			if list, ok := value.([]any); ok {
				for _, item := range list {
					if name, ok := item.(string); ok {
						out.Required = append(out.Required, name)
					}
				}
			}
		default:
			if out.ExtraFields == nil {
				out.ExtraFields = map[string]any{}
			}
			out.ExtraFields[key] = value
		}
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
		return nil, fmt.Errorf("anthropic: encode schema: %w", err)
	}

	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("anthropic: decode schema: %w", err)
	}
	return m, nil
}

// buildMessage builds the final message from the accumulated response.
// effort is the effort of the call, for a model that manages its
// effort. A response with no stop reason, or an unknown one, is an
// error.
func buildMessage(model ai.Model, acc sdk.Message, effort string) (*ai.Message, error) {
	stopReason, err := mapStopReason(acc.StopReason)
	if err != nil {
		return nil, err
	}

	var content []ai.Content
	for _, block := range acc.Content {
		switch block.Type {
		case "text":
			content = append(content, ai.Text{Text: block.Text})
		case "thinking":
			content = append(content, ai.ThinkingBlock{
				Thinking:  block.Thinking,
				Signature: block.Signature,
			})
		case "tool_use":
			content = append(content, toolCall(block))
		}
	}

	usage := mapUsage(acc.Usage)
	usage.Cost = model.Pricing.Cost(usage)

	return &ai.Message{
		Role:       ai.RoleAssistant,
		Content:    content,
		Model:      model.ID,
		Usage:      usage,
		StopReason: stopReason,
		StopDetail: stopDetail(acc),
		Effort:     effort,
	}, nil
}

// toolCall maps a tool_use block to a call. The input is the JSON as the
// model wrote it. It can stop short at the output cap.
func toolCall(block sdk.ContentBlockUnion) ai.ToolCall {
	return ai.ToolCall{
		ID:    block.ID,
		Name:  block.Name,
		Input: json.RawMessage(block.Input),
	}
}

// stopReasonSensitive is a stop by the safety filters of the API. The
// SDK has no constant for it.
const stopReasonSensitive sdk.StopReason = "sensitive"

// mapStopReason maps the stop reason of the API. A context window
// overflow is a length stop, because the caller treats both the same
// way. A pause is a stop, and the caller sends the messages again. A
// sensitive stop is a refusal. A stream with no stop reason ended
// early, so it is an error. An unknown reason is an error, so a new
// reason of the API does not pass as a normal stop.
func mapStopReason(reason sdk.StopReason) (ai.StopReason, error) {
	switch reason {
	case sdk.StopReasonEndTurn, sdk.StopReasonStopSequence, sdk.StopReasonPauseTurn:
		return ai.StopReasonStop, nil
	case sdk.StopReasonMaxTokens, sdk.StopReasonModelContextWindowExceeded:
		return ai.StopReasonLength, nil
	case sdk.StopReasonToolUse:
		return ai.StopReasonToolUse, nil
	case sdk.StopReasonRefusal, stopReasonSensitive:
		return ai.StopReasonRefusal, nil
	case "":
		return "", fmt.Errorf("anthropic: the stream ended with no stop reason")
	default:
		return "", fmt.Errorf("anthropic: unknown stop reason %q", reason)
	}
}

// stopDetail is the explanation of a refusal, or "sensitive" for a stop
// by the safety filters.
func stopDetail(acc sdk.Message) string {
	switch acc.StopReason {
	case sdk.StopReasonRefusal:
		return acc.StopDetails.Explanation
	case stopReasonSensitive:
		return string(stopReasonSensitive)
	default:
		return ""
	}
}

// mapUsage maps the usage of the API. The API reports the input tokens
// without the cached prefix, so the four fields do not overlap. The
// long cache writes are a part of the cache writes.
func mapUsage(u sdk.Usage) ai.Usage {
	return ai.Usage{
		Input:          int(u.InputTokens),
		Output:         int(u.OutputTokens),
		CacheRead:      int(u.CacheReadInputTokens),
		CacheWrite:     int(u.CacheCreationInputTokens),
		CacheWriteLong: int(u.CacheCreation.Ephemeral1hInputTokens),
	}
}
