package fs_test

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

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
	ctx := context.Background()
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"agents/reviewer.md":       "---\nname: reviewer\ndescription: Review a diff.\n---\nRead the diff.\n",
		"skills/deploy/SKILL.md":   "---\ndescription: Deploy the service.\n---\nRun make deploy.\n",
		"skills/deploy/scripts/go": "make deploy\n",
		"AGENTS.md":                "Use make.\n",
	})
	d := storefs.Dir(dir)

	agents, err := d.ReadAgents(ctx)
	require.NoError(t, err)
	require.Len(t, agents, 1)
	assert.Equal(t, "reviewer", agents[0].Name)
	assert.Equal(t, filepath.Join(dir, "agents", "reviewer.md"), agents[0].Path)

	skills, err := d.ReadSkills(ctx)
	require.NoError(t, err)
	require.Len(t, skills, 1)
	assert.Equal(t, "deploy", skills[0].Name)
	assert.Equal(t, filepath.Join(dir, "skills", "deploy", "SKILL.md"), skills[0].Path)
	script, err := os.ReadFile(filepath.Join(dir, "skills", "deploy", "scripts", "go"))
	require.NoError(t, err)
	got, err := readFile(skills[0], "scripts/go")
	require.NoError(t, err)
	assert.Equal(t, script, got)

	instructions, err := d.ReadInstructions(ctx)
	require.NoError(t, err)
	want := []def.Instruction{
		{
			Path: filepath.Join(dir, "AGENTS.md"),
			Text: "Use make.\n",
		},
	}
	assert.Equal(t, want, instructions)
}

func TestDirThatDoesNotExistGivesNothing(t *testing.T) {
	ctx := context.Background()
	d := storefs.Dir(filepath.Join(t.TempDir(), "missing"))

	agents, err := d.ReadAgents(ctx)
	require.NoError(t, err)
	assert.Empty(t, agents)

	skills, err := d.ReadSkills(ctx)
	require.NoError(t, err)
	assert.Empty(t, skills)

	instructions, err := d.ReadInstructions(ctx)
	require.NoError(t, err)
	assert.Empty(t, instructions)
}

func TestDirReadsAFolderThatAppearsLater(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "later")
	d := storefs.Dir(dir)

	instructions, err := d.ReadInstructions(ctx)
	require.NoError(t, err)
	assert.Empty(t, instructions)

	writeFiles(t, dir, map[string]string{"AGENTS.md": "Use make.\n"})
	instructions, err = d.ReadInstructions(ctx)
	require.NoError(t, err)
	assert.Len(t, instructions, 1)
}

func TestDirDoesNotFollowALinkOutOfTheFolder(t *testing.T) {
	ctx := context.Background()
	outside := t.TempDir()
	writeFiles(t, outside, map[string]string{"secret": "token\n"})
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"skills/deploy/SKILL.md": "---\ndescription: Deploy.\n---\nRun it.\n",
	})
	link := filepath.Join(dir, "skills", "deploy", "secret")
	require.NoError(t, os.Symlink(filepath.Join(outside, "secret"), link))

	skills, err := storefs.Dir(dir).ReadSkills(ctx)
	require.NoError(t, err)
	require.Len(t, skills, 1)
	_, err = readFile(skills[0], "secret")
	assert.Error(t, err)
}

func TestDirIsADefReader(t *testing.T) {
	var _ def.Reader = storefs.Dir(t.TempDir())
}

func TestDirReadsTheInstructionFilesThatItIsGiven(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"CLAUDE.md": "Claude rule.\n",
		"AGENTS.md": "Agents rule.\n",
	})

	got, err := storefs.Dir(dir, storefs.InstructionFiles("CLAUDE.md", "GEMINI.md", "AGENTS.md")).ReadInstructions(ctx)
	require.NoError(t, err)

	want := []def.Instruction{
		{
			Path: filepath.Join(dir, "CLAUDE.md"),
			Text: "Claude rule.\n",
		},
		{
			Path: filepath.Join(dir, "AGENTS.md"),
			Text: "Agents rule.\n",
		},
	}
	assert.Equal(t, want, got)
}
