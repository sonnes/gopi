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
	"github.com/sonnes/gopi/pkg/harness/permission"
	"github.com/sonnes/gopi/pkg/sandbox"
	"github.com/sonnes/gopi/pkg/sandbox/read"
	"github.com/sonnes/gopi/pkg/session"
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

// start starts the session id of h with c, and returns its agent.
func start(t *testing.T, h *harness.Harness, c agent.Config) *agent.Agent {
	t.Helper()
	r, err := h.Start(context.Background(), c)
	require.NoError(t, err)
	a, ok := r.(*agent.Agent)
	require.True(t, ok, "the harness starts an *agent.Agent")
	return a
}

func TestStartResumesTheSession(t *testing.T) {
	p := &script{responses: []ai.Message{reply("one"), reply("two")}}
	h := harness.New(base(t, p)...)
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
	h := harness.New(with(base(t, p),
		harness.System("You are a test agent."),
		harness.Skillset(deploy),
	)...)

	a := start(t, h, agent.Config{
		SessionID: "s1",
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
	h := harness.New(with(base(t, p), harness.Mode(permission.Bypass))...)
	var names []string

	a := start(t, h, agent.Config{
		SessionID: "s1",
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
			h := harness.New(with(base(t, p), harness.Toolset(harness.Tools(echo, other, grepTool)))...)

			a := start(t, h, agent.Config{
				SessionID: "s1",
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
	h := harness.New(base(t, p)...)

	_, err := h.Start(context.Background(), agent.Config{
		SessionID: "s1",
		Tools:     []tool.Tool{lookup},
	})

	require.NoError(t, err)
}

// askingHook asks a person about each tool call.
type askingHook struct{ agent.BaseHook }

func (askingHook) BeforeTool(_ context.Context, _ agent.State, c ai.ToolCall) (ai.ToolCall, error) {
	return c, agent.Ask("the hook asks")
}

// TestStartAddsTheRules checks that each rule of the config changes
// what a call of echo does. Without the rule, the mode of the harness
// gives the other outcome.
func TestStartAddsTheRules(t *testing.T) {
	tests := []struct {
		name      string
		mode      permission.Mode
		config    agent.Config
		wantAsked bool
		wantRan   bool
	}{
		{
			name:      "a hook of the config that asks asks the approve function",
			mode:      permission.Bypass,
			config:    agent.Config{Hooks: []agent.Hook{askingHook{}}},
			wantAsked: true,
			wantRan:   true,
		},
		{
			name:   "Deny blocks a call that the mode runs",
			mode:   permission.Bypass,
			config: agent.Config{Deny: []string{"echo"}},
		},
		{
			name:    "the mode of the config replaces the mode of the harness",
			mode:    permission.Default,
			config:  agent.Config{Mode: agent.ModeBypass},
			wantRan: true,
		},
		{
			name:      "the default mode of the config asks for a call that the mode of the harness runs",
			mode:      permission.Bypass,
			config:    agent.Config{Mode: agent.ModeDefault},
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
			h := harness.New(with(base(t, p), harness.Mode(tt.mode))...)
			var asked []string
			c := tt.config
			c.SessionID = "s1"
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
			name: "a hook with no point",
			config: agent.Config{
				SessionID: "s1",
				Hooks:     []agent.Hook{agent.BaseHook{}},
			},
			errText: "harness: agent: hook 1 (agent.BaseHook) implements no point",
		},
		{
			name: "an unknown skill",
			config: agent.Config{
				SessionID: "s1",
				Skills:    []string{"missing"},
			},
			errText: `harness: preload the skill "missing": no skill has this name`,
		},
		{
			name: "an unknown mode",
			config: agent.Config{
				SessionID: "s1",
				Mode:      "auto",
			},
			errText: `harness: agent: unknown mode "auto"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := harness.New(base(t, &script{})...)
			_, err := h.Start(context.Background(), tt.config)
			require.EqualError(t, err, tt.errText)
		})
	}
}

func TestStartReadsTheDirs(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "skills/deploy/SKILL.md", skillFile("Deploy from the dir.", "body"))
	writeFile(t, dir, "AGENTS.md", "Use tabs.")
	p := &script{responses: []ai.Message{reply("done")}}
	h := harness.New(base(t, p)...)

	a := start(t, h, agent.Config{
		SessionID: "s1",
		Dirs:      []string{dir},
	})
	run(t, a, "hi")

	assert.Contains(t, section(a, "skills"), "Deploy from the dir.")
	assert.Contains(t, section(a, "instructions"), "Use tabs.")
}

func TestStartReadsTheSkillsetBeforeTheDirs(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "skills/deploy/SKILL.md", skillFile("Deploy from the dir.", "body"))
	p := &script{responses: []ai.Message{reply("done")}}
	h := harness.New(base(t, p)...)

	a := start(t, h, agent.Config{
		SessionID: "s1",
		Dirs:      []string{dir},
		Skillset: []def.SkillsReader{
			def.Skill{Name: "deploy", Description: "Deploy inline.", Body: "b"},
			def.Skill{Name: "test", Description: "Test inline.", Body: "b"},
		},
	})
	run(t, a, "hi")

	assert.Contains(t, section(a, "skills"), "Deploy from the dir.", "a dir wins")
	assert.NotContains(t, section(a, "skills"), "Deploy inline.")
	assert.Contains(t, section(a, "skills"), "Test inline.")
}

func TestStartSetsTheEffort(t *testing.T) {
	p := &script{responses: []ai.Message{reply("done")}}
	h := harness.New(base(t, p)...)

	a := start(t, h, agent.Config{
		SessionID: "s1",
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
	a, err := harness.New(base(t, p)...).New(context.Background())
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
	a, err := harness.New(base(t, p)...).New(
		context.Background(),
		harness.AgentDefaults(agent.Compaction(nil)),
	)
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
	a := start(t, harness.New(base(t, p)...), agent.Config{
		SessionID:  "s1",
		Compaction: c,
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
	a := start(t, harness.New(base(t, p)...), agent.Config{
		SessionID:   "s1",
		AutoCompact: 100_000,
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
		limits  agent.Limits
		wantRan bool
	}{
		{name: "no limit writes outside in bypass", tool: tool.WriteFile, wantRan: true},
		{name: "a block on writes", tool: tool.WriteFile, limits: agent.Limits{BlockWritesOutside: true}},
		{name: "a block on reads", tool: tool.ReadFile, limits: agent.Limits{BlockReadsOutside: true}},
		{name: "a block on writes reads outside", tool: tool.ReadFile, limits: agent.Limits{BlockWritesOutside: true}, wantRan: true},
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
			h := harness.New(base(t, p)...)

			a := start(t, h, agent.Config{
				SessionID: "s1",
				Dir:       t.TempDir(),
				Mode:      agent.ModeBypass,
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
	h := harness.New(base(t, &script{})...)

	start(t, h, agent.Config{
		SessionID: "s1",
		Dir:       dir,
		Tools:     []tool.Tool{harness.Sandboxed(build)},
		Limits:    agent.Limits{Fence: true},
	})

	require.NotEmpty(t, boxes)
	err := boxes[len(boxes)-1].WriteFile(context.Background(), hook, []byte("x"))
	require.Error(t, err)
}
