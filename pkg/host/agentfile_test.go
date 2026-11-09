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
	"github.com/sonnes/gopi/pkg/permission"
)

// reviewer is an agent file on the fake model.
var reviewer = reviewerWith("You review.")

// reviewerWith returns the agent file of reviewer with the prompt
// prompt.
func reviewerWith(prompt string) string {
	return `---
name: reviewer
description: Review a diff.
model: fake/m
effort: high
permissionMode: acceptEdits
tools: [Read, Grep]
disallowedTools: [Bash]
skills: [deploy]
---
` + prompt + "\n"
}

// agentPrompt returns the text of the section [host.AgentSection] of c.
func agentPrompt(t *testing.T, c agent.Config) string {
	t.Helper()
	require.NotNil(t, c.Sections)
	sections, err := c.Sections(context.Background())
	require.NoError(t, err)
	for _, s := range sections {
		if s.Name == host.AgentSection {
			return s.Text
		}
	}
	return ""
}

// writeAgent writes the agent file name.md with text in dir/agents.
func writeAgent(t *testing.T, dir, name, text string) {
	t.Helper()
	agents := filepath.Join(dir, "agents")
	require.NoError(t, os.MkdirAll(agents, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(agents, name+".md"), []byte(text), 0o644))
}

func TestStartWithAnAgentFillsTheSpec(t *testing.T) {
	f := newFixture(t)
	dir := t.TempDir()
	writeAgent(t, dir, "reviewer", reviewer)

	_, err := f.h.Create(context.Background(), host.Config{
		ID:      "s1",
		DefDirs: []string{dir},
		Agent:   "reviewer",
		Deny:    []string{"write_file"},
		Skills:  []string{"lint"},
	})
	require.NoError(t, err)

	c := f.lastConfig(t)
	assert.Equal(t, "fake/m", c.Spec)
	assert.Equal(t, ai.ThinkingHigh, c.Effort)
	assert.Equal(t, permission.Mode("acceptEdits"), c.Mode)
	assert.Equal(t, []string{"write_file", "Bash"}, c.Deny)
	assert.Equal(t, []string{"Read", "Grep"}, c.ToolNames)
	assert.Equal(t, []string{"lint", "deploy"}, c.Skills)
	set, err := c.Defs.Load(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "reviewer", set.Agents[0].Name, "the driver gets the folders")
}

func TestAFieldOfTheSpecWinsOverTheAgentFile(t *testing.T) {
	f := newFixture(t)
	dir := t.TempDir()
	writeAgent(t, dir, "reviewer", reviewer)

	_, err := f.h.Create(context.Background(), host.Config{
		ID:      "s1",
		DefDirs: []string{dir},
		Agent:   "reviewer",
		Mode:    "acceptEdits",
		Effort:  ai.ThinkingLow,
	})
	require.NoError(t, err)

	c := f.lastConfig(t)
	assert.Equal(t, permission.AcceptEdits, c.Mode)
	assert.Equal(t, ai.ThinkingLow, c.Effort)
}

func TestTheAgentOfALaterDirWins(t *testing.T) {
	f := newFixture(t)
	first := t.TempDir()
	later := t.TempDir()
	writeAgent(t, first, "reviewer", reviewer)
	writeAgent(t, later, "reviewer", "---\nname: reviewer\ndescription: Later.\nmodel: fake/m\n---\nYou review later.\n")

	_, err := f.h.Create(context.Background(), host.Config{
		ID:      "s1",
		DefDirs: []string{first, later},
		Agent:   "reviewer",
	})
	require.NoError(t, err)

	assert.Equal(t, "You review later.", agentPrompt(t, f.lastConfig(t)))
}

func TestStartWithAnUnknownAgentFails(t *testing.T) {
	f := newFixture(t)

	_, err := f.h.Create(context.Background(), host.Config{
		ID:      "s1",
		Model:   "fake/m",
		DefDirs: []string{t.TempDir()},
		Agent:   "reviewer",
	})

	assert.ErrorIs(t, err, host.ErrInvalid)
	_, ok := f.h.Get("s1")
	assert.False(t, ok)
}

func TestAStoredSessionRunsItsAgent(t *testing.T) {
	f := newFixture(t)
	dir := t.TempDir()
	writeAgent(t, dir, "reviewer", reviewer)
	f.create(t, host.Config{
		ID:      "s1",
		DefDirs: []string{dir},
		Agent:   "reviewer",
	})
	f = f.restart(t)

	writeAgent(t, dir, "reviewer", "---\nname: reviewer\ndescription: New.\nmodel: fake/m\n---\nYou review again.\n")
	f.send(t, "s1", "a")

	assert.Equal(t, "You review again.", agentPrompt(t, f.lastConfig(t)), "an edit of the prompt reaches the next run")
	assert.Equal(t, ai.ThinkingHigh, f.lastConfig(t).Effort, "an edit of the effort changes only a new session")
}

func TestStartWithAnUnknownEffortFails(t *testing.T) {
	dir := t.TempDir()
	writeAgent(t, dir, "typo", "---\nname: typo\ndescription: Typo.\nmodel: fake/m\neffort: hgih\n---\nHi.\n")
	tests := []struct {
		name   string
		config host.Config
		want   string
	}{
		{
			name:   "in the agent file",
			config: host.Config{DefDirs: []string{dir}, Agent: "typo"},
			want:   `"hgih"`,
		},
		{
			name:   "in the config",
			config: host.Config{Model: "fake/m", Effort: "huge"},
			want:   `"huge"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)

			_, err := f.h.Create(context.Background(), tt.config)

			assert.ErrorIs(t, err, host.ErrInvalid)
			assert.ErrorContains(t, err, tt.want)
		})
	}
}

func TestTheSessionKeepsItsSpecBeforeTheDriverStarts(t *testing.T) {
	f := newFixture(t)
	f.driver.setErr(errors.New("no agent"))
	dir := t.TempDir()
	writeAgent(t, dir, "reviewer", reviewer)

	_, err := f.h.Create(context.Background(), host.Config{
		ID:      "s1",
		DefDirs: []string{dir},
		Agent:   "reviewer",
	})
	require.Error(t, err)

	f.driver.setErr(nil)
	f = f.restart(t)
	f.send(t, "s1", "a")
	assert.Equal(t, "You review.", agentPrompt(t, f.lastConfig(t)))
}

func TestTheToolsOfTheSpecWinOverTheAgentFile(t *testing.T) {
	f := newFixture(t)
	dir := t.TempDir()
	writeAgent(t, dir, "reviewer", reviewer)

	_, err := f.h.Create(context.Background(), host.Config{
		ID:        "s1",
		DefDirs:   []string{dir},
		Agent:     "reviewer",
		ToolNames: []string{"bash"},
	})
	require.NoError(t, err)

	assert.Equal(t, []string{"bash"}, f.lastConfig(t).ToolNames)
}

func TestDefsReachEachDriverStart(t *testing.T) {
	inline := def.Skill{Name: "deploy", Description: "D.", Body: "b"}
	f := newFixture(t, host.Defs(def.Set{Skills: []def.Skill{inline}}))

	f.create(t, host.Config{ID: "s1"})

	set, err := f.lastConfig(t).Defs.Load(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []def.Skill{inline}, set.Skills)
}

func TestAnAgentFileOfTheHostDefs(t *testing.T) {
	f := newFixture(t, host.Defs(def.Set{Agents: []def.Agent{{
		Name:        "reviewer",
		Description: "Review.",
		Model:       "fake/m",
		Prompt:      "You review.",
	}}}))

	_, err := f.h.Create(context.Background(), host.Config{
		ID:    "s1",
		Agent: "reviewer",
	})
	require.NoError(t, err)

	assert.Equal(t, "fake/m", f.lastConfig(t).Spec)
	assert.Equal(t, "You review.", agentPrompt(t, f.lastConfig(t)))
}

func TestAnInheritModelInAnyCaseGivesNoModel(t *testing.T) {
	f := newFixture(t)
	dir := t.TempDir()
	writeAgent(t, dir, "reviewer", "---\nname: reviewer\ndescription: R.\nmodel: Inherit\n---\nYou review.\n")

	_, err := f.h.Create(context.Background(), host.Config{
		ID:      "s1",
		Model:   "fake/m",
		DefDirs: []string{dir},
		Agent:   "reviewer",
	})
	require.NoError(t, err)

	assert.Equal(t, "fake/m", f.lastConfig(t).Spec)
}

// noCompactor is a strategy that makes no edits.
type noCompactor struct{}

func (noCompactor) Limit(ai.Model) int { return 0 }

func (noCompactor) Compact(context.Context, agent.CompactInput) (agent.CompactResult, error) {
	return agent.CompactResult{}, nil
}

func TestCompactionReachesEachDriverStart(t *testing.T) {
	f := newFixture(t, host.Compaction(noCompactor{}))

	f.create(t, host.Config{ID: "s1"})

	assert.Equal(t, noCompactor{}, f.lastConfig(t).Compaction)
}
