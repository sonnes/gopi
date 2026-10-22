package host_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/host"
)

// send sends the text with the policy p, and fails the test on an
// error.
func send(t *testing.T, s *host.Session, text string, p host.Policy) {
	t.Helper()
	require.NoError(t, s.Send(context.Background(), host.Text(text), p))
}

// gated returns a fixture whose model waits on a gate. close(gate) lets
// each call go on.
func gated(t *testing.T) (fixture, chan struct{}) {
	t.Helper()
	f := newFixture(t)
	gate := make(chan struct{})
	f.model.gate = gate
	return f, gate
}

// inputOf matches an input event with the policy p.
func inputOf(p host.Policy) func(host.Event) bool {
	return func(e host.Event) bool {
		in, ok := e.(host.EventInput)
		return ok && in.Policy == p
	}
}

// runEnd returns the next run_end of events.
func runEnd(t *testing.T, events <-chan host.Event) host.EventRunEnd {
	t.Helper()
	return next(t, events, is[host.EventRunEnd]).(host.EventRunEnd)
}

func TestSendQueue(t *testing.T) {
	f, gate := gated(t)
	s := f.start(t, host.Spec{ID: "s1"})
	_, events := f.watch(t)

	send(t, s, "a", host.Queue)
	send(t, s, "b", "")
	close(gate)

	assert.Empty(t, runEnd(t, events).Error)
	assert.Empty(t, runEnd(t, events).Error)
	assert.Equal(t, []string{"a", "b"}, f.model.texts())
}

func TestSendEvents(t *testing.T) {
	f := newFixture(t)
	s := f.start(t, host.Spec{ID: "s1"})
	_, events := f.watch(t)

	send(t, s, "a", host.Queue)

	got := []host.Event{
		next(t, events, is[host.EventInput]),
		next(t, events, is[host.EventRunStart]),
	}
	end := runEnd(t, events)
	assert.Equal(t, host.EventInput{
		Session: "s1",
		Entries: host.Text("a"),
		Policy:  host.Queue,
	}, got[0])
	assert.Equal(t, host.EventRunStart{Session: "s1"}, got[1])
	assert.Equal(t, 10, end.Usage.Input)
}

func TestSendSteer(t *testing.T) {
	f, gate := gated(t)
	s := f.start(t, host.Spec{ID: "s1"})
	_, events := f.watch(t)

	send(t, s, "a", host.Queue)
	next(t, events, is[host.EventRunStart])
	f.model.waitCalls(t, 1)
	send(t, s, "b", host.Steer)
	next(t, events, inputOf(host.Steer))
	close(gate)

	assert.Empty(t, runEnd(t, events).Error)
	assert.Equal(t, []string{"a", "b"}, f.model.texts())
	state, _ := f.h.Events(context.Background())
	assert.Equal(t, 0, state.Sessions["s1"].Queued)
}

func TestSendSteerWithoutSteerer(t *testing.T) {
	f, gate := gated(t)
	f.driver.plain = true
	s := f.start(t, host.Spec{ID: "s1"})
	_, events := f.watch(t)

	send(t, s, "a", host.Queue)
	next(t, events, is[host.EventRunStart])
	f.model.waitCalls(t, 1)
	send(t, s, "b", host.Steer)
	next(t, events, inputOf(host.Queue))
	close(gate)

	runEnd(t, events)
	runEnd(t, events)
	assert.Equal(t, []string{"a", "b"}, f.model.texts())
}

func TestSendInterrupt(t *testing.T) {
	f, gate := gated(t)
	s := f.start(t, host.Spec{ID: "s1"})
	_, events := f.watch(t)

	send(t, s, "a", host.Queue)
	send(t, s, "c", host.Queue)
	next(t, events, is[host.EventRunStart])
	f.model.waitCalls(t, 1)
	send(t, s, "b", host.Interrupt)

	assert.Equal(t, host.ErrInterrupted.Error(), runEnd(t, events).Error)
	close(gate)
	runEnd(t, events)
	runEnd(t, events)
	assert.Equal(t, []string{"a", "b", "c"}, f.model.texts())
}

func TestSendReject(t *testing.T) {
	f, gate := gated(t)
	s := f.start(t, host.Spec{ID: "s1"})
	_, events := f.watch(t)

	send(t, s, "a", host.Reject)
	next(t, events, is[host.EventRunStart])
	err := s.Send(context.Background(), host.Text("b"), host.Reject)
	close(gate)

	assert.ErrorIs(t, err, host.ErrBusy)
	runEnd(t, events)
	require.NoError(t, s.Close())
	for _, e := range drainUntilEnd(t, events) {
		assert.NotEqual(t, host.EventInput{
			Session: "s1",
			Entries: host.Text("b"),
			Policy:  host.Queue,
		}, e)
	}
	assert.Equal(t, []string{"a"}, f.model.texts())
}

func TestSendInvalid(t *testing.T) {
	f := newFixture(t)
	s := f.start(t, host.Spec{ID: "s1"})
	ctx := context.Background()

	err := s.Send(ctx, nil, host.Queue)
	assert.ErrorIs(t, err, host.ErrInvalid)
	err = s.Send(ctx, host.Text("a"), "later")
	assert.ErrorIs(t, err, host.ErrInvalid)
}

func TestSendAfterClose(t *testing.T) {
	f := newFixture(t)
	s := f.start(t, host.Spec{ID: "s1"})
	require.NoError(t, s.Close())

	err := s.Send(context.Background(), host.Text("a"), host.Queue)

	assert.ErrorIs(t, err, host.ErrClosed)
}

func TestInterruptKeepsQueue(t *testing.T) {
	f, gate := gated(t)
	s := f.start(t, host.Spec{ID: "s1"})
	_, events := f.watch(t)

	send(t, s, "a", host.Queue)
	send(t, s, "b", host.Queue)
	next(t, events, is[host.EventRunStart])
	f.model.waitCalls(t, 1)
	s.Interrupt()

	assert.Equal(t, host.ErrInterrupted.Error(), runEnd(t, events).Error)
	close(gate)
	assert.Empty(t, runEnd(t, events).Error)
	assert.Equal(t, []string{"a", "b"}, f.model.texts())
}

func TestUnreadSteerRunsNext(t *testing.T) {
	f, gate := gated(t)
	s := f.start(t, host.Spec{ID: "s1"})
	_, events := f.watch(t)

	send(t, s, "a", host.Queue)
	next(t, events, is[host.EventRunStart])
	f.model.waitCalls(t, 1)
	send(t, s, "b", host.Steer)
	next(t, events, inputOf(host.Steer))
	s.Interrupt()

	runEnd(t, events)
	requeued := next(t, events, inputOf(host.Queue)).(host.EventInput)
	assert.Equal(t, host.Text("b"), requeued.Entries)
	close(gate)
	assert.Empty(t, runEnd(t, events).Error)
	assert.Equal(t, []string{"a", "b"}, f.model.texts())
	state, _ := f.h.Events(context.Background())
	assert.Equal(t, 0, state.Sessions["s1"].Queued)
	assert.Equal(t, host.StatusIdle, state.Sessions["s1"].Status())
}

func TestCloseEndsRun(t *testing.T) {
	f, _ := gated(t)
	s := f.start(t, host.Spec{ID: "s1"})
	_, events := f.watch(t)

	send(t, s, "a", host.Queue)
	send(t, s, "b", host.Queue)
	next(t, events, is[host.EventRunStart])
	f.model.waitCalls(t, 1)
	require.NoError(t, s.Close())

	assert.Equal(t, host.ErrClosed.Error(), runEnd(t, events).Error)
	next(t, events, is[host.EventSessionEnd])
	assert.Equal(t, []string{"a"}, f.model.texts())
}

// drainUntilEnd reads events up to the session_end, and returns them.
func drainUntilEnd(t *testing.T, events <-chan host.Event) []host.Event {
	t.Helper()
	var out []host.Event
	next(t, events, func(e host.Event) bool {
		out = append(out, e)
		return is[host.EventSessionEnd](e)
	})
	return out
}

// plainRunner hides the optional interfaces of its runner, such as
// [agent.Steerer].
type plainRunner struct {
	agent.Runner
}
