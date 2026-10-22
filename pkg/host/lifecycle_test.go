package host_test

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/host"
)

// rounds is how many times a test of a race tries it.
const rounds = 200

// spin calls cond with no pause until it returns true. A test of a race
// spins, so that it hits a short window.
func spin(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(waitTimeout)
	for !cond() {
		require.True(t, time.Now().Before(deadline), "the condition stayed false")
		runtime.Gosched()
	}
}

func TestStartIsVisibleAfterSessionStart(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	for range rounds {
		started := make(chan error, 1)
		go func() {
			_, err := f.h.Start(ctx, host.Spec{ID: "s1", Model: "fake/m"})
			started <- err
		}()
		var s *host.Session
		spin(t, func() bool {
			var ok bool
			s, ok = f.h.Session("s1")
			return ok
		})
		require.NoError(t, s.Close())
		require.NoError(t, <-started)

		state, _ := f.h.Events(ctx)
		require.NotContains(t, state.Sessions, "s1")
	}
}

func TestRestartAfterClose(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	for range rounds {
		s := f.start(t, host.Spec{ID: "s1"})
		closed := make(chan error, 1)
		go func() { closed <- s.Close() }()
		var again *host.Session
		spin(t, func() bool {
			var err error
			again, err = f.h.Start(ctx, host.Spec{ID: "s1", Model: "fake/m"})
			return !errors.Is(err, host.ErrExists)
		})
		require.NoError(t, <-closed)
		require.NotNil(t, again)

		state, _ := f.h.Events(ctx)
		require.Contains(t, state.Sessions, "s1")
		require.NoError(t, again.Close())
	}
}

func TestInterruptRunsBeforeUnreadSteer(t *testing.T) {
	f, gate := gated(t)
	s := f.start(t, host.Spec{ID: "s1"})
	_, events := f.watch(t)

	send(t, s, "a", host.Queue)
	next(t, events, is[host.EventRunStart])
	f.model.waitCalls(t, 1)
	send(t, s, "b", host.Steer)
	send(t, s, "c", host.Interrupt)

	assert.Equal(t, host.ErrInterrupted.Error(), runEnd(t, events).Error)
	close(gate)
	runEnd(t, events)
	runEnd(t, events)
	assert.Equal(t, []string{"a", "c", "b"}, f.model.texts())
}

func TestInterruptEndsDetachedApproval(t *testing.T) {
	f := newFixture(t)
	f.driver.detached = true
	f.model.replies = []ai.Message{
		callTool("call_1", "echo", `{"s":"hi"}`),
		reply("done"),
	}
	a := newApprover()
	s := f.start(t, host.Spec{
		ID:      "s1",
		Approve: a.approve,
	})
	_, events := f.watch(t)

	send(t, s, "a", host.Queue)
	<-a.calls
	s.Interrupt()

	end := next(t, events, is[host.EventApprovalEnd]).(host.EventApprovalEnd)
	assert.Equal(t, host.ErrInterrupted.Error(), end.Error)
	runEnd(t, events)
	state, _ := f.h.Events(context.Background())
	assert.Equal(t, host.StatusIdle, state.Sessions["s1"].Status())
}

func TestRunFails(t *testing.T) {
	f := newFixture(t)
	f.model.fail = errors.New("model down")
	s := f.start(t, host.Spec{ID: "s1"})
	_, events := f.watch(t)
	_, agentEvents := follow(t, s)

	send(t, s, "a", host.Queue)

	assert.Contains(t, runEnd(t, events).Error, "model down")
	end := next(t, agentEvents, is[agent.EventAgentEnd]).(agent.EventAgentEnd)
	assert.Contains(t, end.Error, "model down")
}
