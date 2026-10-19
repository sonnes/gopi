package catalog_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/catalog"
)

// found returns a detector that finds its credential and builds p.
func found(provider, source string, p ai.TextProvider, models ...ai.Model) catalog.Detector {
	return catalog.Detector{
		Provider: provider,
		Source:   source,
		Models:   models,
		Detect: func() (ai.TextProvider, bool) {
			return p, true
		},
	}
}

// missing returns a detector that finds no credential.
func missing(provider, source string) catalog.Detector {
	return catalog.Detector{
		Provider: provider,
		Source:   source,
		Detect: func() (ai.TextProvider, bool) {
			return nil, false
		},
	}
}

func TestCatalog_Detect(t *testing.T) {
	c := catalog.New()

	got := c.Detect(
		missing("one", "ONE_KEY"),
		found("two", "TWO_KEY", textProvider{}, beta),
		found("paint", "PAINT_KEY", imageProvider{}, pixel),
	)

	assert.Equal(t, []catalog.Detection{
		{Provider: "two", Source: "TWO_KEY"},
		{Provider: "paint", Source: "PAINT_KEY"},
	}, got)
	assert.Equal(t, []string{"paint/pixel", "two/beta"}, c.Specs())
}

func TestCatalog_Detect_FirstSourceWins(t *testing.T) {
	c := catalog.New()

	got := c.Detect(
		found("one", "FIRST_KEY", textProvider{}, alpha),
		found("one", "SECOND_KEY", imageProvider{}, alpha),
	)

	assert.Equal(t, []catalog.Detection{{Provider: "one", Source: "FIRST_KEY"}}, got)
	_, err := c.ImageModel("one/alpha")
	assert.Error(t, err, "the second detector must not register its provider")
}

func TestCatalog_Detect_RegistersCapabilities(t *testing.T) {
	c := catalog.New()

	c.Detect(
		found("paint", "PAINT_KEY", imageProvider{}, pixel),
		found("voice", "VOICE_KEY", speechProvider{}, alpha),
	)

	_, textErr := c.LanguageModel("paint/pixel")
	_, imageErr := c.ImageModel("paint/pixel")
	_, speechErr := c.SpeechModel("voice/alpha")
	require.NoError(t, textErr)
	require.NoError(t, imageErr)
	require.NoError(t, speechErr)
}
