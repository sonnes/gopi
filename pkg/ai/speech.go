package ai

import "context"

// SpeechProvider generates speech from text.
type SpeechProvider interface {
	GenerateSpeech(
		ctx context.Context,
		model Model,
		text string,
		opts Options,
	) (*SpeechResponse, error)
}

// SpeechResponse holds the generated audio.
type SpeechResponse struct {
	Audio     []byte
	MediaType string
}

// SpeechModel is a [Model] bound to a [SpeechProvider].
type SpeechModel struct {
	Model    Model
	Provider SpeechProvider
}

// GenerateSpeech generates audio of text from the bound provider.
func (sm SpeechModel) GenerateSpeech(
	ctx context.Context,
	text string,
	opts ...Options,
) (*SpeechResponse, error) {
	return sm.Provider.GenerateSpeech(ctx, sm.Model, text, JoinOptions(opts...))
}
