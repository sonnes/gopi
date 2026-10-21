package openaichat_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/ai/openaichat"
)

func TestListModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"object":"list","data":[
			{"id":"accounts/fireworks/models/kimi-k2","object":"model","created":1752000000,"owned_by":"fireworks"}
		]}`))
	}))
	t.Cleanup(server.Close)
	p := openaichat.New(openaichat.APIKey("test-key"), openaichat.BaseURL(server.URL))

	got, err := p.ListModels(context.Background())

	require.NoError(t, err)
	assert.Equal(t, []ai.Model{{ID: "accounts/fireworks/models/kimi-k2"}}, got)
}

func TestAPIKey(t *testing.T) {
	tests := []struct {
		name string
		opts []openaichat.Option
		want string
	}{
		{
			name: "no key reads OPENAI_API_KEY",
			want: "Bearer env-key",
		},
		{
			name: "a key replaces OPENAI_API_KEY",
			opts: []openaichat.Option{openaichat.APIKey("my-key")},
			want: "Bearer my-key",
		},
		{
			name: "an empty key sends no key",
			opts: []openaichat.Option{openaichat.APIKey("")},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("OPENAI_API_KEY", "env-key")
			var got string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = r.Header.Get("Authorization")
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"object":"list","data":[]}`))
			}))
			t.Cleanup(server.Close)
			opts := append(tt.opts, openaichat.BaseURL(server.URL))

			_, err := openaichat.New(opts...).ListModels(context.Background())

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
