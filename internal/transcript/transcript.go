// Package transcript holds the rules that every provider applies to the
// messages before it maps them to its API: the place of a system
// message, the text of a system message, the reminder for a model that
// takes no system messages, the tools after the tool changes, and the
// results of tool calls that have none. It also turns a tool schema
// into the JSON object that an API takes.
//
// The reasons are in docs/concepts/ai/message.md.
package transcript

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"

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

// Order returns the messages in the order that a provider sends them.
// It first adds the missing tool results with [CloseToolCalls]. A system
// message then waits until the next assistant message, or the end,
// because a tool result must come directly after its tool call.
//
// If systemMessages is false, the model takes no system message, so the
// text of each one goes as a [Reminder] in a user message. The reminder
// goes at the start of the next user message. If an assistant message
// or the end comes first, it goes at the end of the last message if
// that is a user message, or else in a new user message. A system
// message with no text sends no reminder.
func Order(messages []ai.Message, systemMessages bool) []ai.Message {
	messages = CloseToolCalls(messages)

	var out []ai.Message
	var waiting []ai.Message
	var reminders []ai.Content

	flush := func() {
		out = append(out, waiting...)
		waiting = nil
		if len(reminders) == 0 {
			return
		}
		if n := len(out); n > 0 && out[n-1].Role == ai.RoleUser {
			out[n-1].Content = append(slices.Clip(out[n-1].Content), reminders...)
		} else {
			out = append(out, ai.Message{Role: ai.RoleUser, Content: reminders})
		}
		reminders = nil
	}

	for _, m := range messages {
		switch m.Role {
		case ai.RoleSystem:
			if systemMessages {
				waiting = append(waiting, m)
			} else if text := SystemText(m.Content); text != "" {
				reminders = append(reminders, ai.Text{Text: Reminder(text)})
			}
		case ai.RoleUser:
			if len(reminders) > 0 {
				m.Content = append(reminders, m.Content...)
				reminders = nil
			}
			out = append(out, m)
		case ai.RoleAssistant:
			flush()
			out = append(out, m)
		default:
			out = append(out, m)
		}
	}
	flush()

	return out
}

// ToolChanges is what the tool changes of the messages do to the first
// tools of a request. An API that cannot change its tool list in place
// reads it to keep the list, and the cache, the same.
type ToolChanges struct {
	// Current is the tools that the model can call after the messages,
	// as [CurrentTools] returns them.
	Current []ai.ToolInfo
	// Declared is the first tools, then each tool that an [ai.ToolAdded]
	// block adds with a new name, in order.
	Declared []ai.ToolInfo
	// Removed is true if a system message removes a tool.
	Removed bool
	// Replaced is true if an [ai.ToolAdded] block names a declared tool.
	// The definition of a declared tool then changes.
	Replaced bool
}

// ChangesOf returns what the tool changes of the system messages do to
// base.
func ChangesOf(base []ai.ToolInfo, messages []ai.Message) ToolChanges {
	changes := ToolChanges{
		Current:  CurrentTools(base, messages),
		Declared: slices.Clone(base),
	}
	for _, m := range messages {
		if m.Role != ai.RoleSystem {
			continue
		}
		for _, c := range m.Content {
			switch v := c.(type) {
			case ai.ToolRemoved:
				changes.Removed = true
			case ai.ToolAdded:
				declared := slices.ContainsFunc(changes.Declared, func(t ai.ToolInfo) bool {
					return t.Name == v.Tool.Name
				})
				if declared {
					changes.Replaced = true
				} else {
					changes.Declared = append(changes.Declared, v.Tool)
				}
			}
		}
	}
	return changes
}

// Allowed returns the names of the current tools if a system message
// removed a tool, and nil if none did. An API with an allowed_tools
// choice keeps its tool list after a removal and limits the model to
// these names.
func (c ToolChanges) Allowed() []string {
	if !c.Removed {
		return nil
	}
	names := make([]string, 0, len(c.Current))
	for _, t := range c.Current {
		names = append(names, t.Name)
	}
	return names
}

// LimitChoice returns the tool choice of a request that limits the
// model to the allowed tools. choice and set come from
// [ai.ToolChoiceOf], and send is false if the request sends no choice.
//
// If allowed is nil, the choice goes as it is. A choice of none, or of
// one tool, is narrower than the limit, so it goes as it is too. With
// no allowed tool, the choice is none. Else limit is true, and the
// choice is the mode of the limit: any if the call set any, or else
// auto.
func LimitChoice(
	choice ai.ToolChoiceMode,
	set bool,
	allowed []string,
) (out ai.ToolChoiceMode, send, limit bool) {
	narrower := set && (choice.Type == ai.ToolChoiceNone.Type || choice.Named())
	switch {
	case allowed == nil:
		return choice, set, false
	case narrower:
		return choice, true, false
	case len(allowed) == 0:
		return ai.ToolChoiceNone, true, false
	case set && choice.Type == ai.ToolChoiceAny.Type:
		return ai.ToolChoiceAny, true, true
	default:
		return ai.ToolChoiceAuto, true, true
	}
}

// Schema returns schema as the JSON object that an API takes. A nil
// schema gives nil. A schema that does not encode is an error, so the
// call fails and the model never gets a tool with no parameters.
func Schema(schema *jsonschema.Schema) (map[string]any, error) {
	if schema == nil {
		return nil, nil
	}

	data, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("encode schema: %w", err)
	}

	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("decode schema: %w", err)
	}
	return m, nil
}

// Parameters returns the parameters of a function tool. An API needs an
// object schema for a function tool, so a tool with no schema gets an
// empty object. Each call returns a new map, so a caller can change it.
func Parameters(schema *jsonschema.Schema) (map[string]any, error) {
	if schema == nil {
		empty := map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}
		return empty, nil
	}
	return Schema(schema)
}

// imageMediaPrefix starts the media type of an image.
const imageMediaPrefix = "image/"

// NotImage returns the media type of the first file of messages that is
// not an image. The decisions APIs of OpenAI and OpenRouter read no
// other file.
func NotImage(messages []ai.Message) (string, bool) {
	for _, msg := range messages {
		for _, c := range msg.Content {
			f, ok := c.(ai.File)
			if ok && !strings.HasPrefix(f.MediaType, imageMediaPrefix) {
				return f.MediaType, true
			}
		}
	}
	return "", false
}
