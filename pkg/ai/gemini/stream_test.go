package gemini_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/internal/providertest"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/ai/gemini"
)

// sse answers a request with the events as a server-sent event stream,
// then the end marker of the API.
func sse(events ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, ev := range events {
			fmt.Fprintf(w, "data: %s\n\n", ev)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}
}

// completed is a stream of the text "Hi".
var completed = sse(
	`{"event_type":"interaction.created","interaction":{"id":"i1","status":"in_progress"}}`,
	`{"event_type":"step.start","index":0,"step":{"type":"model_output"}}`,
	`{"event_type":"step.delta","index":0,"delta":{"type":"text","text":"Hi"}}`,
	`{"event_type":"step.stop","index":0}`,
	`{"event_type":"interaction.completed","interaction":{"id":"i1","status":"completed","usage":{"total_input_tokens":3,"total_output_tokens":1,"total_tokens":4}}}`,
)

// fakeProvider returns a provider for a server that answers each
// request with handler. The returned function gives the body of the
// last request.
func fakeProvider(t *testing.T, handler http.HandlerFunc) (*gemini.Provider, func() []byte) {
	t.Helper()

	var (
		mu   sync.Mutex
		body []byte
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		mu.Lock()
		body = data
		mu.Unlock()
		handler(w, r)
	}))
	t.Cleanup(srv.Close)

	p := gemini.New(
		gemini.APIKey("test-key"),
		gemini.BaseURL(srv.URL),
	)
	lastBody := func() []byte {
		mu.Lock()
		defer mu.Unlock()
		return body
	}
	return p, lastBody
}

// collect streams one call and returns the events and the final
// message. The error of the events is the error of Wait.
func collect(t *testing.T, stream *ai.EventStream) ([]ai.Event, *ai.Message, error) {
	t.Helper()

	var events []ai.Event
	var eventsErr error
	for ev, err := range stream.Events() {
		if err != nil {
			eventsErr = err
			break
		}
		events = append(events, ev)
	}
	msg, err := stream.Wait()
	assert.Equal(t, err, eventsErr, "the events and Wait return different errors")
	return events, msg, err
}

var fakeModel = ai.Model{
	ID: "gemini-3.8-flash",
	Pricing: ai.Pricing{
		Input:     1,
		Output:    2,
		CacheRead: 0.5,
	},
}

func TestRunFake(t *testing.T) {
	providertest.RunFake(t, providertest.Fake{
		New: func(baseURL string) ai.TextProvider {
			return gemini.New(
				gemini.APIKey("test-key"),
				gemini.BaseURL(baseURL),
			)
		},
		Text:   completed,
		Object: answer(objectBody),
		Decide: answer(decisionBody),
	})
}

func TestStreamText_Text(t *testing.T) {
	p, _ := fakeProvider(t, completed)

	events, msg, err := collect(t, p.StreamText(t.Context(), fakeModel, []ai.Message{ai.UserMessage("Hi")}, nil))
	require.NoError(t, err)

	assert.Equal(t, []ai.Event{
		{Type: ai.EventStart},
		{Type: ai.EventTextStart},
		{Type: ai.EventTextDelta, Delta: "Hi"},
		{Type: ai.EventTextEnd, Content: "Hi"},
	}, events)
	assert.Equal(t, ai.RoleAssistant, msg.Role)
	assert.Equal(t, "gemini-3.8-flash", msg.Model)
	assert.Equal(t, []ai.Content{ai.Text{Text: "Hi"}}, msg.Content)
	assert.Equal(t, ai.StopReasonStop, msg.StopReason)
}

func TestStreamText_ThoughtAndToolCall(t *testing.T) {
	p, _ := fakeProvider(t, sse(
		`{"event_type":"interaction.created","interaction":{"id":"i1","status":"in_progress"}}`,
		`{"event_type":"step.start","index":0,"step":{"type":"thought"}}`,
		`{"event_type":"step.delta","index":0,"delta":{"type":"thought_summary","content":{"type":"text","text":"Look up "}}}`,
		`{"event_type":"step.delta","index":0,"delta":{"type":"thought_summary","content":{"type":"text","text":"the weather."}}}`,
		`{"event_type":"step.delta","index":0,"delta":{"type":"thought_signature","signature":"sig-1"}}`,
		`{"event_type":"step.stop","index":0}`,
		`{"event_type":"step.start","index":1,"step":{"type":"function_call","id":"call-1","name":"get_weather","arguments":{}}}`,
		`{"event_type":"step.delta","index":1,"delta":{"type":"arguments_delta","arguments":"{\"city\":"}}`,
		`{"event_type":"step.delta","index":1,"delta":{"type":"arguments_delta","arguments":"\"Paris\"}"}}`,
		`{"event_type":"step.stop","index":1}`,
		`{"event_type":"interaction.completed","interaction":{"id":"i1","status":"requires_action","usage":{"total_input_tokens":100,"total_cached_tokens":40,"total_output_tokens":10,"total_thought_tokens":20,"total_tokens":130}}}`,
	))

	events, msg, err := collect(t, p.StreamText(t.Context(), fakeModel, []ai.Message{ai.UserMessage("Weather in Paris?")}, nil))
	require.NoError(t, err)

	call := ai.ToolCall{
		ID:    "call-1",
		Name:  "get_weather",
		Input: json.RawMessage(`{"city":"Paris"}`),
	}
	assert.Equal(t, []ai.Event{
		{Type: ai.EventStart},
		{Type: ai.EventThinkStart},
		{Type: ai.EventThinkDelta, Delta: "Look up "},
		{Type: ai.EventThinkDelta, Delta: "the weather."},
		{Type: ai.EventThinkEnd},
		{Type: ai.EventToolStart, ContentIndex: 1},
		{Type: ai.EventToolDelta, ContentIndex: 1, Delta: `{"city":`},
		{Type: ai.EventToolDelta, ContentIndex: 1, Delta: `"Paris"}`},
		{Type: ai.EventToolEnd, ContentIndex: 1, ToolCall: &call},
	}, events)

	assert.Equal(t, []ai.Content{
		ai.ThinkingBlock{
			Thinking:  "Look up the weather.",
			Signature: `{"gemini":"sig-1"}`,
		},
		call,
	}, msg.Content)
	assert.Equal(t, ai.StopReasonToolUse, msg.StopReason)

	wantUsage := ai.Usage{
		Input:     60,
		CacheRead: 40,
		Output:    30,
	}
	wantUsage.Cost = fakeModel.Pricing.Cost(wantUsage)
	assert.Equal(t, wantUsage, msg.Usage)
}

func TestStreamText_ArgumentsInStart(t *testing.T) {
	p, _ := fakeProvider(t, sse(
		`{"event_type":"step.start","index":0,"step":{"type":"function_call","id":"call-1","name":"list","arguments":{"dir":"."}}}`,
		`{"event_type":"step.stop","index":0}`,
		`{"event_type":"interaction.completed","interaction":{"id":"i1","status":"requires_action"}}`,
	))

	events, msg, err := collect(t, p.StreamText(t.Context(), fakeModel, []ai.Message{ai.UserMessage("List")}, nil))
	require.NoError(t, err)

	call := ai.ToolCall{
		ID:    "call-1",
		Name:  "list",
		Input: json.RawMessage(`{"dir":"."}`),
	}
	assert.Equal(t, []ai.Event{
		{Type: ai.EventStart},
		{Type: ai.EventToolStart},
		{Type: ai.EventToolDelta, Delta: `{"dir":"."}`},
		{Type: ai.EventToolEnd, ToolCall: &call},
	}, events)
	assert.Equal(t, []ai.Content{call}, msg.Content)
}

func TestStreamText_Stops(t *testing.T) {
	tests := []struct {
		name    string
		status  string
		want    ai.StopReason
		wantErr bool
	}{
		{name: "completed", status: "completed", want: ai.StopReasonStop},
		{name: "requires_action", status: "requires_action", want: ai.StopReasonToolUse},
		{name: "incomplete", status: "incomplete", want: ai.StopReasonLength},
		{name: "budget_exceeded", status: "budget_exceeded", want: ai.StopReasonLength},
		{name: "failed", status: "failed", wantErr: true},
		{name: "cancelled", status: "cancelled", wantErr: true},
		{name: "in_progress", status: "in_progress", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, _ := fakeProvider(t, sse(
				`{"event_type":"step.start","index":0,"step":{"type":"model_output"}}`,
				`{"event_type":"step.delta","index":0,"delta":{"type":"text","text":"Hi"}}`,
				`{"event_type":"step.stop","index":0}`,
				`{"event_type":"interaction.completed","interaction":{"id":"i1","status":"`+tt.status+`"}}`,
			))

			_, msg, err := collect(t, p.StreamText(t.Context(), fakeModel, []ai.Message{ai.UserMessage("Hi")}, nil))
			if tt.wantErr {
				assert.ErrorContains(t, err, tt.status)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, msg.StopReason)
		})
	}
}

func TestStreamText_ToolCallStops(t *testing.T) {
	tests := []struct {
		name   string
		status string
		want   ai.StopReason
	}{
		{name: "completed", status: "completed", want: ai.StopReasonToolUse},
		{name: "incomplete", status: "incomplete", want: ai.StopReasonLength},
		{name: "budget_exceeded", status: "budget_exceeded", want: ai.StopReasonLength},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, _ := fakeProvider(t, sse(
				`{"event_type":"step.start","index":0,"step":{"type":"function_call","id":"call-1","name":"list","arguments":{"dir":"."}}}`,
				`{"event_type":"step.stop","index":0}`,
				`{"event_type":"interaction.completed","interaction":{"id":"i1","status":"`+tt.status+`"}}`,
			))

			_, msg, err := collect(t, p.StreamText(t.Context(), fakeModel, []ai.Message{ai.UserMessage("List")}, nil))
			require.NoError(t, err)
			assert.Equal(t, tt.want, msg.StopReason)
		})
	}
}

func TestStreamText_ErrorEvent(t *testing.T) {
	p, _ := fakeProvider(t, sse(
		`{"event_type":"step.start","index":0,"step":{"type":"model_output"}}`,
		`{"event_type":"error","error":{"code":"internal","message":"the model broke"}}`,
	))

	_, _, err := collect(t, p.StreamText(t.Context(), fakeModel, []ai.Message{ai.UserMessage("Hi")}, nil))
	assert.ErrorContains(t, err, "the model broke")
}

func TestStreamText_EndsEarly(t *testing.T) {
	p, _ := fakeProvider(t, sse(
		`{"event_type":"step.start","index":0,"step":{"type":"model_output"}}`,
	))

	_, _, err := collect(t, p.StreamText(t.Context(), fakeModel, []ai.Message{ai.UserMessage("Hi")}, nil))
	assert.ErrorContains(t, err, "stream ended before the interaction completed")
}

func TestStreamText_FailedSendsNoEnd(t *testing.T) {
	p, _ := fakeProvider(t, sse(
		`{"event_type":"step.start","index":0,"step":{"type":"model_output"}}`,
		`{"event_type":"step.delta","index":0,"delta":{"type":"text","text":"Hi"}}`,
		`{"event_type":"interaction.completed","interaction":{"id":"i1","status":"failed"}}`,
	))

	events, _, err := collect(t, p.StreamText(t.Context(), fakeModel, []ai.Message{ai.UserMessage("Hi")}, nil))
	require.ErrorContains(t, err, "failed")
	assert.Equal(t, []ai.Event{
		{Type: ai.EventStart},
		{Type: ai.EventTextStart},
		{Type: ai.EventTextDelta, Delta: "Hi"},
	}, events)
}
