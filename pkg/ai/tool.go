package ai

import (
	"encoding/json"

	"github.com/google/jsonschema-go/jsonschema"
)

// ToolInfo is what the model learns about a tool. Package tool builds
// a tool that runs from it.
type ToolInfo struct {
	Name        string             `json:"name"`
	Description string             `json:"description"`
	InputSchema *jsonschema.Schema `json:"input_schema"`

	// Deferred marks a tool that the model cannot call until a
	// [ToolAdded] block adds it. The agent reads it. It does not go to
	// the model, so it has no JSON.
	Deferred bool `json:"-"`

	// Sequential marks a tool that must not run at the same time as
	// another call of one message. The agent reads it. It does not go to
	// the model, so it has no JSON.
	Sequential bool `json:"-"`
}

// ToolResult is what a tool returns. Content holds the blocks that go
// back to the model. The agent turns a result into a tool result message
// without a conversion table.
type ToolResult struct {
	Content []Content
	IsError bool
}

// toolResultJSON is the wire format of a tool result.
type toolResultJSON struct {
	Content []contentJSON `json:"content"`
	IsError bool          `json:"is_error,omitempty"`
}

// MarshalJSON encodes the result.
func (r ToolResult) MarshalJSON() ([]byte, error) {
	content, err := marshalContents(r.Content)
	if err != nil {
		return nil, err
	}
	return json.Marshal(toolResultJSON{
		Content: content,
		IsError: r.IsError,
	})
}

// UnmarshalJSON decodes the result. An unknown content type is an error.
func (r *ToolResult) UnmarshalJSON(data []byte) error {
	var j toolResultJSON
	if err := json.Unmarshal(data, &j); err != nil {
		return err
	}

	content, err := unmarshalContents(j.Content)
	if err != nil {
		return err
	}

	*r = ToolResult{
		Content: content,
		IsError: j.IsError,
	}
	return nil
}
