package host

import (
	"context"
	"fmt"
	"slices"

	"github.com/sonnes/gopi/pkg/agent"
)

// call is a tool call that waits for [Host.Confirm].
type call struct {
	req agent.ApprovalRequest
	// decision has one slot, so a send on it does not block.
	decision chan agent.Decision
}

// approve is the approve function that the host gives each agent. It
// adds req to the waiting calls of the session, and waits for
// [Host.Confirm], for the end of ctx, or for the end of the run. Each
// call has its own channel, so two or more calls can wait at the same
// time, and a Confirm answers one of them.
//
// If no run is active, approve returns [ErrNoRun]. If the session
// stops, it returns [ErrClosed]. The wait also ends when the active run
// ends. The Codex agent calls approve with the context of its
// connection, and an interrupt does not end that context.
func (s *hosted) approve(ctx context.Context, req agent.ApprovalRequest) (agent.Decision, error) {
	s.mu.Lock()
	run := s.run
	if s.closing {
		s.mu.Unlock()
		return agent.Decision{}, fmt.Errorf("host: session %s: %w", s.id, ErrClosed)
	}
	if run == nil {
		s.mu.Unlock()
		return agent.Decision{}, fmt.Errorf("host: session %s: %w", s.id, ErrNoRun)
	}
	c := &call{
		req:      req,
		decision: make(chan agent.Decision, 1),
	}
	s.calls = append(s.calls, c)
	s.updateLocked()
	s.mu.Unlock()

	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	stop := context.AfterFunc(run, func() {
		cancel(context.Cause(run))
	})
	defer stop()

	select {
	case d := <-c.decision:
		return d, nil
	case <-ctx.Done():
		s.mu.Lock()
		s.dropCallLocked(func(other *call) bool { return other == c })
		s.mu.Unlock()
		return agent.Decision{}, ctx.Err()
	}
}

// Confirm gives d to the tool call callID that waits in the session id.
// The other calls that wait keep their wait. The status stays
// requires_action until the last call ends, in any order. It is the
// user.tool_confirmation event of Claude Managed Agents.
//
// If the host does not know the session, or no such call waits, Confirm
// returns [ErrNotFound].
func (h *Host) Confirm(_ context.Context, id, callID string, d agent.Decision) error {
	s, err := h.session(id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	c, ok := s.dropCallLocked(func(c *call) bool { return c.req.Call.ID == callID })
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("host: session %s: no call %s waits: %w", id, callID, ErrNotFound)
	}
	c.decision <- d
	return nil
}

// dropCallLocked removes the first waiting call that match reports, and
// updates the session. If the last call ends during a run, the status is
// running again. If no call matches, such as after the end of the run,
// it reports false.
func (s *hosted) dropCallLocked(match func(*call) bool) (*call, bool) {
	i := slices.IndexFunc(s.calls, match)
	if i < 0 {
		return nil, false
	}
	c := s.calls[i]
	s.calls = slices.Delete(s.calls, i, i+1)
	s.updateLocked()
	return c, true
}
