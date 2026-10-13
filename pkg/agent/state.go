package agent

import (
	"encoding/json"
	"errors"
	"slices"

	"github.com/sonnes/gopi/internal/transcript"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/session"
)

// State is what an agent knows: the messages that the model reads, the
// progress of the run, the queues, and the loaded tools. A run changes
// the messages and the progress only through [State.Apply], one event
// at a time. So a viewer that applies the same events to a snapshot
// gets the same messages and progress.
//
// The queues and the loaded tools change with no event, because a
// caller can queue an entry when no run is active. A snapshot does not
// hold them.
//
// A State is a value. [Agent.State] and every hook get a copy. A copy
// shares its arrays with the agent and costs no allocation. This is
// safe because the agent only appends to a slice past the length of
// the copy, or replaces it. A caller must not change a message that
// [State.Messages] returns. Every copy points to the queues of the
// agent, so [State.Steer] on a copy queues for the agent.
type State struct {
	// messages are what the model reads: the messages of the log, and
	// the ephemeral messages of the run. During a run the agent only
	// appends to it.
	messages []ai.Message
	// ephemeral are the indexes of the ephemeral messages of the run.
	// agent_end removes them.
	ephemeral []int
	// log is the log of the agent, without Append. A state that a
	// viewer decoded has none.
	log session.Writer

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

	// queues are the queues of the agent. Every copy points to them. A
	// state that a viewer decoded has none.
	queues *queues
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
//   - agent_end ends the run, clears what is open, removes the
//     ephemeral messages, and sets the usage and the error. Then the
//     messages are the messages of the log.
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
		if e.Ephemeral {
			s.ephemeral = append(slices.Clip(s.ephemeral), len(s.messages))
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
		s.dropEphemeral()
		s.usage = e.Usage
		s.err = nil
		if e.Error != "" {
			s.err = errors.New(e.Error)
		}
	}
}

// dropEphemeral removes the ephemeral messages. It builds a new slice,
// because a copy of the state can share the old one.
func (s *State) dropEphemeral() {
	if len(s.ephemeral) == 0 {
		return
	}
	kept := make([]ai.Message, 0, len(s.messages)-len(s.ephemeral))
	for i, m := range s.messages {
		if !slices.Contains(s.ephemeral, i) {
			kept = append(kept, m)
		}
	}
	s.messages = kept
	s.ephemeral = nil
}

// stream sets the partial message to msg if msg is from the model.
func (s *State) stream(msg ai.Message) {
	if msg.Role == ai.RoleAssistant {
		s.streaming = &msg
	}
}

// Clip returns s with its slices capped at their length, so an
// [State.Apply] on the result never writes into the arrays of s.
// [Agent.State] returns a clipped copy. A CLI agent that keeps its own
// State returns one too.
func (s State) Clip() State {
	s.messages = slices.Clip(s.messages)
	s.ephemeral = slices.Clip(s.ephemeral)
	s.pending = slices.Clip(s.pending)
	return s
}

// Messages returns what the model reads: the messages of the log, and
// the ephemeral messages of the active run in their places. A caller
// must not change them.
func (s State) Messages() []ai.Message { return s.messages }

// Steer queues entries that the agent reads before its next model
// call, as [Agent.Steer] does. A hook steers the run through its copy
// of the state. A state that a viewer decoded has no queues, so Steer
// does nothing.
func (s State) Steer(entries ...session.Entry) {
	if s.queues != nil {
		s.queues.add(&s.queues.steer, entries)
	}
}

// FollowUp queues entries that the agent reads when the run would
// stop, as [Agent.FollowUp] does. A state that a viewer decoded has no
// queues, so FollowUp does nothing.
func (s State) FollowUp(entries ...session.Entry) {
	if s.queues != nil {
		s.queues.add(&s.queues.followUp, entries)
	}
}

// Log returns the log of the agent. It has each entry that the agent
// stored before the hook started. The log is live, so during parallel
// tool calls it can also have a result that [State.Messages] of the
// copy does not have. A hook reads the entries, the
// messages, and the values from it, and it sets values through it. It
// cannot append an entry, because each entry of a run goes through the
// agent, which sends its events in order. A hook that adds entries
// returns them from AfterTurn or BeforeStop.
//
// A value write reaches the store when [session.Value.Set] returns,
// also if the run fails after it. The log is not part of the JSON, so
// a state that a viewer decoded returns nil.
func (s State) Log() session.Writer { return s.log }

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
	Ephemeral []int         `json:"ephemeral,omitempty"`
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
		Ephemeral: s.ephemeral,
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
		ephemeral: w.Ephemeral,
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
