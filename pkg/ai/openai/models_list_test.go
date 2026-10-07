package openai_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/ai/openai"
)

// modelsList is GET /v1/models. OpenAI sends the ID only. A compatible
// server, such as OpenRouter, also sends a name.
const modelsList = `{
  "object": "list",
  "data": [
    {"id": "gpt-6-sol", "object": "model", "created": 1780000000, "owned_by": "openai"},
    {"id": "anthropic/claude-sonnet-5", "object": "model", "created": 1782700000, "owned_by": "anthropic", "name": "Anthropic: Claude Sonnet 5"}
  ]
}`

func TestListModels(t *testing.T) {
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(modelsList))
	}))
	t.Cleanup(server.Close)
	p := openai.New(openai.APIKey("test-key"), openai.BaseURL(server.URL+"/v1"))

	got, err := p.ListModels(context.Background())

	require.NoError(t, err)
	assert.Equal(t, "/v1/models", path)
	assert.Equal(t, []ai.Model{
		{ID: "gpt-6-sol"},
		{ID: "anthropic/claude-sonnet-5", Name: "Anthropic: Claude Sonnet 5"},
	}, got)
}
