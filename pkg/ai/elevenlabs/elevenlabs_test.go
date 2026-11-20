package elevenlabs_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/ai/elevenlabs"
)

// serve returns a provider that calls handler, with the key "test-key".
func serve(t *testing.T, handler http.HandlerFunc) *elevenlabs.Provider {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return elevenlabs.New(
		elevenlabs.APIKey("test-key"),
		elevenlabs.BaseURL(srv.URL+"/"),
		elevenlabs.HTTPClient(srv.Client()),
	)
}

var (
	voiceModel  = ai.Model{ID: "eleven_v4"}
	scribeModel = ai.Model{ID: "scribe_v3"}
)

func TestGenerateSpeech(t *testing.T) {
	var gotPath, gotQuery, gotKey, gotType string
	var gotBody map[string]string
	p := serve(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query().Get("output_format")
		gotKey = r.Header.Get("xi-api-key")
		gotType = r.Header.Get("Content-Type")
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		_, _ = w.Write([]byte("mp3 bytes"))
	})

	resp, err := p.GenerateSpeech(t.Context(), voiceModel, "Hello", ai.Voice("voice-1"))

	require.NoError(t, err)
	assert.Equal(t, "/v1/text-to-speech/voice-1", gotPath)
	assert.Equal(t, "mp3_44100_128", gotQuery)
	assert.Equal(t, "test-key", gotKey)
	assert.Equal(t, "application/json", gotType)
	assert.Equal(t, map[string]string{"text": "Hello", "model_id": "eleven_v4"}, gotBody)
	assert.Equal(t, []byte("mp3 bytes"), resp.Audio)
	assert.Equal(t, "audio/mpeg", resp.MediaType)
}

func TestGenerateSpeechDefaultVoice(t *testing.T) {
	var gotPath string
	p := serve(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte("mp3"))
	})

	_, err := p.GenerateSpeech(t.Context(), voiceModel, "Hello", nil)

	require.NoError(t, err)
	assert.Equal(t, "/v1/text-to-speech/"+elevenlabs.DefaultVoice, gotPath)
}

func TestTranscribe(t *testing.T) {
	var gotPath, gotKey, gotModel, gotLanguage, gotFile, gotName, gotFileType string
	p := serve(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotKey = r.Header.Get("xi-api-key")
		require.NoError(t, r.ParseMultipartForm(1<<20))
		gotModel = r.FormValue("model_id")
		gotLanguage = r.FormValue("language_code")
		f, header, err := r.FormFile("file")
		require.NoError(t, err)
		data, err := io.ReadAll(f)
		require.NoError(t, err)
		gotFile = string(data)
		gotName = header.Filename
		gotFileType = header.Header.Get("Content-Type")
		_, _ = w.Write([]byte(`{"text":"Hello there","language_code":"eng","language_probability":0.98,"words":[]}`))
	})
	audio := ai.File{MediaType: "audio/mpeg", Data: []byte("mp3 bytes")}

	got, err := p.Transcribe(t.Context(), scribeModel, audio, ai.Language("en"))

	require.NoError(t, err)
	assert.Equal(t, "/v1/speech-to-text", gotPath)
	assert.Equal(t, "test-key", gotKey)
	assert.Equal(t, "scribe_v3", gotModel)
	assert.Equal(t, "en", gotLanguage)
	assert.Equal(t, "mp3 bytes", gotFile)
	assert.Equal(t, "audio.mp3", gotName)
	assert.Equal(t, "audio/mpeg", gotFileType)
	assert.Equal(t, &ai.Transcription{Text: "Hello there", Language: "eng"}, got)
}

func TestTranscribeNoLanguage(t *testing.T) {
	var hasLanguage bool
	p := serve(t, func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseMultipartForm(1<<20))
		_, hasLanguage = r.MultipartForm.Value["language_code"]
		_, _ = w.Write([]byte(`{"text":"Hi"}`))
	})

	_, err := p.Transcribe(t.Context(), scribeModel, ai.File{MediaType: "audio/wav", Data: []byte("x")}, nil)

	require.NoError(t, err)
	assert.False(t, hasLanguage, "the API detects the language when the call sets none")
}

func TestErrorStatus(t *testing.T) {
	p := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"detail":[{"msg":"bad voice"}]}`))
	})

	_, err := p.GenerateSpeech(t.Context(), voiceModel, "Hello", nil)

	var apiErr *elevenlabs.Error
	require.True(t, errors.As(err, &apiErr), "got %v", err)
	assert.Equal(t, http.StatusUnprocessableEntity, apiErr.StatusCode)
	assert.Contains(t, apiErr.Body, "bad voice")
	assert.ErrorContains(t, err, "elevenlabs")
}

func TestRetry(t *testing.T) {
	calls := 0
	p := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte("mp3"))
	})

	resp, err := p.GenerateSpeech(t.Context(), voiceModel, "Hello", ai.MaxRetries(1))

	require.NoError(t, err)
	assert.Equal(t, 2, calls)
	assert.Equal(t, []byte("mp3"), resp.Audio)
}

const modelsBody = `[
	{"model_id":"eleven_v4","name":"Eleven v4","can_do_text_to_speech":true},
	{"model_id":"eleven_multilingual_sts_v2","name":"Multilingual STS v2","can_do_text_to_speech":false}
]`

func TestListModels(t *testing.T) {
	var gotPath, gotKey string
	p := serve(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotKey = r.Header.Get("xi-api-key")
		_, _ = w.Write([]byte(modelsBody))
	})

	got, err := p.ListModels(t.Context())

	require.NoError(t, err)
	assert.Equal(t, "/v1/models", gotPath)
	assert.Equal(t, "test-key", gotKey)
	assert.Equal(t, []ai.Model{{
		ID:     "eleven_v4",
		Name:   "Eleven v4",
		Input:  []ai.Modality{ai.ModalityText},
		Output: []ai.Output{ai.OutputSpeech},
	}}, got)
}

func TestCheckKey(t *testing.T) {
	p := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(modelsBody))
	})

	info, err := p.CheckKey(t.Context())

	require.NoError(t, err)
	assert.Equal(t, ai.KeyInfo{}, info)
}

func TestCheckKeyRefused(t *testing.T) {
	p := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"detail":{"status":"invalid_api_key"}}`))
	})

	_, err := p.CheckKey(t.Context())

	var apiErr *elevenlabs.Error
	require.True(t, errors.As(err, &apiErr), "got %v", err)
	assert.Equal(t, http.StatusUnauthorized, apiErr.StatusCode)
}
