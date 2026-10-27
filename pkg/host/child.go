package host

import (
	"cmp"
	"fmt"
	"maps"
	"slices"

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
// subagent_end sets the child idle. An end with the status background
// keeps the child running, because the child goes on after the run. An
// event of a child that is itself a subagent event applies to the
// children of the child.
func (h *Host) relay(parentID string, ev agent.Event) {
	switch e := ev.(type) {
	case agent.EventSubagentStart:
		if e.SessionID == "" {
			return
		}
		ref := agent.ParentRef{
			SessionID:  parentID,
			ToolCallID: e.ToolCallID,
		}
		c := h.child(e.SessionID, ref, e.Model)
		if c == nil {
			return
		}
		h.track(parentID, c)
		c.begin()
	case agent.EventSubagent:
		c, err := h.session(e.SessionID)
		if err != nil {
			return
		}
		c.agents.publish(e.Event)
		h.relay(e.SessionID, e.Event)
	case agent.EventSubagentEnd:
		c, err := h.session(e.SessionID)
		if err != nil {
			return
		}
		if e.Status == agent.SubagentBackground {
			c.toBackground()
			return
		}
		h.endChildren(e.SessionID, StopInterrupted, true)
		h.untrack(parentID, c)
		c.end(stopOfChild(e.Status), childError(e))
	}
}

// child returns the session of the child id, and adds it to the host if
// the host does not know it. The driver created the session in the
// store before its start. A host that closed takes no new child, because
// Close stops only the sessions that it knows, so it returns nil.
func (h *Host) child(id string, ref agent.ParentRef, model string) *hosted {
	h.mu.Lock()
	defer h.mu.Unlock()
	if s, ok := h.sessions[id]; ok {
		return s
	}
	if h.closed {
		return nil
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
	s.inBackground = false
	s.sess.StopReason = ""
	s.sess.Error = ""
	s.updateLocked()
}

// toBackground marks the child s as a child that runs after the run of
// its parent.
func (s *hosted) toBackground() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inBackground = true
}

// background reports whether the child s runs in the background.
func (s *hosted) background() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inBackground
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

// track keeps c as a child of parentID that runs.
func (h *Host) track(parentID string, c *hosted) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.running == nil {
		h.running = make(map[string]map[string]*hosted)
	}
	if h.running[parentID] == nil {
		h.running[parentID] = make(map[string]*hosted)
	}
	h.running[parentID][c.id] = c
}

// untrack forgets c as a child of parentID that runs.
func (h *Host) untrack(parentID string, c *hosted) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.running[parentID], c.id)
	if len(h.running[parentID]) == 0 {
		delete(h.running, parentID)
	}
}

// endChildren ends each child of the session parentID that still runs,
// with reason. A run that ends with a child open, such as a run that
// failed, leaves no child running. A child in the background goes on,
// unless all is true, as when the session stops. A nested child ends
// before its owner.
func (h *Host) endChildren(parentID string, reason StopReason, all bool) {
	h.mu.Lock()
	kids := slices.Collect(maps.Values(h.running[parentID]))
	h.mu.Unlock()
	slices.SortFunc(kids, func(a, b *hosted) int { return cmp.Compare(a.id, b.id) })
	for _, c := range kids {
		if !all && c.background() {
			continue
		}
		h.untrack(parentID, c)
		h.endChildren(c.id, reason, true)
		c.end(reason, "")
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

// RunsChildren reports whether a child of the session id runs, such as
// a child in the background after the run of its parent.
func (h *Host) RunsChildren(id string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.running[id]) > 0
}

// idle applies an event that the agent of s sends between runs: an
// event of a child in the background. A session that stops takes none,
// because stop ends its children.
func (s *hosted) idle(ev agent.Event) {
	s.mu.Lock()
	closing := s.closing
	s.mu.Unlock()
	if closing {
		return
	}
	s.host.relay(s.id, ev)
	s.agents.publish(ev)
}
