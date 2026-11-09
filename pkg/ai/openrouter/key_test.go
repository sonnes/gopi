package openrouter_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/internal/httpget"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/ai/openrouter"
)

func TestCheckKey(t *testing.T) {
	tests := []struct {
		name string
		body string
		want ai.KeyInfo
	}{
		{
			name: "a key with a limit",
			body: `{"data":{"label":"sk-or-v1-abc...xyz","limit":50,"limit_remaining":37.6,"usage":12.4,"is_free_tier":false}}`,
			want: ai.KeyInfo{Label: "sk-or-v1-abc...xyz", Usage: 12.4, Limit: 50, Limited: true},
		},
		{
			name: "a key with no limit",
			body: `{"data":{"label":"LittleWork","limit":null,"limit_remaining":null,"usage":0.25,"is_free_tier":true}}`,
			want: ai.KeyInfo{Label: "LittleWork", Usage: 0.25, FreeTier: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, seen := newServer(t, http.StatusOK, []byte(tt.body))
			p := openrouter.New(
				openrouter.APIKey("test-key"),
				openrouter.BaseURL(server.URL+"/api/v1"),
			)

			got, err := p.CheckKey(context.Background())

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, []request{{path: "/api/v1/key", auth: "Bearer test-key"}}, *seen)
		})
	}
}

func TestCheckKey_Error(t *testing.T) {
	server, _ := newServer(t, http.StatusUnauthorized, []byte(`{"error":{"message":"User not found.","code":401}}`))
	p := openrouter.New(openrouter.APIKey("bad"), openrouter.BaseURL(server.URL))

	_, err := p.CheckKey(context.Background())

	require.ErrorContains(t, err, "openrouter: check key: 401 Unauthorized: ")
	require.ErrorContains(t, err, "User not found.")
	var status *httpget.StatusError
	require.ErrorAs(t, err, &status)
	assert.Equal(t, http.StatusUnauthorized, status.Code)
}
