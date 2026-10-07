package anthropic

import (
	"encoding/base64"
	"slices"
	"strings"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"

	"github.com/sonnes/gopi/pkg/ai"
)

// convertConfig says how [convertMessages] maps the messages for one
// model.
type convertConfig struct {
	// systemMessages sends each system message as a system message. If
	// it is false, each one goes as a system-reminder in a user
	// message.
	systemMessages bool
	// toolChanges sends the tool changes of a system message as
	// tool_addition and tool_removal blocks. If it is false, the tool
	// list of the request holds the current tools, and the blocks go.
	toolChanges bool
	// effort sends the effort of each assistant message before it.
	effort bool
	// cache is the marker of the last block. A nil cache sends none.
	cache *sdk.CacheControlEphemeralParam
}

// convertMessages maps the messages to the API.
//
// It groups consecutive tool results into one user message, because the
// API wants every result of a turn in one message. A system message
// waits until the next assistant message, or the end. A tool_result
// must come directly after its tool_use, and a system message must come
// last or before an assistant message. The last block of the last user
// or system message carries the cache marker.
func convertMessages(messages []ai.Message, cfg convertConfig) []sdk.MessageParam {
	messages = closeToolCalls(messages)

	var out []sdk.MessageParam
	var pending []sdk.MessageParam
	var reminders []sdk.ContentBlockParamUnion

	flushPending := func() {
		out = append(out, pending...)
		pending = nil
	}
	flushReminders := func() {
		if len(reminders) == 0 {
			return
		}
		if n := len(out); n > 0 && isUserMessage(out[n-1]) {
			out[n-1].Content = append(out[n-1].Content, reminders...)
		} else {
			out = append(out, sdk.NewUserMessage(reminders...))
		}
		reminders = nil
	}

	i := 0
	for i < len(messages) {
		msg := messages[i]

		switch msg.Role {
		case ai.RoleSystem:
			text := systemText(msg.Content)
			if !cfg.systemMessages {
				if text != "" {
					reminders = append(reminders, sdk.NewTextBlock(systemReminder(text)))
				}
			} else if blocks := systemBlocks(msg, text, cfg.toolChanges); len(blocks) > 0 {
				pending = append(pending, rawMessage("system", blocks))
			}
			i++

		case ai.RoleUser:
			blocks := append(reminders, convertUserContent(msg.Content)...)
			reminders = nil
			if len(blocks) > 0 {
				out = append(out, sdk.NewUserMessage(blocks...))
			}
			i++

		case ai.RoleAssistant:
			flushPending()
			flushReminders()
			blocks := convertAssistantContent(msg.Content)
			if len(blocks) > 0 {
				if cfg.effort && isEffort(msg.Effort) {
					out = append(out, effortMessage(msg.Effort))
				}
				out = append(out, sdk.NewAssistantMessage(blocks...))
			}
			i++

		case ai.RoleToolResult:
			var blocks []sdk.ContentBlockParamUnion
			for i < len(messages) && messages[i].Role == ai.RoleToolResult {
				blocks = append(blocks, convertToolResult(messages[i]))
				i++
			}
			blocks = append(blocks, reminders...)
			reminders = nil
			out = append(out, sdk.NewUserMessage(blocks...))

		default:
			i++
		}
	}

	flushPending()
	flushReminders()

	if cfg.cache != nil {
		markLast(out, *cfg.cache)
	}
	return out
}

// noResultText is the text of a tool result that the provider adds for
// a call with no result. The text is the text of pi-mono.
const noResultText = "No result provided"

// closeToolCalls adds an error result for each tool call with no result.
// The API rejects a tool_use without its tool_result, and a call loses
// its result when a user message interrupts the tools or the
// conversation stops. The added results go after the last result of
// the calls, so they join the same user message.
func closeToolCalls(messages []ai.Message) []ai.Message {
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
			result := ai.ToolResultMessage(call.ID, ai.Text{Text: noResultText})
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

// isUserMessage reports if m is a user message that the SDK built. A
// raw message is a system message.
func isUserMessage(m sdk.MessageParam) bool {
	_, raw := m.Overrides()
	return !raw && m.Role == sdk.MessageParamRoleUser
}

// markLast puts the cache marker on the last block of the last message,
// if it is a user or a system message. The API then reads the whole
// history from the cache on the next call.
func markLast(out []sdk.MessageParam, cache sdk.CacheControlEphemeralParam) {
	if len(out) == 0 {
		return
	}
	last := out[len(out)-1]

	if raw, ok := last.Overrides(); ok {
		blocks, _ := raw.(map[string]any)["content"].([]any)
		if len(blocks) == 0 {
			return
		}
		if block, ok := blocks[len(blocks)-1].(map[string]any); ok {
			block["cache_control"] = cache
		}
		return
	}

	if last.Role != sdk.MessageParamRoleUser || len(last.Content) == 0 {
		return
	}
	if marker := last.Content[len(last.Content)-1].GetCacheControl(); marker != nil {
		*marker = cache
	}
}

// rawMessage builds a message that the params of the SDK cannot hold,
// such as a system message or a tool change, as raw JSON.
func rawMessage(role string, blocks []any) sdk.MessageParam {
	return param.Override[sdk.MessageParam](map[string]any{
		"role":    role,
		"content": blocks,
	})
}

// inlineToolsBeta is the beta of the API that reads tool_addition and
// tool_removal blocks in a system message.
const inlineToolsBeta = "inline-tools-2026-09-15"

// systemSep joins the parts of the text of a system message.
const systemSep = "\n\n"

// systemText returns the text of a system message, with the parts
// joined by a blank line. A [ai.Text] block goes as it is. A
// [ai.Section] or [ai.SectionRemoved] block goes with a frame that names
// the section, so the model can relate it to the system prompt. The
// frame needs no state of earlier sections. The text of the frame is
// the text of pi-mono.
func systemText(content []ai.Content) string {
	var parts []string
	for _, c := range content {
		switch v := c.(type) {
		case ai.Text:
			parts = append(parts, v.Text)
		case ai.Section:
			parts = append(parts, `Updated system prompt section "`+v.Name+`":`+systemSep+v.Text)
		case ai.SectionRemoved:
			parts = append(parts, `Removed system prompt section "`+v.Name+`".`)
		}
	}
	parts = slices.DeleteFunc(parts, func(p string) bool { return p == "" })
	return strings.Join(parts, systemSep)
}

// systemReminder wraps the text of a system message for a model that
// takes no system messages. The system prompt does not change, so its
// cache stays.
func systemReminder(text string) string {
	return "<system-reminder>\n" + text + "\n</system-reminder>"
}

// systemBlocks returns the blocks of a system message: text first, then
// the tool changes if toolChanges is true. A tool_addition replaces a
// tool with the same name, so a removal of that name is left out.
func systemBlocks(msg ai.Message, text string, toolChanges bool) []any {
	var blocks []any
	if text != "" {
		blocks = append(blocks, map[string]any{"type": "text", "text": text})
	}
	if !toolChanges {
		return blocks
	}

	added := make(map[string]bool)
	for _, c := range msg.Content {
		if v, ok := c.(ai.ToolAdded); ok {
			added[v.Tool.Name] = true
		}
	}

	for _, c := range msg.Content {
		switch v := c.(type) {
		case ai.ToolRemoved:
			if added[v.Name] {
				continue
			}
			blocks = append(blocks, map[string]any{
				"type": "tool_removal",
				"tool": map[string]any{"type": "tool_reference", "name": v.Name},
			})
		case ai.ToolAdded:
			definition := convertTools([]ai.ToolInfo{v.Tool})[0]
			blocks = append(blocks, map[string]any{
				"type": "tool_addition",
				"tool": map[string]any{"type": "tool_definition", "definition": definition},
			})
		}
	}
	return blocks
}

// efforts is the set of efforts that the API takes.
var validEfforts = []string{"low", "medium", "high", "xhigh", "max"}

// isEffort reports if effort is an effort of this API. A message from
// another provider can hold an effort that this API does not take.
func isEffort(effort string) bool {
	return slices.Contains(validEfforts, effort)
}

// effortMessage is a system message with no content that sets the
// effort of the next assistant turn.
func effortMessage(effort string) sdk.MessageParam {
	return param.Override[sdk.MessageParam](map[string]any{
		"role":          "system",
		"content":       []any{},
		"output_config": map[string]any{"effort": effort},
	})
}

// convertUserContent maps user blocks. A file goes as an image block,
// or as a document block for a PDF. It drops an empty text block,
// because the API rejects one.
func convertUserContent(content []ai.Content) []sdk.ContentBlockParamUnion {
	var blocks []sdk.ContentBlockParamUnion
	for _, c := range content {
		switch v := c.(type) {
		case ai.Text:
			if v.Text != "" {
				blocks = append(blocks, sdk.NewTextBlock(v.Text))
			}
		case ai.File:
			blocks = append(blocks, fileBlock(v))
		}
	}
	return blocks
}

// convertAssistantContent maps assistant blocks. It sends a thinking
// block only with its signature, because the API verifies the
// signature. It drops an empty text block.
func convertAssistantContent(content []ai.Content) []sdk.ContentBlockParamUnion {
	var blocks []sdk.ContentBlockParamUnion
	for _, c := range content {
		switch v := c.(type) {
		case ai.Text:
			if v.Text != "" {
				blocks = append(blocks, sdk.NewTextBlock(v.Text))
			}
		case ai.ThinkingBlock:
			if v.Signature != "" {
				blocks = append(blocks, sdk.NewThinkingBlock(v.Signature, v.Thinking))
			}
		case ai.ToolCall:
			blocks = append(blocks, sdk.NewToolUseBlock(v.ID, v.Input, v.Name))
		}
	}
	return blocks
}

// convertToolResult maps a tool result message to a tool_result block.
func convertToolResult(msg ai.Message) sdk.ContentBlockParamUnion {
	block := sdk.ToolResultBlockParam{
		ToolUseID: msg.ToolCallID,
	}
	if msg.IsError {
		block.IsError = param.NewOpt(true)
	}

	for _, c := range msg.Content {
		switch v := c.(type) {
		case ai.Text:
			block.Content = append(block.Content, sdk.ToolResultBlockParamContentUnion{
				OfText: &sdk.TextBlockParam{Text: v.Text},
			})
		case ai.File:
			file := fileBlock(v)
			block.Content = append(block.Content, sdk.ToolResultBlockParamContentUnion{
				OfImage:    file.OfImage,
				OfDocument: file.OfDocument,
			})
		}
	}

	return sdk.ContentBlockParamUnion{OfToolResult: &block}
}

// pdfMediaType is the media type of a PDF. The API reads a PDF as a
// document block, and every other file as an image block.
const pdfMediaType = "application/pdf"

// fileBlock maps a file to a document block for a PDF, or to an image
// block.
func fileBlock(f ai.File) sdk.ContentBlockParamUnion {
	data := base64.StdEncoding.EncodeToString(f.Data)
	if f.MediaType == pdfMediaType {
		return sdk.ContentBlockParamUnion{
			OfDocument: &sdk.DocumentBlockParam{
				Source: sdk.DocumentBlockParamSourceUnion{
					OfBase64: &sdk.Base64PDFSourceParam{Data: data},
				},
			},
		}
	}
	return sdk.ContentBlockParamUnion{
		OfImage: &sdk.ImageBlockParam{
			Source: sdk.ImageBlockParamSourceUnion{
				OfBase64: &sdk.Base64ImageSourceParam{
					Data:      data,
					MediaType: sdk.Base64ImageSourceMediaType(f.MediaType),
				},
			},
		},
	}
}
