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
	var asked []string
	approve := func(_ context.Context, req agent.ApprovalRequest) (agent.Decision, error) {
		asked = append(asked, req.Call.Name)
		return agent.Decision{}, nil
	}
	hooks, err := ruleHooks(agent.Config{
		Dir:       workDir,
		Tools:     allTools,
		Deny:      []string{"bash(git push *)", "apply_patch(secrets/**)"},
		Ask:       []string{"mcp__docs__write"},
		ToolNames: []string{"bash", "apply_patch", "mcp__docs__write"},
		Approve:   approve,
	})
	require.NoError(t, err)
	srv, err := startHookServer(hooks, noState)
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
			name:  "a deny rule blocks a command",
			input: `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"git push origin main"}}`,
			want:  deny(`"the rule \"bash(git push *)\" denies the tool call \"bash\""`),
		},
		{
			name:  "a deny rule blocks a patch of a path",
			input: `{"hook_event_name":"PreToolUse","tool_name":"apply_patch","tool_input":{"command":"*** Begin Patch\n*** Update File: secrets/a.txt\n*** End Patch\n"}}`,
			want:  deny(`"the rule \"apply_patch(secrets/**)\" denies the tool call \"apply_patch\""`),
		},
		{
			name:  "another command runs",
			input: `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"git status"}}`,
			want:  `{}`,
		},
		{
			name:  "an ask rule asks the approve function",
			input: `{"hook_event_name":"PreToolUse","tool_name":"mcp__docs__write","tool_input":{}}`,
			want:  deny(`"the approver denied the tool call \"mcp__docs__write\""`),
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
	assert.Equal(t, []string{"mcp__docs__write"}, asked)
}

func TestRuleHooksWithNoRules(t *testing.T) {
	hooks, err := ruleHooks(agent.Config{Tools: allTools, Allow: []string{"bash"}})

	require.NoError(t, err)
	assert.Empty(t, hooks, "an Allow rule needs no hook, so the CLI needs no hook command")
}
