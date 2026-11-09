package ai

import (
	"context"
	"errors"
	"fmt"
)

// ErrContextExceeded reports that a provider refused a request because
// it does not fit the context window of the model. Each provider wraps
// it, so a caller can compact the history and call again.
var ErrContextExceeded = errors.New("ai: the request exceeds the context window")

const (
	// charsPerToken is the characters of text in one estimated token.
	charsPerToken = 4
	// fileTokens is the estimate of one file. The size of a file in
	// bytes says little about its tokens.
	fileTokens = 1500
)

// EstimateTokens returns a rough count of the tokens of messages, with
// no call. It counts the text, the tool calls, and the thinking of each
// message. Use it for the part of a history that no [Usage] covers.
func EstimateTokens(messages ...Message) int {
	total := 0
	for _, m := range messages {
		for _, c := range m.Content {
			total += estimateContent(c)
		}
	}
	return total
}

func estimateContent(c Content) int {
	switch v := c.(type) {
	case Text:
		return len(v.Text) / charsPerToken
	case ThinkingBlock:
		return len(v.Thinking) / charsPerToken
	case ToolCall:
		return (len(v.Name) + len(v.Input)) / charsPerToken
	case Section:
		return len(v.Text) / charsPerToken
	case File:
		return fileTokens
	default:
		return 0
	}
}

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

// CountTokens counts the input tokens of messages and opts with lm. If
// the provider of lm does not implement [TokenCounter], the error wraps
// [errors.ErrUnsupported].
func CountTokens(
	ctx context.Context,
	lm LanguageModel,
	messages []Message,
	opts ...Options,
) (*TokenCount, error) {
	counter, ok := lm.Provider.(TokenCounter)
	if !ok {
		return nil, fmt.Errorf("ai: provider does not count tokens: %w", errors.ErrUnsupported)
	}
	return counter.CountTokens(ctx, lm.Model, messages, JoinOptions(opts...))
}
