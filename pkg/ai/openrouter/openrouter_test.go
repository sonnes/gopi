package openrouter_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/ai/openrouter"
)

// request is what a test server saw of one request.
type request struct {
	path  string
	query string
	auth  string
}

// newServer returns a server that records each request and answers
// with status and body.
func newServer(t *testing.T, status int, body []byte) (*httptest.Server, *[]request) {
	t.Helper()
	var seen []request
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, request{
			path:  r.URL.Path,
			query: r.URL.RawQuery,
			auth:  r.Header.Get("Authorization"),
		})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		w.Write(body)
	}))
	t.Cleanup(server.Close)
	return server, &seen
}

func TestListModels(t *testing.T) {
	body, err := os.ReadFile("testdata/models.json")
	require.NoError(t, err)
	server, seen := newServer(t, http.StatusOK, body)
	p := openrouter.New(
		openrouter.APIKey("test-key"),
		openrouter.BaseURL(server.URL+"/api/v1"),
	)

	got, err := p.ListModels(context.Background())

	require.NoError(t, err)
	assert.Equal(t, []request{{
		path:  "/api/v1/models",
		query: "supported_parameters=tools",
		auth:  "Bearer test-key",
	}}, *seen, "the list asks for the models with tools, because each agent call sends tools")
	all := []ai.ThinkingLevel{
		ai.ThinkingOff,
		ai.ThinkingLow,
		ai.ThinkingMedium,
		ai.ThinkingHigh,
		ai.ThinkingXHigh,
		ai.ThinkingMax,
	}
	documents := []ai.Modality{ai.ModalityText, ai.ModalityImage, ai.ModalityPDF}
	assert.Equal(t, []ai.Model{
		{
			ID:             "openai/gpt-6-sol",
			Name:           "OpenAI: GPT-6 Sol",
			MaxTokens:      128_000,
			ContextWindow:  1_050_000,
			Input:          documents,
			Reasoning:      true,
			ThinkingLevels: all,
			Pricing: ai.Pricing{
				Input:      2,
				Output:     10,
				CacheRead:  0.2,
				CacheWrite: 2.5,
				Tiers: []ai.PriceTier{{
					InputAbove: 271_999,
					Input:      4,
					Output:     15,
					CacheRead:  0.4,
					CacheWrite: 5,
				}},
			},
		},
		{
			ID:             "anthropic/claude-haiku-5.5",
			Name:           "Anthropic: Claude Haiku 5.5",
			MaxTokens:      128_000,
			ContextWindow:  1_000_000,
			Input:          documents,
			Reasoning:      true,
			ThinkingLevels: all,
			Pricing: ai.Pricing{
				Input:          0.1,
				Output:         0.5,
				CacheRead:      0.01,
				CacheWrite:     0.125,
				CacheWriteLong: 0.2,
				Tiers: []ai.PriceTier{{
					InputAbove:     99_999,
					Input:          0.5,
					Output:         2.5,
					CacheRead:      0.05,
					CacheWrite:     0.625,
					CacheWriteLong: 1,
				}},
			},
		},
		{
			ID:            "meta/muse-wonder",
			Name:          "Meta: Muse Wonder",
			MaxTokens:     943_718,
			ContextWindow: 1_048_576,
			Input:         documents,
			Reasoning:     true,
			ThinkingLevels: []ai.ThinkingLevel{
				ai.ThinkingLow,
				ai.ThinkingMedium,
				ai.ThinkingHigh,
				ai.ThinkingXHigh,
				ai.ThinkingMax,
			},
			Pricing: ai.Pricing{
				Input:     4,
				Output:    20,
				CacheRead: 0.2,
			},
		},
		{
			ID:            "openrouter/free",
			Name:          "Free Models Router",
			ContextWindow: 200_000,
			Input:         []ai.Modality{ai.ModalityText, ai.ModalityImage},
		},
		{
			ID:            "typesafe/jev-router",
			Name:          "TypeSafe: Jev Router",
			ContextWindow: 1_000_000,
			Input:         documents,
		},
	}, got)
}

// TestListModels_TierFallsBack checks that an override with
// min_prompt_tokens applies from that many tokens, and that a price
// that the override does not give, or gives as unknown, is the base
// price.
func TestListModels_TierFallsBack(t *testing.T) {
	body := `{"data":[{"id":"a/b","name":"B","pricing":{
		"prompt":"0.000001","completion":"0.000002","input_cache_read":"0.0000001",
		"overrides":[
			{"min_prompt_tokens":1000,"prompt":"0.000003","completion":"-1"},
			{"min_prompt_tokens":5000,"utc_start":1600,"utc_end":0,"prompt":"0.000009"}
		]
	}}]}`
	server, _ := newServer(t, http.StatusOK, []byte(body))
	p := openrouter.New(openrouter.APIKey("k"), openrouter.BaseURL(server.URL))

	got, err := p.ListModels(context.Background())

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, ai.Pricing{
		Input:     1,
		Output:    2,
		CacheRead: 0.1,
		Tiers: []ai.PriceTier{{
			InputAbove: 999,
			Input:      3,
			Output:     2,
			CacheRead:  0.1,
		}},
	}, got[0].Pricing)
}

// TestListModels_Limits checks that the output cap is at most the
// context window, and that a tier that no request can reach is not in
// the list.
func TestListModels_Limits(t *testing.T) {
	body := `{"data":[{"id":"a/b","name":"B","context_length":131072,
		"top_provider":{"max_completion_tokens":147456},
		"pricing":{"prompt":"0.000001","overrides":[
			{"min_prompt_tokens":100000,"prompt":"0.000002"},
			{"min_prompt_tokens":256000,"prompt":"0.000003"}
		]}
	}]}`
	server, _ := newServer(t, http.StatusOK, []byte(body))
	p := openrouter.New(openrouter.APIKey("k"), openrouter.BaseURL(server.URL))

	got, err := p.ListModels(context.Background())

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, 131_072, got[0].MaxTokens)
	assert.Equal(t, ai.Pricing{
		Input: 1,
		Tiers: []ai.PriceTier{{
			InputAbove: 99_999,
			Input:      2,
		}},
	}, got[0].Pricing)
}

// TestListModels_SkipsBatch checks that the list skips a model of the
// Batch API, because an agent streams each call.
func TestListModels_SkipsBatch(t *testing.T) {
	body := `{"data":[{"id":"a/b","name":"B"},{"id":"a/b:batch","name":"B (batch)"}]}`
	server, _ := newServer(t, http.StatusOK, []byte(body))
	p := openrouter.New(openrouter.APIKey("k"), openrouter.BaseURL(server.URL))

	got, err := p.ListModels(context.Background())

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "a/b", got[0].ID)
}

// TestListModels_GoogleCacheWrite checks that a cache write of a Google
// model costs the input price plus the listed price, because OpenRouter
// lists only the storage of the write.
func TestListModels_GoogleCacheWrite(t *testing.T) {
	body := `{"data":[{"id":"google/gemini","name":"Gemini","pricing":{
		"prompt":"0.0000003","input_cache_write":"0.000000041667",
		"overrides":[{"min_prompt_tokens":200000,"prompt":"0.0000006"}]
	}}]}`
	server, _ := newServer(t, http.StatusOK, []byte(body))
	p := openrouter.New(openrouter.APIKey("k"), openrouter.BaseURL(server.URL))

	got, err := p.ListModels(context.Background())

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, ai.Pricing{
		Input:      0.3,
		CacheWrite: 0.341667,
		Tiers: []ai.PriceTier{{
			InputAbove: 199_999,
			Input:      0.6,
			CacheWrite: 0.641667,
		}},
	}, got[0].Pricing)
}

// TestListModels_BaseURLSlash checks that a base URL with a trailing
// slash gives the same path as one without.
func TestListModels_BaseURLSlash(t *testing.T) {
	server, seen := newServer(t, http.StatusOK, []byte(`{"data":[]}`))
	p := openrouter.New(openrouter.APIKey("k"), openrouter.BaseURL(server.URL+"/api/v1/"))

	_, err := p.ListModels(context.Background())

	require.NoError(t, err)
	assert.Equal(t, "/api/v1/models", (*seen)[0].path)
}

func TestListModels_Error(t *testing.T) {
	server, _ := newServer(t, http.StatusUnauthorized, []byte(`{"error":{"message":"No auth credentials found","code":401}}`))
	p := openrouter.New(openrouter.APIKey(""), openrouter.BaseURL(server.URL))

	_, err := p.ListModels(context.Background())

	require.ErrorContains(t, err, "openrouter: list models: 401 Unauthorized: ")
	require.ErrorContains(t, err, "No auth credentials found")
}

func TestNew_APIKey(t *testing.T) {
	tests := []struct {
		name string
		opts []openrouter.Option
		want string
	}{
		{
			name: "no key reads OPENROUTER_API_KEY",
			want: "Bearer router-key",
		},
		{
			name: "a key replaces OPENROUTER_API_KEY",
			opts: []openrouter.Option{openrouter.APIKey("my-key")},
			want: "Bearer my-key",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("OPENROUTER_API_KEY", "router-key")
			t.Setenv("OPENAI_API_KEY", "openai-key")
			server, seen := newServer(t, http.StatusBadRequest, []byte(`{"error":{"message":"bad"}}`))
			opts := append(tt.opts, openrouter.BaseURL(server.URL+"/api/v1"))
			p := openrouter.New(opts...)

			_, _ = p.ListModels(context.Background())
			_, _ = p.StreamText(
				context.Background(),
				ai.Model{ID: "openai/gpt-6-sol"},
				[]ai.Message{ai.UserMessage("hi")},
				nil,
			).Wait()

			assert.Equal(t, []request{
				{path: "/api/v1/models", query: "supported_parameters=tools", auth: tt.want},
				{path: "/api/v1/responses", auth: tt.want},
			}, *seen)
		})
	}
}

// TestProvider_Capabilities checks that the provider has only the calls
// that OpenRouter serves.
func TestProvider_Capabilities(t *testing.T) {
	var p any = openrouter.New()

	assert.Implements(t, (*ai.TextProvider)(nil), p)
	assert.Implements(t, (*ai.ObjectProvider)(nil), p)
	assert.Implements(t, (*ai.ModelLister)(nil), p)
	_, image := p.(ai.ImageProvider)
	_, tokens := p.(ai.TokenCounter)
	assert.False(t, image, "OpenRouter has no image API")
	assert.False(t, tokens, "OpenRouter has no token count API")
}
