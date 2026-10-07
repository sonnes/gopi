package ai

import "context"

// TextProvider streams a text response for a model. It is behavior only.
// A catalog gives a provider its identity when it registers the
// provider.
//
// The messages are the input of the call. The system prompt and the
// first tools travel in opts, as [SystemPrompt] and [Tools].
type TextProvider interface {
	StreamText(
		ctx context.Context,
		model Model,
		messages []Message,
		opts Options,
	) *EventStream
}

// LanguageModel is a [Model] bound to a [TextProvider]. It is a struct,
// not an interface, so a wrapper needs no forwarding methods for the
// optional capabilities. [GenerateObject] and [CountTokens] assert the
// capability on the Provider field. Middleware wraps the provider, not
// the binding.
type LanguageModel struct {
	Model    Model
	Provider TextProvider
}

// StreamText streams a response from the bound provider. It joins opts
// into one value for the provider.
func (lm LanguageModel) StreamText(
	ctx context.Context,
	messages []Message,
	opts ...Options,
) *EventStream {
	return lm.Provider.StreamText(ctx, lm.Model, messages, JoinOptions(opts...))
}
