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

// ErrInterrupted is the cause of a run that [Interrupt] or
// [Session.Interrupt] ended.
var ErrInterrupted = errors.New("host: the run was interrupted")

// ErrNoRun is the error of an approval that an agent asks for while the
// session runs no message. The host does not pass such an approval to
// the approve function of the spec, because no run can take its answer.
var ErrNoRun = errors.New("host: no run is active")

// Message is the input of one message to a session.
type Message []session.Entry

// Text returns a message with one user text entry.
func Text(s string) Message {
	return Message{session.Text(s)}
}

// Policy says what a session does with a message while it is busy. A
// session is busy while a run is active or a message waits.
type Policy string

// The policies of a message. The empty policy is [Queue].
const (
	// Queue puts the message at the end of the queue.
	Queue Policy = "queue"
	// Steer gives the message to the active run, before its next model
	// call. If no run is active, or the agent is not an
	// [agent.Steerer], the message queues.
	Steer Policy = "steer"
	// Interrupt puts the message at the front of the queue, and cancels
	// the active run.
	Interrupt Policy = "interrupt"
	// Reject refuses the message with [ErrBusy] if the session is busy.
	// Otherwise the message queues.
	Reject Policy = "reject"
)

// queued is a message that waits for a run.
type queued struct {
	m Message
	// interrupt is set for a message with [Interrupt].
	interrupt bool
}

var policies = []Policy{
	Queue,
	Steer,
	Interrupt,
	Reject,
}

// Send gives m to the session with the policy p, and returns when the
// session accepts m. It does not wait for the run. The host stream gets
// [EventInput] for each message that the session accepts.
//
// If m is empty or p is unknown, Send returns [ErrInvalid]. If the
// session closed, Send returns [ErrClosed]. A message with [Reject] to
// a busy session gets [ErrBusy].
func (s *Session) Send(_ context.Context, m Message, p Policy) error {
	if len(m) == 0 {
		return fmt.Errorf("host: the message has no entries: %w", ErrInvalid)
	}
	if p == "" {
		p = Queue
	}
	if !slices.Contains(policies, p) {
		return fmt.Errorf("host: the policy %q is not queue, steer, interrupt, or reject: %w", p, ErrInvalid)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return fmt.Errorf("host: session %s: %w", s.id, ErrClosed)
	}
	busy := s.active != nil || len(s.queue) > 0
	switch p {
	case Reject:
		if busy {
			return fmt.Errorf("host: session %s: %w", s.id, ErrBusy)
		}
		p = Queue
	case Steer:
		steerer, ok := s.runner.(agent.Steerer)
		if ok && s.active != nil {
			steerer.Steer(m...)
			s.publishLocked(EventInput{
				Session: s.id,
				Entries: m,
				Policy:  Steer,
			})
			return nil
		}
		p = Queue
	}

	if p == Interrupt {
		s.queue = slices.Insert(s.queue, 0, queued{m: m, interrupt: true})
		if s.active != nil {
			s.active(ErrInterrupted)
		}
	} else {
		s.queue = append(s.queue, queued{m: m})
	}
	s.publishLocked(EventInput{
		Session: s.id,
		Entries: m,
		Policy:  p,
	})
	s.signal()
	return nil
}

// Interrupt cancels the active run. The log keeps its entries, and the
// next queued message runs. If no run is active, Interrupt does
// nothing.
func (s *Session) Interrupt() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active != nil {
		s.active(ErrInterrupted)
	}
}

// work runs the queued messages, one at a time, until the session
// stops.
func (s *Session) work() {
	defer close(s.done)
	for {
		ctx, m, ok := s.next()
		if !ok {
			return
		}
		s.execute(ctx, m)
	}
}

// next waits for a queued message and starts its run. It reports false
// when the session stops.
func (s *Session) next() (context.Context, Message, bool) {
	for {
		s.mu.Lock()
		if s.ctx.Err() != nil {
			s.mu.Unlock()
			return nil, nil, false
		}
		if len(s.queue) > 0 {
			m := s.queue[0].m
			s.queue = s.queue[1:]
			ctx, cancel := context.WithCancelCause(s.ctx)
			s.active = cancel
			s.runCtx = ctx
			s.publishLocked(EventRunStart{Session: s.id})
			s.mu.Unlock()
			return ctx, m, true
		}
		s.mu.Unlock()
		select {
		case <-s.wake:
		case <-s.ctx.Done():
		}
	}
}

// execute runs the agent on m, and ends the run.
func (s *Session) execute(ctx context.Context, m Message) {
	stream := s.runner.Run(ctx, m...)
	var usage ai.Usage
	for ev, err := range stream.Events() {
		if err != nil {
			break
		}
		if end, ok := ev.(agent.EventAgentEnd); ok {
			usage = end.Usage
		}
		s.agents.publish(ev)
	}
	_, err := stream.Wait()
	s.finish(ctx, usage, err)
}

// finish ends the active run, and queues the steer that the run did
// not read.
func (s *Session) finish(ctx context.Context, usage ai.Usage, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cause := context.Cause(ctx)
	s.active(nil)
	s.active = nil
	s.runCtx = nil
	s.endPendingLocked(cause)

	end := EventRunEnd{
		Session: s.id,
		Usage:   usage,
	}
	// A run that a caller canceled reports the cause, such as
	// ErrInterrupted, and not context.Canceled.
	switch {
	case err == nil:
	case cause != nil && errors.Is(err, context.Canceled):
		end.Error = cause.Error()
	default:
		end.Error = err.Error()
	}
	s.publishLocked(end)
	s.queueUnreadSteerLocked()
}

// queueUnreadSteerLocked puts the steer that the run did not read at
// the front of the queue, after the messages with [Interrupt]. A steer
// can come after the last read of a run, or the run can fail first. The
// agent keeps such a steer until TakeSteer.
func (s *Session) queueUnreadSteerLocked() {
	steerer, ok := s.runner.(agent.Steerer)
	if !ok || s.closing {
		return
	}
	unread := steerer.TakeSteer()
	if len(unread) == 0 {
		return
	}
	m := Message(unread)
	at := 0
	for at < len(s.queue) && s.queue[at].interrupt {
		at++
	}
	s.queue = slices.Insert(s.queue, at, queued{m: m})
	s.publishLocked(EventInput{
		Session: s.id,
		Entries: m,
		Policy:  Queue,
	})
	s.signal()
}

// publishLocked sends e to the host stream. The session holds its lock,
// so its events reach the stream in the order of its changes.
func (s *Session) publishLocked(e Event) {
	s.host.hub.publish(e)
}

// signal tells the worker to look at the queue.
func (s *Session) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
