package harness_test

import (
	"context"
	"regexp"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/def"
	"github.com/sonnes/gopi/pkg/harness"
	"github.com/sonnes/gopi/pkg/session"
	"github.com/sonnes/gopi/pkg/tool"
)

var reviewer = def.Agent{
	Name:        "reviewer",
	Description: "Review a diff.",
	Prompt:      "You review code.",
}

var agentID = regexp.MustCompile(`agent_id: (\S+)`)

// childID returns the agent id in the text of the result of an agent call.
func childID(t *testing.T, m ai.Message) string {
	t.Helper()
	match := agentID.FindStringSubmatch(m.Text())
	require.Len(t, match, 2, "the result names the agent: %s", m.Text())
	return match[1]
}

// childConfig returns c for the child session that the tool result m
// names. It has no Spec, so the child keeps its model.
func childConfig(t *testing.T, c agent.Config, m ai.Message) agent.Config {
	t.Helper()
	c.SessionID = childID(t, m)
	c.Spec = ""
	return c
}

// taskHarness returns a harness with the subagents agents and the model
// script p.
func taskHarness(p *script, agents ...def.Agent) *harness.Harness {
	return harness.New(with(base(p), agentTool, harness.Defs(def.Set{Agents: agents}))...)
}

// taskConfig returns the config of the session s1 of a test over store.
func taskConfig(t *testing.T, store session.Storer) agent.Config {
	t.Helper()
	c := config(t)
	c.Store = store
	return c
}

func TestTaskRunsTheSubagentInAChildSession(t *testing.T) {
	ctx := context.Background()
	store := session.NewMemoryStore()
	p := &script{responses: []ai.Message{
		callTool("c1", "agent", `{"agent":"reviewer","prompt":"Review auth.go"}`),
		reply("2 problems"),
		reply("done"),
	}}
	h := taskHarness(p, reviewer)
	c := taskConfig(t, store)

	a := start(t, h, c)
	run(t, a, "fix it")

	result := toolResult(t, a)
	assert.False(t, result.IsError)
	assert.Contains(t, result.Text(), "2 problems")
	assert.Contains(t, result.Text(), "<usage>total_tokens: 15, tool_uses: 0, duration_ms: ")
	id := childID(t, result)
	assert.NoError(t, uuid.Validate(id), "the child has a UUID")

	snap, err := store.Load(ctx, id)
	require.NoError(t, err)
	parent, ok, err := agent.Parent.Read(snap)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, agent.ParentRef{SessionID: "s1", ToolCallID: "c1"}, parent)

	c.SessionID = id
	c.Spec = ""
	child := start(t, h, c)
	assert.Equal(t, "You review code.", section(child, harness.SystemSection))
	assert.NotContains(t, toolNames(child), "agent", "a child cannot start a subagent")
}

func TestTaskListsTheSubagents(t *testing.T) {
	p := &script{responses: []ai.Message{reply("hi")}}
	a, err := taskHarness(p, reviewer).New(context.Background(), config(t))
	require.NoError(t, err)
	run(t, a, "hello")

	assert.Contains(t, section(a, "agents"), "- reviewer: Review a diff.")
	assert.Contains(t, toolNames(a), "agent")
}

func TestTaskResumesAChild(t *testing.T) {
	store := session.NewMemoryStore()
	p := &script{responses: []ai.Message{
		callTool("c1", "agent", `{"agent":"reviewer","prompt":"first"}`),
		reply("one"),
		reply("ok"),
	}}
	h := taskHarness(p, reviewer)
	c := taskConfig(t, store)
	a := start(t, h, c)
	run(t, a, "go")
	id := childID(t, toolResult(t, a))

	p.responses = []ai.Message{
		callTool("c2", "agent", `{"agent":"reviewer","prompt":"second","resume":"`+id+`"}`),
		reply("two"),
		reply("ok"),
	}
	run(t, a, "again")

	c.SessionID = id
	c.Spec = ""
	child := start(t, h, c)
	var inputs []string
	for _, m := range child.Log().Messages() {
		if m.Role == ai.RoleUser {
			inputs = append(inputs, m.Text())
		}
	}
	assert.Equal(t, []string{"first", "second"}, inputs)
}

func TestTaskResumesOnlyItsOwnChild(t *testing.T) {
	store := session.NewMemoryStore()
	p := &script{responses: []ai.Message{
		callTool("c1", "agent", `{"agent":"reviewer","prompt":"first"}`),
		reply("one"),
		reply("ok"),
	}}
	h := taskHarness(p, reviewer)
	c := taskConfig(t, store)
	otherConfig := c
	otherConfig.SessionID = "s2"
	other := start(t, h, otherConfig)
	run(t, other, "go")
	id := childID(t, toolResult(t, other))

	p.responses = []ai.Message{
		callTool("c2", "agent", `{"agent":"reviewer","prompt":"second","resume":"`+id+`"}`),
		reply("ok"),
	}
	a := start(t, h, c)
	run(t, a, "go")

	result := toolResult(t, a)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Text(), "is not a subagent of this session")
}

func TestTaskErrors(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"unknown agent", `{"agent":"nope","prompt":"x"}`, "reviewer"},
		{"no prompt", `{"agent":"reviewer"}`, "prompt"},
		{"resume of another session", `{"agent":"reviewer","prompt":"x","resume":"s2.reviewer-abcdef"}`, "s2.reviewer-abcdef"},
		{"bad input", `{"agent":1}`, "input"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &script{responses: []ai.Message{
				callTool("c1", "agent", tt.input),
				reply("done"),
			}}
			a := start(t, taskHarness(p, reviewer), taskConfig(t, session.NewMemoryStore()))
			run(t, a, "go")

			result := toolResult(t, a)
			assert.True(t, result.IsError)
			assert.Contains(t, result.Text(), tt.want)
		})
	}
}

func TestTaskGivesTheChildOnlyItsTools(t *testing.T) {
	other := tool.New("other", func(_ context.Context, in echoInput) (string, error) {
		return in.S, nil
	}, tool.Description("Another tool."))
	limited := reviewer
	limited.Tools = []string{"echo"}
	p := &script{responses: []ai.Message{
		callTool("c1", "agent", `{"agent":"reviewer","prompt":"go"}`),
		reply("done"),
		reply("ok"),
	}}
	store := session.NewMemoryStore()
	h := harness.New(with(base(p), agentTool,
		harness.Defs(def.Set{Agents: []def.Agent{limited}}),
		harness.Toolset(harness.Tools(echo, other)),
	)...)
	c := taskConfig(t, store)

	a := start(t, h, c)
	run(t, a, "go")

	child := start(t, h, childConfig(t, c, toolResult(t, a)))
	assert.Equal(t, []string{"echo"}, toolNames(child))
}

func TestTaskKeepsAToolWithContent(t *testing.T) {
	other := tool.New("other", func(_ context.Context, in echoInput) (string, error) {
		return in.S, nil
	}, tool.Description("Another tool."))
	limited := reviewer
	limited.Tools = []string{"echo(hi *)", "other"}
	limited.Disallowed = []string{"other"}
	p := &script{responses: []ai.Message{
		callTool("c1", "agent", `{"agent":"reviewer","prompt":"go"}`),
		reply("done"),
		reply("ok"),
	}}
	store := session.NewMemoryStore()
	h := harness.New(with(base(p), agentTool,
		harness.Defs(def.Set{Agents: []def.Agent{limited}}),
		harness.Toolset(harness.Tools(echo, other)),
	)...)
	c := taskConfig(t, store)

	a := start(t, h, c)
	run(t, a, "go")

	child := start(t, h, childConfig(t, c, toolResult(t, a)))
	assert.Equal(t, []string{"echo"}, toolNames(child))
}

func TestTaskRunsTheChildOnItsModel(t *testing.T) {
	tests := []struct {
		name  string
		model string
		want  string
	}{
		{"inherit", "inherit", "one"},
		{"no model", "", "one"},
		{"a spec", "fake/two", "two"},
		{"a bare id", "two", "two"},
		{"an unknown alias", "opus", "one"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sub := reviewer
			sub.Model = tt.model
			p := &script{responses: []ai.Message{
				callTool("c1", "agent", `{"agent":"reviewer","prompt":"go"}`),
				reply("done"),
				reply("ok"),
			}}
			a := start(t, taskHarness(p, sub), taskConfig(t, session.NewMemoryStore()))
			run(t, a, "go")

			assert.Equal(t, []string{"one", tt.want, "one"}, p.called())
		})
	}
}

// TestTaskChildModelWithADriver checks that a driver of the same model
// ID does not change the model of a child.
func TestTaskChildModelWithADriver(t *testing.T) {
	sub := reviewer
	sub.Model = "two"
	p := &script{responses: []ai.Message{
		callTool("c1", "agent", `{"agent":"reviewer","prompt":"go"}`),
		reply("done"),
		reply("ok"),
	}}
	cat := newCatalog(p)
	cat.RegisterDriver("cli", harness.New(), ai.Model{ID: "two"})
	h := harness.New(with(base(p), agentTool,
		harness.Catalog(cat),
		harness.Defs(def.Set{Agents: []def.Agent{sub}}),
	)...)

	a, err := h.New(context.Background(), config(t))
	require.NoError(t, err)
	run(t, a, "go")

	assert.Equal(t, []string{"one", "two", "one"}, p.called())
}

// TestTaskFailsOnAnAmbiguousModel checks that a child with a model that
// two providers serve does not run on the model of its parent.
func TestTaskFailsOnAnAmbiguousModel(t *testing.T) {
	sub := reviewer
	sub.Model = "two"
	p := &script{responses: []ai.Message{
		callTool("c1", "agent", `{"agent":"reviewer","prompt":"go"}`),
		reply("ok"),
	}}
	cat := newCatalog(p)
	cat.RegisterTextProvider("other", p, ai.Model{ID: "two"})
	h := harness.New(with(base(p), agentTool,
		harness.Catalog(cat),
		harness.Defs(def.Set{Agents: []def.Agent{sub}}),
	)...)

	a, err := h.New(context.Background(), config(t))
	require.NoError(t, err)
	run(t, a, "go")

	result := toolResult(t, a)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Text(), `ambiguous model "two"`)
	assert.Equal(t, []string{"one", "one"}, p.called(), "no child runs")
}

func TestTaskSendsTheChildOnTheParentStream(t *testing.T) {
	ctx := context.Background()
	p := &script{responses: []ai.Message{
		callTool("c1", "agent", `{"agent":"reviewer","description":"Review auth","prompt":"go"}`),
		reply("done"),
		reply("ok"),
	}}
	a := start(t, taskHarness(p, reviewer), taskConfig(t, session.NewMemoryStore()))

	var (
		start  agent.EventSubagentStart
		end    agent.EventSubagentEnd
		events []agent.Event
	)
	s := a.Run(ctx, session.Text("go"))
	for e, err := range s.Events() {
		require.NoError(t, err)
		switch e := e.(type) {
		case agent.EventSubagentStart:
			start = e
		case agent.EventSubagent:
			events = append(events, e.Event)
		case agent.EventSubagentEnd:
			end = e
		}
	}
	_, err := s.Wait()
	require.NoError(t, err)

	id := childID(t, toolResult(t, a))
	assert.Equal(t, agent.EventSubagentStart{
		ToolCallID: "c1",
		SessionID:  id,
		Name:       "reviewer",
		Title:      "Review auth",
		Prompt:     "go",
		Model:      start.Model,
	}, start)
	assert.NotEmpty(t, start.Model, "the start names the model of the child")
	require.NotEmpty(t, events)
	assert.IsType(t, agent.EventAgentStart{}, events[0], "the run of the child is wrapped whole")
	assert.IsType(t, agent.EventAgentEnd{}, events[len(events)-1])
	assert.Equal(t, agent.EventSubagentEnd{
		ToolCallID: "c1",
		SessionID:  id,
		Status:     agent.SubagentCompleted,
		Text:       "done",
	}, end)
}

func TestTaskOfAnUnsavedAgentCannotResume(t *testing.T) {
	p := &script{responses: []ai.Message{
		callTool("c1", "agent", `{"agent":"reviewer","prompt":"go"}`),
		reply("done"),
		callTool("c2", "agent", `{"agent":"reviewer","prompt":"go","resume":"x"}`),
		reply("ok"),
	}}
	a, err := harness.New(with(base(p), agentTool, harness.Defs(def.Set{Agents: []def.Agent{reviewer}}))...).New(context.Background(), config(t))
	require.NoError(t, err)
	run(t, a, "go")

	var results []ai.Message
	for _, m := range a.Log().Messages() {
		if m.Role == ai.RoleToolResult {
			results = append(results, m)
		}
	}
	require.Len(t, results, 2)
	assert.False(t, results[0].IsError)
	assert.Contains(t, results[0].Text(), "done")
	assert.True(t, results[1].IsError)
}

func TestASubagentStartsUnderOnly(t *testing.T) {
	store := session.NewMemoryStore()
	p := &script{responses: []ai.Message{
		callTool("c1", "agent", `{"agent":"echoer","prompt":"go"}`),
		reply("echoed"),
		reply("done"),
	}}
	echoer := def.Agent{
		Name:        "echoer",
		Description: "Echo.",
		Tools:       []string{"echo"},
	}
	h := harness.New(with(base(p), agentTool,
		harness.Defs(def.Set{Agents: []def.Agent{echoer}}),
		harness.Toolset(harness.Tools(echo, otherTool)),
	)...)
	c := taskConfig(t, store)
	c.ToolNames = []string{"echo", "other", "agent"}

	a := start(t, h, c)
	run(t, a, "go")

	result := toolResult(t, a)
	assert.False(t, result.IsError, result.Text())
	assert.Contains(t, result.Text(), "echoed")
}

var otherTool = tool.Func("other", func(context.Context, ai.ToolCall) (ai.ToolResult, error) {
	return ai.ToolResult{}, nil
})

func TestASubagentFileWithAnUnknownToolFails(t *testing.T) {
	p := &script{responses: []ai.Message{
		callTool("c1", "agent", `{"agent":"echoer","prompt":"go"}`),
		reply("done"),
	}}
	echoer := def.Agent{
		Name:        "echoer",
		Description: "Echo.",
		Tools:       []string{"echo", "ech0"},
	}
	h := harness.New(with(base(p), agentTool,
		harness.Defs(def.Set{Agents: []def.Agent{echoer}}),
		harness.Toolset(harness.Tools(echo)),
	)...)

	a := start(t, h, taskConfig(t, session.NewMemoryStore()))
	run(t, a, "go")

	result := toolResult(t, a)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Text(), `"ech0" is not in the tool list`)
}
