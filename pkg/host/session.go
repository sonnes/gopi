package host

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/session"
)

// hosted is one session that the host knows. It has an agent only after
// [Host.Create] or the first message starts one. A hosted is safe for
// concurrent use.
type hosted struct {
	id   string
	host *Host
	// agents has the state of the agent, and sends each agent event. It
	// exists before the agent, so a reader can follow a session that has
	// no agent yet.
	agents *hub[agent.State, agent.Event]
	// runs has the goroutine of the active run. stop waits for it.
	runs sync.WaitGroup

	// startMu holds a start of the agent, an update of the config, and
	// a stop, so two messages start one agent, and a stop waits for a
	// start. A write of runner, stale, or closing holds startMu and mu,
	// so either lock lets a caller read them.
	startMu sync.Mutex
	// runner is the agent. It is nil until the agent starts.
	runner agent.Runner
	// stale is set when [Host.Update] changes the config after the agent
	// started. The next run then starts the agent again.
	stale bool
	// closing is set when stop starts. The session then takes no
	// message.
	closing bool

	mu   sync.Mutex
	sess Session
	// writeErr is the first error of a write of the status. Close
	// returns it.
	writeErr error
	// run is the context of the active run, and cancelRun ends it with
	// a cause. Both are nil between runs.
	run       context.Context
	cancelRun context.CancelCauseFunc
	// queue has the messages that run after the active run, in order.
	queue []Message
	// calls has the tool calls that wait for [Host.Confirm], the oldest
	// first.
	calls []*call
}

func newHosted(h *Host, sess Session) *hosted {
	return &hosted{
		id:   sess.ID,
		host: h,
		agents: newHub(
			agent.State{},
			(*agent.State).Apply,
			agent.State.Clip,
		),
		sess: sess,
	}
}

// snapshot returns a copy of the [Session] of s.
func (s *hosted) snapshot() Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sess.clone()
}

// updateLocked sets the status and the waiting calls of s from its run
// and its calls. It then writes the session to the value
// gopi.host/session, and sends it to the host stream. The caller holds
// mu, so the events of s reach the stream in the order of its changes.
func (s *hosted) updateLocked() {
	s.sess.Status = StatusIdle
	if s.run != nil {
		s.sess.Status = StatusRunning
	}
	if len(s.calls) > 0 {
		s.sess.Status = StatusRequiresAction
	}
	s.sess.Waiting = nil
	for _, c := range s.calls {
		s.sess.Waiting = append(s.sess.Waiting, c.req)
	}
	s.sess.UpdatedAt = now()
	sess := s.sess.clone()

	// A run that a caller canceled still writes its end, so the write
	// does not use the context of the run.
	ctx := context.Background()
	var err error
	if s.runner != nil {
		err = sessionValue.Set(ctx, s.runner.Log().Writer(), sess)
	} else {
		err = setStored(ctx, s.host.store, s.id, sessionValue, sess)
	}
	if err != nil && s.writeErr == nil {
		s.writeErr = fmt.Errorf("host: session %s: write the session: %w", s.id, err)
	}
	s.host.hub.publish(Event{Session: sess})
}

// log returns the log of the agent, or the log in the store if no agent
// runs.
func (s *hosted) log(ctx context.Context) (session.Writer, error) {
	s.mu.Lock()
	r := s.runner
	s.mu.Unlock()
	if r != nil {
		return r.Log().Writer(), nil
	}
	log, err := session.Open(ctx, s.host.store, s.id)
	if err != nil {
		return nil, fmt.Errorf("host: session %s: %w", s.id, err)
	}
	return log.Writer(), nil
}

// startAgent starts the agent of the session from its config value, if
// no agent runs. The driver resumes the log. A start that fails leaves
// the session idle with [StopError].
func (s *hosted) startAgent(ctx context.Context) error {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	if s.closing {
		return fmt.Errorf("host: session %s: %w", s.id, ErrClosed)
	}
	if s.runner != nil {
		return nil
	}

	runner, state, err := s.newRunner(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.sess.StopReason = StopError
		s.sess.Error = err.Error()
		s.updateLocked()
		return err
	}
	s.runner = runner
	s.agents.reset(state)
	return nil
}

// newRunner starts the agent from the config value of the session. It
// returns the agent, and the state of the agent for the agent stream.
func (s *hosted) newRunner(ctx context.Context) (agent.Runner, agent.State, error) {
	h := s.host
	snap, err := h.store.Load(ctx, s.id)
	if err != nil {
		return nil, agent.State{}, fmt.Errorf("host: session %s: %w", s.id, err)
	}
	config, ok, err := configValue.Read(snap)
	if err == nil && !ok {
		err = fmt.Errorf("no config: %w", ErrInvalid)
	}
	if err != nil {
		return nil, agent.State{}, fmt.Errorf("host: session %s: %w", s.id, err)
	}
	d, model, err := h.cat.Driver(config.Model)
	if err != nil {
		return nil, agent.State{}, fmt.Errorf("host: session %s: %w", s.id, err)
	}

	// The driver gets the snapshot, so it does not read the log a second
	// time.
	runner, err := d.Start(ctx, agent.Config{
		Session:  s.id,
		Store:    session.Preloaded(h.store, s.id, snap),
		Spec:     config.Model,
		Model:    model,
		Mode:     config.Mode,
		Dir:      config.Dir,
		Sections: sectionsOf(config, s.id, h.sections),
		Skills:   config.Skills,
		Dirs:     config.Dirs,
		Effort:   config.Effort,
		Allow:    config.Allow,
		Ask:      config.Ask,
		Deny:     config.Deny,
		MCP:      config.MCP,
		Approve:  s.approve,
		Hooks:    h.hooks,
		Tools:    h.tools,
	})
	if err != nil {
		return nil, agent.State{}, fmt.Errorf("host: start session %s: %w", s.id, err)
	}
	state, err := viewerState(runner.State())
	if err != nil {
		err = fmt.Errorf("host: session %s: copy the state of the agent: %w", s.id, err)
		return nil, agent.State{}, errors.Join(err, closeRunner(runner))
	}
	return runner, state, nil
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

// stop cancels the active run, drops the held messages, stops the
// agent, and ends the agent stream. The log stays. It waits for a start
// of the agent, and then sets closing, so no agent starts after it. It
// lets go of startMu before it waits for the run, because a run can
// start the agent again.
func (s *hosted) stop() error {
	s.startMu.Lock()
	s.mu.Lock()
	s.closing = true
	s.queue = nil
	if s.cancelRun != nil {
		s.cancelRun(ErrClosed)
	}
	s.mu.Unlock()
	s.startMu.Unlock()

	s.runs.Wait()
	s.mu.Lock()
	runner := s.runner
	s.mu.Unlock()
	var err error
	if runner != nil {
		err = closeRunner(runner)
	}
	s.agents.close()

	s.mu.Lock()
	defer s.mu.Unlock()
	return errors.Join(err, s.writeErr)
}

// freshRunner returns the agent for a run. If [Host.Update] changed the
// config after the agent started, freshRunner stops the agent and starts
// it again from the new config. The driver resumes the log. While the
// agent starts, runner is nil, so a message to the session waits.
func (s *hosted) freshRunner(ctx context.Context) (agent.Runner, error) {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	old := s.runner
	if !s.stale {
		s.mu.Unlock()
		return old, nil
	}
	s.runner = nil
	s.stale = false
	s.mu.Unlock()

	if err := closeRunner(old); err != nil {
		return nil, fmt.Errorf("host: session %s: stop the agent: %w", s.id, err)
	}
	runner, state, err := s.newRunner(ctx)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.runner = runner
	s.agents.reset(state)
	return runner, nil
}

// closeRunner stops the process of an agent that has one.
func closeRunner(r agent.Runner) error {
	c, ok := r.(io.Closer)
	if !ok {
		return nil
	}
	return c.Close()
}
