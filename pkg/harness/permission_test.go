package harness_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/harness"
	"github.com/sonnes/gopi/pkg/harness/permission"
	"github.com/sonnes/gopi/pkg/session"
	"github.com/sonnes/gopi/pkg/tool"
)

// callEcho runs an agent whose model calls echo once, with opts on top
// of the base, and returns the tool result.
func callEcho(t *testing.T, opts ...harness.Option) ai.Message {
	t.Helper()
	p := &script{responses: []ai.Message{
		callTool("c1", "echo", `{"s":"hi"}`),
		reply("done"),
	}}
	all := with(base(t, p), harness.Toolset(harness.Tools(echo)))
	a, err := harness.New(with(all, opts...)...).New(context.Background())
	require.NoError(t, err)
	run(t, a, "go")
	return toolResult(t, a)
}

func TestPermissions(t *testing.T) {
	approve := func(context.Context, agent.ApprovalRequest) (agent.Decision, error) {
		return agent.Decision{Allow: true}, nil
	}
	tests := []struct {
		name   string
		opts   []harness.Option
		denied bool
	}{
		{"default mode with no approver", nil, true},
		{"default mode with an approver", []harness.Option{harness.Approve(approve)}, false},
		{"bypass mode", []harness.Option{harness.Mode(permission.Bypass)}, false},
		{"an allow rule", []harness.Option{harness.Allow("echo")}, false},
		{"a deny rule in bypass mode", []harness.Option{harness.Mode(permission.Bypass), harness.Deny("echo")}, true},
		{"plan mode with an approver", []harness.Option{harness.Mode(permission.Plan), harness.Approve(approve)}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := callEcho(t, tt.opts...)

			assert.Equal(t, tt.denied, m.IsError, m.Text())
			if !tt.denied {
				assert.Equal(t, "hi", m.Text())
			}
		})
	}
}

func TestABadRuleFailsTheAssembly(t *testing.T) {
	h := harness.New(with(base(t, &script{}), harness.Allow("bash("))...)

	_, err := h.New(context.Background())
	assert.ErrorContains(t, err, "bash(")
}

func TestASubagentRunsInTheStricterMode(t *testing.T) {
	planner := reviewer
	planner.Mode = "plan"
	p := &script{responses: []ai.Message{
		callTool("c1", "agent", `{"agent":"reviewer","prompt":"go"}`),
		callTool("c2", "echo", `{"s":"hi"}`),
		reply("child done"),
		reply("ok"),
	}}
	store := session.NewMemoryStore()
	h := harness.New(with(base(t, p),
		harness.Store(store),
		harness.Agents(planner),
		harness.Toolset(harness.Tools(echo)),
		harness.Mode(permission.Bypass),
	)...)

	a, err := h.Durable(context.Background(), "s1")
	require.NoError(t, err)
	run(t, a, "go")

	child, err := h.Durable(context.Background(), childID(t, toolResult(t, a)))
	require.NoError(t, err)
	assert.True(t, toolResult(t, child).IsError, "plan mode denies echo in the child")
}

// readIn runs an agent whose model reads path once with a read_file
// tool, with opts on top of the base, and returns the tool result.
func readIn(t *testing.T, path string, opts ...harness.Option) ai.Message {
	t.Helper()
	input, err := json.Marshal(map[string]string{"path": path})
	require.NoError(t, err)
	p := &script{responses: []ai.Message{
		callTool("c1", tool.ReadFile, string(input)),
		reply("done"),
	}}
	readFile := tool.Func(tool.ReadFile, func(context.Context, ai.ToolCall) (ai.ToolResult, error) {
		return ai.ToolResult{}, nil
	})
	all := with(base(t, p), harness.Toolset(harness.Tools(readFile)))
	a, err := harness.New(with(all, opts...)...).New(context.Background())
	require.NoError(t, err)
	run(t, a, "go")
	return toolResult(t, a)
}

func TestTheRootsAreDirAndAddDirs(t *testing.T) {
	dir := t.TempDir()
	extra := t.TempDir()
	outside := t.TempDir()
	tests := []struct {
		name   string
		path   string
		denied bool
	}{
		{name: "Dir", path: filepath.Join(dir, "a.txt")},
		{name: "an AddDir", path: filepath.Join(extra, "a.txt")},
		{name: "outside", path: filepath.Join(outside, "a.txt"), denied: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := readIn(t, tt.path, harness.Dir(dir), harness.AddDirs(extra))

			assert.Equal(t, tt.denied, m.IsError, m.Text())
		})
	}
}
