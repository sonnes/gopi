package permission_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/harness/permission"
	"github.com/sonnes/gopi/pkg/tool"
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
	p := &permission.Policy{Mode: permission.Default, Allow: []string{"Grep"}}

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
			p := &permission.Policy{Mode: permission.Default, Dir: t.TempDir(), Deny: []string{tt.rule}}

			got := decide(t, p, tt.call)

			assert.Equal(t, tt.match, got == denied, "outcome %s", got)
		})
	}
}

func TestKindsOverride(t *testing.T) {
	p := &permission.Policy{
		Mode: permission.AcceptEdits,
		Kinds: map[string]tool.Kind{
			"lookup": tool.KindRead,
			"patch":  tool.KindEdit,
			"bash":   tool.KindRead,
		},
	}
	assert.Equal(t, ran, decide(t, p, call(t, "lookup", map[string]any{})))
	assert.Equal(t, ran, decide(t, p, call(t, "patch", map[string]any{})))
	assert.Equal(t, ran, decide(t, p, bashCall(t, "ls")))

	p.Mode = permission.Default
	assert.Equal(t, asked, decide(t, p, call(t, "patch", map[string]any{})))
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
				Allow: []string{"bash"},
				Deny:  []string{"bash(rm:*)"},
			},
			call: func(t *testing.T) ai.ToolCall { return bashCall(t, "rm -rf build") },
			want: denied,
		},
		{
			name: "ask beats allow",
			policy: &permission.Policy{
				Mode:  permission.Bypass,
				Allow: []string{"bash(git:*)"},
				Ask:   []string{"bash(git push:*)"},
			},
			call: func(t *testing.T) ai.ToolCall { return bashCall(t, "git push origin") },
			want: asked,
		},
		{
			name: "deny beats ask",
			policy: &permission.Policy{
				Ask:  []string{"bash"},
				Deny: []string{"bash"},
			},
			call: func(t *testing.T) ai.ToolCall { return bashCall(t, "ls") },
			want: denied,
		},
		{
			name:   "allow beats the mode",
			policy: &permission.Policy{Allow: []string{"bash(go test:*)"}},
			call:   func(t *testing.T) ai.ToolCall { return bashCall(t, "go test ./...") },
			want:   ran,
		},
		{
			name: "deny in bypass",
			policy: &permission.Policy{
				Mode: permission.Bypass,
				Deny: []string{"bash(rm:*)"},
			},
			call: func(t *testing.T) ai.ToolCall { return bashCall(t, "rm -rf /") },
			want: denied,
		},
		{
			name: "ask in bypass",
			policy: &permission.Policy{
				Mode: permission.Bypass,
				Ask:  []string{"write_file"},
			},
			call: func(t *testing.T) ai.ToolCall { return pathCall(t, "write_file", "a.txt") },
			want: asked,
		},
		{
			name: "deny a read tool",
			policy: &permission.Policy{
				Deny: []string{"read_file(.env)"},
			},
			call: func(t *testing.T) ai.ToolCall { return pathCall(t, "read_file", ".env") },
			want: denied,
		},
		{
			name: "no match falls to the mode",
			policy: &permission.Policy{
				Mode: permission.Bypass,
				Deny: []string{"bash(rm:*)"},
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
		Mode: permission.Bypass,
		Deny: []string{"read_file", "bash(rm:*)"},
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
				Mode: permission.Bypass,
				Deny: []string{tt.rule},
			}
			c := call(t, tt.tool, map[string]any{"path": "a.txt", "command": "ls"})
			got := runHook(t, p, c) == denied
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestAlways(t *testing.T) {
	a := &approver{decision: allowAlways}
	p := permission.Policy{Approve: a.approve}
	h := p.Hook()

	assert.Equal(t, ran, runOn(t, h, bashCall(t, "make test")))
	assert.Equal(t, ran, runOn(t, h, bashCall(t, "make test")))
	assert.Len(t, a.calls, 1, "the second call uses the kept rule")

	assert.Equal(t, ran, runOn(t, h, bashCall(t, "make build")))
	assert.Len(t, a.calls, 2, "the kept rule is the exact command")

	assert.Equal(t, ran, runOn(t, h, call(t, "deploy", map[string]any{"env": "a"})))
	assert.Equal(t, ran, runOn(t, h, call(t, "deploy", map[string]any{"env": "b"})))
	assert.Len(t, a.calls, 3, "the kept rule is the tool name")

	assert.Equal(t, ran, runOn(t, p.Hook(), bashCall(t, "make test")))
	assert.Len(t, a.calls, 4, "a new hook starts with no kept rules")
}

func TestPolicyIsAValue(t *testing.T) {
	p := permission.Policy{Mode: permission.Bypass}
	child := p
	child.Mode = permission.Stricter(child.Mode, permission.Default)

	assert.NoError(t, child.Validate())
	assert.Equal(t, ran, runOn(t, p.Hook(), bashCall(t, "make build")))
	assert.Equal(t, denied, runOn(t, child.Hook(), bashCall(t, "make build")))
}

func TestAlwaysDoesNotBeatDeny(t *testing.T) {
	a := &approver{decision: allowAlways}
	p := permission.Policy{
		Approve: a.approve,
		Deny:    []string{"bash(rm:*)"},
	}
	h := p.Hook()
	assert.Equal(t, ran, runOn(t, h, call(t, "deploy", map[string]any{})))
	assert.Equal(t, denied, runOn(t, h, bashCall(t, "rm -rf /")))
}

func TestAllow(t *testing.T) {
	a := &approver{decision: allow}
	p := &permission.Policy{Approve: a.approve}

	assert.Equal(t, ran, runHook(t, p, bashCall(t, "make test")))
	assert.Equal(t, ran, runHook(t, p, bashCall(t, "make test")))
	assert.Len(t, a.calls, 2, "Allow keeps no rule")
}

func TestApproveDenies(t *testing.T) {
	tests := []struct {
		name     string
		decision agent.Decision
		want     string
	}{
		{
			name:     "the zero decision denies with the name of the tool",
			decision: agent.Decision{},
			want:     `the approver denied the tool call "bash"`,
		},
		{
			name:     "a message is the reason",
			decision: agent.Decision{Message: "Run make check first."},
			want:     "Run make check first.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &approver{decision: tt.decision}
			p := &permission.Policy{Approve: a.approve}

			reason := denyReason(t, p, bashCall(t, "make test"))
			assert.Equal(t, tt.want, reason)
			assert.Len(t, a.calls, 1)
		})
	}
}

func TestApproveChangesInput(t *testing.T) {
	a := &approver{decision: agent.Decision{
		Allow: true,
		Input: json.RawMessage(`{"command":"make test -v"}`),
	}}
	p := &permission.Policy{Approve: a.approve}

	c := bashCall(t, "make test")
	got, err := beforeTool(t, p.Hook()).BeforeTool(context.Background(), agent.State{}, c)
	require.NoError(t, err)
	assert.Equal(t, c.ID, got.ID)
	assert.Equal(t, c.Name, got.Name)
	assert.JSONEq(t, `{"command":"make test -v"}`, string(got.Input))
}

func TestAlwaysKeepsTheEditedInput(t *testing.T) {
	a := &approver{decision: agent.Decision{
		Allow:  true,
		Always: true,
		Input:  json.RawMessage(`{"command":"rm -rf build/tmp"}`),
	}}
	p := permission.Policy{Approve: a.approve}
	h := p.Hook()

	got, out := runEdited(t, h, bashCall(t, "rm -rf build"))
	assert.Equal(t, ran, out)
	assert.JSONEq(t, `{"command":"rm -rf build/tmp"}`, string(got.Input))

	a.decision = allow
	assert.Equal(t, ran, runOn(t, h, bashCall(t, "rm -rf build/tmp")))
	assert.Len(t, a.calls, 1, "the kept rule is the edited command")

	assert.Equal(t, ran, runOn(t, h, bashCall(t, "rm -rf build")))
	assert.Len(t, a.calls, 2, "the command before the edit asks again")
}

func TestDenyChecksTheEditedInput(t *testing.T) {
	a := &approver{decision: agent.Decision{
		Allow:  true,
		Always: true,
		Input:  json.RawMessage(`{"command":"rm -rf /"}`),
	}}
	p := permission.Policy{
		Approve: a.approve,
		Deny:    []string{"bash(rm:*)"},
	}
	h := p.Hook()

	_, out := runEdited(t, h, bashCall(t, "make build"))
	assert.Equal(t, denied, out)

	a.decision = allow
	assert.Equal(t, ran, runOn(t, h, bashCall(t, "make build")))
	assert.Len(t, a.calls, 2, "a denied edit keeps no rule")
}

func TestApproveError(t *testing.T) {
	boom := errors.New("boom")
	p := &permission.Policy{
		Approve: func(context.Context, agent.ApprovalRequest) (agent.Decision, error) {
			return allow, boom
		},
	}
	_, err := beforeTool(t, p.Hook()).BeforeTool(context.Background(), agent.State{}, bashCall(t, "make build"))
	assert.ErrorIs(t, err, boom)
}

func TestNilApproveDenies(t *testing.T) {
	p := &permission.Policy{}
	reason := denyReason(t, p, bashCall(t, "make build"))
	assert.Contains(t, reason, "approve")

	assert.Equal(t, ran, runHook(t, p, pathCall(t, "read_file", "a.txt")))
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
			p := &permission.Policy{Allow: []string{rule}}
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
			p := &permission.Policy{Deny: []string{"bash", rule}}
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

func TestHookOfInvalidPolicyDenies(t *testing.T) {
	p := &permission.Policy{
		Mode:  permission.Bypass,
		Allow: []string{"bash(ls"},
	}
	reason := denyReason(t, p, pathCall(t, "read_file", "a.txt"))
	assert.Contains(t, reason, "bash(ls")
}

func TestProtectedEditAsksForASafetyCheck(t *testing.T) {
	var got []agent.ApprovalRequest
	p := permission.Policy{
		Mode:  permission.AcceptEdits,
		Dir:   t.TempDir(),
		Allow: []string{"Edit"},
		Approve: func(_ context.Context, req agent.ApprovalRequest) (agent.Decision, error) {
			got = append(got, req)
			return allow, nil
		},
	}

	assert.Equal(t, ran, runOn(t, p.Hook(), pathCall(t, "edit_file", ".git/hooks/pre-commit")))
	assert.Equal(t, ran, runOn(t, p.Hook(), bashCall(t, "make build")))

	require.Len(t, got, 2)
	assert.True(t, got[0].Safety, "an edit of a protected path asks for a safety check")
	assert.False(t, got[1].Safety, "a command that the mode asks for is not a safety check")
}
