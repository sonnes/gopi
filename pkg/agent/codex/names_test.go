package codex

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
)

func TestMCPCallKeepsThePrefix(t *testing.T) {
	call, ok := toolCall(item{
		Type:   itemMcpToolCall,
		Server: "docs",
		Tool:   "search",
	})

	require.True(t, ok)
	assert.Equal(t, "mcp__docs__search", call.Name)
}

func TestHookToolNames(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "Bash", want: "bash"},
		{in: "apply_patch", want: "apply_patch"},
		{in: "mcp__docs__search", want: "mcp__docs__search"},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, toolName(tt.in))
	}
}

func TestRuleHooks(t *testing.T) {
	hooks, err := ruleHooks(agent.Config{
		Dir:       workDir,
		Tools:     allTools,
		Deny:      []string{"mcp__docs__drop", "bash(git push *)"},
		ToolNames: []string{"bash", "apply_patch", "mcp__docs__write", "mcp__docs__drop"},
	})
	require.NoError(t, err)
	srv, err := startHookServer(hooks, noState, nil)
	require.NoError(t, err)
	defer srv.close()

	deny := func(reason string) string {
		return `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":` + reason + `}}`
	}
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "a deny rule with no content blocks the tool",
			input: `{"hook_event_name":"PreToolUse","tool_name":"mcp__docs__drop","tool_input":{}}`,
			want:  deny(`"the rule \"mcp__docs__drop\" denies the tool call \"mcp__docs__drop\""`),
		},
		{
			name:  "the driver does not check a deny rule with content",
			input: `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"git push origin main"}}`,
			want:  `{}`,
		},
		{
			name:  "a tool that ToolNames does not name is blocked",
			input: `{"hook_event_name":"PreToolUse","tool_name":"mcp__docs__delete","tool_input":{}}`,
			want:  deny(`"the tool \"mcp__docs__delete\" is not one of the tools of this agent"`),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			err := HookShim(context.Background(), srv.socket, strings.NewReader(tt.input), &out)
			require.NoError(t, err)
			assert.JSONEq(t, tt.want, out.String())
		})
	}
}

func TestRuleHooksWithNoRules(t *testing.T) {
	hooks, err := ruleHooks(agent.Config{Tools: allTools, Deny: []string{"bash(rm *)"}})

	require.NoError(t, err)
	assert.Empty(t, hooks, "a rule with content needs no hook in the driver, so the CLI needs no hook command")
}
