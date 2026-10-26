package claude_test

import (
	"context"
	"errors"
	"io"
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
		Session: "s1",
		Store:   session.NewMemoryStore(),
		Spec:    "claude-code/haiku",
		Model:   ai.Model{ID: "haiku"},
		Dir:     "/work",
		System:  "Be brief.",
		Mode:    agent.ModePlan,
		Allow:   []string{"Read"},
		Ask:     []string{"Bash(gh pr comment:*)"},
		Deny:    []string{"WebFetch"},
		Effort:  ai.ThinkingHigh,
		MCP:     []agent.MCPServer{{Name: "local", Command: "lw-mcp"}},
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
				Session: "s1",
				Skills:  []string{"deploy"},
			},
			unsupported: true,
			errText:     "claude: the config field Skills: unsupported operation",
		},
		{
			name: "tools",
			config: agent.Config{
				Session: "s1",
				Tools:   []tool.Tool{tool.Func("echo", nil)},
			},
			unsupported: true,
			errText:     "claude: the config field Tools: unsupported operation",
		},
		{
			name: "a hook at a model call",
			config: agent.Config{
				Session: "s1",
				Hooks:   []agent.Hook{stopHook{}, callHook{}},
			},
			unsupported: true,
			errText:     "claude: agent: hook 2 (claude_test.callHook) uses BeforeCall: unsupported operation",
		},
		{
			name:        "a hook of the driver at a model call",
			opts:        []claude.Option{claude.Hooks(callHook{})},
			config:      agent.Config{Session: "s1"},
			unsupported: true,
			errText:     "claude: agent: hook 1 (claude_test.callHook) uses BeforeCall: unsupported operation",
		},
		{
			name: "a hook with no point",
			config: agent.Config{
				Session: "s1",
				Hooks:   []agent.Hook{agent.BaseHook{}},
			},
			errText: "claude: agent: hook 1 (agent.BaseHook) implements no point",
		},
		{
			name: "an unknown mode",
			config: agent.Config{
				Session: "s1",
				Mode:    "auto",
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
