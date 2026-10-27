package harness_test

import (
	"context"
	"regexp"
	"testing"

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

// taskHarness returns a durable harness over store, with the subagents
// agents, and the model script p.
func taskHarness(t *testing.T, p *script, store session.Storer, agents ...def.Agent) *harness.Harness {
	return harness.New(taskOptions(t, p, store, agents...)...)
}

// taskOptions returns the options of a harness with p, store, and the
// subagents agents.
func taskOptions(t *testing.T, p *script, store session.Storer, agents ...def.Agent) []harness.Option {
	readers := make([]def.AgentsReader, len(agents))
	for i, a := range agents {
		readers[i] = a
	}
	return with(base(t, p),
		harness.Store(store),
		harness.Agents(readers...),
	)
}

func TestTaskRunsTheSubagentInAChildSession(t *testing.T) {
	ctx := context.Background()
	store := session.NewMemoryStore()
	p := &script{responses: []ai.Message{
		callTool("c1", "agent", `{"agent":"reviewer","prompt":"Review auth.go"}`),
		reply("2 problems"),
		reply("done"),
	}}
	h := taskHarness(t, p, store, reviewer)

	a, err := h.Durable(ctx, "s1")
	require.NoError(t, err)
	run(t, a, "fix it")

	result := toolResult(t, a)
	assert.False(t, result.IsError)
	assert.Contains(t, result.Text(), "2 problems")
	assert.Contains(t, result.Text(), "<usage>total_tokens: 15, tool_uses: 0, duration_ms: ")
	id := childID(t, result)
	assert.Regexp(t, `^s1\.reviewer-[0-9a-f]{6}$`, id)

	snap, err := store.Load(ctx, id)
	require.NoError(t, err)
	parent, ok, err := agent.Parent.Read(snap)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, agent.ParentRef{SessionID: "s1", ToolCallID: "c1"}, parent)

	child, err := h.Durable(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "You review code.", section(child, harness.SystemSection))
	assert.NotContains(t, toolNames(child), "agent", "a child cannot start a subagent")
}

func TestTaskListsTheSubagents(t *testing.T) {
	p := &script{responses: []ai.Message{reply("hi")}}
	a, err := taskHarness(t, p, session.NewMemoryStore(), reviewer).New(context.Background())
	require.NoError(t, err)
	run(t, a, "hello")

	assert.Contains(t, section(a, "agents"), "- reviewer: Review a diff.")
	assert.Contains(t, toolNames(a), "agent")
}

func TestTaskResumesAChild(t *testing.T) {
	ctx := context.Background()
	store := session.NewMemoryStore()
	p := &script{responses: []ai.Message{
		callTool("c1", "agent", `{"agent":"reviewer","prompt":"first"}`),
		reply("one"),
		reply("ok"),
	}}
	h := taskHarness(t, p, store, reviewer)
	a, err := h.Durable(ctx, "s1")
	require.NoError(t, err)
	run(t, a, "go")
	id := childID(t, toolResult(t, a))

	p.responses = []ai.Message{
		callTool("c2", "agent", `{"agent":"reviewer","prompt":"second","resume":"`+id+`"}`),
		reply("two"),
		reply("ok"),
	}
	run(t, a, "again")

	child, err := h.Durable(ctx, id)
	require.NoError(t, err)
	var inputs []string
	for _, m := range child.Log().Messages() {
		if m.Role == ai.RoleUser {
			inputs = append(inputs, m.Text())
		}
	}
	assert.Equal(t, []string{"first", "second"}, inputs)
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
			a, err := taskHarness(t, p, session.NewMemoryStore(), reviewer).Durable(context.Background(), "s1")
			require.NoError(t, err)
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
	h := harness.New(with(base(t, p),
		harness.Store(store),
		harness.Agents(limited),
		harness.Toolset(harness.Tools(echo, other)),
	)...)

	a, err := h.Durable(context.Background(), "s1")
	require.NoError(t, err)
	run(t, a, "go")

	child, err := h.Durable(context.Background(), childID(t, toolResult(t, a)))
	require.NoError(t, err)
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
			a, err := taskHarness(t, p, session.NewMemoryStore(), sub).Durable(context.Background(), "s1")
			require.NoError(t, err)
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
	h := harness.New(with(base(t, p),
		harness.Catalog(cat),
		harness.Agents(sub),
	)...)

	a, err := h.New(context.Background())
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
	h := harness.New(with(base(t, p),
		harness.Catalog(cat),
		harness.Agents(sub),
	)...)

	a, err := h.New(context.Background())
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
	a, err := taskHarness(t, p, session.NewMemoryStore(), reviewer).Durable(ctx, "s1")
	require.NoError(t, err)

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
	_, err = s.Wait()
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
	a, err := harness.New(with(base(t, p), harness.Agents(reviewer))...).New(context.Background())
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
