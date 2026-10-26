package host_test

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/host"
	"github.com/sonnes/gopi/pkg/session"
)

func TestSendSteersARun(t *testing.T) {
	f, gate := gated(t)
	f.create(t, host.Config{ID: "s1"})
	_, events := f.watch(t)

	f.send(t, "s1", "a")
	f.model.waitCalls(t, 1)
	f.send(t, "s1", "b")
	close(gate)

	assert.Equal(t, host.StopEndTurn, runEnd(t, events).StopReason)
	assert.Equal(t, []string{"a", "b"}, f.model.texts(), "one run reads both messages")
}

func TestSendHoldsAMessageForAnAgentThatCannotSteer(t *testing.T) {
	f, gate := gated(t)
	f.driver.plain = true
	f.create(t, host.Config{ID: "s1"})
	_, events := f.watch(t)

	f.send(t, "s1", "a")
	f.model.waitCalls(t, 1)
	f.send(t, "s1", "b")
	close(gate)

	runEnd(t, events)
	runEnd(t, events)
	assert.Equal(t, []string{"a", "b"}, f.model.texts())
}

func TestSendAnEmptyMessage(t *testing.T) {
	f := newFixture(t)
	f.create(t, host.Config{ID: "s1"})

	err := f.h.Send(context.Background(), "s1", nil)

	assert.ErrorIs(t, err, host.ErrInvalid)
}

func TestInterruptKeepsTheHeldMessages(t *testing.T) {
	f, gate := gated(t)
	f.driver.plain = true
	f.create(t, host.Config{ID: "s1"})
	_, events := f.watch(t)
	f.send(t, "s1", "a")
	f.model.waitCalls(t, 1)
	f.send(t, "s1", "b")

	require.NoError(t, f.h.Interrupt(context.Background(), "s1"))

	assert.Equal(t, host.StopInterrupted, runEnd(t, events).StopReason)
	close(gate)
	assert.Equal(t, host.StopEndTurn, runEnd(t, events).StopReason)
	assert.Equal(t, []string{"a", "b"}, f.model.texts())
}

func TestAnUnreadSteerRunsNext(t *testing.T) {
	f, gate := gated(t)
	f.create(t, host.Config{ID: "s1"})
	_, events := f.watch(t)
	f.send(t, "s1", "a")
	f.model.waitCalls(t, 1)
	f.send(t, "s1", "b")

	require.NoError(t, f.h.Interrupt(context.Background(), "s1"))

	assert.Equal(t, host.StopInterrupted, runEnd(t, events).StopReason)
	close(gate)
	assert.Equal(t, host.StopEndTurn, runEnd(t, events).StopReason)
	assert.Equal(t, []string{"a", "b"}, f.model.texts(), "the steer was meant for the work")
}

func TestSendWakesAStoredSession(t *testing.T) {
	f := newFixture(t)
	f.create(t, host.Config{
		ID:  "s1",
		Dir: "/tmp",
	})
	_, events := f.watch(t)
	f.send(t, "s1", "a")
	runEnd(t, events)

	f = f.restart(t)
	_, events = f.watch(t)
	f.send(t, "s1", "b")
	runEnd(t, events)

	c := f.lastConfig(t)
	assert.Equal(t, 2, f.driver.starts())
	assert.Equal(t, "/tmp", c.Dir, "the agent starts from the config of the session")
	f.model.mu.Lock()
	last := f.model.calls[len(f.model.calls)-1]
	f.model.mu.Unlock()
	assert.Subset(t, texts(last), []string{"a", "ok", "b"}, "the driver resumes the log")
}

func TestTwoMessagesStartOneAgent(t *testing.T) {
	f := newFixture(t)
	f.create(t, host.Config{ID: "s1"})
	f = f.restart(t)

	var wg sync.WaitGroup
	for _, text := range []string{"a", "b"} {
		wg.Go(func() { f.send(t, "s1", text) })
	}
	wg.Wait()

	assert.Equal(t, 2, f.driver.starts(), "one start at Create, and one after the restart")
}

func TestLogOfAStoredSession(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	title := session.Value[string]{Namespace: "app", Key: "title"}
	f.create(t, host.Config{ID: "s1"})
	_, events := f.watch(t)
	f.send(t, "s1", "a")
	runEnd(t, events)
	f = f.restart(t)

	log, err := f.h.Log(ctx, "s1")
	require.NoError(t, err)
	assert.Contains(t, texts(log.Messages()), "a")
	require.NoError(t, title.Set(ctx, log, "first"))
	f.send(t, "s1", "b")

	log, err = f.h.Log(ctx, "s1")
	require.NoError(t, err)
	got, ok, err := title.Get(log)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "first", got, "the agent reads the value that the store has")
}

// texts returns the text of each message.
func texts(msgs []ai.Message) []string {
	var out []string
	for _, m := range msgs {
		out = append(out, m.Text())
	}
	return out
}
