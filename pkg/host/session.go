package host

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"sync"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/session"
)

// Session is one session of a host: an agent and its log. A Session is
// safe for concurrent use.
type Session struct {
	id     string
	host   *Host
	spec   Spec
	runner agent.Runner
	// agents has the state of the agent, and sends each agent event.
	agents *hub[agent.State, agent.Event]

	// ctx is the parent of each run. stop cancels it.
	ctx    context.Context
	cancel context.CancelCauseFunc
	// done closes when the worker ends.
	done chan struct{}
	// wake has one slot. A send tells the worker to look at the queue.
	wake chan struct{}

	mu    sync.Mutex
	queue []queued
	// active cancels the active run with a cause. It is nil between
	// runs. runCtx is the context of the active run.
	active context.CancelCauseFunc
	runCtx context.Context
	// pending are the approvals of the active run that have no
	// approval_end yet, in the order of their start.
	pending []*approval
	// closing is set when Close starts. The session then takes no
	// message.
	closing bool

	closeOnce sync.Once
	closeErr  error
}

func newSession(h *Host, id string, spec Spec) *Session {
	ctx, cancel := context.WithCancelCause(context.Background())
	return &Session{
		id:     id,
		host:   h,
		spec:   spec,
		ctx:    ctx,
		cancel: cancel,
		done:   make(chan struct{}),
		wake:   make(chan struct{}, 1),
	}
}

// ID returns the ID of the session.
func (s *Session) ID() string { return s.id }

// Log returns the log of the session: the messages of the agent.
func (s *Session) Log() *session.Log { return s.runner.Log() }

// Events returns the state of the agent of the session, and the stream
// of the agent events after that state. The stream has the events of
// each run of the session. A reader applies each event to the state
// with [agent.State.Apply].
//
// The state is a copy for a viewer: [agent.State.Steer] on it does
// nothing. To steer, call [Session.Send] with [Steer].
//
// The stream ends when the session closes, with the error of ctx when
// ctx ends, or with [ErrSlowReader]. After ErrSlowReader, call Events
// again.
func (s *Session) Events(ctx context.Context) (agent.State, iter.Seq2[agent.Event, error]) {
	return s.agents.subscribe(ctx)
}

// setRunner sets the agent of the session, and starts the agent hub
// from the state of the agent.
func (s *Session) setRunner(r agent.Runner) error {
	state, err := viewerState(r.State())
	if err != nil {
		return fmt.Errorf("host: session %s: copy the state of the agent: %w", s.id, err)
	}
	s.runner = r
	s.agents = newHub(
		state,
		(*agent.State).Apply,
		agent.State.Clip,
	)
	return nil
}

// viewerState returns a copy of s with no queues and no log, as a
// viewer decodes it. The hub then applies events to its own copy, and a
// reader cannot steer the agent through it.
func viewerState(s agent.State) (agent.State, error) {
	data, err := json.Marshal(s)
	if err != nil {
		return agent.State{}, err
	}
	var out agent.State
	err = json.Unmarshal(data, &out)
	return out, err
}

// Close cancels the active run, drops the queued messages, and stops
// the agent. The log stays. The host stream gets [EventSessionEnd]. A
// second Close returns the result of the first. Do not call Close from
// the approve function or a hook of the session, because Close waits
// for the run.
func (s *Session) Close() error {
	s.closeOnce.Do(func() {
		s.closeErr = s.stop()
		end := EventSessionEnd{Session: s.id}
		if s.closeErr != nil {
			end.Error = s.closeErr.Error()
		}
		s.host.end(end)
		s.agents.close()
	})
	return s.closeErr
}

// stop ends the worker, and then stops the agent.
func (s *Session) stop() error {
	s.mu.Lock()
	s.closing = true
	s.queue = nil
	s.mu.Unlock()
	s.cancel(ErrClosed)
	<-s.done
	return closeRunner(s.runner)
}

// closeRunner stops the process of an agent that has one.
func closeRunner(r agent.Runner) error {
	c, ok := r.(io.Closer)
	if !ok {
		return nil
	}
	return c.Close()
}
