package anthropic_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/ai/anthropic"
)

func TestModels(t *testing.T) {
	got := anthropic.Models()

	// The default model comes first.
	assert.Equal(t, []ai.Model{
		anthropic.Sonnet5_5,
		anthropic.Fable5_1,
		anthropic.Opus5_5,
		anthropic.Opus5,
		anthropic.Sonnet5,
		anthropic.Haiku4_5,
	}, got)
}

func TestModels_Limits(t *testing.T) {
	tests := []struct {
		model         ai.Model
		id            string
		maxTokens     int
		contextWindow int
		pricing       ai.Pricing
	}{
		{
			model:         anthropic.Fable5_1,
			id:            "claude-fable-5-1",
			maxTokens:     128_000,
			contextWindow: 1_000_000,
			pricing:       ai.Pricing{Input: 10, Output: 50, CacheRead: 0.25, CacheWrite: 12.5, CacheWriteLong: 20},
		},
		{
			model:         anthropic.Sonnet5_5,
			id:            "claude-sonnet-5-5",
			maxTokens:     128_000,
			contextWindow: 1_000_000,
			pricing:       ai.Pricing{Input: 2, Output: 10, CacheRead: 0.2, CacheWrite: 2.5, CacheWriteLong: 4},
		},
		{
			model:         anthropic.Haiku4_5,
			id:            "claude-haiku-4-5",
			maxTokens:     64_000,
			contextWindow: 200_000,
			pricing:       ai.Pricing{Input: 1, Output: 5, CacheRead: 0.1, CacheWrite: 1.25, CacheWriteLong: 2},
		},
	}

	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			assert.Equal(t, tt.id, tt.model.ID)
			assert.Equal(t, tt.maxTokens, tt.model.MaxTokens)
			assert.Equal(t, tt.contextWindow, tt.model.ContextWindow)
			assert.Equal(t, tt.pricing, tt.model.Pricing)
		})
	}
}

func TestModels_ThinkingLevels(t *testing.T) {
	// The four models that manage their effort always think. Haiku 4.5
	// takes only a token budget, which the provider does not send.
	for _, m := range []ai.Model{
		anthropic.Fable5_1,
		anthropic.Opus5_5,
		anthropic.Opus5,
		anthropic.Sonnet5_5,
	} {
		assert.NotContains(t, m.ThinkingLevels, ai.ThinkingOff, m.ID)
	}
	assert.Contains(t, anthropic.Sonnet5.ThinkingLevels, ai.ThinkingLow)
	assert.Equal(t, []ai.ThinkingLevel{ai.ThinkingOff}, anthropic.Haiku4_5.ThinkingLevels)
	assert.False(t, anthropic.Haiku4_5.Reasoning)
}
