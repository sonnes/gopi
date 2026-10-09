package agent_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/agent/internal/emittest"
	"github.com/sonnes/gopi/pkg/ai"
)

// blocks returns the sections as content blocks, in order.
func blocks(sections []ai.Section) []ai.Content {
	out := make([]ai.Content, 0, len(sections))
	for _, s := range sections {
		out = append(out, s)
	}
	return out
}

// lastSystem returns the last message of a call if it is a system
// message, or nil.
func lastSystem(call []ai.Message) *ai.Message {
	last := call[len(call)-1]
	if last.Role != ai.RoleSystem {
		return nil
	}
	return &last
}

// twoTurns runs an agent with the sections function fn over a tool call
// and an answer. It returns the script, with the messages and the
// options of each model call, and the agent.
func twoTurns(t *testing.T, fn func(context.Context) ([]ai.Section, error)) (*script, *agent.Agent) {
	t.Helper()
	p := &script{responses: []ai.Message{
		toolCall("c1", "echo", `{"s":"x"}`),
		text("done"),
	}}
	a := agent.New(model(p), agent.Toolset(echo), agent.System(fn))
	events, _, err := collect(t, a.Run(context.Background(), ai.UserMessage("hi")))
	require.NoError(t, err)
	emittest.CheckEvents(t, events)

	require.Len(t, p.received(), 2)
	return p, a
}

func TestSections(t *testing.T) {
	tests := []struct {
		name   string
		first  []ai.Section
		second []ai.Section
		// want is the content of the system message before the second
		// call, or nil if there is none.
		want []ai.Content
	}{
		{
			name:   "unchanged sections add nothing",
			first:  []ai.Section{rules, todos},
			second: []ai.Section{rules, todos},
			want:   nil,
		},
		{
			name:   "a changed section is patched in place",
			first:  []ai.Section{rules, todos},
			second: []ai.Section{thorough, todos},
			want:   []ai.Content{thorough},
		},
		{
			name:   "a missing section is removed",
			first:  []ai.Section{rules, todos},
			second: []ai.Section{rules},
			want:   []ai.Content{ai.SectionRemoved{Name: "todos"}},
		},
		{
			name:   "a new section is appended",
			first:  []ai.Section{rules},
			second: []ai.Section{rules, todos},
			want:   []ai.Content{todos},
		},
		{
			name:   "a new order sets every section again",
			first:  []ai.Section{rules, todos},
			second: []ai.Section{todos, rules},
			want: []ai.Content{
				ai.SectionRemoved{Name: "rules"},
				ai.SectionRemoved{Name: "todos"},
				todos,
				rules,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, a := twoTurns(t, sequence(tt.first, tt.second))
			calls := p.received()

			assert.Equal(t, ai.RoleUser, calls[0][0].Role, "the base is not in the messages")
			for i := range calls {
				assert.Equal(t, blocks(tt.first), systemPrompt(p.opts[i]),
					"call %d sends the sections of the base as the system prompt", i)
			}

			second := lastSystem(calls[1])
			if tt.want == nil {
				assert.Nil(t, second)
			} else {
				require.NotNil(t, second)
				assert.Equal(t, tt.want, second.Content)
			}
			assert.Equal(t, tt.second, a.State().Sections())
		})
	}
}

func TestSectionsError(t *testing.T) {
	boom := errors.New("boom")
	p := &script{responses: []ai.Message{text("done")}}
	a := agent.New(model(p), agent.System(func(context.Context) ([]ai.Section, error) {
		return nil, boom
	}))

	_, _, err := collect(t, a.Run(context.Background(), ai.UserMessage("hi")))
	require.ErrorIs(t, err, boom)
	assert.Empty(t, p.received(), "the agent calls no model without its sections")
}

func TestNoSectionsOptionKeepsTheHistory(t *testing.T) {
	ctx := context.Background()

	p := &script{responses: []ai.Message{text("one")}}
	a := agent.New(model(p),
		agent.System(sequence([]ai.Section{rules})),
	)
	_, _, err := collect(t, a.Run(ctx, ai.UserMessage("first")))
	require.NoError(t, err)

	q := &script{responses: []ai.Message{text("two")}}
	b := agent.New(model(q), agent.History(history(a)...))
	_, _, err = collect(t, b.Run(ctx, ai.UserMessage("second")))
	require.NoError(t, err)

	calls := q.received()
	require.Len(t, calls, 1)
	assert.Nil(t, lastSystem(calls[0]), "an agent without System leaves the sections of the history")
	assert.Equal(t, []ai.Content{rules}, systemPrompt(q.opts[0]))
	assert.Equal(t, []ai.Section{rules}, b.State().Sections())
}

func TestHistoryReplaysSections(t *testing.T) {
	ctx := context.Background()

	p := &script{responses: []ai.Message{
		toolCall("c1", "tool_search", `{"query":"weather"}`),
		text("loaded"),
	}}
	a := agent.New(model(p),
		agent.Toolset(echo, weather),
		agent.System(sequence([]ai.Section{rules})),
	)
	_, _, err := collect(t, a.Run(ctx, ai.UserMessage("first")))
	require.NoError(t, err)

	q := &script{responses: []ai.Message{text("done")}}
	b := agent.New(model(q), agent.History(history(a)...),
		agent.Toolset(echo),
		agent.System(sequence([]ai.Section{thorough})),
	)
	_, _, err = collect(t, b.Run(ctx, ai.UserMessage("second")))
	require.NoError(t, err)

	calls := q.received()
	require.Len(t, calls, 1)
	change := calls[0][len(calls[0])-2]
	require.Equal(t, ai.RoleSystem, change.Role, "the change comes before the input")
	assert.Equal(t, []ai.Content{
		thorough,
		ai.ToolRemoved{Name: "tool_search"},
		ai.ToolRemoved{Name: "get_weather"},
	}, change.Content, "one message patches the sections, then changes the tools")
}

func TestBaseGoesInTheOptions(t *testing.T) {
	p := &script{responses: []ai.Message{
		toolCall("c1", "echo", `{"s":"x"}`),
		text("done"),
	}}
	a := agent.New(model(p),
		agent.Toolset(echo),
		agent.System(sequence([]ai.Section{rules})),
	)
	_, msgs, err := collect(t, a.Run(context.Background(), ai.UserMessage("hi")))
	require.NoError(t, err)

	require.NotEmpty(t, msgs)
	base := ai.SystemMessage(rules, ai.ToolAdded{Tool: echo.Info()})
	assert.Equal(t, base.Content, msgs[0].Content, "the log starts with the base")

	calls := p.received()
	require.Len(t, calls, 2)
	for i := range calls {
		assert.Equal(t, ai.RoleUser, calls[i][0].Role, "call %d gets the base in the options", i)
		assert.Equal(t, []ai.Content{rules}, systemPrompt(p.opts[i]))
		assert.Equal(t, []string{"echo"}, toolNames(p.opts[i]))
	}
}

func TestHistoryWithoutABase(t *testing.T) {
	ctx := context.Background()

	p := &script{responses: []ai.Message{text("one")}}
	a := agent.New(model(p))
	_, _, err := collect(t, a.Run(ctx, ai.UserMessage("first")))
	require.NoError(t, err)

	q := &script{responses: []ai.Message{text("two")}}
	b := agent.New(model(q), agent.History(history(a)...), agent.Toolset(echo))
	_, _, err = collect(t, b.Run(ctx, ai.UserMessage("second")))
	require.NoError(t, err)

	calls := q.received()
	require.Len(t, calls, 1)
	assert.Equal(t, ai.RoleUser, calls[0][0].Role, "a history that starts with a user message has no base")
	assert.Empty(t, toolNames(q.opts[0]))
	change := calls[0][len(calls[0])-2]
	require.Equal(t, ai.RoleSystem, change.Role)
	assert.Equal(t, []ai.Content{ai.ToolAdded{Tool: echo.Info()}}, change.Content,
		"the tools go as a change")
}
