package harness_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/def"
	"github.com/sonnes/gopi/pkg/harness"
	"github.com/sonnes/gopi/pkg/permission"
	"github.com/sonnes/gopi/pkg/sandbox"
	"github.com/sonnes/gopi/pkg/sandbox/read"
	"github.com/sonnes/gopi/pkg/session"
	storefs "github.com/sonnes/gopi/pkg/store/fs"
	"github.com/sonnes/gopi/pkg/tool"
)

var _ agent.Driver = (*harness.Harness)(nil)

// toolLog is a hook at BeforeTool that records the name of each call.
type toolLog struct {
	agent.BaseHook
	names *[]string
}

func (h toolLog) BeforeTool(_ context.Context, _ agent.State, c ai.ToolCall) (ai.ToolCall, error) {
	*h.names = append(*h.names, c.Name)
	return c, nil
}

func TestStartResumesTheSession(t *testing.T) {
	p := &script{responses: []ai.Message{reply("one"), reply("two")}}
	h := harness.New(base(p)...)
	store := session.NewMemoryStore()
	c := agent.Config{
		SessionID: "s1",
		Store:     store,
		Spec:      "fake/two",
	}

	run(t, start(t, h, c), "first")
	a := start(t, h, c)
	run(t, a, "second")

	assert.Equal(t, []string{"two", "two"}, p.called(), "the spec of the config sets the model")
	assert.Len(t, a.Log().Messages(), 4, "the second start resumes the session")
}

func TestStartAddsTheSystemAndTheSkills(t *testing.T) {
	p := &script{responses: []ai.Message{reply("done")}}
	h := harness.New(with(base(p), harness.System("You are a test agent."))...)

	a := start(t, h, agent.Config{
		SessionID: "s1",
		Spec:      "fake/one",
		Defs:      def.Set{Skills: []def.Skill{deploy}},
		Sections: func(context.Context) ([]ai.Section, error) {
			return []ai.Section{{Name: "review", Text: "Review the diff."}}, nil
		},
		Skills: []string{"deploy"},
	})
	run(t, a, "hi")

	want := "You are a test agent.\n\n## Skill: deploy\n\nRun scripts/run.sh."
	assert.Equal(t, want, section(a, harness.SystemSection))
	assert.Equal(t, "Review the diff.", section(a, "review"), "the sections of the config come after")
}

func TestStartAddsTheHooksAndTheTools(t *testing.T) {
	p := &script{responses: []ai.Message{
		callTool("c1", "echo", `{"s":"x"}`),
		reply("done"),
	}}
	h := harness.New(base(p)...)
	var names []string

	a := start(t, h, agent.Config{
		SessionID: "s1",
		Spec:      "fake/one",
		Mode:      permission.Bypass,
		Hooks:     []agent.Hook{toolLog{names: &names}},
		Tools:     []tool.Tool{echo},
	})
	run(t, a, "hi")

	assert.Equal(t, []string{"echo"}, names, "the hook of the config runs")
	assert.Equal(t, "x", toolResult(t, a).Text(), "the tool of the config runs")
}

func TestStartKeepsOnlyTheToolNames(t *testing.T) {
	grepTool := tool.New("grep", func(_ context.Context, in echoInput) (string, error) {
		return in.S, nil
	}, tool.Description("A tool named grep."))
	other := tool.New("other", func(_ context.Context, in echoInput) (string, error) {
		return in.S, nil
	}, tool.Description("Another tool."))
	tests := []struct {
		name  string
		names []string
		want  []string
	}{
		{name: "nil keeps each tool", names: nil, want: []string{"echo", "other", "grep"}},
		{name: "a name with content", names: []string{"echo(hi *)"}, want: []string{"echo"}},
		{name: "a Claude Code name", names: []string{"Grep"}, want: []string{"grep"}},
		{name: "empty keeps no tool", names: []string{}, want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &script{responses: []ai.Message{reply("done")}}
			h := harness.New(with(base(p), harness.Toolset(harness.Tools(echo, other, grepTool)))...)

			a := start(t, h, agent.Config{
				SessionID: "s1",
				Spec:      "fake/one",
				ToolNames: tt.names,
			})
			run(t, a, "hi")

			assert.Equal(t, tt.want, toolNames(a))
		})
	}
}

func TestStartTakesAToolWithAnyName(t *testing.T) {
	lookup := tool.New("LookupOrder", func(_ context.Context, in echoInput) (string, error) {
		return in.S, nil
	})
	p := &script{responses: []ai.Message{reply("done")}}
	h := harness.New(base(p)...)

	_, err := h.Start(context.Background(), agent.Config{
		SessionID: "s1",
		Spec:      "fake/one",
		Tools:     []tool.Tool{lookup},
	})

	require.NoError(t, err)
}

// askingHook asks a person about each tool call.
type askingHook struct{ agent.BaseHook }

func (askingHook) BeforeTool(_ context.Context, _ agent.State, c ai.ToolCall) (ai.ToolCall, error) {
	return c, agent.Ask("the hook asks")
}

// TestStartAddsTheRules checks that each rule and each mode of the
// config changes what a call of echo does.
func TestStartAddsTheRules(t *testing.T) {
	tests := []struct {
		name      string
		config    agent.Config
		wantAsked bool
		wantRan   bool
	}{
		{
			name: "a hook of the config that asks asks the approve function",
			config: agent.Config{
				Mode:  permission.Bypass,
				Hooks: []agent.Hook{askingHook{}},
			},
			wantAsked: true,
			wantRan:   true,
		},
		{
			name: "Deny blocks a call that the mode runs",
			config: agent.Config{
				Mode: permission.Bypass,
				Deny: []string{"echo"},
			},
		},
		{
			name:    "the bypass mode runs the call",
			config:  agent.Config{Mode: permission.Bypass},
			wantRan: true,
		},
		{
			name:      "the default mode asks for the call",
			config:    agent.Config{Mode: permission.Default},
			wantAsked: true,
			wantRan:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &script{responses: []ai.Message{
				callTool("c1", "echo", `{"s":"x"}`),
				reply("done"),
			}}
			h := harness.New(base(p)...)
			var asked []string
			c := tt.config
			c.SessionID = "s1"
			c.Spec = "fake/one"
			c.Tools = []tool.Tool{echo}
			c.Approve = func(_ context.Context, req agent.ApprovalRequest) (agent.Decision, error) {
				asked = append(asked, req.Call.Name)
				return agent.Decision{Allow: true}, nil
			}

			a := start(t, h, c)
			run(t, a, "hi")

			assert.Equal(t, tt.wantAsked, len(asked) > 0, "the approve function ran")
			assert.Equal(t, tt.wantRan, !toolResult(t, a).IsError, "the tool ran")
		})
	}
}

func TestStartFails(t *testing.T) {
	tests := []struct {
		name    string
		config  agent.Config
		errText string
	}{
		{
			name: "an unknown skill",
			config: agent.Config{
				SessionID: "s1",
				Spec:      "fake/one",
				Skills:    []string{"missing"},
			},
			errText: `harness: preload the skill "missing": no skill has this name`,
		},
		{
			name: "an unknown mode",
			config: agent.Config{
				SessionID: "s1",
				Spec:      "fake/one",
				Mode:      "auto",
			},
			errText: `harness: permission: unknown mode "auto"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := harness.New(base(&script{})...)
			_, err := h.Start(context.Background(), tt.config)
			require.EqualError(t, err, tt.errText)
		})
	}
}

func TestStartLoadsTheDefsOfTheConfig(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "skills/deploy/SKILL.md", skillFile("Deploy from the dir.", "body"))
	writeFile(t, dir, "AGENTS.md", "Use tabs.")
	p := &script{responses: []ai.Message{reply("done")}}
	h := harness.New(with(base(p), skillTool)...)

	a := start(t, h, agent.Config{
		SessionID: "s1",
		Spec:      "fake/one",
		Defs:      storefs.Dir(dir),
	})
	run(t, a, "hi")

	assert.Contains(t, section(a, "skills"), "Deploy from the dir.")
	assert.Contains(t, section(a, "instructions"), "Use tabs.")
}

func TestStartLoadsTheDefsOfTheConfigAfterTheDefsOfTheHarness(t *testing.T) {
	p := &script{responses: []ai.Message{reply("done")}}
	h := harness.New(with(base(p), skillTool, harness.Defs(def.Set{Skills: []def.Skill{
		{Name: "deploy", Description: "Deploy from the harness.", Body: "b"},
		{Name: "test", Description: "Test from the harness.", Body: "b"},
	}}))...)

	a := start(t, h, agent.Config{
		SessionID: "s1",
		Spec:      "fake/one",
		Defs: def.Set{Skills: []def.Skill{
			{Name: "deploy", Description: "Deploy from the config.", Body: "b"},
		}},
	})
	run(t, a, "hi")

	assert.Contains(t, section(a, "skills"), "Deploy from the config.", "the config wins")
	assert.NotContains(t, section(a, "skills"), "Deploy from the harness.")
	assert.Contains(t, section(a, "skills"), "Test from the harness.")
}

func TestStartSetsTheEffort(t *testing.T) {
	p := &script{responses: []ai.Message{reply("done")}}
	h := harness.New(base(p)...)

	a := start(t, h, agent.Config{
		SessionID: "s1",
		Spec:      "fake/one",
		Effort:    ai.ThinkingHigh,
	})
	run(t, a, "hi")

	p.mu.Lock()
	defer p.mu.Unlock()
	level, ok := ai.GetOption(p.opts[0], ai.Thinking)
	assert.True(t, ok)
	assert.Equal(t, ai.ThinkingHigh, level)
}

// bigReply returns an answer of about 25k tokens that reports tokens of
// context.
func bigReply(tokens int) ai.Message {
	m := reply(strings.Repeat("abcd", 25_000))
	m.Usage = ai.Usage{Input: tokens}
	return m
}

func TestDefaultCompactionSummarizes(t *testing.T) {
	p := &script{responses: []ai.Message{
		bigReply(300_000),
		bigReply(400_000),
		reply("the summary"),
		reply("done"),
	}}
	a, err := harness.New(base(p)...).New(context.Background(), config(t))
	require.NoError(t, err)

	run(t, a, "first")
	run(t, a, "second")
	run(t, a, "third")

	assert.Less(t,
		ai.EstimateTokens(a.State().View()...),
		ai.EstimateTokens(a.State().Messages()...),
	)
	var texts []string
	for _, m := range a.State().View() {
		texts = append(texts, m.Text())
	}
	assert.Contains(t, strings.Join(texts, "\n"), "the summary")
}

func TestCompactionNilTurnsItOff(t *testing.T) {
	p := &script{responses: []ai.Message{
		bigReply(300_000),
		bigReply(400_000),
		reply("done"),
	}}
	h := harness.New(with(base(p), harness.AgentDefaults(agent.Compaction(nil)))...)
	a, err := h.New(context.Background(), config(t))
	require.NoError(t, err)

	run(t, a, "first")
	run(t, a, "second")
	run(t, a, "third")

	assert.Len(t, a.State().View(), len(a.State().Messages()))
}

// countingCompactor counts its compactions and makes no edits.
type countingCompactor struct{ count int }

func (c *countingCompactor) Limit(ai.Model) int { return 1 }

func (c *countingCompactor) Compact(
	context.Context,
	agent.CompactInput,
) (agent.CompactResult, error) {
	c.count++
	return agent.CompactResult{}, nil
}

func TestStartUsesTheCompactionOfTheConfig(t *testing.T) {
	p := &script{responses: []ai.Message{reply("done")}}
	c := &countingCompactor{}
	a := start(t, harness.New(base(p)...), agent.Config{
		SessionID:  "s1",
		Compaction: c,
		Spec:       "fake/one",
	})

	run(t, a, strings.Repeat("abcd", 100))

	assert.Equal(t, 1, c.count)
}

func TestStartGivesTheAutoCompactSizeToTheDefaultStrategy(t *testing.T) {
	p := &script{responses: []ai.Message{
		bigReply(150_000),
		bigReply(170_000),
		reply("the summary"),
		reply("done"),
	}}
	a := start(t, harness.New(base(p)...), agent.Config{
		SessionID:   "s1",
		AutoCompact: 100_000,
		Spec:        "fake/one",
	})

	run(t, a, "first")
	run(t, a, "second")
	run(t, a, "third")

	assert.Less(t,
		ai.EstimateTokens(a.State().View()...),
		ai.EstimateTokens(a.State().Messages()...),
		"the default limit of 200k does not start this compaction",
	)
}

func TestStartAppliesTheLimits(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "a.txt")
	tests := []struct {
		name    string
		tool    string
		limits  permission.Limits
		wantRan bool
	}{
		{name: "no limit writes outside in bypass", tool: tool.WriteFile, wantRan: true},
		{name: "a block on writes", tool: tool.WriteFile, limits: permission.Limits{BlockWritesOutside: true}},
		{name: "a block on reads", tool: tool.ReadFile, limits: permission.Limits{BlockReadsOutside: true}},
		{name: "a block on writes reads outside", tool: tool.ReadFile, limits: permission.Limits{BlockWritesOutside: true}, wantRan: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input, err := json.Marshal(map[string]string{"path": outside})
			require.NoError(t, err)
			p := &script{responses: []ai.Message{
				callTool("c1", tt.tool, string(input)),
				reply("done"),
			}}
			fileTool := tool.Func(tt.tool, func(context.Context, ai.ToolCall) (ai.ToolResult, error) {
				return ai.ToolResult{}, nil
			})
			h := harness.New(base(p)...)

			a := start(t, h, agent.Config{
				SessionID: "s1",
				Spec:      "fake/one",
				Dir:       t.TempDir(),
				Mode:      permission.Bypass,
				Tools:     []tool.Tool{fileTool},
				Limits:    tt.limits,
			})
			run(t, a, "hi")

			assert.Equal(t, tt.wantRan, !toolResult(t, a).IsError, toolResult(t, a).Text())
		})
	}
}

func TestStartFencesTheSandbox(t *testing.T) {
	needFence(t)
	dir, hook := gitHook(t)
	var boxes []sandbox.Sandbox
	build := func(box sandbox.Sandbox) tool.Tool {
		boxes = append(boxes, box)
		return read.New(box)
	}
	h := harness.New(base(&script{})...)

	start(t, h, agent.Config{
		SessionID: "s1",
		Spec:      "fake/one",
		Dir:       dir,
		Tools:     []tool.Tool{harness.Sandboxed(build)},
		Limits:    permission.Limits{Fence: true},
	})

	require.NotEmpty(t, boxes)
	err := boxes[len(boxes)-1].WriteFile(context.Background(), hook, []byte("x"))
	require.Error(t, err)
}
