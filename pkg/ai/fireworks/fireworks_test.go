package fireworks_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/ai/fireworks"
)

// request is what a test server saw of one request.
type request struct {
	path  string
	query string
	auth  string
}

// newServer returns a server that records each request and answers it
// with the response of its path and query in bodies. A request that
// bodies does not have gets a 400.
func newServer(t *testing.T, bodies map[string]string) (*httptest.Server, *[]request) {
	t.Helper()
	var seen []request
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, request{
			path:  r.URL.Path,
			query: r.URL.RawQuery,
			auth:  r.Header.Get("Authorization"),
		})
		w.Header().Set("Content-Type", "application/json")
		body, ok := bodies[r.URL.Path+"?"+r.URL.RawQuery]
		if !ok {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"error":{"message":"bad"}}`))
			return
		}
		w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server, &seen
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(b)
}

func TestListModels(t *testing.T) {
	server, seen := newServer(t, map[string]string{
		"/v1/accounts/fireworks/models?pageSize=200":                  readFile(t, "testdata/models-page1.json"),
		"/v1/accounts/fireworks/models?pageSize=200&pageToken=page-2": readFile(t, "testdata/models-page2.json"),
	})
	p := fireworks.New(
		fireworks.APIKey("test-key"),
		fireworks.BaseURL(server.URL),
	)

	got, err := p.ListModels(context.Background())

	require.NoError(t, err)
	assert.Equal(t, []request{
		{path: "/v1/accounts/fireworks/models", query: "pageSize=200", auth: "Bearer test-key"},
		{path: "/v1/accounts/fireworks/models", query: "pageSize=200&pageToken=page-2", auth: "Bearer test-key"},
	}, *seen)
	text := []ai.Modality{ai.ModalityText}
	assert.Equal(t, []ai.Model{
		{
			ID:            "accounts/fireworks/models/kimi-k2-thinking",
			Name:          "Kimi K2 Thinking",
			ContextWindow: 262_144,
			Input:         text,
		},
		{
			ID:            "accounts/fireworks/models/qwen3-vl-235b-a22b-instruct",
			Name:          "Qwen3 VL 235B A22B Instruct",
			ContextWindow: 262_144,
			Input:         []ai.Modality{ai.ModalityText, ai.ModalityImage},
		},
		{
			ID:            "accounts/fireworks/models/deepseek-v3p2",
			Name:          "DeepSeek V3.2",
			ContextWindow: 163_840,
			Input:         text,
		},
	}, got, "the list keeps only the ready serverless models with tools")
}

func TestListModels_Account(t *testing.T) {
	server, seen := newServer(t, map[string]string{
		"/v1/accounts/acme/models?pageSize=200": `{"models":[]}`,
	})
	p := fireworks.New(
		fireworks.APIKey("test-key"),
		fireworks.BaseURL(server.URL),
		fireworks.Account("acme"),
	)

	got, err := p.ListModels(context.Background())

	require.NoError(t, err)
	assert.Empty(t, got)
	assert.Equal(t, "/v1/accounts/acme/models", (*seen)[0].path)
}

// TestListModels_BaseURLSlash checks that a base URL with a trailing
// slash gives the same path as one without.
func TestListModels_BaseURLSlash(t *testing.T) {
	server, seen := newServer(t, map[string]string{
		"/v1/accounts/fireworks/models?pageSize=200": `{"models":[]}`,
	})
	p := fireworks.New(fireworks.APIKey("test-key"), fireworks.BaseURL(server.URL+"/"))

	_, err := p.ListModels(context.Background())

	require.NoError(t, err)
	assert.Equal(t, "/v1/accounts/fireworks/models", (*seen)[0].path)
}

func TestListModels_Error(t *testing.T) {
	server, _ := newServer(t, nil)
	p := fireworks.New(fireworks.APIKey("test-key"), fireworks.BaseURL(server.URL))

	_, err := p.ListModels(context.Background())

	require.ErrorContains(t, err, "fireworks: list models: 400 Bad Request: ")
}

func TestNew_APIKey(t *testing.T) {
	tests := []struct {
		name string
		opts []fireworks.Option
		want string
	}{
		{
			name: "no key reads FIREWORKS_API_KEY",
			want: "Bearer fireworks-key",
		},
		{
			name: "a key replaces FIREWORKS_API_KEY",
			opts: []fireworks.Option{fireworks.APIKey("my-key")},
			want: "Bearer my-key",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("FIREWORKS_API_KEY", "fireworks-key")
			t.Setenv("OPENAI_API_KEY", "openai-key")
			server, seen := newServer(t, nil)
			opts := append(tt.opts, fireworks.BaseURL(server.URL))
			p := fireworks.New(opts...)

			_, _ = p.ListModels(context.Background())
			_, _ = p.StreamText(
				context.Background(),
				ai.Model{ID: "accounts/fireworks/models/kimi-k2-thinking"},
				[]ai.Message{ai.UserMessage("hi")},
				nil,
			).Wait()

			assert.Equal(t, []request{
				{path: "/v1/accounts/fireworks/models", query: "pageSize=200", auth: tt.want},
				{path: "/inference/v1/chat/completions", auth: tt.want},
			}, *seen)
		})
	}
}

// TestProvider_Capabilities checks that the provider has only the calls
// that Fireworks serves.
func TestProvider_Capabilities(t *testing.T) {
	var p any = fireworks.New()

	assert.Implements(t, (*ai.TextProvider)(nil), p)
	assert.Implements(t, (*ai.ObjectProvider)(nil), p)
	assert.Implements(t, (*ai.ModelLister)(nil), p)
	_, image := p.(ai.ImageProvider)
	_, tokens := p.(ai.TokenCounter)
	assert.False(t, image)
	assert.False(t, tokens)
}

func TestListModels_RepeatedPageToken(t *testing.T) {
	page := `{"models":[{"name":"accounts/fireworks/models/a","kind":"HF_BASE_MODEL"}],"nextPageToken":"same"}`
	server, seen := newServer(t, map[string]string{
		"/v1/accounts/fireworks/models?pageSize=200":                page,
		"/v1/accounts/fireworks/models?pageSize=200&pageToken=same": page,
	})
	p := fireworks.New(fireworks.APIKey("test-key"), fireworks.BaseURL(server.URL))

	_, err := p.ListModels(context.Background())

	require.EqualError(t, err, `fireworks: list models: the page token "same" repeats`)
	assert.Len(t, *seen, 2)
}
