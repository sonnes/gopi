package ai

import "context"

// SpeechProvider speaks a text.
type SpeechProvider interface {
	GenerateSpeech(
		ctx context.Context,
		model Model,
		text string,
		opts Options,
	) (*SpeechResponse, error)
}

// SpeechResponse holds the audio of a text.
type SpeechResponse struct {
	// Audio is the audio file, in the format of MediaType.
	Audio []byte
	// MediaType names the format of Audio, for example audio/mpeg.
	MediaType string
}

// SpeechModel is a [Model] bound to a [SpeechProvider].
type SpeechModel struct {
	Model    Model
	Provider SpeechProvider
}

// GenerateSpeech speaks text with the bound provider.
func (sm SpeechModel) GenerateSpeech(
	ctx context.Context,
	text string,
	opts ...Options,
) (*SpeechResponse, error) {
	return sm.Provider.GenerateSpeech(ctx, sm.Model, text, JoinOptions(opts...))
}
