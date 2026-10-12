package agent_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/session"
	"github.com/sonnes/gopi/pkg/tool"
)

func TestHistoryReplaysTools(t *testing.T) {
	ctx := context.Background()
	opts := []agent.Option{agent.Toolset(echo, weather)}

	p := &script{responses: []ai.Message{
		toolCall("c1", "tool_search", `{"query":"weather"}`),
		text("loaded"),
	}}
	a := agent.New(model(p), opts...)
	_, _, err := collect(t, a.Run(ctx, session.Text("first")))
	require.NoError(t, err)

	q := &script{responses: []ai.Message{
		toolCall("c2", "get_weather", `{"city":"Paris"}`),
		text("done"),
	}}
	b := resume(t, model(q), history(a), opts...)
	_, msgs, err := collect(t, b.Run(ctx, session.Text("second")))
	require.NoError(t, err)

	calls := q.received()
	require.Len(t, calls, 2)
	last := calls[0][len(calls[0])-1]
	assert.NotEqual(t, ai.RoleSystem, last.Role, "an agent with a saved history declares no change twice")
	require.Len(t, msgs, 3)
	assert.Equal(t, "Sunny in Paris", msgs[1].Text(), "an agent with a saved history keeps the loaded tool")
}

func TestHistoryRemovesUnknownTools(t *testing.T) {
	ctx := context.Background()

	p := &script{responses: []ai.Message{
		toolCall("c1", "tool_search", `{"query":"weather"}`),
		text("loaded"),
	}}
	a := agent.New(model(p),
		agent.Toolset(echo, weather),
	)
	_, _, err := collect(t, a.Run(ctx, session.Text("first")))
	require.NoError(t, err)

	q := &script{responses: []ai.Message{text("done")}}
	b := resume(t, model(q), history(a), agent.Toolset(echo))
	_, _, err = collect(t, b.Run(ctx, session.Text("second")))
	require.NoError(t, err)

	calls := q.received()
	require.Len(t, calls, 1)
	change := calls[0][len(calls[0])-2]
	assert.Equal(t, []ai.Content{
		ai.ToolRemoved{Name: "tool_search"},
		ai.ToolRemoved{Name: "get_weather"},
	}, change.Content, "an agent without deferred tools has no search, and an agent without the tool removes it")
}

func TestHistoryKeepsTheBaseTools(t *testing.T) {
	tests := []struct {
		name   string
		first  []tool.Tool
		second []tool.Tool
		// wantTools are the tools of the call after the history: the tools of the
		// base.
		wantTools []string
		// wantChange is the system message before the input.
		wantChange []ai.Content
		// wantState are the tools that the model can call after it.
		wantState []string
	}{
		{
			name:       "a new tool is a change",
			first:      []tool.Tool{echo},
			second:     []tool.Tool{echo, upper},
			wantTools:  []string{"echo"},
			wantChange: []ai.Content{ai.ToolAdded{Tool: upper.Info()}},
			wantState:  []string{"echo", "upper"},
		},
		{
			name:       "a missing tool is a change",
			first:      []tool.Tool{echo, upper},
			second:     []tool.Tool{echo},
			wantTools:  []string{"echo", "upper"},
			wantChange: []ai.Content{ai.ToolRemoved{Name: "upper"}},
			wantState:  []string{"echo"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			p := &script{responses: []ai.Message{text("one")}}
			a := agent.New(model(p), agent.Toolset(tt.first...))
			_, _, err := collect(t, a.Run(ctx, session.Text("first")))
			require.NoError(t, err)

			q := &script{responses: []ai.Message{text("two")}}
			b := resume(t, model(q), history(a), agent.Toolset(tt.second...))
			_, _, err = collect(t, b.Run(ctx, session.Text("second")))
			require.NoError(t, err)

			calls := q.received()
			require.Len(t, calls, 1)
			assert.Equal(t, tt.wantTools, toolNames(q.opts[0]), "the tools of the base stay the same")
			change := calls[0][len(calls[0])-2]
			require.Equal(t, ai.RoleSystem, change.Role, "the change comes before the input")
			assert.Equal(t, tt.wantChange, change.Content)
			assert.Equal(t, tt.wantState, names(b.State().Tools()))
		})
	}
}
