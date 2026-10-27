package host

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/session"
)

// ErrInterrupted is the cause of a run that [Host.Interrupt] ended.
var ErrInterrupted = errors.New("host: the run was interrupted")

// ErrNoRun is the error of an approval that an agent asks for while the
// session runs no message. The host does not wait for a Confirm of such
// an approval, because no run can take its answer.
var ErrNoRun = errors.New("host: no run is active")

// Message is the input of one message to a session.
type Message []session.Entry

// Text returns a message with one user text entry.
func Text(s string) Message {
	return Message{session.Text(s)}
}

// Send gives m to the session id, and returns when the session takes m.
// It does not wait for the run. It is the user.message event of Claude
// Managed Agents.
//
//   - On an idle session, m runs at once, and the status is running when
//     Send returns.
//   - On a running session, m steers the run, before its next model call.
//   - If the agent cannot steer, such as a Codex agent,
//     the host holds m, and m runs after the run.
//
// If no agent runs, Send starts it from the config of the session, and the
// driver resumes the log. If m is empty, Send returns [ErrInvalid]. If
// the host does not know the session, Send returns [ErrNotFound]. If the
// host closed, Send returns [ErrClosed].
func (h *Host) Send(ctx context.Context, id string, m Message) error {
	if len(m) == 0 {
		return fmt.Errorf("host: the message has no entries: %w", ErrInvalid)
	}
	s, err := h.session(id)
	if err != nil {
		return err
	}
	if err := s.writable(); err != nil {
		return err
	}
	if err := s.startAgent(ctx); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return fmt.Errorf("host: session %s: %w", s.id, ErrClosed)
	}
	steerer, canSteer := s.runner.(agent.Steerer)
	switch {
	case s.run == nil:
		s.startRunLocked(m)
	case canSteer:
		steerer.Steer(m...)
	default:
		s.queue = append(s.queue, m)
	}
	return nil
}

// Interrupt cancels the active run of the session id. The log keeps its
// entries, and a held message runs next. If no run is active, Interrupt
// does nothing. It is the user.interrupt event of Claude Managed Agents.
// If the host does not know the session, Interrupt returns
// [ErrNotFound].
func (h *Host) Interrupt(_ context.Context, id string) error {
	s, err := h.session(id)
	if err != nil {
		return err
	}
	if err := s.writable(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancelRun != nil {
		s.cancelRun(ErrInterrupted)
	}
	return nil
}

// startRunLocked sets the session running, and runs m in a new
// goroutine. The context of the run exists when it returns, so an
// Interrupt right after Send stops the run. With no m, the run is the
// turn that the agent started on its own. A run with m reads that turn
// first, so a wake that waits goes.
func (s *hosted) startRunLocked(m Message) {
	if m != nil {
		s.wakePending = false
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	s.run = ctx
	s.cancelRun = cancel
	s.sess.StopReason = ""
	s.sess.Error = ""
	s.updateLocked()
	s.runs.Go(func() { s.execute(ctx, m) })
}

// execute runs the agent on m, and sends each agent event to the agent
// stream. Then it ends the run. With no m, it continues the turn that
// the agent started on its own.
func (s *hosted) execute(ctx context.Context, m Message) {
	runner, err := s.freshRunner(ctx)
	if err != nil {
		s.finish(ctx, ai.Usage{}, err)
		return
	}
	stream := s.stream(ctx, runner, m)
	var usage ai.Usage
	for ev, err := range stream.Events() {
		if err != nil {
			break
		}
		if end, ok := ev.(agent.EventAgentEnd); ok {
			usage = end.Usage
		}
		// The child exists before a reader of the parent sees its start.
		s.host.relay(s.id, ev)
		s.agents.publish(ev)
	}
	_, err = stream.Wait()
	// A child that the run left open ends as the run did: restarted when
	// Close cut it, and interrupted otherwise. A child in the background
	// goes on, unless Close cut the run.
	reason := StopInterrupted
	closed := errors.Is(context.Cause(ctx), ErrClosed)
	if closed {
		reason = StopRestarted
	}
	s.host.endChildren(s.id, reason, closed)
	s.finish(ctx, usage, err)
}

// stream starts the run of m on runner. With no m, it continues the
// turn of an [agent.Waker]. A runner that is not a Waker does not wake,
// so it never gets a run with no m.
func (s *hosted) stream(ctx context.Context, runner agent.Runner, m Message) *agent.Stream {
	if w, ok := runner.(agent.Waker); ok && m == nil {
		return w.Continue(ctx)
	}
	return runner.Run(ctx, m...)
}

// finish ends the active run, and starts the run of the next held
// message.
func (s *hosted) finish(ctx context.Context, usage ai.Usage, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	reason, msg := stopOf(context.Cause(ctx), err)
	s.cancelRun(nil)
	s.run = nil
	s.cancelRun = nil
	// A call that waits ends with its run. Its approve function sees the
	// end of the run in its context.
	s.calls = nil
	s.sess.StopReason = reason
	s.sess.Error = msg
	s.sess.Usage = usage
	s.updateLocked()
	if s.closing {
		return
	}

	// The agent keeps a steer that the run did not read, because the
	// steer came after the last read, or the run failed first. The steer
	// was meant for the work, so it runs next, also after an interrupt.
	if steerer, ok := s.runner.(agent.Steerer); ok {
		unread := steerer.TakeSteer()
		if len(unread) > 0 {
			s.queue = slices.Insert(s.queue, 0, Message(unread))
		}
	}
	if len(s.queue) == 0 {
		if s.wakePending {
			s.wakePending = false
			s.startRunLocked(nil)
		}
		return
	}
	next := s.queue[0]
	s.queue = s.queue[1:]
	s.startRunLocked(next)
}

// stopOf returns the stop reason and the error of a run that ended with
// err. A run that a caller canceled reports the cause, such as
// [ErrInterrupted], and not context.Canceled.
func stopOf(cause, err error) (StopReason, string) {
	canceled := cause != nil && errors.Is(err, context.Canceled)
	switch {
	case err == nil:
		return StopEndTurn, ""
	case canceled && errors.Is(cause, ErrInterrupted):
		return StopInterrupted, ""
	case canceled && errors.Is(cause, ErrClosed):
		return StopRestarted, ""
	case canceled:
		return StopError, cause.Error()
	default:
		return StopError, err.Error()
	}
}
