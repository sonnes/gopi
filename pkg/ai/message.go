package ai

import (
	"encoding/json"
	"slices"
	"strings"
)

// Role names who produced a message.
type Role string

const (
	RoleSystem     Role = "system"
	RoleUser       Role = "user"
	RoleAssistant  Role = "assistant"
	RoleToolResult Role = "tool_result"
)

// Message is one turn of a conversation. A field that does not apply to
// the role holds its zero value.
type Message struct {
	Role    Role      `json:"role"`
	Content []Content `json:"content"`

	// Model, Usage, StopReason, StopDetail, and Effort apply to an
	// assistant message.
	Model      string     `json:"model,omitempty"`
	Usage      Usage      `json:"usage,omitzero"`
	StopReason StopReason `json:"stop_reason,omitempty"`
	// StopDetail is the explanation of the stop, if the API gives one.
	// A refusal can carry one.
	StopDetail string `json:"stop_detail,omitempty"`
	// Effort is the effort that the model used, in the words of the
	// API of the provider. A provider sends it back with the message,
	// so a change of level does not change the cached prefix.
	Effort string `json:"effort,omitempty"`

	// ToolCallID and IsError apply to a tool result message.
	ToolCallID string `json:"tool_call_id,omitempty"`
	IsError    bool   `json:"is_error,omitempty"`
}

// SystemMessage returns a system message with the given blocks, in
// order. A [Text] block adds instructions to the [SystemPrompt] from
// the place of the message. A [Section] or [SectionRemoved] block
// changes a named part of the prompt. A [ToolAdded] or [ToolRemoved]
// block changes the tools that the model can call. A provider sends the
// message in its place, so the start of the request stays the same, and
// a provider can cache it.
func SystemMessage(content ...Content) Message {
	return Message{
		Role:    RoleSystem,
		Content: content,
	}
}

// CurrentTools returns the tools that the model can call after the
// messages: tools, then the [ToolAdded] and [ToolRemoved] blocks of the
// system messages, in order. A ToolAdded block for a tool in the list
// replaces the tool in its place. A new tool goes at the end.
func CurrentTools(tools []ToolInfo, messages []Message) []ToolInfo {
	out := slices.Clone(tools)
	for _, m := range messages {
		if m.Role != RoleSystem {
			continue
		}
		for _, c := range m.Content {
			switch v := c.(type) {
			case ToolAdded:
				i := slices.IndexFunc(out, func(t ToolInfo) bool { return t.Name == v.Tool.Name })
				if i >= 0 {
					out[i] = v.Tool
				} else {
					out = append(out, v.Tool)
				}
			case ToolRemoved:
				out = slices.DeleteFunc(out, func(t ToolInfo) bool { return t.Name == v.Name })
			}
		}
	}
	return out
}

// UserMessage returns a user message with one text block.
func UserMessage(text string) Message {
	return Message{
		Role:    RoleUser,
		Content: []Content{Text{Text: text}},
	}
}

// AssistantMessage returns an assistant message with the given blocks.
func AssistantMessage(content ...Content) Message {
	return Message{
		Role:    RoleAssistant,
		Content: content,
	}
}

// ToolResultMessage returns a tool result message for the call with
// toolCallID. If the tool failed, the caller sets IsError.
func ToolResultMessage(toolCallID string, content ...Content) Message {
	return Message{
		Role:       RoleToolResult,
		ToolCallID: toolCallID,
		Content:    content,
	}
}

// Text returns the text of the [Text] blocks, joined in order. It leaves
// out the thinking and tool call blocks.
func (m Message) Text() string {
	var b strings.Builder
	for _, c := range m.Content {
		if t, ok := c.(Text); ok {
			b.WriteString(t.Text)
		}
	}
	return b.String()
}

// ToolCalls returns the [ToolCall] blocks in order, or nil when there are
// none.
func (m Message) ToolCalls() []ToolCall {
	var calls []ToolCall
	for _, c := range m.Content {
		if tc, ok := c.(ToolCall); ok {
			calls = append(calls, tc)
		}
	}
	return calls
}

// message drops the methods of [Message] so that the marshaler does not
// call itself.
type message Message

// messageJSON is the wire format of a message. The tags of [Message]
// carry every field except Content. Content holds an interface, so it
// needs the tagged form of contentJSON.
type messageJSON struct {
	message
	Content []contentJSON `json:"content"`
}

// MarshalJSON encodes the message.
func (m Message) MarshalJSON() ([]byte, error) {
	content, err := marshalContents(m.Content)
	if err != nil {
		return nil, err
	}

	return json.Marshal(messageJSON{
		message: message(m),
		Content: content,
	})
}

// UnmarshalJSON decodes the message. An unknown content type is an error.
func (m *Message) UnmarshalJSON(data []byte) error {
	var j messageJSON
	if err := json.Unmarshal(data, &j); err != nil {
		return err
	}

	content, err := unmarshalContents(j.Content)
	if err != nil {
		return err
	}

	*m = Message(j.message)
	m.Content = content
	return nil
}
