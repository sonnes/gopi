package claude_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/agent/claude"
	"github.com/sonnes/gopi/pkg/agent/internal/drivertest"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/def"
	"github.com/sonnes/gopi/pkg/permission"
	"github.com/sonnes/gopi/pkg/session"
	storefs "github.com/sonnes/gopi/pkg/store/fs"
	"github.com/sonnes/gopi/pkg/tool"
)

var _ agent.Runner = (*claude.Agent)(nil)

// stopHook is a hook at BeforeStop.
type stopHook struct{ agent.BaseHook }

func (stopHook) BeforeStop(context.Context, agent.State) ([]session.Entry, error) {
	return nil, nil
}

func TestDriverStart(t *testing.T) {
	starts := claude.Replay(t, nil, "testdata/text.trace")
	d := claude.Driver(claude.Binary("/opt/claude"))

	r, err := d.Start(context.Background(), agent.Config{
		SessionID: "s1",
		Store:     session.NewMemoryStore(),
		Spec:      "claude-code/haiku",
		Model:     ai.Model{ID: "haiku"},
		Dir:       "/work",
	})
	require.NoError(t, err)
	a, ok := r.(*claude.Agent)
	require.True(t, ok, "the driver starts a *claude.Agent")
	defer closeAgent(t, a)

	_, _, err = run(context.Background(), a, "Say hello")
	require.NoError(t, err)

	cmds := starts.Cmds()
	require.Len(t, cmds, 1)
	cmd := cmds[0]
	assert.Equal(t, "/opt/claude", cmd.Args[0], "the driver gives its options")
	assert.Equal(t, "/work", cmd.Dir)
	assert.Contains(t, strings.Join(cmd.Args, " "), "--model haiku", "the driver gives the config")
}

func TestDriverDefs(t *testing.T) {
	low := t.TempDir()
	writeAgent(t, low, "penny.md", `---
name: penny
description: Old penny
---
Old prompt.
`)
	writeAgent(t, low, "rex.md", `---
name: rex
description: Reviews a diff
model: inherit
tools: Read, bash
---
Review the diff.
`)
	high := t.TempDir()
	writeAgent(t, high, "penny.md", `---
name: penny
description: Counts coins
model: claude-code/claude-sonnet-5-5
---
Count the coins.
`)
	writeAgent(t, high, "bea.md", `---
name: bea
description: Names a bare model
model: sonnet
---
Answer.
`)
	writeAgent(t, high, "otto.md", `---
name: otto
description: Runs on another provider
model: openai/gpt-5
---
Answer.
`)

	root := defsRoot(t)
	starts := claude.Replay(t, nil, "testdata/text.trace")
	r, err := claude.Driver().Start(context.Background(), agent.Config{
		SessionID: "s1",
		Store:     session.NewMemoryStore(),
		Spec:      "claude-code/claude-opus-5-5",
		Defs:      def.Layers(storefs.Dir(low), storefs.Dir(high)),
	})
	require.NoError(t, err)
	a, ok := r.(*claude.Agent)
	require.True(t, ok, "the driver starts a *claude.Agent")
	defer closeAgent(t, a)

	_, _, err = run(context.Background(), a, "Say hello")
	require.NoError(t, err)

	args := starts.Cmds()[0].Args
	assert.NotContains(t, args, "--agents", "the folder holds the agents")
	assert.NotContains(t, args, "--agent", "the host gives the prompt of the agent of the session")

	written, err := storefs.Dir(filepath.Join(root, "s1", ".claude")).Load(context.Background())
	require.NoError(t, err)
	byName := make(map[string]def.Agent, len(written.Agents))
	for _, agent := range written.Agents {
		byName[agent.Name] = agent
	}
	require.Len(t, byName, 4)
	assert.Equal(t, "Counts coins", byName["penny"].Description, "a later folder wins")
	assert.Equal(t, "claude-sonnet-5-5", byName["penny"].Model, "a spec of the account gives its ID")
	assert.Empty(t, byName["bea"].Model, "a bare model gives the model of the parent")
	assert.Empty(t, byName["otto"].Model, "another account gives the model of the parent")
	assert.Empty(t, byName["rex"].Model, "inherit gives the model of the parent")
	assert.Equal(t, []string{"Read", "Bash"}, byName["rex"].Tools, "the file has Claude Code names")
}

// writeAgent writes an agent file to the agents folder of dir.
func writeAgent(t *testing.T, dir, name, text string) {
	t.Helper()
	agents := filepath.Join(dir, "agents")
	require.NoError(t, os.MkdirAll(agents, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(agents, name), []byte(text), 0o644))
}

// defsDir writes a folder with the skill deploy and the agent reviewer.
func defsDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	skills := filepath.Join(dir, "skills", "deploy")
	agents := filepath.Join(dir, "agents")
	require.NoError(t, os.MkdirAll(skills, 0o755))
	require.NoError(t, os.MkdirAll(agents, 0o755))
	skill := "---\ndescription: Deploy.\n---\nDeploy it.\n"
	require.NoError(t, os.WriteFile(filepath.Join(skills, "SKILL.md"), []byte(skill), 0o644))
	reviewer := "---\nname: reviewer\ndescription: Review.\nmodel: codex/gpt-6\n---\nReview it.\n"
	require.NoError(t, os.WriteFile(filepath.Join(agents, "reviewer.md"), []byte(reviewer), 0o644))
	return dir
}

// defsRoot gives the test its own temporary directory, and returns the
// folder that holds the folder of each session.
func defsRoot(t *testing.T) string {
	t.Helper()
	t.Setenv("TMPDIR", t.TempDir())
	return filepath.Join(os.TempDir(), "gopi-defs")
}

func TestDriverWritesNoEffortForOff(t *testing.T) {
	claude.Replay(t, nil, "testdata/text.trace")
	root := defsRoot(t)
	dir := t.TempDir()
	agents := filepath.Join(dir, "agents")
	require.NoError(t, os.MkdirAll(agents, 0o755))
	text := "---\nname: quiet\ndescription: D.\neffort: off\n---\nAnswer.\n"
	require.NoError(t, os.WriteFile(filepath.Join(agents, "quiet.md"), []byte(text), 0o644))

	r, err := claude.Driver().Start(context.Background(), agent.Config{
		SessionID: "s1",
		Store:     session.NewMemoryStore(),
		Defs:      storefs.Dir(dir),
	})
	require.NoError(t, err)
	a, ok := r.(*claude.Agent)
	require.True(t, ok)
	defer closeAgent(t, a)

	_, _, err = run(context.Background(), a, "Say hello")
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(root, "s1", ".claude", "agents", "quiet.md"))
	require.NoError(t, err)
	assert.NotContains(t, string(data), "effort")
}

func TestDriverConformance(t *testing.T) {
	drivertest.Run(t, drivertest.Suite{
		Driver:          claude.Driver(),
		UnsupportedHook: callHook{},
		SkillDir:        filepath.Join(".claude", "skills"),
		WriteDefs: func(t *testing.T, r agent.Runner) {
			claude.Replay(t, nil, "testdata/text.trace")
			_, err := r.Run(context.Background(), session.Text("Say hello")).Wait()
			require.NoError(t, err)
		},
	})
}

func TestDriverGivesTheDefsToTheCLI(t *testing.T) {
	starts := claude.Replay(t, nil, "testdata/text.trace")
	root := defsRoot(t)

	r, err := claude.Driver().Start(context.Background(), agent.Config{
		SessionID: "s1",
		Store:     session.NewMemoryStore(),
		Defs:      storefs.Dir(defsDir(t)),
	})
	require.NoError(t, err)
	a, ok := r.(*claude.Agent)
	require.True(t, ok)
	defer closeAgent(t, a)

	_, _, err = run(context.Background(), a, "Say hello")
	require.NoError(t, err)

	folder := filepath.Join(root, "s1")
	cmds := starts.Cmds()
	require.Len(t, cmds, 1)
	assert.Contains(t, strings.Join(cmds[0].Args, " "), "--add-dir "+folder)
	agentFile, err := os.ReadFile(filepath.Join(folder, ".claude", "agents", "reviewer.md"))
	require.NoError(t, err)
	assert.NotContains(t, string(agentFile), "gpt-6", "the model of another CLI stays out")
}

func TestDriverGivesEachAddDirToTheCLI(t *testing.T) {
	starts := claude.Replay(t, nil, "testdata/text.trace")

	r, err := claude.Driver().Start(context.Background(), agent.Config{
		SessionID: "s1",
		Store:     session.NewMemoryStore(),
		AddDirs:   []string{"/lw/repos", "/lw/shared"},
	})
	require.NoError(t, err)
	a, ok := r.(*claude.Agent)
	require.True(t, ok)
	defer closeAgent(t, a)

	_, _, err = run(context.Background(), a, "Say hello")
	require.NoError(t, err)

	args := strings.Join(starts.Cmds()[0].Args, " ")
	assert.Contains(t, args, "--add-dir /lw/repos")
	assert.Contains(t, args, "--add-dir /lw/shared")
}

func TestDriverGivesTheLimitsToTheCLI(t *testing.T) {
	tests := []struct {
		name   string
		config agent.Config
		want   string
	}{
		{
			name: "each limit",
			config: agent.Config{
				Limits: permission.Limits{BlockReadsOutside: true, BlockWritesOutside: true, Fence: true},
			},
			want: `{"permissions":{"blockReadsOutsideWorkingDirectories":true,"blockWritesOutsideWorkingDirectories":true},"sandbox":{"enabled":true}}`,
		},
		{
			name: "one limit",
			config: agent.Config{
				Limits: permission.Limits{BlockWritesOutside: true},
			},
			want: `{"permissions":{"blockWritesOutsideWorkingDirectories":true}}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			starts := claude.Replay(t, nil, "testdata/text.trace")
			tt.config.SessionID = "s1"
			tt.config.Store = session.NewMemoryStore()

			r, err := claude.Driver().Start(context.Background(), tt.config)
			require.NoError(t, err)
			a, ok := r.(*claude.Agent)
			require.True(t, ok)
			defer closeAgent(t, a)
			_, _, err = run(context.Background(), a, "Say hello")
			require.NoError(t, err)

			args := starts.Cmds()[0].Args
			i := slices.Index(args, "--settings")
			require.GreaterOrEqual(t, i, 0, "the CLI gets --settings")
			assert.JSONEq(t, tt.want, args[i+1])
		})
	}
}

func TestDriverToolNames(t *testing.T) {
	starts := claude.Replay(t, nil, "testdata/only_tools_mcp.trace")
	d := claude.Driver()

	r, err := d.Start(context.Background(), agent.Config{
		SessionID: "s1",
		Store:     session.NewMemoryStore(),
		Tools:     []tool.Tool{tool.Named(tool.ReadFile), tool.Named(tool.Bash)},
		ToolNames: []string{"read_file", "bash(git diff *)", "mcp__lw__threads_get"},
	})
	require.NoError(t, err)
	a, ok := r.(*claude.Agent)
	require.True(t, ok, "the driver starts a *claude.Agent")
	defer closeAgent(t, a)

	_, msgs, err := run(context.Background(), a, "Send it")
	require.NoError(t, err, "the hook denies the MCP tool that ToolNames does not name")

	args := strings.Join(starts.Cmds()[0].Args, " ")
	assert.Contains(t, args, "--tools Read,Bash,ToolSearch", "--tools lists the built-in tools only")
	require.Len(t, msgs, 3)
	assert.Equal(t, "mcp__lw__threads_send", msgs[0].Content[0].(ai.ToolCall).Name)
	assert.True(t, msgs[1].IsError)
}

func TestDriverTools(t *testing.T) {
	tools := []tool.Tool{
		tool.Named(tool.Bash),
		tool.Named(tool.ReadFile),
		tool.Named(tool.ApplyPatch),
		tool.Named(tool.Skill),
	}
	tests := []struct {
		name   string
		config agent.Config
		want   string
	}{
		{
			name:   "no tools",
			config: agent.Config{},
			want:   "--tools ToolSearch",
		},
		{
			name:   "each tool of the list that Claude Code has",
			config: agent.Config{Tools: tools},
			want:   "--tools Bash,Read,Skill,ToolSearch",
		},
		{
			name:   "a named tool that the registry does not hold",
			config: agent.Config{Tools: []tool.Tool{tool.Named(tool.Bash), tool.Named("DesignSync")}},
			want:   "--tools Bash,DesignSync,ToolSearch",
		},
		{
			name:   "a tool of the caller is skipped",
			config: agent.Config{Tools: append([]tool.Tool{tool.Func("lookup", nil)}, tools...)},
			want:   "--tools Bash,Read,Skill,ToolSearch",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			starts := claude.Replay(t, nil, "testdata/text.trace")
			config := tt.config
			config.SessionID = "s1"
			config.Store = session.NewMemoryStore()

			r, err := claude.Driver().Start(context.Background(), config)
			require.NoError(t, err)
			a, ok := r.(*claude.Agent)
			require.True(t, ok)
			defer closeAgent(t, a)
			_, _, err = run(context.Background(), a, "Say hello")
			require.NoError(t, err)

			args := strings.Join(starts.Cmds()[0].Args, " ")
			assert.Contains(t, args+" ", tt.want+" ")
		})
	}
}

func TestDriverMovesTheTextOfASkillCallLast(t *testing.T) {
	tests := []struct {
		name  string
		trace string
		text  string
	}{
		{name: "a skill", trace: "testdata/skill_last.trace", text: "/deploy now"},
		{name: "no skill", trace: "testdata/skill_unknown.trace", text: "/missing now"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claude.Replay(t, nil, tt.trace)
			defsRoot(t)

			r, err := claude.Driver().Start(context.Background(), agent.Config{
				SessionID: "s1",
				Store:     session.NewMemoryStore(),
				Defs:      def.Set{Skills: []def.Skill{{Name: "deploy", Description: "D.", Body: "Do it."}}},
			})
			require.NoError(t, err)
			a, ok := r.(*claude.Agent)
			require.True(t, ok)
			defer closeAgent(t, a)

			input := session.NewEntry(ai.Message{
				Role: ai.RoleUser,
				Content: []ai.Content{
					ai.Text{Text: tt.text},
					ai.File{MediaType: "image/png", Data: []byte("png")},
				},
			})
			_, err = a.Run(context.Background(), input).Wait()
			require.NoError(t, err, "the trace matches the order of the blocks")
		})
	}
}
