package fs_test

import (
	"context"
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

func file(text string) *fstest.MapFile {
	return &fstest.MapFile{Data: []byte(text)}
}

func skillFile(description, body string) *fstest.MapFile {
	return file("---\ndescription: " + description + "\n---\n" + body)
}

func skillNames(skills []def.Skill) []string {
	out := make([]string, len(skills))
	for i, s := range skills {
		out[i] = s.Name
	}
	return out
}

// brokenFS is a MapFS that fails to open the file broken.
// It has only the Open method, so each call of the io/fs functions
// goes through Open.
type brokenFS struct {
	files  fstest.MapFS
	broken string
}

func (b brokenFS) Open(name string) (fs.File, error) {
	if name == b.broken {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrPermission}
	}
	return b.files.Open(name)
}

// readSkill reads the one skill of text as skills/deploy/SKILL.md.
func readSkill(t *testing.T, text string) def.Skill {
	t.Helper()
	fsys := fstest.MapFS{
		"skills/deploy/SKILL.md": file(text),
	}

	got, err := storefs.Skills(fsys, "skills").ReadSkills(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 1)
	return got[0]
}

func TestSkillsReadsEachSkillFolder(t *testing.T) {
	fsys := fstest.MapFS{
		".agents/skills/deploy/SKILL.md":       skillFile("Deploy the service.", "\nRun scripts/run.sh.\n"),
		".agents/skills/deploy/scripts/run.sh": file("echo deploy\n"),
		".agents/skills/review/SKILL.md":       skillFile("Review a diff.", "Read the diff.\n"),
		".agents/skills/notes/README.md":       file("not a skill\n"),
		".agents/skills/README.md":             file("not a skill\n"),
		".agents/skills/deep/nested/SKILL.md":  skillFile("Too deep.", "body"),
	}

	got, err := storefs.Skills(fsys, ".agents/skills").ReadSkills(context.Background())
	require.NoError(t, err)

	require.Equal(t, []string{"deploy", "review"}, skillNames(got))
	deploy := got[0]
	assert.Equal(t, "Deploy the service.", deploy.Description)
	assert.Equal(t, "Run scripts/run.sh.", deploy.Body)
	assert.Equal(t, ".agents/skills/deploy/SKILL.md", deploy.Path)
	assert.Empty(t, deploy.Qualifier)
	assert.Nil(t, deploy.Extra)

	script, err := fs.ReadFile(deploy.Files, "scripts/run.sh")
	require.NoError(t, err)
	assert.Equal(t, "echo deploy\n", string(script))
}

func TestSkillsTakeTheNameOfTheFolder(t *testing.T) {
	tests := []struct {
		name      string
		text      string
		wantExtra def.Extra
	}{
		{
			name: "no name",
			text: "---\ndescription: Deploy.\n---\nbody",
		},
		{
			name: "the same name",
			text: "---\nname: deploy\ndescription: Deploy.\n---\nbody",
		},
		{
			name:      "another name",
			text:      "---\nname: ship\ndescription: Deploy.\n---\nbody",
			wantExtra: def.Extra{"name": "ship"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := readSkill(t, tt.text)

			assert.Equal(t, "deploy", got.Name)
			assert.Equal(t, tt.wantExtra, got.Extra)
		})
	}
}

func TestSkillDescription(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{
			name: "description",
			text: "---\ndescription: Deploy the service.\n---\nbody",
			want: "Deploy the service.",
		},
		{
			name: "description and when_to_use",
			text: "---\ndescription: Deploy the service.\nwhen_to_use: The user asks to ship.\n---\nbody",
			want: "Deploy the service.\n\nThe user asks to ship.",
		},
		{
			name: "no description",
			text: "---\nmodel: haiku\n---\n\n\nDeploy the service.\nRun it.",
			want: "Deploy the service.",
		},
		{
			name: "no description and a heading",
			text: "---\nmodel: haiku\n---\n# Deploy the service\n\nRun it.",
			want: "Deploy the service",
		},
		{
			name: "no description and when_to_use",
			text: "---\nwhen_to_use: The user asks to ship.\n---\nDeploy the service.",
			want: "Deploy the service.\n\nThe user asks to ship.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := readSkill(t, tt.text)

			assert.Equal(t, tt.want, got.Description)
		})
	}
}

func TestSkillFields(t *testing.T) {
	text := `---
description: Deploy.
allowed-tools: Bash(git push *), Read
model: claude-haiku-4-5
disable-model-invocation: "True"
context: fork
agent: deployer
license: MIT
user-invocable: false
argument-hint: "[env]"
metadata:
  owner: ops
---
Run it.
`

	got := readSkill(t, text)

	want := def.Skill{
		Name:        "deploy",
		Description: "Deploy.",
		Body:        "Run it.",
		Path:        "skills/deploy/SKILL.md",
		AllowedTools: []string{
			"Bash(git push *)",
			"Read",
		},
		Model:       "claude-haiku-4-5",
		NoModelCall: true,
		Fork:        true,
		Agent:       "deployer",
		Extra: map[string]any{
			"license":        "MIT",
			"user-invocable": false,
			"argument-hint":  "[env]",
			"metadata":       map[string]any{"owner": "ops"},
		},
	}
	got.Files = nil
	assert.Equal(t, want, got)
}

func TestSkillAllowedTools(t *testing.T) {
	tests := []struct {
		name string
		line string
		want []string
	}{
		{"absent", "", nil},
		{"empty", "allowed-tools:\n", nil},
		{"commas", "allowed-tools: Read, Grep\n", []string{"Read", "Grep"}},
		{"spaces", "allowed-tools: Read Grep\n", []string{"Read", "Grep"}},
		{"parentheses", "allowed-tools: Bash(git push *) Bash(git status), Read\n", []string{"Bash(git push *)", "Bash(git status)", "Read"}},
		{"a list", "allowed-tools:\n  - Read\n  - Bash(git log *)\n", []string{"Read", "Bash(git log *)"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := readSkill(t, "---\ndescription: Deploy.\n"+tt.line+"---\nbody")

			assert.Equal(t, tt.want, got.AllowedTools)
		})
	}
}

func TestSkillsQuoteValuesThatAreNotValidYAML(t *testing.T) {
	text := `---
description: Deploy. Use when: the user asks to ship.
argument-hint: [env] [region]
---
body`

	got := readSkill(t, text)

	assert.Equal(t, "Deploy. Use when: the user asks to ship.", got.Description)
	assert.Equal(t, def.Extra{"argument-hint": "[env] [region]"}, got.Extra)
}

func TestSkillsSkipBadSkills(t *testing.T) {
	tests := []struct {
		name string
		file *fstest.MapFile
	}{
		{"no frontmatter", file("Just text.\n")},
		{"unclosed frontmatter", file("---\ndescription: x\n")},
		{"bad yaml", file("---\ndescription: x\n- item\n---\nbody\n")},
		{"a description that is not text", file("---\ndescription:\n  a: b\n---\nbody\n")},
		{"no description and no body", file("---\nmodel: haiku\n---\n\n")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fsys := fstest.MapFS{
				"skills/bad/SKILL.md":  tt.file,
				"skills/good/SKILL.md": skillFile("Works.", "body"),
			}

			got, err := storefs.Skills(fsys, "skills").ReadSkills(context.Background())
			require.NoError(t, err)

			assert.Equal(t, []string{"good"}, skillNames(got))
		})
	}
}

func TestSkillsReadWindowsLineEnds(t *testing.T) {
	got := readSkill(t, "---\r\ndescription: Deploy.\r\n---\r\nRun it.\r\n")

	assert.Equal(t, "Deploy.", got.Description)
	assert.Equal(t, "Run it.", got.Body)
}

func TestSkillsOnDisk(t *testing.T) {
	repo := t.TempDir()
	dir := filepath.Join(repo, ".claude", "skills", "deploy")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	data := []byte("---\ndescription: Deploy.\n---\nRun it.\n")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), data, 0o644))
	root, err := os.OpenRoot(repo)
	require.NoError(t, err)
	t.Cleanup(func() { root.Close() })

	got, err := storefs.Skills(root.FS(), ".claude/skills").ReadSkills(context.Background())
	require.NoError(t, err)

	assert.Equal(t, []string{"deploy"}, skillNames(got))
}

func TestSkillsThatFindNothingGiveNothing(t *testing.T) {
	ctx := context.Background()
	empty := fstest.MapFS{}
	missing := os.DirFS(filepath.Join(t.TempDir(), "missing"))

	for _, fsys := range []fs.FS{empty, missing} {
		got, err := storefs.Skills(fsys, ".agents/skills").ReadSkills(ctx)
		require.NoError(t, err)
		assert.Empty(t, got)
	}
}

func TestSkillsFailOnAFileThatCannotBeRead(t *testing.T) {
	fsys := brokenFS{
		files: fstest.MapFS{
			"skills/deploy/SKILL.md": skillFile("Deploy.", "body"),
		},
		broken: "skills/deploy/SKILL.md",
	}

	_, err := storefs.Skills(fsys, "skills").ReadSkills(context.Background())
	assert.Error(t, err)
}
