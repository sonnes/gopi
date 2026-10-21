package main

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/internal/modelsfile"
	"github.com/sonnes/gopi/pkg/ai"
)

// lists returns a list function that returns models, or err.
func lists(err error, models ...ai.Model) func(context.Context) ([]ai.Model, error) {
	return func(context.Context) ([]ai.Model, error) {
		return models, err
	}
}

// entry returns a provider entry with the default def and models.
func entry(def string, models ...modelsfile.Model) modelsfile.Entry {
	return modelsfile.Entry{
		Kind:    modelsfile.KindProvider,
		Default: def,
		Models:  models,
	}
}

func TestSync_MergesAList(t *testing.T) {
	f := modelsfile.File{"p": entry("a",
		modelsfile.Model{
			Model: ai.Model{ID: "a", Name: "A", Pricing: ai.Pricing{Input: 5}},
			Note:  "Keep me.",
		},
		modelsfile.Model{Model: ai.Model{ID: "old", Name: "Old"}},
	)}
	src := source{
		ID:   "p",
		Kind: modelsfile.KindProvider,
		List: lists(nil,
			ai.Model{ID: "a", ContextWindow: 1000, Pricing: ai.Pricing{Input: 1}},
			ai.Model{ID: "b", Name: "B"},
		),
	}

	got, report := syncFile(context.Background(), f, []source{src}, nil, false)

	assert.Equal(t, entry("a",
		modelsfile.Model{
			Model: ai.Model{ID: "a", Name: "A", ContextWindow: 1000, Pricing: ai.Pricing{Input: 5}},
			Note:  "Keep me.",
		},
		modelsfile.Model{Model: ai.Model{ID: "b", Name: "B"}},
		modelsfile.Model{Model: ai.Model{ID: "old", Name: "Old"}},
	), got["p"], "a registered price and a note stay, and a model that the list leaves out stays")
	assert.Equal(t, []string{
		"p: listed 2: 1 added, 1 changed, 0 removed",
		"  + b",
		"  ~ a",
	}, report)
}

func TestSync_Prune(t *testing.T) {
	f := modelsfile.File{"p": entry("a",
		modelsfile.Model{Model: ai.Model{ID: "a", Name: "A"}},
		modelsfile.Model{Model: ai.Model{ID: "old", Name: "Old"}},
	)}
	src := source{ID: "p", Kind: modelsfile.KindProvider, List: lists(nil, ai.Model{ID: "a", Name: "A"})}

	got, report := syncFile(context.Background(), f, []source{src}, nil, true)

	assert.Equal(t, entry("a", modelsfile.Model{Model: ai.Model{ID: "a", Name: "A"}}), got["p"])
	assert.Equal(t, []string{
		"p: listed 1: 0 added, 0 changed, 1 removed",
		"  - old",
	}, report)
}

func TestSync_FailedOrMissingList(t *testing.T) {
	old := entry("a", modelsfile.Model{Model: ai.Model{ID: "a", Name: "A"}})
	f := modelsfile.File{"p": old, "q": old}
	sources := []source{
		{ID: "p", Kind: modelsfile.KindProvider, List: lists(errors.New("401 Unauthorized"))},
		{ID: "q", Kind: modelsfile.KindProvider, Skip: "set Q_KEY"},
	}

	got, report := syncFile(context.Background(), f, sources, nil, true)

	assert.Equal(t, old, got["p"], "a failed list changes nothing, even with prune")
	assert.Equal(t, old, got["q"])
	assert.Equal(t, []string{
		"p: the list failed: 401 Unauthorized",
		"q: no list: set Q_KEY",
	}, report)
}

func TestSync_NewEntryDefault(t *testing.T) {
	src := source{
		ID:   "new",
		Kind: modelsfile.KindProvider,
		List: lists(nil, ai.Model{ID: "z", Name: "Z"}, ai.Model{ID: "m", Name: "M"}),
	}

	got, report := syncFile(context.Background(), modelsfile.File{}, []source{src}, nil, false)

	assert.Equal(t, modelsfile.KindProvider, got["new"].Kind)
	assert.Equal(t, "m", got["new"].Default, "a new provider entry takes its first model by ID")
	assert.Contains(t, report, "new: the default is now m. Check it.")
}

func readDev(t *testing.T) devCatalog {
	t.Helper()
	data, err := os.ReadFile("testdata/modelsdev.json")
	require.NoError(t, err)
	dev, err := parseDev(data)
	require.NoError(t, err)
	return dev
}

func TestSync_FillsFromModelsDev(t *testing.T) {
	f := modelsfile.File{"anthropic": entry("claude-sonnet-5-5",
		modelsfile.Model{Model: ai.Model{
			ID:      "claude-sonnet-5-5",
			Name:    "Claude Sonnet 5.5",
			Pricing: ai.Pricing{Input: 2, Output: 10, CacheRead: 0.2, CacheWrite: 2.5, CacheWriteLong: 4},
		}},
		modelsfile.Model{Model: ai.Model{ID: "claude-haiku-5-5", Name: "Claude Haiku 5.5"}},
	)}
	src := source{ID: "anthropic", Kind: modelsfile.KindProvider, Skip: "set ANTHROPIC_API_KEY", Dev: "anthropic"}

	got, report := syncFile(context.Background(), f, []source{src}, readDev(t), false)

	levels := []ai.ThinkingLevel{ai.ThinkingLow, ai.ThinkingMedium, ai.ThinkingHigh, ai.ThinkingXHigh, ai.ThinkingMax}
	documents := []ai.Modality{ai.ModalityText, ai.ModalityImage, ai.ModalityPDF}
	assert.Equal(t, entry("claude-sonnet-5-5",
		modelsfile.Model{Model: ai.Model{
			ID:             "claude-haiku-5-5",
			Name:           "Claude Haiku 5.5",
			MaxTokens:      128_000,
			ContextWindow:  1_000_000,
			Input:          documents,
			Reasoning:      true,
			ThinkingLevels: append([]ai.ThinkingLevel{ai.ThinkingOff}, levels...),
			Pricing: ai.Pricing{
				Input:      0.1,
				Output:     0.5,
				CacheRead:  0.01,
				CacheWrite: 0.125,
				Tiers: []ai.PriceTier{{
					InputAbove: 100_000,
					Input:      0.5,
					Output:     2.5,
					CacheRead:  0.05,
					CacheWrite: 0.625,
				}},
			},
		}},
		modelsfile.Model{Model: ai.Model{
			ID:             "claude-sonnet-5-5",
			Name:           "Claude Sonnet 5.5",
			MaxTokens:      128_000,
			ContextWindow:  1_000_000,
			Input:          documents,
			Reasoning:      true,
			ThinkingLevels: levels,
			Pricing:        ai.Pricing{Input: 2, Output: 10, CacheRead: 0.2, CacheWrite: 2.5, CacheWriteLong: 4},
		}},
	), got["anthropic"], "models.dev fills empty values, and a price in the file stays")
	assert.Equal(t, []string{
		"anthropic: no list: set ANTHROPIC_API_KEY",
		"  filled from models.dev: claude-haiku-5-5, claude-sonnet-5-5",
		"prices that differ from models.dev:",
		"  anthropic/claude-sonnet-5-5: cache_read 0.2 in the file, 0.1 in models.dev",
	}, report)
}

func TestSync_ModelsDevAdds(t *testing.T) {
	src := source{ID: "fireworks", Kind: modelsfile.KindProvider, Skip: "models.dev is the source", Dev: "fireworks-ai", DevAdds: true}

	got, report := syncFile(context.Background(), modelsfile.File{}, []source{src}, readDev(t), false)

	ids := []string{}
	for _, m := range got["fireworks"].Models {
		ids = append(ids, m.ID)
	}
	assert.Equal(t, []string{
		"accounts/fireworks/routers/glm-5p3-fast",
		"accounts/fireworks/routers/qwen-max-latest",
	}, ids, "models.dev adds each model with tools, sorted by ID")
	glm := got["fireworks"].Models[0]
	assert.Equal(t, ai.Pricing{Input: 2.1, Output: 6.6, CacheRead: 0.39}, glm.Pricing)
	assert.Equal(t, []ai.ThinkingLevel{ai.ThinkingLow, ai.ThinkingHigh, ai.ThinkingMax}, glm.ThinkingLevels)
	assert.Equal(t, 262_144, glm.MaxTokens)
	assert.Contains(t, report, "  + accounts/fireworks/routers/glm-5p3-fast (models.dev)")
}

func TestSync_DevAdmits(t *testing.T) {
	src := source{
		ID:        "openai",
		Kind:      modelsfile.KindProvider,
		List:      lists(nil, ai.Model{ID: "gpt-6.1-sol"}, ai.Model{ID: "text-embedding-3-small"}, ai.Model{ID: "dall-e-9"}),
		Dev:       "openai",
		DevAdmits: true,
	}

	got, report := syncFile(context.Background(), modelsfile.File{}, []source{src}, readDev(t), false)

	require.Len(t, got["openai"].Models, 1)
	m := got["openai"].Models[0]
	assert.Equal(t, "gpt-6.1-sol", m.ID)
	assert.Equal(t, "GPT-6.1 Sol", m.Name, "models.dev fills the name")
	assert.Contains(t, report, "openai: ignored 2 listed models that models.dev does not have with tools")
}

func TestSelect(t *testing.T) {
	all := []source{{ID: "a"}, {ID: "b"}, {ID: "c"}}

	got, err := selectSources(all, "")
	require.NoError(t, err)
	assert.Equal(t, all, got)

	got, err = selectSources(all, "c, a")
	require.NoError(t, err)
	assert.Equal(t, []source{{ID: "a"}, {ID: "c"}}, got)

	_, err = selectSources(all, "a,typo")
	assert.EqualError(t, err, `no source "typo": use one of a, b, c`, "a typo does not rewrite the file in silence")
}

func TestSync_Driver(t *testing.T) {
	high := []ai.ThinkingLevel{ai.ThinkingHigh}
	more := []ai.ThinkingLevel{ai.ThinkingLow, ai.ThinkingHigh}
	f := modelsfile.File{"cli": {
		Kind:    modelsfile.KindDriver,
		Default: "a",
		Models: []modelsfile.Model{
			{Model: ai.Model{ID: "a", ThinkingLevels: high}},
			{Model: ai.Model{ID: "b", ThinkingLevels: high}},
		},
	}}
	src := source{
		ID:   "cli",
		Kind: modelsfile.KindDriver,
		List: lists(nil,
			ai.Model{ID: "b", ThinkingLevels: more},
			ai.Model{ID: "a", ThinkingLevels: high},
		),
	}

	got, report := syncFile(context.Background(), f, []source{src}, nil, false)

	assert.Equal(t, "b", got["cli"].Default, "the first model of the list of a CLI is its default")
	assert.Equal(t, more, got["cli"].Models[1].ThinkingLevels, "the levels of the CLI replace the levels of the file")
	assert.Contains(t, report, "cli: the default is now b, the default of its list")
}

func TestSync_PruneEmptyList(t *testing.T) {
	f := modelsfile.File{"p": entry("a", modelsfile.Model{Model: ai.Model{ID: "a"}})}
	src := source{ID: "p", Kind: modelsfile.KindProvider, List: lists(nil)}

	got, report := syncFile(context.Background(), f, []source{src}, nil, true)

	assert.Equal(t, f["p"], got["p"], "an empty list removes nothing")
	assert.Equal(t, []string{
		"p: listed 0: 0 added, 0 changed, 0 removed",
		"p: the list is empty, so the prune removes nothing",
	}, report)
}

// TestSync_KeepsThinking checks that models.dev does not change the
// thinking of a model that has thinking levels. A false Reasoning in the
// file is a choice, and JSON cannot tell it from a missing value.
func TestSync_KeepsThinking(t *testing.T) {
	haiku := ai.Model{
		ID:             "claude-haiku-5-5",
		Name:           "Claude Haiku 5.5",
		ThinkingLevels: []ai.ThinkingLevel{ai.ThinkingOff},
		Pricing:        ai.Pricing{Input: 0.1},
	}
	f := modelsfile.File{"anthropic": entry(haiku.ID, modelsfile.Model{Model: haiku})}
	src := source{ID: "anthropic", Kind: modelsfile.KindProvider, Skip: "no key", Dev: "anthropic"}

	got, _ := syncFile(context.Background(), f, []source{src}, readDev(t), false)

	m := got["anthropic"].Models[0]
	assert.False(t, m.Reasoning)
	assert.Equal(t, []ai.ThinkingLevel{ai.ThinkingOff}, m.ThinkingLevels)
}
