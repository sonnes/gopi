// Package transcript holds the rules that every provider applies to the
// messages before it maps them to its API: the text of a system
// message, the reminder for a model that takes no system messages, the
// tools after the tool changes, and the results of tool calls that have
// none.
//
// The reasons are in docs/concepts/ai/message.md.
package transcript

import (
	"slices"
	"strings"

	"github.com/sonnes/gopi/pkg/ai"
)

// sep joins the parts of the text of a system message.
const sep = "\n\n"

// SystemText returns the text of a system message, with the parts
// joined by a blank line. A [ai.Text] block goes as it is. A
// [ai.Section] or [ai.SectionRemoved] block goes with a frame that names
// the section, so the model can relate it to the system prompt. The
// frame needs no state of earlier sections. Every other block adds no
// text.
func SystemText(content []ai.Content) string {
	var parts []string
	for _, c := range content {
		switch v := c.(type) {
		case ai.Text:
			parts = append(parts, v.Text)
		case ai.Section:
			parts = append(parts, `Updated system prompt section "`+v.Name+`":`+sep+v.Text)
		case ai.SectionRemoved:
			parts = append(parts, `Removed system prompt section "`+v.Name+`".`)
		}
	}
	parts = slices.DeleteFunc(parts, func(p string) bool { return p == "" })
	return strings.Join(parts, sep)
}

// CurrentTools returns the tools that the model can call after the
// messages: base, then the [ai.ToolAdded] and [ai.ToolRemoved] blocks
// of the system messages, in order. A ToolAdded block for a tool in the
// list replaces the tool in its place. A new tool goes at the end. A
// provider sends the result as its tool list when its API cannot change
// the tools in place.
func CurrentTools(base []ai.ToolInfo, messages []ai.Message) []ai.ToolInfo {
	out := slices.Clone(base)
	for _, m := range messages {
		if m.Role != ai.RoleSystem {
			continue
		}
		for _, c := range m.Content {
			switch v := c.(type) {
			case ai.ToolAdded:
				i := slices.IndexFunc(out, func(t ai.ToolInfo) bool { return t.Name == v.Tool.Name })
				if i >= 0 {
					out[i] = v.Tool
				} else {
					out = append(out, v.Tool)
				}
			case ai.ToolRemoved:
				out = slices.DeleteFunc(out, func(t ai.ToolInfo) bool { return t.Name == v.Name })
			}
		}
	}
	return out
}

// Reminder wraps the text of a system message for a model that takes no
// system messages. The provider sends it in a user message, so the
// system prompt does not change, and its cache stays.
func Reminder(text string) string {
	return "<system-reminder>\n" + text + "\n</system-reminder>"
}

// NoResultText is the text of a tool result that [CloseToolCalls] adds.
const NoResultText = "No result provided"

// CloseToolCalls adds an error result for each tool call with no
// result. An API rejects a tool call without its result, and a call
// loses its result when a user message interrupts the tools or the
// conversation stops. The added results go after the last result of
// the calls, so a provider groups them with the others.
func CloseToolCalls(messages []ai.Message) []ai.Message {
	var out []ai.Message
	var open []ai.ToolCall
	answered := map[string]bool{}
	insertAt := 0

	closeCalls := func() {
		var added []ai.Message
		for _, call := range open {
			if answered[call.ID] {
				continue
			}
			result := ai.ToolResultMessage(call.ID, ai.Text{Text: NoResultText})
			result.IsError = true
			added = append(added, result)
		}
		out = slices.Insert(out, insertAt, added...)
		open = nil
		answered = map[string]bool{}
	}

	for _, msg := range messages {
		switch msg.Role {
		case ai.RoleAssistant:
			closeCalls()
			out = append(out, msg)
			open = msg.ToolCalls()
			insertAt = len(out)
		case ai.RoleUser:
			closeCalls()
			out = append(out, msg)
		case ai.RoleToolResult:
			answered[msg.ToolCallID] = true
			out = append(out, msg)
			insertAt = len(out)
		default:
			out = append(out, msg)
		}
	}
	closeCalls()

	return out
}
