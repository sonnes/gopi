package harness_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/def"
	"github.com/sonnes/gopi/pkg/harness"
	"github.com/sonnes/gopi/pkg/permission"
	"github.com/sonnes/gopi/pkg/sandbox"
	"github.com/sonnes/gopi/pkg/session"
	"github.com/sonnes/gopi/pkg/tool"
)

// callEcho runs an agent of c whose model calls echo once, with opts
// on top of the base, and returns the tool result.
func callEcho(t *testing.T, c agent.Config, opts ...harness.Option) ai.Message {
	t.Helper()
	p := &script{responses: []ai.Message{
		callTool("c1", "echo", `{"s":"hi"}`),
		reply("done"),
	}}
	all := with(base(p), harness.Toolset(harness.Tools(echo)))
	c.Spec = "fake/one"
	a, err := harness.New(with(all, opts...)...).New(context.Background(), c)
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
		config agent.Config
		opts   []harness.Option
		denied bool
	}{
		{
			name:   "default mode with no approver",
			denied: true,
		},
		{
			name:   "default mode with an approver",
			config: agent.Config{Approve: approve},
		},
		{
			name:   "bypass mode",
			config: agent.Config{Mode: permission.Bypass},
		},
		{
			name: "an allow rule",
			opts: []harness.Option{harness.Allow("echo")},
		},
		{
			name: "a deny rule in bypass mode",
			config: agent.Config{
				Mode: permission.Bypass,
				Deny: []string{"echo"},
			},
			denied: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := callEcho(t, tt.config, tt.opts...)

			assert.Equal(t, tt.denied, m.IsError, m.Text())
			if !tt.denied {
				assert.Equal(t, "hi", m.Text())
			}
		})
	}
}

func TestABadRuleFailsTheAssembly(t *testing.T) {
	h := harness.New(with(base(&script{}), harness.Allow("bash("))...)

	_, err := h.New(context.Background(), config(t))
	assert.ErrorContains(t, err, "bash(")
}

func TestTheModeOfASubagent(t *testing.T) {
	tests := []struct {
		name   string
		mode   string
		denied bool
	}{
		{
			name:   "a stricter mode in the file",
			mode:   "default",
			denied: true,
		},
		{
			name: "no mode in the file gives the mode of the parent",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sub := reviewer
			sub.Mode = tt.mode
			p := &script{responses: []ai.Message{
				callTool("c1", "agent", `{"agent":"reviewer","prompt":"go"}`),
				callTool("c2", "echo", `{"s":"hi"}`),
				reply("child done"),
				reply("ok"),
			}}
			h := harness.New(with(base(p),
				agentTool,
				harness.Defs(def.Set{Agents: []def.Agent{sub}}),
				harness.Toolset(harness.Tools(echo)),
			)...)
			c := config(t)
			c.Store = session.NewMemoryStore()
			c.Mode = permission.Bypass

			a := start(t, h, c)
			run(t, a, "go")

			child := start(t, h, childConfig(t, c, toolResult(t, a)))
			m := toolResult(t, child)
			assert.Equal(t, tt.denied, m.IsError, m.Text())
		})
	}
}

// readIn runs an agent of c whose model reads path once with a
// read_file tool, and returns the tool result.
func readIn(t *testing.T, path string, c agent.Config, opts ...harness.Option) ai.Message {
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
	all := with(base(p), harness.Toolset(harness.Tools(readFile)))
	h := harness.New(with(all, opts...)...)
	c.Spec = "fake/one"
	a, err := h.New(context.Background(), c)
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
			m := readIn(t, tt.path, agent.Config{
				Dir:     dir,
				AddDirs: []string{extra},
			})

			assert.Equal(t, tt.denied, m.IsError, m.Text())
		})
	}
}

func TestTheDirOfTheSandboxIsARoot(t *testing.T) {
	dir := t.TempDir()
	boxDir := t.TempDir()
	box, err := sandbox.Local(boxDir)
	require.NoError(t, err)
	c := agent.Config{Dir: dir}

	m := readIn(t, filepath.Join(boxDir, "a.txt"), c, harness.Sandbox(box))

	assert.False(t, m.IsError, m.Text())
}

func TestARelativePathIsInTheDirOfTheSandbox(t *testing.T) {
	box, err := sandbox.Local(t.TempDir())
	require.NoError(t, err)
	c := agent.Config{Dir: t.TempDir()}

	m := readIn(t, "a.txt", c, harness.Sandbox(box))

	assert.False(t, m.IsError, m.Text())
}

func TestTheApproverAnswersEachAsk(t *testing.T) {
	tests := []struct {
		name     string
		deny     []string
		decision agent.Decision
		err      error
		want     string
		isError  bool
	}{
		{
			name:     "an allow runs the call",
			decision: agent.Decision{Allow: true},
			want:     "hi",
		},
		{
			name:    "the zero decision denies with the name of the tool",
			want:    `the approver denied the tool call "echo"`,
			isError: true,
		},
		{
			name:     "a message is the reason of the deny",
			decision: agent.Decision{Message: "Run make check first."},
			want:     "Run make check first.",
			isError:  true,
		},
		{
			name: "an edited input runs",
			decision: agent.Decision{
				Allow: true,
				Input: json.RawMessage(`{"s":"bye"}`),
			},
			want: "bye",
		},
		{
			name: "a Deny rule checks the edited input",
			deny: []string{"echo"},
			decision: agent.Decision{
				Allow: true,
				Input: json.RawMessage(`{"s":"bye"}`),
			},
			isError: true,
		},
		{
			name:    "an error of the approver fails the call",
			err:     errors.New("boom"),
			want:    "boom",
			isError: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			approve := func(context.Context, agent.ApprovalRequest) (agent.Decision, error) {
				return tt.decision, tt.err
			}
			m := callEcho(t, agent.Config{
				Approve: approve,
				Deny:    tt.deny,
			})

			assert.Equal(t, tt.isError, m.IsError, m.Text())
			assert.Contains(t, m.Text(), tt.want)
		})
	}
}

func TestAProtectedEditAsksForASafetyCheck(t *testing.T) {
	dir := t.TempDir()
	p := &script{responses: []ai.Message{
		callTool("c1", tool.EditFile, `{"path":".git/hooks/pre-commit"}`),
		reply("done"),
	}}
	editFile := tool.Func(tool.EditFile, func(context.Context, ai.ToolCall) (ai.ToolResult, error) {
		return ai.ToolResult{}, nil
	})
	var asked []agent.ApprovalRequest
	approve := func(_ context.Context, req agent.ApprovalRequest) (agent.Decision, error) {
		asked = append(asked, req)
		return agent.Decision{Allow: true}, nil
	}
	h := harness.New(with(base(p), harness.Toolset(harness.Tools(editFile)))...)
	a, err := h.New(context.Background(), agent.Config{
		Spec:    "fake/one",
		Dir:     dir,
		Mode:    permission.AcceptEdits,
		Approve: approve,
	})
	require.NoError(t, err)

	run(t, a, "go")

	require.Len(t, asked, 1)
	assert.True(t, asked[0].Safety)
}
