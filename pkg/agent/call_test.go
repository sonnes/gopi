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

func TestRunProviderFailure(t *testing.T) {
	boom := errors.New("boom")
	p := &script{fail: boom}
	a := agent.New(model(p))

	events, msgs, err := collect(t, a.Run(context.Background(), ai.UserMessage("hi")))
	assert.ErrorIs(t, err, boom)
	emittest.CheckEvents(t, events)

	require.Len(t, msgs, 1, "the partial message is part of the result")
	assert.Equal(t, ai.StopReasonError, msgs[0].StopReason)
	assert.Equal(t, "boom", msgs[0].StopDetail)

	agentEnd := as[agent.EventAgentEnd](t, events[len(events)-1])
	assert.Equal(t, "boom", agentEnd.Error)
	assert.Equal(t, []string{"hi", "par"}, msgTexts(history(a)), "the history keeps the partial message")

	st := a.State()
	assert.False(t, st.Running())
	assert.Nil(t, st.Streaming(), "agent_end clears the partial message")
	assert.EqualError(t, st.Err(), "boom")
}

func TestRunCanceledModelCall(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	gate := make(chan struct{})
	p := &script{responses: []ai.Message{text("ok")}, gate: gate}
	a := agent.New(model(p))

	s := a.Run(ctx, ai.UserMessage("hi"))
	cancel()
	events, _, err := collect(t, s)
	assert.ErrorIs(t, err, context.Canceled)
	emittest.CheckEvents(t, events)
	assert.Equal(t, []string{"hi"}, msgTexts(history(a)), "a call with no events adds no message")
}

func TestRunDropsUnfinishedToolCalls(t *testing.T) {
	boom := errors.New("boom")
	p := &script{fail: boom, failWithToolStart: true}
	a := agent.New(model(p), agent.Toolset(echo))

	_, msgs, err := collect(t, a.Run(context.Background(), ai.UserMessage("hi")))
	assert.ErrorIs(t, err, boom)
	msgs = afterBase(t, msgs)

	require.Len(t, msgs, 1)
	assert.Empty(t, msgs[0].ToolCalls(), "a tool call with no ID did not finish")
	assert.Equal(t, "par", msgs[0].Text())
}

// fixed is a [ai.TextProvider] that sends events, then returns msg.
type fixed struct {
	events []ai.Event
	msg    *ai.Message
}

func (p fixed) StreamText(context.Context, ai.Model, []ai.Message, ai.Options) *ai.EventStream {
	return ai.NewEventStream(func(push func(ai.Event)) (*ai.Message, error) {
		for _, e := range p.events {
			push(e)
		}
		return p.msg, nil
	})
}

func TestRunMessageEvents(t *testing.T) {
	hello := text("hello")
	tests := []struct {
		name    string
		p       fixed
		opts    []agent.Option
		wantErr string
		want    []string
	}{
		{
			name: "a message with no model events still has a start",
			p:    fixed{msg: &hello},
			want: []string{"hi", "hello"},
		},
		{
			name: "a failed hook on a message with no model events closes the message",
			p:    fixed{msg: &hello},
			opts: []agent.Option{
				agent.AfterCall(func(_ context.Context, _ agent.State, _ agent.Request, m ai.Message) (ai.Message, error) {
					return m, errors.New("boom")
				}),
			},
			wantErr: "boom",
			want:    []string{"hi"},
		},
		{
			name:    "a stream that starts and returns no message ends the message",
			p:       fixed{events: []ai.Event{{Type: ai.EventStart}}},
			wantErr: "no message",
			want:    []string{"hi"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lm := ai.LanguageModel{Model: ai.Model{ID: "test"}, Provider: tt.p}
			a := agent.New(lm, tt.opts...)

			events, _, err := collect(t, a.Run(context.Background(), ai.UserMessage("hi")))
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
			}
			emittest.CheckEvents(t, events)
			assert.Equal(t, tt.want, historyTexts(a))
		})
	}
}
