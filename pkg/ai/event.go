package ai

// StopReason says why the model stopped.
type StopReason string

const (
	// StopReasonStop is a natural end of the turn.
	StopReasonStop StopReason = "stop"
	// StopReasonLength is a stop at the output cap or at the end of the
	// context window.
	StopReasonLength StopReason = "length"
	// StopReasonToolUse is a stop that waits for tool results.
	StopReasonToolUse StopReason = "tool_use"
	// StopReasonRefusal is a stop by a safety classifier. The message can
	// hold partial content, and a caller must not run its tool calls.
	// [Message.StopDetail] holds the explanation, if the API gives one.
	StopReasonRefusal StopReason = "refusal"
	// StopReasonAborted is a stop because the caller canceled the call. A
	// provider never sets it. A caller that keeps a partial message sets
	// it, so a later reader knows that the message is incomplete.
	StopReasonAborted StopReason = "aborted"
	// StopReasonError is a stop because the call failed. A provider never
	// sets it. A caller that keeps a partial message sets it, and puts
	// the error in [Message.StopDetail].
	StopReasonError StopReason = "error"
)

// EventType names what happened in a stream.
type EventType string

const (
	EventStart      EventType = "start"
	EventTextStart  EventType = "text_start"
	EventTextDelta  EventType = "text_delta"
	EventTextEnd    EventType = "text_end"
	EventThinkStart EventType = "thinking_start"
	EventThinkDelta EventType = "thinking_delta"
	EventThinkEnd   EventType = "thinking_end"
	EventToolStart  EventType = "tool_start"
	EventToolDelta  EventType = "tool_delta"
	EventToolEnd    EventType = "tool_end"
)

// Event is one thing that happened inside a model call. The final
// [Message] and the final error are not events. They are the result of
// the [EventStream].
//
// An Event round-trips through JSON, so an application can forward a
// stream to a client. The encoder omits a zero field.
type Event struct {
	Type EventType `json:"type"`
	// ContentIndex is the index of the content block that the event
	// belongs to. It applies to start, delta, and end events.
	ContentIndex int `json:"content_index,omitempty"`
	// Delta is the new text of a text, thinking, or tool delta event.
	Delta string `json:"delta,omitempty"`
	// Content is the full text of a text block. It is set on the end
	// event.
	Content string `json:"content,omitempty"`
	// ToolCall is the full call. It is set on the tool end event.
	ToolCall *ToolCall `json:"tool_call,omitempty"`
}
