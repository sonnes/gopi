package def_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/def"
)

// failingSource is a source that always fails.
type failingSource struct{ err error }

func (s failingSource) Load(context.Context) (def.Set, error) {
	return def.Set{}, s.err
}

func TestLayers_LaterSourceWinsAtTheEarlierPosition(t *testing.T) {
	low := def.Set{
		Instructions: []def.Instruction{{Path: "low", Text: "low"}},
		Agents:       []def.Agent{{Name: "a", Prompt: "low"}, {Name: "b", Prompt: "low"}},
		Skills:       []def.Skill{{Name: "x", Body: "low"}, {Name: "y", Body: "low"}},
	}
	high := def.Set{
		Instructions: []def.Instruction{{Path: "high", Text: "high"}},
		Agents:       []def.Agent{{Name: "a", Prompt: "high"}},
		Skills:       []def.Skill{{Name: "x", Body: "high"}},
	}

	set, err := def.Layers(low, high).Load(t.Context())

	require.NoError(t, err)
	want := def.Set{
		Instructions: []def.Instruction{{Path: "low", Text: "low"}, {Path: "high", Text: "high"}},
		Agents:       []def.Agent{{Name: "a", Prompt: "high"}, {Name: "b", Prompt: "low"}},
		Skills:       []def.Skill{{Name: "x", Body: "high"}, {Name: "y", Body: "low"}},
	}
	assert.Equal(t, want, set)
}

func TestLayers_SkipsANilSource(t *testing.T) {
	set, err := def.Layers(nil, def.Set{Agents: []def.Agent{{Name: "a"}}}).Load(t.Context())

	require.NoError(t, err)
	assert.Equal(t, []def.Agent{{Name: "a"}}, set.Agents)
}

func TestLayers_ASourceErrorFails(t *testing.T) {
	boom := errors.New("boom")

	_, err := def.Layers(def.Set{}, failingSource{err: boom}).Load(t.Context())

	assert.ErrorIs(t, err, boom)
}

func TestSet_SkillNames(t *testing.T) {
	set := def.Set{Skills: []def.Skill{
		{Name: "deploy", Qualifier: "ops"},
		{Name: "lint"},
		{Name: "deploy"},
	}}

	assert.Equal(t, []string{"ops:deploy", "lint", "deploy"}, set.SkillNames())
}

func TestSet_Skill(t *testing.T) {
	set := def.Set{Skills: []def.Skill{
		{Name: "deploy", Qualifier: "ops", Body: "ops"},
		{Name: "deploy", Body: "bare"},
		{Name: "lint", Qualifier: "ci", Body: "lint"},
	}}

	tests := []struct {
		name string
		body string
		ok   bool
	}{
		{name: "deploy", body: "bare", ok: true},
		{name: "ops:deploy", body: "ops", ok: true},
		{name: "lint", body: "lint", ok: true},
		{name: "ci:lint", body: "lint", ok: true},
		{name: "missing"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, ok := set.Skill(tt.name)
			assert.Equal(t, tt.ok, ok)
			assert.Equal(t, tt.body, s.Body)
		})
	}
}

func TestSet_AgentGivesTheLastAgentWithTheName(t *testing.T) {
	set := def.Set{Agents: []def.Agent{
		{Name: "reviewer", Prompt: "first"},
		{Name: "writer"},
		{Name: "reviewer", Prompt: "last"},
	}}

	got, ok := set.Agent("reviewer")
	assert.True(t, ok)
	assert.Equal(t, "last", got.Prompt)

	_, ok = set.Agent("missing")
	assert.False(t, ok)
}

func TestAgent_InheritsModel(t *testing.T) {
	tests := []struct {
		model string
		want  bool
	}{
		{model: "", want: true},
		{model: "inherit", want: true},
		{model: "Inherit", want: true},
		{model: "opus"},
		{model: "anthropic/claude-opus-5-5"},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			a := def.Agent{Model: tt.model}
			assert.Equal(t, tt.want, a.InheritsModel())
		})
	}
}

func TestSet_Slash(t *testing.T) {
	set := def.Set{Skills: []def.Skill{
		{Name: "deploy", Qualifier: "ops"},
		{Name: "deploy"},
		{Name: "lw-tasks", NoModelCall: true},
	}}

	tests := []struct {
		text string
		name string
		rest string
		ok   bool
	}{
		{text: "/lw-tasks File a task", name: "lw-tasks", rest: " File a task", ok: true},
		{text: "/lw-tasks", name: "lw-tasks", ok: true},
		{text: "/lw-tasks\nFile a task", name: "lw-tasks", rest: "\nFile a task", ok: true},
		{text: "/deploy now", name: "deploy", rest: " now", ok: true},
		{text: "/ops:deploy now", name: "ops:deploy", rest: " now", ok: true},
		{text: "/missing now"},
		{text: "/lw-tasks, now"},
		{text: "lw-tasks now"},
		{text: " /lw-tasks now"},
		{text: "/"},
		{text: ""},
	}
	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			name, rest, ok := set.Slash(tt.text)
			assert.Equal(t, tt.ok, ok)
			assert.Equal(t, tt.name, name)
			assert.Equal(t, tt.rest, rest)
		})
	}
}

func TestSet_LoadGivesACopy(t *testing.T) {
	set := def.Set{Skills: []def.Skill{{Name: "deploy", Body: "b"}}}

	got, err := set.Load(t.Context())
	require.NoError(t, err)
	got.Skills[0].Body = "changed"

	assert.Equal(t, "b", set.Skills[0].Body)
}
