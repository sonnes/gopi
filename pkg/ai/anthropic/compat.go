package anthropic

import "strings"

// features holds what a model accepts beyond the base Messages API. The
// zero value is the base API, so a model that this package does not know
// gets a request that every model takes.
type features struct {
	// systemMessages is true if the model takes system messages in the
	// messages. Without it, the provider sends each system message as a
	// system-reminder in a user message.
	systemMessages bool
	// toolChanges is true if the model takes tool_addition and
	// tool_removal blocks in a system message. Without it, the tool list
	// of the request holds the current tools.
	toolChanges bool
	// effort is true if the model takes effort-only system messages and
	// the drop_block thinking control. With it, each assistant message
	// keeps the effort that it used, and a change of level does not
	// change the cached prefix.
	effort bool
	// noTemperature is true if the model rejects the temperature
	// parameter with a 400.
	noTemperature bool
}

// claude5 is the feature set of the Claude models that manage their
// effort.
var claude5 = features{
	systemMessages: true,
	toolChanges:    true,
	effort:         true,
	noTemperature:  true,
}

// modelFeatures maps a model ID of the Anthropic API to its features.
// The list comes from the Claude docs.
var modelFeatures = map[string]features{
	"claude-fable-5-1":  claude5,
	"claude-opus-5-5":   claude5,
	"claude-opus-5":     claude5,
	"claude-sonnet-5-5": claude5,
	"claude-sonnet-5":   {noTemperature: true},
}

// openRouterPrefix starts the ID of a Claude model on OpenRouter, such
// as "anthropic/claude-opus-5.5".
const openRouterPrefix = "anthropic/"

// featuresOf returns the features of a model ID. An OpenRouter ID gets
// the features of the same Claude model, less the tool changes:
// OpenRouter rejects a request with a tool_addition block.
func featuresOf(id string) features {
	if f, ok := modelFeatures[id]; ok {
		return f
	}

	rest, ok := strings.CutPrefix(id, openRouterPrefix)
	if !ok {
		return features{}
	}
	f := modelFeatures[strings.ReplaceAll(rest, ".", "-")]
	f.toolChanges = false
	return f
}
