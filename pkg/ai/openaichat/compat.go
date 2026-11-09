package openaichat

import (
	"strings"

	"github.com/sonnes/gopi/internal/family"
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

// openRouterPrefix starts the ID of an OpenAI model on OpenRouter, such
// as "openai/gpt-6-luna".
const openRouterPrefix = "openai/"

// featuresOf returns the features of a model ID. A model of a GPT family
// of [family.GPT] gets [openAIModel]. An OpenRouter ID gets the features
// of the same OpenAI model.
func featuresOf(id string) features {
	if family.GPT(strings.TrimPrefix(id, openRouterPrefix)) {
		return openAIModel
	}
	return features{}
}
