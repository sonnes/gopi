package agent

import (
	"encoding/json"
	"errors"
	"slices"

	"github.com/sonnes/gopi/internal/transcript"
	"github.com/sonnes/gopi/pkg/ai"
)

// State is what an agent knows: the messages that the model reads, the
// progress of the run, the queues, and the loaded tools. A run changes
// the messages and the progress only through [State.Apply], one event
// at a time. So a viewer that applies the same events to a snapshot
// gets the same messages and progress.
//
// The queues and the loaded tools change with no event, because a
// caller can queue a message when no run is active. A snapshot does not
// hold them.
//
// A State is a value. [Agent.State] and every hook get a copy. A copy
// shares its arrays with the agent and costs no allocation. This is
// safe because the agent only appends to a slice past the length of
// the copy, or replaces it. A caller must not change a message that
// [State.Messages] returns.
type State struct {
	// messages are what the model reads: the messages of [History],
	// then each message of each run. The agent only appends to it.
	messages []ai.Message

	// running is true from agent_start to agent_end. A second run
	// fails while it is true.
	running bool
	turn    int
	// streaming is the partial assistant message, or nil.
	streaming *ai.Message
	// pending are the tool calls that started and did not end. Apply
	// replaces the slice and never changes it in place.
	pending []ai.ToolCall
	usage   ai.Usage
	err     error

	// steer and followUp are the queues of [Agent.Steer] and
	// [Agent.FollowUp].
	steer    []ai.Message
	followUp []ai.Message
	// loaded are the deferred tools that the search loaded. The next
	// model call declares them. A run can end before that call, so the
	// list outlives the run.
	loaded []string
}

// Apply changes the state by one event. Each event type makes one
// change:
//
//   - agent_start starts a run: running, turn 0, no usage, no error.
//   - turn_start sets the turn.
//   - message_start and message_update set the partial message.
//   - message_end clears it, adds its usage, and appends the message.
//     An assistant message with no content is not appended, because a
//     provider rejects it.
//   - tool_execution_start adds a pending tool call. tool_execution_end
//     removes it.
//   - agent_end ends the run, clears what is open, and sets the usage
//     and the error.
func (s *State) Apply(e Event) {
	switch e := e.(type) {
	case EventAgentStart:
		s.running = true
		s.turn = 0
		s.usage = ai.Usage{}
		s.err = nil
		s.streaming = nil
		s.pending = nil
	case EventTurnStart:
		s.turn = e.Turn
	case EventMessageStart:
		s.stream(e.Message)
	case EventMessageUpdate:
		s.stream(e.Message)
	case EventMessageEnd:
		s.streaming = nil
		s.usage = s.usage.Add(e.Message.Usage)
		empty := e.Message.Role == ai.RoleAssistant && len(e.Message.Content) == 0
		if empty {
			return
		}
		s.messages = append(s.messages, e.Message)
	case EventToolExecutionStart:
		call := ai.ToolCall{
			ID:    e.ToolCallID,
			Name:  e.ToolName,
			Input: e.Args,
		}
		s.pending = append(slices.Clip(s.pending), call)
	case EventToolExecutionEnd:
		// A copy of the state can share the slice, so the delete works
		// on a clone.
		s.pending = slices.DeleteFunc(slices.Clone(s.pending), func(c ai.ToolCall) bool {
			return c.ID == e.ToolCallID
		})
	case EventAgentEnd:
		s.running = false
		s.streaming = nil
		s.pending = nil
		s.usage = e.Usage
		s.err = nil
		if e.Error != "" {
			s.err = errors.New(e.Error)
		}
	}
}

// stream sets the partial message to msg if msg is from the model.
func (s *State) stream(msg ai.Message) {
	if msg.Role == ai.RoleAssistant {
		s.streaming = &msg
	}
}

// Messages returns what the model reads: the history, then each message
// of each run. A caller must not change them.
func (s State) Messages() []ai.Message { return s.messages }

// Tools returns the tools that the model can call after
// [State.Messages]: the tools of the base, then the changes of the
// later system messages.
func (s State) Tools() []ai.ToolInfo { return transcript.CurrentTools(nil, s.messages) }

// Sections returns the sections of the system prompt after
// [State.Messages].
func (s State) Sections() []ai.Section { return currentSections(s.messages) }

// Turn returns the turn of the run. The first turn is 1. It is 0
// before the first model call of a run.
func (s State) Turn() int { return s.turn }

// Running reports if a run is active.
func (s State) Running() bool { return s.running }

// Streaming returns the partial assistant message, or nil if the model
// sends no message now.
func (s State) Streaming() *ai.Message { return s.streaming }

// Pending returns the tool calls that started and did not end. A
// caller must not change them.
func (s State) Pending() []ai.ToolCall { return s.pending }

// Usage returns the usage of the run, summed.
func (s State) Usage() ai.Usage { return s.usage }

// Err returns the error of the last run, or nil if it did not fail.
func (s State) Err() error { return s.err }

// stateJSON is the wire form of a [State].
type stateJSON struct {
	Messages  []ai.Message  `json:"messages,omitempty"`
	Running   bool          `json:"running,omitempty"`
	Turn      int           `json:"turn,omitempty"`
	Streaming *ai.Message   `json:"streaming,omitempty"`
	Pending   []ai.ToolCall `json:"pending,omitempty"`
	Usage     ai.Usage      `json:"usage,omitzero"`
	Error     string        `json:"error,omitempty"`
}

// MarshalJSON encodes a snapshot of the state for a viewer.
func (s State) MarshalJSON() ([]byte, error) {
	w := stateJSON{
		Messages:  s.messages,
		Running:   s.running,
		Turn:      s.turn,
		Streaming: s.streaming,
		Pending:   s.pending,
		Usage:     s.usage,
	}
	if s.err != nil {
		w.Error = s.err.Error()
	}
	return json.Marshal(w)
}

// UnmarshalJSON decodes a snapshot that [State.MarshalJSON] encoded.
func (s *State) UnmarshalJSON(data []byte) error {
	var w stateJSON
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	*s = State{
		messages:  w.Messages,
		running:   w.Running,
		turn:      w.Turn,
		streaming: w.Streaming,
		pending:   w.Pending,
		usage:     w.Usage,
	}
	if w.Error != "" {
		s.err = errors.New(w.Error)
	}
	return nil
}
