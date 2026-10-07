package openaichat_test

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
	"github.com/sonnes/gopi/pkg/ai/openai"
	"github.com/sonnes/gopi/pkg/ai/openaichat"
	"github.com/sonnes/gopi/pkg/tool"
)

// testModel is the recorded model. The fixtures come from OpenRouter,
// which serves GPT-6 Luna under a vendor prefix. The provider passes the
// ID through, so the prefix is a test concern only.
var testModel = ai.Model{
	ID:        "openai/gpt-6-luna",
	Name:      openai.GPT6Luna.Name,
	MaxTokens: openai.GPT6Luna.MaxTokens,
}

// openModel is an open model with visible reasoning, served by
// OpenRouter. It sends its reasoning as text, and it needs the
// reasoning back between tool calls.
var openModel = ai.Model{
	ID:        "deepseek/deepseek-v4-flash",
	Name:      "DeepSeek V4 Flash",
	MaxTokens: 8192,
}

// fireworksModel is a model with interleaved thinking on Fireworks.
var fireworksModel = ai.Model{
	ID:        "accounts/fireworks/models/kimi-k2-thinking",
	Name:      "Kimi K2 Thinking",
	MaxTokens: 8192,
}

// fireworksBaseURL is the base URL of the Chat Completions API of
// Fireworks.
const fireworksBaseURL = "https://api.fireworks.ai/inference/v1"

// canonicalHost is the host in every fixture. The scrubber replaces the
// host and the path prefix of a recording, so a replay does not depend
// on the base URL that the recording used.
const canonicalHost = "api.openai.com"

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
	req.Header.Del("Authorization")
	req.Header.Del("Accept")
	req.Header.Del("OpenAI-Organization")
	req.Header.Del("OpenAI-Project")
	for key := range req.Header {
		if strings.HasPrefix(key, "X-Stainless-") {
			req.Header.Del(key)
		}
	}
	req.Header.Set("User-Agent", "Go-http-client/1.1")
	return nil
}

// server names the API that a test records from. If the key of the
// server is unset, the test records from the fallback.
type server struct {
	keyEnv  string
	baseURL string
	// fallback is the server to record from without keyEnv, or nil.
	fallback *server
}

// openRouter is the Chat Completions API of OpenRouter.
var openRouter = server{keyEnv: "OPENROUTER_API_KEY", baseURL: "https://openrouter.ai/api/v1"}

// compatible is the API of OPENAI_BASE_URL, OpenAI by default. Without
// OPENAI_API_KEY, the test records from OpenRouter.
var compatible = server{
	keyEnv:   "OPENAI_API_KEY",
	baseURL:  os.Getenv("OPENAI_BASE_URL"),
	fallback: &openRouter,
}

// fireworks is the API of Fireworks.
var fireworks = server{keyEnv: "FIREWORKS_API_KEY", baseURL: fireworksBaseURL}

// newTestProvider returns a provider that replays the fixture of the
// test. If -httprecord matches, it records against srv and needs the key
// of srv. A test with no fixture is skipped.
func newTestProvider(t *testing.T, srv server, opts ...openaichat.Option) *openaichat.Provider {
	t.Helper()

	trace := filepath.Join("testdata", strings.ReplaceAll(t.Name(), "/", "_")+".httprr")

	recording, err := httprr.Recording(trace)
	require.NoError(t, err)

	apiKey := "fake-api-key"
	var baseURL string
	if recording {
		if os.Getenv(srv.keyEnv) == "" && srv.fallback != nil {
			srv = *srv.fallback
		}
		apiKey = os.Getenv(srv.keyEnv)
		if apiKey == "" {
			t.Skipf("%s is not set", srv.keyEnv)
		}
		baseURL = srv.baseURL
	} else if _, err := os.Stat(trace); os.IsNotExist(err) {
		t.Skipf("fixture %s not found; run make record PKG=openaichat", trace)
	}

	rr, err := httprr.Open(trace, http.DefaultTransport)
	require.NoError(t, err)
	rr.ScrubReq(scrubRequest)
	t.Cleanup(func() { _ = rr.Close() })

	opts = append(opts,
		openaichat.APIKey(apiKey),
		openaichat.BaseURL(baseURL),
		openaichat.HTTPClient(rr.Client()),
	)
	return openaichat.New(opts...)
}

func TestStreamText(t *testing.T) {
	p := newTestProvider(t, compatible)

	stream := p.StreamText(
		t.Context(),
		testModel,
		[]ai.Message{ai.UserMessage("Say hi in Portuguese")},
		ai.JoinOptions(
			ai.SystemPrompt([]ai.Content{ai.Text{Text: "You are a helpful assistant. Be concise."}}),
			ai.Thinking(ai.ThinkingOff),
		),
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

	assert.Equal(t, testModel.ID, msg.Model)
	assert.Equal(t, ai.StopReasonStop, msg.StopReason)
	assert.NotZero(t, msg.Usage.Input)
	assert.NotZero(t, msg.Usage.Output)
}

// toolCallMultiTurn asks for the weather, runs the tool, and sends the
// result back. It returns the first response.
func toolCallMultiTurn(t *testing.T, p *openaichat.Provider, model ai.Model, opts ai.Options) *ai.Message {
	t.Helper()

	messages := []ai.Message{ai.UserMessage("What is the weather in Paris?")}
	opts = ai.JoinOptions(
		ai.SystemPrompt([]ai.Content{ai.Text{Text: "Use the get_weather tool to answer questions about the weather."}}),
		ai.Tools([]ai.ToolInfo{weatherTool.Info()}),
		opts,
	)

	first, err := p.StreamText(t.Context(), model, messages, opts).Wait()
	require.NoError(t, err)
	assert.Equal(t, ai.StopReasonToolUse, first.StopReason)

	calls := first.ToolCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, "get_weather", calls[0].Name)

	var in weatherInput
	require.NoError(t, json.Unmarshal(calls[0].Input, &in))
	assert.Contains(t, in.City, "Paris")

	result, err := weatherTool.Run(t.Context(), calls[0])
	require.NoError(t, err)

	messages = append(messages, *first)
	messages = append(messages, ai.ToolResultMessage(calls[0].ID, result.Content...))

	second, err := p.StreamText(t.Context(), model, messages, opts).Wait()
	require.NoError(t, err)
	assert.Equal(t, ai.StopReasonStop, second.StopReason)
	assert.Contains(t, second.Text(), "22")

	return first
}

func TestToolCallMultiTurn(t *testing.T) {
	p := newTestProvider(t, compatible)
	toolCallMultiTurn(t, p, testModel, ai.Thinking(ai.ThinkingLow))
}

// The open model streams its reasoning as text, and the second call
// sends it back as reasoning_content.
func TestReasoningToolCallMultiTurn(t *testing.T) {
	p := newTestProvider(t, compatible)
	first := toolCallMultiTurn(t, p, openModel, ai.Thinking(ai.ThinkingHigh))

	thinking, ok := first.Content[0].(ai.ThinkingBlock)
	require.True(t, ok, "the first block is %T, not a thinking block", first.Content[0])
	assert.NotEmpty(t, thinking.Thinking)
}

// Fireworks needs the reasoning of a model with interleaved thinking
// back between tool calls, and it rejects a field that it does not know.
func TestFireworksToolCallMultiTurn(t *testing.T) {
	p := newTestProvider(t, fireworks)
	first := toolCallMultiTurn(t, p, fireworksModel, ai.Thinking(ai.ThinkingHigh))

	_, ok := first.Content[0].(ai.ThinkingBlock)
	assert.True(t, ok, "the first block is %T, not a thinking block", first.Content[0])
}

func TestCacheBreakpoints(t *testing.T) {
	p := newTestProvider(t, compatible, openaichat.CacheBreakpoints())

	// The prefix must pass the minimum cacheable size of the model.
	system := "You are a helpful assistant. " + strings.Repeat("Answer in one short sentence. ", 1200)
	opts := ai.JoinOptions(
		ai.SystemPrompt([]ai.Content{ai.Text{Text: system}}),
		ai.Thinking(ai.ThinkingOff),
		openai.PromptCacheKey("gopi-test-cache"),
	)

	first, err := p.StreamText(
		t.Context(),
		testModel,
		[]ai.Message{ai.UserMessage("What is the capital of France?")},
		opts,
	).Wait()
	require.NoError(t, err)

	second, err := p.StreamText(
		t.Context(),
		testModel,
		[]ai.Message{ai.UserMessage("What is the capital of Spain?")},
		opts,
	).Wait()
	require.NoError(t, err)

	cached := first.Usage.CacheRead + first.Usage.CacheWrite + second.Usage.CacheRead
	assert.NotZero(t, cached)
}

func TestGenerateObject(t *testing.T) {
	p := newTestProvider(t, compatible)
	lm := ai.LanguageModel{Model: testModel, Provider: p}

	res, err := ai.GenerateObject[person](
		t.Context(),
		lm,
		[]ai.Message{ai.UserMessage("Describe Alice, who is 30 and lives in Lisbon.")},
		ai.Thinking(ai.ThinkingOff),
	)
	require.NoError(t, err)

	assert.Equal(t, "Alice", res.Object.Name)
	assert.Equal(t, 30, res.Object.Age)
	assert.Equal(t, "Lisbon", res.Object.City)
	assert.NotZero(t, res.Usage.Output)
}
