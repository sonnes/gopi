package gemini

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"google.golang.org/genai/interactions/models/interactions"

	"github.com/sonnes/gopi/internal/transcript"
	"github.com/sonnes/gopi/pkg/ai"
)

// signature is the content of [ai.ThinkingBlock.Signature] for a block
// from this provider. The JSON key keeps it apart from the signatures
// of the other providers. A caller treats the value as opaque.
type signature struct {
	Gemini string `json:"gemini"`
}

func (s signature) String() string {
	data, _ := json.Marshal(s)
	return string(data)
}

// A signature from another provider does not decode, and the bool is
// false.
func parseSignature(s string) (string, bool) {
	var sig signature
	if err := json.Unmarshal([]byte(s), &sig); err != nil || sig.Gemini == "" {
		return "", false
	}
	return sig.Gemini, true
}

// convertMessages maps the messages to the steps of the input, in the
// order of [transcript.Order]. The input has no system role, so each
// system message goes as a system-reminder in a user turn. Each block of
// an assistant message becomes one step, in order, because a thought
// step must come before the output that it produced.
func convertMessages(messages []ai.Message) ([]interactions.Step, error) {
	ordered := transcript.Order(messages, false)
	names := toolNames(ordered)

	var steps []interactions.Step
	for _, msg := range ordered {
		switch msg.Role {
		case ai.RoleUser:
			if content := contents(msg.Content); len(content) > 0 {
				steps = append(steps, interactions.NewStep(interactions.UserInputStep{Content: content}))
			}
		case ai.RoleAssistant:
			assistant, err := assistantSteps(msg.Content)
			if err != nil {
				return nil, err
			}
			steps = append(steps, assistant...)
		case ai.RoleToolResult:
			steps = append(steps, toolResultStep(msg, names[msg.ToolCallID]))
		}
	}
	return steps, nil
}

// toolNames maps the ID of each tool call to the name of its tool. A
// function result step names its tool, and a tool result message does
// not.
func toolNames(messages []ai.Message) map[string]string {
	names := map[string]string{}
	for _, msg := range messages {
		for _, c := range msg.Content {
			if call, ok := c.(ai.ToolCall); ok {
				names[call.ID] = call.Name
			}
		}
	}
	return names
}

func contents(blocks []ai.Content) []interactions.Content {
	var out []interactions.Content
	for _, c := range blocks {
		switch v := c.(type) {
		case ai.Text:
			if v.Text != "" {
				out = append(out, interactions.NewContent(textContent(v.Text)))
			}
		case ai.File:
			out = append(out, fileContent(v))
		}
	}
	return out
}

func textContent(text string) interactions.TextContent {
	return interactions.TextContent{Text: text}
}

func fileContent(f ai.File) interactions.Content {
	data := base64.StdEncoding.EncodeToString(f.Data)
	if strings.HasPrefix(f.MediaType, "image/") {
		mime := interactions.ImageContentMimeType(f.MediaType)
		return interactions.NewContent(interactions.ImageContent{
			Data:     &data,
			MimeType: &mime,
		})
	}
	mime := interactions.DocumentContentMimeType(f.MediaType)
	return interactions.NewContent(interactions.DocumentContent{
		Data:     &data,
		MimeType: &mime,
	})
}

// skipSignature is the signature that the API takes in place of a
// thought signature, for a tool call that another model made.
const skipSignature = "skip_thought_signature_validator"

// assistantSteps sends a thinking block only with a signature from this
// provider, because the API checks the signature.
//
// The API refuses a function call turn with no signature. If the
// message has a tool call and no signature from this provider, the
// tool call came from another model, and the steps start with a
// thought step that has [skipSignature].
func assistantSteps(blocks []ai.Content) ([]interactions.Step, error) {
	var (
		steps     []interactions.Step
		signed    bool
		toolCalls bool
	)
	for _, c := range blocks {
		switch v := c.(type) {
		case ai.ThinkingBlock:
			if step, ok := thoughtStep(v); ok {
				steps = append(steps, step)
				signed = true
			}
		case ai.Text:
			if v.Text != "" {
				content := []interactions.Content{interactions.NewContent(textContent(v.Text))}
				steps = append(steps, interactions.NewStep(interactions.ModelOutputStep{Content: content}))
			}
		case ai.ToolCall:
			step, err := functionCallStep(v)
			if err != nil {
				return nil, err
			}
			steps = append(steps, step)
			toolCalls = true
		}
	}

	if toolCalls && !signed {
		sig := skipSignature
		skip := interactions.NewStep(interactions.ThoughtStep{Signature: &sig})
		steps = append([]interactions.Step{skip}, steps...)
	}
	return steps, nil
}

// The bool is false if the signature is not from this provider.
func thoughtStep(block ai.ThinkingBlock) (interactions.Step, bool) {
	sig, ok := parseSignature(block.Signature)
	if !ok {
		return interactions.Step{}, false
	}

	step := interactions.ThoughtStep{Signature: &sig}
	if block.Thinking != "" {
		step.Summary = []interactions.ThoughtSummaryContent{
			interactions.NewThoughtSummaryContent(textContent(block.Thinking)),
		}
	}
	return interactions.NewStep(step), true
}

// The API takes the arguments as an object, so an empty input is an
// empty object.
func functionCallStep(call ai.ToolCall) (interactions.Step, error) {
	args := map[string]any{}
	if len(call.Input) > 0 {
		if err := json.Unmarshal(call.Input, &args); err != nil {
			return interactions.Step{}, fmt.Errorf("gemini: input of tool call %q: %w", call.ID, err)
		}
	}
	return interactions.NewStep(interactions.FunctionCallStep{
		ID:        call.ID,
		Name:      call.Name,
		Arguments: args,
	}), nil
}

func toolResultStep(msg ai.Message, name string) interactions.Step {
	var result []interactions.FunctionResultSubcontent
	for _, c := range contents(msg.Content) {
		result = append(result, subcontent(c))
	}

	step := interactions.FunctionResultStep{
		CallID: msg.ToolCallID,
		Result: interactions.NewFunctionResultStepResultUnion(result),
	}
	if name != "" {
		step.Name = &name
	}
	if msg.IsError {
		isError := true
		step.IsError = &isError
	}
	return interactions.NewStep(step)
}

// The two unions have the same members.
func subcontent(c interactions.Content) interactions.FunctionResultSubcontent {
	switch {
	case c.ImageContent != nil:
		return interactions.NewFunctionResultSubcontent(*c.ImageContent)
	case c.DocumentContent != nil:
		return interactions.NewFunctionResultSubcontent(*c.DocumentContent)
	default:
		return interactions.NewFunctionResultSubcontent(*c.TextContent)
	}
}
