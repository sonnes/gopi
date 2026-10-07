package ai

import (
	"context"
	"errors"
)

// TokenCounter counts the input tokens of a call before it is made. A
// provider implements it together with [TextProvider].
type TokenCounter interface {
	CountTokens(
		ctx context.Context,
		model Model,
		messages []Message,
		opts Options,
	) (*TokenCount, error)
}

// TokenCount is the number of input tokens, as the provider counts them.
type TokenCount struct {
	Total int
}

// CountTokens counts the input tokens of messages and opts with lm. If the provider of
// lm does not implement [TokenCounter], CountTokens returns an error.
func CountTokens(
	ctx context.Context,
	lm LanguageModel,
	messages []Message,
	opts ...Options,
) (*TokenCount, error) {
	counter, ok := lm.Provider.(TokenCounter)
	if !ok {
		return nil, errors.New("ai: provider does not support token counting")
	}
	return counter.CountTokens(ctx, lm.Model, messages, JoinOptions(opts...))
}
