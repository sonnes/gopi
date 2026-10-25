package claude

import "encoding/json"

// The types in this file are the parts of the stream-json protocol that
// the agent reads and writes. The agent ignores every other field.

// line is one line from the CLI. Type says which fields hold a value.
type line struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
	// SessionID is set on most lines. The agent reads it from the
	// system/init and result lines.
	SessionID string `json:"session_id"`
	// ParentToolUseID is set on the lines of a subagent.
	ParentToolUseID *string `json:"parent_tool_use_id"`

	// stream_event
	Event *streamEvent `json:"event"`

	// assistant and user
	Message *wireMessage `json:"message"`

	// result
	IsError bool      `json:"is_error"`
	Result  string    `json:"result"`
	Errors  []string  `json:"errors"`
	Usage   wireUsage `json:"usage"`

	// control_request
	RequestID string          `json:"request_id"`
	Request   *controlRequest `json:"request"`

	// control_response
	Response *controlResponse `json:"response"`
}

// streamEvent is one raw event of the Messages API stream.
type streamEvent struct {
	Type         string     `json:"type"`
	Index        int        `json:"index"`
	ContentBlock *wireBlock `json:"content_block"`
	Delta        struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		Thinking    string `json:"thinking"`
		PartialJSON string `json:"partial_json"`
		StopReason  string `json:"stop_reason"`
	} `json:"delta"`
	// Message is set on message_start.
	Message *wireMessage `json:"message"`
	// Usage is set on message_delta.
	Usage *wireUsage `json:"usage"`
}

// wireMessage is the message of an assistant or user line, and of a
// message_start event.
type wireMessage struct {
	ID    string `json:"id"`
	Model string `json:"model"`
	// Content is a list of blocks. A user line can also hold a string,
	// which the agent ignores.
	Content json.RawMessage `json:"content"`
	Usage   *wireUsage      `json:"usage"`
}

// blocks decodes the content blocks of m. Content that is not a list
// has no blocks.
func (m *wireMessage) blocks() []wireBlock {
	var blocks []wireBlock
	if err := json.Unmarshal(m.Content, &blocks); err != nil {
		return nil
	}
	return blocks
}

// wireBlock is one content block.
type wireBlock struct {
	Type string `json:"type"`

	// text
	Text string `json:"text"`

	// thinking
	Thinking  string `json:"thinking"`
	Signature string `json:"signature"`

	// tool_use
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`

	// tool_result. Content is a string or a list of blocks.
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

// wireUsage is the token usage of a message or a run.
type wireUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
}

// controlRequest is the request of a control_request line. The agent
// reads the subtype and the tool call of a permission prompt.
type controlRequest struct {
	Subtype   string `json:"subtype"`
	ToolUseID string `json:"tool_use_id"`
	ToolName  string `json:"tool_name"`
	// Input is the input of the tool call of a permission prompt, or
	// the input of a hook callback.
	Input json.RawMessage `json:"input"`
	// CallbackID names the callback of a hook_callback request.
	CallbackID string `json:"callback_id"`
	// PermissionSuggestions are the permission updates that the CLI
	// offers for "allow always".
	PermissionSuggestions json.RawMessage `json:"permission_suggestions"`
	// DecisionReason tells a person why the call asks. It can hold the
	// ANSI escapes of the terminal of the CLI.
	DecisionReason string `json:"decision_reason"`
	// SuppressAlwaysAllowRule reports that the rule for "allow always"
	// allows more than the call, such as each call of the tool.
	SuppressAlwaysAllowRule bool `json:"suppress_always_allow_rule"`
}

// controlResponse is the response of a control_response line.
// Response is the body of a success.
type controlResponse struct {
	Subtype   string          `json:"subtype"`
	RequestID string          `json:"request_id"`
	Error     string          `json:"error"`
	Response  json.RawMessage `json:"response"`
}

// outRequest is a control_request that the agent sends.
type outRequest struct {
	Type      string         `json:"type"`
	RequestID string         `json:"request_id"`
	Request   outRequestBody `json:"request"`
}

type outRequestBody struct {
	Subtype string `json:"subtype"`
	// Hooks registers the hook callbacks, in the initialize request.
	Hooks map[string][]hookMatcher `json:"hooks,omitempty"`
}

// outResponse is a control_response that the agent sends.
type outResponse struct {
	Type     string          `json:"type"`
	Response outResponseBody `json:"response"`
}

// outResponseBody is a success with Response set, or an error with
// Error set.
type outResponseBody struct {
	Subtype   string `json:"subtype"`
	RequestID string `json:"request_id"`
	Response  any    `json:"response,omitempty"`
	Error     string `json:"error,omitempty"`
}

// permission is the answer to a can_use_tool request. An allow has
// the input of the call, and a deny has a message.
type permission struct {
	Behavior     string          `json:"behavior"`
	Message      string          `json:"message,omitempty"`
	UpdatedInput json.RawMessage `json:"updatedInput,omitempty"`
	ToolUseID    string          `json:"toolUseID,omitempty"`
	// UpdatedPermissions are the permission updates that the CLI keeps
	// for the session, so that it does not ask again.
	UpdatedPermissions json.RawMessage `json:"updatedPermissions,omitempty"`
}

// outUser is a user line that the agent sends.
type outUser struct {
	Type            string         `json:"type"`
	Message         outUserMessage `json:"message"`
	ParentToolUseID *string        `json:"parent_tool_use_id"`
	SessionID       string         `json:"session_id"`
}

type outUserMessage struct {
	Role    string     `json:"role"`
	Content []outBlock `json:"content"`
}

// outBlock is one content block of a user line: text, an image, or a
// document.
type outBlock struct {
	Type   string     `json:"type"`
	Text   string     `json:"text,omitempty"`
	Source *outSource `json:"source,omitempty"`
}

// outSource is the base64 data of an image or a document block.
type outSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}
