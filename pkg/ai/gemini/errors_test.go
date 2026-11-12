package gemini_test

import (
	"net/http"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/ai/gemini"
)

// errorAnswer answers with a status and an error body.
func errorAnswer(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=UTF-8")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

// arrayBody is an error body in a JSON array, as the API sends for a
// project with no credits.
const arrayBody = `[{"error":{"code":402,"message":"Your prepayment credits are depleted.","status":"RESOURCE_EXHAUSTED"}}]`

// sseBody is an error event, as the API sends for a stream request that
// it refuses.
const sseBody = "event: error\ndata: {\"error\":{\"message\":\"Request contains an invalid argument.\",\"code\":\"invalid_request\"},\"event_type\":\"error\"}\n\n"

// objectErrorBody is an error body in a JSON object.
const objectErrorBody = `{"error":{"code":400,"message":"API key not valid.","status":"INVALID_ARGUMENT"}}`

func TestAPIError(t *testing.T) {
	messages := []ai.Message{ai.UserMessage("Hi")}

	text := func(p *gemini.Provider) error {
		_, err := p.StreamText(t.Context(), fakeModel, messages, nil).Wait()
		return err
	}
	object := func(p *gemini.Provider) error {
		_, err := p.GenerateObject(t.Context(), fakeModel, messages, &jsonschema.Schema{Type: "object"}, nil)
		return err
	}
	image := func(p *gemini.Provider) error {
		_, err := p.GenerateImage(t.Context(), imageModel, "A red circle", nil)
		return err
	}
	list := func(p *gemini.Provider) error {
		_, err := p.ListModels(t.Context())
		return err
	}

	tests := []struct {
		name        string
		call        func(*gemini.Provider) error
		status      int
		body        string
		wantMessage string
	}{
		{
			name:        "text, array body",
			call:        text,
			status:      http.StatusPaymentRequired,
			body:        arrayBody,
			wantMessage: "Your prepayment credits are depleted.",
		},
		{
			name:        "object, array body",
			call:        object,
			status:      http.StatusPaymentRequired,
			body:        arrayBody,
			wantMessage: "Your prepayment credits are depleted.",
		},
		{
			name:        "text, error event",
			call:        text,
			status:      http.StatusBadRequest,
			body:        sseBody,
			wantMessage: "Request contains an invalid argument.",
		},
		{
			name:        "text, object body",
			call:        text,
			status:      http.StatusBadRequest,
			body:        objectErrorBody,
			wantMessage: "API key not valid.",
		},
		{
			name:        "image, array body",
			call:        image,
			status:      http.StatusPaymentRequired,
			body:        arrayBody,
			wantMessage: "Your prepayment credits are depleted.",
		},
		{
			name:        "list, object body",
			call:        list,
			status:      http.StatusBadRequest,
			body:        objectErrorBody,
			wantMessage: "API key not valid.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, _ := fakeProvider(t, errorAnswer(tt.status, tt.body))

			err := tt.call(p)

			var e *gemini.Error
			require.ErrorAs(t, err, &e)
			assert.Equal(t, tt.status, e.StatusCode)
			assert.Equal(t, tt.wantMessage, e.Message)
		})
	}
}
