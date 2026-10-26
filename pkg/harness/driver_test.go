package harness_test

import (
	"context"
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
		Session: "s1",
		Store:   store,
		Spec:    "fake/two",
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
		Session: "s1",
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
		Session: "s1",
		Hooks:   []agent.Hook{toolLog{names: &names}},
		Tools:   []tool.Tool{echo},
	})
	run(t, a, "hi")

	assert.Equal(t, []string{"echo"}, names, "the hook of the config runs")
	assert.Equal(t, "x", toolResult(t, a).Text(), "the tool of the config runs")
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
			name:    "Allow runs a call that the mode asks for",
			mode:    permission.Default,
			config:  agent.Config{Allow: []string{"echo"}},
			wantRan: true,
		},
		{
			name:      "Ask asks for a call that the mode runs",
			mode:      permission.Bypass,
			config:    agent.Config{Ask: []string{"echo"}},
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
			name:   "the plan mode blocks a call that the harness asks for",
			mode:   permission.Default,
			config: agent.Config{Mode: agent.ModePlan},
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
			c.Session = "s1"
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
				Session: "s1",
				Hooks:   []agent.Hook{agent.BaseHook{}},
			},
			errText: "harness: agent: hook 1 (agent.BaseHook) implements no point",
		},
		{
			name: "an unknown skill",
			config: agent.Config{
				Session: "s1",
				Skills:  []string{"missing"},
			},
			errText: `harness: preload the skill "missing": no skill has this name`,
		},
		{
			name: "an unknown mode",
			config: agent.Config{
				Session: "s1",
				Mode:    "auto",
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
		Session: "s1",
		Dirs:    []string{dir},
	})
	run(t, a, "hi")

	assert.Contains(t, section(a, "skills"), "Deploy from the dir.")
	assert.Contains(t, section(a, "instructions"), "Use tabs.")
}

func TestStartSetsTheEffort(t *testing.T) {
	p := &script{responses: []ai.Message{reply("done")}}
	h := harness.New(base(t, p)...)

	a := start(t, h, agent.Config{
		Session: "s1",
		Effort:  ai.ThinkingHigh,
	})
	run(t, a, "hi")

	p.mu.Lock()
	defer p.mu.Unlock()
	level, ok := ai.GetOption(p.opts[0], ai.Thinking)
	assert.True(t, ok)
	assert.Equal(t, ai.ThinkingHigh, level)
}
