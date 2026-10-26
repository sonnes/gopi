package host_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/host"
)

func TestStreamDuringARun(t *testing.T) {
	f, gate := gated(t)
	f.create(t, host.Config{ID: "s1"})

	f.send(t, "s1", "a")
	f.model.waitCalls(t, 1)
	state, events := f.follow(t, "s1")
	close(gate)
	for {
		e := next(t, events, func(agent.Event) bool { return true })
		state.Apply(e)
		if is[agent.EventAgentEnd](e) {
			break
		}
	}

	log, err := f.h.Log(t.Context(), "s1")
	require.NoError(t, err)
	// The log also has the system message of the tools.
	assert.Equal(t, texts(log.Messages()), texts(state.Messages()))
	assert.Subset(t, texts(state.Messages()), []string{"a", "ok"})
	assert.False(t, state.Running())
}

func TestStreamTwoReaders(t *testing.T) {
	f := newFixture(t)
	f.create(t, host.Config{ID: "s1"})
	_, first := f.follow(t, "s1")
	_, second := f.follow(t, "s1")

	f.send(t, "s1", "a")

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

func TestStreamOfASessionWithNoAgent(t *testing.T) {
	f := newFixture(t)
	f.create(t, host.Config{ID: "s1"})
	_, events := f.watch(t)
	f.send(t, "s1", "a")
	runEnd(t, events)
	f = f.restart(t)

	_, agentEvents := f.follow(t, "s1")
	f.send(t, "s1", "b")

	next(t, agentEvents, is[agent.EventAgentStart])
	next(t, agentEvents, is[agent.EventAgentEnd])
	state, _, err := f.h.Stream(t.Context(), "s1")
	require.NoError(t, err)
	assert.Subset(t, texts(state.Messages()), []string{"a", "ok", "b"}, "a reader that joins later gets the log")
}

func TestStreamEndsOnClose(t *testing.T) {
	f := newFixture(t)
	f.create(t, host.Config{ID: "s1"})
	_, events := f.follow(t, "s1")

	require.NoError(t, f.h.Close())

	assert.Empty(t, drain(t, events))
}
