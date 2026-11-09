package clidefs_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent/internal/clidefs"
	"github.com/sonnes/gopi/pkg/def"
)

// someDefs is a set with one skill and one agent.
var someDefs = def.Set{
	Skills: []def.Skill{{Name: "deploy", Description: "D.", Body: "b"}},
	Agents: []def.Agent{{Name: "reviewer", Description: "D.", Prompt: "p"}},
}

// failingSource is a source that always fails.
type failingSource struct{}

func (failingSource) Load(context.Context) (def.Set, error) {
	return def.Set{}, errors.New("disk on fire")
}

// useRoot gives the test its own temporary directory, and returns the
// folder that holds the folder of each session.
func useRoot(t *testing.T) string {
	t.Helper()
	t.Setenv("TMPDIR", t.TempDir())
	return filepath.Join(os.TempDir(), "gopi-defs")
}

func TestPrepare_GivesTheFolderOfTheSession(t *testing.T) {
	root := useRoot(t)

	dir, err := clidefs.New("s1", "").Prepare(someDefs)

	require.NoError(t, err)
	assert.Equal(t, filepath.Join(root, "s1"), dir)
	assert.DirExists(t, dir)
}

func TestPrepare_ClearsTheFolderOfTheLastProcess(t *testing.T) {
	useRoot(t)
	f := clidefs.New("s1", "")
	dir, err := f.Prepare(someDefs)
	require.NoError(t, err)
	old := filepath.Join(dir, "old.md")
	require.NoError(t, os.WriteFile(old, nil, 0o600))

	_, err = f.Prepare(someDefs)

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

			_, err := clidefs.New(session, "").Prepare(someDefs)

			assert.ErrorContains(t, err, "is not a file name")
		})
	}
}

func TestPrepare_AnEmptySetRemovesTheFolder(t *testing.T) {
	useRoot(t)
	f := clidefs.New("s1", "")
	folder, err := f.Prepare(someDefs)
	require.NoError(t, err)

	empty, err := f.Prepare(def.Set{})

	require.NoError(t, err)
	assert.Empty(t, empty)
	assert.NoDirExists(t, folder)
}

func TestRemove_RemovesTheFolder(t *testing.T) {
	useRoot(t)
	f := clidefs.New("s1", "")
	dir, err := f.Prepare(someDefs)
	require.NoError(t, err)

	require.NoError(t, f.Remove())

	assert.NoDirExists(t, dir)
}

func TestLoad_ANilSourceGivesNothing(t *testing.T) {
	set, err := clidefs.Load(t.Context(), nil)

	require.NoError(t, err)
	assert.Equal(t, def.Set{}, set)
}

func TestCheckSkills_FailsOnAnUnknownSkill(t *testing.T) {
	require.NoError(t, clidefs.CheckSkills(t.Context(), someDefs, []string{"deploy"}))

	err := clidefs.CheckSkills(t.Context(), someDefs, []string{"missing"})

	assert.ErrorContains(t, err, `"missing"`)
}

func TestCheckSkills_NoNamesLoadsNothing(t *testing.T) {
	assert.NoError(t, clidefs.CheckSkills(t.Context(), failingSource{}, nil))
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
		f := clidefs.New("s1", tt.parent)
		assert.Equal(t, tt.want, f.Model(tt.model), "%s on %s", tt.model, tt.parent)
	}
}
