package anthropic

import (
	"encoding/base64"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"

	"github.com/sonnes/gopi/internal/transcript"
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

// convertMessages maps the messages to the API, in the order of
// [transcript.Order].
//
// It groups consecutive tool results into one user message, because the
// API wants every result of a turn in one message. The last block of
// the last user or system message carries the cache marker.
func convertMessages(messages []ai.Message, cfg convertConfig) ([]sdk.MessageParam, error) {
	messages = transcript.Order(messages, cfg.systemMessages)

	var out []sdk.MessageParam
	for i := 0; i < len(messages); i++ {
		msg := messages[i]

		switch msg.Role {
		case ai.RoleSystem:
			blocks, err := systemBlocks(msg, cfg.toolChanges)
			if err != nil {
				return nil, err
			}
			if len(blocks) > 0 {
				out = append(out, rawMessage("system", blocks))
			}

		case ai.RoleUser:
			if blocks := convertUserContent(msg.Content); len(blocks) > 0 {
				out = append(out, sdk.NewUserMessage(blocks...))
			}

		case ai.RoleAssistant:
			blocks := convertAssistantContent(msg.Content)
			if len(blocks) == 0 {
				continue
			}
			if cfg.effort && isEffort(msg.Effort) {
				out = append(out, effortMessage(msg.Effort))
			}
			out = append(out, sdk.NewAssistantMessage(blocks...))

		case ai.RoleToolResult:
			var blocks []sdk.ContentBlockParamUnion
			for ; i < len(messages) && messages[i].Role == ai.RoleToolResult; i++ {
				blocks = append(blocks, convertToolResult(messages[i]))
			}
			i--
			out = append(out, sdk.NewUserMessage(blocks...))
		}
	}

	if cfg.cache != nil {
		markLast(out, *cfg.cache)
	}
	return out, nil
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

// systemBlocks returns the blocks of a system message: text first, then
// the tool changes if toolChanges is true. A tool_addition replaces a
// tool with the same name, so a removal of that name is left out.
func systemBlocks(msg ai.Message, toolChanges bool) ([]any, error) {
	var blocks []any
	if text := transcript.SystemText(msg.Content); text != "" {
		blocks = append(blocks, map[string]any{"type": "text", "text": text})
	}
	if !toolChanges {
		return blocks, nil
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
			definition, err := convertTools([]ai.ToolInfo{v.Tool})
			if err != nil {
				return nil, err
			}
			blocks = append(blocks, map[string]any{
				"type": "tool_addition",
				"tool": map[string]any{"type": "tool_definition", "definition": definition[0]},
			})
		}
	}
	return blocks, nil
}

// isEffort reports if effort is an effort of this API: a thinking level
// other than off. A message from another provider can hold an effort
// that this API does not take.
func isEffort(effort string) bool {
	level, err := ai.ParseThinking(effort)
	return err == nil && level != ai.ThinkingOff
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
