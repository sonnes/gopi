package fal_test

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/ai/fal"
)

// serve returns a provider that sends each call to handler, with the
// key "test-key".
func serve(t *testing.T, handler http.HandlerFunc) *fal.Provider {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return fal.New(
		fal.APIKey("test-key"),
		fal.BaseURL(srv.URL+"/"),
		fal.APIBaseURL(srv.URL+"/"),
		fal.HTTPClient(srv.Client()),
	)
}

const (
	gptImage   = "openai/gpt-image-2.6/sunburst/text-to-image"
	flux3      = "blackforestlabs/flux-3/text-to-image"
	nanoBanana = "google/nano-banana-2.1"
	maiImage   = "microsoft/mai-image-2.7/text-to-image"
	bria       = "bria/fibo-2/generate"
	recraft    = "fal-ai/recraft/v4.1/text-to-image"
)

var pngData = []byte("png bytes")

// imagesBody is an answer of a sync call with one data URI image.
var imagesBody = `{"images":[{"url":"data:image/png;base64,` +
	base64.StdEncoding.EncodeToString(pngData) +
	`","content_type":"image/png"}]}`

func TestGenerateImage(t *testing.T) {
	var gotPath, gotAuth, gotType string
	var gotBody map[string]any
	p := serve(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotType = r.Header.Get("Content-Type")
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		_, _ = w.Write([]byte(imagesBody))
	})

	resp, err := p.GenerateImage(
		t.Context(),
		ai.Model{ID: gptImage},
		"a red fox",
		ai.JoinOptions(ai.ImageCount(2), ai.AspectRatio("16:9")),
	)

	require.NoError(t, err)
	assert.Equal(t, "/"+gptImage, gotPath)
	assert.Equal(t, "Key test-key", gotAuth)
	assert.Equal(t, "application/json", gotType)
	assert.Equal(t, map[string]any{
		"prompt":     "a red fox",
		"sync_mode":  true,
		"image_size": "landscape_16_9",
		"num_images": float64(2),
	}, gotBody)
	assert.Equal(t, []ai.GeneratedImage{{
		Data:      pngData,
		MediaType: "image/png",
	}}, resp.Images)
}

func TestGenerateImageFields(t *testing.T) {
	tests := []struct {
		name  string
		model string
		opts  ai.Options
		want  map[string]any
	}{
		{
			name:  "a size is width and height",
			model: gptImage,
			opts:  ai.JoinOptions(ai.ImageSize("1024x1536"), ai.AspectRatio("1:1")),
			want: map[string]any{
				"image_size": map[string]any{"width": float64(1024), "height": float64(1536)},
			},
		},
		{
			name:  "a square ratio",
			model: gptImage,
			opts:  ai.AspectRatio("1:1"),
			want:  map[string]any{"image_size": "square_hd"},
		},
		{
			name:  "a tall ratio",
			model: gptImage,
			opts:  ai.AspectRatio("9:16"),
			want:  map[string]any{"image_size": "portrait_16_9"},
		},
		{
			name:  "a near portrait ratio",
			model: gptImage,
			opts:  ai.AspectRatio("2:3"),
			want:  map[string]any{"image_size": "portrait_4_3"},
		},
		{
			name:  "a near landscape ratio",
			model: gptImage,
			opts:  ai.AspectRatio("3:2"),
			want:  map[string]any{"image_size": "landscape_4_3"},
		},
		{
			name:  "a wide ratio",
			model: gptImage,
			opts:  ai.AspectRatio("21:9"),
			want:  map[string]any{"image_size": "landscape_16_9"},
		},
		{
			name:  "an image size model ignores the resolution",
			model: gptImage,
			opts:  ai.ImageResolution("2K"),
			want:  map[string]any{},
		},
		{
			name:  "an aspect ratio model sends the ratio and the resolution",
			model: nanoBanana,
			opts: ai.JoinOptions(
				ai.AspectRatio("9:16"),
				ai.ImageResolution("4K"),
				ai.ImageSize("1024x1024"),
			),
			want: map[string]any{"aspect_ratio": "9:16", "resolution": "4K"},
		},
		{
			name:  "a resolution takes the spelling of the model",
			model: flux3,
			opts:  ai.ImageResolution("2K"),
			want:  map[string]any{"resolution": "2k"},
		},
		{
			name:  "a resolution maps to the nearest that the model takes",
			model: bria,
			opts:  ai.ImageResolution("4K"),
			want:  map[string]any{"resolution": "2K"},
		},
		{
			name:  "an image size model with resolutions sends both",
			model: maiImage,
			opts:  ai.JoinOptions(ai.AspectRatio("4:3"), ai.ImageResolution("2K")),
			want:  map[string]any{"image_size": "landscape_4_3", "resolution": "2K"},
		},
		{
			name:  "a model with no count drops the count",
			model: flux3,
			opts:  ai.ImageCount(3),
			want:  map[string]any{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotBody map[string]any
			p := serve(t, func(w http.ResponseWriter, r *http.Request) {
				require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
				_, _ = w.Write([]byte(imagesBody))
			})

			_, err := p.GenerateImage(t.Context(), ai.Model{ID: tt.model}, "a fox", tt.opts)

			require.NoError(t, err)
			delete(gotBody, "prompt")
			delete(gotBody, "sync_mode")
			assert.Equal(t, tt.want, gotBody)
		})
	}
}

// TestGenerateImageURL checks that the provider downloads an image that
// comes back as a URL, because the model takes no sync mode. The
// download sends no key, because the file is on another host.
func TestGenerateImageURL(t *testing.T) {
	var fileAuth string
	files := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fileAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "image/webp")
		_, _ = w.Write([]byte("webp bytes"))
	}))
	t.Cleanup(files.Close)
	var gotBody map[string]any
	p := serve(t, func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		fmt.Fprintf(w, `{"images":[{"url":%q,"content_type":"image/webp"}]}`, files.URL+"/files/fox.webp")
	})

	resp, err := p.GenerateImage(t.Context(), ai.Model{ID: recraft}, "a fox", nil)

	require.NoError(t, err)
	assert.NotContains(t, gotBody, "sync_mode", "the model takes no sync mode")
	assert.Empty(t, fileAuth, "the download sends no key")
	assert.Equal(t, []ai.GeneratedImage{{
		Data:      []byte("webp bytes"),
		MediaType: "image/webp",
	}}, resp.Images)
}

func TestGenerateImageCharacter(t *testing.T) {
	calls := 0
	p := serve(t, func(http.ResponseWriter, *http.Request) { calls++ })

	_, err := p.GenerateImage(t.Context(), ai.Model{ID: gptImage}, "a fox", ai.Character("soul-1"))

	require.ErrorIs(t, err, errors.ErrUnsupported)
	assert.Zero(t, calls)
}

func TestErrorStatus(t *testing.T) {
	p := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"detail":"prompt is empty","error_type":"validation_error"}`))
	})

	_, err := p.GenerateImage(t.Context(), ai.Model{ID: gptImage}, "", nil)

	var apiErr *fal.Error
	require.True(t, errors.As(err, &apiErr), "got %v", err)
	assert.Equal(t, http.StatusUnprocessableEntity, apiErr.StatusCode)
	assert.Contains(t, apiErr.Body, "prompt is empty")
	assert.ErrorContains(t, err, "fal")
}

func TestRetryConcurrentLimit(t *testing.T) {
	calls := 0
	p := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("X-Fal-Needs-Retry", "1")
			w.Header().Set("Retry-After-Ms", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"detail":"too many requests","error_type":"concurrent_requests_limit"}`))
			return
		}
		_, _ = w.Write([]byte(imagesBody))
	})

	resp, err := p.GenerateImage(t.Context(), ai.Model{ID: gptImage}, "a fox", ai.MaxRetries(1))

	require.NoError(t, err)
	assert.Equal(t, 2, calls)
	assert.Len(t, resp.Images, 1)
}

func TestListModels(t *testing.T) {
	p := fal.New(fal.APIKey("test-key"))

	got, err := p.ListModels(t.Context())

	require.NoError(t, err)
	require.NotEmpty(t, got)
	assert.Equal(t, ai.Model{
		ID:     flux3,
		Name:   "FLUX 3",
		Input:  []ai.Modality{ai.ModalityText},
		Output: []ai.Output{ai.OutputImage},
	}, got[0])
	assert.Contains(t, got, ai.Model{
		ID:     "fal-ai/recraft/v4.1/text-to-vector",
		Name:   "Recraft V4.1 Vector",
		Input:  []ai.Modality{ai.ModalityText},
		Output: []ai.Output{ai.OutputImage},
	}, "a vector model gives an SVG")
	for _, m := range got {
		assert.NotEmpty(t, m.Name, m.ID)
		assert.Equal(t, []ai.Output{ai.OutputImage}, m.Output, m.ID)
	}
}

func TestCheckKey(t *testing.T) {
	var gotPath, gotEndpoint, gotAuth string
	p := serve(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotEndpoint = r.URL.Query().Get("endpoint_id")
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"prices":[]}`))
	})

	info, err := p.CheckKey(t.Context())

	require.NoError(t, err)
	assert.Equal(t, "/v1/models/pricing", gotPath)
	assert.Equal(t, flux3, gotEndpoint)
	assert.Equal(t, "Key test-key", gotAuth)
	assert.Equal(t, ai.KeyInfo{}, info)
}

func TestCheckKeyRefused(t *testing.T) {
	p := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"type":"authorization_error","message":"API key authentication required"}}`))
	})

	_, err := p.CheckKey(t.Context())

	var apiErr *fal.Error
	require.True(t, errors.As(err, &apiErr), "got %v", err)
	assert.Equal(t, http.StatusUnauthorized, apiErr.StatusCode)
}
