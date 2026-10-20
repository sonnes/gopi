package catalog_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/catalog"
)

// fakeDriver is an [agent.Driver] with a name, so a test can tell two
// drivers apart.
type fakeDriver struct{ name string }

func (fakeDriver) Start(context.Context, agent.Config) (agent.Runner, error) {
	return nil, nil
}

var (
	cliDriver     = fakeDriver{name: "cli"}
	harnessDriver = fakeDriver{name: "harness"}
)

// driverCatalog returns a catalog with a text provider "anthropic", a
// driver "claude-code", and the default driver. Both serve the model
// "opus". Only the driver serves "codex-mini".
func driverCatalog() *catalog.Catalog {
	cat := catalog.New()
	cat.RegisterTextProvider("anthropic", textProvider{}, ai.Model{ID: "opus", MaxTokens: 100})
	cat.RegisterDriver(
		"claude-code",
		cliDriver,
		ai.Model{ID: "opus", MaxTokens: 200},
		ai.Model{ID: "codex-mini"},
	)
	cat.RegisterSpeechProvider("voice", speechProvider{}, ai.Model{ID: "tts"})
	cat.DefaultDriver(harnessDriver)
	return cat
}

func TestCatalog_Driver(t *testing.T) {
	tests := []struct {
		name       string
		spec       string
		wantDriver agent.Driver
		wantModel  ai.Model
		errText    string
	}{
		{
			name:       "a provider with a driver",
			spec:       "claude-code/opus",
			wantDriver: cliDriver,
			wantModel:  ai.Model{ID: "opus", MaxTokens: 200},
		},
		{
			name:       "an unregistered model of a driver passes through",
			spec:       "claude-code/haiku",
			wantDriver: cliDriver,
			wantModel:  ai.Model{ID: "haiku", Name: "haiku"},
		},
		{
			name:       "a text provider runs on the default driver",
			spec:       "anthropic/opus",
			wantDriver: harnessDriver,
			wantModel:  ai.Model{ID: "opus", MaxTokens: 100},
		},
		{
			name:       "a bare ID that a driver and a text provider serve",
			spec:       "opus",
			wantDriver: harnessDriver,
			wantModel:  ai.Model{ID: "opus", MaxTokens: 100},
		},
		{
			name:       "a bare ID that only a driver serves",
			spec:       "codex-mini",
			wantDriver: cliDriver,
			wantModel:  ai.Model{ID: "codex-mini"},
		},
		{
			name:    "a provider with no text and no driver",
			spec:    "voice/tts",
			errText: `catalog: provider "voice" has no driver and no text generation`,
		},
		{
			name:    "an unknown provider",
			spec:    "nope/opus",
			errText: `catalog: unknown model "nope/opus"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, m, err := driverCatalog().Driver(tt.spec)
			if tt.errText != "" {
				require.EqualError(t, err, tt.errText)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantDriver, d)
			assert.Equal(t, tt.wantModel, m)
		})
	}
}

func TestCatalog_DriverWithoutDefault(t *testing.T) {
	cat := catalog.New()
	cat.RegisterTextProvider("anthropic", textProvider{}, ai.Model{ID: "opus"})

	_, _, err := cat.Driver("anthropic/opus")
	require.EqualError(t, err, `catalog: provider "anthropic" needs the default driver: set one with DefaultDriver`)
}

func TestCatalog_RegisterDriverAddsSpecs(t *testing.T) {
	assert.Equal(t,
		[]string{"anthropic/opus", "claude-code/codex-mini", "claude-code/opus", "voice/tts"},
		driverCatalog().Specs(),
	)
}

// TestCatalog_BareIDSkipsDrivers checks that a bare ID of a text model
// does not count a provider that has only a driver, because a driver
// cannot serve a call to a model.
func TestCatalog_BareIDSkipsDrivers(t *testing.T) {
	cat := driverCatalog()

	m, err := cat.Model("opus")
	require.NoError(t, err)
	assert.Equal(t, ai.Model{ID: "opus", MaxTokens: 100}, m)

	lm, err := cat.LanguageModel("opus")
	require.NoError(t, err)
	assert.Equal(t, ai.Model{ID: "opus", MaxTokens: 100}, lm.Model)

	_, err = cat.LanguageModel("codex-mini")
	require.EqualError(t, err, `catalog: unknown model "codex-mini"`)

	_, err = cat.Model("codex-mini")
	require.EqualError(t, err, `catalog: unknown model "codex-mini"`)
}
