package host_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/def"
	"github.com/sonnes/gopi/pkg/host"
)

// reviewer is an agent file on the fake model.
const reviewer = `---
name: reviewer
description: Review a diff.
model: fake/m
effort: high
permissionMode: acceptEdits
disallowedTools: [Bash]
skills: [deploy]
---
You review.
`

// writeAgent writes the agent file name.md with text in dir/agents.
func writeAgent(t *testing.T, dir, name, text string) {
	t.Helper()
	agents := filepath.Join(dir, "agents")
	require.NoError(t, os.MkdirAll(agents, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(agents, name+".md"), []byte(text), 0o644))
}

// lastConfig returns the config of the last start of the driver.
func (f fixture) lastConfig(t *testing.T) agent.Config {
	t.Helper()
	f.driver.mu.Lock()
	defer f.driver.mu.Unlock()
	require.NotEmpty(t, f.driver.configs)
	return f.driver.configs[len(f.driver.configs)-1]
}

func TestStartWithAnAgentFillsTheSpec(t *testing.T) {
	f := newFixture(t)
	dir := t.TempDir()
	writeAgent(t, dir, "reviewer", reviewer)

	_, err := f.h.Start(context.Background(), host.Spec{
		ID:     "s1",
		Dirs:   []string{dir},
		Agent:  "reviewer",
		System: "Be brief.",
		Deny:   []string{"Write"},
		Skills: []string{"lint"},
	})
	require.NoError(t, err)

	c := f.lastConfig(t)
	assert.Equal(t, "fake/m", c.Spec)
	assert.Equal(t, ai.ThinkingHigh, c.Effort)
	assert.Equal(t, agent.Mode("acceptEdits"), c.Mode)
	assert.Equal(t, "You review.\n\nBe brief.", c.System)
	assert.Equal(t, []string{"Write", "Bash"}, c.Deny)
	assert.Equal(t, []string{"lint", "deploy"}, c.Skills)
	assert.Equal(t, []string{dir}, c.Dirs)
}

func TestAFieldOfTheSpecWinsOverTheAgentFile(t *testing.T) {
	f := newFixture(t)
	dir := t.TempDir()
	writeAgent(t, dir, "reviewer", reviewer)

	_, err := f.h.Start(context.Background(), host.Spec{
		ID:     "s1",
		Dirs:   []string{dir},
		Agent:  "reviewer",
		Mode:   "plan",
		Effort: ai.ThinkingLow,
	})
	require.NoError(t, err)

	c := f.lastConfig(t)
	assert.Equal(t, agent.Mode("plan"), c.Mode)
	assert.Equal(t, ai.ThinkingLow, c.Effort)
}

func TestTheAgentOfALaterDirWins(t *testing.T) {
	f := newFixture(t)
	first := t.TempDir()
	later := t.TempDir()
	writeAgent(t, first, "reviewer", reviewer)
	writeAgent(t, later, "reviewer", "---\nname: reviewer\ndescription: Later.\nmodel: fake/m\n---\nYou review later.\n")

	_, err := f.h.Start(context.Background(), host.Spec{
		ID:    "s1",
		Dirs:  []string{first, later},
		Agent: "reviewer",
	})
	require.NoError(t, err)

	assert.Equal(t, "You review later.", f.lastConfig(t).System)
}

func TestStartWithAnUnknownAgentFails(t *testing.T) {
	f := newFixture(t)

	_, err := f.h.Start(context.Background(), host.Spec{
		ID:    "s1",
		Model: "fake/m",
		Dirs:  []string{t.TempDir()},
		Agent: "reviewer",
	})

	assert.ErrorIs(t, err, def.ErrNotFound)
	_, ok := f.h.Session("s1")
	assert.False(t, ok)
}

func TestAResumedSessionRunsItsAgent(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	dir := t.TempDir()
	writeAgent(t, dir, "reviewer", reviewer)
	spec := host.Spec{
		ID:    "s1",
		Dirs:  []string{dir},
		Agent: "reviewer",
	}
	s, err := f.h.Start(ctx, spec)
	require.NoError(t, err)
	require.NoError(t, s.Close())

	snap, err := f.store.Load(ctx, "s1")
	require.NoError(t, err)
	pinned, ok, err := host.SessionAgent.Read(snap)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "reviewer", pinned.Name)
	assert.Equal(t, filepath.Join(dir, "agents", "reviewer.md"), pinned.Path)

	writeAgent(t, dir, "reviewer", "---\nname: reviewer\ndescription: New.\nmodel: fake/m\n---\nYou review again.\n")
	for _, name := range []string{"reviewer", ""} {
		spec.Agent = name
		s, err = f.h.Start(ctx, spec)
		require.NoError(t, err)
		assert.Equal(t, "You review.", f.lastConfig(t).System, "agent %q", name)
		assert.Equal(t, ai.ThinkingHigh, f.lastConfig(t).Effort, "agent %q", name)
		require.NoError(t, s.Close())
	}
}

func TestAResumeWithAnotherAgentFails(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	dir := t.TempDir()
	writeAgent(t, dir, "reviewer", reviewer)
	writeAgent(t, dir, "writer", "---\nname: writer\ndescription: Write.\nmodel: fake/m\n---\nYou write.\n")
	s, err := f.h.Start(ctx, host.Spec{
		ID:    "s1",
		Dirs:  []string{dir},
		Agent: "reviewer",
	})
	require.NoError(t, err)
	require.NoError(t, s.Close())

	_, err = f.h.Start(ctx, host.Spec{
		ID:    "s1",
		Dirs:  []string{dir},
		Agent: "writer",
	})

	assert.ErrorContains(t, err, `started with the agent "reviewer"`)
}

func TestStartWithAnUnknownEffortFails(t *testing.T) {
	dir := t.TempDir()
	writeAgent(t, dir, "typo", "---\nname: typo\ndescription: Typo.\nmodel: fake/m\neffort: hgih\n---\nHi.\n")
	tests := []struct {
		name string
		spec host.Spec
		want string
	}{
		{
			name: "in the agent file",
			spec: host.Spec{Dirs: []string{dir}, Agent: "typo"},
			want: `"hgih"`,
		},
		{
			name: "in the spec",
			spec: host.Spec{Model: "fake/m", Effort: "huge"},
			want: `"huge"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)

			_, err := f.h.Start(context.Background(), tt.spec)

			assert.ErrorContains(t, err, tt.want)
		})
	}
}

func TestTheSessionKeepsItsAgentBeforeTheDriverStarts(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.driver.err = errors.New("no agent")
	dir := t.TempDir()
	writeAgent(t, dir, "reviewer", reviewer)

	_, err := f.h.Start(ctx, host.Spec{
		ID:    "s1",
		Dirs:  []string{dir},
		Agent: "reviewer",
	})
	require.Error(t, err)

	snap, err := f.store.Load(ctx, "s1")
	require.NoError(t, err)
	pinned, ok, err := host.SessionAgent.Read(snap)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "reviewer", pinned.Name)
}
