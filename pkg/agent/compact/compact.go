// Package compact holds the compaction strategies of gopi. A strategy is
// an [agent.Compactor]: pass it to [agent.Compaction].
//
// [Summarize] replaces the old part of the view with one summary.
// A caller can write another strategy, because a strategy only returns
// edits.
//
// A strategy uses the model of the agent and the same prefix by default,
// so its call reads the prompt cache. The reasons are in
// docs/concepts/agent/compaction.md.
package compact

import (
	"github.com/sonnes/gopi/pkg/ai"
)

const (
	// defaultLimit is the highest limit that a strategy uses by default.
	defaultLimit = 200_000
	// defaultFraction is the part of the context window that starts a
	// compaction by default.
	defaultFraction = 0.8
	// defaultKeepRecent is the tokens after the cut of [Summarize].
	defaultKeepRecent = 20_000
	// defaultKeepFirst is the first user messages that [Summarize] keeps.
	defaultKeepFirst = 1
	// defaultKeepFiles is the file reads that [Summarize] keeps.
	defaultKeepFiles = 5
)

// Option configures a strategy.
type Option func(*options)

type options struct {
	limitTokens   int
	limitFraction float64
	keepRecent    int
	keepFirst     int
	keepFiles     int
	model         *ai.LanguageModel
}

func newOptions(opts []Option) options {
	o := options{
		keepRecent: defaultKeepRecent,
		keepFirst:  defaultKeepFirst,
		keepFiles:  defaultKeepFiles,
	}
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

// Limit sets the size that starts a compaction: tokens, and a fraction of
// the context window of the model. The lower value wins, and 0 ignores
// one of them. The default is the lower of 200k tokens and 80% of the
// window. A fraction has no effect on a model with no known window.
func Limit(tokens int, fraction float64) Option {
	return func(o *options) {
		o.limitTokens = tokens
		o.limitFraction = fraction
	}
}

// KeepRecent sets the tokens after the cut of [Summarize]. A value of 0
// gives a fresh start with one summary. The default is 20k.
func KeepRecent(tokens int) Option {
	return func(o *options) { o.keepRecent = tokens }
}

// KeepFirst sets the first user messages that [Summarize] keeps word for
// word. The default is 1.
func KeepFirst(n int) Option {
	return func(o *options) { o.keepFirst = n }
}

// KeepFiles keeps the results of the last n calls of the read_file tool
// before the cut, after the summary of [Summarize]. The default is 5. If
// a result is more than 5k tokens, the strategy clips it. A result shows
// the file at the time of the call, and the model can read it again.
func KeepFiles(n int) Option {
	return func(o *options) { o.keepFiles = n }
}

// Model sets the model of the strategy. Without it, the strategy uses
// [agent.CompactInput.Model] and the same prefix, so the call reads the
// cache. With it, the call reads the history as serialized text, and it
// reads no cache. If the text does not fit the window of the model, the
// strategy clips each tool result. If the text still does not fit, the
// compaction fails with [ErrTooLarge].
func Model(lm ai.LanguageModel) Option {
	return func(o *options) { o.model = &lm }
}

// Limit returns the limit of the options for the model m.
func (o options) Limit(m ai.Model) int {
	tokens := o.limitTokens
	fraction := o.limitFraction
	if tokens == 0 && fraction == 0 {
		tokens = defaultLimit
		fraction = defaultFraction
	}

	byWindow := int(fraction * float64(m.ContextWindow))
	switch {
	case byWindow == 0 && tokens == 0:
		return defaultLimit
	case byWindow == 0:
		return tokens
	case tokens == 0:
		return byWindow
	default:
		return min(tokens, byWindow)
	}
}
