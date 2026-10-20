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
			return bashCall(t, "ls")
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
			mode: permission.Plan,
			want: map[string]outcome{"read": ran, "edit": denied, "exec": denied, "other": denied},
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
		{name: "task", want: ran},
		{name: "write_file", want: asked},
		{name: "edit_file", want: asked},
		{name: "bash", want: asked},
		{name: "deploy", want: asked},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &permission.Policy{Mode: permission.Default}
			c := call(t, tt.name, map[string]any{})
			assert.Equal(t, tt.want, decide(t, p, c))
		})
	}
}

func TestKindsOverride(t *testing.T) {
	p := &permission.Policy{
		Mode: permission.AcceptEdits,
		Kinds: map[string]permission.Kind{
			"lookup": permission.Read,
			"patch":  permission.Edit,
			"bash":   permission.Read,
		},
	}
	assert.Equal(t, ran, decide(t, p, call(t, "lookup", map[string]any{})))
	assert.Equal(t, ran, decide(t, p, call(t, "patch", map[string]any{})))
	assert.Equal(t, ran, decide(t, p, bashCall(t, "ls")))

	p.Mode = permission.Plan
	assert.Equal(t, denied, decide(t, p, call(t, "patch", map[string]any{})))
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
				Deny:  []string{"Bash(rm:*)"},
			},
			call: func(t *testing.T) ai.ToolCall { return bashCall(t, "rm -rf build") },
			want: denied,
		},
		{
			name: "ask beats allow",
			policy: &permission.Policy{
				Mode:  permission.Bypass,
				Allow: []string{"Bash(git:*)"},
				Ask:   []string{"Bash(git push:*)"},
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
			policy: &permission.Policy{Allow: []string{"Bash(go test:*)"}},
			call:   func(t *testing.T) ai.ToolCall { return bashCall(t, "go test ./...") },
			want:   ran,
		},
		{
			name: "allow beats plan",
			policy: &permission.Policy{
				Mode:  permission.Plan,
				Allow: []string{"Bash(go test:*)"},
			},
			call: func(t *testing.T) ai.ToolCall { return bashCall(t, "go test ./...") },
			want: ran,
		},
		{
			name: "deny in bypass",
			policy: &permission.Policy{
				Mode: permission.Bypass,
				Deny: []string{"Bash(rm:*)"},
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
				Deny: []string{"Read(.env)"},
			},
			call: func(t *testing.T) ai.ToolCall { return pathCall(t, "read_file", ".env") },
			want: denied,
		},
		{
			name: "no match falls to the mode",
			policy: &permission.Policy{
				Mode: permission.Bypass,
				Deny: []string{"Bash(rm:*)"},
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
		Deny: []string{"Read", "Bash(rm:*)"},
	}
	reason := denyReason(t, p, bashCall(t, "rm -rf /"))
	assert.Contains(t, reason, `"Bash(rm:*)"`)
}

func TestToolNames(t *testing.T) {
	tests := []struct {
		rule string
		tool string
		want bool
	}{
		{rule: "bash", tool: "bash", want: true},
		{rule: "Bash", tool: "bash", want: true},
		{rule: "Read", tool: "read_file", want: true},
		{rule: "Read", tool: "grep", want: true},
		{rule: "Read", tool: "find", want: true},
		{rule: "Read", tool: "edit_file", want: false},
		{rule: "Edit", tool: "edit_file", want: true},
		{rule: "Edit", tool: "write_file", want: true},
		{rule: "Write", tool: "write_file", want: true},
		{rule: "Write", tool: "edit_file", want: false},
		{rule: "read_file", tool: "grep", want: false},
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
	child.Mode = permission.Stricter(child.Mode, permission.Plan)

	assert.NoError(t, child.Validate())
	assert.Equal(t, ran, runOn(t, p.Hook(), bashCall(t, "ls")))
	assert.Equal(t, denied, runOn(t, child.Hook(), bashCall(t, "ls")))
}

func TestAlwaysDoesNotBeatDeny(t *testing.T) {
	a := &approver{decision: allowAlways}
	p := permission.Policy{
		Approve: a.approve,
		Deny:    []string{"Bash(rm:*)"},
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
		Deny:    []string{"Bash(rm:*)"},
	}
	h := p.Hook()

	_, out := runEdited(t, h, bashCall(t, "ls"))
	assert.Equal(t, denied, out)

	a.decision = allow
	assert.Equal(t, ran, runOn(t, h, bashCall(t, "ls")))
	assert.Len(t, a.calls, 2, "a denied edit keeps no rule")
}

func TestApproveError(t *testing.T) {
	boom := errors.New("boom")
	p := &permission.Policy{
		Approve: func(context.Context, ai.ToolCall) (agent.Decision, error) {
			return allow, boom
		},
	}
	_, err := beforeTool(t, p.Hook()).BeforeTool(context.Background(), agent.State{}, bashCall(t, "ls"))
	assert.ErrorIs(t, err, boom)
}

func TestNilApproveDenies(t *testing.T) {
	p := &permission.Policy{}
	reason := denyReason(t, p, bashCall(t, "ls"))
	assert.Contains(t, reason, "approve")

	assert.Equal(t, ran, runHook(t, p, pathCall(t, "read_file", "a.txt")))
}

func TestPlanReason(t *testing.T) {
	p := &permission.Policy{Mode: permission.Plan}
	reason := denyReason(t, p, pathCall(t, "write_file", "a.txt"))
	assert.Contains(t, reason, "plan")
}

func TestValidate(t *testing.T) {
	valid := []string{
		"bash",
		"Bash",
		"Bash()",
		"Bash(*)",
		"Bash(git push:*)",
		"Bash(npm run *)",
		`Bash(python -c "print\(1\)")`,
		"Read(src/**)",
		"Edit(//tmp/**)",
		"Write(~/notes/*.md)",
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
		"Bash(ls",
		"Bash(ls)x",
		"Bash ls",
		"de*ploy",
		"deploy(prod)",
		"mcp__github(x)",
		"mcp__github__*(x)",
		"Read([a)",
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
		Allow: []string{"Bash(ls"},
	}
	reason := denyReason(t, p, pathCall(t, "read_file", "a.txt"))
	assert.Contains(t, reason, "Bash(ls")
}
