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
