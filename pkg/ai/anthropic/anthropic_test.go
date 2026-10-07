package anthropic_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/internal/httprr"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/ai/anthropic"
	"github.com/sonnes/gopi/pkg/tool"
)

// testModel is the model of the recorded tests.
var testModel = anthropic.Sonnet5

// canonicalHost is the host in every fixture. The scrubber replaces the
// host and the path prefix of a recording, so a replay does not depend
// on the base URL that the recording used.
const canonicalHost = "api.anthropic.com"

type weatherInput struct {
	City string `json:"city" jsonschema:"The city name"`
}

type person struct {
	Name string `json:"name" jsonschema:"The name of the person"`
	Age  int    `json:"age" jsonschema:"The age of the person"`
	City string `json:"city" jsonschema:"The city where the person lives"`
}

var weatherTool = tool.New("get_weather", func(_ context.Context, in weatherInput) (string, error) {
	return "Sunny, 22C in " + in.City, nil
}, tool.Description("Get the current weather in a city"))

// scrubRequest removes the secrets and the headers that change between
// runs, so a replay matches a recording.
func scrubRequest(req *http.Request) error {
	req.URL.Host = canonicalHost
	req.Host = canonicalHost
	if i := strings.Index(req.URL.Path, "/v1/"); i > 0 {
		req.URL.Path = req.URL.Path[i:]
	}
	req.Header.Del("x-api-key")
	req.Header.Del("Authorization")
	req.Header.Del("Accept")
	req.Header.Del("Anthropic-Version")
	for key := range req.Header {
		if strings.HasPrefix(key, "X-Stainless-") {
			req.Header.Del(key)
		}
	}
	req.Header.Set("User-Agent", "Go-http-client/1.1")
	return nil
}

// openRouterURL is the base URL of the Messages API of OpenRouter.
const openRouterURL = "https://openrouter.ai/api"

// newTestProvider returns a provider that replays the fixture of the
// test. If -httprecord matches, it records against a real API. It uses
// ANTHROPIC_API_KEY and ANTHROPIC_BASE_URL if they are set. Else it uses
// OPENROUTER_API_KEY and the API of OpenRouter. A test with no fixture
// is skipped.
func newTestProvider(t *testing.T) *anthropic.Provider {
	t.Helper()

	trace := filepath.Join("testdata", strings.ReplaceAll(t.Name(), "/", "_")+".httprr")

	recording, err := httprr.Recording(trace)
	require.NoError(t, err)

	apiKey := "fake-api-key"
	var baseURL string
	if recording {
		apiKey = os.Getenv("ANTHROPIC_API_KEY")
		baseURL = os.Getenv("ANTHROPIC_BASE_URL")
		if apiKey == "" {
			apiKey = os.Getenv("OPENROUTER_API_KEY")
			baseURL = openRouterURL
		}
		if apiKey == "" {
			t.Skip("neither ANTHROPIC_API_KEY nor OPENROUTER_API_KEY is set")
		}
	} else if _, err := os.Stat(trace); os.IsNotExist(err) {
		t.Skipf("fixture %s not found; run make record", trace)
	}

	rr, err := httprr.Open(trace, http.DefaultTransport)
	require.NoError(t, err)
	rr.ScrubReq(scrubRequest)
	t.Cleanup(func() { _ = rr.Close() })

	return anthropic.New(
		anthropic.APIKey(apiKey),
		anthropic.BaseURL(baseURL),
		anthropic.HTTPClient(rr.Client()),
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

func TestGenerateObject(t *testing.T) {
	p := newTestProvider(t)
	lm := ai.LanguageModel{Model: testModel, Provider: p}

	res, err := ai.GenerateObject[person](
		t.Context(),
		lm,
		[]ai.Message{ai.UserMessage("Describe Alice, who is 30 and lives in Lisbon.")},
	)
	require.NoError(t, err)

	assert.Equal(t, "Alice", res.Object.Name)
	assert.Equal(t, 30, res.Object.Age)
	assert.Equal(t, "Lisbon", res.Object.City)
	assert.NotZero(t, res.Usage.Output)
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
