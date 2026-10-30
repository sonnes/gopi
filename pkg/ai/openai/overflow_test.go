package openai_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/ai/openai"
)

func TestStreamText_ContextExceeded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":{"code":"context_length_exceeded","message":"Your input exceeds the context window of this model."}}`)
	}))
	t.Cleanup(server.Close)
	p := openai.New(openai.APIKey("test-key"), openai.BaseURL(server.URL+"/v1"))

	_, err := p.StreamText(
		t.Context(),
		ai.Model{ID: "m"},
		[]ai.Message{ai.UserMessage("Hi")},
		nil,
	).Wait()

	assert.ErrorIs(t, err, ai.ErrContextExceeded)
}
