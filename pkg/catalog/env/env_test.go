package env_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/catalog"
	"github.com/sonnes/gopi/pkg/catalog/env"
)

// The variables that the chain reads. Each test clears all of them, so
// the environment of the machine does not leak in.
var variables = []string{
	"ANTHROPIC_API_KEY",
	"OPENAI_API_KEY",
	"OPENROUTER_API_KEY",
}

func TestDetectors(t *testing.T) {
	tests := []struct {
		name string
		set  []string
		want []catalog.Detection
	}{
		{
			name: "nothing set",
		},
		{
			name: "anthropic",
			set:  []string{"ANTHROPIC_API_KEY"},
			want: []catalog.Detection{
				{Provider: "anthropic", Source: "ANTHROPIC_API_KEY"},
			},
		},
		{
			name: "openai serves the Responses API and the Chat Completions API",
			set:  []string{"OPENAI_API_KEY"},
			want: []catalog.Detection{
				{Provider: "openai", Source: "OPENAI_API_KEY"},
				{Provider: "openaichat", Source: "OPENAI_API_KEY"},
			},
		},
		{
			name: "openrouter",
			set:  []string{"OPENROUTER_API_KEY"},
			want: []catalog.Detection{
				{Provider: "openrouter", Source: "OPENROUTER_API_KEY"},
			},
		},
		{
			name: "all, in chain order",
			set:  variables,
			want: []catalog.Detection{
				{Provider: "anthropic", Source: "ANTHROPIC_API_KEY"},
				{Provider: "openai", Source: "OPENAI_API_KEY"},
				{Provider: "openrouter", Source: "OPENROUTER_API_KEY"},
				{Provider: "openaichat", Source: "OPENAI_API_KEY"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, v := range variables {
				t.Setenv(v, "")
			}
			for _, v := range tt.set {
				t.Setenv(v, "test-key")
			}
			c := catalog.New()

			got := c.Detect(env.Detectors()...)

			assert.Equal(t, tt.want, got)
		})
	}
}

func TestDetectors_Capabilities(t *testing.T) {
	for _, v := range variables {
		t.Setenv(v, "test-key")
	}
	c := catalog.New()
	c.Detect(env.Detectors()...)

	_, sonnet := c.LanguageModel("claude-sonnet-5")
	_, image := c.ImageModel("gpt-image-2")
	_, routed := c.LanguageModel("openrouter/qwen/qwen3-coder")
	_, chat := c.LanguageModel("openaichat/llama-4")

	require.NoError(t, sonnet)
	require.NoError(t, image)
	require.NoError(t, routed)
	require.NoError(t, chat)
}
