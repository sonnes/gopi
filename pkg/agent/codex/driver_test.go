package codex

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/agent/internal/cliproc"
	"github.com/sonnes/gopi/pkg/ai"
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

// startAgent starts the session s1 with d and c, and returns its agent.
func startAgent(t *testing.T, d agent.Driver, c agent.Config) *Agent {
	t.Helper()
	c.Session = "s1"
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
		Store:  session.NewMemoryStore(),
		Spec:   "codex/gpt-5-codex",
		Model:  ai.Model{ID: "gpt-5-codex"},
		Dir:    workDir,
		System: "Be brief.",
		Effort: ai.ThinkingHigh,
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

func TestDriverRejects(t *testing.T) {
	tests := []struct {
		name        string
		config      agent.Config
		unsupported bool
		errText     string
	}{
		{
			name:        "skills",
			config:      agent.Config{Skills: []string{"deploy"}},
			unsupported: true,
			errText:     "codex: the config field Skills: unsupported operation",
		},
		{
			name:        "tools",
			config:      agent.Config{Tools: []tool.Tool{tool.Func("echo", nil)}},
			unsupported: true,
			errText:     "codex: the config field Tools: unsupported operation",
		},
		{
			name:        "allow rules",
			config:      agent.Config{Allow: []string{"Read"}},
			unsupported: true,
			errText:     "codex: the config field Allow: unsupported operation",
		},
		{
			name:        "ask rules",
			config:      agent.Config{Ask: []string{"Bash(git push:*)"}},
			unsupported: true,
			errText:     "codex: the config field Ask: unsupported operation",
		},
		{
			name:        "deny rules",
			config:      agent.Config{Deny: []string{"Bash(rm:*)"}},
			unsupported: true,
			errText:     "codex: the config field Deny: unsupported operation",
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
			tt.config.Session = "s1"
			_, err := Driver().Start(context.Background(), tt.config)
			require.EqualError(t, err, tt.errText)
			assert.Equal(t, tt.unsupported, errors.Is(err, errors.ErrUnsupported))
		})
	}
}

func TestDriverRejectsAnOptionHook(t *testing.T) {
	d := Driver(Hooks(turnHook{}))

	_, err := d.Start(context.Background(), agent.Config{
		Session: "s1",
		Hooks:   []agent.Hook{stopHook{}},
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
