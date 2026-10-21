package catalog_test

import (
	"context"
	"errors"
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
			name:    "an unregistered model of a driver",
			spec:    "claude-code/haiku",
			errText: `catalog: unknown model "claude-code/haiku": driver "claude-code" has no model "haiku"`,
		},
		{
			name:    "an unregistered model of a text provider",
			spec:    "anthropic/haiku",
			errText: `catalog: unknown model "anthropic/haiku": provider "anthropic" has no model "haiku"`,
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
			errText: `catalog: unknown model "nope/opus": no provider "nope": use one of anthropic, claude-code, voice`,
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

func TestCatalog_DriverSpecs(t *testing.T) {
	cat := driverCatalog()

	assert.Equal(t,
		[]string{"anthropic/opus", "voice/tts"},
		cat.Specs(),
		"Specs lists the models of the providers only",
	)
	assert.Equal(t,
		[]string{"claude-code/codex-mini", "claude-code/opus"},
		cat.DriverSpecs(),
	)
}

// TestCatalog_ProvidersSkipDrivers checks that the methods of a model
// do not read the models of a driver, because a driver cannot serve a
// call to a model.
func TestCatalog_ProvidersSkipDrivers(t *testing.T) {
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

	_, err = cat.Model("claude-code/opus")
	require.EqualError(t, err, `catalog: provider "claude-code" has only a driver: start its agent with Driver`)

	_, err = cat.LanguageModel("claude-code/codex-mini")
	require.EqualError(t, err, `catalog: provider "claude-code" has only a driver: start its agent with Driver`)
}

// TestCatalog_DriverOfATextProvider checks that a provider with a text
// provider and a driver starts its driver with the model of the driver,
// and serves calls with the model of the provider.
func TestCatalog_DriverOfATextProvider(t *testing.T) {
	cat := catalog.New()
	cat.RegisterTextProvider("both", textProvider{}, ai.Model{ID: "m", MaxTokens: 100})
	cat.RegisterDriver("both", cliDriver, ai.Model{ID: "m", MaxTokens: 200})
	cat.DefaultDriver(harnessDriver)

	for _, spec := range []string{"both/m", "m"} {
		d, m, err := cat.Driver(spec)
		require.NoError(t, err, spec)
		assert.Equal(t, cliDriver, d, spec)
		assert.Equal(t, ai.Model{ID: "m", MaxTokens: 200}, m, spec)

		lm, err := cat.LanguageModel(spec)
		require.NoError(t, err, spec)
		assert.Equal(t, ai.Model{ID: "m", MaxTokens: 100}, lm.Model, spec)
	}
}

// listingDriver is a driver that lists its models, or fails.
type listingDriver struct {
	fakeDriver
	models []ai.Model
	err    error
	calls  *int
}

func (d listingDriver) ListModels(context.Context) ([]ai.Model, error) {
	*d.calls++
	return d.models, d.err
}

func TestCatalog_FetchDriverModels(t *testing.T) {
	var cliCalls, brokenCalls int
	boom := errors.New("boom")
	cli := listingDriver{
		fakeDriver: fakeDriver{name: "cli"},
		models:     []ai.Model{{ID: "opus", Name: "Opus"}, {ID: "next"}},
		calls:      &cliCalls,
	}
	broken := listingDriver{
		fakeDriver: fakeDriver{name: "broken"},
		err:        boom,
		calls:      &brokenCalls,
	}
	provider := &lister{models: []ai.Model{{ID: "alpha"}}}
	cat := catalog.New()
	cat.RegisterDriver("cli", cli, ai.Model{ID: "opus", MaxTokens: 200})
	cat.RegisterDriver("broken", broken)
	cat.RegisterDriver("static", cliDriver)
	cat.RegisterTextProvider("one", provider)

	results := cat.FetchDriverModels(t.Context())

	assert.Equal(t, []catalog.FetchResult{
		{Provider: "broken", Err: boom},
		{Provider: "cli", Models: 2},
	}, results)
	assert.Equal(t, []string{"cli/next", "cli/opus"}, cat.DriverSpecs())
	assert.Empty(t, cat.Specs(), "the models of a driver stay out of the models of the providers")
	assert.Equal(t, 0, provider.calls, "FetchDriverModels does not ask the providers")

	_, m, err := cat.Driver("cli/opus")
	require.NoError(t, err)
	assert.Equal(t, ai.Model{ID: "opus", Name: "Opus", MaxTokens: 200}, m)
}

func TestCatalog_FetchDriverModels_Levels(t *testing.T) {
	var calls int
	cli := listingDriver{
		models: []ai.Model{{ID: "opus", ThinkingLevels: []ai.ThinkingLevel{ai.ThinkingLow, ai.ThinkingMax}}},
		calls:  &calls,
	}
	cat := catalog.New()
	cat.RegisterDriver("cli", cli, ai.Model{ID: "opus", ThinkingLevels: []ai.ThinkingLevel{ai.ThinkingHigh}})

	cat.FetchDriverModels(t.Context())

	_, m, err := cat.Driver("cli/opus")
	require.NoError(t, err)
	assert.Equal(t, []ai.ThinkingLevel{ai.ThinkingLow, ai.ThinkingMax}, m.ThinkingLevels, "the levels of the CLI replace the registered ones")
}

func TestCatalog_FetchModelsSkipsDrivers(t *testing.T) {
	var calls int
	cat := catalog.New()
	cat.RegisterDriver("cli", listingDriver{calls: &calls})

	results := cat.FetchModels(t.Context())

	assert.Empty(t, results)
	assert.Equal(t, 0, calls)
}

func TestCatalog_FetchDriverModels_Some(t *testing.T) {
	var oneCalls, twoCalls int
	cat := catalog.New()
	cat.RegisterDriver("one", listingDriver{models: []ai.Model{{ID: "a"}}, calls: &oneCalls})
	cat.RegisterDriver("two", listingDriver{models: []ai.Model{{ID: "b"}}, calls: &twoCalls})

	results := cat.FetchDriverModels(t.Context(), "two", "nope")

	require.Len(t, results, 2)
	assert.Equal(t, "nope", results[0].Provider)
	assert.ErrorIs(t, results[0].Err, catalog.ErrNoModelList)
	assert.Equal(t, catalog.FetchResult{Provider: "two", Models: 1}, results[1])
	assert.Equal(t, 0, oneCalls)
	assert.Equal(t, []string{"two/b"}, cat.DriverSpecs())
}

func TestCatalog_FetchNothing(t *testing.T) {
	cat := catalog.New()
	cat.RegisterDriver("static", cliDriver)

	assert.Nil(t, cat.FetchModels(t.Context()))
	assert.Nil(t, cat.FetchDriverModels(t.Context()))
}
