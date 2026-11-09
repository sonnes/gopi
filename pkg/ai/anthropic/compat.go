package anthropic

import (
	"strings"

	"github.com/sonnes/gopi/internal/family"
)

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

// modelFeatures maps a family of the model IDs of the Anthropic API to
// its features. The longest family of an ID wins, so a later model of a
// family gets the features of the family. Claude Sonnet 5.5 has more
// features than Claude Sonnet 5. The list comes from the Claude docs.
var modelFeatures = map[string]features{
	"claude-fable-5-1":  claude5,
	"claude-opus-5":     claude5,
	"claude-sonnet-5":   {noTemperature: true},
	"claude-sonnet-5-5": claude5,
}

// openRouterPrefix starts the ID of a Claude model on OpenRouter, such
// as "anthropic/claude-opus-5.5".
const openRouterPrefix = "anthropic/"

// featuresOf returns the features of a model ID. An OpenRouter ID gets
// the features of the same Claude model, less the tool changes:
// OpenRouter rejects a request with a tool_addition block.
func featuresOf(id string) features {
	rest, onOpenRouter := strings.CutPrefix(id, openRouterPrefix)
	if !onOpenRouter {
		f, _ := family.Of(modelFeatures, id)
		return f
	}

	f, _ := family.Of(modelFeatures, strings.ReplaceAll(rest, ".", "-"))
	f.toolChanges = false
	return f
}
