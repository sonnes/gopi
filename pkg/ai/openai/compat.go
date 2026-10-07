package openai

import "strings"

// features holds what a model accepts beyond the base Responses API.
// The zero value is the base API, so a pass-through model ID gets a
// request that every model takes.
type features struct {
	// systemMessages is true if the model takes a developer message in
	// the conversation. Without it, the provider sends each system
	// message as a system-reminder in a user message.
	systemMessages bool
	// additionalTools is true if the model takes an additional_tools
	// item. Without it, the tool list of the request holds the current
	// tools.
	additionalTools bool
	// cacheBreakpoint is true if the model takes a cache breakpoint on
	// a content part. An older model rejects one with a 400.
	cacheBreakpoint bool
}

// gpt is the feature set of the GPT text models of this package.
var gpt = features{
	systemMessages:  true,
	additionalTools: true,
	cacheBreakpoint: true,
}

// modelFeatures maps a model ID of the OpenAI API to its features. The
// list comes from the OpenAI docs.
var modelFeatures = map[string]features{
	GPT6Sol.ID:   gpt,
	GPT6Luna.ID:  gpt,
	GPT6Astra.ID: gpt,
	GPT5_6.ID:    gpt,
}

// openRouterPrefix starts the ID of an OpenAI model on OpenRouter, such
// as "openai/gpt-6-luna".
const openRouterPrefix = "openai/"

// featuresOf returns the features of a model ID. An OpenRouter ID gets
// the features of the same OpenAI model. OpenRouter accepts a developer
// message in the conversation and an additional_tools item.
func featuresOf(id string) features {
	return modelFeatures[strings.TrimPrefix(id, openRouterPrefix)]
}
