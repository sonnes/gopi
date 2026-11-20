package openrouter_test

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

	"github.com/sonnes/gopi/internal/httpapi"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/ai/openrouter"
)

var imageModel = ai.Model{ID: "black-forest-labs/flux-3-image-klein"}

func TestGenerateImage(t *testing.T) {
	png := []byte("png bytes")
	var gotPath, gotAuth string
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"created":1,"data":[{"b64_json":%q,"media_type":"image/webp"}],"usage":{"cost":0.007}}`,
			base64.StdEncoding.EncodeToString(png))
	}))
	t.Cleanup(server.Close)
	p := openrouter.New(openrouter.APIKey("test-key"), openrouter.BaseURL(server.URL+"/api/v1"))

	got, err := p.GenerateImage(
		t.Context(),
		imageModel,
		"a red cube",
		ai.JoinOptions(
			ai.AspectRatio("9:16"),
			ai.ImageResolution("2K"),
			ai.ImageSize("1024x1536"),
			ai.ImageCount(2),
		),
	)

	require.NoError(t, err)
	assert.Equal(t, "/api/v1/images", gotPath)
	assert.Equal(t, "Bearer test-key", gotAuth)
	assert.Equal(t, map[string]any{
		"model":        "black-forest-labs/flux-3-image-klein",
		"prompt":       "a red cube",
		"aspect_ratio": "9:16",
		"resolution":   "2K",
		"size":         "1024x1536",
		"n":            float64(2),
	}, gotBody)
	assert.Equal(t, []ai.GeneratedImage{{Data: png, MediaType: "image/webp"}}, got.Images)
}

// TestGenerateImage_NoOptions checks that a call with no options sends
// only the model and the prompt, and that an image with no media type
// is a PNG.
func TestGenerateImage_NoOptions(t *testing.T) {
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		fmt.Fprintf(w, `{"created":1,"data":[{"b64_json":%q}]}`, base64.StdEncoding.EncodeToString([]byte("x")))
	}))
	t.Cleanup(server.Close)
	p := openrouter.New(openrouter.APIKey("k"), openrouter.BaseURL(server.URL))

	got, err := p.GenerateImage(t.Context(), imageModel, "a red cube", nil)

	require.NoError(t, err)
	assert.Equal(t, map[string]any{
		"model":  "black-forest-labs/flux-3-image-klein",
		"prompt": "a red cube",
	}, gotBody)
	assert.Equal(t, "image/png", got.Images[0].MediaType)
}

func TestGenerateImage_Character(t *testing.T) {
	p := openrouter.New(openrouter.APIKey("k"), openrouter.BaseURL("http://127.0.0.1:1"))

	_, err := p.GenerateImage(t.Context(), imageModel, "a red cube", ai.Character("c1"))

	require.ErrorIs(t, err, errors.ErrUnsupported)
}

func TestGenerateImage_Error(t *testing.T) {
	server, _ := newServer(t, http.StatusPaymentRequired, []byte(`{"error":{"code":402,"message":"no credits"}}`))
	p := openrouter.New(openrouter.APIKey("k"), openrouter.BaseURL(server.URL))

	_, err := p.GenerateImage(t.Context(), imageModel, "a red cube", nil)

	var status *httpapi.StatusError
	require.ErrorAs(t, err, &status)
	assert.Equal(t, http.StatusPaymentRequired, status.Code)
	assert.Contains(t, err.Error(), "openrouter: images")
}

// TestListModels_ImageModels checks that the list holds the models of
// GET /images/models with an image output, the vector models too.
func TestListModels_ImageModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/images/models" {
			fmt.Fprint(w, `{"data":[
				{"id":"google/gemini-nano-banana-2.1","name":"Google: Nano Banana 2.1",
					"architecture":{"input_modalities":["text","image"],"output_modalities":["image","text"]}},
				{"id":"recraft/recraft-v4.1-vector","name":"Recraft V4.1 Vector",
					"architecture":{"input_modalities":["text"],"output_modalities":["image"]}},
				{"id":"a/tools","name":"Tools again","architecture":{"output_modalities":["image"]}}
			]}`)
			return
		}
		if r.URL.Query().Get("output_modalities") == "decisions" {
			fmt.Fprint(w, `{"data":[]}`)
			return
		}
		fmt.Fprint(w, `{"data":[{"id":"a/tools","name":"Tools","architecture":{"output_modalities":["text"]}}]}`)
	}))
	t.Cleanup(server.Close)
	p := openrouter.New(openrouter.APIKey("k"), openrouter.BaseURL(server.URL))

	got, err := p.ListModels(t.Context())

	require.NoError(t, err)
	assert.Equal(t, []ai.Model{
		{ID: "a/tools", Name: "Tools", Output: []ai.Output{ai.OutputText}},
		{
			ID:     "google/gemini-nano-banana-2.1",
			Name:   "Google: Nano Banana 2.1",
			Input:  []ai.Modality{ai.ModalityText, ai.ModalityImage},
			Output: []ai.Output{ai.OutputImage},
		},
		{
			ID:     "recraft/recraft-v4.1-vector",
			Name:   "Recraft V4.1 Vector",
			Input:  []ai.Modality{ai.ModalityText},
			Output: []ai.Output{ai.OutputImage},
		},
	}, got)
}

// TestListModels_ImageListFails checks that the text models stay if the
// list of the image models fails.
func TestListModels_ImageListFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/images/models" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		fmt.Fprint(w, `{"data":[{"id":"a/tools","name":"Tools"}]}`)
	}))
	t.Cleanup(server.Close)
	p := openrouter.New(openrouter.APIKey("k"), openrouter.BaseURL(server.URL))

	got, err := p.ListModels(t.Context())

	require.NoError(t, err)
	assert.Equal(t, []ai.Model{{ID: "a/tools", Name: "Tools"}}, got)
}
