package openai_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/ai/openai"
)

// audioServer serves handler and returns a provider that calls it.
func audioServer(t *testing.T, handler http.HandlerFunc) *openai.Provider {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return openai.New(openai.APIKey("test-key"), openai.BaseURL(srv.URL+"/v1"))
}

func TestGenerateSpeech(t *testing.T) {
	tests := []struct {
		name      string
		opts      []ai.Options
		wantVoice string
	}{
		{name: "default voice", wantVoice: "marin"},
		{name: "voice option", opts: []ai.Options{ai.Voice("cedar")}, wantVoice: "cedar"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var path string
			var body map[string]any
			p := audioServer(t, func(w http.ResponseWriter, r *http.Request) {
				path = r.URL.Path
				require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
				w.Header().Set("Content-Type", "audio/mpeg")
				w.Write([]byte("ID3 audio"))
			})
			sm := ai.SpeechModel{Model: ai.Model{ID: "gpt-4o-mini-tts"}, Provider: p}

			resp, err := sm.GenerateSpeech(t.Context(), "Hello there", tt.opts...)

			require.NoError(t, err)
			assert.Equal(t, "/v1/audio/speech", path)
			assert.Equal(t, map[string]any{
				"input":           "Hello there",
				"model":           "gpt-4o-mini-tts",
				"voice":           tt.wantVoice,
				"response_format": "mp3",
			}, body)
			assert.Equal(t, []byte("ID3 audio"), resp.Audio)
			assert.Equal(t, "audio/mpeg", resp.MediaType)
		})
	}
}

func TestGenerateSpeech_Error(t *testing.T) {
	p := audioServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":{"message":"bad voice","type":"invalid_request_error"}}`))
	})

	_, err := p.GenerateSpeech(t.Context(), ai.Model{ID: "gpt-4o-mini-tts"}, "Hi", nil)

	var apiErr *openai.Error
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
}

func TestTranscribe(t *testing.T) {
	tests := []struct {
		name         string
		mediaType    string
		opts         []ai.Options
		response     string
		wantFile     string
		wantLanguage string
		wantSent     string
	}{
		{
			name:         "mp3 with detected language",
			mediaType:    "audio/mpeg",
			response:     `{"text":"Hello there","languages":[{"code":"en"}]}`,
			wantFile:     "audio.mp3",
			wantLanguage: "en",
		},
		{
			name:      "wav with language option",
			mediaType: "audio/wav",
			opts:      []ai.Options{ai.Language("fr")},
			response:  `{"text":"Hello there"}`,
			wantFile:  "audio.wav",
			wantSent:  "fr",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var path, fileName, fileType, model, language string
			var data []byte
			p := audioServer(t, func(w http.ResponseWriter, r *http.Request) {
				path = r.URL.Path
				require.NoError(t, r.ParseMultipartForm(1<<20))
				model = r.FormValue("model")
				language = r.FormValue("language")
				f, header, err := r.FormFile("file")
				require.NoError(t, err)
				defer f.Close()
				fileName = header.Filename
				fileType = header.Header.Get("Content-Type")
				data, _ = io.ReadAll(f)
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(tt.response))
			})
			tm := ai.TranscriptionModel{Model: ai.Model{ID: "gpt-transcribe"}, Provider: p}
			audio := ai.File{MediaType: tt.mediaType, Data: []byte("sound")}

			got, err := tm.Transcribe(t.Context(), audio, tt.opts...)

			require.NoError(t, err)
			assert.Equal(t, "/v1/audio/transcriptions", path)
			assert.Equal(t, "gpt-transcribe", model)
			assert.Equal(t, tt.wantSent, language)
			assert.Equal(t, tt.wantFile, fileName)
			assert.Equal(t, tt.mediaType, fileType)
			assert.Equal(t, []byte("sound"), data)
			assert.Equal(t, &ai.Transcription{Text: "Hello there", Language: tt.wantLanguage}, got)
		})
	}
}
