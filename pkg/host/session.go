package host

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

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
	// childRuns is true while the run of the parent of a child runs the
	// child. inBackground is true while the child runs after the run of
	// its parent.
	childRuns    bool
	inBackground bool
	// wakePending is true when the agent woke during a run, so a run of
	// the turn of the agent follows the run.
	wakePending bool
	// idleTimer stops the agent after the [IdleTimeout] of the host. It
	// runs only while the session is idle and has an agent.
	idleTimer *time.Timer
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
	if s.run != nil || s.childRuns {
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
	s.armIdleLocked()
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
		SessionID: s.id,
		Store:     session.Preloaded(h.store, s.id, snap),
		Spec:      config.Model,
		Model:     model,
		Mode:      config.Mode,
		Dir:       config.Dir,
		Sections:  sectionsOf(config, s.id, h.sections),
		Skillset:  h.skillset,
		Skills:    config.Skills,
		Dirs:      config.Dirs,
		Effort:    config.Effort,
		ToolNames: config.ToolNames,
		Allow:     config.Allow,
		Ask:       config.Ask,
		Deny:      config.Deny,
		MCP:       config.MCP,
		Approve:   s.approve,
		Idle:      s.idle,
		Wake:      s.woke,
		Hooks:     h.hooks,
		Tools:     h.tools,

		Compaction:  h.compaction,
		AutoCompact: config.AutoCompact,
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
	s.stopIdleLocked()
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
	// No child outlives the agent that runs it.
	s.host.endChildren(s.id, StopRestarted, true)
	s.agents.close()

	s.mu.Lock()
	defer s.mu.Unlock()
	return errors.Join(err, s.writeErr)
}

// Release stops the agent of the idle session id, so the agent of a CLI
// frees its process. The session and its log stay. The next message
// starts the agent again from the config of the session, and the driver
// resumes the log. A session with no agent has nothing to release.
//
// If the session runs, holds a message, or runs a child in the
// background, Release returns [ErrBusy], and the agent stays. If the host
// does not know the session, Release returns [ErrNotFound]. A child has
// no agent of its own, so Release returns [ErrChild].
func (h *Host) Release(_ context.Context, id string) error {
	s, err := h.session(id)
	if err != nil {
		return err
	}
	if err := s.writable(); err != nil {
		return err
	}
	return s.release()
}

// release stops the agent of s if s is idle. It holds startMu to the end
// of the stop, so a message waits, and then starts a new agent.
func (s *hosted) release() error {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	// The host lock comes before the lock of s, so the check of the
	// children comes first.
	children := s.host.RunsChildren(s.id)

	s.mu.Lock()
	busy := s.run != nil ||
		len(s.queue) > 0 ||
		s.wakePending ||
		children
	switch {
	case s.closing || s.runner == nil:
		s.mu.Unlock()
		return nil
	case busy:
		s.mu.Unlock()
		return fmt.Errorf("host: session %s: %w", s.id, ErrBusy)
	}
	runner := s.runner
	s.runner = nil
	s.stale = false
	s.stopIdleLocked()
	s.mu.Unlock()

	if err := closeRunner(runner); err != nil {
		return fmt.Errorf("host: session %s: stop the agent: %w", s.id, err)
	}
	return nil
}

// armIdleLocked starts the idle timer of s again, if the host has an
// [IdleTimeout]. The caller holds mu.
func (s *hosted) armIdleLocked() {
	d := s.host.idleTimeout
	if d <= 0 || s.closing {
		return
	}
	s.stopIdleLocked()
	s.idleTimer = time.AfterFunc(d, s.idleRelease)
}

// stopIdleLocked stops the idle timer of s. The caller holds mu.
func (s *hosted) stopIdleLocked() {
	if s.idleTimer != nil {
		s.idleTimer.Stop()
		s.idleTimer = nil
	}
}

// idleRelease stops the agent of s at the end of its idle time. A
// session that is busy with no run, such as one with a child in the
// background, waits one more idle time. A run starts the timer again at
// its end.
func (s *hosted) idleRelease() {
	err := s.release()
	if !errors.Is(err, ErrBusy) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.run == nil {
		s.armIdleLocked()
	}
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

// woke runs the turn that the agent started on its own. During a run,
// the turn runs after the run. A wake that comes after a run read the
// turn starts a run that ends at once.
func (s *hosted) woke() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return
	}
	if s.run != nil {
		s.wakePending = true
		return
	}
	s.startRunLocked(nil)
}

// closeRunner stops the process of an agent that has one.
func closeRunner(r agent.Runner) error {
	c, ok := r.(io.Closer)
	if !ok {
		return nil
	}
	return c.Close()
}
