package elevenlabs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/sonnes/gopi/pkg/ai"
)

// speechFormat is the output format of a speech call: MP3 at 44.1 kHz
// and 128 kbps. Each plan of ElevenLabs has it.
const speechFormat = "mp3_44100_128"

// speechMediaType is the media type of [speechFormat].
const speechMediaType = "audio/mpeg"

type speechRequest struct {
	Text    string `json:"text"`
	ModelID string `json:"model_id"`
}

// GenerateSpeech speaks text with the text-to-speech API. It reads
// [ai.Voice] as a voice ID. The audio is MP3.
func (p *Provider) GenerateSpeech(
	ctx context.Context,
	model ai.Model,
	text string,
	opts ai.Options,
) (*ai.SpeechResponse, error) {
	voice := DefaultVoice
	if v, ok := ai.GetOption(opts, ai.Voice); ok && v != "" {
		voice = v
	}
	body, err := json.Marshal(speechRequest{Text: text, ModelID: model.ID})
	if err != nil {
		return nil, fmt.Errorf("elevenlabs: speech: %w", err)
	}
	endpoint := p.cfg.baseURL + "/v1/text-to-speech/" + url.PathEscape(voice) +
		"?output_format=" + speechFormat

	build := func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", speechMediaType)
		return req, nil
	}
	audio, err := p.call(ctx, opts, build)
	if err != nil {
		return nil, err
	}
	return &ai.SpeechResponse{Audio: audio, MediaType: speechMediaType}, nil
}
