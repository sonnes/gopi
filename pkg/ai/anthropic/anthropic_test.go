package anthropic_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/internal/modelstest"
	"github.com/sonnes/gopi/internal/providertest"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/ai/anthropic"
	"github.com/sonnes/gopi/pkg/tool"
)

// testModel is the model of the recorded tests.
var testModel = modelstest.Model("anthropic", "claude-sonnet-5")

// canonicalHost is the host in every fixture.
const canonicalHost = "api.anthropic.com"

type weatherInput struct {
	City string `json:"city" jsonschema:"The city name"`
}

var weatherTool = tool.New("get_weather", func(_ context.Context, in weatherInput) (string, error) {
	return "Sunny, 22C in " + in.City, nil
}, tool.Description("Get the current weather in a city"))

// recordFrom is the API that a test records from: the API of
// ANTHROPIC_BASE_URL, Anthropic by default. Without ANTHROPIC_API_KEY,
// the test records from the Messages API of OpenRouter.
var recordFrom = providertest.Server{
	KeyEnv:  "ANTHROPIC_API_KEY",
	BaseURL: os.Getenv("ANTHROPIC_BASE_URL"),
	Fallback: &providertest.Server{
		KeyEnv:  "OPENROUTER_API_KEY",
		BaseURL: "https://openrouter.ai/api",
	},
}

// newTestProvider returns a provider that replays the fixture of the
// test.
func newTestProvider(t *testing.T) *anthropic.Provider {
	t.Helper()
	r := providertest.Open(t, recordFrom, canonicalHost)
	return anthropic.New(
		anthropic.APIKey(r.APIKey),
		anthropic.BaseURL(r.BaseURL),
		anthropic.HTTPClient(r.Client),
	)
}

func TestStreamText(t *testing.T) {
	p := newTestProvider(t)

	stream := p.StreamText(
		t.Context(),
		testModel,
		[]ai.Message{ai.UserMessage("Say hi in Portuguese")},
		ai.SystemPrompt([]ai.Content{ai.Text{Text: "You are a helpful assistant. Be concise."}}),
	)

	var types []ai.EventType
	var text strings.Builder
	for ev, err := range stream.Events() {
		require.NoError(t, err)
		types = append(types, ev.Type)
		if ev.Type == ai.EventTextDelta {
			text.WriteString(ev.Delta)
		}
	}

	msg, err := stream.Wait()
	require.NoError(t, err)

	assert.Equal(t, ai.EventStart, types[0])
	assert.Contains(t, types, ai.EventTextStart)
	assert.Contains(t, types, ai.EventTextEnd)
	assert.Equal(t, text.String(), msg.Text())
	greeting := strings.ToLower(msg.Text())
	isPortuguese := strings.Contains(greeting, "oi") || strings.Contains(greeting, "olá")
	assert.True(t, isPortuguese, "response %q is not a Portuguese greeting", msg.Text())

	assert.Equal(t, ai.RoleAssistant, msg.Role)
	assert.Equal(t, testModel.ID, msg.Model)
	assert.Equal(t, ai.StopReasonStop, msg.StopReason)
	assert.NotZero(t, msg.Usage.Input)
	assert.NotZero(t, msg.Usage.Output)
	assert.NotZero(t, msg.Usage.Cost.Total(), "the provider prices the usage")
	assert.Equal(t, testModel.Pricing.Cost(msg.Usage), msg.Usage.Cost)
}

func TestToolCall(t *testing.T) {
	p := newTestProvider(t)

	stream := p.StreamText(
		t.Context(),
		testModel,
		[]ai.Message{ai.UserMessage("What is the weather in Paris?")},
		ai.JoinOptions(
			ai.SystemPrompt([]ai.Content{ai.Text{Text: "Use the get_weather tool to answer questions about the weather."}}),
			ai.Tools([]ai.ToolInfo{weatherTool.Info()}),
			ai.Thinking(ai.ThinkingLow),
		),
	)

	var deltas strings.Builder
	var end *ai.ToolCall
	for ev, err := range stream.Events() {
		require.NoError(t, err)
		switch ev.Type {
		case ai.EventToolDelta:
			deltas.WriteString(ev.Delta)
		case ai.EventToolEnd:
			end = ev.ToolCall
		}
	}

	msg, err := stream.Wait()
	require.NoError(t, err)
	assert.Equal(t, ai.StopReasonToolUse, msg.StopReason)

	calls := msg.ToolCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, "get_weather", calls[0].Name)
	assert.NotEmpty(t, calls[0].ID)

	var in weatherInput
	require.NoError(t, json.Unmarshal(calls[0].Input, &in))
	assert.Contains(t, in.City, "Paris")

	require.NotNil(t, end)
	assert.Equal(t, calls[0], *end)
	assert.JSONEq(t, deltas.String(), string(end.Input))
}

func TestToolCallMultiTurn(t *testing.T) {
	p := newTestProvider(t)

	messages := []ai.Message{ai.UserMessage("What is the weather in Paris?")}
	opts := ai.JoinOptions(
		ai.SystemPrompt([]ai.Content{ai.Text{Text: "Use the get_weather tool to answer questions about the weather."}}),
		ai.Tools([]ai.ToolInfo{weatherTool.Info()}),
		ai.Thinking(ai.ThinkingLow),
	)

	first, err := p.StreamText(t.Context(), testModel, messages, opts).Wait()
	require.NoError(t, err)
	calls := first.ToolCalls()
	require.Len(t, calls, 1)

	result, err := weatherTool.Run(t.Context(), calls[0])
	require.NoError(t, err)

	messages = append(messages, *first)
	messages = append(messages, ai.ToolResultMessage(calls[0].ID, result.Content...))

	second, err := p.StreamText(t.Context(), testModel, messages, opts).Wait()
	require.NoError(t, err)
	assert.Equal(t, ai.StopReasonStop, second.StopReason)
	assert.Contains(t, second.Text(), "22")
}

func TestThinking(t *testing.T) {
	p := newTestProvider(t)

	stream := p.StreamText(
		t.Context(),
		testModel,
		[]ai.Message{ai.UserMessage("How many r's are in strawberry? Think it through.")},
		ai.Thinking(ai.ThinkingHigh),
	)

	var types []ai.EventType
	for ev, err := range stream.Events() {
		require.NoError(t, err)
		types = append(types, ev.Type)
	}

	msg, err := stream.Wait()
	require.NoError(t, err)

	assert.Contains(t, types, ai.EventThinkStart)
	assert.Contains(t, types, ai.EventThinkEnd)

	var thinking []ai.ThinkingBlock
	for _, c := range msg.Content {
		if th, ok := c.(ai.ThinkingBlock); ok {
			thinking = append(thinking, th)
		}
	}
	require.NotEmpty(t, thinking)
	assert.NotEmpty(t, thinking[0].Signature)
	assert.Contains(t, msg.Text(), "3")
}

func TestCacheReadUsage(t *testing.T) {
	p := newTestProvider(t)

	// The prefix must pass the minimum cacheable size of the model.
	system := "You are a helpful assistant. " + strings.Repeat("Answer in one short sentence. ", 1200)

	first, err := p.StreamText(
		t.Context(),
		testModel,
		[]ai.Message{ai.UserMessage("What is the capital of France?")},
		ai.SystemPrompt([]ai.Content{ai.Text{Text: system}}),
	).Wait()
	require.NoError(t, err)
	assert.NotZero(t, first.Usage.CacheWrite+first.Usage.CacheRead)

	second, err := p.StreamText(
		t.Context(),
		testModel,
		[]ai.Message{ai.UserMessage("What is the capital of Spain?")},
		ai.SystemPrompt([]ai.Content{ai.Text{Text: system}}),
	).Wait()
	require.NoError(t, err)
	assert.NotZero(t, second.Usage.CacheRead)
}

func TestCountTokens(t *testing.T) {
	p := newTestProvider(t)
	lm := ai.LanguageModel{Model: testModel, Provider: p}

	count, err := ai.CountTokens(
		t.Context(),
		lm,
		[]ai.Message{ai.UserMessage("Hello")},
		ai.SystemPrompt([]ai.Content{ai.Text{Text: "You are a helpful assistant."}}),
		ai.Tools([]ai.ToolInfo{weatherTool.Info()}),
	)
	require.NoError(t, err)
	assert.Greater(t, count.Total, 0)
}

// TestThinkingAfterSummary is a recorded call on a history that a
// compaction edited: an old turn becomes a summary, and a later turn
// keeps its thinking and its signature. The provider sends the drop_block
// control, so the API accepts the request and drops the thinking of that
// turn. The strict call shows what the control prevents. Run it with
// make record PKG=anthropic RUN=TestThinkingAfterSummary.
func TestThinkingAfterSummary(t *testing.T) {
	p := newTestProvider(t)
	model := modelstest.Model("anthropic", "claude-opus-5-5")
	opts := ai.JoinOptions(
		ai.SystemPrompt([]ai.Content{ai.Text{Text: "Always think before every tool call."}}),
		ai.Tools([]ai.ToolInfo{weatherTool.Info()}),
		ai.Thinking(ai.ThinkingHigh),
	)

	question := ai.UserMessage("Name one city in France.")
	oldAnswer, err := p.StreamText(t.Context(), model, []ai.Message{question}, opts).Wait()
	require.NoError(t, err)

	request := ai.UserMessage("First compute 982451653 multiplied by 961748941 in your head. " +
		"If the last digit is odd, call get_weather for Paris. If it is even, call it for Lyon.")
	turn, err := p.StreamText(
		t.Context(),
		model,
		[]ai.Message{question, *oldAnswer, request},
		opts,
	).Wait()
	require.NoError(t, err)
	calls := turn.ToolCalls()
	require.NotEmpty(t, calls)
	thinking := false
	for _, c := range turn.Content {
		if th, ok := c.(ai.ThinkingBlock); ok && th.Signature != "" {
			thinking = true
		}
	}
	require.True(t, thinking, "the kept turn must have signed thinking, or the test proves nothing")

	edited := []ai.Message{
		ai.UserMessage("A summary of the earlier conversation: the user asked for a city in France."),
		request,
		*turn,
		ai.ToolResultMessage(calls[0].ID, ai.Text{Text: "Sunny"}),
	}
	last, err := p.StreamText(t.Context(), model, edited, opts).Wait()
	require.NoError(t, err)
	assert.NotEmpty(t, last.Text())

	strict := anthropic.OnPayload(func(params *sdk.MessageNewParams) error {
		params.Thinking.OfAdaptive.SetExtraFields(map[string]any{
			"block_binding": map[string]any{"prefix_mismatch_behavior": "error"},
		})
		return nil
	})
	_, err = p.StreamText(t.Context(), model, edited, ai.JoinOptions(opts, strict)).Wait()
	require.Error(t, err, "the summary changes the prefix of the kept thinking")
	assert.Contains(t, err.Error(), "signature")
}
