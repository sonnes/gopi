package ai_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
)

func TestUsage_Add(t *testing.T) {
	a := ai.Usage{
		Input:          1,
		Output:         2,
		CacheRead:      3,
		CacheWrite:     4,
		CacheWriteLong: 1,
		Cost:           ai.Cost{Input: 0.5, Output: 1},
	}
	b := ai.Usage{
		Input:          10,
		Output:         20,
		CacheRead:      30,
		CacheWrite:     40,
		CacheWriteLong: 10,
		Cost:           ai.Cost{Input: 0.25, CacheRead: 2},
	}
	assert.Equal(t, ai.Usage{
		Input:          11,
		Output:         22,
		CacheRead:      33,
		CacheWrite:     44,
		CacheWriteLong: 11,
		Cost:           ai.Cost{Input: 0.75, Output: 1, CacheRead: 2},
	}, a.Add(b))
}

func TestCost_Total(t *testing.T) {
	c := ai.Cost{Input: 1, Output: 2, CacheRead: 0.5, CacheWrite: 0.25}
	assert.InDelta(t, 3.75, c.Total(), 1e-9)
}

func TestPricing_Cost(t *testing.T) {
	pricing := ai.Pricing{
		Input:      2,
		Output:     10,
		CacheRead:  0.2,
		CacheWrite: 2.5,
		Tiers: []ai.PriceTier{
			{InputAbove: 272_000, Input: 4, Output: 15, CacheRead: 0.4, CacheWrite: 5},
			{InputAbove: 500_000, Input: 8, Output: 30, CacheRead: 0.8, CacheWrite: 10},
		},
	}

	tests := []struct {
		name  string
		usage ai.Usage
		want  ai.Cost
	}{
		{
			name:  "base prices",
			usage: ai.Usage{Input: 100_000, Output: 100_000, CacheRead: 100_000},
			want:  ai.Cost{Input: 2 * 0.1, Output: 10 * 0.1, CacheRead: 0.2 * 0.1},
		},
		{
			name:  "at the threshold, the base prices",
			usage: ai.Usage{Input: 272_000},
			want:  ai.Cost{Input: 2 * 0.272},
		},
		{
			name:  "above a threshold, the tier prices for the whole request",
			usage: ai.Usage{Input: 100_000, CacheRead: 200_000, Output: 1000},
			want:  ai.Cost{Input: 4 * 0.1, CacheRead: 0.4 * 0.2, Output: 15 * 0.001},
		},
		{
			name:  "the highest tier that applies",
			usage: ai.Usage{Input: 600_000},
			want:  ai.Cost{Input: 8 * 0.6},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pricing.Cost(tt.usage)
			assert.InDelta(t, tt.want.Input, got.Input, 1e-9)
			assert.InDelta(t, tt.want.Output, got.Output, 1e-9)
			assert.InDelta(t, tt.want.CacheRead, got.CacheRead, 1e-9)
			assert.InDelta(t, tt.want.CacheWrite, got.CacheWrite, 1e-9)
		})
	}
}

func TestPricing_CostLongCacheWrite(t *testing.T) {
	pricing := ai.Pricing{
		CacheWrite:     2.5,
		CacheWriteLong: 4,
		Tiers: []ai.PriceTier{
			{InputAbove: 500_000, CacheWrite: 5, CacheWriteLong: 8},
		},
	}

	// CacheWriteLong is a part of CacheWrite at its own price.
	got := pricing.Cost(ai.Usage{CacheWrite: 300_000, CacheWriteLong: 100_000})
	assert.InDelta(t, 2.5*0.2+4*0.1, got.CacheWrite, 1e-9)

	got = pricing.Cost(ai.Usage{CacheWrite: 600_000, CacheWriteLong: 600_000})
	assert.InDelta(t, 8*0.6, got.CacheWrite, 1e-9, "the tier price")
}

func TestPricing_CostWithNoPrices(t *testing.T) {
	var pricing ai.Pricing
	assert.Equal(t, ai.Cost{}, pricing.Cost(ai.Usage{Input: 1000, Output: 1000}))
}

func TestModel_JSONRoundTrip(t *testing.T) {
	m := ai.Model{
		ID:             "m",
		Name:           "M",
		MaxTokens:      128000,
		ContextWindow:  1_000_000,
		Input:          []ai.Modality{ai.ModalityText, ai.ModalityImage, ai.ModalityPDF},
		Reasoning:      true,
		ThinkingLevels: []ai.ThinkingLevel{ai.ThinkingLow, ai.ThinkingMax},
		Pricing: ai.Pricing{
			Input:  2,
			Output: 10,
			Tiers:  []ai.PriceTier{{InputAbove: 272_000, Input: 4, Output: 15}},
		},
	}

	data, err := json.Marshal(m)
	require.NoError(t, err)

	var got ai.Model
	require.NoError(t, json.Unmarshal(data, &got))
	assert.Equal(t, m, got)
}

func TestModel_JSONOmitsUnknownFields(t *testing.T) {
	data, err := json.Marshal(ai.Model{ID: "m", Name: "M"})
	require.NoError(t, err)
	assert.JSONEq(t, `{"id":"m","name":"M"}`, string(data))
}
