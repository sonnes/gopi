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

// fakeCompactor is a strategy with a fixed limit. Without compact, it
// replaces each message after the first user message with one summary.
type fakeCompactor struct {
	limit   int
	compact func(agent.CompactInput) (agent.CompactResult, error)
	inputs  []agent.CompactInput
}

func (f *fakeCompactor) Limit(ai.Model) int { return f.limit }

func (f *fakeCompactor) Compact(
	_ context.Context,
	in agent.CompactInput,
) (agent.CompactResult, error) {
	f.inputs = append(f.inputs, in)
	if f.compact != nil {
		return f.compact(in)
	}
	return summarizeAfterFirst(in), nil
}

func summarizeAfterFirst(in agent.CompactInput) agent.CompactResult {
	first := 0
	for in.Entries[first].Message.Role != ai.RoleUser {
		first++
	}
	return agent.CompactResult{
		Strategy: "fake",
		Usage:    ai.Usage{Input: 7},
		Edits: []session.Edit{{
			First:    in.Entries[first+1].ID,
			Last:     in.Entries[len(in.Entries)-1].ID,
			Messages: []ai.Message{ai.UserMessage("summary")},
		}},
	}
}

// large returns a text answer that reports tokens of context.
func large(s string, tokens int) ai.Message {
	m := text(s)
	m.Usage = ai.Usage{Input: tokens}
	return m
}

// largeCall returns a tool call that reports tokens of context.
func largeCall(id string, tokens int) ai.Message {
	m := toolCall(id, "echo", `{"s":"x"}`)
	m.Usage = ai.Usage{Input: tokens}
	return m
}

func TestCompactionEditsTheViewAndKeepsTheLog(t *testing.T) {
	ctx := context.Background()
	p := &script{responses: []ai.Message{
		largeCall("c1", 1000),
		text("done"),
	}}
	c := &fakeCompactor{limit: 500}
	a := agent.New(model(p), agent.Toolset(echo), agent.Compaction(c))

	events, _, err := collect(t, a.Run(ctx, session.Text("hi")))
	require.NoError(t, err)

	require.Len(t, c.inputs, 1)
	assert.Equal(t, 500, c.inputs[0].Limit)
	assert.GreaterOrEqual(t, c.inputs[0].Tokens, 1000)
	assert.Equal(t, []string{"hi", "summary"}, msgTexts(p.received()[1]), "the call reads the view")

	state := a.State()
	assert.Equal(t, []string{"hi", "", "echo:x", "done"}, msgTexts(afterBase(t, state.Messages())), "the state keeps the history")
	assert.Equal(t, []string{"hi", "summary", "done"}, msgTexts(afterBase(t, state.View())))
	assert.Equal(t, 1000+1+7, state.Usage().Input, "the usage counts the strategy")

	var kinds []string
	for _, e := range a.Log().Entries() {
		if e.Kind != "" {
			kinds = append(kinds, e.Kind)
		}
	}
	assert.Equal(t, []string{session.KindCompaction}, kinds)

	assert.Equal(t, "agent.EventCompactionStart", typeBefore(events, "agent.EventCompaction"))
	var ended agent.EventCompaction
	for _, e := range events {
		if v, ok := e.(agent.EventCompaction); ok {
			ended = v
		}
	}
	assert.Equal(t, "fake", ended.Compaction.Strategy)
	assert.Empty(t, ended.Error)
	assert.Greater(t, ended.Compaction.Before, ended.Compaction.After)
}

func typeBefore(events []agent.Event, name string) string {
	all := types(events)
	for i, n := range all {
		if n == name && i > 0 {
			return all[i-1]
		}
	}
	return ""
}

func TestNoCompactionUnderTheLimit(t *testing.T) {
	p := &script{responses: []ai.Message{text("done")}}
	c := &fakeCompactor{limit: 500}
	a := agent.New(model(p), agent.Compaction(c))

	_, _, err := collect(t, a.Run(context.Background(), session.Text("hi")))
	require.NoError(t, err)
	assert.Empty(t, c.inputs)
}

func TestFailedCompactionDoesNotStopTheRun(t *testing.T) {
	tests := []struct {
		name    string
		compact func(agent.CompactInput) (agent.CompactResult, error)
		want    string
	}{
		{
			name: "the strategy fails",
			compact: func(agent.CompactInput) (agent.CompactResult, error) {
				return agent.CompactResult{}, errors.New("boom")
			},
			want: "boom",
		},
		{
			name: "the edits break the history",
			compact: func(in agent.CompactInput) (agent.CompactResult, error) {
				return agent.CompactResult{
					Edits: []session.Edit{{First: "nope", Last: "nope"}},
				}, nil
			},
			want: `session: edit 0: no entry "nope"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &script{responses: []ai.Message{
				largeCall("c1", 1000),
				text("done"),
			}}
			c := &fakeCompactor{limit: 500, compact: tt.compact}
			a := agent.New(model(p), agent.Toolset(echo), agent.Compaction(c))

			events, _, err := collect(t, a.Run(context.Background(), session.Text("hi")))
			require.NoError(t, err)

			var got string
			for _, e := range events {
				if v, ok := e.(agent.EventCompaction); ok {
					got = v.Error
				}
			}
			assert.Equal(t, tt.want, got)
			assert.Equal(t, a.State().Messages(), a.State().View())
			assert.Len(t, p.received(), 2, "the next call reads the old view")
		})
	}
}

func TestFailedCompactionRetriesAfterGrowth(t *testing.T) {
	p := &script{responses: []ai.Message{
		largeCall("c1", 1000),
		largeCall("c2", 1050),
		largeCall("c3", 1200),
		text("done"),
	}}
	c := &fakeCompactor{
		limit: 500,
		compact: func(agent.CompactInput) (agent.CompactResult, error) {
			return agent.CompactResult{}, errors.New("boom")
		},
	}
	a := agent.New(model(p), agent.Toolset(echo), agent.Compaction(c))

	_, _, err := collect(t, a.Run(context.Background(), session.Text("hi")))
	require.NoError(t, err)
	assert.Len(t, c.inputs, 2, "1000 fails, 1050 is under 10% of growth, 1200 is over")
}

func TestOldUsageDoesNotStartACompaction(t *testing.T) {
	noUsage := toolCall("c2", "echo", `{"s":"x"}`)
	noUsage.Usage = ai.Usage{}
	p := &script{responses: []ai.Message{
		largeCall("c1", 1000),
		noUsage,
		text("done"),
	}}
	c := &fakeCompactor{limit: 500}
	a := agent.New(model(p), agent.Toolset(echo), agent.Compaction(c))

	_, _, err := collect(t, a.Run(context.Background(), session.Text("hi")))
	require.NoError(t, err)
	assert.Len(t, c.inputs, 1, "the usage of 1000 is from before the compaction")
}

func TestCompactionKeepsSystemChangesInTheView(t *testing.T) {
	sections := "one"
	p := &script{responses: []ai.Message{
		largeCall("c1", 1000),
		text("done"),
	}}
	c := &fakeCompactor{limit: 500}
	a := agent.New(model(p),
		agent.Toolset(echo),
		agent.Compaction(c),
		agent.System(func(context.Context) ([]ai.Section, error) {
			return []ai.Section{{Name: "s", Text: sections}}, nil
		}),
		agent.AfterTurn(func(context.Context, agent.State) ([]session.Entry, error) {
			sections = "two"
			return nil, nil
		}),
	)

	_, _, err := collect(t, a.Run(context.Background(), session.Text("hi")))
	require.NoError(t, err)

	got := a.State().Sections()
	require.Len(t, got, 1)
	assert.Equal(t, "two", got[0].Text)

	var systems int
	for _, m := range a.State().View() {
		if m.Role == ai.RoleSystem {
			systems++
		}
	}
	assert.Equal(t, 2, systems, "the base and the change")
	assert.Equal(t, ai.RoleSystem, a.State().View()[0].Role, "the base stays first")
}

func TestViewerGetsTheSameView(t *testing.T) {
	p := &script{responses: []ai.Message{
		largeCall("c1", 1000),
		text("done"),
	}}
	a := agent.New(model(p), agent.Toolset(echo), agent.Compaction(&fakeCompactor{limit: 500}))
	s := a.Run(context.Background(), session.Text("hi"))

	var viewer agent.State
	for evt, err := range s.Events() {
		require.NoError(t, err)
		data, err := json.Marshal(evt)
		require.NoError(t, err)
		decoded, err := agent.UnmarshalEvent(data)
		require.NoError(t, err)
		viewer.Apply(decoded)
	}
	_, err := s.Wait()
	require.NoError(t, err)

	assert.Equal(t, msgTexts(a.State().View()), msgTexts(viewer.View()))
	assert.Equal(t, a.State().Usage(), viewer.Usage())

	data, err := json.Marshal(viewer)
	require.NoError(t, err)
	var snapshot agent.State
	require.NoError(t, json.Unmarshal(data, &snapshot))
	assert.Equal(t, msgTexts(viewer.View()), msgTexts(snapshot.View()), "a snapshot keeps the edits")
}

func TestDurableResumesTheView(t *testing.T) {
	ctx := context.Background()
	store := session.NewMemoryStore()
	p := &script{responses: []ai.Message{
		largeCall("c1", 1000),
		text("done"),
	}}
	a, err := agent.Durable(ctx, model(p), "s1",
		agent.Store(store),
		agent.Toolset(echo),
		agent.Compaction(&fakeCompactor{limit: 500}),
	)
	require.NoError(t, err)
	_, _, err = collect(t, a.Run(ctx, session.Text("hi")))
	require.NoError(t, err)

	q := &script{responses: []ai.Message{text("again")}}
	c := &fakeCompactor{limit: 500}
	b, err := agent.Durable(ctx, model(q), "s1",
		agent.Store(store),
		agent.Toolset(echo),
		agent.Compaction(c),
	)
	require.NoError(t, err)
	assert.Equal(t, msgTexts(a.State().View()), msgTexts(b.State().View()))
	assert.Len(t, b.State().Messages(), len(a.State().Messages()))

	_, _, err = collect(t, b.Run(ctx, session.Text("more")))
	require.NoError(t, err)
	assert.Equal(t, []string{"hi", "summary", "done", "more"}, msgTexts(q.received()[0]))
	assert.Empty(t, c.inputs, "the usage before the compaction does not count")
}

func TestManualCompact(t *testing.T) {
	ctx := context.Background()
	p := &script{responses: []ai.Message{text("one")}}
	c := &fakeCompactor{limit: 1 << 30}
	a := agent.New(model(p), agent.Compaction(c))
	_, _, err := collect(t, a.Run(ctx, session.Text("hi"), session.Text("more")))
	require.NoError(t, err)

	events, msgs, err := collect(t, a.Compact(ctx, "keep the schema"))
	require.NoError(t, err)
	assert.Empty(t, msgs)
	assert.Equal(t, []string{
		"agent.EventAgentStart",
		"agent.EventCompactionStart",
		"agent.EventCompaction",
		"agent.EventAgentEnd",
	}, types(events))

	require.Len(t, c.inputs, 1)
	assert.Equal(t, "keep the schema", c.inputs[0].Instructions)
	assert.Equal(t, []string{"hi", "summary"}, msgTexts(a.State().View()))
	assert.False(t, a.State().Running())
}

func TestManualCompactFailures(t *testing.T) {
	ctx := context.Background()

	t.Run("no strategy", func(t *testing.T) {
		a := agent.New(model(&script{}))
		_, _, err := collect(t, a.Compact(ctx, ""))
		assert.EqualError(t, err, "agent: the agent has no compaction strategy")
	})

	t.Run("a run is active", func(t *testing.T) {
		gate := make(chan struct{})
		p := &script{responses: []ai.Message{text("x")}, gate: gate}
		a := agent.New(model(p), agent.Compaction(&fakeCompactor{limit: 500}))
		s := a.Run(ctx, session.Text("hi"))
		for evt := range s.Events() {
			if _, ok := evt.(agent.EventTurnStart); ok {
				break
			}
		}
		_, _, err := collect(t, a.Compact(ctx, ""))
		assert.EqualError(t, err, "agent: a run is active")
		close(gate)
		_, err = s.Wait()
		require.NoError(t, err)
	})

	t.Run("the strategy fails", func(t *testing.T) {
		c := &fakeCompactor{
			compact: func(agent.CompactInput) (agent.CompactResult, error) {
				return agent.CompactResult{}, errors.New("boom")
			},
		}
		a := agent.New(model(&script{}), agent.Compaction(c))
		events, _, err := collect(t, a.Compact(ctx, ""))
		assert.EqualError(t, err, "boom")
		assert.Equal(t, "agent.EventAgentEnd", types(events)[len(events)-1])
	})
}

// overflow is a provider that refuses the first calls because of the
// size, then answers from next.
type overflow struct {
	refuse int
	next   *script
	calls  int
}

func (o *overflow) StreamText(
	ctx context.Context,
	m ai.Model,
	msgs []ai.Message,
	opts ai.Options,
) *ai.EventStream {
	o.calls++
	if o.calls <= o.refuse {
		return ai.ErrStream(ai.ErrContextExceeded)
	}
	return o.next.StreamText(ctx, m, msgs, opts)
}

func TestOverflowCompactsAndCallsAgain(t *testing.T) {
	ctx := context.Background()

	t.Run("one refusal", func(t *testing.T) {
		next := &script{responses: []ai.Message{text("done")}}
		p := &overflow{refuse: 1, next: next}
		c := &fakeCompactor{limit: 1 << 30}
		a := agent.New(ai.LanguageModel{Provider: p}, agent.Compaction(c))

		_, _, err := collect(t, a.Run(ctx, session.Text("hi"), session.Text("more")))
		require.NoError(t, err)
		assert.Len(t, c.inputs, 1)
		assert.Equal(t, []string{"hi", "summary"}, msgTexts(next.received()[0]))
	})

	t.Run("two refusals fail the run", func(t *testing.T) {
		p := &overflow{refuse: 2, next: &script{}}
		a := agent.New(ai.LanguageModel{Provider: p}, agent.Compaction(&fakeCompactor{limit: 1 << 30}))

		_, _, err := collect(t, a.Run(ctx, session.Text("hi"), session.Text("more")))
		assert.ErrorIs(t, err, ai.ErrContextExceeded)
		assert.Equal(t, 2, p.calls)
	})

	t.Run("no strategy", func(t *testing.T) {
		p := &overflow{refuse: 1, next: &script{}}
		a := agent.New(ai.LanguageModel{Provider: p})

		_, _, err := collect(t, a.Run(ctx, session.Text("hi")))
		assert.ErrorIs(t, err, ai.ErrContextExceeded)
		assert.Equal(t, 1, p.calls)
	})
}

func TestCompactionEventJSON(t *testing.T) {
	events := []agent.Event{
		agent.EventCompactionStart{},
		agent.EventCompaction{Compaction: session.Compaction{
			Strategy: "s",
			Edits:    []session.Edit{{First: "a", Last: "b"}},
			Before:   3,
			After:    1,
		}},
		agent.EventCompaction{Error: "boom"},
		agent.EventMessageEnd{Message: ai.UserMessage("x"), EntryID: "id"},
	}
	for _, e := range events {
		data, err := json.Marshal(e)
		require.NoError(t, err)
		got, err := agent.UnmarshalEvent(data)
		require.NoError(t, err)
		assert.Equal(t, e, got)
	}
}
