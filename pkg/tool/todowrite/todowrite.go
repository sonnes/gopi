// Package todowrite provides the todo_write tool. The model sends the full
// updated checklist for the current session. The tool validates the
// checklist and returns an acknowledgement.
//
// The tool has no store. A caller shows the checklist from the input of
// the tool call.
package todowrite

import (
	"context"
	"errors"
	"fmt"

	_ "embed"

	"github.com/sonnes/gopi/pkg/tool"
)

// Description is the description of the tool for the model. It comes
// from description.md.
//
//go:embed description.md
var Description string

// Name is the name of the tool.
const Name = "todo_write"

// Status is the state of one todo item.
type Status string

const (
	// StatusPending is a task that is not started.
	StatusPending Status = "pending"
	// StatusInProgress is the task in work now. Only one item can hold
	// this status at a time.
	StatusInProgress Status = "in_progress"
	// StatusCompleted is a task that is done.
	StatusCompleted Status = "completed"
)

// maxInProgress is the maximum number of items with [StatusInProgress].
const maxInProgress = 1

// Item is one entry in the checklist.
type Item struct {
	Content    string `json:"content"     jsonschema:"Imperative form describing the task, e.g. \"Run tests\". Required."`
	ActiveForm string `json:"active_form" jsonschema:"Present-continuous form shown while the task runs, e.g. \"Running tests\". Required."`
	Status     Status `json:"status"      jsonschema:"One of: pending, in_progress, completed."`
}

// Input is the input of one call. It holds the full updated list. The
// new list replaces the previous list completely.
type Input struct {
	Todos []Item `json:"todos" jsonschema:"The full updated todo list. Replaces the previous list entirely. Keep exactly one item in_progress while work is ongoing."`
}

// ackMessage is the result of every valid update.
const ackMessage = "Todos have been modified successfully. Ensure that you continue to use the todo list to track your progress. Please proceed with the current tasks if applicable."

// shapeExample helps the model correct the call after a validation error.
const shapeExample = `Expected shape: {"todos":[{"content":"Run tests","active_form":"Running tests","status":"in_progress"},{"content":"Ship it","active_form":"Shipping it","status":"pending"}]}. Each item needs a non-empty content (imperative) and active_form (present continuous); status is one of pending, in_progress, completed; at most one item may be in_progress.`

// New returns the todo_write tool. An invalid checklist becomes an error
// result with an example of the correct shape.
func New() tool.Tool {
	return tool.New(
		Name,
		run,
		tool.Description(Description),
	)
}

// run validates the checklist and returns the acknowledgement.
func run(_ context.Context, in Input) (string, error) {
	if err := validate(in); err != nil {
		return "", fmt.Errorf("%w. %s", err, shapeExample)
	}
	return ackMessage, nil
}

// validate returns an error for the first item that breaks a rule.
func validate(in Input) error {
	// A missing todos key, or a key with another name, decodes to nil.
	// An empty list clears the checklist.
	if in.Todos == nil {
		return errors.New("todowrite: todos is required")
	}
	inProgress := 0
	for i, todo := range in.Todos {
		if todo.Content == "" {
			return fmt.Errorf("todowrite: todo %d: content is required", i)
		}
		if todo.ActiveForm == "" {
			return fmt.Errorf("todowrite: todo %d: active_form is required", i)
		}
		switch todo.Status {
		case StatusPending, StatusInProgress, StatusCompleted:
		default:
			return fmt.Errorf("todowrite: todo %d: status must be one of pending, in_progress, completed", i)
		}
		if todo.Status == StatusInProgress {
			inProgress++
		}
	}
	if inProgress > maxInProgress {
		return fmt.Errorf("todowrite: at most one todo may be in_progress at a time (found %d)", inProgress)
	}
	return nil
}
