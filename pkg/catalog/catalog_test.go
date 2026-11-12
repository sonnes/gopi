package catalog_test

import (
	"context"
	"errors"
	"reflect"
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

// decisionProvider implements [ai.TextProvider] and
// [ai.DecisionProvider].
type decisionProvider struct {
	textProvider
}

func (decisionProvider) Decide(
	_ context.Context,
	_ ai.Model,
	_ []ai.Message,
	_ []ai.Question,
	_ ai.Options,
) (*ai.DecisionResponse, error) {
	return &ai.DecisionResponse{}, nil
}

var (
	alpha  = ai.Model{ID: "alpha", Name: "Alpha", MaxTokens: 100}
	beta   = ai.Model{ID: "beta", Name: "Beta", MaxTokens: 200}
	shared = ai.Model{ID: "shared", Name: "Shared"}
	pixel  = ai.Model{ID: "pixel", Name: "Pixel"}
	jev    = ai.Model{ID: "jev", Name: "Jev"}
)

// newCatalog returns a catalog with two text providers, one image
// provider, and one decision provider. "shared" is served by both text
// providers.
func newCatalog() *catalog.Catalog {
	c := catalog.New()
	c.RegisterTextProvider("one", textProvider{}, alpha, shared)
	c.RegisterTextProvider("two", textProvider{}, beta, shared)
	c.RegisterImageProvider("paint", imageProvider{}, pixel)
	c.RegisterDecisionProvider("judge", decisionProvider{}, jev)
	return c
}

func TestCatalog_Model(t *testing.T) {
	tests := []struct {
		name    string
		spec    string
		want    ai.Model
		wantErr error
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
			name:    "unregistered ID on a known provider",
			spec:    "one/next",
			wantErr: catalog.ErrUnknownModel,
		},
		{
			name:    "bare ID with two providers",
			spec:    "shared",
			wantErr: catalog.ErrAmbiguousModel,
		},
		{
			name:    "unknown bare ID",
			spec:    "nope",
			wantErr: catalog.ErrUnknownModel,
		},
		{
			name:    "unknown provider",
			spec:    "nope/alpha",
			wantErr: catalog.ErrUnknownModel,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newCatalog()

			got, err := c.Model(tt.spec)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestCatalog_ModelIDWithASlash(t *testing.T) {
	c := newCatalog()
	next := ai.Model{ID: "vendor/next", Name: "Next"}
	c.RegisterModel("one", next)

	for _, spec := range []string{"one/vendor/next", "vendor/next"} {
		got, err := c.Model(spec)
		require.NoError(t, err, spec)
		assert.Equal(t, next, got, spec)
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

func TestCatalog_DecisionModel(t *testing.T) {
	c := newCatalog()

	dm, err := c.DecisionModel("jev")

	require.NoError(t, err)
	assert.Equal(t, jev, dm.Model)
	assert.Equal(t, decisionProvider{}, dm.Provider)
}

func TestCatalog_MissingCapability(t *testing.T) {
	c := newCatalog()

	_, textErr := c.LanguageModel("paint/pixel")
	_, imageErr := c.ImageModel("one/alpha")
	_, decisionErr := c.DecisionModel("one/alpha")

	assert.ErrorIs(t, textErr, errors.ErrUnsupported)
	assert.ErrorIs(t, imageErr, errors.ErrUnsupported)
	assert.ErrorIs(t, decisionErr, errors.ErrUnsupported)
}

func TestCatalog_ModelOutput(t *testing.T) {
	c := catalog.New()
	decides := ai.Model{ID: "decides", Output: []ai.Output{ai.OutputDecisions}}
	writes := ai.Model{ID: "writes", Output: []ai.Output{ai.OutputText}}
	unknown := ai.Model{ID: "unknown"}
	c.RegisterTextProvider("judge", decisionProvider{}, decides, writes, unknown)
	c.RegisterDecisionProvider("judge", decisionProvider{}, decides, writes, unknown)

	_, textErr := c.LanguageModel("judge/decides")
	_, decisionErr := c.DecisionModel("judge/writes")
	_, unknownTextErr := c.LanguageModel("judge/unknown")
	_, unknownDecisionErr := c.DecisionModel("judge/unknown")

	require.ErrorIs(t, textErr, errors.ErrUnsupported)
	assert.ErrorContains(t, textErr, `"judge/decides"`)
	require.ErrorIs(t, decisionErr, errors.ErrUnsupported)
	assert.ErrorContains(t, decisionErr, `"judge/writes"`)
	assert.NoError(t, unknownTextErr, "a model with no output list binds to each capability")
	assert.NoError(t, unknownDecisionErr)
}

func TestCatalog_ImageModelOutput(t *testing.T) {
	c := catalog.New()
	writes := ai.Model{ID: "writes", Output: []ai.Output{ai.OutputText}}
	unknown := ai.Model{ID: "unknown"}
	c.RegisterImageProvider("paint", imageProvider{}, writes, unknown)

	_, writesErr := c.ImageModel("paint/writes")
	_, unknownErr := c.ImageModel("paint/unknown")

	require.ErrorIs(t, writesErr, errors.ErrUnsupported)
	assert.ErrorContains(t, writesErr, `"paint/writes"`)
	assert.NoError(t, unknownErr, "a model with no output list binds to image generation")
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
		"judge/jev",
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
		Pricing:        ai.Pricing{Input: 1, Output: 2},
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

func TestCatalog_FetchModels_ListedPrices(t *testing.T) {
	listed := ai.Model{
		ID:             "next",
		ThinkingLevels: []ai.ThinkingLevel{ai.ThinkingLow},
		Pricing: ai.Pricing{
			Input:  1,
			Output: 2,
			Tiers:  []ai.PriceTier{{InputAbove: 100, Input: 3}},
		},
	}
	c := catalog.New()
	c.RegisterTextProvider("one", &lister{models: []ai.Model{listed}})

	c.FetchModels(t.Context())

	got, err := c.Model("one/next")
	require.NoError(t, err)
	assert.Equal(t, listed, got, "a model with no registered prices and levels keeps the listed ones")
}

func TestCatalog_FetchModels_OneCallPerProvider(t *testing.T) {
	c := catalog.New()
	l := &lister{models: []ai.Model{{ID: "alpha"}}}
	c.RegisterTextProvider("one", l)
	c.RegisterImageProvider("one", imageProvider{})

	c.FetchModels(t.Context())

	assert.Equal(t, 1, l.calls)
}

func TestCatalog_FetchModels_Some(t *testing.T) {
	one := &lister{models: []ai.Model{{ID: "alpha"}}}
	two := &lister{models: []ai.Model{{ID: "beta"}}}
	c := catalog.New()
	c.RegisterTextProvider("one", one)
	c.RegisterTextProvider("two", two)

	results := c.FetchModels(t.Context(), "two", "nope")

	require.Len(t, results, 2)
	assert.Equal(t, "nope", results[0].Provider)
	assert.ErrorIs(t, results[0].Err, catalog.ErrNoModelList, "an ID with no list is an error, so a typo does not pass")
	assert.EqualError(t, results[0].Err, `catalog: "nope" has no model list`)
	assert.Equal(t, catalog.FetchResult{Provider: "two", Models: 1}, results[1])
	assert.Equal(t, 0, one.calls, "a provider that ids does not name is not asked")
	assert.Equal(t, []string{"two/beta"}, c.Specs())
}

func TestCatalog_UnknownModelError(t *testing.T) {
	tests := []struct {
		name    string
		spec    string
		want    catalog.UnknownModelError
		wantMsg string
	}{
		{
			name:    "a known provider",
			spec:    "one/next",
			want:    catalog.UnknownModelError{Spec: "one/next", Provider: "one"},
			wantMsg: `catalog: unknown model "one/next": provider "one" has no model "next"`,
		},
		{
			name:    "a bare model ID",
			spec:    "next",
			want:    catalog.UnknownModelError{Spec: "next"},
			wantMsg: `catalog: unknown model "next"`,
		},
		{
			name: "an unknown provider",
			spec: "onee/alpha",
			want: catalog.UnknownModelError{
				Spec:      "onee/alpha",
				Providers: []string{"judge", "one", "paint", "two"},
			},
			wantMsg: `catalog: unknown model "onee/alpha": no provider "onee": use one of judge, one, paint, two`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := newCatalog().Model(tt.spec)

			require.EqualError(t, err, tt.wantMsg)
			require.ErrorIs(t, err, catalog.ErrUnknownModel)
			var got *catalog.UnknownModelError
			require.ErrorAs(t, err, &got)
			assert.Equal(t, tt.want, *got)
		})
	}
}

func TestCatalog_AmbiguousModelError(t *testing.T) {
	_, err := newCatalog().Model("shared")

	require.ErrorIs(t, err, catalog.ErrAmbiguousModel)
	var got *catalog.AmbiguousModelError
	require.ErrorAs(t, err, &got)
	assert.Equal(t, catalog.AmbiguousModelError{
		ID:    "shared",
		Specs: []string{"one/shared", "two/shared"},
	}, *got)
}

func TestMergeDriverModel(t *testing.T) {
	registered := ai.Model{
		ID:             "opus",
		Name:           "Opus",
		ThinkingLevels: []ai.ThinkingLevel{ai.ThinkingHigh},
	}
	listed := ai.Model{
		ID:             "opus",
		ThinkingLevels: []ai.ThinkingLevel{ai.ThinkingLow, ai.ThinkingHigh, ai.ThinkingMax},
	}

	got := catalog.MergeDriverModel(registered, listed)

	assert.Equal(t, ai.Model{
		ID:             "opus",
		Name:           "Opus",
		ThinkingLevels: []ai.ThinkingLevel{ai.ThinkingLow, ai.ThinkingHigh, ai.ThinkingMax},
	}, got, "the CLI knows the levels that it takes")
}

func TestMergeModel(t *testing.T) {
	registered := ai.Model{
		ID:             "m",
		Name:           "Old",
		MaxTokens:      100,
		ThinkingLevels: []ai.ThinkingLevel{ai.ThinkingOff},
		Pricing:        ai.Pricing{Input: 5},
	}
	listed := ai.Model{
		ID:             "m",
		ContextWindow:  1000,
		Input:          []ai.Modality{ai.ModalityText},
		ThinkingLevels: []ai.ThinkingLevel{ai.ThinkingHigh},
		Pricing:        ai.Pricing{Input: 1},
	}

	got := catalog.MergeModel(registered, listed)

	assert.Equal(t, ai.Model{
		ID:             "m",
		Name:           "Old",
		MaxTokens:      100,
		ContextWindow:  1000,
		Input:          []ai.Modality{ai.ModalityText},
		ThinkingLevels: []ai.ThinkingLevel{ai.ThinkingOff},
		Pricing:        ai.Pricing{Input: 5},
	}, got)
}

// fullModel returns a model with each field set.
func fullModel() ai.Model {
	return ai.Model{
		ID:             "m",
		Name:           "M",
		MaxTokens:      100,
		ContextWindow:  1000,
		Input:          []ai.Modality{ai.ModalityText},
		Output:         []ai.Output{ai.OutputText},
		Reasoning:      true,
		ThinkingLevels: []ai.ThinkingLevel{ai.ThinkingHigh},
		Pricing: ai.Pricing{
			Input: 1,
			Tiers: []ai.PriceTier{{InputAbove: 10, Input: 2}},
		},
	}
}

// TestFillModel_EachField checks that FillModel fills each field of
// [ai.Model]. A new field fails the test until fullModel sets it.
func TestFillModel_EachField(t *testing.T) {
	full := fullModel()
	v := reflect.ValueOf(full)
	for i := range v.NumField() {
		require.False(t, v.Field(i).IsZero(), "fullModel must set %s", v.Type().Field(i).Name)
	}

	assert.Equal(t, full, catalog.FillModel(ai.Model{}, full), "each zero field takes the value of d")
	assert.Equal(t, full, catalog.FillModel(full, ai.Model{ID: "d", Name: "D", MaxTokens: 5}), "each set field stays")
}

func TestFillModel_Copies(t *testing.T) {
	full := fullModel()

	got := catalog.FillModel(full, ai.Model{})
	got.Input[0] = ai.ModalityPDF
	got.Output[0] = ai.OutputImage
	got.ThinkingLevels[0] = ai.ThinkingLow
	got.Pricing.Tiers[0].Input = 9

	assert.Equal(t, fullModel(), full, "the result has its own copy of each slice")
}

func TestFillModel_Prices(t *testing.T) {
	m := ai.Model{ID: "m", Pricing: ai.Pricing{Output: 2}}
	d := ai.Model{Pricing: ai.Pricing{Input: 1, Output: 5, CacheRead: 0.1}}

	got := catalog.FillModel(m, d)

	assert.Equal(t, ai.Pricing{Output: 2}, got.Pricing, "a model with a price keeps all of its prices")
}
