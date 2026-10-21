package env_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/ai/fireworks"
	"github.com/sonnes/gopi/pkg/ai/openrouter"
	"github.com/sonnes/gopi/pkg/catalog"
	"github.com/sonnes/gopi/pkg/catalog/env"
	"github.com/sonnes/gopi/pkg/catalog/models"
)

// The variables that the chain reads. Each test clears all of them, so
// the environment of the machine does not leak in.
var variables = []string{
	"ANTHROPIC_API_KEY",
	"OPENAI_API_KEY",
	"OPENROUTER_API_KEY",
	"FIREWORKS_API_KEY",
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
			name: "fireworks",
			set:  []string{"FIREWORKS_API_KEY"},
			want: []catalog.Detection{
				{Provider: "fireworks", Source: "FIREWORKS_API_KEY"},
			},
		},
		{
			name: "all, in chain order",
			set:  variables,
			want: []catalog.Detection{
				{Provider: "anthropic", Source: "ANTHROPIC_API_KEY"},
				{Provider: "openai", Source: "OPENAI_API_KEY"},
				{Provider: "openrouter", Source: "OPENROUTER_API_KEY"},
				{Provider: "fireworks", Source: "FIREWORKS_API_KEY"},
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
	require.NoError(t, sonnet)
	require.NoError(t, image)

	// The hosts register the models of models.json. openaichat serves
	// any server, so it registers none, and a caller lists them with
	// FetchModels.
	for _, spec := range []string{
		"openrouter/anthropic/claude-sonnet-5.5",
		"fireworks/accounts/fireworks/models/kimi-k3",
	} {
		_, err := c.LanguageModel(spec)
		require.NoError(t, err, spec)
	}
	_, err := c.LanguageModel("openaichat/llama-4")
	require.ErrorIs(t, err, catalog.ErrUnknownModel)

	// OpenRouter and Fireworks have no image API.
	c.RegisterModel("openrouter", ai.Model{ID: "openai/gpt-image-2"})
	_, err = c.ImageModel("openrouter/openai/gpt-image-2")
	require.EqualError(t, err, `catalog: provider "openrouter" does not support image generation`)
}

func TestDetectors_ListedProviders(t *testing.T) {
	for _, v := range variables {
		t.Setenv(v, "test-key")
	}
	c := catalog.New()
	c.Detect(env.Detectors()...)

	for _, id := range []string{"openrouter", "fireworks"} {
		c.RegisterModel(id, ai.Model{ID: "m"})
		lm, err := c.LanguageModel(id + "/m")
		require.NoError(t, err, id)
		assert.Implements(t, (*ai.ModelLister)(nil), lm.Provider, id)
	}
	lm, _ := c.LanguageModel("openrouter/m")
	assert.IsType(t, &openrouter.Provider{}, lm.Provider)
	lm, _ = c.LanguageModel("fireworks/m")
	assert.IsType(t, &fireworks.Provider{}, lm.Provider)
}

// fakePath sets PATH to a folder that holds an executable file for
// each name.
func fakePath(t *testing.T, names ...string) {
	t.Helper()
	dir := t.TempDir()
	for _, name := range names {
		path := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755))
	}
	t.Setenv("PATH", dir)
}

func TestDriverDetectors(t *testing.T) {
	tests := []struct {
		name      string
		path      []string
		want      []catalog.Detection
		wantSpecs []string
	}{
		{
			name: "no CLI",
		},
		{
			name: "claude",
			path: []string{"claude"},
			want: []catalog.Detection{
				{Provider: "claude-code", Source: "claude on PATH"},
			},
			wantSpecs: []string{"claude-code/opus"},
		},
		{
			name: "both, in chain order",
			path: []string{"codex", "claude"},
			want: []catalog.Detection{
				{Provider: "claude-code", Source: "claude on PATH"},
				{Provider: "codex", Source: "codex on PATH"},
			},
			wantSpecs: []string{"claude-code/opus", "codex/gpt-6.1-sol"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakePath(t, tt.path...)
			c := catalog.New()

			got := c.DetectDrivers(env.DriverDetectors()...)

			assert.Equal(t, tt.want, got)
			assert.Subset(t, c.DriverSpecs(), tt.wantSpecs)
			for _, spec := range tt.wantSpecs {
				_, _, err := c.Driver(spec)
				require.NoError(t, err, spec)
			}
		})
	}
}

// TestDetectors_ModelsFromFile checks that each detector with models
// registers the models of its entry in models.json, with the default
// model first.
func TestDetectors_ModelsFromFile(t *testing.T) {
	for _, d := range env.Detectors() {
		if len(d.Models) == 0 {
			continue
		}
		assert.Equal(t, models.Of(d.Provider), d.Models, d.Provider)
	}
	for _, d := range env.DriverDetectors() {
		require.NotEmpty(t, d.Models, d.Provider)
		assert.Equal(t, models.Of(d.Provider), d.Models, d.Provider)
	}
}

// TestDetectors_HostModels checks that the hosts register the models of
// models.json, so a spec of a host resolves with no fetch.
func TestDetectors_HostModels(t *testing.T) {
	for _, d := range env.Detectors() {
		if d.Provider == "openrouter" || d.Provider == "fireworks" {
			assert.NotEmpty(t, d.Models, d.Provider)
		}
	}
}
