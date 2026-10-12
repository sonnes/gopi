package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/ai/anthropic"
	"github.com/sonnes/gopi/pkg/ai/openai"
	"github.com/sonnes/gopi/pkg/ai/openaichat"
)

func TestNewModel(t *testing.T) {
	tests := []struct {
		name         string
		provider     string
		model        string
		env          map[string]string
		wantName     string
		wantModel    ai.Model
		wantProvider ai.TextProvider
		wantErr      string
	}{
		{
			name:         "the Anthropic key picks Anthropic and its first model",
			env:          map[string]string{"ANTHROPIC_API_KEY": "k", "OPENAI_API_KEY": "k"},
			wantName:     "anthropic",
			wantModel:    anthropic.Models()[0],
			wantProvider: &anthropic.Provider{},
		},
		{
			name:         "the OpenAI key picks OpenAI",
			env:          map[string]string{"OPENAI_API_KEY": "k"},
			wantName:     "openai",
			wantModel:    openai.Models()[0],
			wantProvider: &openai.Provider{},
		},
		{
			name:    "no key and no provider",
			wantErr: "no provider",
		},
		{
			name:         "a known model keeps its prices",
			provider:     "anthropic",
			model:        anthropic.Opus5_5.ID,
			wantName:     "anthropic",
			wantModel:    anthropic.Opus5_5,
			wantProvider: &anthropic.Provider{},
		},
		{
			name:         "an unknown model is sent as its ID",
			provider:     "openaichat",
			model:        "llama-9",
			wantName:     "openaichat",
			wantModel:    ai.Model{ID: "llama-9"},
			wantProvider: &openaichat.Provider{},
		},
		{
			name:     "openaichat has no default model",
			provider: "openaichat",
			wantErr:  "-model",
		},
		{
			name:     "an unknown provider",
			provider: "nope",
			wantErr:  `unknown provider "nope"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			getenv := func(key string) string { return tt.env[key] }

			lm, name, err := newModel(tt.provider, tt.model, getenv)

			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantName, name)
			assert.Equal(t, tt.wantModel, lm.Model)
			assert.IsType(t, tt.wantProvider, lm.Provider)
		})
	}
}

func TestNewTools(t *testing.T) {
	tests := []struct {
		name    string
		names   string
		want    []string
		wantErr string
	}{
		{
			name:  "the default gives every tool",
			names: defaultTools,
			want: []string{
				"read_file",
				"write_file",
				"edit_file",
				"bash",
				"grep",
				"find",
				"todo_write",
				"ask_user_question",
			},
		},
		{
			name:  "a list picks tools in its order",
			names: "grep, read_file",
			want:  []string{"grep", "read_file"},
		},
		{
			name:  "an empty list gives no tool",
			names: "",
		},
		{
			name:    "an unknown tool",
			names:   "read_file,nope",
			wantErr: `unknown tool "nope"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tools, err := newTools(t.TempDir(), tt.names, true, terminal{})

			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			var got []string
			for _, tl := range tools {
				got = append(got, tl.Info().Name)
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNewTools_Fence(t *testing.T) {
	tests := []struct {
		name    string
		fenced  bool
		isError bool
	}{
		{
			name:    "the fence keeps .git/config read-only",
			fenced:  true,
			isError: true,
		},
		{
			name:   "without the fence, the tools write anywhere",
			fenced: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0o755))
			tools, err := newTools(root, "write_file", tt.fenced, terminal{})
			require.NoError(t, err)

			res, err := tools[0].Run(t.Context(), ai.ToolCall{
				ID:    "call_1",
				Name:  "write_file",
				Input: []byte(`{"path":".git/config","content":"[core]"}`),
			})

			require.NoError(t, err)
			assert.Equal(t, tt.isError, res.IsError)
		})
	}
}

func TestFenceError(t *testing.T) {
	err := fenceError(fmt.Errorf("sandbox: fence: no backend for linux: %w", errors.ErrUnsupported))

	require.ErrorIs(t, err, errors.ErrUnsupported)
	assert.ErrorContains(t, err, "--fence=false")
	assert.Equal(t, errors.New("boom"), fenceError(errors.New("boom")))
}

func TestSessionsDir(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{
			name: "GOPI_HOME",
			env:  map[string]string{"GOPI_HOME": "/data/gopi", "HOME": "/home/ada"},
			want: "/data/gopi/sessions",
		},
		{
			name: "the home of the user",
			env:  map[string]string{"HOME": "/home/ada"},
			want: "/home/ada/.gopi/sessions",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			getenv := func(k string) string { return tt.env[k] }
			got, err := sessionsDir(getenv)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNewAgentKeepsASession(t *testing.T) {
	dir := t.TempDir()
	lm := ai.LanguageModel{Model: ai.Model{ID: "test"}}

	a, err := newAgent(t.Context(), lm, nil, "ada", dir)
	require.NoError(t, err)
	assert.Equal(t, "ada", a.Log().Session().ID)
	assert.FileExists(t, filepath.Join(dir, "ada.jsonl"))

	b, err := newAgent(t.Context(), lm, nil, "", dir)
	require.NoError(t, err)
	assert.Empty(t, b.Log().Session().ID, "with no session ID, the agent keeps no session")
}
