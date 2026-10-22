package host

import (
	"encoding/json"
	"fmt"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
)

// Event is one change of the state of a session of the host. The
// interface is sealed, like [agent.Event].
// The types are [EventSessionStart], [EventSessionEnd], [EventInput],
// [EventRunStart], [EventRunEnd], [EventApprovalStart], and
// [EventApprovalEnd]. Each event carries what [State.Apply] needs.
//
// The events of the agent of a session are not host events.
// [Session.Events] has them.
//
// Each event type encodes to JSON with a "type" field that names the
// event. [UnmarshalEvent] decodes an event.
type Event interface {
	event()
	// SessionID returns the ID of the session of the event.
	SessionID() string
}

// EventSessionStart is the first event of a session in the host.
type EventSessionStart struct {
	Session string
	// Model is the model spec of the session.
	Model string
	// Mode is the permission mode of the spec. It is empty if the
	// driver keeps its own mode.
	Mode agent.Mode
	Dir  string
	// Resumed is set when the store had the log of the session.
	Resumed bool
}

// EventSessionEnd is the last event of a session in the host.
type EventSessionEnd struct {
	Session string
	// Error is the error of the close, or empty.
	Error string
}

// EventInput reports a message that the session accepted.
type EventInput struct {
	Session string
	Entries Message
	// Policy is the policy that the host used. A message with [Steer]
	// that no run can read has [Queue].
	Policy Policy
}

// EventRunStart marks the start of a run of the session.
type EventRunStart struct {
	Session string
}

// EventRunEnd marks the end of a run. It arrives on a failure too, so
// the pair stays balanced.
type EventRunEnd struct {
	Session string
	// Usage is the usage of every model call of the run, summed.
	Usage ai.Usage
	// Error is the error of a run that failed or that a caller
	// interrupted. It is a string, so that the event round-trips
	// through JSON.
	Error string
}

// EventApprovalStart marks a tool call that waits for the approve
// function of the session.
type EventApprovalStart struct {
	Session string
	Call    ai.ToolCall
}

// EventApprovalEnd closes an approval. It arrives on an error too, so
// the pair stays balanced.
type EventApprovalEnd struct {
	Session    string
	ToolCallID string
	Decision   agent.Decision
	// Error is the error of the approve function, or empty.
	Error string
}

func (EventSessionStart) event()  {}
func (EventSessionEnd) event()    {}
func (EventInput) event()         {}
func (EventRunStart) event()      {}
func (EventRunEnd) event()        {}
func (EventApprovalStart) event() {}
func (EventApprovalEnd) event()   {}

// SessionID returns the ID of the session.
func (e EventSessionStart) SessionID() string { return e.Session }

// SessionID returns the ID of the session.
func (e EventSessionEnd) SessionID() string { return e.Session }

// SessionID returns the ID of the session.
func (e EventInput) SessionID() string { return e.Session }

// SessionID returns the ID of the session.
func (e EventRunStart) SessionID() string { return e.Session }

// SessionID returns the ID of the session.
func (e EventRunEnd) SessionID() string { return e.Session }

// SessionID returns the ID of the session.
func (e EventApprovalStart) SessionID() string { return e.Session }

// SessionID returns the ID of the session.
func (e EventApprovalEnd) SessionID() string { return e.Session }

// MarshalJSON encodes the event with its type.
func (e EventSessionStart) MarshalJSON() ([]byte, error) { return marshalEvent(e) }

// MarshalJSON encodes the event with its type.
func (e EventSessionEnd) MarshalJSON() ([]byte, error) { return marshalEvent(e) }

// MarshalJSON encodes the event with its type.
func (e EventInput) MarshalJSON() ([]byte, error) { return marshalEvent(e) }

// MarshalJSON encodes the event with its type.
func (e EventRunStart) MarshalJSON() ([]byte, error) { return marshalEvent(e) }

// MarshalJSON encodes the event with its type.
func (e EventRunEnd) MarshalJSON() ([]byte, error) { return marshalEvent(e) }

// MarshalJSON encodes the event with its type.
func (e EventApprovalStart) MarshalJSON() ([]byte, error) { return marshalEvent(e) }

// MarshalJSON encodes the event with its type.
func (e EventApprovalEnd) MarshalJSON() ([]byte, error) { return marshalEvent(e) }

// The names of the events in JSON.
const (
	typeSessionStart  = "session_start"
	typeSessionEnd    = "session_end"
	typeInput         = "input"
	typeRunStart      = "run_start"
	typeRunEnd        = "run_end"
	typeApprovalStart = "approval_start"
	typeApprovalEnd   = "approval_end"
)

// eventJSON is the wire format of every event. Type tells the events
// apart. The encoder omits a field that the event does not have.
type eventJSON struct {
	Type    string `json:"type"`
	Session string `json:"session"`

	// session_start
	Model   string     `json:"model,omitempty"`
	Mode    agent.Mode `json:"mode,omitempty"`
	Dir     string     `json:"dir,omitempty"`
	Resumed bool       `json:"resumed,omitempty"`

	// input
	Entries Message `json:"entries,omitempty"`
	Policy  Policy  `json:"policy,omitempty"`

	// run_end
	Usage ai.Usage `json:"usage,omitzero"`

	// approval_start
	Call *callJSON `json:"call,omitempty"`

	// approval_end
	ToolCallID string          `json:"tool_call_id,omitempty"`
	Decision   *agent.Decision `json:"decision,omitempty"`

	// session_end, run_end, and approval_end
	Error string `json:"error,omitempty"`
}

// callJSON is the wire format of a tool call.
type callJSON struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input,omitempty"`
}

func marshalEvent(e Event) ([]byte, error) {
	w := eventJSON{Session: e.SessionID()}
	switch v := e.(type) {
	case EventSessionStart:
		w.Type = typeSessionStart
		w.Model = v.Model
		w.Mode = v.Mode
		w.Dir = v.Dir
		w.Resumed = v.Resumed
	case EventSessionEnd:
		w.Type = typeSessionEnd
		w.Error = v.Error
	case EventInput:
		w.Type = typeInput
		w.Entries = v.Entries
		w.Policy = v.Policy
	case EventRunStart:
		w.Type = typeRunStart
	case EventRunEnd:
		w.Type = typeRunEnd
		w.Usage = v.Usage
		w.Error = v.Error
	case EventApprovalStart:
		w.Type = typeApprovalStart
		w.Call = &callJSON{
			ID:    v.Call.ID,
			Name:  v.Call.Name,
			Input: v.Call.Input,
		}
	case EventApprovalEnd:
		w.Type = typeApprovalEnd
		w.ToolCallID = v.ToolCallID
		w.Decision = &v.Decision
		w.Error = v.Error
	default:
		return nil, fmt.Errorf("host: unknown event type %T", e)
	}
	return json.Marshal(w)
}

// UnmarshalEvent decodes an event that json.Marshal encoded. Go cannot
// decode JSON into an interface, so UnmarshalEvent reads the "type"
// field first. An unknown type is an error.
func UnmarshalEvent(data []byte) (Event, error) {
	var w eventJSON
	if err := json.Unmarshal(data, &w); err != nil {
		return nil, err
	}
	switch w.Type {
	case typeSessionStart:
		return EventSessionStart{
			Session: w.Session,
			Model:   w.Model,
			Mode:    w.Mode,
			Dir:     w.Dir,
			Resumed: w.Resumed,
		}, nil
	case typeSessionEnd:
		return EventSessionEnd{
			Session: w.Session,
			Error:   w.Error,
		}, nil
	case typeInput:
		return EventInput{
			Session: w.Session,
			Entries: w.Entries,
			Policy:  w.Policy,
		}, nil
	case typeRunStart:
		return EventRunStart{Session: w.Session}, nil
	case typeRunEnd:
		return EventRunEnd{
			Session: w.Session,
			Usage:   w.Usage,
			Error:   w.Error,
		}, nil
	case typeApprovalStart:
		var call ai.ToolCall
		if w.Call != nil {
			call = ai.ToolCall{
				ID:    w.Call.ID,
				Name:  w.Call.Name,
				Input: w.Call.Input,
			}
		}
		return EventApprovalStart{
			Session: w.Session,
			Call:    call,
		}, nil
	case typeApprovalEnd:
		var d agent.Decision
		if w.Decision != nil {
			d = *w.Decision
		}
		return EventApprovalEnd{
			Session:    w.Session,
			ToolCallID: w.ToolCallID,
			Decision:   d,
			Error:      w.Error,
		}, nil
	default:
		return nil, fmt.Errorf("host: unknown event type %q", w.Type)
	}
}
