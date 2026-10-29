package agent

import (
	"context"
	"slices"
	"strings"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/session"
)

// Parent is the session value of a child session that names its
// parent. A UI reads it to find the children of a session. The driver
// that runs the subagent writes it when it creates the child; a caller
// does not. It lives in this package, so that every driver can write
// it.
var Parent = session.Value[ParentRef]{
	Namespace: "gopi.agent",
	Key:       "parent",
}

// ParentRef names the session and the tool call that started a child
// session.
type ParentRef struct {
	// SessionID is the ID of the parent session.
	SessionID string `json:"session_id"`
	// ToolCallID is the ID of the tool call that started the child.
	ToolCallID string `json:"tool_call_id"`
}

// ChildID returns the ID of a child session of parent: the parent ID,
// ".", name, "-", and suffix. A driver takes the suffix from the native
// ID of the subagent if it has one, so that a replay of the same output
// finds the same child. If parent is "", the parent is unsaved, and
// ChildID returns "": the child is unsaved too.
func ChildID(parentID, name, suffix string) string {
	if parentID == "" {
		return ""
	}
	return parentID + "." + name + "-" + suffix
}

// The status values of [EventSubagentEnd].
const (
	// SubagentCompleted is a child that ended its work.
	SubagentCompleted = "completed"
	// SubagentFailed is a child that ended with an error.
	SubagentFailed = "failed"
	// SubagentInterrupted is a child that a stop or an interrupt ended.
	SubagentInterrupted = "interrupted"
	// SubagentBackground is a child that goes on after the run that
	// carries it, such as a Claude Code subagent in the background. The
	// next run or the idle events of the agent send a new start of the
	// same child, and then its end. So the stream of each run stays
	// balanced.
	SubagentBackground = "background"
)

// EventSubagentStart marks the start of a subagent under a tool call
// of the run. A child that starts again, such as a Codex child that
// gets more input, sends a new start.
type EventSubagentStart struct {
	// ToolCallID is the call that started the child.
	ToolCallID string
	// SessionID is the ID of the child session. It is "" for an unsaved
	// child.
	SessionID string
	// Name is the name of the subagent, such as its agent file.
	Name string
	// Title is a short title for a UI. It is "" if the call has none.
	Title string
	// Prompt is the task that the child got.
	Prompt string
	// Model is the model of the child, if the driver knows it.
	Model string
}

// EventSubagent carries one event of the run of a child. The order
// rules of a run apply inside each child, and not across the parent
// and the child.
type EventSubagent struct {
	ToolCallID string
	SessionID  string
	Event      Event
}

// EventSubagentEnd marks the end of a subagent.
type EventSubagentEnd struct {
	ToolCallID string
	SessionID  string
	// Status is [SubagentCompleted], [SubagentFailed],
	// [SubagentInterrupted], or [SubagentBackground].
	Status string
	// Text is the last text of the child, from [LastText]. For
	// [SubagentFailed], it is the error.
	Text string
}

// LastText returns the text of the last assistant message of msgs that
// has text, with no space at its ends. It returns "" if no message has
// text.
func LastText(msgs []ai.Message) string {
	for _, m := range slices.Backward(msgs) {
		if m.Role != ai.RoleAssistant {
			continue
		}
		if text := strings.TrimSpace(m.Text()); text != "" {
			return text
		}
	}
	return ""
}

func (EventSubagentStart) event() {}
func (EventSubagent) event()      {}
func (EventSubagentEnd) event()   {}

// MarshalJSON encodes the event with its type.
func (e EventSubagentStart) MarshalJSON() ([]byte, error) { return marshalEvent(e) }

// MarshalJSON encodes the event with its type.
func (e EventSubagent) MarshalJSON() ([]byte, error) { return marshalEvent(e) }

// MarshalJSON encodes the event with its type.
func (e EventSubagentEnd) MarshalJSON() ([]byte, error) { return marshalEvent(e) }

// subagentSink is the context key of the function that sends the
// subagent events of one tool call.
type subagentSink struct{}

// sink sends one event of a tool call to the run stream, with the ID of
// the call.
type sink struct {
	callID string
	emit   func(Event)
}

// withSubagentSink returns ctx with the sink of one tool call.
func withSubagentSink(ctx context.Context, callID string, emit func(Event)) context.Context {
	return context.WithValue(ctx, subagentSink{}, sink{
		callID: callID,
		emit:   emit,
	})
}

// EmitSubagent sends e on the run stream of the tool call of ctx, with
// the ID of that call. A tool that runs a subagent, such as the agent
// tool of the harness, calls it. The tool sends the start, the events
// of the child, and the end before it returns. e must be an
// [EventSubagentStart], an [EventSubagent], or an [EventSubagentEnd],
// so that a tool cannot break the order of the run. EmitSubagent
// reports whether the run took e. Outside a tool call of an [Agent], it
// takes nothing.
func EmitSubagent(ctx context.Context, e Event) bool {
	s, ok := ctx.Value(subagentSink{}).(sink)
	if !ok {
		return false
	}
	switch v := e.(type) {
	case EventSubagentStart:
		v.ToolCallID = s.callID
		s.emit(v)
	case EventSubagent:
		v.ToolCallID = s.callID
		s.emit(v)
	case EventSubagentEnd:
		v.ToolCallID = s.callID
		s.emit(v)
	default:
		return false
	}
	return true
}
