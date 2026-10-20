package codex

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
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
	d := Driver(
		Sandbox(SandboxReadOnly),
		Thinking(ai.ThinkingHigh),
	)
	a := startAgent(t, d, agent.Config{
		Store:  session.NewMemoryStore(),
		Spec:   "codex/gpt-5-codex",
		Model:  ai.Model{ID: "gpt-5-codex"},
		Dir:    workDir,
		System: "Be brief.",
	})

	_, msgs, err := collect(a.Run(context.Background(), session.Text("Say hello.")))
	require.NoError(t, err, "the thread has the model, the dir, the system, and the sandbox")
	require.Len(t, msgs, 1)
	assert.Equal(t, "Hello", msgs[0].Text())
}

func TestDriverAsksWithAnApproveFunction(t *testing.T) {
	useTraces(t, []string{"approval_accept.trace"})
	var asked []string
	a := startAgent(t, Driver(), agent.Config{
		Dir: workDir,
		Approve: func(_ context.Context, c ai.ToolCall) (agent.Decision, error) {
			asked = append(asked, c.Name)
			return agent.Decision{Allow: true}, nil
		},
	})

	_, _, err := collect(a.Run(context.Background(), session.Text("Remove /tmp/x.")))
	require.NoError(t, err, "the thread has the untrusted policy")
	assert.Equal(t, []string{"bash"}, asked)
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
