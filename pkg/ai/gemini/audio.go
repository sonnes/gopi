package gemini

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"

	"google.golang.org/genai/interactions/models/interactions"

	"github.com/sonnes/gopi/pkg/ai"
)

var (
	_ ai.SpeechProvider        = (*Provider)(nil)
	_ ai.TranscriptionProvider = (*Provider)(nil)
)

// defaultVoice is the voice of a speech call that sets no [ai.Voice].
const defaultVoice = "Kore"

// The format of raw PCM audio when the content does not give it. A TTS
// model gives 24 kHz mono 16-bit audio.
const (
	defaultSampleRate = 24000
	defaultChannels   = 1
	pcmBitsPerSample  = 16
)

// wavMediaType is the media type of the audio of a speech call.
const wavMediaType = "audio/wav"

// transcribeInstruction asks a text model for the words of the audio
// and nothing else.
const transcribeInstruction = "Transcribe the speech in this audio word for word. " +
	"Give only the transcript, with no heading, no notes, and no timestamps. " +
	"If the audio has no speech, give an empty answer."

// GenerateSpeech speaks text with a TTS model on the Interactions API,
// such as gemini-3.8-flash-tts. It reads [ai.Voice], and the default
// voice is Kore. The result is a WAV file. If the API gives raw PCM, the
// provider adds the WAV header.
func (p *Provider) GenerateSpeech(
	ctx context.Context,
	model ai.Model,
	text string,
	opts ai.Options,
) (*ai.SpeechResponse, error) {
	if p.err != nil {
		return nil, p.err
	}
	body, err := buildSpeechRequest(model, text, opts)
	if err != nil {
		return nil, err
	}

	res, err := p.interact(ctx, body, opts)
	if err != nil {
		return nil, err
	}
	interaction := res.Interaction
	if interaction == nil {
		return nil, errors.New("gemini: the response has no interaction")
	}
	audio := outputAudio(interaction.Steps)
	if audio == nil {
		return nil, fmt.Errorf("gemini: no audio in the interaction, status %q", interaction.Status)
	}
	return speechOf(audio)
}

func buildSpeechRequest(
	model ai.Model,
	text string,
	opts ai.Options,
) (interactions.CreateModelInteraction, error) {
	steps, err := convertMessages([]ai.Message{ai.UserMessage(text)})
	if err != nil {
		return interactions.CreateModelInteraction{}, err
	}

	voice := defaultVoice
	if v, ok := ai.GetOption(opts, ai.Voice); ok && v != "" {
		voice = v
	}
	speech := interactions.NewSpeechConfigUnion([]interactions.SpeechConfig{{Voice: &voice}})

	format := interactions.NewResponseFormat(interactions.AudioResponseFormat{})
	responseFormat := interactions.NewCreateModelInteractionResponseFormat(format)

	store := false
	streaming := false
	input := interactions.NewInteractionsInput(steps)
	return interactions.CreateModelInteraction{
		Model:            interactions.Model(model.ID),
		Store:            &store,
		Stream:           &streaming,
		Input:            &input,
		ResponseFormat:   &responseFormat,
		GenerationConfig: &interactions.GenerationConfig{SpeechConfig: &speech},
	}, nil
}

// outputAudio returns the first audio content of the model output, or
// nil.
func outputAudio(steps []interactions.Step) *interactions.AudioContent {
	for _, step := range steps {
		if step.ModelOutputStep == nil {
			continue
		}
		for _, c := range step.ModelOutputStep.Content {
			if c.AudioContent != nil && c.AudioContent.Data != nil {
				return c.AudioContent
			}
		}
	}
	return nil
}

// speechOf decodes the audio, and wraps raw PCM in a WAV header, so the
// result is always a file that a player opens.
func speechOf(audio *interactions.AudioContent) (*ai.SpeechResponse, error) {
	data, err := base64.StdEncoding.DecodeString(*audio.Data)
	if err != nil {
		return nil, fmt.Errorf("gemini: decode audio: %w", err)
	}

	mediaType := wavMediaType
	if audio.MimeType != nil {
		mediaType = string(*audio.MimeType)
	}
	if !isPCM(mediaType) {
		return &ai.SpeechResponse{Audio: data, MediaType: mediaType}, nil
	}

	rate := defaultSampleRate
	if audio.SampleRate != nil && *audio.SampleRate > 0 {
		rate = *audio.SampleRate
	}
	channels := defaultChannels
	if audio.Channels != nil && *audio.Channels > 0 {
		channels = *audio.Channels
	}
	return &ai.SpeechResponse{
		Audio:     wav(data, rate, channels),
		MediaType: wavMediaType,
	}, nil
}

// isPCM reports whether mediaType is raw 16-bit PCM, such as
// "audio/l16;rate=24000" or "audio/pcm".
func isPCM(mediaType string) bool {
	base, _, _ := strings.Cut(mediaType, ";")
	switch strings.TrimSpace(strings.ToLower(base)) {
	case "audio/l16", "audio/pcm":
		return true
	default:
		return false
	}
}

// wav returns pcm, 16-bit signed little-endian samples, with the
// 44-byte header of a WAV file.
func wav(pcm []byte, rate, channels int) []byte {
	blockAlign := channels * pcmBitsPerSample / 8
	out := make([]byte, 0, 44+len(pcm))
	out = append(out, "RIFF"...)
	out = binary.LittleEndian.AppendUint32(out, uint32(36+len(pcm)))
	out = append(out, "WAVEfmt "...)
	out = binary.LittleEndian.AppendUint32(out, 16)
	out = binary.LittleEndian.AppendUint16(out, 1) // PCM
	out = binary.LittleEndian.AppendUint16(out, uint16(channels))
	out = binary.LittleEndian.AppendUint32(out, uint32(rate))
	out = binary.LittleEndian.AppendUint32(out, uint32(rate*blockAlign))
	out = binary.LittleEndian.AppendUint16(out, uint16(blockAlign))
	out = binary.LittleEndian.AppendUint16(out, pcmBitsPerSample)
	out = append(out, "data"...)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(pcm)))
	return append(out, pcm...)
}

// Transcribe writes the speech of audio as text with a text model, such
// as gemini-3.8-flash. Gemini has no transcription endpoint, so the
// provider sends the audio with a fixed instruction and returns the text
// of the answer. [ai.Language] adds the language to the instruction.
// The Transcription has no language, because the model does not give
// one.
func (p *Provider) Transcribe(
	ctx context.Context,
	model ai.Model,
	audio ai.File,
	opts ai.Options,
) (*ai.Transcription, error) {
	if p.err != nil {
		return nil, p.err
	}
	if kindOf(audio.MediaType) != fileAudio {
		return nil, fmt.Errorf(
			"gemini: transcription takes audio, not %q: %w",
			audio.MediaType,
			errors.ErrUnsupported,
		)
	}
	body, err := buildTranscribeRequest(model, audio, opts)
	if err != nil {
		return nil, err
	}

	res, err := p.interact(ctx, body, opts)
	if err != nil {
		return nil, err
	}
	interaction := res.Interaction
	if interaction == nil {
		return nil, errors.New("gemini: the response has no interaction")
	}
	if _, err := stopReason(interaction.Status, false); err != nil {
		return nil, err
	}
	return &ai.Transcription{Text: strings.TrimSpace(outputText(interaction.Steps))}, nil
}

func buildTranscribeRequest(
	model ai.Model,
	audio ai.File,
	opts ai.Options,
) (interactions.CreateModelInteraction, error) {
	instruction := transcribeInstruction
	if lang, ok := ai.GetOption(opts, ai.Language); ok && lang != "" {
		instruction += " The speech is in the language with the ISO 639-1 code " + lang + "."
	}
	msg := ai.Message{
		Role:    ai.RoleUser,
		Content: []ai.Content{ai.Text{Text: instruction}, audio},
	}
	body, err := baseRequest(model, []ai.Message{msg}, opts)
	if err != nil {
		return body, err
	}
	streaming := false
	body.Stream = &streaming
	return body, nil
}
