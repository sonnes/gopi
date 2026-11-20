package catalog_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/catalog"
)

// voiceProvider implements [ai.SpeechProvider] and
// [ai.TranscriptionProvider], and no text.
type voiceProvider struct{}

func (voiceProvider) GenerateSpeech(
	_ context.Context,
	_ ai.Model,
	_ string,
	_ ai.Options,
) (*ai.SpeechResponse, error) {
	return &ai.SpeechResponse{}, nil
}

func (voiceProvider) Transcribe(
	_ context.Context,
	_ ai.Model,
	_ ai.File,
	_ ai.Options,
) (*ai.Transcription, error) {
	return &ai.Transcription{}, nil
}

var (
	speaks  = ai.Model{ID: "speaks", Output: []ai.Output{ai.OutputSpeech}}
	listens = ai.Model{ID: "listens", Output: []ai.Output{ai.OutputTranscript}}
)

func TestCatalog_RegisterProvider(t *testing.T) {
	c := catalog.New()

	err := c.RegisterProvider("voice", voiceProvider{}, speaks, listens)
	require.NoError(t, err)

	speech, err := c.SpeechModel("voice/speaks")
	require.NoError(t, err)
	assert.Equal(t, speaks, speech.Model)
	transcription, err := c.TranscriptionModel("voice/listens")
	require.NoError(t, err)
	assert.Equal(t, listens, transcription.Model)

	_, err = c.LanguageModel("voice/speaks")
	assert.ErrorIs(t, err, errors.ErrUnsupported, "a provider with no text has no language model")
	_, _, err = c.Driver("voice/speaks")
	assert.ErrorIs(t, err, catalog.ErrNoDriver)
}

func TestCatalog_RegisterProviderNoCapability(t *testing.T) {
	c := catalog.New()

	err := c.RegisterProvider("none", struct{}{}, speaks)

	require.ErrorIs(t, err, errors.ErrUnsupported)
	assert.Empty(t, c.Specs(), "a provider with no capability adds no model")
}

func TestCatalog_RegisterProviderKeepsCapabilities(t *testing.T) {
	c := catalog.New()
	c.RegisterTextProvider("both", textProvider{}, alpha)

	require.NoError(t, c.RegisterProvider("both", voiceProvider{}, speaks))

	_, err := c.LanguageModel("both/alpha")
	assert.NoError(t, err, "a later provider keeps the text of an earlier one")
	_, err = c.SpeechModel("both/speaks")
	assert.NoError(t, err)
}

func TestCatalog_MediaModelOutput(t *testing.T) {
	c := catalog.New()
	require.NoError(t, c.RegisterProvider("voice", voiceProvider{}, speaks, listens))

	_, speechErr := c.SpeechModel("voice/listens")
	_, transcriptionErr := c.TranscriptionModel("voice/speaks")

	require.ErrorIs(t, speechErr, errors.ErrUnsupported)
	assert.ErrorContains(t, speechErr, `"voice/listens"`)
	require.ErrorIs(t, transcriptionErr, errors.ErrUnsupported)
	assert.ErrorContains(t, transcriptionErr, `"voice/speaks"`)
}

func TestCatalog_MediaModelNoProvider(t *testing.T) {
	c := newCatalog()

	_, speechErr := c.SpeechModel("one/alpha")
	_, transcriptionErr := c.TranscriptionModel("one/alpha")

	assert.ErrorIs(t, speechErr, errors.ErrUnsupported)
	assert.ErrorIs(t, transcriptionErr, errors.ErrUnsupported)
}

func TestCatalog_DriverRefusesMediaModel(t *testing.T) {
	c := catalog.New()
	paints := ai.Model{ID: "paints", Output: []ai.Output{ai.OutputImage}}
	require.NoError(t, c.RegisterProvider("paint", imageProvider{}, paints))
	c.DefaultDriver(nil)

	_, _, err := c.Driver("paint/paints")

	assert.ErrorIs(t, err, errors.ErrUnsupported, "an image model starts no agent")
}
