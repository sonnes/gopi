package chatgpt_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/internal/httpget"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/ai/chatgpt"
	"github.com/sonnes/gopi/pkg/ai/oauth"
)

// staticSource gives one token.
type staticSource string

func (s staticSource) Token(context.Context) (oauth.Token, error) {
	return oauth.Token{Access: string(s)}, nil
}

func newProvider(f *fakeOpenAI) *chatgpt.Provider {
	return chatgpt.New(
		staticSource("plan-token"),
		chatgpt.BaseURL(f.srv.URL+"/v1"),
	)
}

func TestProvider_StreamText(t *testing.T) {
	f := newFakeOpenAI(t)
	p := newProvider(f)
	tool := ai.ToolInfo{
		Name:        "get_time",
		Description: "Get the current time.",
		InputSchema: &jsonschema.Schema{Type: "object"},
	}

	msg, err := p.StreamText(
		t.Context(),
		ai.Model{ID: "gpt-6.1-sol"},
		[]ai.Message{ai.UserMessage("Hi")},
		ai.JoinOptions(
			ai.Tools([]ai.ToolInfo{tool}),
			ai.MaxTokens(100),
		),
	).Wait()

	require.NoError(t, err)
	assert.Equal(t, []ai.Content{ai.Text{Text: "Hello."}}, msg.Content)
	assert.Equal(t, "Bearer plan-token", f.bearer)
	require.Len(t, f.responses, 1)
	body := f.responses[0]
	assert.NotContains(t, body, "max_output_tokens")
	tools := body["tools"].([]any)
	assert.Equal(t, "namespace", tools[0].(map[string]any)["type"])
}

func TestProvider_ListModels(t *testing.T) {
	f := newFakeOpenAI(t)
	p := newProvider(f)

	models, err := p.ListModels(t.Context())

	require.NoError(t, err)
	assert.Equal(t, "Bearer plan-token", f.bearer)
	assert.Equal(t, []ai.Model{
		{
			ID:            "gpt-6.1-sol",
			Name:          "GPT-6.1 Sol",
			ContextWindow: 272000,
			Input:         []ai.Modality{ai.ModalityText, ai.ModalityImage},
			Reasoning:     true,
			ThinkingLevels: []ai.ThinkingLevel{
				ai.ThinkingLow,
				ai.ThinkingMedium,
				ai.ThinkingHigh,
				ai.ThinkingXHigh,
			},
		},
		{
			ID:            "gpt-6-luna",
			Name:          "GPT-6 Luna",
			ContextWindow: 400000,
		},
	}, models)
}

func TestProvider_ListModelsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"detail":"expired"}`))
	}))
	t.Cleanup(srv.Close)
	p := chatgpt.New(staticSource("plan-token"), chatgpt.BaseURL(srv.URL))

	_, err := p.ListModels(t.Context())

	require.ErrorContains(t, err, "chatgpt: list models: 401 Unauthorized: ")
	var status *httpget.StatusError
	require.ErrorAs(t, err, &status)
	assert.Equal(t, http.StatusUnauthorized, status.Code)
}
