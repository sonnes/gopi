package openai_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/ai/openai"
)

// The events of the ChatGPT plan route, from a live stream of
// gpt-6.1-sol. The route sends each output item in
// response.output_item.done, and an empty output in response.completed.
var planToolCallEvents = []string{
	`{"type":"response.created","response":{"id":"resp_1","status":"in_progress","output":[]}}`,
	`{"type":"response.output_item.added","output_index":0,"item":{"id":"fc_1","type":"function_call","status":"in_progress","arguments":"","call_id":"call_1","name":"get_time","namespace":"gopi"}}`,
	`{"type":"response.function_call_arguments.delta","output_index":0,"item_id":"fc_1","delta":"{}"}`,
	`{"type":"response.function_call_arguments.done","output_index":0,"item_id":"fc_1","arguments":"{}"}`,
	`{"type":"response.output_item.done","output_index":0,"item":{"id":"fc_1","type":"function_call","status":"completed","arguments":"{}","call_id":"call_1","name":"get_time","namespace":"gopi"}}`,
	`{"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[],"usage":{"input_tokens":50,"output_tokens":5,"total_tokens":55}}}`,
}

var planTextEvents = []string{
	`{"type":"response.created","response":{"id":"resp_2","status":"in_progress","output":[]}}`,
	`{"type":"response.output_item.added","output_index":0,"item":{"id":"rs_1","type":"reasoning","summary":[]}}`,
	`{"type":"response.output_item.done","output_index":0,"item":{"id":"rs_1","type":"reasoning","summary":[{"type":"summary_text","text":"Count the primes."}],"encrypted_content":"enc-1"}}`,
	`{"type":"response.output_item.added","output_index":1,"item":{"id":"msg_1","type":"message","role":"assistant","content":[]}}`,
	`{"type":"response.content_part.added","output_index":1,"content_index":0,"part":{"type":"output_text","text":""}}`,
	`{"type":"response.output_text.delta","output_index":1,"content_index":0,"delta":"{\"n\":16}"}`,
	`{"type":"response.output_item.done","output_index":1,"item":{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"{\"n\":16}","annotations":[]}]}}`,
	`{"type":"response.completed","response":{"id":"resp_2","status":"completed","output":[],"usage":{"input_tokens":40,"output_tokens":120,"total_tokens":160}}}`,
}

// planServer answers each request with events, and records the JSON
// body of each request.
func planServer(t *testing.T, events []string) (*openai.Provider, *[]map[string]any) {
	t.Helper()

	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		var body map[string]any
		require.NoError(t, json.Unmarshal(data, &body))
		bodies = append(bodies, body)

		w.Header().Set("Content-Type", "text/event-stream")
		for _, ev := range events {
			fmt.Fprintf(w, "data: %s\n\n", ev)
		}
	}))
	t.Cleanup(srv.Close)

	p := openai.New(
		openai.APIKey("test-key"),
		openai.BaseURL(srv.URL),
		openai.ChatGPTPlan(),
	)
	return p, &bodies
}

var timeTool = ai.ToolInfo{
	Name:        "get_time",
	Description: "Get the current time.",
	InputSchema: &jsonschema.Schema{Type: "object"},
}

func TestStream_OutputFromItemDone(t *testing.T) {
	p := sseProvider(t, planToolCallEvents...)

	_, msg, err := run(t, p)

	require.NoError(t, err)
	require.Len(t, msg.Content, 1)
	call, ok := msg.Content[0].(ai.ToolCall)
	require.True(t, ok)
	assert.Equal(t, "call_1", call.ID)
	assert.Equal(t, "get_time", call.Name)
	assert.Equal(t, ai.StopReasonToolUse, msg.StopReason)
}

func TestChatGPTPlan_Request(t *testing.T) {
	p, bodies := planServer(t, planToolCallEvents)
	history := []ai.Message{
		ai.UserMessage("What time is it?"),
		ai.AssistantMessage(ai.ToolCall{
			ID:    "call_0",
			Name:  "get_time",
			Input: json.RawMessage(`{}`),
		}),
		ai.ToolResultMessage("call_0", ai.Text{Text: "11:00"}),
		ai.UserMessage("And now?"),
	}

	stream := p.StreamText(
		t.Context(),
		luna,
		history,
		ai.JoinOptions(
			ai.Tools([]ai.ToolInfo{timeTool}),
			ai.MaxTokens(1000),
			ai.Temperature(0.2),
			ai.CacheRetention(ai.CacheLong),
		),
	)
	_, err := stream.Wait()
	require.NoError(t, err)

	require.Len(t, *bodies, 1)
	body := (*bodies)[0]
	assert.Equal(t, true, body["stream"])
	assert.Equal(t, false, body["store"])
	for _, field := range []string{"max_output_tokens", "temperature", "prompt_cache_retention"} {
		assert.NotContains(t, body, field)
	}

	tools := body["tools"].([]any)
	require.Len(t, tools, 1)
	ns := tools[0].(map[string]any)
	assert.Equal(t, "namespace", ns["type"])
	assert.Equal(t, "gopi", ns["name"])
	inner := ns["tools"].([]any)
	require.Len(t, inner, 1)
	assert.Equal(t, "get_time", inner[0].(map[string]any)["name"])

	var calls []map[string]any
	for _, item := range body["input"].([]any) {
		m := item.(map[string]any)
		if m["type"] == "function_call" {
			calls = append(calls, m)
		}
	}
	require.Len(t, calls, 1)
	assert.Equal(t, "gopi", calls[0]["namespace"])
}

func TestChatGPTPlan_GenerateObject(t *testing.T) {
	p, bodies := planServer(t, planTextEvents)
	schema := &jsonschema.Schema{
		Type:       "object",
		Properties: map[string]*jsonschema.Schema{"n": {Type: "integer"}},
	}

	res, err := p.GenerateObject(
		t.Context(),
		luna,
		[]ai.Message{ai.UserMessage("How many primes are there between 1000 and 1100?")},
		schema,
		nil,
	)

	require.NoError(t, err)
	assert.Equal(t, `{"n":16}`, res.Raw)
	assert.Equal(t, 120, res.Usage.Output)
	require.Len(t, *bodies, 1)
	assert.Equal(t, true, (*bodies)[0]["stream"])
}

func TestChatGPTPlan_CountTokens(t *testing.T) {
	p, _ := planServer(t, nil)

	_, err := p.CountTokens(
		t.Context(),
		luna,
		[]ai.Message{ai.UserMessage("Hi")},
		nil,
	)

	require.ErrorIs(t, err, openai.ErrNotOnPlan)
}
