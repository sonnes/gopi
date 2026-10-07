package ai

import (
	"encoding/json"
	"fmt"
)

// Content is one block of a [Message]. The interface is sealed. The
// types are [Text], [ThinkingBlock], [ToolCall], [File], [ToolAdded],
// [ToolRemoved], [Section], and [SectionRemoved].
type Content interface {
	content()
}

// Text is a block of text.
type Text struct {
	Text string
}

func (Text) content() {}

// ThinkingBlock is a block of reasoning from the model. A provider that
// signs its thinking sets Signature. A later turn sends the block back
// unchanged, so the provider can verify the signature.
type ThinkingBlock struct {
	Thinking  string
	Signature string
}

func (ThinkingBlock) content() {}

// ToolCall is a request from the model to run a tool.
//
// Input is the raw JSON that the model wrote. A provider streams the
// input as text, and [Tool.Run] decodes it into the typed input of the
// tool. A map in between decodes the input twice.
type ToolCall struct {
	ID    string
	Name  string
	Input json.RawMessage
}

func (ToolCall) content() {}

// File is a block of binary data, such as an image or a PDF. MediaType
// names the format, for example image/png or application/pdf.
//
// A tool result carries a File, so that a tool can show the model an
// image or a document. A provider drops a File in another message.
type File struct {
	MediaType string
	Data      []byte
}

func (File) content() {}

// ToolAdded is a tool that the model can call from the place of a
// system message. The [Tools] option holds the tools of the first
// call. A ToolAdded block adds one tool after that, and a ToolAdded
// block for a tool that the model can call already replaces its
// definition.
type ToolAdded struct {
	Tool ToolInfo
}

func (ToolAdded) content() {}

// ToolRemoved is a tool that the model cannot call from the place of a
// system message. Name is the name of the tool.
type ToolRemoved struct {
	Name string
}

func (ToolRemoved) content() {}

// Section is a named part of the system prompt. The [SystemPrompt]
// option sets the first sections, and a system message sets one from its
// place. A provider sends Text as it is and does not read Name. The name
// lets a layer above match a later change to the section, and that layer
// writes any framing, such as an "updated" note, as a [Text] block.
//
// Wrap Text in a tag or start it with a heading, so that the model can
// match an update to the section.
type Section struct {
	Name string
	Text string
}

func (Section) content() {}

// SectionRemoved is a section that the system prompt does not have from
// the place of a system message. Name is the name of the section. A
// provider sends no text for it. The layer that removes a section also
// adds a [Text] block that tells the model.
type SectionRemoved struct {
	Name string
}

func (SectionRemoved) content() {}

// contentJSON is the wire format of every content block. Type tells the
// blocks apart.
type contentJSON struct {
	Type string `json:"type"`

	// text, and the text of section
	Text string `json:"text,omitempty"`

	// thinking
	Thinking  string `json:"thinking,omitempty"`
	Signature string `json:"signature,omitempty"`

	// tool_call, and the name of tool_removed, section, and
	// section_removed
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	// tool_added
	Tool *ToolInfo `json:"tool,omitempty"`

	// file
	MediaType string `json:"media_type,omitempty"`
	Data      []byte `json:"data,omitempty"`
}

func marshalContent(c Content) (contentJSON, error) {
	switch v := c.(type) {
	case Text:
		return contentJSON{Type: "text", Text: v.Text}, nil
	case ThinkingBlock:
		return contentJSON{
			Type:      "thinking",
			Thinking:  v.Thinking,
			Signature: v.Signature,
		}, nil
	case ToolCall:
		return contentJSON{
			Type:  "tool_call",
			ID:    v.ID,
			Name:  v.Name,
			Input: v.Input,
		}, nil
	case File:
		return contentJSON{
			Type:      "file",
			MediaType: v.MediaType,
			Data:      v.Data,
		}, nil
	case ToolAdded:
		return contentJSON{Type: "tool_added", Tool: &v.Tool}, nil
	case ToolRemoved:
		return contentJSON{Type: "tool_removed", Name: v.Name}, nil
	case Section:
		return contentJSON{Type: "section", Name: v.Name, Text: v.Text}, nil
	case SectionRemoved:
		return contentJSON{Type: "section_removed", Name: v.Name}, nil
	default:
		return contentJSON{}, fmt.Errorf("ai: unknown content type %T", c)
	}
}

func unmarshalContent(c contentJSON) (Content, error) {
	switch c.Type {
	case "text":
		return Text{Text: c.Text}, nil
	case "thinking":
		return ThinkingBlock{Thinking: c.Thinking, Signature: c.Signature}, nil
	case "tool_call":
		return ToolCall{ID: c.ID, Name: c.Name, Input: c.Input}, nil
	case "file":
		return File{MediaType: c.MediaType, Data: c.Data}, nil
	case "tool_added":
		if c.Tool == nil {
			return nil, fmt.Errorf("ai: tool_added has no tool")
		}
		return ToolAdded{Tool: *c.Tool}, nil
	case "tool_removed":
		return ToolRemoved{Name: c.Name}, nil
	case "section":
		return Section{Name: c.Name, Text: c.Text}, nil
	case "section_removed":
		return SectionRemoved{Name: c.Name}, nil
	default:
		return nil, fmt.Errorf("ai: unknown content type %q", c.Type)
	}
}

func marshalContents(blocks []Content) ([]contentJSON, error) {
	out := make([]contentJSON, 0, len(blocks))
	for _, c := range blocks {
		j, err := marshalContent(c)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, nil
}

func unmarshalContents(blocks []contentJSON) ([]Content, error) {
	if len(blocks) == 0 {
		return nil, nil
	}
	out := make([]Content, 0, len(blocks))
	for _, j := range blocks {
		c, err := unmarshalContent(j)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}
