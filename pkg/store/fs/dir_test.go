package fs_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/def"
	storefs "github.com/sonnes/gopi/pkg/store/fs"
)

// writeFiles writes each file of files under dir, with its folders.
func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, text := range files {
		path := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(text), 0o644))
	}
}

// readFile reads a support file of s.
func readFile(s def.Skill, name string) ([]byte, error) {
	return fs.ReadFile(s.Files, name)
}

func TestDirReadsTheDefinitionsOfAFolder(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"agents/reviewer.md":       "---\nname: reviewer\ndescription: Review a diff.\n---\nRead the diff.\n",
		"skills/deploy/SKILL.md":   "---\ndescription: Deploy the service.\n---\nRun make deploy.\n",
		"skills/deploy/scripts/go": "make deploy\n",
		"AGENTS.md":                "Use make.\n",
	})

	set, err := storefs.Dir(dir).Load(t.Context())
	require.NoError(t, err)

	require.Len(t, set.Agents, 1)
	assert.Equal(t, "reviewer", set.Agents[0].Name)
	assert.Equal(t, filepath.Join(dir, "agents", "reviewer.md"), set.Agents[0].Path)

	require.Len(t, set.Skills, 1)
	assert.Equal(t, "deploy", set.Skills[0].Name)
	assert.Equal(t, filepath.Join(dir, "skills", "deploy", "SKILL.md"), set.Skills[0].Path)
	got, err := readFile(set.Skills[0], "scripts/go")
	require.NoError(t, err)
	assert.Equal(t, "make deploy\n", string(got))

	want := []def.Instruction{
		{
			Path: filepath.Join(dir, "AGENTS.md"),
			Text: "Use make.\n",
		},
	}
	assert.Equal(t, want, set.Instructions)
}

func TestDirThatDoesNotExistGivesNothing(t *testing.T) {
	set, err := storefs.Dir(filepath.Join(t.TempDir(), "missing")).Load(t.Context())

	require.NoError(t, err)
	assert.Equal(t, def.Set{}, set)
}

func TestDirReadsAFolderThatAppearsLater(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "later")
	d := storefs.Dir(dir)

	set, err := d.Load(t.Context())
	require.NoError(t, err)
	assert.Empty(t, set.Instructions)

	writeFiles(t, dir, map[string]string{"AGENTS.md": "Use make.\n"})
	set, err = d.Load(t.Context())
	require.NoError(t, err)
	assert.Len(t, set.Instructions, 1)
}

func TestDirDoesNotFollowALinkOutOfTheFolder(t *testing.T) {
	outside := t.TempDir()
	writeFiles(t, outside, map[string]string{"secret": "token\n"})
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"skills/deploy/SKILL.md": "---\ndescription: Deploy.\n---\nRun it.\n",
	})
	link := filepath.Join(dir, "skills", "deploy", "secret")
	require.NoError(t, os.Symlink(filepath.Join(outside, "secret"), link))

	set, err := storefs.Dir(dir).Load(t.Context())
	require.NoError(t, err)
	require.Len(t, set.Skills, 1)
	_, err = readFile(set.Skills[0], "secret")
	assert.Error(t, err)
}

func TestInstructionFiles(t *testing.T) {
	files := map[string]string{
		"CLAUDE.md": "Claude rule.\n",
		"AGENTS.md": "Agents rule.\n",
	}
	opt := storefs.InstructionFiles("CLAUDE.md", "GEMINI.md", "AGENTS.md")

	t.Run("Dir", func(t *testing.T) {
		dir := t.TempDir()
		writeFiles(t, dir, files)

		set, err := storefs.Dir(dir, opt).Load(t.Context())
		require.NoError(t, err)

		want := []def.Instruction{
			{Path: filepath.Join(dir, "CLAUDE.md"), Text: "Claude rule.\n"},
			{Path: filepath.Join(dir, "AGENTS.md"), Text: "Agents rule.\n"},
		}
		assert.Equal(t, want, set.Instructions)
	})
	t.Run("FS", func(t *testing.T) {
		fsys := fstest.MapFS{}
		for name, text := range files {
			fsys[name] = file(text)
		}

		set, err := storefs.FS(fsys, opt).Load(t.Context())
		require.NoError(t, err)

		want := []def.Instruction{
			{Path: "CLAUDE.md", Text: "Claude rule.\n"},
			{Path: "AGENTS.md", Text: "Agents rule.\n"},
		}
		assert.Equal(t, want, set.Instructions)
	})
}
