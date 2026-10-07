package openai_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/ai/openai"
)

// sseProvider returns a provider whose server answers every request with
// the given events as a server-sent event stream.
func sseProvider(t *testing.T, events ...string) *openai.Provider {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, ev := range events {
			fmt.Fprintf(w, "data: %s\n\n", ev)
		}
	}))
	t.Cleanup(srv.Close)

	return openai.New(
		openai.APIKey("test-key"),
		openai.BaseURL(srv.URL),
	)
}

// run streams one call and returns the events and the final message.
func run(t *testing.T, p *openai.Provider) ([]ai.Event, *ai.Message, error) {
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

const usageJSON = `{"input_tokens":100,"input_tokens_details":{"cached_tokens":20,"cache_write_tokens":30},` +
	`"output_tokens":7,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":107}`

func TestStream_Text(t *testing.T) {
	p := sseProvider(t,
		`{"type":"response.created","response":{"id":"resp_1","status":"in_progress","output":[]}}`,
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg_1","role":"assistant","content":[]}}`,
		`{"type":"response.content_part.added","output_index":0,"content_index":0,"part":{"type":"output_text","text":""}}`,
		`{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"Olá"}`,
		`{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"!"}`,
		`{"type":"response.output_text.done","output_index":0,"content_index":0,"text":"Olá!"}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"Olá!"}]}}`,
		`{"type":"response.completed","response":{"id":"resp_1","status":"completed",`+
			`"output":[{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"Olá!"}]}],`+
			`"usage":`+usageJSON+`}}`,
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

	assert.Equal(t, ai.RoleAssistant, msg.Role)
	assert.Equal(t, openai.GPT6Luna.ID, msg.Model)
	assert.Equal(t, "Olá!", msg.Text())
	assert.Equal(t, ai.StopReasonStop, msg.StopReason)
	usage := ai.Usage{Input: 50, Output: 7, CacheRead: 20, CacheWrite: 30}
	usage.Cost = openai.GPT6Luna.Pricing.Cost(usage)
	assert.NotZero(t, usage.Cost.Total(), "the model has prices")
	assert.Equal(t, usage, msg.Usage)
}

func TestStream_ReasoningAndParallelToolCalls(t *testing.T) {
	reasoning := `{"type":"reasoning","id":"rs_1","encrypted_content":"enc",` +
		`"summary":[{"type":"summary_text","text":"First."},{"type":"summary_text","text":"Second."}]}`
	call1 := `{"type":"function_call","id":"fc_1","call_id":"call_1","name":"get_weather","arguments":"{\"city\":\"Paris\"}"}`
	call2 := `{"type":"function_call","id":"fc_2","call_id":"call_2","name":"get_weather","arguments":"{\"city\":\"Rome\"}"}`

	p := sseProvider(t,
		`{"type":"response.created","response":{"id":"resp_1","status":"in_progress","output":[]}}`,
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"rs_1","summary":[]}}`,
		`{"type":"response.reasoning_summary_text.delta","output_index":0,"summary_index":0,"delta":"First."}`,
		`{"type":"response.reasoning_summary_text.delta","output_index":0,"summary_index":1,"delta":"Second."}`,
		`{"type":"response.output_item.done","output_index":0,"item":`+reasoning+`}`,
		`{"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"get_weather","arguments":""}}`,
		`{"type":"response.function_call_arguments.delta","output_index":1,"delta":"{\"city\":\"Paris\"}"}`,
		`{"type":"response.output_item.done","output_index":1,"item":`+call1+`}`,
		`{"type":"response.output_item.added","output_index":2,"item":{"type":"function_call","id":"fc_2","call_id":"call_2","name":"get_weather","arguments":""}}`,
		`{"type":"response.function_call_arguments.delta","output_index":2,"delta":"{\"city\":\"Rome\"}"}`,
		`{"type":"response.output_item.done","output_index":2,"item":`+call2+`}`,
		`{"type":"response.completed","response":{"id":"resp_1","status":"completed",`+
			`"output":[`+reasoning+`,`+call1+`,`+call2+`],"usage":`+usageJSON+`}}`,
	)

	events, msg, err := run(t, p)
	require.NoError(t, err)

	assert.Equal(t, []ai.EventType{
		ai.EventStart,
		ai.EventThinkStart,
		ai.EventThinkDelta,
		ai.EventThinkDelta,
		ai.EventThinkEnd,
		ai.EventToolStart,
		ai.EventToolDelta,
		ai.EventToolEnd,
		ai.EventToolStart,
		ai.EventToolDelta,
		ai.EventToolEnd,
	}, types(events))

	// Each event carries the index of its block in the final message.
	var thinking strings.Builder
	for _, ev := range events[1:] {
		switch ev.Type {
		case ai.EventThinkStart, ai.EventThinkDelta, ai.EventThinkEnd:
			assert.Equal(t, 0, ev.ContentIndex)
			thinking.WriteString(ev.Delta)
		}
	}
	assert.Equal(t, 1, events[5].ContentIndex)
	assert.Equal(t, 2, events[8].ContentIndex)

	require.Len(t, msg.Content, 3)
	block := msg.Content[0].(ai.ThinkingBlock)
	assert.Equal(t, "First.\n\nSecond.", block.Thinking)
	assert.Equal(t, block.Thinking, thinking.String())
	assert.NotEmpty(t, block.Signature)

	calls := msg.ToolCalls()
	require.Len(t, calls, 2)
	assert.Equal(t, "call_1", calls[0].ID)
	assert.JSONEq(t, `{"city":"Paris"}`, string(calls[0].Input))
	assert.Equal(t, "call_2", calls[1].ID)
	assert.Equal(t, calls[0], *events[7].ToolCall)
	assert.Equal(t, calls[1], *events[10].ToolCall)

	assert.Equal(t, ai.StopReasonToolUse, msg.StopReason)
}

func TestStream_ReasoningRoundTrip(t *testing.T) {
	reasoning := `{"type":"reasoning","id":"rs_1","encrypted_content":"enc","summary":[]}`
	var body map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: %s\n\n",
			`{"type":"response.completed","response":{"status":"completed","output":[`+reasoning+`]}}`)
	}))
	t.Cleanup(srv.Close)
	p := openai.New(openai.APIKey("test-key"), openai.BaseURL(srv.URL))

	first, err := p.StreamText(t.Context(), openai.GPT6Luna, []ai.Message{ai.UserMessage("Hi")}, nil).Wait()
	require.NoError(t, err)

	_, err = p.StreamText(t.Context(), openai.GPT6Luna, []ai.Message{ai.UserMessage("Hi"), *first}, nil).Wait()
	require.NoError(t, err)

	items := body["input"].([]any)
	require.Len(t, items, 2)
	item := items[1].(map[string]any)
	assert.Equal(t, "reasoning", item["type"])
	assert.Equal(t, "rs_1", item["id"])
	assert.Equal(t, "enc", item["encrypted_content"])
}

func TestStream_Incomplete(t *testing.T) {
	p := sseProvider(t,
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg_1","role":"assistant","content":[]}}`,
		`{"type":"response.content_part.added","output_index":0,"content_index":0,"part":{"type":"output_text","text":""}}`,
		`{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"Once upon"}`,
		`{"type":"response.incomplete","response":{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},`+
			`"output":[{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"Once upon"}]}],`+
			`"usage":`+usageJSON+`}}`,
	)

	_, msg, err := run(t, p)
	require.NoError(t, err)

	assert.Equal(t, ai.StopReasonLength, msg.StopReason)
	assert.Equal(t, "Once upon", msg.Text())
	assert.Equal(t, 7, msg.Usage.Output)
}

func TestStream_Refusal(t *testing.T) {
	p := sseProvider(t,
		`{"type":"response.completed","response":{"status":"completed",`+
			`"output":[{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"refusal","refusal":"I can't help with that."}]}]}}`,
	)

	_, msg, err := run(t, p)
	require.NoError(t, err)

	assert.Equal(t, ai.StopReasonRefusal, msg.StopReason)
	assert.Equal(t, "I can't help with that.", msg.Text())
	assert.Equal(t, "I can't help with that.", msg.StopDetail)
}

func TestStream_ContentFilter(t *testing.T) {
	p := sseProvider(t,
		`{"type":"response.incomplete","response":{"status":"incomplete","incomplete_details":{"reason":"content_filter"},"output":[]}}`,
	)

	_, msg, err := run(t, p)
	require.NoError(t, err)
	assert.Equal(t, ai.StopReasonRefusal, msg.StopReason)
	assert.Equal(t, "content_filter", msg.StopDetail)
}

func TestStream_Errors(t *testing.T) {
	tests := []struct {
		name   string
		events []string
		want   string
	}{
		{
			name: "failed",
			events: []string{
				`{"type":"response.failed","response":{"status":"failed","error":{"code":"server_error","message":"boom"}}}`,
			},
			want: "boom",
		},
		{
			name:   "error event",
			events: []string{`{"type":"error","code":"rate_limit_exceeded","message":"slow down"}`},
			want:   "slow down",
		},
		{
			name: "incomplete for another reason",
			events: []string{
				`{"type":"response.incomplete","response":{"status":"incomplete","incomplete_details":{"reason":"other"},"output":[]}}`,
			},
			want: "other",
		},
		{
			name: "cancelled",
			events: []string{
				`{"type":"response.completed","response":{"status":"cancelled","output":[]}}`,
			},
			want: "cancelled",
		},
		{
			name: "no terminal event",
			events: []string{
				`{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"Hi"}`,
			},
			want: "ended",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := run(t, sseProvider(t, tt.events...))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

// OpenRouter sends content_part.delta and reasoning.delta, and it can
// skip the added and done events. The terminal event closes each open
// block.
func TestStream_OpenRouterEventNames(t *testing.T) {
	p := sseProvider(t,
		`{"type":"response.reasoning.delta","output_index":0,"delta":"Think."}`,
		`{"type":"response.content_part.delta","output_index":1,"content_index":0,"delta":"Hi"}`,
		`{"type":"response.completed","response":{"status":"completed","output":[`+
			`{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"Think."}]},`+
			`{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"Hi"}]}]}}`,
	)

	events, msg, err := run(t, p)
	require.NoError(t, err)

	assert.Equal(t, []ai.EventType{
		ai.EventStart,
		ai.EventThinkStart,
		ai.EventThinkDelta,
		ai.EventTextStart,
		ai.EventTextDelta,
		ai.EventThinkEnd,
		ai.EventTextEnd,
	}, types(events))
	assert.Equal(t, 1, events[3].ContentIndex)
	assert.Equal(t, 0, events[5].ContentIndex)
	assert.Equal(t, "Hi", events[6].Content)
	assert.Equal(t, "Hi", msg.Text())
}

// sequence answers each request with the next handler, and repeats the
// last one. It keeps the last request.
type sequence struct {
	handlers []http.HandlerFunc
	calls    atomic.Int32
	last     *http.Request
	body     []byte
}

func (s *sequence) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	n := int(s.calls.Add(1)) - 1
	s.last = r
	s.body, _ = io.ReadAll(r.Body)
	s.handlers[min(n, len(s.handlers)-1)](w, r)
}

// completed answers with a short completed stream.
func completed(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprintf(w, "data: %s\n\n", `{"type":"response.completed","response":{"status":"completed",`+
		`"output":[{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"Hi"}]}]}}`)
}

// failing answers with an API error.
func failing(code int, headers map[string]string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		for k, v := range headers {
			w.Header().Set(k, v)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		fmt.Fprint(w, `{"error":{"message":"busy","type":"server_error"}}`)
	}
}

func sequenceProvider(t *testing.T, s *sequence) *openai.Provider {
	t.Helper()
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)
	return openai.New(
		openai.APIKey("test-key"),
		openai.BaseURL(srv.URL),
	)
}

func TestStreamText_Retry(t *testing.T) {
	s := &sequence{handlers: []http.HandlerFunc{
		failing(http.StatusTooManyRequests, map[string]string{"retry-after-ms": "1"}),
		completed,
	}}
	p := sequenceProvider(t, s)

	msg, err := p.StreamText(t.Context(), openai.GPT6Luna, []ai.Message{ai.UserMessage("Hi")}, ai.MaxRetries(1)).Wait()
	require.NoError(t, err)
	assert.Equal(t, "Hi", msg.Text())
	assert.Equal(t, int32(2), s.calls.Load())
}

func TestStreamText_NoRetryByDefault(t *testing.T) {
	s := &sequence{handlers: []http.HandlerFunc{failing(http.StatusTooManyRequests, nil), completed}}
	p := sequenceProvider(t, s)

	_, err := p.StreamText(t.Context(), openai.GPT6Luna, []ai.Message{ai.UserMessage("Hi")}, nil).Wait()
	require.Error(t, err)
	assert.Equal(t, int32(1), s.calls.Load())
}

func TestStreamText_HeadersAndPayload(t *testing.T) {
	s := &sequence{handlers: []http.HandlerFunc{completed}}
	p := sequenceProvider(t, s)

	hook := openai.OnPayload(func(params *responses.ResponseNewParams) error {
		params.MaxOutputTokens = param.NewOpt[int64](7)
		return nil
	})
	_, err := p.StreamText(
		t.Context(),
		openai.GPT6Luna,
		[]ai.Message{ai.UserMessage("Hi")},
		ai.JoinOptions(ai.Headers(map[string]string{"X-Team": "infra"}), hook),
	).Wait()
	require.NoError(t, err)
	assert.Equal(t, "infra", s.last.Header.Get("X-Team"))

	var body map[string]any
	require.NoError(t, json.Unmarshal(s.body, &body))
	assert.Equal(t, 7.0, body["max_output_tokens"], "the hook changes the request")
}
