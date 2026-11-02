package fs_test

import (
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/def"
	storefs "github.com/sonnes/gopi/pkg/store/fs"
)

func TestWriteSkills_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	skill := def.Skill{
		Name:         "deploy",
		Description:  "Deploy the app.\n\nUse when: the user asks.",
		Body:         "Run the script.",
		AllowedTools: []string{"Bash(git push *)", "Read"},
		Model:        "opus",
		NoModelCall:  true,
		Fork:         true,
		Agent:        "ops",
		Extra:        def.Extra{"license": "MIT"},
	}

	err := storefs.WriteSkills(dir, []def.Skill{skill}, []string{"deploy"})
	require.NoError(t, err)

	got, err := storefs.Skills(os.DirFS(dir), ".").ReadSkills(t.Context())
	require.NoError(t, err)
	require.Len(t, got, 1)
	got[0].Files = nil
	got[0].Path = ""
	assert.Equal(t, skill, got[0])
}

func TestWriteSkills_CopiesTheFilesOfASkill(t *testing.T) {
	dir := t.TempDir()
	skill := def.Skill{
		Name:        "deploy",
		Description: "Deploy.",
		Body:        "New body.",
		Files: fstest.MapFS{
			"SKILL.md":       {Data: []byte("old")},
			"bin/run.sh":     {Data: []byte("#!/bin/sh\n"), Mode: 0o755},
			"docs/notes.txt": {Data: []byte("notes")},
		},
	}

	err := storefs.WriteSkills(dir, []def.Skill{skill}, []string{"deploy"})
	require.NoError(t, err)

	notes, err := os.ReadFile(filepath.Join(dir, "deploy", "docs", "notes.txt"))
	require.NoError(t, err)
	assert.Equal(t, "notes", string(notes))
	info, err := os.Stat(filepath.Join(dir, "deploy", "bin", "run.sh"))
	require.NoError(t, err)
	assert.NotZero(t, info.Mode().Perm()&0o100, "a script stays executable")
	main, err := os.ReadFile(filepath.Join(dir, "deploy", "SKILL.md"))
	require.NoError(t, err)
	assert.Contains(t, string(main), "New body.")
}

func TestWriteSkills_UsesTheNameThatTheModelSees(t *testing.T) {
	dir := t.TempDir()
	skills := []def.Skill{
		{Name: "deploy", Qualifier: "ops", Description: "Ops.", Body: "ops"},
		{Name: "deploy", Description: "Plain.", Body: "plain"},
	}

	err := storefs.WriteSkills(dir, skills, []string{"ops:deploy", "deploy"})
	require.NoError(t, err)

	assert.FileExists(t, filepath.Join(dir, "ops:deploy", "SKILL.md"))
	assert.FileExists(t, filepath.Join(dir, "deploy", "SKILL.md"))
}

func TestWriteAgents_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	agent := def.Agent{
		Name:        "reviewer",
		Description: "Review a diff.",
		Tools:       []string{"Read", "Grep"},
		Disallowed:  []string{"Bash"},
		Model:       "opus",
		Effort:      ai.ThinkingHigh,
		Mode:        "plan",
		MaxTurns:    3,
		Skills:      []string{"deploy"},
		Prompt:      "Read the diff.",
		Extra:       def.Extra{"color": "blue"},
	}

	err := storefs.WriteAgents(dir, []def.Agent{agent})
	require.NoError(t, err)

	got, err := storefs.Agents(os.DirFS(dir), ".").ReadAgents(t.Context())
	require.NoError(t, err)
	require.Len(t, got, 1)
	got[0].Path = ""
	assert.Equal(t, agent, got[0])
}

func TestWriteAgents_KeepsNoToolsAndAllTools(t *testing.T) {
	dir := t.TempDir()
	agents := []def.Agent{
		{Name: "all", Description: "All.", Prompt: "p"},
		{Name: "none", Description: "None.", Tools: []string{}, Prompt: "p"},
	}

	err := storefs.WriteAgents(dir, agents)
	require.NoError(t, err)

	got, err := storefs.Agents(os.DirFS(dir), ".").ReadAgents(t.Context())
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Nil(t, got[0].Tools)
	assert.NotNil(t, got[1].Tools)
	assert.Empty(t, got[1].Tools)
}

// A name comes from a file of a folder that a person can clone, so it
// must not write outside the folder.
func TestWrite_SkipsANameThatLeavesTheFolder(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "out")

	err := storefs.WriteAgents(dir, []def.Agent{
		{Name: "../evil", Description: "E.", Prompt: "p"},
		{Name: "good", Description: "G.", Prompt: "p"},
	})
	require.NoError(t, err)
	err = storefs.WriteSkills(
		dir,
		[]def.Skill{{Name: "x", Description: "E.", Body: "b"}},
		[]string{"../evil-skill"},
	)
	require.NoError(t, err)

	assert.NoFileExists(t, filepath.Join(root, "evil.md"))
	assert.NoDirExists(t, filepath.Join(root, "evil-skill"))
	assert.FileExists(t, filepath.Join(dir, "good.md"))
}

func TestOneElement(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{name: "deploy", want: true},
		{name: "ops:deploy", want: true},
		{name: "", want: false},
		{name: ".", want: false},
		{name: "..", want: false},
		{name: "a/b", want: false},
		{name: `a\b`, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, storefs.OneElement(tt.name))
		})
	}
}
