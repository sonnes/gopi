// Package askquestion provides the ask_user_question tool. The model sends
// one batch of multiple-choice questions. The tool validates the batch
// and then blocks until a [Responder] gives the answers.
//
// The tool does not show the questions. The caller gives a [Responder]
// that shows them on a surface, such as a terminal prompt or a web form.
package askquestion

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"

	_ "embed"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/tool"
)

// Description is the description of the tool for the model. It comes
// from description.md.
//
//go:embed description.md
var Description string

// Name is the name of the tool.
const Name = "ask_user_question"

// HeaderMaxLen is the maximum length of a question header in bytes.
const HeaderMaxLen = 20

// MaxQuestions is the maximum number of questions in one tool call.
const MaxQuestions = 4

const (
	// minQuestions is the minimum number of questions in one tool call.
	minQuestions = 1
	// minOptions is the minimum number of options of one question.
	minOptions = 2
	// maxOptions is the maximum number of options of one question.
	maxOptions = 4
)

// Responder shows the questions of one call to the user and returns the answers.
//
// The surface of the responder can also accept an answer that no
// [Option] gives, such as free text.
//
// Ask receives the context of the tool call. When the context ends, Ask
// must return the context error, so that the tool call does not block
// forever.
type Responder interface {
	Ask(ctx context.Context, toolCallID string, questions []Question) ([]Answer, error)
}

// Input is one batch of questions that the user answers together.
type Input struct {
	Questions []Question `json:"questions" jsonschema:"One to four related questions answered in one batch."`
}

// Question is one multiple-choice prompt in a batch.
type Question struct {
	Header      string   `json:"header" jsonschema:"Unique short label with at most 20 bytes."`
	Question    string   `json:"question" jsonschema:"Complete question ending in a question mark."`
	Options     []Option `json:"options" jsonschema:"Two to four choices. Do not add an Other choice. The surface supplies its own free-text choice."`
	MultiSelect bool     `json:"multi_select,omitempty" jsonschema:"Allow more than one choice."`
}

// Option is one choice of a [Question].
type Option struct {
	Label       string `json:"label" jsonschema:"Display label with one to five words."`
	Description string `json:"description,omitempty" jsonschema:"Brief consequence or explanation of the choice."`
}

// Answer is the response of the user to one [Question]. Header
// identifies the question that it answers.
type Answer struct {
	Header string `json:"header"`
	Answer string `json:"answer"`
}

// Output is the batch of answers that goes back to the model.
type Output struct {
	Answers []Answer `json:"answers"`
}

// shapeExample helps the model correct the call after a validation error.
const shapeExample = `Expected shape: {"questions":[{"header":"Short","question":"Full question?","options":[{"label":"Option A","description":"..."},{"label":"Option B","description":"..."}],"multi_select":false}]}. Each option must be an object. Use one to four questions and two to four options per question.`

// New returns the ask_user_question tool. The tool asks responder for the
// answers. If responder is nil, each call returns an error result.
//
// The run function needs the ID of the tool call, so New builds the tool
// with [tool.Func] and not with [tool.New]. The tool is sequential: the
// other calls of its message wait for the answers, and a user sees one
// question batch at a time.
func New(responder Responder) tool.Tool {
	schema, err := jsonschema.For[Input](nil)
	if err != nil {
		panic(fmt.Sprintf("askquestion: input schema: %v", err))
	}

	run := func(ctx context.Context, call ai.ToolCall) (ai.ToolResult, error) {
		return ask(ctx, responder, call)
	}
	return tool.Func(
		Name,
		run,
		tool.InputSchema(schema),
		tool.Description(Description),
		tool.Sequential(),
	)
}

// ask validates the questions of call and then waits for the answers. A
// failure becomes an error result, so the model can correct the call. A
// canceled or expired context is the exception. Ask returns it as a Go
// error, so the agent stops the run.
func ask(ctx context.Context, responder Responder, call ai.ToolCall) (ai.ToolResult, error) {
	var in Input
	if err := json.Unmarshal(call.Input, &in); err != nil {
		msg := fmt.Sprintf("askquestion: invalid input: %s. %s", err, shapeExample)
		return errorResult(msg), nil
	}

	if err := validate(in); err != nil {
		return errorResult(err.Error() + ". " + shapeExample), nil
	}

	if responder == nil {
		return errorResult("askquestion: no responder configured"), nil
	}

	answers, err := responder.Ask(ctx, call.ID, in.Questions)
	if err != nil {
		if ctx.Err() != nil {
			return ai.ToolResult{}, ctx.Err()
		}
		return errorResult("askquestion: " + err.Error()), nil
	}

	payload, err := json.Marshal(Output{Answers: answers})
	if err != nil {
		return errorResult("askquestion: marshal answers: " + err.Error()), nil
	}

	return ai.ToolResult{
		Content: []ai.Content{ai.Text{Text: string(payload)}},
	}, nil
}

// errorResult returns a result that reports msg as an error to the model.
func errorResult(msg string) ai.ToolResult {
	return ai.ToolResult{
		Content: []ai.Content{ai.Text{Text: msg}},
		IsError: true,
	}
}

// validate returns an error for the first rule that the batch breaks.
func validate(in Input) error {
	if len(in.Questions) < minQuestions {
		return fmt.Errorf("askquestion: at least %d question is required", minQuestions)
	}
	if len(in.Questions) > MaxQuestions {
		return fmt.Errorf("askquestion: at most %d questions are allowed", MaxQuestions)
	}

	headers := make(map[string]bool, len(in.Questions))
	for i, question := range in.Questions {
		if err := validateQuestion(i, question); err != nil {
			return err
		}
		if headers[question.Header] {
			return fmt.Errorf("askquestion: headers must be unique: %q", question.Header)
		}
		headers[question.Header] = true
	}

	return nil
}

// validateQuestion returns an error for the first rule that question
// breaks. index is the position of question in the batch.
func validateQuestion(index int, question Question) error {
	if question.Question == "" {
		return fmt.Errorf("askquestion: question %d text is required", index)
	}
	if question.Question[len(question.Question)-1] != '?' {
		return fmt.Errorf("askquestion: question %d must end with a question mark", index)
	}
	if question.Header == "" {
		return fmt.Errorf("askquestion: question %d header is required", index)
	}
	if len(question.Header) > HeaderMaxLen {
		return fmt.Errorf("askquestion: question %d header must be at most %d bytes", index, HeaderMaxLen)
	}
	if len(question.Options) < minOptions {
		return fmt.Errorf("askquestion: question %d requires at least %d options", index, minOptions)
	}
	if len(question.Options) > maxOptions {
		return fmt.Errorf("askquestion: question %d allows at most %d options", index, maxOptions)
	}

	labels := make(map[string]bool, len(question.Options))
	for _, option := range question.Options {
		if option.Label == "" {
			return fmt.Errorf("askquestion: question %d option labels must not be empty", index)
		}
		if labels[option.Label] {
			return fmt.Errorf("askquestion: question %d option labels must be unique", index)
		}
		labels[option.Label] = true
	}

	return nil
}
