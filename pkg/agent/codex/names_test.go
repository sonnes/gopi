package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
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

func TestImageGeneration(t *testing.T) {
	tests := []struct {
		name      string
		item      string
		wantInput string
		wantText  string
		wantError bool
	}{
		{
			name:      "the result is the saved path",
			item:      `{"type":"imageGeneration","id":"ig1","status":"completed","revisedPrompt":"a red fox","result":"iVBORw0KGgo=","savedPath":"/home/me/.codex/generated_images/s1/ig1.png","transparentBackground":null,"failure":null}`,
			wantInput: `{"prompt":"a red fox","path":"/home/me/.codex/generated_images/s1/ig1.png"}`,
			wantText:  "/home/me/.codex/generated_images/s1/ig1.png",
		},
		{
			name:      "a failure is an error",
			item:      `{"type":"imageGeneration","id":"ig1","status":"failed","revisedPrompt":null,"result":"","savedPath":null,"failure":{"type":"usageLimitExceeded","limitId":"images","resetsAt":null}}`,
			wantInput: `{"prompt":null,"path":null}`,
			wantText:  "usageLimitExceeded: images",
			wantError: true,
		},
		{
			name:      "a failure with no limit has no separator",
			item:      `{"type":"imageGeneration","id":"ig1","status":"failed","revisedPrompt":null,"result":"","savedPath":null,"failure":{"type":"contentPolicy","limitId":null,"resetsAt":null}}`,
			wantInput: `{"prompt":null,"path":null}`,
			wantText:  "contentPolicy",
			wantError: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var it item
			require.NoError(t, json.Unmarshal([]byte(tt.item), &it))

			call, ok := toolCall(it)
			require.True(t, ok)
			assert.Equal(t, "ig1", call.ID)
			assert.Equal(t, "image_generation", call.Name)
			assert.JSONEq(t, tt.wantInput, string(call.Input), "the input never holds the image data")

			result := toolResult(it)
			assert.Equal(t, []ai.Content{ai.Text{Text: tt.wantText}}, result.Content)
			assert.Equal(t, tt.wantError, result.IsError)
		})
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
	srv, err := startHookServer(hooks, noState, nil, newStops())
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
