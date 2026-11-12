package gemini_test

import (
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
)

// imageBody is a response with one PNG image. "cG5n" is "png" in
// base64.
const imageBody = `{"candidates": [{"content": {"role": "model", "parts": [
	{"text": "Here is the circle."},
	{"inlineData": {"mimeType": "image/png", "data": "cG5n"}}
]}, "finishReason": "STOP"}]}`

var imageModel = ai.Model{ID: "gemini-nano-banana-2.1"}

func TestGenerateImage(t *testing.T) {
	p, body := fakeProvider(t, answer(imageBody))

	resp, err := p.GenerateImage(t.Context(), imageModel, "A red circle", ai.ImageSize("2K"))
	require.NoError(t, err)

	assert.Equal(t, []ai.GeneratedImage{{Data: []byte("png"), MediaType: "image/png"}}, resp.Images)

	var got struct {
		Contents []struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"contents"`
		Config struct {
			Modalities []string `json:"responseModalities"`
			Image      struct {
				Size string `json:"imageSize"`
			} `json:"imageConfig"`
		} `json:"generationConfig"`
	}
	require.NoError(t, json.Unmarshal(body(), &got))
	require.Len(t, got.Contents, 1)
	assert.Equal(t, "A red circle", got.Contents[0].Parts[0].Text)
	assert.Equal(t, []string{"IMAGE"}, got.Config.Modalities)
	assert.Equal(t, "2K", got.Config.Image.Size)
}

func TestGenerateImage_Count(t *testing.T) {
	var calls atomic.Int32
	p, _ := fakeProvider(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		answer(imageBody)(w, r)
	})

	resp, err := p.GenerateImage(t.Context(), imageModel, "A red circle", ai.ImageCount(3))
	require.NoError(t, err)

	assert.Len(t, resp.Images, 3)
	assert.Equal(t, int32(3), calls.Load())
}

func TestGenerateImage_SizeAsGiven(t *testing.T) {
	p, body := fakeProvider(t, answer(imageBody))

	_, err := p.GenerateImage(t.Context(), imageModel, "A red circle", ai.ImageSize("1024x1024"))
	require.NoError(t, err)

	assert.Contains(t, string(body()), `"imageSize":"1024x1024"`)
}

func TestGenerateImage_Headers(t *testing.T) {
	var got atomic.Value
	p, _ := fakeProvider(t, func(w http.ResponseWriter, r *http.Request) {
		got.Store(r.Header.Get("X-Trace"))
		answer(imageBody)(w, r)
	})

	opts := ai.Headers(map[string]string{"X-Trace": "abc"})
	_, err := p.GenerateImage(t.Context(), imageModel, "A red circle", opts)
	require.NoError(t, err)

	assert.Equal(t, "abc", got.Load())
}

func TestGenerateImage_NoImage(t *testing.T) {
	p, _ := fakeProvider(t, answer(`{"candidates": [{"content": {"role": "model", "parts": [{"text": "No."}]}, "finishReason": "IMAGE_SAFETY"}]}`))

	_, err := p.GenerateImage(t.Context(), imageModel, "A red circle", nil)
	assert.ErrorContains(t, err, "IMAGE_SAFETY")
}

func TestGenerateImage_Retries(t *testing.T) {
	var calls atomic.Int32
	p, _ := fakeProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error": {"code": 503, "message": "busy", "status": "UNAVAILABLE"}}`))
			return
		}
		answer(imageBody)(w, r)
	})

	_, err := p.GenerateImage(t.Context(), imageModel, "A red circle", ai.MaxRetries(1))
	require.NoError(t, err)
	assert.Equal(t, int32(2), calls.Load())
}
