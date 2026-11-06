package tool_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/sonnes/gopi/pkg/tool"
)

func TestCanonical(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "gopi name", in: "bash", want: "bash"},
		{name: "Claude Code name", in: "Bash", want: "bash"},
		{name: "Glob", in: "Glob", want: "find"},
		{name: "older Claude Code name", in: "Task", want: "agent"},
		{name: "MultiEdit", in: "MultiEdit", want: "edit_file"},
		{name: "MCP name", in: "mcp__claude_ai_Docs__batch", want: "mcp__claude_ai_Docs__batch"},
		{name: "Claude Code only", in: "ExitPlanMode", want: "exit_plan_mode"},
		{name: "unknown passes through", in: "Frobnicate", want: "Frobnicate"},
		{name: "caller tool passes through", in: "lookup_order", want: "lookup_order"},
		{name: "case matters", in: "BASH", want: "BASH"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tool.Canonical(tt.in))
		})
	}
}

func TestRuleNames(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{name: "gopi name", in: "bash", want: []string{"bash"}},
		{name: "Claude Code name keeps itself", in: "Bash", want: []string{"bash", "Bash"}},
		{name: "Read covers a group", in: "Read", want: []string{"read_file", "grep", "find", "Read"}},
		{name: "Edit covers a group", in: "Edit", want: []string{"edit_file", "write_file", "apply_patch", "Edit"}},
		{name: "gopi name is one tool", in: "read_file", want: []string{"read_file"}},
		{name: "Grep", in: "Grep", want: []string{"grep", "Grep"}},
		{name: "unknown passes through", in: "lookup_order", want: []string{"lookup_order"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tool.RuleNames(tt.in))
		})
	}
}

func TestListNames(t *testing.T) {
	got := tool.ListNames([]string{"Read", "bash(git diff *)", "lookup_order", "mcp__lw__threads_get"})

	want := []string{"Read", "read_file", "bash", "lookup_order", "mcp__lw__threads_get"}
	assert.Equal(t, want, got)
}

func TestLookup(t *testing.T) {
	tests := []struct {
		in      string
		name    string
		claude  string
		kind    tool.Kind
		content tool.Content
	}{
		{in: "Bash", name: "bash", claude: "Bash", kind: tool.KindExec, content: tool.ContentCommand},
		{in: "read_file", name: "read_file", claude: "Read", kind: tool.KindRead, content: tool.ContentPath},
		{in: "Glob", name: "find", claude: "Glob", kind: tool.KindRead, content: tool.ContentPath},
		{in: "apply_patch", name: "apply_patch", claude: "", kind: tool.KindEdit, content: tool.ContentPath},
		{in: "Task", name: "agent", claude: "Agent", kind: tool.KindRead, content: tool.ContentNone},
		{in: "web_fetch", name: "web_fetch", claude: "WebFetch", kind: tool.KindOther, content: tool.ContentDomain},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			k, ok := tool.Lookup(tt.in)

			assert.True(t, ok)
			assert.Equal(t, tt.name, k.Name)
			assert.Equal(t, tt.claude, k.ClaudeName())
			assert.Equal(t, tt.kind, k.Kind)
			assert.Equal(t, tt.content, k.Content)
		})
	}

	_, ok := tool.Lookup("lookup_order")
	assert.False(t, ok)
}

func TestSplitRule(t *testing.T) {
	name, content := tool.SplitRule("bash(git push *)")

	assert.Equal(t, "bash", name)
	assert.Equal(t, "(git push *)", content)
}

func TestMCPName(t *testing.T) {
	assert.Equal(t, "mcp__linear__get_issue", tool.MCPName("linear", "get_issue"))
}
