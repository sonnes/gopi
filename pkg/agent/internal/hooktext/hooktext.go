// Package hooktext turns the values of a gopi hook into the text that
// an agent CLI takes from a hook, and back. A CLI takes only text from
// a hook, so a hook of a CLI agent gives text entries only.
package hooktext

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/session"
)

// ErrNotText reports an entry of a hook that is not a text message.
var ErrNotText = errors.New("a hook entry of a CLI agent must be a text message")

// separator joins the texts of two entries.
const separator = "\n\n"

// Entries returns the text of the entries of a hook. Each entry must be
// a message with text blocks only. Otherwise Entries returns
// [ErrNotText].
func Entries(entries []session.Entry) (string, error) {
	texts := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.Kind != "" {
			return "", ErrNotText
		}
		for _, c := range e.Message.Content {
			if _, ok := c.(ai.Text); !ok {
				return "", ErrNotText
			}
		}
		texts = append(texts, e.Message.Text())
	}
	return strings.Join(texts, separator), nil
}

// Join returns the texts that are not empty, joined.
func Join(texts []string) string {
	var out []string
	for _, t := range texts {
		if t != "" {
			out = append(out, t)
		}
	}
	return strings.Join(out, separator)
}

// Response returns the tool_response of a hook input as text: a JSON
// string as its value, and any other JSON as it is.
func Response(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return string(raw)
}

// Result returns the text of the text blocks of r.
func Result(r ai.ToolResult) string {
	var b strings.Builder
	for _, c := range r.Content {
		if t, ok := c.(ai.Text); ok {
			b.WriteString(t.Text)
		}
	}
	return b.String()
}

// ToolResult returns a result with text.
func ToolResult(text string) ai.ToolResult {
	return ai.ToolResult{Content: []ai.Content{ai.Text{Text: text}}}
}
