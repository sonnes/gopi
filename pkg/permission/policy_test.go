package permission_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/permission"
)

func TestModeByKind(t *testing.T) {
	calls := map[string]func(t *testing.T) ai.ToolCall{
		"read": func(t *testing.T) ai.ToolCall {
			return pathCall(t, "read_file", "main.go")
		},
		"edit": func(t *testing.T) ai.ToolCall {
			return pathCall(t, "edit_file", "main.go")
		},
		"exec": func(t *testing.T) ai.ToolCall {
			return bashCall(t, "make build")
		},
		"other": func(t *testing.T) ai.ToolCall {
			return call(t, "mcp__github__create_issue", map[string]any{})
		},
	}
	tests := []struct {
		mode permission.Mode
		want map[string]outcome
	}{
		{
			mode: permission.Default,
			want: map[string]outcome{"read": ran, "edit": asked, "exec": asked, "other": asked},
		},
		{
			mode: "",
			want: map[string]outcome{"read": ran, "edit": asked, "exec": asked, "other": asked},
		},
		{
			mode: permission.AcceptEdits,
			want: map[string]outcome{"read": ran, "edit": ran, "exec": asked, "other": asked},
		},
		{
			mode: permission.Bypass,
			want: map[string]outcome{"read": ran, "edit": ran, "exec": ran, "other": ran},
		},
	}
	for _, tt := range tests {
		for kind, want := range tt.want {
			t.Run(string(tt.mode)+"/"+kind, func(t *testing.T) {
				p := &permission.Policy{Mode: tt.mode}
				assert.Equal(t, want, decide(t, p, calls[kind](t)))
			})
		}
	}
}

func TestBuiltinKinds(t *testing.T) {
	tests := []struct {
		name string
		want outcome
	}{
		{name: "read_file", want: ran},
		{name: "grep", want: ran},
		{name: "find", want: ran},
		{name: "todo_write", want: ran},
		{name: "ask_user_question", want: ran},
		{name: "skill", want: ran},
		{name: "tool_search", want: ran},
		{name: "agent", want: ran},
		{name: "write_file", want: asked},
		{name: "edit_file", want: asked},
		{name: "bash", want: asked},
		{name: "apply_patch", want: asked},
		{name: "deploy", want: asked},
		// An alias is the name of a tool of the caller, and not a
		// built-in tool.
		{name: "Grep", want: asked},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &permission.Policy{Mode: permission.Default}
			c := call(t, tt.name, map[string]any{})
			assert.Equal(t, tt.want, decide(t, p, c))
		})
	}
}

// TestARuleOnAClaudeCodeNameMatchesTheLiteralName checks that a rule
// on "Grep" also matches a tool of the caller with that name.
func TestARuleOnAClaudeCodeNameMatchesTheLiteralName(t *testing.T) {
	p := &permission.Policy{Mode: permission.Default, Rules: permission.Rules{Allow: []string{"Grep"}}}

	assert.Equal(t, ran, decide(t, p, call(t, "Grep", map[string]any{})))
}

// TestClaudeCodeRules checks that a rule with a Claude Code name covers
// the gopi tools of its group, as in Claude Code.
func TestClaudeCodeRules(t *testing.T) {
	tests := []struct {
		name  string
		rule  string
		call  ai.ToolCall
		match bool
	}{
		{
			name:  "Bash with content",
			rule:  "Bash(git push *)",
			call:  call(t, "bash", map[string]any{"command": "git push origin"}),
			match: true,
		},
		{
			name:  "Read covers grep",
			rule:  "Read(src/**)",
			call:  call(t, "grep", map[string]any{"pattern": "x", "path": "src/a.go"}),
			match: true,
		},
		{
			name:  "Read does not cover bash",
			rule:  "Read",
			call:  call(t, "bash", map[string]any{"command": "ls"}),
			match: false,
		},
		{
			name:  "Edit covers apply_patch",
			rule:  "Edit",
			call:  call(t, "apply_patch", map[string]any{}),
			match: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &permission.Policy{Mode: permission.Default, Dir: t.TempDir(), Rules: permission.Rules{Deny: []string{tt.rule}}}

			got := decide(t, p, tt.call)

			assert.Equal(t, tt.match, got == denied, "outcome %s", got)
		})
	}
}

func TestRuleOrder(t *testing.T) {
	tests := []struct {
		name   string
		policy *permission.Policy
		call   func(t *testing.T) ai.ToolCall
		want   outcome
	}{
		{
			name: "deny beats allow",
			policy: &permission.Policy{
				Rules: permission.Rules{Allow: []string{"bash"}, Deny: []string{"bash(rm:*)"}},
			},
			call: func(t *testing.T) ai.ToolCall { return bashCall(t, "rm -rf build") },
			want: denied,
		},
		{
			name: "ask beats allow",
			policy: &permission.Policy{
				Mode:  permission.Bypass,
				Rules: permission.Rules{Allow: []string{"bash(git:*)"}, Ask: []string{"bash(git push:*)"}},
			},
			call: func(t *testing.T) ai.ToolCall { return bashCall(t, "git push origin") },
			want: asked,
		},
		{
			name: "deny beats ask",
			policy: &permission.Policy{
				Rules: permission.Rules{Ask: []string{"bash"}, Deny: []string{"bash"}},
			},
			call: func(t *testing.T) ai.ToolCall { return bashCall(t, "ls") },
			want: denied,
		},
		{
			name:   "allow beats the mode",
			policy: &permission.Policy{Rules: permission.Rules{Allow: []string{"bash(go test:*)"}}},
			call:   func(t *testing.T) ai.ToolCall { return bashCall(t, "go test ./...") },
			want:   ran,
		},
		{
			name: "deny in bypass",
			policy: &permission.Policy{
				Mode:  permission.Bypass,
				Rules: permission.Rules{Deny: []string{"bash(rm:*)"}},
			},
			call: func(t *testing.T) ai.ToolCall { return bashCall(t, "rm -rf /") },
			want: denied,
		},
		{
			name: "ask in bypass",
			policy: &permission.Policy{
				Mode:  permission.Bypass,
				Rules: permission.Rules{Ask: []string{"write_file"}},
			},
			call: func(t *testing.T) ai.ToolCall { return pathCall(t, "write_file", "a.txt") },
			want: asked,
		},
		{
			name: "deny a read tool",
			policy: &permission.Policy{
				Rules: permission.Rules{Deny: []string{"read_file(.env)"}},
			},
			call: func(t *testing.T) ai.ToolCall { return pathCall(t, "read_file", ".env") },
			want: denied,
		},
		{
			name: "no match falls to the mode",
			policy: &permission.Policy{
				Mode:  permission.Bypass,
				Rules: permission.Rules{Deny: []string{"bash(rm:*)"}},
			},
			call: func(t *testing.T) ai.ToolCall { return bashCall(t, "ls") },
			want: ran,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, decide(t, tt.policy, tt.call(t)))
		})
	}
}

func TestDenyReasonNamesTheRule(t *testing.T) {
	p := &permission.Policy{
		Mode:  permission.Bypass,
		Rules: permission.Rules{Deny: []string{"read_file", "bash(rm:*)"}},
	}
	reason := denyReason(t, p, bashCall(t, "rm -rf /"))
	assert.Contains(t, reason, `"bash(rm:*)"`)
}

func TestToolNames(t *testing.T) {
	tests := []struct {
		rule string
		tool string
		want bool
	}{
		{rule: "bash", tool: "bash", want: true},
		{rule: "read_file", tool: "read_file", want: true},
		{rule: "read_file", tool: "grep", want: false},
		{rule: "write_file", tool: "edit_file", want: false},
		{rule: "deploy", tool: "deploy", want: true},
		{rule: "deploy", tool: "deploy_all", want: false},
		{rule: "mcp__github__*", tool: "mcp__github__create_issue", want: true},
		{rule: "mcp__github__*", tool: "mcp__gitlab__create_issue", want: false},
		{rule: "mcp__github", tool: "mcp__github__create_issue", want: true},
		{rule: "mcp__github", tool: "mcp__githubx__create_issue", want: false},
		{rule: "mcp__github__create_issue", tool: "mcp__github__create_issue", want: true},
		{rule: "mcp__github__create_issue", tool: "mcp__github__list_issues", want: false},
		{rule: "deploy_*", tool: "deploy_prod", want: true},
		{rule: "*", tool: "anything", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.rule+"/"+tt.tool, func(t *testing.T) {
			p := &permission.Policy{
				Mode:  permission.Bypass,
				Rules: permission.Rules{Deny: []string{tt.rule}},
			}
			c := call(t, tt.tool, map[string]any{"path": "a.txt", "command": "ls"})
			got := decide(t, p, c) == denied
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestPolicyIsAValue(t *testing.T) {
	p := permission.Policy{Mode: permission.Bypass}
	child := p
	child.Mode = permission.Stricter(child.Mode, permission.Default)

	assert.NoError(t, child.Validate())
	assert.Equal(t, ran, decide(t, &p, bashCall(t, "make build")))
	assert.Equal(t, asked, decide(t, &child, bashCall(t, "make build")))
}

func TestValidate(t *testing.T) {
	valid := []string{
		"bash",
		"bash()",
		"bash(*)",
		"bash(git push:*)",
		"bash(npm run *)",
		`bash(python -c "print\(1\)")`,
		"read_file(src/**)",
		"edit_file(//tmp/**)",
		"write_file(~/notes/*.md)",
		"grep(/docs)",
		"find(./web)",
		"read_file(*.env)",
		"mcp__github",
		"mcp__github__*",
		"deploy",
		"deploy()",
	}
	for _, rule := range valid {
		t.Run(rule, func(t *testing.T) {
			p := &permission.Policy{Rules: permission.Rules{Allow: []string{rule}}}
			assert.NoError(t, p.Validate())
		})
	}

	invalid := []string{
		"",
		"(ls)",
		"bash(ls",
		"bash(ls)x",
		"bash ls",
		"de*ploy",
		"deploy(prod)",
		"mcp__github(x)",
		"mcp__github__*(x)",
		"read_file([a)",
		"todo_write(x)",
	}
	for _, rule := range invalid {
		t.Run("invalid "+rule, func(t *testing.T) {
			p := &permission.Policy{Rules: permission.Rules{Deny: []string{"bash", rule}}}
			err := p.Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), `"`+rule+`"`)
		})
	}
}

func TestValidateMode(t *testing.T) {
	p := &permission.Policy{Mode: "auto"}
	err := p.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "auto")
}

func TestAnInvalidPolicyDeniesEachCall(t *testing.T) {
	p := &permission.Policy{
		Mode:  permission.Bypass,
		Rules: permission.Rules{Allow: []string{"bash(ls"}},
	}
	reason := denyReason(t, p, pathCall(t, "read_file", "a.txt"))
	assert.Contains(t, reason, "bash(ls")
}

func TestProtectedEditAsksForASafetyCheck(t *testing.T) {
	p := permission.Policy{
		Mode:  permission.AcceptEdits,
		Dir:   t.TempDir(),
		Rules: permission.Rules{Allow: []string{"Edit"}},
	}

	edit := p.Decide(pathCall(t, "edit_file", ".git/hooks/pre-commit"))
	command := p.Decide(bashCall(t, "make build"))

	assert.Equal(t, permission.Decision{Match: permission.AskMatch, Safety: true}, edit)
	assert.Equal(t, permission.Decision{Match: permission.AskMatch}, command, "a command that the mode asks for is not a safety check")
}
