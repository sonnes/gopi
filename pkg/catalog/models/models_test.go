package models

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/internal/modelsfile"
	"github.com/sonnes/gopi/pkg/ai"
)

// knownLevels and knownInput are the values of the ai package that the
// file can hold.
var (
	knownLevels = []ai.ThinkingLevel{
		ai.ThinkingOff,
		ai.ThinkingLow,
		ai.ThinkingMedium,
		ai.ThinkingHigh,
		ai.ThinkingXHigh,
		ai.ThinkingMax,
	}
	knownInput = []ai.Modality{ai.ModalityText, ai.ModalityImage, ai.ModalityPDF}
)

// TestFile checks the rules of models.json that the generator and a
// person who edits the file must keep.
func TestFile(t *testing.T) {
	f, err := modelsfile.Parse(data)
	require.NoError(t, err)
	require.NotEmpty(t, f)

	for id, e := range f {
		t.Run(id, func(t *testing.T) {
			assert.Contains(t, []string{modelsfile.KindProvider, modelsfile.KindDriver}, e.Kind)
			require.NotEmpty(t, e.Models)

			ids := make([]string, len(e.Models))
			for i, m := range e.Models {
				ids[i] = m.ID
			}
			assert.True(t, slices.IsSorted(ids), "the models are sorted by ID")
			assert.Len(t, slices.Compact(slices.Clone(ids)), len(ids), "each ID is unique")
			assert.Contains(t, ids, e.Default, "the default is a model of the list")

			for _, m := range e.Models {
				assert.NotEmpty(t, m.Name, m.ID)
				for _, level := range m.ThinkingLevels {
					assert.Contains(t, knownLevels, level, m.ID)
				}
				for _, mod := range m.Input {
					assert.Contains(t, knownInput, mod, m.ID)
				}
				checkPricing(t, m.ID, m.Pricing)
				checkLimits(t, m.Model)
			}
		})
	}
}

// checkLimits checks that the output cap and each price tier of m fit
// in its context window. A call with no MaxTokens option sends the
// output cap, and an API rejects a cap that is larger than the window.
func checkLimits(t *testing.T, m ai.Model) {
	t.Helper()
	if m.ContextWindow == 0 {
		return
	}
	assert.LessOrEqual(t, m.MaxTokens, m.ContextWindow, "%s: the output cap fits in the context window", m.ID)
	for _, tier := range m.Pricing.Tiers {
		assert.Less(t, tier.InputAbove, m.ContextWindow, "%s: a request can reach the tier", m.ID)
	}
}

// checkPricing checks that no price of p is negative, and that its
// tiers are in order of InputAbove.
func checkPricing(t *testing.T, id string, p ai.Pricing) {
	t.Helper()
	prices := []float64{p.Input, p.Output, p.CacheRead, p.CacheWrite, p.CacheWriteLong}
	for _, tier := range p.Tiers {
		prices = append(prices, tier.Input, tier.Output, tier.CacheRead, tier.CacheWrite, tier.CacheWriteLong)
	}
	for _, price := range prices {
		assert.GreaterOrEqual(t, price, 0.0, id)
	}
	sorted := slices.IsSortedFunc(p.Tiers, func(a, b ai.PriceTier) int {
		return a.InputAbove - b.InputAbove
	})
	assert.True(t, sorted, "the tiers of %s are in order of InputAbove", id)
}

func TestOf(t *testing.T) {
	for _, id := range IDs() {
		t.Run(id, func(t *testing.T) {
			got := Of(id)

			require.NotEmpty(t, got)
			assert.Equal(t, defaultOf(t, id), got[0].ID, "the default model comes first")
		})
	}
	assert.Nil(t, Of("nope"))
}

// defaultOf returns the default model ID of id in the embedded file.
func defaultOf(t *testing.T, id string) string {
	t.Helper()
	f, err := modelsfile.Parse(data)
	require.NoError(t, err)
	return f[id].Default
}

func TestOf_ReturnsACopy(t *testing.T) {
	id := IDs()[0]
	got := Of(id)
	got[0].ID = "changed"
	got[0].Input = append(got[0].Input[:0], "changed")

	again := Of(id)
	assert.NotEqual(t, "changed", again[0].ID)
	assert.NotContains(t, again[0].Input, ai.Modality("changed"))
}

func TestLookup(t *testing.T) {
	id := IDs()[0]
	first := Of(id)[0]

	got, ok := Lookup(id, first.ID)
	assert.True(t, ok)
	assert.Equal(t, first, got)

	_, ok = Lookup(id, "nope")
	assert.False(t, ok)
	_, ok = Lookup("nope", first.ID)
	assert.False(t, ok)
}

func TestMust(t *testing.T) {
	id := IDs()[0]
	first := Of(id)[0]

	assert.Equal(t, first, Must(id, first.ID))
	assert.PanicsWithValue(t, `models: no model "nope" of "`+id+`" in models.json`, func() {
		Must(id, "nope")
	})
}

func TestIDs(t *testing.T) {
	ids := IDs()

	assert.True(t, slices.IsSorted(ids))
	assert.Subset(t, ids, []string{"anthropic", "openai", "claude-code", "codex"})
}
