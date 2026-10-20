package emit_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/agent/internal/emit"
	"github.com/sonnes/gopi/pkg/agent/internal/emittest"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/session"
)

// recorder collects the events that an Emitter sends.
type recorder struct {
	events []agent.Event
}

func (r *recorder) push(ev agent.Event) {
	r.events = append(r.events, ev)
}

// types returns the type names of the events.
func (r *recorder) types() []string { return emittest.Types(r.events) }

// newEmitter returns an Emitter over log and a new state, with the
// events going to r.
func newEmitter(log *session.Log, r *recorder) (*emit.Emitter, *emit.State) {
	st := emit.NewState(log)
	return emit.New(log, "codex", st, r.push), st
}

func TestEmitterToolTurns(t *testing.T) {
	ctx := context.Background()
	log := session.New()
	var r recorder
	e, st := newEmitter(log, &r)

	require.NoError(t, e.Start())
	require.NoError(t, e.Input(ctx, []session.Entry{session.Text("list files")}))

	call := ai.ToolCall{
		ID:    "c1",
		Name:  "bash",
		Input: json.RawMessage(`{"command":"ls"}`),
	}
	require.NoError(t, e.End(ctx, ai.Message{
		Content:    []ai.Content{call},
		StopReason: ai.StopReasonToolUse,
	}))
	e.ToolStart(call)

	ok, err := e.ToolEnd(ctx, "c1", ai.ToolResult{Content: []ai.Content{ai.Text{Text: "a.go"}}})
	require.NoError(t, err)
	assert.True(t, ok)

	e.Update(ai.Event{Type: ai.EventTextDelta, Delta: "one "})
	e.Update(ai.Event{Type: ai.EventTextDelta, Delta: "file"})
	assert.Equal(t, []ai.Content{ai.Text{Text: "one file"}}, e.Partial().Content)
	require.NoError(t, e.End(ctx, ai.Message{
		Content:    []ai.Content{ai.Text{Text: "one file"}},
		StopReason: ai.StopReasonStop,
	}))

	msgs, err := e.Finish(ctx, ai.Usage{Input: 3, Output: 2}, nil)
	require.NoError(t, err)

	assert.Equal(t, []string{
		"agent_start",
		"turn_start",
		"message_start", // input
		"message_end",
		"message_start", // tool call
		"message_end",
		"tool_execution_start",
		"tool_execution_end",
		"message_start", // tool result
		"message_end",
		"turn_end",
		"turn_start",
		"message_start",
		"message_update",
		"message_update",
		"message_end",
		"turn_end",
		"agent_end",
	}, r.types())
	emittest.CheckEvents(t, r.events)

	assert.Equal(t, agent.EventTurnStart{Turn: 1}, r.events[1])
	assert.Equal(t, agent.EventTurnStart{Turn: 2}, r.events[11])
	turnEnd, ok := r.events[10].(agent.EventTurnEnd)
	require.True(t, ok)
	require.NotNil(t, turnEnd.Message)
	assert.Len(t, turnEnd.ToolResults, 1)
	assert.Equal(t, agent.EventAgentEnd{Usage: ai.Usage{Input: 3, Output: 2}}, r.events[len(r.events)-1])

	require.Len(t, msgs, 3, "the result has no input")
	assert.Equal(t, ai.RoleAssistant, msgs[0].Role)
	assert.Equal(t, ai.RoleToolResult, msgs[1].Role)
	assert.Equal(t, "one file", msgs[2].Text())

	entries := log.Entries()
	require.Len(t, entries, 4, "the input and the three messages are stored")
	assert.Equal(t, "codex", entries[1].Author)

	state := st.Get()
	assert.False(t, state.Running())
	assert.Equal(t, log.Messages(), state.Messages(), "the state has the messages of the log")
}

func TestEmitterStartFailsWhileRunning(t *testing.T) {
	log := session.New()
	var first, second recorder
	st := emit.NewState(session.New())
	a := emit.New(log, "", st, first.push)
	b := emit.New(log, "", st, second.push)

	require.NoError(t, a.Start())
	require.Error(t, b.Start())
	assert.Empty(t, second.events, "a run that cannot start sends no event")

	_, err := a.Finish(context.Background(), ai.Usage{}, nil)
	require.NoError(t, err)
	require.NoError(t, b.Start(), "the end of the first run frees the state")
}

func TestEmitterFinishWithError(t *testing.T) {
	ctx := context.Background()
	log := session.New()
	var r recorder
	e, st := newEmitter(log, &r)

	require.NoError(t, e.Start())
	require.NoError(t, e.Input(ctx, []session.Entry{session.Text("hi")}))
	call := ai.ToolCall{
		ID:   "c1",
		Name: "bash",
	}
	require.NoError(t, e.End(ctx, ai.Message{Content: []ai.Content{call}}))
	e.ToolStart(call)

	cause := errors.New("the CLI exited")
	_, err := e.Finish(ctx, ai.Usage{}, cause)
	require.ErrorIs(t, err, cause)

	emittest.CheckEvents(t, r.events)
	assert.Equal(t, agent.EventAgentEnd{Error: "the CLI exited"}, r.events[len(r.events)-1])

	toolEnd, ok := r.events[len(r.events)-5].(agent.EventToolExecutionEnd)
	require.True(t, ok, "the open call ends: %T", r.events[len(r.events)-5])
	assert.True(t, toolEnd.Result.IsError)

	msgs := log.Messages()
	require.Len(t, msgs, 3, "the input, the call, and its interrupted result are stored")
	assert.Equal(t, session.Interrupted("c1"), msgs[2])
	assert.Equal(t, log.Messages(), st.Get().Messages())
	assert.EqualError(t, st.Get().Err(), "the CLI exited")
}

func TestEmitterFinishDropsPartialMessage(t *testing.T) {
	ctx := context.Background()
	log := session.New()
	var r recorder
	e, st := newEmitter(log, &r)

	require.NoError(t, e.Start())
	require.NoError(t, e.Input(ctx, []session.Entry{session.Text("hi")}))
	e.Update(ai.Event{Type: ai.EventTextDelta, Delta: "par"})
	_, err := e.Finish(ctx, ai.Usage{}, context.Canceled)
	require.ErrorIs(t, err, context.Canceled)

	emittest.CheckEvents(t, r.events)
	end, ok := r.events[len(r.events)-3].(agent.EventMessageEnd)
	require.True(t, ok)
	assert.Empty(t, end.Message.Content, "the message_end of a dropped message has no content")
	assert.Len(t, log.Entries(), 1, "a partial message is not stored")
	assert.Len(t, st.Get().Messages(), 1)
}

func TestEmitterInputKinds(t *testing.T) {
	ctx := context.Background()
	log := session.New()
	var r recorder
	e, st := newEmitter(log, &r)

	custom, err := session.Custom("note", map[string]string{"k": "v"})
	require.NoError(t, err)
	reminder := session.Text("be brief", session.Ephemeral())

	require.NoError(t, e.Start())
	require.NoError(t, e.Input(ctx, []session.Entry{
		session.Text("hi"),
		custom,
		reminder,
	}))
	assert.Len(t, st.Get().Messages(), 2, "the ephemeral message is in the state during the run")

	_, err = e.Finish(ctx, ai.Usage{}, nil)
	require.NoError(t, err)

	assert.Equal(t, []string{
		"agent_start",
		"turn_start",
		"message_start", // hi
		"message_end",
		"message_start", // be brief
		"message_end",
		"turn_end",
		"agent_end",
	}, r.types(), "a custom entry gets no event")
	end, ok := r.events[5].(agent.EventMessageEnd)
	require.True(t, ok)
	assert.True(t, end.Ephemeral)

	entries := log.Entries()
	require.Len(t, entries, 2, "the log keeps the text and the custom entry")
	assert.Equal(t, "note", entries[1].Kind)
	assert.Len(t, st.Get().Messages(), 1, "agent_end removes the ephemeral message")
}

func TestEmitterToolEndWithoutStart(t *testing.T) {
	ctx := context.Background()
	var r recorder
	e, _ := newEmitter(session.New(), &r)
	require.NoError(t, e.Start())

	ok, err := e.ToolEnd(ctx, "nope", ai.ToolResult{})
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Equal(t, []string{"agent_start"}, r.types())
}

func TestEmitterDrop(t *testing.T) {
	ctx := context.Background()
	log := session.New()
	var r recorder
	e, _ := newEmitter(log, &r)

	require.NoError(t, e.Start())
	require.NoError(t, e.Input(ctx, []session.Entry{session.Text("hi")}))
	e.Begin()
	e.Drop()
	e.Update(ai.Event{Type: ai.EventTextDelta, Delta: "ok"})
	require.NoError(t, e.End(ctx, ai.Message{Content: []ai.Content{ai.Text{Text: "ok"}}}))
	_, err := e.Finish(ctx, ai.Usage{}, nil)
	require.NoError(t, err)

	assert.Equal(t, []string{
		"agent_start",
		"turn_start",
		"message_start",
		"message_end",
		"message_start",
		"message_end",
		"message_start",
		"message_update",
		"message_end",
		"turn_end",
		"agent_end",
	}, r.types())
	emittest.CheckEvents(t, r.events)
	assert.Len(t, log.Entries(), 2, "a dropped message is not stored")
}

func TestNewStateHasTheMessages(t *testing.T) {
	log := session.New()
	msgs := []ai.Message{
		ai.UserMessage("one"),
		ai.UserMessage("two"),
	}
	for _, m := range msgs {
		_, err := log.Append(t.Context(), session.NewEntry(m))
		require.NoError(t, err)
	}
	st := emit.NewState(log)
	assert.Equal(t, msgs, st.Get().Messages())
	assert.False(t, st.Get().Running())
}

// A hook of a CLI agent reads the session through the log of its state,
// as a hook of the harness does.
func TestNewStateHasTheLog(t *testing.T) {
	log := session.New()
	st := emit.NewState(log)
	got := st.Get().Log()
	require.NotNil(t, got)

	note := session.Value[string]{Namespace: "test.note"}
	require.NoError(t, note.Set(t.Context(), got, "kept"))
	v, ok, err := note.Get(log)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "kept", v)
}

func TestInput(t *testing.T) {
	custom, err := session.Custom("note", 1)
	require.NoError(t, err)
	png := ai.File{
		MediaType: "image/png",
		Data:      []byte("png"),
	}
	got := emit.Input([]session.Entry{
		session.Text("one"),
		custom,
		session.Text("two"),
		session.NewEntry(ai.Message{
			Role:    ai.RoleUser,
			Content: []ai.Content{png, ai.Text{Text: "three"}},
		}),
	})
	want := []ai.Content{
		ai.Text{Text: "one\n\ntwo"},
		png,
		ai.Text{Text: "three"},
	}
	assert.Equal(t, want, got, "adjacent text joins, and the order stays")
}
