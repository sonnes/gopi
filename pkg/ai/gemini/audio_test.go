package gemini_test

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
)

var ttsModel = ai.Model{ID: "gemini-3.8-flash-tts"}

// audioAnswer is an interaction with one audio output.
func audioAnswer(mimeType string, data []byte) string {
	return fmt.Sprintf(
		`{"id": "i1", "status": "completed", "steps": [{"type": "model_output", "content": [{"type": "audio", "mime_type": %q, "data": %q}]}]}`,
		mimeType,
		base64.StdEncoding.EncodeToString(data),
	)
}

func TestGenerateSpeech_Request(t *testing.T) {
	p, body := fakeProvider(t, answer(audioAnswer("audio/wav", []byte("RIFF"))))

	_, err := p.GenerateSpeech(t.Context(), ttsModel, "Say hi", ai.Voice("Puck"))
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(body(), &got))
	assert.Equal(t, "gemini-3.8-flash-tts", got["model"])
	assert.Equal(t, false, got["stream"])
	assert.Equal(t, map[string]any{"type": "audio"}, got["response_format"])
	config := got["generation_config"].(map[string]any)
	assert.Equal(t, []any{map[string]any{"voice": "Puck"}}, config["speech_config"])
	assert.Contains(t, string(body()), "Say hi")
}

func TestGenerateSpeech_DefaultVoice(t *testing.T) {
	p, body := fakeProvider(t, answer(audioAnswer("audio/wav", []byte("RIFF"))))

	_, err := p.GenerateSpeech(t.Context(), ttsModel, "Say hi", nil)
	require.NoError(t, err)

	assert.Contains(t, string(body()), `"voice":"Kore"`)
}

func TestGenerateSpeech_WAV(t *testing.T) {
	p, _ := fakeProvider(t, answer(audioAnswer("audio/wav", []byte("RIFFdata"))))

	got, err := p.GenerateSpeech(t.Context(), ttsModel, "Say hi", nil)
	require.NoError(t, err)

	assert.Equal(t, "audio/wav", got.MediaType)
	assert.Equal(t, []byte("RIFFdata"), got.Audio, "a WAV file stays as it is")
}

func TestGenerateSpeech_PCM(t *testing.T) {
	pcm := []byte{1, 0, 2, 0, 3, 0, 4, 0}
	p, _ := fakeProvider(t, answer(audioAnswer("audio/l16", pcm)))

	got, err := p.GenerateSpeech(t.Context(), ttsModel, "Say hi", nil)
	require.NoError(t, err)

	assert.Equal(t, "audio/wav", got.MediaType)
	require.Len(t, got.Audio, 44+len(pcm))
	assert.Equal(t, "RIFF", string(got.Audio[0:4]))
	assert.Equal(t, "WAVE", string(got.Audio[8:12]))
	assert.Equal(t, uint16(1), binary.LittleEndian.Uint16(got.Audio[22:24]), "mono")
	assert.Equal(t, uint32(24000), binary.LittleEndian.Uint32(got.Audio[24:28]), "24 kHz")
	assert.Equal(t, uint16(16), binary.LittleEndian.Uint16(got.Audio[34:36]), "16-bit")
	assert.Equal(t, uint32(len(pcm)), binary.LittleEndian.Uint32(got.Audio[40:44]))
	assert.Equal(t, pcm, got.Audio[44:])
}

func TestGenerateSpeech_NoAudio(t *testing.T) {
	p, _ := fakeProvider(t, answer(`{"id": "i1", "status": "completed", "steps": []}`))

	_, err := p.GenerateSpeech(t.Context(), ttsModel, "Say hi", nil)

	assert.ErrorContains(t, err, "no audio")
}

func TestTranscribe(t *testing.T) {
	p, body := fakeProvider(t, answer(
		`{"id": "i1", "status": "completed", "steps": [{"type": "model_output", "content": [{"type": "text", "text": " Hello there. \n"}]}]}`,
	))
	audio := ai.File{MediaType: "audio/mpeg", Data: []byte("mp3")}

	got, err := p.Transcribe(t.Context(), fakeModel, audio, ai.Language("en"))
	require.NoError(t, err)

	assert.Equal(t, "Hello there.", got.Text)
	raw := string(body())
	assert.Contains(t, raw, `"type":"audio"`)
	assert.Contains(t, raw, `"mime_type":"audio/mpeg"`)
	assert.Contains(t, raw, base64.StdEncoding.EncodeToString([]byte("mp3")))
	assert.Contains(t, raw, "Transcribe the speech")
	assert.Contains(t, raw, "ISO 639-1 code en")
	assert.Contains(t, raw, `"stream":false`)
}

func TestTranscribe_NotAudio(t *testing.T) {
	p, _ := fakeProvider(t, answer(`{}`))

	_, err := p.Transcribe(t.Context(), fakeModel, ai.File{MediaType: "image/png"}, nil)

	assert.ErrorContains(t, err, "takes audio")
}
