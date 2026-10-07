package openaichat

import (
	"strings"

	"github.com/sonnes/gopi/pkg/ai/openai"
)

// features holds what a model accepts beyond the base Chat Completions
// API. The zero value is the base API, so a model of another server
// gets a request that every server takes.
type features struct {
	// systemMessages is true if the model takes a system message in the
	// conversation. Without it, the provider sends each system message
	// as a system-reminder in a user message.
	systemMessages bool
	// allowedTools is true if the model takes an allowed_tools tool
	// choice. With it, a removal limits the model to the current tools,
	// and the tool list stays the same. Without it, the tool list holds
	// the current tools.
	allowedTools bool
}

// openAIModel is the feature set of the OpenAI models of this package.
var openAIModel = features{
	systemMessages: true,
	allowedTools:   true,
}

// modelFeatures maps a model ID of the OpenAI API to its features. The
// list comes from the OpenAI docs.
var modelFeatures = map[string]features{
	openai.GPT6Sol.ID:   openAIModel,
	openai.GPT6Luna.ID:  openAIModel,
	openai.GPT6Astra.ID: openAIModel,
	openai.GPT5_6.ID:    openAIModel,
}

// openRouterPrefix starts the ID of an OpenAI model on OpenRouter, such
// as "openai/gpt-6-luna".
const openRouterPrefix = "openai/"

// featuresOf returns the features of a model ID. An OpenRouter ID gets
// the features of the same OpenAI model.
func featuresOf(id string) features {
	return modelFeatures[strings.TrimPrefix(id, openRouterPrefix)]
}
