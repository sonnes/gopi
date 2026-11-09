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

	err := storefs.WriteSkills(filepath.Join(dir, "skills"), []def.Skill{skill}, []string{"deploy"})
	require.NoError(t, err)

	got, err := skillsOf(t.Context(), os.DirFS(dir))
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
		Mode:        "acceptEdits",
		MaxTurns:    3,
		Skills:      []string{"deploy"},
		Prompt:      "Read the diff.",
		Extra:       def.Extra{"color": "blue"},
	}

	err := storefs.WriteAgents(filepath.Join(dir, "agents"), []def.Agent{agent})
	require.NoError(t, err)

	got, err := agentsOf(t.Context(), os.DirFS(dir))
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

	err := storefs.WriteAgents(filepath.Join(dir, "agents"), agents)
	require.NoError(t, err)

	got, err := agentsOf(t.Context(), os.DirFS(dir))
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Nil(t, got[0].Tools)
	assert.NotNil(t, got[1].Tools)
	assert.Empty(t, got[1].Tools)
}

// A name comes from a file of a folder that a person can clone, so it
// must not write outside the folder.
func TestWrite_FailsOnANameThatLeavesTheFolder(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "out")

	err := storefs.WriteAgents(dir, []def.Agent{
		{Name: "../evil", Description: "E.", Prompt: "p"},
	})
	assert.ErrorContains(t, err, "../evil")
	err = storefs.WriteSkills(
		dir,
		[]def.Skill{{Name: "x", Description: "E.", Body: "b"}},
		[]string{"../evil-skill"},
	)
	assert.ErrorContains(t, err, "../evil-skill")

	assert.NoFileExists(t, filepath.Join(root, "evil.md"))
	assert.NoDirExists(t, filepath.Join(root, "evil-skill"))
}

func TestCheckName(t *testing.T) {
	tests := []struct {
		name string
		ok   bool
	}{
		{name: "deploy", ok: true},
		{name: "ops:deploy", ok: true},
		{name: ""},
		{name: "."},
		{name: ".."},
		{name: "a/b"},
		{name: `a\b`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := storefs.CheckName(tt.name)
			if tt.ok {
				assert.NoError(t, err)
				return
			}
			assert.Error(t, err)
		})
	}
}
