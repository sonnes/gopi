package elevenlabs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"

	"github.com/sonnes/gopi/pkg/ai"
)

type transcriptionResponse struct {
	Text         string `json:"text"`
	LanguageCode string `json:"language_code"`
}

// Transcribe writes the speech of audio as text with the speech-to-text
// API. It reads [ai.Language]. The language of the result is the
// ISO 639-3 code that the API detects, for example "eng".
func (p *Provider) Transcribe(
	ctx context.Context,
	model ai.Model,
	audio ai.File,
	opts ai.Options,
) (*ai.Transcription, error) {
	language, _ := ai.GetOption(opts, ai.Language)
	body, contentType, err := transcriptionForm(model.ID, audio, language)
	if err != nil {
		return nil, fmt.Errorf("elevenlabs: transcription: %w", err)
	}
	endpoint := p.cfg.baseURL + "/v1/speech-to-text"

	build := func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", contentType)
		return req, nil
	}
	raw, err := p.call(ctx, opts, build)
	if err != nil {
		return nil, err
	}

	var resp transcriptionResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("elevenlabs: transcription: %w", err)
	}
	return &ai.Transcription{Text: resp.Text, Language: resp.LanguageCode}, nil
}

// transcriptionForm returns the multipart body of a transcription call
// and its content type. The file part has a name with the extension of
// the media type, because the API reads the format from the file.
func transcriptionForm(modelID string, audio ai.File, language string) ([]byte, string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if err := w.WriteField("model_id", modelID); err != nil {
		return nil, "", err
	}
	if language != "" {
		if err := w.WriteField("language_code", language); err != nil {
			return nil, "", err
		}
	}

	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", fmt.Sprintf(
		`form-data; name="file"; filename=%q`,
		"audio"+extension(audio.MediaType),
	))
	if audio.MediaType != "" {
		header.Set("Content-Type", audio.MediaType)
	}
	part, err := w.CreatePart(header)
	if err != nil {
		return nil, "", err
	}
	if _, err := part.Write(audio.Data); err != nil {
		return nil, "", err
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), w.FormDataContentType(), nil
}

// extensions are the file extensions of the common audio media types.
// The table of package mime differs between systems, so it comes
// second.
var extensions = map[string]string{
	"audio/mpeg":  ".mp3",
	"audio/mp3":   ".mp3",
	"audio/wav":   ".wav",
	"audio/x-wav": ".wav",
	"audio/ogg":   ".ogg",
	"audio/webm":  ".webm",
	"audio/flac":  ".flac",
	"audio/mp4":   ".m4a",
	"audio/x-m4a": ".m4a",
	"audio/aac":   ".aac",
}

// extension returns the file extension of mediaType, or "" if it is not
// known.
func extension(mediaType string) string {
	if ext, ok := extensions[mediaType]; ok {
		return ext
	}
	if exts, err := mime.ExtensionsByType(mediaType); err == nil && len(exts) > 0 {
		return exts[0]
	}
	return ""
}
