package clidefs_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent/internal/clidefs"
	"github.com/sonnes/gopi/pkg/def"
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

// useRoot gives the test its own temporary directory, and returns the
// folder that holds the folder of each session.
func useRoot(t *testing.T) string {
	t.Helper()
	t.Setenv("TMPDIR", t.TempDir())
	return filepath.Join(os.TempDir(), "gopi-defs")
}

func TestPrepare_NoDirsNeedsNoFolder(t *testing.T) {
	useRoot(t)
	d := clidefs.New(nil, nil, "s1", "")

	set, dir, err := d.Prepare(t.Context())

	require.NoError(t, err)
	assert.Empty(t, set.Skills)
	assert.Empty(t, dir)
}

func TestPrepare_MergesTheDirsLaterWins(t *testing.T) {
	low := writeDir(t, "deploy", "reviewer", "low")
	high := writeDir(t, "deploy", "reviewer", "high")
	root := useRoot(t)
	d := clidefs.New(nil, []string{low, high}, "s1", "")

	set, dir, err := d.Prepare(t.Context())

	require.NoError(t, err)
	assert.Equal(t, filepath.Join(root, "s1"), dir)
	require.Len(t, set.Skills, 1)
	assert.Equal(t, "high", set.Skills[0].Body)
	require.Len(t, set.Agents, 1)
	assert.Equal(t, "high", set.Agents[0].Prompt)
}

func TestPrepare_ADirWinsOverASkillset(t *testing.T) {
	useRoot(t)
	inline := []def.SkillsReader{
		def.Skill{Name: "deploy", Description: "D.", Body: "inline"},
		def.Skill{Name: "kept", Description: "D.", Body: "inline"},
	}
	dir := writeDir(t, "deploy", "reviewer", "dir")
	d := clidefs.New(inline, []string{dir}, "s1", "")

	set, _, err := d.Prepare(t.Context())

	require.NoError(t, err)
	require.Len(t, set.Skills, 2)
	assert.Equal(t, "dir", set.Skills[0].Body)
	assert.Equal(t, "inline", set.Skills[1].Body)
}

func TestPrepare_ASkillsetNeedsNoDirs(t *testing.T) {
	inline := []def.SkillsReader{def.Skill{Name: "deploy", Description: "D.", Body: "b"}}
	root := useRoot(t)
	d := clidefs.New(inline, nil, "s1", "")

	require.NoError(t, d.Check(t.Context(), []string{"deploy"}))
	_, dir, err := d.Prepare(t.Context())

	require.NoError(t, err)
	assert.Equal(t, filepath.Join(root, "s1"), dir)
}

func TestPrepare_ClearsTheFolderOfTheLastProcess(t *testing.T) {
	useRoot(t)
	d := clidefs.New(nil, []string{writeDir(t, "deploy", "reviewer", "x")}, "s1", "")
	_, dir, err := d.Prepare(t.Context())
	require.NoError(t, err)
	old := filepath.Join(dir, "old.md")
	require.NoError(t, os.WriteFile(old, nil, 0o600))

	_, _, err = d.Prepare(t.Context())

	require.NoError(t, err)
	assert.NoFileExists(t, old)
}

func TestPrepare_RejectsASessionThatIsNotAFolderName(t *testing.T) {
	tests := []string{
		"",
		".",
		"..",
		"../s1",
		"a/b",
	}
	for _, session := range tests {
		t.Run(session, func(t *testing.T) {
			useRoot(t)
			d := clidefs.New(nil, []string{writeDir(t, "deploy", "reviewer", "x")}, session, "")

			_, _, err := d.Prepare(t.Context())

			assert.ErrorContains(t, err, "is not a folder name")
		})
	}
}

func TestPrepare_AnEmptySetRemovesTheFolder(t *testing.T) {
	root := useRoot(t)
	dir := writeDir(t, "deploy", "reviewer", "x")
	d := clidefs.New(nil, []string{dir}, "s1", "")
	_, folder, err := d.Prepare(t.Context())
	require.NoError(t, err)
	require.Equal(t, filepath.Join(root, "s1"), folder)
	require.DirExists(t, folder)
	require.NoError(t, os.RemoveAll(dir))

	_, empty, err := d.Prepare(t.Context())

	require.NoError(t, err)
	assert.Empty(t, empty)
	assert.NoDirExists(t, folder)
}

func TestPrepare_FailsOnASkillNameWithASlash(t *testing.T) {
	useRoot(t)
	inline := []def.SkillsReader{
		def.Skill{Name: "deploy", Qualifier: "apps/web", Description: "D.", Body: "web"},
		def.Skill{Name: "deploy", Description: "D.", Body: "plain"},
	}
	d := clidefs.New(inline, nil, "s1", "")

	_, _, err := d.Prepare(t.Context())

	assert.ErrorContains(t, err, `the skill "apps/web:deploy" is not a folder name`)
}

func TestRemove_RemovesTheFolder(t *testing.T) {
	useRoot(t)
	d := clidefs.New(nil, []string{writeDir(t, "deploy", "reviewer", "x")}, "s1", "")
	_, dir, err := d.Prepare(t.Context())
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(dir, 0o700))

	require.NoError(t, d.Remove())

	assert.NoDirExists(t, dir)
}

func TestCheck_FailsOnAnUnknownSkill(t *testing.T) {
	useRoot(t)
	d := clidefs.New(nil, []string{writeDir(t, "deploy", "reviewer", "x")}, "s1", "")

	require.NoError(t, d.Check(t.Context(), []string{"deploy"}))
	err := d.Check(t.Context(), []string{"missing"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), `"missing"`)
}

func TestCheck_NoNamesReadsNothing(t *testing.T) {
	useRoot(t)
	d := clidefs.New(nil, []string{"/does/not/exist"}, "s1", "")

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
		d := clidefs.New(nil, nil, "s1", tt.parent)
		assert.Equal(t, tt.want, d.Model(tt.model), "%s on %s", tt.model, tt.parent)
	}
}
