package host

import (
	"slices"

	"github.com/sonnes/gopi/pkg/agent"
)

// Status is the status of a session.
type Status string

// The statuses of a session.
const (
	// StatusIdle: no run is active.
	StatusIdle Status = "idle"
	// StatusRunning: a run is active.
	StatusRunning Status = "running"
	// StatusRequiresAction: a run waits for the approve function.
	StatusRequiresAction Status = "requires_action"
)

// State is what a reader of the host knows about each session. A reader
// changes it only through [State.Apply], one event at a time.
//
// Apply changes the map and the slices in place, because the host
// applies each event, and a reader joins less often. A copy of a State
// with = shares them. To keep a state as it is, call [State.Clone].
type State struct {
	// Sessions has the state of each session of the host, by ID.
	Sessions map[string]SessionState
}

// SessionState is the state of one session.
type SessionState struct {
	// Model is the model spec of the session.
	Model string
	// Mode is the permission mode of the spec, or empty.
	Mode agent.Mode
	// Running is true from run_start to run_end.
	Running bool
	// Queued is the number of messages that wait for a run.
	Queued int
	// Waiting are the requests that wait for the approve function.
	Waiting []agent.ApprovalRequest
}

// Status returns the status of the session. An approval that waits
// comes first, because a person must act.
func (s SessionState) Status() Status {
	switch {
	case len(s.Waiting) > 0:
		return StatusRequiresAction
	case s.Running:
		return StatusRunning
	default:
		return StatusIdle
	}
}

// Apply changes the state by one event. Each event type makes one
// change:
//
//   - session_start adds the session. session_end removes it.
//   - input with [Queue] or [Interrupt] adds a queued message. input
//     with [Steer] goes into the active run, so it does not queue.
//   - run_start takes the first queued message. run_end ends the run.
//   - approval_start adds a waiting call. approval_end removes it.
//
// An event of a session that the state does not have changes nothing.
func (s *State) Apply(e Event) {
	id := e.SessionID()
	if start, ok := e.(EventSessionStart); ok {
		s.set(id, SessionState{
			Model: start.Model,
			Mode:  start.Mode,
		})
		return
	}
	cur, ok := s.Sessions[id]
	if !ok {
		return
	}
	switch e := e.(type) {
	case EventSessionEnd:
		delete(s.Sessions, id)
		return
	case EventInput:
		if e.Policy == Steer {
			return
		}
		cur.Queued++
	case EventRunStart:
		cur.Running = true
		cur.Queued = max(cur.Queued-1, 0)
	case EventRunEnd:
		cur.Running = false
	case EventApprovalStart:
		cur.Waiting = append(cur.Waiting, e.Request)
	case EventApprovalEnd:
		cur.Waiting = slices.DeleteFunc(
			cur.Waiting,
			func(r agent.ApprovalRequest) bool { return r.Call.ID == e.ToolCallID },
		)
	}
	s.set(id, cur)
}

// set sets the state of the session id.
func (s *State) set(id string, ss SessionState) {
	if s.Sessions == nil {
		s.Sessions = make(map[string]SessionState)
	}
	s.Sessions[id] = ss
}

// Clone returns a copy of s that shares no map and no slice with s.
func (s State) Clone() State {
	if s.Sessions == nil {
		return State{}
	}
	sessions := make(map[string]SessionState, len(s.Sessions))
	for id, ss := range s.Sessions {
		ss.Waiting = slices.Clone(ss.Waiting)
		sessions[id] = ss
	}
	return State{Sessions: sessions}
}
