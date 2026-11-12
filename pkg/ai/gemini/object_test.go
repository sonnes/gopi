package gemini_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
)

// objectBody is a completed interaction with one object as its text.
const objectBody = `{
	"id": "i1",
	"status": "completed",
	"steps": [
		{"type": "thought", "signature": "sig-1"},
		{"type": "model_output", "content": [{"type": "text", "text": "{\"name\":"}, {"type": "text", "text": "\"Ada\"}"}]}
	],
	"usage": {"total_input_tokens": 10, "total_output_tokens": 5, "total_thought_tokens": 2}
}`

// answer answers a request with body as JSON.
func answer(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	}
}

func TestGenerateObject(t *testing.T) {
	p, body := fakeProvider(t, answer(objectBody))
	schema := &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"name": {Type: "string"},
		},
	}

	resp, err := p.GenerateObject(
		t.Context(),
		fakeModel,
		[]ai.Message{ai.UserMessage("Name a scientist.")},
		schema,
		ai.Tools([]ai.ToolInfo{weatherTool}),
	)
	require.NoError(t, err)

	assert.Equal(t, `{"name":"Ada"}`, resp.Raw)
	wantUsage := ai.Usage{Input: 10, Output: 7}
	wantUsage.Cost = fakeModel.Pricing.Cost(wantUsage)
	assert.Equal(t, wantUsage, resp.Usage)

	assert.JSONEq(t, `{
		"model": "gemini-3.8-flash",
		"store": false,
		"stream": false,
		"input": [{"type": "user_input", "content": [{"type": "text", "text": "Name a scientist."}]}],
		"generation_config": {"thinking_summaries": "auto"},
		"response_format": {
			"type": "text",
			"mime_type": "application/json",
			"schema": {"type": "object", "properties": {"name": {"type": "string"}}}
		}
	}`, string(body()))
}

func TestGenerateObject_NoText(t *testing.T) {
	p, _ := fakeProvider(t, answer(`{"id": "i1", "status": "completed", "steps": []}`))

	_, err := p.GenerateObject(t.Context(), fakeModel, []ai.Message{ai.UserMessage("Hi")}, &jsonschema.Schema{Type: "object"}, nil)
	assert.ErrorContains(t, err, "no object")
}

func TestGenerateObject_Stops(t *testing.T) {
	tests := []struct {
		name    string
		status  string
		wantErr string
	}{
		{name: "failed", status: "failed", wantErr: "failed"},
		{name: "cancelled", status: "cancelled", wantErr: "cancelled"},
		{name: "incomplete", status: "incomplete", wantErr: "cut off"},
		{name: "budget_exceeded", status: "budget_exceeded", wantErr: "cut off"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, _ := fakeProvider(t, answer(`{
				"id": "i1",
				"status": "`+tt.status+`",
				"steps": [{"type": "model_output", "content": [{"type": "text", "text": "{\"name\":"}]}]
			}`))

			_, err := p.GenerateObject(t.Context(), fakeModel, []ai.Message{ai.UserMessage("Hi")}, &jsonschema.Schema{Type: "object"}, nil)
			assert.ErrorContains(t, err, tt.wantErr)
		})
	}
}

// TestGenerateObject_IgnoresTools checks that an object call does not
// convert the tools, because it sends none. A tool with a bad schema
// does not fail the call.
func TestGenerateObject_IgnoresTools(t *testing.T) {
	p, body := fakeProvider(t, answer(objectBody))
	badTool := ai.ToolInfo{
		Name: "bad",
		InputSchema: &jsonschema.Schema{
			Type:  "object",
			Types: []string{"object", "null"},
		},
	}

	resp, err := p.GenerateObject(
		t.Context(),
		fakeModel,
		[]ai.Message{ai.UserMessage("Name a scientist.")},
		&jsonschema.Schema{Type: "object"},
		ai.Tools([]ai.ToolInfo{badTool}),
	)
	require.NoError(t, err)

	assert.Equal(t, `{"name":"Ada"}`, resp.Raw)
	assert.NotContains(t, string(body()), `"tools"`)
}
