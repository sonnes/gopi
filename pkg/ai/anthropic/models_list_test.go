package anthropic_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/ai/anthropic"
)

// The two pages of GET /v1/models, in the shape of the Models API.
const (
	modelsPage1 = `{
  "data": [
    {"type": "model", "id": "claude-opus-5-5", "display_name": "Claude Opus 5.5", "created_at": "2026-08-20T00:00:00Z", "max_input_tokens": 1000000, "max_tokens": 128000,
     "capabilities": {
       "image_input": {"supported": true}, "pdf_input": {"supported": true},
       "thinking": {"supported": true, "types": {"adaptive": {"supported": true}, "enabled": {"supported": false}}},
       "effort": {"supported": true, "low": {"supported": true}, "medium": {"supported": true}, "high": {"supported": true}, "xhigh": {"supported": true}, "max": {"supported": true}}
     }},
    {"type": "model", "id": "claude-sonnet-5", "display_name": "Claude Sonnet 5", "created_at": "2026-06-29T00:00:00Z", "max_input_tokens": 1000000, "max_tokens": 128000}
  ],
  "has_more": true,
  "first_id": "claude-opus-5-5",
  "last_id": "claude-sonnet-5"
}`
	modelsPage2 = `{
  "data": [
    {"type": "model", "id": "claude-haiku-4-5", "display_name": "Claude Haiku 4.5", "created_at": "2025-10-01T00:00:00Z", "max_input_tokens": 200000, "max_tokens": 64000,
     "capabilities": {
       "image_input": {"supported": true}, "pdf_input": {"supported": false},
       "thinking": {"supported": true, "types": {"adaptive": {"supported": false}, "enabled": {"supported": true}}},
       "effort": {"supported": false}
     }}
  ],
  "has_more": false,
  "first_id": "claude-haiku-4-5",
  "last_id": "claude-haiku-4-5"
}`
)

func TestListModels(t *testing.T) {
	var keys []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		keys = append(keys, r.Header.Get("X-Api-Key"))
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("after_id") == "claude-sonnet-5" {
			w.Write([]byte(modelsPage2))
			return
		}
		w.Write([]byte(modelsPage1))
	}))
	t.Cleanup(server.Close)
	p := anthropic.New(
		anthropic.APIKey("test-key"),
		anthropic.BaseURL(server.URL),
	)

	got, err := p.ListModels(context.Background())

	require.NoError(t, err)
	assert.Equal(t, []ai.Model{
		{
			ID:            "claude-opus-5-5",
			Name:          "Claude Opus 5.5",
			MaxTokens:     128000,
			ContextWindow: 1000000,
			Input:         []ai.Modality{ai.ModalityText, ai.ModalityImage, ai.ModalityPDF},
			Reasoning:     true,
			ThinkingLevels: []ai.ThinkingLevel{
				ai.ThinkingLow,
				ai.ThinkingMedium,
				ai.ThinkingHigh,
				ai.ThinkingXHigh,
				ai.ThinkingMax,
			},
		},
		{
			ID:            "claude-sonnet-5",
			Name:          "Claude Sonnet 5",
			MaxTokens:     128000,
			ContextWindow: 1000000,
			Input:         []ai.Modality{ai.ModalityText},
		},
		{
			ID:            "claude-haiku-4-5",
			Name:          "Claude Haiku 4.5",
			MaxTokens:     64000,
			ContextWindow: 200000,
			Input:         []ai.Modality{ai.ModalityText, ai.ModalityImage},
			Reasoning:     true,
		},
	}, got, "the list has no prices, and a level only when the model has effort")
	assert.Equal(t, []string{"test-key", "test-key"}, keys)
}

func TestListModels_Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`))
	}))
	t.Cleanup(server.Close)
	p := anthropic.New(anthropic.APIKey("bad"), anthropic.BaseURL(server.URL))

	_, err := p.ListModels(context.Background())

	var apiErr *anthropic.Error
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusUnauthorized, apiErr.StatusCode)
}
