package clidefs_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent/internal/clidefs"
)

// writeDir writes one folder with one skill and one agent.
func writeDir(t *testing.T, skill, agent, text string) string {
	t.Helper()
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "skills", skill)
	require.NoError(t, os.MkdirAll(skillDir, 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "agents"), 0o755))
	skillText := "---\ndescription: D.\n---\n" + text + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillText), 0o644))
	agentText := "---\nname: " + agent + "\ndescription: D.\n---\n" + text + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "agents", agent+".md"), []byte(agentText), 0o644))
	return dir
}

func TestPrepare_NoDirsNeedsNoFolder(t *testing.T) {
	d := clidefs.New(nil, t.TempDir(), "s1", "")

	set, dir, err := d.Prepare(t.Context())

	require.NoError(t, err)
	assert.Empty(t, set.Skills)
	assert.Empty(t, dir)
}

func TestPrepare_MergesTheDirsLaterWins(t *testing.T) {
	low := writeDir(t, "deploy", "reviewer", "low")
	high := writeDir(t, "deploy", "reviewer", "high")
	root := t.TempDir()
	d := clidefs.New([]string{low, high}, root, "s1", "")

	set, dir, err := d.Prepare(t.Context())

	require.NoError(t, err)
	assert.Equal(t, filepath.Join(root, "s1"), dir)
	require.Len(t, set.Skills, 1)
	assert.Equal(t, "high", set.Skills[0].Body)
	require.Len(t, set.Agents, 1)
	assert.Equal(t, "high", set.Agents[0].Prompt)
}

func TestPrepare_ClearsTheFolderOfTheLastProcess(t *testing.T) {
	root := t.TempDir()
	d := clidefs.New([]string{writeDir(t, "deploy", "reviewer", "x")}, root, "s1", "")
	_, dir, err := d.Prepare(t.Context())
	require.NoError(t, err)
	old := filepath.Join(dir, "old.md")
	require.NoError(t, os.WriteFile(old, nil, 0o600))

	_, _, err = d.Prepare(t.Context())

	require.NoError(t, err)
	assert.NoFileExists(t, old)
}

func TestPrepare_RejectsASessionThatLeavesTheRoot(t *testing.T) {
	d := clidefs.New([]string{writeDir(t, "deploy", "reviewer", "x")}, t.TempDir(), "../s1", "")

	_, _, err := d.Prepare(t.Context())

	assert.Error(t, err)
}

func TestRemove_RemovesTheFolder(t *testing.T) {
	d := clidefs.New([]string{writeDir(t, "deploy", "reviewer", "x")}, t.TempDir(), "s1", "")
	_, dir, err := d.Prepare(t.Context())
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(dir, 0o700))

	require.NoError(t, d.Remove())

	assert.NoDirExists(t, dir)
}

func TestCheck_FailsOnAnUnknownSkill(t *testing.T) {
	d := clidefs.New([]string{writeDir(t, "deploy", "reviewer", "x")}, t.TempDir(), "s1", "")

	require.NoError(t, d.Check(t.Context(), []string{"deploy"}))
	err := d.Check(t.Context(), []string{"missing"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), `"missing"`)
}

func TestCheck_NoNamesReadsNothing(t *testing.T) {
	d := clidefs.New([]string{"/does/not/exist"}, t.TempDir(), "s1", "")

	assert.NoError(t, d.Check(t.Context(), nil))
}

func TestModel(t *testing.T) {
	tests := []struct{ parent, model, want string }{
		{"claude-code/opus", "", ""},
		{"claude-code/opus", "inherit", ""},
		{"claude-code/opus", "sonnet", ""},
		{"claude-code/opus", "claude-code/sonnet", "sonnet"},
		{"codex/gpt-6", "claude-code/sonnet", ""},
		{"codex/gpt-6", "codex/gpt-7", "gpt-7"},
	}
	for _, tt := range tests {
		d := clidefs.New(nil, "", "s1", tt.parent)
		assert.Equal(t, tt.want, d.Model(tt.model), "%s on %s", tt.model, tt.parent)
	}
}
