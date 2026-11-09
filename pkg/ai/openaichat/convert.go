package openaichat

import (
	"encoding/base64"
	"strings"

	sdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/param"

	"github.com/sonnes/gopi/internal/transcript"
	"github.com/sonnes/gopi/pkg/ai"
)

// reasoningField is the field of an assistant message that carries the
// reasoning of the model. Fireworks needs it back on the next call for
// a model with interleaved thinking. OpenRouter reads it as its
// reasoning field.
const reasoningField = "reasoning_content"

// convertConfig says how [convertMessages] maps the messages for one
// model.
type convertConfig struct {
	// systemMessages sends each system message as a system message. If
	// it is false, each one goes as a system-reminder in a user
	// message.
	systemMessages bool
}

// attachedText starts the user message that carries the files of the
// tool results.
const attachedText = "Attached image(s) from tool result:"

// convertMessages maps the messages to the API, in the order of
// [transcript.Order]. Each tool result is its own message. The tool
// message takes only text, so the files of the results of one turn
// follow in one user message, before the next message of another role.
func convertMessages(messages []ai.Message, cfg convertConfig) []sdk.ChatCompletionMessageParamUnion {
	var out []sdk.ChatCompletionMessageParamUnion
	var files []sdk.ChatCompletionContentPartUnionParam

	flushFiles := func() {
		if len(files) > 0 {
			parts := append([]sdk.ChatCompletionContentPartUnionParam{sdk.TextContentPart(attachedText)}, files...)
			out = append(out, userMessage(parts))
		}
		files = nil
	}

	for _, msg := range transcript.Order(messages, cfg.systemMessages) {
		switch msg.Role {
		case ai.RoleSystem:
			flushFiles()
			if text := transcript.SystemText(msg.Content); text != "" {
				out = append(out, sdk.SystemMessage(text))
			}
		case ai.RoleUser:
			flushFiles()
			if parts := userParts(msg.Content); len(parts) > 0 {
				out = append(out, userMessage(parts))
			}
		case ai.RoleAssistant:
			flushFiles()
			if m, ok := assistantMessage(msg.Content); ok {
				out = append(out, m)
			}
		case ai.RoleToolResult:
			out = append(out, sdk.ToolMessage(toolText(msg.Content), msg.ToolCallID))
			files = append(files, fileParts(msg.Content)...)
		}
	}

	flushFiles()
	return out
}

// userMessage builds a user message from parts. One text part is a
// string.
func userMessage(parts []sdk.ChatCompletionContentPartUnionParam) sdk.ChatCompletionMessageParamUnion {
	if len(parts) == 1 && parts[0].OfText != nil {
		return sdk.UserMessage(parts[0].OfText.Text)
	}
	return sdk.UserMessage(parts)
}

// userParts maps user blocks to parts. It drops an empty text block,
// because the API rejects one.
func userParts(content []ai.Content) []sdk.ChatCompletionContentPartUnionParam {
	var parts []sdk.ChatCompletionContentPartUnionParam
	for _, c := range content {
		switch v := c.(type) {
		case ai.Text:
			if v.Text != "" {
				parts = append(parts, sdk.TextContentPart(v.Text))
			}
		case ai.File:
			parts = append(parts, filePart(v))
		}
	}
	return parts
}

// pdfMediaType is the media type of a PDF. The API reads a PDF as a file
// part, and every other file as an image part.
const pdfMediaType = "application/pdf"

// pdfFilename is the name of a PDF part. The API needs a name for a file
// part, and a file block has none.
const pdfFilename = "file.pdf"

// filePart maps a file to a file part for a PDF, or to an image part.
// The data goes as a data URL.
func filePart(f ai.File) sdk.ChatCompletionContentPartUnionParam {
	url := "data:" + f.MediaType + ";base64," + base64.StdEncoding.EncodeToString(f.Data)
	if f.MediaType == pdfMediaType {
		return sdk.FileContentPart(sdk.ChatCompletionContentPartFileFileParam{
			FileData: param.NewOpt(url),
			Filename: param.NewOpt(pdfFilename),
		})
	}
	return sdk.ImageContentPart(sdk.ChatCompletionContentPartImageImageURLParam{URL: url})
}

// fileParts maps the files of a tool result to parts.
func fileParts(content []ai.Content) []sdk.ChatCompletionContentPartUnionParam {
	var parts []sdk.ChatCompletionContentPartUnionParam
	for _, c := range content {
		if f, ok := c.(ai.File); ok {
			parts = append(parts, filePart(f))
		}
	}
	return parts
}

// assistantMessage maps assistant blocks to one message. The text is the
// content, the thinking is the reasoning field, and each call is a tool
// call. A message with none of them is dropped.
func assistantMessage(content []ai.Content) (sdk.ChatCompletionMessageParamUnion, bool) {
	var text, thinking strings.Builder
	var calls []sdk.ChatCompletionMessageToolCallUnionParam

	for _, c := range content {
		switch v := c.(type) {
		case ai.Text:
			text.WriteString(v.Text)
		case ai.ThinkingBlock:
			thinking.WriteString(v.Thinking)
		case ai.ToolCall:
			calls = append(calls, sdk.ChatCompletionMessageToolCallUnionParam{
				OfFunction: &sdk.ChatCompletionMessageFunctionToolCallParam{
					ID: v.ID,
					Function: sdk.ChatCompletionMessageFunctionToolCallFunctionParam{
						Name:      v.Name,
						Arguments: string(v.Input),
					},
				},
			})
		}
	}

	if text.Len() == 0 && len(calls) == 0 {
		return sdk.ChatCompletionMessageParamUnion{}, false
	}

	msg := sdk.ChatCompletionAssistantMessageParam{ToolCalls: calls}
	if text.Len() > 0 {
		msg.Content.OfString = param.NewOpt(text.String())
	}
	if thinking.Len() > 0 {
		msg.SetExtraFields(map[string]any{reasoningField: thinking.String()})
	}

	return sdk.ChatCompletionMessageParamUnion{OfAssistant: &msg}, true
}

// noTextResult is the text of a tool message for a result with only
// files. The files follow in a user message.
const noTextResult = "(see attached image)"

// toolText returns the text of the text blocks of a tool result, joined
// in order.
func toolText(content []ai.Content) string {
	var b strings.Builder
	hasFile := false
	for _, c := range content {
		switch v := c.(type) {
		case ai.Text:
			b.WriteString(v.Text)
		case ai.File:
			hasFile = true
		}
	}
	if b.Len() == 0 && hasFile {
		return noTextResult
	}
	return b.String()
}
