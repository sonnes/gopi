package host

import (
	"context"
	"fmt"

	"github.com/sonnes/gopi/pkg/agent"
)

// writable returns [ErrChild] if s is the session of a subagent.
func (s *hosted) writable() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sess.Parent != nil {
		return fmt.Errorf("host: session %s: %w", s.id, ErrChild)
	}
	return nil
}

// relay applies one event of the run of the session parentID to its
// children. A subagent_start makes the child a session of the host and
// sets it running. Each subagent event goes to the stream of the child
// without its wrapper, so the stream of a child has its own events. A
// subagent_end sets the child idle. An event of a child that is itself
// a subagent event applies to the children of the child.
func (h *Host) relay(ctx context.Context, parentID string, ev agent.Event) {
	switch e := ev.(type) {
	case agent.EventSubagentStart:
		if e.SessionID == "" {
			return
		}
		ref := agent.ParentRef{
			SessionID:  parentID,
			ToolCallID: e.ToolCallID,
		}
		h.child(e.SessionID, ref, e.Model).begin()
	case agent.EventSubagent:
		c, err := h.session(e.SessionID)
		if err != nil {
			return
		}
		c.agents.publish(e.Event)
		h.relay(ctx, e.SessionID, e.Event)
	case agent.EventSubagentEnd:
		c, err := h.session(e.SessionID)
		if err != nil {
			return
		}
		h.endChildren(ctx, e.SessionID)
		c.end(stopOfChild(e.Status), childError(e))
	}
}

// child returns the session of the child id, and adds it to the host if
// the host does not know it. The driver created the session in the
// store before its start.
func (h *Host) child(id string, ref agent.ParentRef, model string) *hosted {
	h.mu.Lock()
	defer h.mu.Unlock()
	if s, ok := h.sessions[id]; ok {
		return s
	}
	t := now()
	s := newHosted(h, Session{
		ID:        id,
		Status:    StatusIdle,
		Parent:    &ref,
		Model:     model,
		CreatedAt: t,
		UpdatedAt: t,
	})
	h.sessions[id] = s
	return s
}

// begin sets the child s running.
func (s *hosted) begin() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.childRuns = true
	s.sess.StopReason = ""
	s.sess.Error = ""
	s.updateLocked()
}

// end sets the child s idle with reason and msg.
func (s *hosted) end(reason StopReason, msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.childRuns {
		return
	}
	s.childRuns = false
	s.sess.StopReason = reason
	s.sess.Error = msg
	s.updateLocked()
}

// endChildren ends each child of the session parentID that still runs,
// as interrupted. A run that ends with a child open, such as a run that
// failed, leaves no child running.
func (h *Host) endChildren(ctx context.Context, parentID string) {
	for _, c := range h.all() {
		snap := c.snapshot()
		isChild := snap.Parent != nil && snap.Parent.SessionID == parentID
		if !isChild || snap.Status != StatusRunning {
			continue
		}
		h.endChildren(ctx, c.id)
		c.end(StopInterrupted, "")
	}
}

// stopOfChild returns the stop reason of a child that ended with status.
func stopOfChild(status string) StopReason {
	switch status {
	case agent.SubagentCompleted:
		return StopEndTurn
	case agent.SubagentInterrupted:
		return StopInterrupted
	default:
		return StopError
	}
}

// childError returns the error of a child that failed: its last text.
func childError(e agent.EventSubagentEnd) string {
	if e.Status != agent.SubagentFailed {
		return ""
	}
	return e.Text
}
