package openaichat_test

import (
	"sync/atomic"

	"encoding/json"
	"fmt"
	sdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/param"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/ai/openai"
	"github.com/sonnes/gopi/pkg/ai/openaichat"
)

// sseProvider returns a provider whose server answers every request with
// the given chunks as a server-sent event stream. body gets the decoded
// request.
func sseProvider(t *testing.T, body *map[string]any, chunks ...string) *openaichat.Provider {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if body != nil {
			_ = json.NewDecoder(r.Body).Decode(body)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, c := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", c)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)

	return openaichat.New(
		openaichat.APIKey("test-key"),
		openaichat.BaseURL(srv.URL),
	)
}

// run streams one call and returns the events and the final message.
func run(t *testing.T, p *openaichat.Provider) ([]ai.Event, *ai.Message, error) {
	t.Helper()

	stream := p.StreamText(
		t.Context(),
		openai.GPT6Luna,
		[]ai.Message{ai.UserMessage("Hi")},
		nil,
	)

	var events []ai.Event
	for ev, err := range stream.Events() {
		if err != nil {
			break
		}
		events = append(events, ev)
	}

	msg, err := stream.Wait()
	return events, msg, err
}

func types(events []ai.Event) []ai.EventType {
	out := make([]ai.EventType, 0, len(events))
	for _, ev := range events {
		out = append(out, ev.Type)
	}
	return out
}

// chunk returns a chunk with one choice.
func chunk(delta, finish string) string {
	reason := "null"
	if finish != "" {
		reason = `"` + finish + `"`
	}
	return `{"id":"c1","object":"chat.completion.chunk","created":1,"model":"m",` +
		`"choices":[{"index":0,"delta":` + delta + `,"finish_reason":` + reason + `}]}`
}

const usageChunk = `{"id":"c1","object":"chat.completion.chunk","created":1,"model":"m","choices":[],` +
	`"usage":{"prompt_tokens":100,"completion_tokens":7,"total_tokens":107,` +
	`"prompt_tokens_details":{"cached_tokens":20,"cache_write_tokens":30}}}`

func TestStream_Text(t *testing.T) {
	var body map[string]any
	p := sseProvider(t, &body,
		chunk(`{"role":"assistant","content":""}`, ""),
		chunk(`{"content":"Olá"}`, ""),
		chunk(`{"content":"!"}`, ""),
		chunk(`{}`, "stop"),
		usageChunk,
	)

	events, msg, err := run(t, p)
	require.NoError(t, err)

	assert.Equal(t, []ai.EventType{
		ai.EventStart,
		ai.EventTextStart,
		ai.EventTextDelta,
		ai.EventTextDelta,
		ai.EventTextEnd,
	}, types(events))
	assert.Equal(t, "Olá!", events[4].Content)

	assert.Equal(t, "Olá!", msg.Text())
	assert.Equal(t, openai.GPT6Luna.ID, msg.Model)
	assert.Equal(t, ai.StopReasonStop, msg.StopReason)
	usage := ai.Usage{Input: 50, Output: 7, CacheRead: 20, CacheWrite: 30}
	usage.Cost = openai.GPT6Luna.Pricing.Cost(usage)
	assert.NotZero(t, usage.Cost.Total(), "the model has prices")
	assert.Equal(t, usage, msg.Usage)

	assert.Equal(t, true, body["stream"])
	assert.Equal(t, map[string]any{"include_usage": true}, body["stream_options"])
}

// Fireworks sends reasoning_content and OpenRouter sends reasoning.
func TestStream_Reasoning(t *testing.T) {
	tests := []struct {
		name  string
		field string
	}{
		{name: "fireworks", field: "reasoning_content"},
		{name: "openrouter", field: "reasoning"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := sseProvider(t, nil,
				chunk(`{"role":"assistant","`+tt.field+`":"Think"}`, ""),
				chunk(`{"`+tt.field+`":"ing."}`, ""),
				chunk(`{"content":"Hi"}`, ""),
				chunk(`{}`, "stop"),
			)

			events, msg, err := run(t, p)
			require.NoError(t, err)

			assert.Equal(t, []ai.EventType{
				ai.EventStart,
				ai.EventThinkStart,
				ai.EventThinkDelta,
				ai.EventThinkDelta,
				ai.EventThinkEnd,
				ai.EventTextStart,
				ai.EventTextDelta,
				ai.EventTextEnd,
			}, types(events))
			assert.Equal(t, 1, events[5].ContentIndex)

			require.Len(t, msg.Content, 2)
			assert.Equal(t, ai.ThinkingBlock{Thinking: "Thinking."}, msg.Content[0])
			assert.Equal(t, "Hi", msg.Text())
		})
	}
}

func TestStream_ParallelToolCalls(t *testing.T) {
	p := sseProvider(t, nil,
		chunk(`{"role":"assistant","content":"Checking."}`, ""),
		chunk(`{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"get_weather","arguments":""}}]}`, ""),
		chunk(`{"tool_calls":[{"index":0,"function":{"arguments":"{\"city\":"}}]}`, ""),
		chunk(`{"tool_calls":[{"index":0,"function":{"arguments":"\"Paris\"}"}}]}`, ""),
		chunk(`{"tool_calls":[{"index":1,"id":"call_2","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Rome\"}"}}]}`, ""),
		chunk(`{}`, "tool_calls"),
	)

	events, msg, err := run(t, p)
	require.NoError(t, err)

	assert.Equal(t, []ai.EventType{
		ai.EventStart,
		ai.EventTextStart,
		ai.EventTextDelta,
		ai.EventTextEnd,
		ai.EventToolStart,
		ai.EventToolDelta,
		ai.EventToolDelta,
		ai.EventToolEnd,
		ai.EventToolStart,
		ai.EventToolDelta,
		ai.EventToolEnd,
	}, types(events))
	assert.Equal(t, 1, events[4].ContentIndex)
	assert.Equal(t, 2, events[8].ContentIndex)

	calls := msg.ToolCalls()
	require.Len(t, calls, 2)
	assert.Equal(t, "call_1", calls[0].ID)
	assert.JSONEq(t, `{"city":"Paris"}`, string(calls[0].Input))
	assert.Equal(t, "call_2", calls[1].ID)
	assert.Equal(t, calls[0], *events[7].ToolCall)
	assert.Equal(t, calls[1], *events[10].ToolCall)
	assert.Equal(t, ai.StopReasonToolUse, msg.StopReason)
}

func TestStream_StopReasons(t *testing.T) {
	tests := []struct {
		finish     string
		want       ai.StopReason
		wantDetail string
	}{
		{finish: "stop", want: ai.StopReasonStop},
		{finish: "length", want: ai.StopReasonLength},
		{finish: "content_filter", want: ai.StopReasonRefusal, wantDetail: "content_filter"},
	}

	for _, tt := range tests {
		t.Run(tt.finish, func(t *testing.T) {
			p := sseProvider(t, nil,
				chunk(`{"content":"Hi"}`, ""),
				chunk(`{}`, tt.finish),
			)

			_, msg, err := run(t, p)
			require.NoError(t, err)
			assert.Equal(t, tt.want, msg.StopReason)
			assert.Equal(t, tt.wantDetail, msg.StopDetail)
		})
	}
}

func TestStream_Refusal(t *testing.T) {
	p := sseProvider(t, nil,
		chunk(`{"refusal":"I can't help."}`, ""),
		chunk(`{}`, "stop"),
	)

	_, msg, err := run(t, p)
	require.NoError(t, err)
	assert.Equal(t, ai.StopReasonRefusal, msg.StopReason)
	assert.Equal(t, "I can't help.", msg.StopDetail)
}

func TestStream_StopErrors(t *testing.T) {
	tests := []struct {
		name   string
		finish string
		want   string
	}{
		{name: "no finish reason", finish: "", want: "no finish reason"},
		{name: "unknown", finish: "network_error", want: "network_error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := sseProvider(t, nil, chunk(`{"content":"Hi"}`, tt.finish))

			_, _, err := run(t, p)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

// OpenRouter repeats the finish reason on the usage chunk.
func TestStream_UsageOnFinishChunk(t *testing.T) {
	usage := `{"id":"c1","object":"chat.completion.chunk","created":1,"model":"m",` +
		`"choices":[{"index":0,"delta":{},"finish_reason":"stop"}],` +
		`"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}`
	p := sseProvider(t, nil,
		chunk(`{"content":"Hi"}`, ""),
		chunk(`{}`, "stop"),
		usage,
	)

	events, msg, err := run(t, p)
	require.NoError(t, err)

	assert.Len(t, events, 4)
	want := ai.Usage{Input: 10, Output: 2}
	want.Cost = openai.GPT6Luna.Pricing.Cost(want)
	assert.Equal(t, want, msg.Usage)
}

// OpenRouter reports an error after the stream starts as a chunk with an
// error field and the finish reason error.
func TestStream_ErrorChunk(t *testing.T) {
	errChunk := `{"id":"c1","object":"chat.completion.chunk","created":1,"model":"m",` +
		`"error":{"code":502,"message":"Provider disconnected"},` +
		`"choices":[{"index":0,"delta":{"content":""},"finish_reason":"error"}]}`
	p := sseProvider(t, nil,
		chunk(`{"content":"Hi"}`, ""),
		errChunk,
	)

	_, _, err := run(t, p)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Provider disconnected")
}

func TestStream_ReasoningRoundTrip(t *testing.T) {
	var body map[string]any
	p := sseProvider(t, &body,
		chunk(`{"reasoning_content":"Think."}`, ""),
		chunk(`{"content":"Hi"}`, ""),
		chunk(`{}`, "stop"),
	)

	first, err := p.StreamText(t.Context(), openai.GPT6Luna, []ai.Message{ai.UserMessage("Hi")}, nil).Wait()
	require.NoError(t, err)

	_, err = p.StreamText(t.Context(), openai.GPT6Luna, []ai.Message{ai.UserMessage("Hi"), *first}, nil).Wait()
	require.NoError(t, err)

	sent := body["messages"].([]any)
	require.Len(t, sent, 2)
	assert.Equal(t, "Think.", sent[1].(map[string]any)["reasoning_content"])
}

func TestStreamText_RetryHeadersAndPayload(t *testing.T) {
	var calls atomic.Int32
	var last *http.Request
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		last = r
		_ = json.NewDecoder(r.Body).Decode(&body)
		if calls.Add(1) == 1 {
			w.Header().Set("retry-after-ms", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"error":{"message":"busy"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: %s\n\n", chunk(`{"content":"Hi"}`, "stop"))
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)
	p := openaichat.New(openaichat.APIKey("test-key"), openaichat.BaseURL(srv.URL))

	hook := openaichat.OnPayload(func(params *sdk.ChatCompletionNewParams) error {
		params.MaxCompletionTokens = param.NewOpt[int64](7)
		return nil
	})
	msg, err := p.StreamText(
		t.Context(),
		openai.GPT6Luna,
		[]ai.Message{ai.UserMessage("Hi")},
		ai.JoinOptions(ai.MaxRetries(1), ai.Headers(map[string]string{"X-Team": "infra"}), hook),
	).Wait()
	require.NoError(t, err)

	assert.Equal(t, "Hi", msg.Text())
	assert.Equal(t, int32(2), calls.Load(), "one retry")
	assert.Equal(t, "infra", last.Header.Get("X-Team"))
	assert.Equal(t, 7.0, body["max_completion_tokens"], "the hook changes the request")
}
