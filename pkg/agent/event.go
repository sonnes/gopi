package agent

import (
	"encoding/json"
	"fmt"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/stream"
)

// Stream is the event stream of one run. Its final result is the
// messages that the run produced, in the order of the log. The input of
// the caller is not part of the result, because the caller already has
// it. The results that turn 1 adds for the interrupted tool calls of an
// earlier run come first, so a caller that keeps the result has an
// answer for each call.
type Stream = stream.Stream[Event, []ai.Message]

// Event is one thing that happened in a run. The interface is sealed,
// like [ai.Content], so a consumer reads only the fields that the event
// carries.
// The types are [EventAgentStart], [EventAgentEnd], [EventTurnStart],
// [EventTurnEnd], [EventMessageStart], [EventMessageUpdate],
// [EventMessageEnd], [EventToolExecutionStart], and
// [EventToolExecutionEnd]. Each event carries what [State.Apply]
// needs.
//
// Each event type encodes to JSON with a "type" field that names the
// event. [UnmarshalEvent] decodes an event.
type Event interface {
	event()
}

// EventAgentStart is the first event of a run.
type EventAgentStart struct{}

// EventAgentEnd is the last event of every run that starts, also of a
// run that fails. It closes every scope that is still open.
type EventAgentEnd struct {
	// Usage is the usage of every model call of the run, summed.
	Usage ai.Usage
	// Error is the error of a run that failed. It is a string, so that
	// the event round-trips through JSON.
	Error string
}

// EventTurnStart marks one model call and the tool calls that it makes.
type EventTurnStart struct {
	// Turn is the number of the turn. The first turn is 1.
	Turn int
}

// EventTurnEnd closes a turn. It arrives on a failure too, so the pair
// stays balanced.
type EventTurnEnd struct {
	// Message is the assistant message of the turn. It is nil if the
	// turn failed before the model sent a message.
	Message *ai.Message
	// ToolResults are the tool results of the turn, in the order in
	// which the calls ended. The interrupted results of turn 1 are not
	// in it.
	ToolResults []ai.Message
}

// EventMessageStart marks the first event of one message: the input, a
// message from the model, a tool result, a system change, or a message
// that the loop adds in the middle of a run.
type EventMessageStart struct {
	Message ai.Message
}

// EventMessageUpdate carries one model event and a snapshot of the
// partial message.
type EventMessageUpdate struct {
	// Message is a snapshot of the partial message.
	Message ai.Message
	// AssistantEvent is the model event behind the update.
	AssistantEvent ai.Event
}

// EventMessageEnd carries the final message. The log stores it before
// the event, unless it is ephemeral. [State.Apply] appends it to the
// messages of the state, unless it is an assistant message with no
// content.
type EventMessageEnd struct {
	Message ai.Message
	// Ephemeral marks a message of an ephemeral entry. The model reads
	// it in this run, and the store does not keep it. [State.Apply]
	// removes it at agent_end.
	Ephemeral bool
}

// EventToolExecutionStart marks the start of one tool call.
type EventToolExecutionStart struct {
	ToolCallID string
	ToolName   string
	// Args is the input of the call.
	Args json.RawMessage
}

// EventToolExecutionEnd carries the result of one tool call.
type EventToolExecutionEnd struct {
	ToolCallID string
	ToolName   string
	Result     ai.ToolResult
}

func (EventAgentStart) event()         {}
func (EventAgentEnd) event()           {}
func (EventTurnStart) event()          {}
func (EventTurnEnd) event()            {}
func (EventMessageStart) event()       {}
func (EventMessageUpdate) event()      {}
func (EventMessageEnd) event()         {}
func (EventToolExecutionStart) event() {}
func (EventToolExecutionEnd) event()   {}

// MarshalJSON encodes the event with its type.
func (e EventAgentStart) MarshalJSON() ([]byte, error) { return marshalEvent(e) }

// MarshalJSON encodes the event with its type.
func (e EventAgentEnd) MarshalJSON() ([]byte, error) { return marshalEvent(e) }

// MarshalJSON encodes the event with its type.
func (e EventTurnStart) MarshalJSON() ([]byte, error) { return marshalEvent(e) }

// MarshalJSON encodes the event with its type.
func (e EventTurnEnd) MarshalJSON() ([]byte, error) { return marshalEvent(e) }

// MarshalJSON encodes the event with its type.
func (e EventMessageStart) MarshalJSON() ([]byte, error) { return marshalEvent(e) }

// MarshalJSON encodes the event with its type.
func (e EventMessageUpdate) MarshalJSON() ([]byte, error) { return marshalEvent(e) }

// MarshalJSON encodes the event with its type.
func (e EventMessageEnd) MarshalJSON() ([]byte, error) { return marshalEvent(e) }

// MarshalJSON encodes the event with its type.
func (e EventToolExecutionStart) MarshalJSON() ([]byte, error) { return marshalEvent(e) }

// MarshalJSON encodes the event with its type.
func (e EventToolExecutionEnd) MarshalJSON() ([]byte, error) { return marshalEvent(e) }

// The names of the events in JSON.
const (
	typeAgentStart         = "agent_start"
	typeAgentEnd           = "agent_end"
	typeTurnStart          = "turn_start"
	typeTurnEnd            = "turn_end"
	typeMessageStart       = "message_start"
	typeMessageUpdate      = "message_update"
	typeMessageEnd         = "message_end"
	typeToolExecutionStart = "tool_execution_start"
	typeToolExecutionEnd   = "tool_execution_end"
)

// eventJSON is the wire format of every event. Type tells the events
// apart. The encoder omits a field that the event does not have.
type eventJSON struct {
	Type string `json:"type"`

	// turn_start
	Turn int `json:"turn,omitempty"`

	// agent_end
	Usage ai.Usage `json:"usage,omitzero"`
	Error string   `json:"error,omitempty"`

	// turn_end
	ToolResults []ai.Message `json:"tool_results,omitempty"`

	// message_start, message_update, message_end, and turn_end
	Message *ai.Message `json:"message,omitempty"`
	// message_update
	AssistantEvent *ai.Event `json:"assistant_event,omitempty"`
	// message_end
	Ephemeral bool `json:"ephemeral,omitempty"`

	// tool_execution_start and tool_execution_end
	ToolCallID string          `json:"tool_call_id,omitempty"`
	ToolName   string          `json:"tool_name,omitempty"`
	Args       json.RawMessage `json:"args,omitempty"`
	Result     *ai.ToolResult  `json:"result,omitempty"`
}

func marshalEvent(e Event) ([]byte, error) {
	var w eventJSON
	switch v := e.(type) {
	case EventAgentStart:
		w = eventJSON{Type: typeAgentStart}
	case EventAgentEnd:
		w = eventJSON{
			Type:  typeAgentEnd,
			Usage: v.Usage,
			Error: v.Error,
		}
	case EventTurnStart:
		w = eventJSON{Type: typeTurnStart, Turn: v.Turn}
	case EventTurnEnd:
		w = eventJSON{
			Type:        typeTurnEnd,
			Message:     v.Message,
			ToolResults: v.ToolResults,
		}
	case EventMessageStart:
		w = eventJSON{Type: typeMessageStart, Message: &v.Message}
	case EventMessageUpdate:
		w = eventJSON{
			Type:           typeMessageUpdate,
			Message:        &v.Message,
			AssistantEvent: &v.AssistantEvent,
		}
	case EventMessageEnd:
		w = eventJSON{
			Type:      typeMessageEnd,
			Message:   &v.Message,
			Ephemeral: v.Ephemeral,
		}
	case EventToolExecutionStart:
		w = eventJSON{
			Type:       typeToolExecutionStart,
			ToolCallID: v.ToolCallID,
			ToolName:   v.ToolName,
			Args:       v.Args,
		}
	case EventToolExecutionEnd:
		w = eventJSON{
			Type:       typeToolExecutionEnd,
			ToolCallID: v.ToolCallID,
			ToolName:   v.ToolName,
			Result:     &v.Result,
		}
	default:
		return nil, fmt.Errorf("agent: unknown event type %T", e)
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
	case typeAgentStart:
		return EventAgentStart{}, nil
	case typeAgentEnd:
		return EventAgentEnd{Usage: w.Usage, Error: w.Error}, nil
	case typeTurnStart:
		return EventTurnStart{Turn: w.Turn}, nil
	case typeTurnEnd:
		return EventTurnEnd{Message: w.Message, ToolResults: w.ToolResults}, nil
	case typeMessageStart:
		return EventMessageStart{Message: deref(w.Message)}, nil
	case typeMessageUpdate:
		return EventMessageUpdate{
			Message:        deref(w.Message),
			AssistantEvent: deref(w.AssistantEvent),
		}, nil
	case typeMessageEnd:
		return EventMessageEnd{
			Message:   deref(w.Message),
			Ephemeral: w.Ephemeral,
		}, nil
	case typeToolExecutionStart:
		return EventToolExecutionStart{
			ToolCallID: w.ToolCallID,
			ToolName:   w.ToolName,
			Args:       w.Args,
		}, nil
	case typeToolExecutionEnd:
		return EventToolExecutionEnd{
			ToolCallID: w.ToolCallID,
			ToolName:   w.ToolName,
			Result:     deref(w.Result),
		}, nil
	default:
		return nil, fmt.Errorf("agent: unknown event type %q", w.Type)
	}
}

// deref returns *p, or the zero value if p is nil.
func deref[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}
