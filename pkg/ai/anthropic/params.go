package anthropic

import (
	"encoding/json"
	"fmt"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/google/jsonschema-go/jsonschema"

	"github.com/sonnes/gopi/internal/transcript"
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
	converted, err := convertMessages(messages, cfg)
	if err != nil {
		return request{}, err
	}
	maxTokens, ok := ai.MaxTokensOf(model, opts)
	if !ok {
		maxTokens = defaultMaxTokens
	}
	req := request{
		params: sdk.MessageNewParams{
			Model:     sdk.Model(model.ID),
			MaxTokens: int64(maxTokens),
			Messages:  converted,
			System:    systemPromptBlocks(ai.SystemPromptOf(opts), cache),
		},
	}

	tools := firstTools
	if !nativeTools {
		tools = transcript.CurrentTools(firstTools, messages)
	}
	if len(tools) > 0 {
		req.params.Tools, err = convertTools(tools)
		if err != nil {
			return request{}, err
		}
		markLastTool(req.params.Tools, cache)
	}
	if nativeTools {
		// The list never changes. A later tool goes in a tool_addition
		// block, so the cached prefix stays.
		req.params.Tools = append(req.params.Tools, placeholderTool())
		req.betas = append(req.betas, inlineToolsBeta)
	}

	level, levelSet, err := ai.ThinkingOf(opts)
	if err != nil {
		return request{}, err
	}

	if f.effort {
		effort, err := managedEffort(model, level, levelSet)
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
	} else if levelSet {
		req.params.Thinking, req.params.OutputConfig.Effort = thinkingConfig(level)
	}

	thinkingOn := levelSet && level != ai.ThinkingOff
	sendTemperature := !f.noTemperature && !thinkingOn
	if t, ok := ai.GetOption(opts, ai.Temperature); ok && sendTemperature {
		req.params.Temperature = param.NewOpt(t)
	}

	choice, ok, err := ai.ToolChoiceOf(opts)
	if err != nil {
		return request{}, err
	}
	if ok {
		req.params.ToolChoice = toolChoiceParam(choice)
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

	schemaMap, err := transcript.Schema(schema)
	if err != nil {
		return req, fmt.Errorf("anthropic: %w", err)
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

// cacheMarker returns the cache marker of the retention in opts, or nil
// for [ai.CacheNone]. The short cache sends no TTL, because 5 minutes
// is the default of the API.
func cacheMarker(opts ai.Options) *sdk.CacheControlEphemeralParam {
	retention := ai.RetentionOf(opts)
	if retention == ai.CacheNone {
		return nil
	}
	marker := sdk.NewCacheControlEphemeralParam()
	if retention == ai.CacheLong {
		marker.TTL = sdk.CacheControlEphemeralTTLTTL1h
	}
	return &marker
}

// systemPromptBlocks maps each text of the system prompt to one text
// block, in order. The last block carries the cache marker.
func systemPromptBlocks(system []string, cache *sdk.CacheControlEphemeralParam) []sdk.TextBlockParam {
	var out []sdk.TextBlockParam
	for _, text := range system {
		out = append(out, sdk.TextBlockParam{Text: text})
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
// Without it, the first tool change misses the whole cache.
func placeholderTool() sdk.ToolUnionParam {
	return sdk.ToolUnionParam{OfTool: &sdk.ToolParam{
		Name:         placeholderName,
		Description:  param.NewOpt("Reserved placeholder. Never available. Never call this."),
		InputSchema:  sdk.ToolInputSchemaParam{Properties: map[string]any{}},
		DeferLoading: param.NewOpt(true),
	}}
}

// thinkingConfig maps a thinking level to the request. Off sends the
// disabled config. Every other level sends adaptive thinking with the
// level as the effort. The display is explicit, because the API default
// omits the thinking text. If the call sets no level, the request omits
// the parameter and the model uses its default.
func thinkingConfig(level ai.ThinkingLevel) (sdk.ThinkingConfigParamUnion, sdk.OutputConfigEffort) {
	if level == ai.ThinkingOff {
		disabled := sdk.ThinkingConfigParamUnion{OfDisabled: &sdk.ThinkingConfigDisabledParam{}}
		return disabled, ""
	}

	adaptive := sdk.ThinkingConfigParamUnion{
		OfAdaptive: &sdk.ThinkingConfigAdaptiveParam{
			Display: sdk.ThinkingConfigAdaptiveDisplaySummarized,
		},
	}
	return adaptive, sdk.OutputConfigEffort(level)
}

// managedEffort returns the effort of a call to a model that manages
// its effort. The thinking level sets it, and high is the default. The
// model always thinks, so off is an error.
func managedEffort(model ai.Model, level ai.ThinkingLevel, set bool) (string, error) {
	if !set {
		return defaultEffort, nil
	}
	if level == ai.ThinkingOff {
		return "", fmt.Errorf("anthropic: %s always thinks, so thinking cannot be off", model.ID)
	}
	return string(level), nil
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

// toolChoiceParam maps a tool choice that [ai.ToolChoiceOf] returned to
// the API.
func toolChoiceParam(c ai.ToolChoiceMode) sdk.ToolChoiceUnionParam {
	switch c.Type {
	case ai.ToolChoiceAuto.Type:
		return sdk.ToolChoiceUnionParam{OfAuto: &sdk.ToolChoiceAutoParam{}}
	case ai.ToolChoiceAny.Type:
		return sdk.ToolChoiceUnionParam{OfAny: &sdk.ToolChoiceAnyParam{}}
	case ai.ToolChoiceNone.Type:
		return sdk.ToolChoiceUnionParam{OfNone: &sdk.ToolChoiceNoneParam{}}
	default:
		return sdk.ToolChoiceParamOfTool(c.Name)
	}
}

// convertTools maps each tool to a function tool with eager input
// streaming, so the input streams as the model writes it.
func convertTools(tools []ai.ToolInfo) ([]sdk.ToolUnionParam, error) {
	out := make([]sdk.ToolUnionParam, 0, len(tools))
	for _, t := range tools {
		schema, err := convertSchema(t.InputSchema)
		if err != nil {
			return nil, fmt.Errorf("anthropic: tool %q: %w", t.Name, err)
		}
		tool := &sdk.ToolParam{
			Name:                t.Name,
			InputSchema:         schema,
			EagerInputStreaming: param.NewOpt(true),
		}
		if t.Description != "" {
			tool.Description = param.NewOpt(t.Description)
		}
		out = append(out, sdk.ToolUnionParam{OfTool: tool})
	}
	return out, nil
}

// convertSchema maps a JSON schema to the input schema param. The param
// has fields for properties and required. Every other keyword goes in
// an extra field.
func convertSchema(schema *jsonschema.Schema) (sdk.ToolInputSchemaParam, error) {
	var out sdk.ToolInputSchemaParam

	m, err := transcript.Schema(schema)
	if err != nil {
		return out, err
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

	return out, nil
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
