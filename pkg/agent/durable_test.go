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
	"github.com/sonnes/gopi/pkg/session"
	"github.com/sonnes/gopi/pkg/tool"
)

// entryTexts returns the text of each message entry, and the kind of
// each custom entry.
func entryTexts(entries []session.Entry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.Kind != "" {
			out = append(out, e.Kind)
			continue
		}
		out = append(out, e.Message.Text())
	}
	return out
}

func TestDurableResumes(t *testing.T) {
	ctx := context.Background()
	store := session.NewMemoryStore()

	p := &script{responses: []ai.Message{text("one")}}
	a, err := agent.Durable(ctx, model(p), "s1", agent.Store(store))
	require.NoError(t, err)
	_, _, err = collect(t, a.Run(ctx, session.Text("first")))
	require.NoError(t, err)

	q := &script{responses: []ai.Message{text("two")}}
	b, err := agent.Durable(ctx, model(q), "s1", agent.Store(store))
	require.NoError(t, err)
	assert.Equal(t, []string{"first", "one"}, msgTexts(history(b)), "the state starts from the log")

	_, msgs, err := collect(t, b.Run(ctx, session.Text("second")))
	require.NoError(t, err)
	assert.Equal(t, []string{"first", "one", "second"}, msgTexts(q.received()[0]))
	assert.Equal(t, []string{"two"}, msgTexts(msgs), "the result has no history and no input")
	assert.Equal(t, []string{"first", "one", "second", "two"}, entryTexts(b.Log().Entries()))
}

func TestDurableRequiresID(t *testing.T) {
	_, err := agent.Durable(context.Background(), model(&script{}), "")
	assert.Error(t, err)
}

func TestDurableDuplicateToolName(t *testing.T) {
	other := tool.New("echo", func(_ context.Context, in echoInput) (string, error) {
		return in.S, nil
	}, tool.Description("another echo"))
	store := session.NewMemoryStore()

	_, err := agent.Durable(context.Background(), model(&script{}), "s1",
		agent.Store(store),
		agent.Toolset(echo, other),
	)
	require.EqualError(t, err, `agent: two tools are named "echo"`)

	_, err = store.Load(context.Background(), "s1")
	assert.ErrorIs(t, err, session.ErrNotFound, "the check comes before the store")
}

func TestMessageEndComesAfterTheStore(t *testing.T) {
	p := &script{responses: []ai.Message{
		toolCall("c1", "echo", `{"s":"x"}`),
		text("done"),
	}}
	a := agent.New(model(p), agent.Toolset(echo))

	s := a.Run(context.Background(), session.Text("hi"))
	ends := 0
	for evt, err := range s.Events() {
		require.NoError(t, err)
		end, ok := evt.(agent.EventMessageEnd)
		if !ok {
			continue
		}
		ends++
		assert.Contains(t, a.Log().Messages(), end.Message, "the log holds the message of message_end")
	}
	_, err := s.Wait()
	require.NoError(t, err)
	assert.Equal(t, 5, ends, "base, input, call, result, answer")
}

func TestEphemeralEntry(t *testing.T) {
	ctx := context.Background()
	p := &script{responses: []ai.Message{text("one"), text("two")}}
	a := agent.New(model(p))

	_, _, err := collect(t, a.Run(ctx,
		session.Text("hi"),
		session.Text("the time is 9:00", session.Ephemeral()),
	))
	require.NoError(t, err)
	assert.Equal(t, []string{"hi", "the time is 9:00"}, msgTexts(p.received()[0]), "the model reads it in its run")
	assert.Equal(t, []string{"hi", "one"}, entryTexts(a.Log().Entries()), "the log does not keep it")

	_, _, err = collect(t, a.Run(ctx, session.Text("again")))
	require.NoError(t, err)
	assert.Equal(t, []string{"hi", "one", "again"}, msgTexts(p.received()[1]), "the next run does not read it")
}

func TestName(t *testing.T) {
	p := &script{responses: []ai.Message{
		toolCall("c1", "echo", `{"s":"x"}`),
		text("done"),
	}}
	a := agent.New(model(p), agent.Toolset(echo), agent.Name("helper"))

	_, _, err := collect(t, a.Run(context.Background(), session.Text("hi", session.Author("user-1"))))
	require.NoError(t, err)

	var authors []string
	for _, e := range a.Log().Entries() {
		authors = append(authors, string(e.Message.Role)+":"+e.Author)
	}
	assert.Equal(t, []string{
		"system:",
		"user:user-1",
		"assistant:helper",
		"tool_result:helper",
		"assistant:helper",
	}, authors)
}

// failingStore fails each append after the first n.
type failingStore struct {
	*session.MemoryStore
	n int
}

var errDisk = errors.New("disk full")

func (s *failingStore) Append(ctx context.Context, id string, entries ...session.Entry) error {
	if s.n == 0 {
		return errDisk
	}
	s.n--
	return s.MemoryStore.Append(ctx, id, entries...)
}

func TestStoreFailureFailsTheRun(t *testing.T) {
	ctx := context.Background()
	store := &failingStore{MemoryStore: session.NewMemoryStore(), n: 1}
	p := &script{responses: []ai.Message{text("one")}}
	a, err := agent.Durable(ctx, model(p), "s1", agent.Store(store))
	require.NoError(t, err)

	events, _, err := collect(t, a.Run(ctx, session.Text("hi")))
	require.ErrorIs(t, err, errDisk)
	emittest.CheckEvents(t, events)

	end := as[agent.EventAgentEnd](t, events[len(events)-1])
	assert.Contains(t, end.Error, "disk full")
	assert.Equal(t, []string{"hi"}, entryTexts(a.Log().Entries()))
	assert.Equal(t, []string{"hi"}, msgTexts(history(a)), "the state has only what the store has")
}

func TestParallelResultIsStoredWhenItsCallEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	slow := tool.Func("slow", func(ctx context.Context, _ ai.ToolCall) (ai.ToolResult, error) {
		<-ctx.Done()
		return ai.ToolResult{}, ctx.Err()
	})
	fast := tool.Func("fast", func(context.Context, ai.ToolCall) (ai.ToolResult, error) {
		return textResult("fast"), nil
	})
	store := session.NewMemoryStore()
	p := &script{responses: []ai.Message{toolCalls("slow", "fast")}}
	a, err := agent.Durable(ctx, model(p), "s1",
		agent.Store(store),
		agent.Toolset(slow, fast),
	)
	require.NoError(t, err)

	s := a.Run(ctx, session.Text("hi"))
	for evt := range s.Events() {
		if end, ok := evt.(agent.EventMessageEnd); ok && end.Message.Role == ai.RoleToolResult {
			cancel()
		}
	}
	_, err = s.Wait()
	require.ErrorIs(t, err, context.Canceled)

	b, err := agent.Durable(context.Background(), model(&script{}), "s1",
		agent.Store(store),
		agent.Toolset(slow, fast),
	)
	require.NoError(t, err)
	results := toolResults(history(b))
	require.Len(t, results, 2)
	assert.Equal(t, "c2", results[0].ToolCallID, "the fast call ended first")
	assert.Equal(t, "fast", results[0].Text())
	assert.Equal(t, "c1", results[1].ToolCallID)
	assert.True(t, results[1].IsError, "the log repairs the call that did not end")
}

func TestDurableReloadsSearchedTools(t *testing.T) {
	ctx := context.Background()
	store := session.NewMemoryStore()

	// The store takes the base, the input, the call, and the search
	// result. Then it fails, before it stores the change that declares
	// the tool.
	const stored = 4
	p := &script{responses: []ai.Message{
		toolCall("c1", "tool_search", `{"query":"select:get_weather"}`),
	}}
	a, err := agent.Durable(ctx, model(p), "s1",
		agent.Store(&failingStore{MemoryStore: store, n: stored}),
		agent.Toolset(echo, weather),
	)
	require.NoError(t, err)
	_, _, err = collect(t, a.Run(ctx, session.Text("hi")))
	require.ErrorIs(t, err, errDisk)

	q := &script{responses: []ai.Message{text("done")}}
	b, err := agent.Durable(ctx, model(q), "s1",
		agent.Store(store),
		agent.Toolset(echo, weather),
	)
	require.NoError(t, err)
	_, _, err = collect(t, b.Run(ctx))
	require.NoError(t, err)

	assert.Contains(t, names(b.State().Tools()), "get_weather", "the resumed agent declares the tool")
}

func TestHookSetsAValue(t *testing.T) {
	ctx := context.Background()
	denied := session.Value[int]{Namespace: "guard", Key: "denied"}

	p := &script{responses: []ai.Message{
		toolCall("c1", "echo", `{"s":"x"}`),
		text("done"),
	}}
	a := agent.New(model(p),
		agent.Toolset(echo),
		agent.BeforeTool(func(ctx context.Context, s agent.State, c ai.ToolCall) (ai.ToolCall, error) {
			n, _, err := denied.Get(s.Log())
			if err != nil {
				return c, err
			}
			if err := denied.Set(ctx, s.Log(), n+1); err != nil {
				return c, err
			}
			return c, agent.Deny("no")
		}),
	)

	_, _, err := collect(t, a.Run(ctx, session.Text("hi")))
	require.NoError(t, err)

	n, ok, err := denied.Get(a.Log())
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, 1, n)
}

func TestAfterTurnStoresACustomEntry(t *testing.T) {
	ctx := context.Background()
	p := &script{responses: []ai.Message{text("one"), text("two")}}
	a := agent.New(model(p),
		agent.AfterTurn(func(_ context.Context, s agent.State) ([]session.Entry, error) {
			e, err := session.Custom("compaction", map[string]int{"turn": s.Turn()})
			return []session.Entry{e}, err
		}),
	)

	events, _, err := collect(t, a.Run(ctx, session.Text("hi")))
	require.NoError(t, err)
	emittest.CheckEvents(t, events)
	for _, e := range events {
		if end, ok := e.(agent.EventMessageEnd); ok {
			assert.NotEmpty(t, end.Message.Role, "a custom entry sends no event")
		}
	}
	assert.Equal(t, []string{"hi", "one", "compaction"}, entryTexts(a.Log().Entries()))

	_, _, err = collect(t, a.Run(ctx, session.Text("again")))
	require.NoError(t, err)
	assert.Equal(t, []string{"hi", "one", "again"}, msgTexts(p.received()[1]), "the model does not read it")
}

func TestNextRunAnswersAnInterruptedCall(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	stop := tool.Func("stop", func(context.Context, ai.ToolCall) (ai.ToolResult, error) {
		cancel()
		return ai.ToolResult{}, context.Canceled
	})
	p := &script{responses: []ai.Message{
		toolCalls("stop"),
		text("done"),
	}}
	a := agent.New(model(p), agent.Toolset(stop))
	_, _, err := collect(t, a.Run(ctx, session.Text("hi")))
	require.ErrorIs(t, err, context.Canceled)

	events, msgs, err := collect(t, a.Run(context.Background(), session.Text("again")))
	require.NoError(t, err)
	emittest.CheckEvents(t, events)
	require.NotEmpty(t, msgs)
	assert.Equal(t, "c1", msgs[0].ToolCallID, "the result of the run has the repair")

	sent := p.received()[1]
	require.Len(t, sent, 4)
	assert.Equal(t, "c1", sent[2].ToolCallID)
	assert.True(t, sent[2].IsError, "the call gets its error result before the input")
	assert.Equal(t, "again", sent[3].Text())
	assert.Equal(t, sent, afterBase(t, a.Log().Messages())[:4], "the log stores the result")
}

func TestFailedRunReturnsTheStoredRepair(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	stop := tool.Func("stop", func(context.Context, ai.ToolCall) (ai.ToolResult, error) {
		cancel()
		return ai.ToolResult{}, context.Canceled
	})
	calls := 0
	system := func(context.Context) ([]ai.Section, error) {
		calls++
		if calls > 1 {
			return nil, errors.New("no prompt")
		}
		return []ai.Section{{Name: "base", Text: "x"}}, nil
	}
	p := &script{responses: []ai.Message{toolCalls("stop")}}
	a := agent.New(model(p), agent.Toolset(stop), agent.System(system))
	_, _, err := collect(t, a.Run(ctx, session.Text("hi")))
	require.ErrorIs(t, err, context.Canceled)

	_, msgs, err := collect(t, a.Run(context.Background(), session.Text("again")))
	require.Error(t, err)
	require.Len(t, msgs, 1, "the result has each message that the log stored")
	assert.Equal(t, "c1", msgs[0].ToolCallID)
}
