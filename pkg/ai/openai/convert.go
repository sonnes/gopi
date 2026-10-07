package openai

import (
	"encoding/base64"
	"encoding/json"
	"strings"

	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"

	"github.com/sonnes/gopi/internal/transcript"
	"github.com/sonnes/gopi/pkg/ai"
)

// signature is the content of [ai.ThinkingBlock.Signature] for a block
// from this provider. The API needs the ID and the encrypted content of
// a reasoning item to accept it again, and the block has one field for
// both. A caller treats the value as opaque.
type signature struct {
	ID               string `json:"id"`
	EncryptedContent string `json:"encrypted_content,omitempty"`
}

// String encodes the signature as JSON.
func (s signature) String() string {
	data, _ := json.Marshal(s)
	return string(data)
}

// parseSignature decodes a signature. A signature from another provider
// does not decode, and the bool is false.
func parseSignature(s string) (signature, bool) {
	var sig signature
	if err := json.Unmarshal([]byte(s), &sig); err != nil || sig.ID == "" {
		return signature{}, false
	}
	return sig, true
}

// convertConfig says how [convertMessages] maps the messages for one
// model.
type convertConfig struct {
	// systemMessages sends each system message as a developer message.
	// If it is false, each one goes as a system-reminder in a user
	// message.
	systemMessages bool
	// addTools sends the [ai.ToolAdded] blocks of a system message as
	// one additional_tools item after its text.
	addTools bool
}

// convertMessages maps the messages to input items. Each block of an
// assistant message becomes one item, in order, because a reasoning
// item must come before the output that it produced.
//
// A system message waits until the next assistant message, or the end,
// so it never comes between a function call and its output. A
// system-reminder goes at the start of the next user message, or at the
// end of the last one.
func convertMessages(messages []ai.Message, cfg convertConfig) responses.ResponseInputParam {
	messages = transcript.CloseToolCalls(messages)

	var out responses.ResponseInputParam
	var pending responses.ResponseInputParam
	var reminders responses.ResponseInputMessageContentListParam

	// lastUser holds the parts of the last item if it is a user
	// message, so a reminder can join it.
	var lastUser responses.ResponseInputMessageContentListParam
	pushUser := func(parts responses.ResponseInputMessageContentListParam) {
		out = append(out, responses.ResponseInputItemParamOfMessage(parts, responses.EasyInputMessageRoleUser))
		lastUser = parts
	}
	push := func(items ...responses.ResponseInputItemUnionParam) {
		out = append(out, items...)
		lastUser = nil
	}
	flushPending := func() {
		if len(pending) > 0 {
			push(pending...)
		}
		pending = nil
	}
	flushReminders := func() {
		if len(reminders) == 0 {
			return
		}
		if lastUser != nil {
			out = out[:len(out)-1]
			pushUser(append(lastUser, reminders...))
		} else {
			pushUser(reminders)
		}
		reminders = nil
	}

	for _, msg := range messages {
		switch msg.Role {
		case ai.RoleSystem:
			text := transcript.SystemText(msg.Content)
			if !cfg.systemMessages {
				if text != "" {
					reminders = append(reminders, inputText(transcript.Reminder(text)))
				}
				continue
			}
			if text != "" {
				parts := responses.ResponseInputMessageContentListParam{inputText(text)}
				pending = append(pending, responses.ResponseInputItemParamOfMessage(
					parts,
					responses.EasyInputMessageRoleDeveloper,
				))
			}
			if item, ok := additionalTools(msg.Content); ok && cfg.addTools {
				pending = append(pending, item)
			}
		case ai.RoleUser:
			parts := append(reminders, userParts(msg.Content)...)
			reminders = nil
			if len(parts) > 0 {
				pushUser(parts)
			}
		case ai.RoleAssistant:
			flushPending()
			flushReminders()
			push(assistantItems(msg.Content)...)
		case ai.RoleToolResult:
			push(toolResultItem(msg))
		}
	}

	flushPending()
	flushReminders()
	return out
}

// inputText is one text part of an input message.
func inputText(text string) responses.ResponseInputContentUnionParam {
	return responses.ResponseInputContentUnionParam{
		OfInputText: &responses.ResponseInputTextParam{Text: text},
	}
}

// additionalTools maps the [ai.ToolAdded] blocks to one
// additional_tools item. The bool is false if there are none.
func additionalTools(content []ai.Content) (responses.ResponseInputItemUnionParam, bool) {
	var tools []ai.ToolInfo
	for _, c := range content {
		if v, ok := c.(ai.ToolAdded); ok {
			tools = append(tools, v.Tool)
		}
	}
	if len(tools) == 0 {
		return responses.ResponseInputItemUnionParam{}, false
	}
	return responses.ResponseInputItemParamOfAdditionalTools(convertTools(tools)), true
}

// userParts maps the blocks of a user message to the parts of an input
// message. A file goes as a data URL: a PDF as a file part, and every
// other file as an image part. It drops an empty text block, because
// the API rejects one.
func userParts(content []ai.Content) responses.ResponseInputMessageContentListParam {
	var parts responses.ResponseInputMessageContentListParam
	for _, c := range content {
		switch v := c.(type) {
		case ai.Text:
			if v.Text != "" {
				parts = append(parts, inputText(v.Text))
			}
		case ai.File:
			parts = append(parts, inputFile(v))
		}
	}
	return parts
}

// imageDetail is the detail of an image part. Auto lets the model pick.
const imageDetail = responses.ResponseInputImageDetailAuto

// inputFile maps a file of a user message to a file part for a PDF, or
// to an image part.
func inputFile(f ai.File) responses.ResponseInputContentUnionParam {
	url := dataURL(f)
	if f.MediaType == pdfMediaType {
		return responses.ResponseInputContentUnionParam{
			OfInputFile: &responses.ResponseInputFileParam{
				FileData: param.NewOpt(url),
				Filename: param.NewOpt(pdfFilename),
			},
		}
	}
	return responses.ResponseInputContentUnionParam{
		OfInputImage: &responses.ResponseInputImageParam{
			Detail:   imageDetail,
			ImageURL: param.NewOpt(url),
		},
	}
}

// dataURL encodes a file as a data URL.
func dataURL(f ai.File) string {
	return "data:" + f.MediaType + ";base64," + base64.StdEncoding.EncodeToString(f.Data)
}

// assistantItems maps assistant blocks to items. It sends a thinking
// block only with a signature from this provider, because the API
// verifies the encrypted content. It drops an empty text block.
func assistantItems(content []ai.Content) []responses.ResponseInputItemUnionParam {
	var items []responses.ResponseInputItemUnionParam
	for _, c := range content {
		switch v := c.(type) {
		case ai.Text:
			if v.Text != "" {
				items = append(items, responses.ResponseInputItemParamOfMessage(
					v.Text,
					responses.EasyInputMessageRoleAssistant,
				))
			}
		case ai.ThinkingBlock:
			if item, ok := reasoningItem(v); ok {
				items = append(items, item)
			}
		case ai.ToolCall:
			items = append(items, responses.ResponseInputItemParamOfFunctionCall(
				string(v.Input),
				v.ID,
				v.Name,
			))
		}
	}
	return items
}

// reasoningItem maps a thinking block back to its reasoning item. The
// summary list is never nil, because the API requires the field.
func reasoningItem(block ai.ThinkingBlock) (responses.ResponseInputItemUnionParam, bool) {
	sig, ok := parseSignature(block.Signature)
	if !ok {
		return responses.ResponseInputItemUnionParam{}, false
	}

	summary := []responses.ResponseReasoningItemSummaryParam{}
	if block.Thinking != "" {
		summary = append(summary, responses.ResponseReasoningItemSummaryParam{Text: block.Thinking})
	}

	item := responses.ResponseInputItemParamOfReasoning(sig.ID, summary)
	if sig.EncryptedContent != "" {
		item.OfReasoning.EncryptedContent = param.NewOpt(sig.EncryptedContent)
	}
	return item, true
}

// toolResultItem maps a tool result message to a function call output.
// The API has no error flag, so the text of the result carries the
// error. A result with a file is a list of parts. A result with only
// text is one string.
func toolResultItem(msg ai.Message) responses.ResponseInputItemUnionParam {
	var item responses.ResponseInputItemUnionParam
	if hasFile(msg.Content) {
		item = responses.ResponseInputItemParamOfFunctionCallOutput(outputParts(msg.Content))
	} else {
		item = responses.ResponseInputItemParamOfFunctionCallOutput(joinText(msg.Content))
	}
	item.OfFunctionCallOutput.CallID = param.NewOpt(msg.ToolCallID)
	return item
}

// pdfMediaType is the media type of a PDF. The API reads a PDF as a file
// part, and every other file as an image part.
const pdfMediaType = "application/pdf"

// pdfFilename is the name of a PDF part. The API needs a name for a file
// part, and a file block has none.
const pdfFilename = "file.pdf"

// outputParts maps the blocks of a tool result to the parts of a
// function call output. A file goes as a data URL.
func outputParts(content []ai.Content) responses.ResponseFunctionCallOutputItemListParam {
	var parts responses.ResponseFunctionCallOutputItemListParam
	for _, c := range content {
		switch v := c.(type) {
		case ai.Text:
			parts = append(parts, responses.ResponseFunctionCallOutputItemUnionParam{
				OfInputText: &responses.ResponseInputTextContentParam{Text: v.Text},
			})
		case ai.File:
			parts = append(parts, filePart(v))
		}
	}
	return parts
}

// filePart maps a file to a file part for a PDF, or to an image part.
func filePart(f ai.File) responses.ResponseFunctionCallOutputItemUnionParam {
	url := dataURL(f)
	if f.MediaType == pdfMediaType {
		return responses.ResponseFunctionCallOutputItemUnionParam{
			OfInputFile: &responses.ResponseInputFileContentParam{
				FileData: param.NewOpt(url),
				Filename: param.NewOpt(pdfFilename),
			},
		}
	}
	return responses.ResponseFunctionCallOutputItemUnionParam{
		OfInputImage: &responses.ResponseInputImageContentParam{
			ImageURL: param.NewOpt(url),
		},
	}
}

// hasFile reports whether content holds a file.
func hasFile(content []ai.Content) bool {
	for _, c := range content {
		if _, ok := c.(ai.File); ok {
			return true
		}
	}
	return false
}

// joinText returns the text of the text blocks, joined in order.
func joinText(content []ai.Content) string {
	var b strings.Builder
	for _, c := range content {
		if t, ok := c.(ai.Text); ok {
			b.WriteString(t.Text)
		}
	}
	return b.String()
}
