package files_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/harness/files"
	"github.com/sonnes/gopi/pkg/harness/subagent"
)

func agentFile(name, description, prompt string) *fstest.MapFile {
	return file("---\nname: " + name + "\ndescription: " + description + "\n---\n" + prompt)
}

func agentNames(agents []subagent.Agent) []string {
	out := make([]string, len(agents))
	for i, a := range agents {
		out[i] = a.Name
	}
	return out
}

// readAgent reads the one agent of text as agents/a.md.
func readAgent(t *testing.T, text string) subagent.Agent {
	t.Helper()
	fsys := fstest.MapFS{
		"agents/a.md": file(text),
	}

	got, err := files.Agents(fsys, "agents").ReadAgents(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 1)
	return got[0]
}

func TestAgentsReadEveryFileInPathOrder(t *testing.T) {
	fsys := fstest.MapFS{
		".claude/agents/review.md":        agentFile("reviewer", "Review a diff.", "Read the diff.\n"),
		".claude/agents/team/deploy.md":   agentFile("deployer", "Deploy.", "Ship it.\n"),
		".claude/agents/team/a/b/deep.md": agentFile("deep", "Deep.", "body"),
		".claude/agents/team.md":          agentFile("team", "Team.", "body"),
		".claude/agents/README.md":        file("# Agents\n\nThe agents of the team.\n"),
		".claude/agents/notes.txt":        agentFile("notes", "Not markdown.", "body"),
	}

	got, err := files.Agents(fsys, ".claude/agents").ReadAgents(context.Background())
	require.NoError(t, err)

	assert.Equal(t, []string{"reviewer", "team", "deep", "deployer"}, agentNames(got))
	assert.Equal(t, ".claude/agents/review.md", got[0].Path)
	assert.Equal(t, "Read the diff.", got[0].Prompt)
}

func TestAgentFields(t *testing.T) {
	text := `---
name: reviewer
description: Review a diff.\nUse it after each change.
tools: Read, Grep, Bash(git diff *)
disallowedTools: [Write]
model: anthropic/claude-sonnet-4-5
permissionMode: acceptEdits
maxTurns: 12
skills: lint, test
color: blue
background: true
mcpServers:
  - github
---
Read the diff.
`

	got := readAgent(t, text)

	want := subagent.Agent{
		Name:        "reviewer",
		Description: "Review a diff.\nUse it after each change.",
		Tools: []string{
			"Read",
			"Grep",
			"Bash(git diff *)",
		},
		Disallowed: []string{"Write"},
		Model:      "anthropic/claude-sonnet-4-5",
		Mode:       "acceptEdits",
		MaxTurns:   12,
		Skills: []string{
			"lint",
			"test",
		},
		Prompt: "Read the diff.",
		Path:   "agents/a.md",
		Meta: map[string]any{
			"color":      "blue",
			"background": true,
			"mcpServers": []any{"github"},
		},
	}
	assert.Equal(t, want, got)
}

func TestAgentTools(t *testing.T) {
	tests := []struct {
		name string
		line string
		want []string
	}{
		{"absent", "", nil},
		{"empty", "tools:\n", []string{}},
		{"an empty string", "tools: \"\"\n", []string{}},
		{"an empty list", "tools: []\n", []string{}},
		{"a star", "tools: \"*\"\n", nil},
		{"a star in a list", "tools: [\"*\"]\n", nil},
		{"commas", "tools: Read, Grep\n", []string{"Read", "Grep"}},
		{"spaces", "tools: Read Grep\n", []string{"Read", "Grep"}},
		{"parentheses", "tools: Bash(git push *), Read\n", []string{"Bash(git push *)", "Read"}},
		{"a list", "tools:\n  - Read\n  - Bash(git push *)\n", []string{"Read", "Bash(git push *)"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := readAgent(t, "---\nname: a\ndescription: A.\n"+tt.line+"---\nbody")

			assert.Equal(t, tt.want, got.Tools)
		})
	}
}

func TestAgentMaxTurns(t *testing.T) {
	tests := []struct {
		name string
		line string
		want int
	}{
		{"absent", "", 0},
		{"a number", "maxTurns: 5\n", 5},
		{"a string", "maxTurns: \"7\"\n", 7},
		{"zero", "maxTurns: 0\n", 0},
		{"negative", "maxTurns: -3\n", 0},
		{"not a number", "maxTurns: many\n", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := readAgent(t, "---\nname: a\ndescription: A.\n"+tt.line+"---\nbody")

			assert.Equal(t, tt.want, got.MaxTurns)
		})
	}
}

func TestAgentsQuoteValuesThatAreNotValidYAML(t *testing.T) {
	got := readAgent(t, "---\nname: a\ndescription: Use when: the user asks for #review.\n---\nbody")

	assert.Equal(t, "Use when: the user asks for #review.", got.Description)
}

func TestAgentsSkipBadFiles(t *testing.T) {
	tests := []struct {
		name string
		file *fstest.MapFile
	}{
		{"no frontmatter", file("# Notes\n")},
		{"unclosed frontmatter", file("---\nname: x\ndescription: X.\n")},
		{"bad yaml", file("---\nname: x\ndescription: X.\n- item\n---\nbody\n")},
		{"no name", file("---\ndescription: X.\n---\nbody\n")},
		{"no description", file("---\nname: x\n---\nbody\n")},
		{"a name that is not text", file("---\nname: [x, y]\ndescription: X.\n---\nbody\n")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fsys := fstest.MapFS{
				"agents/bad.md":  tt.file,
				"agents/good.md": agentFile("good", "Works.", "body"),
			}

			got, err := files.Agents(fsys, "agents").ReadAgents(context.Background())
			require.NoError(t, err)

			assert.Equal(t, []string{"good"}, agentNames(got))
		})
	}
}

func TestAgentsThatFindNothingGiveNothing(t *testing.T) {
	ctx := context.Background()
	missing := os.DirFS(filepath.Join(t.TempDir(), "missing"))

	got, err := files.Agents(missing, ".claude/agents").ReadAgents(ctx)
	require.NoError(t, err)
	assert.Empty(t, got)

	got, err = files.Agents(fstest.MapFS{}, ".claude/agents").ReadAgents(ctx)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestAgentsFailOnAFileThatCannotBeRead(t *testing.T) {
	fsys := brokenFS{
		files: fstest.MapFS{
			"agents/a.md": agentFile("a", "A.", "body"),
		},
		broken: "agents/a.md",
	}

	_, err := files.Agents(fsys, "agents").ReadAgents(context.Background())
	assert.Error(t, err)
}
