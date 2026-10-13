package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/session"
)

// view is what a caller reads from a [agent.State].
type view struct {
	Messages  []ai.Message
	Tools     []string
	Sections  []ai.Section
	Turn      int
	Running   bool
	Streaming *ai.Message
	Pending   []ai.ToolCall
	Usage     ai.Usage
	Err       string
}

func viewOf(st agent.State) view {
	v := view{
		Messages:  st.Messages(),
		Sections:  st.Sections(),
		Turn:      st.Turn(),
		Running:   st.Running(),
		Streaming: st.Streaming(),
		Pending:   st.Pending(),
		Usage:     st.Usage(),
	}
	for _, info := range st.Tools() {
		v.Tools = append(v.Tools, info.Name)
	}
	if err := st.Err(); err != nil {
		v.Err = err.Error()
	}
	return v
}

func TestStateApplyMatchesAgent(t *testing.T) {
	tests := []struct {
		name  string
		p     *script
		opts  []agent.Option
		input []session.Entry
	}{
		{
			name:  "text",
			p:     &script{responses: []ai.Message{text("hello")}},
			input: []session.Entry{session.Text("hi")},
		},
		{
			name: "a tool call",
			p: &script{responses: []ai.Message{
				toolCall("c1", "echo", `{"s":"x"}`),
				text("done"),
			}},
			opts:  []agent.Option{agent.Toolset(echo)},
			input: []session.Entry{session.Text("hi")},
		},
		{
			name: "a search loads a tool",
			p: &script{responses: []ai.Message{
				toolCall("c1", "tool_search", `{"query":"weather"}`),
				text("loaded"),
			}},
			opts:  []agent.Option{agent.Toolset(echo, weather)},
			input: []session.Entry{session.Text("hi")},
		},
		{
			name:  "sections",
			p:     &script{responses: []ai.Message{text("ok")}},
			opts:  []agent.Option{agent.System(sequence([]ai.Section{rules}))},
			input: []session.Entry{session.Text("hi")},
		},
		{
			name:  "a failure",
			p:     &script{fail: errors.New("boom")},
			input: []session.Entry{session.Text("hi")},
		},
		{
			name: "an ephemeral entry",
			p:    &script{responses: []ai.Message{text("ok")}},
			input: []session.Entry{
				session.Text("hi"),
				session.Text("the time is 9:00", session.Ephemeral()),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := agent.New(model(tt.p), tt.opts...)

			var replay agent.State
			for evt := range a.Run(context.Background(), tt.input...).Events() {
				replay.Apply(evt)
			}

			assert.Equal(t, viewOf(a.State()), viewOf(replay))
		})
	}
}

func TestStateSnapshotRoundTrips(t *testing.T) {
	p := &script{responses: []ai.Message{
		toolCall("c1", "echo", `{"s":"x"}`),
		text("done"),
	}}
	a := agent.New(model(p), agent.Toolset(echo))
	_, _, err := collect(t, a.Run(context.Background(), session.Text("hi")))
	require.NoError(t, err)

	data, err := json.Marshal(a.State())
	require.NoError(t, err)
	var back agent.State
	require.NoError(t, json.Unmarshal(data, &back))

	again, err := json.Marshal(back)
	require.NoError(t, err)
	assert.JSONEq(t, string(data), string(again))
	assert.Equal(t, msgTexts(a.State().Messages()), msgTexts(back.Messages()))
}

func TestStateSnapshotBetweenRuns(t *testing.T) {
	tests := []struct {
		name      string
		responses []ai.Message
		// hook gets the cancel of the first run.
		hook func(cancel context.CancelFunc) agent.BeforeToolFunc
	}{
		{
			name:      "the events of a run keep the snapshot fresh",
			responses: []ai.Message{text("one"), text("two")},
		},
		{
			name:      "a call with no result stays in the history",
			responses: []ai.Message{toolCall("c1", "echo", `{"s":"x"}`), text("two")},
			hook: func(cancel context.CancelFunc) agent.BeforeToolFunc {
				return func(ctx context.Context, _ agent.State, c ai.ToolCall) (ai.ToolCall, error) {
					cancel()
					return c, ctx.Err()
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			opts := []agent.Option{agent.Toolset(echo)}
			if tt.hook != nil {
				opts = append(opts, agent.BeforeTool(tt.hook(cancel)))
			}
			a := agent.New(model(&script{responses: tt.responses}), opts...)

			_, _, _ = collect(t, a.Run(ctx, session.Text("first")))

			viewer := a.State()
			for evt := range a.Run(context.Background(), session.Text("second")).Events() {
				viewer.Apply(evt)
			}
			assert.Equal(t, viewOf(a.State()), viewOf(viewer))
		})
	}
}

func TestStateCopyDoesNotAllocate(t *testing.T) {
	p := &script{responses: []ai.Message{text("one")}}
	a := agent.New(model(p))
	_, _, err := collect(t, a.Run(context.Background(), session.Text("hi")))
	require.NoError(t, err)

	allocs := testing.AllocsPerRun(100, func() {
		st := a.State()
		_ = st.Messages()
		_ = st.Pending()
	})
	assert.Zero(t, allocs)
}

func TestStateCopyKeepsItsLength(t *testing.T) {
	ctx := context.Background()
	p := &script{responses: []ai.Message{text("one"), text("two")}}
	a := agent.New(model(p))
	_, _, err := collect(t, a.Run(ctx, session.Text("first")))
	require.NoError(t, err)

	before := a.State()
	_, _, err = collect(t, a.Run(ctx, session.Text("second")))
	require.NoError(t, err)

	assert.Equal(t, []string{"first", "one"}, msgTexts(before.Messages()), "a later run does not change a copy")
	assert.Len(t, a.State().Messages(), 4)
}

func TestStateClipKeepsTheOriginal(t *testing.T) {
	var st agent.State
	for _, s := range []string{"one", "two", "three"} {
		st.Apply(agent.EventMessageEnd{Message: ai.UserMessage(s)})
	}

	c := st.Clip()
	c.Apply(agent.EventMessageEnd{Message: ai.UserMessage("copy")})
	st.Apply(agent.EventMessageEnd{Message: ai.UserMessage("original")})

	assert.Equal(t, []string{"one", "two", "three", "copy"}, msgTexts(c.Messages()))
	assert.Equal(t, []string{"one", "two", "three", "original"}, msgTexts(st.Messages()))
}
