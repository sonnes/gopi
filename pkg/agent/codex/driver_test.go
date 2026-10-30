package codex

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/agent/internal/cliproc"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/def"
	"github.com/sonnes/gopi/pkg/session"
	"github.com/sonnes/gopi/pkg/tool"
)

// The agent is a runner that a host stops with Close.
var (
	_ agent.Runner = (*Agent)(nil)
	_ io.Closer    = (*Agent)(nil)
)

// stopHook is a hook at BeforeStop.
type stopHook struct{ agent.BaseHook }

func (stopHook) BeforeStop(context.Context, agent.State) ([]session.Entry, error) {
	return nil, nil
}

// allTools are the tools of the CLI, so a config with them needs no
// hook that removes a tool.
var allTools = []tool.Tool{
	tool.Named(tool.Bash),
	tool.Named(tool.ApplyPatch),
	tool.Named(tool.WebSearch),
	tool.Named(tool.Agent),
}

// startAgent starts the session s1 with d and c, and returns its agent.
// A config with no tools gets [allTools].
func startAgent(t *testing.T, d agent.Driver, c agent.Config) *Agent {
	t.Helper()
	c.SessionID = "s1"
	if c.Tools == nil {
		c.Tools = allTools
	}
	r, err := d.Start(context.Background(), c)
	require.NoError(t, err)
	a, ok := r.(*Agent)
	require.True(t, ok, "the driver starts a *codex.Agent")
	t.Cleanup(func() { a.Close() })
	return a
}

func TestDriverStart(t *testing.T) {
	useTraces(t, []string{"text.trace"})
	d := Driver(Sandbox(SandboxReadOnly))
	a := startAgent(t, d, agent.Config{
		Store:    session.NewMemoryStore(),
		Spec:     "codex/gpt-5-codex",
		Model:    ai.Model{ID: "gpt-5-codex"},
		Dir:      workDir,
		Sections: agent.SystemText("Be brief."),
		Effort:   ai.ThinkingHigh,
	})

	_, msgs, err := collect(a.Run(context.Background(), session.Text("Say hello.")))
	require.NoError(t, err, "the thread has the model, the dir, the system, the sandbox, and the effort")
	require.Len(t, msgs, 1)
	assert.Equal(t, "Hello", msgs[0].Text())
}

func TestDriverAsksWithAnApproveFunction(t *testing.T) {
	useTraces(t, []string{"approval_accept.trace"})
	var asked []string
	a := startAgent(t, Driver(), agent.Config{
		Dir: workDir,
		Approve: func(_ context.Context, req agent.ApprovalRequest) (agent.Decision, error) {
			asked = append(asked, req.Call.Name)
			return agent.Decision{Allow: true}, nil
		},
	})

	_, _, err := collect(a.Run(context.Background(), session.Text("Remove /tmp/x.")))
	require.NoError(t, err, "the thread has the untrusted policy")
	assert.Equal(t, []string{"bash"}, asked)
}

func TestDriverMode(t *testing.T) {
	approve := func(context.Context, agent.ApprovalRequest) (agent.Decision, error) {
		return agent.Decision{}, nil
	}
	tests := []struct {
		name        string
		mode        agent.Mode
		wantPolicy  ApprovalPolicy
		wantSandbox SandboxMode
	}{
		{
			name:        "no mode keeps the untrusted policy of an approve function",
			wantPolicy:  ApprovalUntrusted,
			wantSandbox: SandboxWorkspaceWrite,
		},
		{
			name:        "the plan mode reads and never asks",
			mode:        agent.ModePlan,
			wantPolicy:  ApprovalNever,
			wantSandbox: SandboxReadOnly,
		},
		{
			name:        "the default mode reads, and asks before a write or an unsafe command",
			mode:        agent.ModeDefault,
			wantPolicy:  ApprovalUntrusted,
			wantSandbox: SandboxReadOnly,
		},
		{
			name:        "the accept edits mode writes in the workspace, and asks for more",
			mode:        agent.ModeAcceptEdits,
			wantPolicy:  ApprovalOnRequest,
			wantSandbox: SandboxWorkspaceWrite,
		},
		{
			name:        "the bypass mode turns the sandbox off and never asks",
			mode:        agent.ModeBypass,
			wantPolicy:  ApprovalNever,
			wantSandbox: SandboxDangerFullAccess,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := Driver(Sandbox(SandboxWorkspaceWrite))
			a := startAgent(t, d, agent.Config{
				Mode:    tt.mode,
				Approve: approve,
			})

			p := a.threadParams("", nil, "")
			assert.Equal(t, tt.wantPolicy, p.ApprovalPolicy)
			assert.Equal(t, tt.wantSandbox, p.Sandbox)
		})
	}
}

func TestDriverAllowAnswersAnApproval(t *testing.T) {
	useTraces(t, []string{"approval_accept.trace"})
	var asked []string
	a := startAgent(t, Driver(), agent.Config{
		Dir:   workDir,
		Allow: []string{"bash(rm *)"},
		Approve: func(_ context.Context, req agent.ApprovalRequest) (agent.Decision, error) {
			asked = append(asked, req.Call.Name)
			return agent.Decision{}, nil
		},
	})

	_, _, err := collect(a.Run(context.Background(), session.Text("Remove /tmp/x.")))
	require.NoError(t, err, "the agent accepts the request that the Allow rule covers")
	assert.Empty(t, asked, "the approve function does not get the request")
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
	reviewer := "---\nname: reviewer\ndescription: \"Review \\\"it\\\".\"\n" +
		"model: codex/gpt-6\neffort: max\ntools: Read\nmaxTurns: 3\n---\nReview\nit.\n"
	require.NoError(t, os.WriteFile(filepath.Join(agents, "reviewer.md"), []byte(reviewer), 0o644))
	return dir
}

func TestDriverGivesTheDirsToTheCLI(t *testing.T) {
	root := t.TempDir()
	a := startAgent(t, Driver(DefsRoot(root)), agent.Config{
		Spec:   "codex/gpt-5",
		Dirs:   []string{defsDir(t)},
		Skills: []string{"deploy"},
	})
	folder := filepath.Join(root, "s1")

	require.NoError(t, a.writeDefs(context.Background()))
	p := a.threadParams("", nil, "")

	assert.Equal(t, filepath.Join(folder, "skills"), p.skillRoot)
	assert.FileExists(t, filepath.Join(folder, "skills", "deploy", "SKILL.md"))

	roleFile := filepath.Join(folder, "codex", "agents", "reviewer.toml")
	assert.Equal(t, map[string]any{
		"reviewer": map[string]any{
			"description": `Review "it".`,
			"config_file": roleFile,
		},
	}, p.Config["agents"])
	data, err := os.ReadFile(roleFile)
	require.NoError(t, err)
	want := `name = "reviewer"
description = "Review \"it\"."
developer_instructions = "Review\nit."
model = "gpt-6"
model_reasoning_effort = "xhigh"
`
	assert.Equal(t, want, string(data), "a role has no limits: Codex has no key for them")

	require.NoError(t, a.Close())
	assert.NoDirExists(t, folder, "the folder lives for the process of the CLI")
}

func TestDriverWritesAnInlineSkillWithNoDirs(t *testing.T) {
	root := t.TempDir()
	a := startAgent(t, Driver(DefsRoot(root)), agent.Config{
		Skillset: []def.SkillsReader{def.Skill{Name: "deploy", Description: "D.", Body: "Do it."}},
	})

	require.NoError(t, a.writeDefs(context.Background()))
	p := a.threadParams("", nil, "")

	assert.Equal(t, filepath.Join(root, "s1", "skills"), p.skillRoot)
	assert.FileExists(t, filepath.Join(p.skillRoot, "deploy", "SKILL.md"))
}

func TestDriverWithNoDirsGivesNoFolder(t *testing.T) {
	a := startAgent(t, Driver(), agent.Config{})

	require.NoError(t, a.writeDefs(context.Background()))
	p := a.threadParams("", nil, "")

	assert.Empty(t, p.skillRoot)
	assert.NotContains(t, p.Config, "agents")
}

func TestDriverRejects(t *testing.T) {
	tests := []struct {
		name        string
		config      agent.Config
		unsupported bool
		errText     string
	}{
		{
			name:    "a skill that no dir has",
			config:  agent.Config{Skills: []string{"deploy"}},
			errText: `codex: clidefs: the skill "deploy": no skill has this name`,
		},
		{
			name: "a tool of the caller that ToolNames names",
			config: agent.Config{
				Tools:     []tool.Tool{tool.Named(tool.Bash), tool.Func("echo", nil)},
				ToolNames: []string{"bash", "echo"},
			},
			errText: `codex: tool: unknown tool: "echo" is not a tool of codex`,
		},
		{
			name:    "a name outside the tools",
			config:  agent.Config{Tools: []tool.Tool{tool.Named(tool.Bash)}, ToolNames: []string{"apply_patch"}},
			errText: `codex: tool: unknown tool: "apply_patch" is not in the tool list`,
		},
		{
			name:    "a rule that does not parse",
			config:  agent.Config{Deny: []string{"bash(rm *"}},
			errText: `codex: permission: rule "bash(rm *": the content has no closing parenthesis`,
		},
		{
			name:        "a hook after a turn",
			config:      agent.Config{Hooks: []agent.Hook{stopHook{}, turnHook{}}},
			unsupported: true,
			errText:     "codex: agent: hook 2 (codex.turnHook) uses AfterTurn: unsupported operation",
		},
		{
			name:    "a hook with no point",
			config:  agent.Config{Hooks: []agent.Hook{agent.BaseHook{}}},
			errText: "codex: agent: hook 1 (agent.BaseHook) implements no point",
		},
		{
			name:    "an unknown mode",
			config:  agent.Config{Mode: "auto"},
			errText: `codex: agent: unknown mode "auto"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.config.SessionID = "s1"
			_, err := Driver().Start(context.Background(), tt.config)
			require.EqualError(t, err, tt.errText)
			assert.Equal(t, tt.unsupported, errors.Is(err, errors.ErrUnsupported))
		})
	}
}

func TestLeftOutTools(t *testing.T) {
	tests := []struct {
		name   string
		config agent.Config
		want   []string
	}{
		{name: "each tool of the CLI", config: agent.Config{Tools: allTools}, want: nil},
		{
			name:   "a tool of the caller is skipped",
			config: agent.Config{Tools: append([]tool.Tool{tool.Func("lookup", nil)}, allTools...)},
			want:   nil,
		},
		{
			name:   "a well-known tool that the CLI does not have",
			config: agent.Config{Tools: append([]tool.Tool{tool.Named(tool.ReadFile)}, allTools...)},
			want:   nil,
		},
		{
			name:   "a short list",
			config: agent.Config{Tools: []tool.Tool{tool.Named(tool.Bash), tool.Named(tool.ReadFile)}},
			want:   []string{tool.ApplyPatch},
		},
		{
			name:   "ToolNames keeps one tool",
			config: agent.Config{Tools: allTools, ToolNames: []string{"Bash"}},
			want:   []string{tool.ApplyPatch},
		},
		{name: "no tools", config: agent.Config{}, want: []string{tool.Bash, tool.ApplyPatch}},
		{
			name: "a named tool of another CLI",
			config: agent.Config{
				Tools:     append([]tool.Tool{tool.Named("DesignSync")}, allTools...),
				ToolNames: []string{"bash", "apply_patch", "DesignSync"},
			},
			want: nil,
		},
		{
			name:   "web_search and agent are not removed",
			config: agent.Config{Tools: []tool.Tool{tool.Named(tool.Bash), tool.Named(tool.ApplyPatch)}},
			want:   nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := leftOutTools(tt.config)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestDriverRejectsAnOptionHook(t *testing.T) {
	d := Driver(Hooks(turnHook{}))

	_, err := d.Start(context.Background(), agent.Config{
		SessionID: "s1",
		Hooks:     []agent.Hook{stopHook{}},
	})
	require.ErrorIs(t, err, errors.ErrUnsupported, "Start checks the hooks of the options")
	assert.EqualError(t, err, "codex: agent: hook 1 (codex.turnHook) uses AfterTurn: unsupported operation")
}

func TestDriver_ListModels(t *testing.T) {
	useTraces(t, []string{"list_models.trace"})
	lister, ok := Driver().(ai.ModelLister)
	require.True(t, ok)

	got, err := lister.ListModels(context.Background())

	require.NoError(t, err)
	input := []ai.Modality{ai.ModalityText, ai.ModalityImage}
	assert.Equal(t, []ai.Model{
		{
			ID:        "gpt-6.1-sol",
			Name:      "GPT-6.1-Sol",
			Input:     input,
			Reasoning: true,
			ThinkingLevels: []ai.ThinkingLevel{
				ai.ThinkingLow,
				ai.ThinkingMedium,
				ai.ThinkingHigh,
				ai.ThinkingXHigh,
				ai.ThinkingMax,
			},
		},
		{
			ID:             "gpt-6-luna",
			Name:           "GPT-6-Luna",
			Input:          input,
			Reasoning:      true,
			ThinkingLevels: []ai.ThinkingLevel{ai.ThinkingLow, ai.ThinkingMax},
		},
	}, got, "the default model comes first, and the list skips a hidden model")
}

func TestDriver_ListModelsError(t *testing.T) {
	useTraces(t, []string{"list_models_error.trace"})

	_, err := Driver().(ai.ModelLister).ListModels(context.Background())

	require.EqualError(t, err, "codex: list models: codex: model/list: not logged in")
}

// TestRecordedListModels replays a trace of the real CLI. To record it
// again:
//
//	make record-cli AGENT=codex RUN=TestRecordedListModels
func TestRecordedListModels(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	useTraces(t,
		[]string{"TestRecordedListModels.trace"},
		machineScrub,
		replaceScrub(jsonString(home), "/home/me"),
	)

	got, err := Driver().(ai.ModelLister).ListModels(context.Background())

	require.NoError(t, err)
	require.NotEmpty(t, got)
	for _, m := range got {
		assert.NotEmpty(t, m.ID)
		assert.NotEmpty(t, m.Name, m.ID)
		assert.Contains(t, m.Input, ai.ModalityText, m.ID)
	}
}

func TestDriver_ListModelsRepeatedCursor(t *testing.T) {
	useTraces(t, []string{"list_models_repeat.trace"})

	_, err := Driver().(ai.ModelLister).ListModels(context.Background())

	require.EqualError(t, err, `codex: list models: the cursor "same" repeats`)
}

func TestDriver_ListModelsCanceled(t *testing.T) {
	useTraces(t, []string{"list_models_hang.trace"})
	killed := countKills(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := Driver().(ai.ModelLister).ListModels(ctx)

	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 1, *killed, "a failed list kills the app server, and does not wait for it to exit")
}

func TestDriver_ListModelsTimeout(t *testing.T) {
	useTraces(t, []string{"list_models_hang.trace"})
	old := listTimeout
	t.Cleanup(func() { listTimeout = old })
	listTimeout = 10 * time.Millisecond

	_, err := Driver().(ai.ModelLister).ListModels(context.Background())

	require.ErrorIs(t, err, context.DeadlineExceeded, "an app server that never answers does not block the list")
}

// countKills counts the kills of the processes that the test starts.
func countKills(t *testing.T) *int {
	t.Helper()
	var n int
	replay := start
	t.Cleanup(func() { start = replay })
	start = func(cmd *exec.Cmd) (cliproc.Pipes, error) {
		p, err := replay(cmd)
		if err != nil {
			return p, err
		}
		kill := p.Kill
		p.Kill = func() error {
			n++
			return kill()
		}
		return p, nil
	}
	return &n
}

func TestDriverMCP(t *testing.T) {
	servers := []agent.MCPServer{
		{
			Name:    "lw",
			Type:    agent.MCPHTTP,
			URL:     "http://127.0.0.1:1/mcp",
			Headers: map[string]string{"Authorization": "Bearer t"},
		},
		{
			Name:    "local",
			Command: "lw-mcp",
			Args:    []string{"--quiet"},
			Env:     map[string]string{"A": "1"},
		},
	}
	wantServers := map[string]any{
		"lw": map[string]any{
			"url":          "http://127.0.0.1:1/mcp",
			"http_headers": map[string]string{"Authorization": "Bearer t"},
		},
		"local": map[string]any{
			"command": "lw-mcp",
			"args":    []string{"--quiet"},
			"env":     map[string]string{"A": "1"},
		},
	}

	t.Run("the config of the thread holds the servers", func(t *testing.T) {
		a := startAgent(t, Driver(), agent.Config{MCP: servers})

		p := a.threadParams("", nil, "")
		assert.Equal(t, wantServers, p.Config["mcp_servers"])
	})

	t.Run("the servers go next to the hooks", func(t *testing.T) {
		a := startAgent(t, Driver(Hooks(stopHook{})), agent.Config{MCP: servers})

		p := a.threadParams("", &hookServer{socket: "/tmp/hooks.sock"}, "gopi-hook")
		assert.Equal(t, wantServers, p.Config["mcp_servers"])
		assert.Contains(t, p.Config, "hooks")
	})
}

func TestToolsIsEachToolOfCodex(t *testing.T) {
	var names []string
	for _, tl := range Tools() {
		assert.True(t, tool.IsNamed(tl))
		names = append(names, tl.Info().Name)
	}

	assert.Equal(t, []string{tool.Bash, tool.ApplyPatch, tool.WebSearch, tool.Agent}, names)
}
