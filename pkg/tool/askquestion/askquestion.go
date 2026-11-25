// Package askquestion provides the ask_user_question tool. The model sends
// one batch of multiple-choice questions, and the tool validates the
// batch. The answers come in one of two ways:
//
//   - [New] blocks until a [Responder] gives the answers. Use it when the
//     caller shows the questions on its own surface, such as a terminal
//     prompt or a web form.
//   - [ViaApprover] reads the answers from the input of the call. Use it
//     when a host shows the questions in its approval of the call, and
//     the approver allows the call with the answers in a new input, as
//     [WithAnswers] makes it.
//
// The tool does not show the questions.
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
const Name = tool.AskUserQuestion

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
// answers. If responder is nil, New panics, because that is a bug in the
// caller.
//
// The run function needs the ID of the tool call, so New builds the tool
// with [tool.Func] and not with [tool.New]. The tool is sequential: the
// other calls of its message wait for the answers, and a user sees one
// question batch at a time.
func New(responder Responder) tool.Tool {
	if responder == nil {
		panic("askquestion: New: no responder")
	}
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

// ViaApprover returns the ask_user_question tool for a host that
// answers it with its approver, as Claude Code does. The tool does not
// ask a [Responder]. The approver allows the call with a new input that
// [WithAnswers] makes: the input of the model with an "answers" key.
// The key maps the text of each question to the answer of the user: the
// label that the user picked, or the free text that the user wrote. For
// a question with multi_select, the answer joins the picked labels with
// ", ", as Claude Code does. The tool then returns these answers. A call
// with no answer for a question fails, so the model can ask again.
//
// The "answers" key is not in the input schema, so only the approver
// gives it. The caller must ask the approver about each call, even in a
// mode that allows each call, and must remove the answers of the model
// with [WithoutAnswers] before it asks, so the model cannot answer its
// own question. harness.AskTool does both.
func ViaApprover() tool.Tool {
	schema, err := jsonschema.For[Input](nil)
	if err != nil {
		panic(fmt.Sprintf("askquestion: input schema: %v", err))
	}

	return tool.Func(
		Name,
		answered,
		tool.InputSchema(schema),
		tool.Description(Description),
		tool.Sequential(),
	)
}

// answersKey is the key of the input that holds the answers of the
// approver.
const answersKey = "answers"

// WithAnswers returns input with answers as its "answers" key, as the
// tool of [ViaApprover] reads them. answers maps the text of each
// question to its answer. An "answers" key of input is replaced. An
// input that is not a JSON object fails.
func WithAnswers(input json.RawMessage, answers map[string]string) (json.RawMessage, error) {
	fields, err := inputFields(input)
	if err != nil {
		return nil, err
	}

	raw, err := json.Marshal(answers)
	if err != nil {
		return nil, fmt.Errorf("askquestion: marshal the answers: %w", err)
	}
	fields[answersKey] = raw

	return json.Marshal(fields)
}

// WithoutAnswers returns input with no "answers" key. An input that is
// not a JSON object fails.
func WithoutAnswers(input json.RawMessage) (json.RawMessage, error) {
	fields, err := inputFields(input)
	if err != nil {
		return nil, err
	}
	delete(fields, answersKey)

	return json.Marshal(fields)
}

// inputFields returns the fields of the JSON object input. An empty
// input has no fields.
func inputFields(input json.RawMessage) (map[string]json.RawMessage, error) {
	fields := map[string]json.RawMessage{}
	if len(input) == 0 {
		return fields, nil
	}
	if err := json.Unmarshal(input, &fields); err != nil {
		return nil, fmt.Errorf("askquestion: the input is not a JSON object: %w", err)
	}

	return fields, nil
}

// answered returns the answers that the approver added to the input of
// call. It looks up each answer by the text of its question, and the
// result gives each answer with the header of its question. It reads
// the "answers" key apart from the questions, so a bad shape of the
// answers is an error of the approver and not of the model.
func answered(_ context.Context, call ai.ToolCall) (ai.ToolResult, error) {
	var in Input
	if err := json.Unmarshal(call.Input, &in); err != nil {
		msg := fmt.Sprintf("askquestion: invalid input: %s. %s", err, shapeExample)
		return errorResult(msg), nil
	}

	if err := validate(in); err != nil {
		return errorResult(err.Error() + ". " + shapeExample), nil
	}

	fields, err := inputFields(call.Input)
	if err != nil {
		return errorResult(err.Error()), nil
	}
	var given map[string]string
	if raw, ok := fields[answersKey]; ok {
		if err := json.Unmarshal(raw, &given); err != nil {
			msg := fmt.Sprintf("askquestion: the approver gave the answers in a bad shape: %s", err)
			return errorResult(msg), nil
		}
	}

	answers := make([]Answer, 0, len(in.Questions))
	for _, q := range in.Questions {
		answer, ok := given[q.Question]
		if !ok {
			return errorResult(fmt.Sprintf("askquestion: the user gave no answer to %q", q.Question)), nil
		}
		answers = append(answers, Answer{
			Header: q.Header,
			Answer: answer,
		})
	}

	payload, err := json.Marshal(Output{Answers: answers})
	if err != nil {
		return errorResult("askquestion: marshal answers: " + err.Error()), nil
	}

	return ai.ToolResult{
		Content: []ai.Content{ai.Text{Text: string(payload)}},
	}, nil
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
	texts := make(map[string]bool, len(in.Questions))
	for i, question := range in.Questions {
		if err := validateQuestion(i, question); err != nil {
			return err
		}
		if headers[question.Header] {
			return fmt.Errorf("askquestion: headers must be unique: %q", question.Header)
		}
		headers[question.Header] = true
		// An approver gives each answer by the text of its question.
		if texts[question.Question] {
			return fmt.Errorf("askquestion: questions must be unique: %q", question.Question)
		}
		texts[question.Question] = true
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
