package ai

import "context"

// TranscriptionProvider writes the speech of an audio file as text.
type TranscriptionProvider interface {
	Transcribe(
		ctx context.Context,
		model Model,
		audio File,
		opts Options,
	) (*Transcription, error)
}

// Transcription is the text of an audio file.
type Transcription struct {
	Text string
	// Language is the language of the speech, if the API gives it, for
	// example "en" or "eng".
	Language string
}

// TranscriptionModel is a [Model] bound to a [TranscriptionProvider].
type TranscriptionModel struct {
	Model    Model
	Provider TranscriptionProvider
}

// Transcribe writes the speech of audio as text with the bound provider.
// The MediaType of audio names its format, for example audio/mpeg.
func (tm TranscriptionModel) Transcribe(
	ctx context.Context,
	audio File,
	opts ...Options,
) (*Transcription, error) {
	return tm.Provider.Transcribe(ctx, tm.Model, audio, JoinOptions(opts...))
}
