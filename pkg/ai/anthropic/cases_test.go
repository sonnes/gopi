package anthropic_test

import (
	"regexp"
	"strings"
	"testing"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/internal/providertest"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/ai/anthropic"
)

func TestCases(t *testing.T) {
	providertest.Run(t, providertest.Suite{
		New:           func(t *testing.T) ai.TextProvider { return newTestProvider(t) },
		Model:         haiku,
		SystemModel:   sonnet55,
		ReminderModel: sonnet5,
	})
}

// The models of the cases.
var (
	haiku    = anthropic.Haiku4_5
	sonnet5  = anthropic.Sonnet5
	sonnet55 = anthropic.Sonnet5_5
	fable    = anthropic.Fable5_1
)

// stream runs one call and returns the final message.
func stream(t *testing.T, p *anthropic.Provider, model ai.Model, messages []ai.Message, opts ...ai.Options) (*ai.Message, error) {
	t.Helper()
	return p.StreamText(t.Context(), model, messages, ai.JoinOptions(opts...)).Wait()
}

func TestCase_InterleavedThinking(t *testing.T) {
	p := newTestProvider(t)
	opts := ai.JoinOptions(
		ai.SystemPrompt([]ai.Content{ai.Text{Text: "You are a helpful assistant that must use tools for arithmetic. " +
			"Always think before every tool call, not just the first one. " +
			"Do not answer with plain text when a tool call is required."}}),
		ai.Tools([]ai.ToolInfo{providertest.MathTool.Info()}),
		ai.Thinking(ai.ThinkingHigh),
	)
	prompt := "Use math_operation to calculate 328 * 29. You must call the math_operation tool exactly once. " +
		"Provide the final answer based on the best guess given the tool result, even if it seems unreliable. " +
		"Start by thinking about the steps you will take to solve the problem."

	messages := []ai.Message{ai.UserMessage(prompt)}
	first, err := stream(t, p, sonnet55, messages, opts)
	require.NoError(t, err)
	require.Equal(t, ai.StopReasonToolUse, first.StopReason)
	assert.Equal(t, "high", first.Effort)

	calls := first.ToolCalls()
	require.Len(t, calls, 1)
	result, err := providertest.MathTool.Run(t.Context(), calls[0])
	require.NoError(t, err)
	answer := result.Content[0].(ai.Text).Text

	messages = append(
		messages,
		*first,
		ai.ToolResultMessage(calls[0].ID, ai.Text{Text: "The answer is " + answer + " or " + answer + answer + "."}),
	)
	second, err := stream(t, p, sonnet55, messages, opts)
	require.NoError(t, err)
	assert.Equal(t, ai.StopReasonStop, second.StopReason)
	assert.NotEmpty(t, second.Text())
	// Adaptive thinking can skip the thinking before the first call. The
	// thinking after a tool result is the interleaved thinking.
	assert.True(t, hasThinking(second), "thinking after the tool result")
}

// hasThinking reports if msg has a signed thinking block.
func hasThinking(msg *ai.Message) bool {
	for _, c := range msg.Content {
		if th, ok := c.(ai.ThinkingBlock); ok && th.Signature != "" {
			return true
		}
	}
	return false
}

func TestCase_ThinkingBinding(t *testing.T) {
	p := newTestProvider(t)

	// strict makes the API fail on a thinking block whose prefix does
	// not match, instead of a drop of the block.
	strict := anthropic.OnPayload(func(params *sdk.MessageNewParams) error {
		params.Thinking.OfAdaptive.SetExtraFields(map[string]any{
			"block_binding": map[string]any{"prefix_mismatch_behavior": "error"},
		})
		return nil
	})
	opts := func(level ai.ThinkingLevel) ai.Options {
		return ai.JoinOptions(
			ai.CacheRetention(ai.CacheNone),
			ai.MaxTokens(4096),
			ai.Thinking(level),
			strict,
		)
	}

	firstUser := ai.UserMessage("Compute 982451653 multiplied by 961748941. Return only the integer.")
	first, err := stream(t, p, fable, []ai.Message{firstUser}, opts(ai.ThinkingLow))
	require.NoError(t, err)
	require.Equal(t, ai.StopReasonStop, first.StopReason)
	require.True(t, hasThinking(first))
	assert.Equal(t, "low", first.Effort)

	secondUser := ai.UserMessage("Reply with exactly: ok")
	exact, err := stream(t, p, fable, []ai.Message{firstUser, *first, secondUser}, opts(ai.ThinkingHigh))
	require.NoError(t, err)
	assert.Equal(t, ai.StopReasonStop, exact.StopReason, "the effort of the old turn keeps its prefix")

	unmanaged := *first
	unmanaged.Effort = ""
	_, err = stream(t, p, fable, []ai.Message{firstUser, unmanaged, secondUser}, opts(ai.ThinkingHigh))
	require.Error(t, err, "without its effort, the old turn has another prefix")
	assert.Contains(t, err.Error(), "signature")
}

func TestCase_ThinkingOff(t *testing.T) {
	p := newTestProvider(t)

	s := p.StreamText(
		t.Context(),
		sonnet5,
		[]ai.Message{ai.UserMessage("Before replying, carefully solve 36863 * 5279 internally. " +
			"Then reply with the word pong repeated exactly 40 times, separated by single spaces. " +
			"Do not add any other text.")},
		ai.JoinOptions(
			ai.SystemPrompt([]ai.Content{ai.Text{Text: "You are a precise assistant. Follow the requested output format exactly."}}),
			ai.Thinking(ai.ThinkingOff),
			ai.Temperature(0),
			ai.MaxTokens(160),
		),
	)
	var thinkingEvents int
	for ev, err := range s.Events() {
		require.NoError(t, err)
		if ev.Type == ai.EventThinkStart || ev.Type == ai.EventThinkDelta {
			thinkingEvents++
		}
	}
	msg, err := s.Wait()
	require.NoError(t, err)

	assert.Zero(t, thinkingEvents)
	for _, c := range msg.Content {
		assert.IsType(t, ai.Text{}, c, "no thinking block")
	}
	pongs := regexp.MustCompile(`(?i)\bpong\b`).FindAllString(msg.Text(), -1)
	assert.GreaterOrEqual(t, len(pongs), 35)
}

func TestCase_LongCacheRetention(t *testing.T) {
	p := newTestProvider(t)

	// The prefix must pass the minimum cacheable size of the model.
	system := "You are a concise assistant. " + strings.Repeat("Answer in one short sentence. ", 1200)
	msg, err := stream(
		t,
		p,
		haiku,
		[]ai.Message{ai.UserMessage("Reply with exactly: long cache retention accepted")},
		ai.SystemPrompt([]ai.Content{ai.Text{Text: system}}),
		ai.CacheRetention(ai.CacheLong),
		ai.MaxTokens(128),
	)
	require.NoError(t, err)
	assert.Contains(t, msg.Text(), "long cache retention accepted")
	assert.Positive(t, msg.Usage.CacheWriteLong+msg.Usage.CacheRead, "the 1-hour cache holds the prefix")
}
