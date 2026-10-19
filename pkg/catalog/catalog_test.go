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

// textProvider implements only [ai.TextProvider].
type textProvider struct{}

func (textProvider) StreamText(
	_ context.Context,
	_ ai.Model,
	_ []ai.Message,
	_ ai.Options,
) *ai.EventStream {
	return ai.NewEventStream(func(func(ai.Event)) (*ai.Message, error) {
		msg := ai.AssistantMessage()
		return &msg, nil
	})
}

// imageProvider implements [ai.TextProvider] and [ai.ImageProvider].
type imageProvider struct {
	textProvider
}

func (imageProvider) GenerateImage(
	_ context.Context,
	_ ai.Model,
	_ string,
	_ ai.Options,
) (*ai.ImageResponse, error) {
	return &ai.ImageResponse{}, nil
}

// speechProvider implements [ai.TextProvider] and [ai.SpeechProvider].
type speechProvider struct {
	textProvider
}

func (speechProvider) GenerateSpeech(
	_ context.Context,
	_ ai.Model,
	_ string,
	_ ai.Options,
) (*ai.SpeechResponse, error) {
	return &ai.SpeechResponse{}, nil
}

var (
	alpha  = ai.Model{ID: "alpha", Name: "Alpha", MaxTokens: 100}
	beta   = ai.Model{ID: "beta", Name: "Beta", MaxTokens: 200}
	shared = ai.Model{ID: "shared", Name: "Shared"}
	pixel  = ai.Model{ID: "pixel", Name: "Pixel"}
)

// newCatalog returns a catalog with two text providers and one image
// provider. "shared" is served by both text providers.
func newCatalog() *catalog.Catalog {
	c := catalog.New()
	c.RegisterTextProvider("one", textProvider{}, alpha, shared)
	c.RegisterTextProvider("two", textProvider{}, beta, shared)
	c.RegisterImageProvider("paint", imageProvider{}, pixel)
	return c
}

func TestCatalog_Model(t *testing.T) {
	tests := []struct {
		name    string
		spec    string
		want    ai.Model
		wantErr string
	}{
		{
			name: "full spec",
			spec: "one/alpha",
			want: alpha,
		},
		{
			name: "bare ID with one provider",
			spec: "beta",
			want: beta,
		},
		{
			name: "unknown ID on a known provider passes through",
			spec: "one/next",
			want: ai.Model{ID: "next", Name: "next"},
		},
		{
			name: "model ID with a slash",
			spec: "one/vendor/next",
			want: ai.Model{ID: "vendor/next", Name: "vendor/next"},
		},
		{
			name:    "bare ID with two providers",
			spec:    "shared",
			wantErr: `catalog: ambiguous model "shared": use one of one/shared, two/shared`,
		},
		{
			name:    "unknown bare ID",
			spec:    "nope",
			wantErr: `catalog: unknown model "nope"`,
		},
		{
			name:    "unknown provider",
			spec:    "nope/alpha",
			wantErr: `catalog: unknown model "nope/alpha"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newCatalog()

			got, err := c.Model(tt.spec)

			if tt.wantErr != "" {
				require.EqualError(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestCatalog_LanguageModel(t *testing.T) {
	c := newCatalog()

	lm, err := c.LanguageModel("one/alpha")

	require.NoError(t, err)
	assert.Equal(t, alpha, lm.Model)
	assert.Equal(t, textProvider{}, lm.Provider)
}

func TestCatalog_ImageModel(t *testing.T) {
	c := newCatalog()

	im, err := c.ImageModel("pixel")

	require.NoError(t, err)
	assert.Equal(t, pixel, im.Model)
	assert.Equal(t, imageProvider{}, im.Provider)
}

func TestCatalog_SpeechModel(t *testing.T) {
	c := catalog.New()
	c.RegisterSpeechProvider("voice", speechProvider{}, alpha)

	sm, err := c.SpeechModel("voice/alpha")

	require.NoError(t, err)
	assert.Equal(t, alpha, sm.Model)
}

func TestCatalog_MissingCapability(t *testing.T) {
	c := newCatalog()

	_, textErr := c.LanguageModel("paint/pixel")
	_, imageErr := c.ImageModel("one/alpha")
	_, speechErr := c.SpeechModel("one/alpha")

	assert.EqualError(t, textErr, `catalog: provider "paint" does not support text generation`)
	assert.EqualError(t, imageErr, `catalog: provider "one" does not support image generation`)
	assert.EqualError(t, speechErr, `catalog: provider "one" does not support speech generation`)
}

func TestCatalog_RegisterModel(t *testing.T) {
	c := newCatalog()
	gamma := ai.Model{ID: "gamma", Name: "Gamma", MaxTokens: 300}

	c.RegisterModel("one", gamma)

	got, err := c.Model("gamma")
	require.NoError(t, err)
	assert.Equal(t, gamma, got)
}

func TestCatalog_Specs(t *testing.T) {
	c := newCatalog()

	got := c.Specs()

	assert.Equal(t, []string{
		"one/alpha",
		"one/shared",
		"paint/pixel",
		"two/beta",
		"two/shared",
	}, got)
}

// lister is a text provider that lists models, or fails.
type lister struct {
	textProvider
	models []ai.Model
	err    error
	calls  int
}

func (l *lister) ListModels(context.Context) ([]ai.Model, error) {
	l.calls++
	return l.models, l.err
}

func TestCatalog_FetchModels(t *testing.T) {
	c := catalog.New()
	listed := &lister{models: []ai.Model{
		// The API has no name for alpha, and a new cap.
		{ID: "alpha", MaxTokens: 150},
		{ID: "gamma", Name: "Gamma"},
	}}
	broken := &lister{err: errors.New("401 Unauthorized")}
	c.RegisterTextProvider("one", listed, alpha)
	c.RegisterTextProvider("two", broken, beta)
	c.RegisterTextProvider("quiet", textProvider{}, shared)

	got := c.FetchModels(t.Context())

	assert.Equal(t, []catalog.FetchResult{
		{Provider: "one", Models: 2},
		{Provider: "two", Err: broken.err},
	}, got)
	assert.Equal(t, []string{"one/alpha", "one/gamma", "quiet/shared", "two/beta"}, c.Specs())
	merged, err := c.Model("one/alpha")
	require.NoError(t, err)
	assert.Equal(t, ai.Model{ID: "alpha", Name: "Alpha", MaxTokens: 150}, merged,
		"a field that the API leaves zero keeps the registered value")
}

func TestCatalog_FetchModels_KeepsPricesAndLevels(t *testing.T) {
	registered := ai.Model{
		ID:             "opus",
		Name:           "Opus",
		ContextWindow:  500_000,
		ThinkingLevels: []ai.ThinkingLevel{ai.ThinkingOff, ai.ThinkingHigh},
		Pricing:        ai.Pricing{Input: 5, Output: 25},
	}
	c := catalog.New()
	c.RegisterTextProvider("one", &lister{models: []ai.Model{{
		ID:             "opus",
		ContextWindow:  1_000_000,
		Input:          []ai.Modality{ai.ModalityText, ai.ModalityImage},
		Reasoning:      true,
		ThinkingLevels: []ai.ThinkingLevel{ai.ThinkingHigh},
	}}}, registered)

	c.FetchModels(t.Context())

	merged, err := c.Model("one/opus")
	require.NoError(t, err)
	assert.Equal(t, ai.Model{
		ID:             "opus",
		Name:           "Opus",
		ContextWindow:  1_000_000,
		Input:          []ai.Modality{ai.ModalityText, ai.ModalityImage},
		Reasoning:      true,
		ThinkingLevels: []ai.ThinkingLevel{ai.ThinkingOff, ai.ThinkingHigh},
		Pricing:        ai.Pricing{Input: 5, Output: 25},
	}, merged, "the list adds limits and capabilities, and the registered prices and levels stay")
}

func TestCatalog_FetchModels_OneCallPerProvider(t *testing.T) {
	c := catalog.New()
	l := &lister{models: []ai.Model{{ID: "alpha"}}}
	c.RegisterTextProvider("one", l)
	c.RegisterImageProvider("one", imageProvider{})

	c.FetchModels(t.Context())

	assert.Equal(t, 1, l.calls)
}
