package openai

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"

	sdk "github.com/openai/openai-go/v3"

	"github.com/sonnes/gopi/pkg/ai"
)

// The audio capabilities of the provider, checked at compile time.
var (
	_ ai.SpeechProvider        = (*Provider)(nil)
	_ ai.TranscriptionProvider = (*Provider)(nil)
)

// defaultVoice is the voice of a speech call that sets no [ai.Voice].
// OpenAI names marin and cedar as the voices with the best quality.
const defaultVoice = "marin"

// speechMediaType is the media type of the mp3 audio that a speech call
// asks for.
const speechMediaType = "audio/mpeg"

// GenerateSpeech speaks text with the Audio API. It reads [ai.Voice]
// and returns mp3 audio.
func (p *Provider) GenerateSpeech(
	ctx context.Context,
	model ai.Model,
	text string,
	opts ai.Options,
) (*ai.SpeechResponse, error) {
	voice, ok := ai.GetOption(opts, ai.Voice)
	if !ok || voice == "" {
		voice = defaultVoice
	}
	params := sdk.AudioSpeechNewParams{
		Input:          text,
		Model:          sdk.SpeechModel(model.ID),
		Voice:          sdk.AudioSpeechNewParamsVoiceUnion{OfString: sdk.String(voice)},
		ResponseFormat: sdk.AudioSpeechNewParamsResponseFormatMP3,
	}
	reqOpts := requestOptions(opts)
	send := func() ([]byte, error) {
		resp, err := p.client.Audio.Speech.New(ctx, params, reqOpts...)
		if err != nil {
			return nil, err
		}
		return readBody(resp)
	}
	audio, err := call(ctx, opts, send)
	if err != nil {
		return nil, err
	}
	return &ai.SpeechResponse{Audio: audio, MediaType: speechMediaType}, nil
}

// readBody reads and closes the body of resp.
func readBody(resp *http.Response) ([]byte, error) {
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("openai: read speech: %w", err)
	}
	return data, nil
}

// Transcribe writes the speech of audio as text with the Audio API. It
// reads [ai.Language]. The file name of the upload carries the format
// of audio, because the API reads the format from it.
func (p *Provider) Transcribe(
	ctx context.Context,
	model ai.Model,
	audio ai.File,
	opts ai.Options,
) (*ai.Transcription, error) {
	reqOpts := requestOptions(opts)
	send := func() (*sdk.AudioTranscriptionNewResponseUnion, error) {
		// Each try reads the file from the start.
		params := sdk.AudioTranscriptionNewParams{
			File:  sdk.File(bytes.NewReader(audio.Data), audioFileName(audio.MediaType), audio.MediaType),
			Model: sdk.AudioModel(model.ID),
		}
		if lang, ok := ai.GetOption(opts, ai.Language); ok && lang != "" {
			params.Language = sdk.String(lang)
		}
		return p.client.Audio.Transcriptions.New(ctx, params, reqOpts...)
	}
	resp, err := call(ctx, opts, send)
	if err != nil {
		return nil, err
	}

	out := &ai.Transcription{Text: resp.Text, Language: resp.Language}
	if out.Language == "" && len(resp.Languages) > 0 {
		out.Language = resp.Languages[0].Code
	}
	return out, nil
}

// audioExtensions maps a media type to the extension that the API reads
// the format from, where the standard table does not have it.
var audioExtensions = map[string]string{
	"audio/mpeg":   "mp3",
	"audio/mp3":    "mp3",
	"audio/mp4":    "m4a",
	"audio/x-m4a":  "m4a",
	"audio/m4a":    "m4a",
	"audio/wav":    "wav",
	"audio/x-wav":  "wav",
	"audio/wave":   "wav",
	"audio/webm":   "webm",
	"audio/ogg":    "ogg",
	"audio/flac":   "flac",
	"audio/x-flac": "flac",
}

// audioFileName returns the name of an upload of mediaType, such as
// "audio.mp3". An unknown media type gives "audio" with no extension.
func audioFileName(mediaType string) string {
	base, _, _ := mime.ParseMediaType(mediaType)
	if ext, ok := audioExtensions[strings.ToLower(base)]; ok {
		return "audio." + ext
	}
	if exts, _ := mime.ExtensionsByType(base); len(exts) > 0 {
		return "audio" + exts[0]
	}
	return "audio"
}
