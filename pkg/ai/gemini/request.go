package gemini

import (
	"fmt"
	"strings"

	"google.golang.org/genai/interactions/models/interactions"

	"github.com/sonnes/gopi/internal/transcript"
	"github.com/sonnes/gopi/pkg/ai"
)

// systemSeparator joins the texts of the system prompt. The API takes
// one string.
const systemSeparator = "\n\n"

func buildStreamRequest(model ai.Model, messages []ai.Message, opts ai.Options) (interactions.CreateModelInteraction, error) {
	body, err := baseRequest(model, messages, opts)
	if err != nil {
		return body, err
	}

	streaming := true
	body.Stream = &streaming

	tools, _ := ai.GetOption(opts, ai.Tools)
	body.Tools, err = convertTools(transcript.CurrentTools(tools, messages))
	if err != nil {
		return body, err
	}

	choice, ok, err := ai.ToolChoiceOf(opts)
	if err != nil {
		return body, err
	}
	if ok {
		mapped := toolChoice(choice)
		body.GenerationConfig.ToolChoice = &mapped
	}

	return body, nil
}

// baseRequest builds the part of a request that a stream call and an
// object call share. The generation config is never nil.
func baseRequest(model ai.Model, messages []ai.Message, opts ai.Options) (interactions.CreateModelInteraction, error) {
	steps, err := convertMessages(messages)
	if err != nil {
		return interactions.CreateModelInteraction{}, err
	}

	store := false
	input := interactions.NewInteractionsInput(steps)
	body := interactions.CreateModelInteraction{
		Model: interactions.Model(model.ID),
		Store: &store,
		Input: &input,
	}

	if system := ai.SystemPromptOf(opts); len(system) > 0 {
		text := strings.Join(system, systemSeparator)
		body.SystemInstruction = &text
	}

	config, err := generationConfig(model, opts)
	if err != nil {
		return body, err
	}
	body.GenerationConfig = &config

	return body, nil
}

// generationConfig always asks for thought summaries, because a thought
// step has no text without them. It does not send [ai.Temperature],
// because the SDK marks the temperature of the Interactions API as
// deprecated.
func generationConfig(model ai.Model, opts ai.Options) (interactions.GenerationConfig, error) {
	summaries := interactions.ThinkingSummariesAuto
	config := interactions.GenerationConfig{
		ThinkingSummaries: &summaries,
	}

	if n, ok := ai.MaxTokensOf(model, opts); ok {
		config.MaxOutputTokens = &n
	}

	level, ok, err := ai.ThinkingOf(opts)
	if err != nil {
		return config, err
	}
	if ok {
		mapped := thinkingLevel(level)
		config.ThinkingLevel = &mapped
	}

	return config, nil
}

// Gemini 3 and Gemini 4 cannot stop the thinking, so off is minimal,
// the lowest level. The API has no level above high. The provider does
// not check the level against the model, and the API rejects a level
// that the model does not take.
func thinkingLevel(level ai.ThinkingLevel) interactions.ThinkingLevel {
	switch level {
	case ai.ThinkingOff:
		return interactions.ThinkingLevelMinimal
	case ai.ThinkingLow:
		return interactions.ThinkingLevelLow
	case ai.ThinkingMedium:
		return interactions.ThinkingLevelMedium
	default:
		return interactions.ThinkingLevelHigh
	}
}

func toolChoice(c ai.ToolChoiceMode) interactions.ToolChoice {
	switch c.Type {
	case ai.ToolChoiceAuto.Type:
		return interactions.NewToolChoice(interactions.ToolChoiceTypeAuto)
	case ai.ToolChoiceAny.Type:
		return interactions.NewToolChoice(interactions.ToolChoiceTypeAny)
	case ai.ToolChoiceNone.Type:
		return interactions.NewToolChoice(interactions.ToolChoiceTypeNone)
	default:
		mode := interactions.ToolChoiceTypeAny
		return interactions.NewToolChoice(interactions.ToolChoiceConfig{
			AllowedTools: &interactions.AllowedTools{
				Mode:  &mode,
				Tools: []string{c.Name},
			},
		})
	}
}

// The request sends the current tools of the messages, because the
// input has no step that adds or removes a tool.
func convertTools(tools []ai.ToolInfo) ([]interactions.Tool, error) {
	var out []interactions.Tool
	for _, t := range tools {
		parameters, err := transcript.Parameters(t.InputSchema)
		if err != nil {
			return nil, fmt.Errorf("gemini: tool %q: %w", t.Name, err)
		}

		fn := interactions.Function{
			Name:       &t.Name,
			Parameters: parameters,
		}
		if t.Description != "" {
			fn.Description = &t.Description
		}
		out = append(out, interactions.NewTool(fn))
	}
	return out, nil
}
