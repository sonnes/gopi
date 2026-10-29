package claude_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/agent/claude"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/session"
	"github.com/sonnes/gopi/pkg/tool"
)

// The agent is a runner that a host stops with Close.
var (
	_ agent.Runner = (*claude.Agent)(nil)
	_ io.Closer    = (*claude.Agent)(nil)
)

// stopHook is a hook at BeforeStop.
type stopHook struct{ agent.BaseHook }

func (stopHook) BeforeStop(context.Context, agent.State) ([]session.Entry, error) {
	return nil, nil
}

func TestDriverStart(t *testing.T) {
	starts := claude.Replay(t, nil, "testdata/text.trace")
	d := claude.Driver(
		claude.Binary("/opt/claude"),
		claude.PermissionMode(claude.PermissionBypass),
	)

	r, err := d.Start(context.Background(), agent.Config{
		SessionID: "s1",
		Store:     session.NewMemoryStore(),
		Spec:      "claude-code/haiku",
		Model:     ai.Model{ID: "haiku"},
		Dir:       "/work",
		Sections:  agent.SystemText("Be brief."),
		Mode:      agent.ModePlan,
		Allow:     []string{"read_file"},
		Ask:       []string{"bash(gh pr comment:*)"},
		Deny:      []string{"web_fetch"},
		Effort:    ai.ThinkingHigh,
		MCP:       []agent.MCPServer{{Name: "local", Command: "lw-mcp"}},
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
	assert.Equal(t, "/opt/claude", cmd.Args[0], "the options of the driver come first")
	assert.Equal(t, "/work", cmd.Dir)
	args := strings.Join(cmd.Args, " ")
	for _, want := range []string{
		"--model haiku",
		"--append-system-prompt Be brief.",
		"--allowedTools Read",
		"--disallowedTools WebFetch",
		`--settings {"permissions":{"ask":["Bash(gh pr comment:*)"]}}`,
		"--permission-mode plan",
		"--effort high",
		`--mcp-config {"mcpServers":{"local":{"command":"lw-mcp"}}}`,
	} {
		assert.Contains(t, args, want)
	}
	assert.NotContains(t, args, "bypassPermissions", "the mode of the config replaces the mode of the driver")
	assert.NotContains(t, args, "--agents", "a config with no dirs gives no agents")
}

func TestDriverDirs(t *testing.T) {
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
model: sonnet
---
Count the coins.
`)
	writeAgent(t, high, "otto.md", `---
name: otto
description: Runs on another provider
model: openai/gpt-5
---
Answer.
`)

	starts := claude.Replay(t, nil, "testdata/text.trace")
	r, err := claude.Driver().Start(context.Background(), agent.Config{
		SessionID: "s1",
		Store:     session.NewMemoryStore(),
		Dirs:      []string{low, high},
	})
	require.NoError(t, err)
	a, ok := r.(*claude.Agent)
	require.True(t, ok, "the driver starts a *claude.Agent")
	defer closeAgent(t, a)

	_, _, err = run(context.Background(), a, "Say hello")
	require.NoError(t, err)

	args := starts.Cmds()[0].Args
	at := slices.Index(args, "--agents")
	require.NotEqual(t, -1, at, "the CLI gets --agents")
	require.Less(t, at+1, len(args))
	assert.JSONEq(t, `{
		"otto": {"description": "Runs on another provider", "prompt": "Answer."},
		"penny": {"description": "Counts coins", "prompt": "Count the coins.", "model": "sonnet"},
		"rex": {"description": "Reviews a diff", "prompt": "Review the diff.", "tools": ["Read", "Bash"]}
	}`, args[at+1])
	assert.NotContains(t, args, "--agent", "the host gives the prompt of the agent of the session")
}

// writeAgent writes an agent file to the agents folder of dir.
func writeAgent(t *testing.T, dir, name, text string) {
	t.Helper()
	agents := filepath.Join(dir, "agents")
	require.NoError(t, os.MkdirAll(agents, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(agents, name), []byte(text), 0o644))
}

func TestDriverRejects(t *testing.T) {
	tests := []struct {
		name string
		// opts are the options of the driver.
		opts        []claude.Option
		config      agent.Config
		unsupported bool
		errText     string
	}{
		{
			name: "skills",
			config: agent.Config{
				SessionID: "s1",
				Skills:    []string{"deploy"},
			},
			unsupported: true,
			errText:     "claude: the config field Skills: unsupported operation",
		},
		{
			name: "a tool of the caller that ToolNames names",
			config: agent.Config{
				SessionID: "s1",
				Tools:     []tool.Tool{tool.Named(tool.Bash), tool.Func("echo", nil)},
				ToolNames: []string{"bash", "echo"},
			},
			errText: `claude: tool: unknown tool: "echo" is not a tool of claude-code`,
		},
		{
			name: "a name outside the tools",
			config: agent.Config{
				SessionID: "s1",
				Tools:     []tool.Tool{tool.Named(tool.Bash)},
				ToolNames: []string{"Read"},
			},
			errText: `claude: tool: unknown tool: "Read" is not in the tool list`,
		},
		{
			name: "a hook at a model call",
			config: agent.Config{
				SessionID: "s1",
				Hooks:     []agent.Hook{stopHook{}, callHook{}},
			},
			unsupported: true,
			errText:     "claude: agent: hook 2 (claude_test.callHook) uses BeforeCall: unsupported operation",
		},
		{
			name:        "a hook of the driver at a model call",
			opts:        []claude.Option{claude.Hooks(callHook{})},
			config:      agent.Config{SessionID: "s1"},
			unsupported: true,
			errText:     "claude: agent: hook 1 (claude_test.callHook) uses BeforeCall: unsupported operation",
		},
		{
			name: "a hook with no point",
			config: agent.Config{
				SessionID: "s1",
				Hooks:     []agent.Hook{agent.BaseHook{}},
			},
			errText: "claude: agent: hook 1 (agent.BaseHook) implements no point",
		},
		{
			name: "an unknown mode",
			config: agent.Config{
				SessionID: "s1",
				Mode:      "auto",
			},
			errText: `claude: agent: unknown mode "auto"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := claude.Driver(tt.opts...).Start(context.Background(), tt.config)
			require.EqualError(t, err, tt.errText)
			assert.Equal(t, tt.unsupported, errors.Is(err, errors.ErrUnsupported))
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

func TestDriverOnlyToolsLimitsTheList(t *testing.T) {
	starts := claude.Replay(t, nil, "testdata/text.trace")
	d := claude.Driver(claude.OnlyTools("Read", "Grep"))

	r, err := d.Start(context.Background(), agent.Config{
		SessionID: "s1",
		Store:     session.NewMemoryStore(),
		Tools:     []tool.Tool{tool.Named(tool.Bash), tool.Named(tool.ReadFile)},
	})
	require.NoError(t, err)
	a, ok := r.(*claude.Agent)
	require.True(t, ok)
	defer closeAgent(t, a)
	_, _, err = run(context.Background(), a, "Say hello")
	require.NoError(t, err)

	args := strings.Join(starts.Cmds()[0].Args, " ") + " "
	assert.Contains(t, args, "--tools Read,ToolSearch ", "the option of the driver limits the list of the config")
}

func TestToolsIsEachToolOfClaudeCode(t *testing.T) {
	var names []string
	for _, tl := range claude.Tools() {
		assert.True(t, tool.IsNamed(tl))
		names = append(names, tl.Info().Name)
	}

	for _, want := range []string{tool.Bash, tool.ReadFile, tool.AskUserQuestion, tool.ExitPlanMode, tool.NotebookEdit} {
		assert.Contains(t, names, want)
	}
	assert.NotContains(t, names, tool.ApplyPatch, "Claude Code has no apply_patch")
}
