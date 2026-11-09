package codex

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/agent/internal/cliproc"
	"github.com/sonnes/gopi/pkg/agent/internal/drivertest"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/def"
	"github.com/sonnes/gopi/pkg/permission"
	"github.com/sonnes/gopi/pkg/session"
	storefs "github.com/sonnes/gopi/pkg/store/fs"
	"github.com/sonnes/gopi/pkg/tool"
)

var _ agent.Runner = (*Agent)(nil)

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
	d := Driver(Binary("/opt/codex"))
	a := startAgent(t, d, agent.Config{
		Store:    session.NewMemoryStore(),
		Spec:     "codex/gpt-5-codex",
		Model:    ai.Model{ID: "gpt-5-codex"},
		Dir:      workDir,
		Sections: agent.SystemText("Be brief."),
		Mode:     permission.Default,
		Effort:   ai.ThinkingHigh,
	})

	_, msgs, err := collect(a.Run(context.Background(), session.Text("Say hello.")))
	require.NoError(t, err, "the thread has the model, the dir, the system, the mode, and the effort")
	assert.Equal(t, "/opt/codex", a.command().Path, "the driver gives its options")
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
		mode        permission.Mode
		wantPolicy  approvalPolicy
		wantSandbox sandboxMode
	}{
		{
			name:       "no mode keeps the sandbox of the CLI, and asks with an approve function",
			wantPolicy: approvalUntrusted,
		},
		{
			name:        "the default mode reads, and asks before a write or an unsafe command",
			mode:        permission.Default,
			wantPolicy:  approvalUntrusted,
			wantSandbox: sandboxReadOnly,
		},
		{
			name:        "the accept edits mode writes in the workspace, and asks for more",
			mode:        permission.AcceptEdits,
			wantPolicy:  approvalOnRequest,
			wantSandbox: sandboxWorkspaceWrite,
		},
		{
			name:        "the bypass mode turns the sandbox off and never asks",
			mode:        permission.Bypass,
			wantPolicy:  approvalNever,
			wantSandbox: sandboxDangerFullAccess,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := startAgent(t, Driver(), agent.Config{
				Mode:    tt.mode,
				Approve: approve,
			})

			p := a.threadParams("", nil, "", nil)
			assert.Equal(t, tt.wantPolicy, p.ApprovalPolicy)
			assert.Equal(t, tt.wantSandbox, p.Sandbox)
		})
	}
}

func TestDriverLimits(t *testing.T) {
	tests := []struct {
		name        string
		mode        permission.Mode
		limits      permission.Limits
		wantSandbox sandboxMode
	}{
		{
			name:        "the bypass mode with no limit turns the sandbox off",
			mode:        permission.Bypass,
			wantSandbox: sandboxDangerFullAccess,
		},
		{
			name:        "the bypass mode with a block on writes writes in the workspace",
			mode:        permission.Bypass,
			limits:      permission.Limits{BlockWritesOutside: true},
			wantSandbox: sandboxWorkspaceWrite,
		},
		{
			name:        "the bypass mode with the fence writes in the workspace",
			mode:        permission.Bypass,
			limits:      permission.Limits{Fence: true},
			wantSandbox: sandboxWorkspaceWrite,
		},
		{
			name: "no mode with no limit keeps the sandbox of the CLI",
		},
		{
			name:        "no mode with a block on writes writes in the workspace",
			limits:      permission.Limits{BlockWritesOutside: true},
			wantSandbox: sandboxWorkspaceWrite,
		},
		{
			name:        "no mode with the fence writes in the workspace",
			limits:      permission.Limits{Fence: true},
			wantSandbox: sandboxWorkspaceWrite,
		},
		{
			name:        "the default mode keeps its sandbox",
			mode:        permission.Default,
			limits:      permission.Limits{BlockWritesOutside: true, Fence: true},
			wantSandbox: sandboxReadOnly,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := startAgent(t, Driver(), agent.Config{
				Mode:   tt.mode,
				Limits: tt.limits,
			})

			p := a.threadParams("", nil, "", nil)
			assert.Equal(t, tt.wantSandbox, p.Sandbox)
		})
	}
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

// defsRoot gives the test its own temporary directory, and returns the
// folder that holds the folder of each session.
func defsRoot(t *testing.T) string {
	t.Helper()
	t.Setenv("TMPDIR", t.TempDir())
	return filepath.Join(os.TempDir(), "gopi-defs")
}

func TestDriverWritesOnlyTheSkillKeysOfCodex(t *testing.T) {
	defsRoot(t)
	skill := def.Skill{
		Name:         "deploy",
		Description:  "D.",
		Body:         "Do it.",
		Model:        "gpt-6",
		AllowedTools: []string{"bash"},
	}
	a := startAgent(t, Driver(), agent.Config{
		Defs: def.Set{Skills: []def.Skill{skill}},
	})

	_, skillRoot, _, err := a.writeDefs(context.Background())
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(skillRoot, "deploy", "SKILL.md"))
	require.NoError(t, err)
	assert.NotContains(t, string(data), "model")
	assert.NotContains(t, string(data), "allowed-tools")
}

func TestDriverStartErrorOfTheSkillRoots(t *testing.T) {
	const (
		initialize = "read line\nprintf '{\"id\":1,\"result\":{}}\\n'\nread line\nread line\n"
		unknown    = "unknown variant `skills/extraRoots/set`"
	)
	reply := func(code int, message string) string {
		return initialize + fmt.Sprintf("printf '{\"id\":2,\"error\":{\"code\":%d,\"message\":\"%s\"}}\\n'\nsleep 5\n", code, message)
	}
	tests := []struct {
		name        string
		body        string
		unsupported bool
		text        string
	}{
		{
			name:        "Codex 0.163 has no method",
			body:        reply(errInvalidRequest, "Invalid request: "+unknown),
			unsupported: true,
			text:        "the config field Defs",
		},
		{
			name:        "the method-not-found code",
			body:        reply(errMethodNotFound, "no method"),
			unsupported: true,
			text:        "the config field Defs",
		},
		{
			name: "an invalid request for another reason",
			body: reply(errInvalidRequest, "Invalid request: a bad root"),
			text: "codex: set the skill roots",
		},
		{
			name: "the CLI stops",
			body: initialize + "exit 1\n",
			text: "codex: set the skill roots",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defsRoot(t)
			a := startAgent(t, Driver(Binary(fakeCLI(t, tt.body))), agent.Config{
				Defs: def.Set{Skills: []def.Skill{{Name: "deploy", Description: "D.", Body: "Do it."}}},
			})

			_, _, err := collect(a.Run(context.Background(), session.Text("Say hello.")))

			assert.ErrorContains(t, err, tt.text)
			assert.Equal(t, tt.unsupported, errors.Is(err, errors.ErrUnsupported))
		})
	}
}

func TestDriverGivesTheAddDirsAsWritableRoots(t *testing.T) {
	a := startAgent(t, Driver(), agent.Config{
		Spec:    "codex/gpt-5",
		AddDirs: []string{"/lw/repos"},
	})

	args := a.command().Args
	p := a.threadParams("", nil, "", nil)

	assert.Contains(t, args, `sandbox_workspace_write.writable_roots=["/lw/repos"]`)
	assert.NotContains(t, p.Config, "sandbox_workspace_write", "the config of the thread would replace the table of -c")
}

func TestAppServerPinsThePermissionKeys(t *testing.T) {
	tests := []struct {
		name   string
		config agent.Config
		want   []string
	}{
		{
			name: "no sandbox",
			want: []string{
				"/bin/codex", "app-server",
				"-c", `approvals_reviewer="user"`,
				"-c", `sandbox_workspace_write.writable_roots=[]`,
				"-c", "sandbox_workspace_write.network_access=false",
				"-c", "sandbox_workspace_write.exclude_tmpdir_env_var=false",
				"-c", "sandbox_workspace_write.exclude_slash_tmp=false",
			},
		},
		{
			name: "a sandbox and a writable root",
			config: agent.Config{
				Mode:    permission.AcceptEdits,
				AddDirs: []string{"/lw/repos", `/lw/a "b"`},
			},
			want: []string{
				"/bin/codex", "app-server",
				"-c", `approvals_reviewer="user"`,
				"-c", `sandbox_mode="workspace-write"`,
				"-c", `sandbox_workspace_write.writable_roots=["/lw/repos","/lw/a \"b\""]`,
				"-c", "sandbox_workspace_write.network_access=false",
				"-c", "sandbox_workspace_write.exclude_tmpdir_env_var=false",
				"-c", "sandbox_workspace_write.exclude_slash_tmp=false",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := newConfig(tt.config, []Option{Binary("/bin/codex")})
			require.NoError(t, err)

			cmd := appServer(c)

			assert.Equal(t, tt.want, cmd.Args)
		})
	}
}

func TestDriverConformance(t *testing.T) {
	drivertest.Run(t, drivertest.Suite{
		Driver:          Driver(),
		Tools:           allTools,
		UnsupportedHook: turnHook{},
		SkillDir:        skillsDir,
		WriteDefs: func(t *testing.T, r agent.Runner) {
			a, ok := r.(*Agent)
			require.True(t, ok, "the driver starts a *codex.Agent")
			_, _, _, err := a.writeDefs(context.Background())
			require.NoError(t, err)
		},
	})
}

func TestDriverGivesTheRolesToTheCLI(t *testing.T) {
	root := defsRoot(t)
	a := startAgent(t, Driver(), agent.Config{
		Spec: "codex/gpt-5",
		Defs: storefs.Dir(defsDir(t)),
	})

	_, _, roles, err := a.writeDefs(context.Background())
	require.NoError(t, err)
	p := a.threadParams("", nil, "", roles)

	roleFile := filepath.Join(root, "s1", "codex", "agents", "reviewer.toml")
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
model_reasoning_effort = "max"
`
	assert.Equal(t, want, string(data), "a role has no limits: Codex has no key for them")
}

func TestDriverFailsOnARoleNameThatLeavesTheFolder(t *testing.T) {
	defsRoot(t)
	a := startAgent(t, Driver(), agent.Config{
		Defs: def.Set{Agents: []def.Agent{{Name: "../evil", Description: "E."}}},
	})

	_, _, _, err := a.writeDefs(context.Background())

	assert.ErrorContains(t, err, "../evil")
}

func TestDriverWithNoDefsGivesNoRoles(t *testing.T) {
	a := startAgent(t, Driver(), agent.Config{})

	_, _, roles, err := a.writeDefs(context.Background())
	require.NoError(t, err)
	p := a.threadParams("", nil, "", roles)

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
			name:    "a rule that does not parse",
			config:  agent.Config{Deny: []string{"bash(rm *"}},
			errText: `codex: permission: rule "bash(rm *": the content has no closing parenthesis`,
		},
		{
			name:        "a block on reads outside the roots",
			config:      agent.Config{Limits: permission.Limits{BlockReadsOutside: true}},
			unsupported: true,
			errText:     "codex: the limit BlockReadsOutside: Codex has no control for reads outside the roots: unsupported operation",
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

		p := a.threadParams("", nil, "", nil)
		assert.Equal(t, wantServers, p.Config["mcp_servers"])
	})

	t.Run("the servers go next to the hooks", func(t *testing.T) {
		a := startAgent(t, Driver(), agent.Config{
			MCP:   servers,
			Hooks: []agent.Hook{stopHook{}},
		})

		p := a.threadParams("", &hookServer{socket: "/tmp/hooks.sock"}, "gopi-hook", nil)
		assert.Equal(t, wantServers, p.Config["mcp_servers"])
		assert.Contains(t, p.Config, "hooks")
	})
}
