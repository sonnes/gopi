package host_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/host"
	"github.com/sonnes/gopi/pkg/session"
)

// follow reads the agent stream of s until the test ends. It returns
// the state at the start, and a channel of the events.
func follow(t *testing.T, s *host.Session) (agent.State, <-chan agent.Event) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	state, events := s.Events(ctx)
	ch := make(chan agent.Event, 100)
	go func() {
		defer close(ch)
		for e, err := range events {
			if err != nil {
				return
			}
			ch <- e
		}
	}()
	return state, ch
}

// texts returns the text of each message.
func texts(msgs []ai.Message) []string {
	var out []string
	for _, m := range msgs {
		out = append(out, m.Text())
	}
	return out
}

func TestSessionEventsDuringRun(t *testing.T) {
	f, gate := gated(t)
	s := f.start(t, host.Spec{ID: "s1"})

	send(t, s, "a", host.Queue)
	f.model.waitCalls(t, 1)
	state, events := follow(t, s)
	close(gate)
	for {
		e := next(t, events, func(agent.Event) bool { return true })
		state.Apply(e)
		if is[agent.EventAgentEnd](e) {
			break
		}
	}

	// The log also has the system message of the tools.
	assert.Equal(t, texts(s.Log().Messages()), texts(state.Messages()))
	assert.Subset(t, texts(state.Messages()), []string{"a", "ok"})
	assert.False(t, state.Running())
}

func TestSessionEventsTwoReaders(t *testing.T) {
	f := newFixture(t)
	s := f.start(t, host.Spec{ID: "s1"})
	_, first := follow(t, s)
	_, second := follow(t, s)

	send(t, s, "a", host.Queue)

	var a, b []agent.Event
	next(t, first, func(e agent.Event) bool {
		a = append(a, e)
		return is[agent.EventAgentEnd](e)
	})
	next(t, second, func(e agent.Event) bool {
		b = append(b, e)
		return is[agent.EventAgentEnd](e)
	})
	assert.Equal(t, a, b)
	assert.True(t, is[agent.EventAgentStart](a[0]))
}

func TestSessionEventsResumed(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	log, err := session.Open(ctx, f.store, "s1")
	require.NoError(t, err)
	_, err = log.Append(ctx, session.Text("hello"))
	require.NoError(t, err)

	s := f.start(t, host.Spec{ID: "s1"})
	state, _ := s.Events(ctx)

	assert.Equal(t, []string{"hello"}, texts(state.Messages()))
}

func TestSessionEventsEndOnClose(t *testing.T) {
	f := newFixture(t)
	s := f.start(t, host.Spec{ID: "s1"})
	_, events := follow(t, s)

	require.NoError(t, s.Close())

	assert.Empty(t, drain(t, events))
}
