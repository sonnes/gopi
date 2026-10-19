package main

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/harness/permission"
	"github.com/sonnes/gopi/pkg/tool/askquestion"
)

// terminal answers the questions of the ask_user_question tool on the
// terminal. It writes each question to w and reads one answer from in.
// The prompt loop reads the same input. If shown is set, a question
// waits until the tree prints its call.
type terminal struct {
	w     io.Writer
	in    *input
	shown *shownCalls
}

// Ask writes each question with its numbered options and reads one line
// for each. A number picks an option. For a multi-select question,
// numbers with commas pick more than one. Any other line is the answer
// as text.
func (r terminal) Ask(
	ctx context.Context,
	toolCallID string,
	questions []askquestion.Question,
) ([]askquestion.Answer, error) {
	if err := r.shown.wait(ctx, toolCallID); err != nil {
		return nil, err
	}

	answers := make([]askquestion.Answer, 0, len(questions))
	for _, q := range questions {
		shown := time.Now()
		r.show(q)

		line, err := r.in.after(ctx, shown)
		if err != nil {
			return nil, err
		}

		answer := askquestion.Answer{
			Header: q.Header,
			Answer: pick(q, strings.TrimSpace(line)),
		}
		answers = append(answers, answer)
	}
	return answers, nil
}

func (r terminal) show(q askquestion.Question) {
	fmt.Fprintf(r.w, "? %s: %s\n", q.Header, q.Question)
	for i, o := range q.Options {
		line := fmt.Sprintf("  %d. %s", i+1, o.Label)
		if o.Description != "" {
			line += "  " + o.Description
		}
		fmt.Fprintln(r.w, line)
	}
	if q.MultiSelect {
		fmt.Fprint(r.w, "answers, with commas: ")
		return
	}
	fmt.Fprint(r.w, "answer: ")
}

// pick returns the labels of the options that line numbers. If line is
// not a valid choice, pick returns line as it is.
func pick(q askquestion.Question, line string) string {
	parts := []string{line}
	if q.MultiSelect {
		parts = strings.Split(line, ",")
	}

	labels := make([]string, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil || n < 1 || n > len(q.Options) {
			return line
		}
		labels = append(labels, q.Options[n-1].Label)
	}
	return strings.Join(labels, ", ")
}

// Approve asks on the terminal if a tool call can run. "y" allows the
// call. "a" allows it and each later call that the same rule matches.
// Any other line denies it.
func (r terminal) Approve(ctx context.Context, call ai.ToolCall) (permission.Decision, error) {
	if err := r.shown.wait(ctx, call.ID); err != nil {
		return permission.Deny, err
	}

	shown := time.Now()
	fmt.Fprintf(r.w, "? allow %s %s? [y]es, [a]lways, [n]o: ", call.Name, compact(call.Input))
	line, err := r.in.after(ctx, shown)
	if err != nil {
		return permission.Deny, err
	}

	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return permission.Allow, nil
	case "a", "always":
		return permission.AllowAlways, nil
	default:
		return permission.Deny, nil
	}
}
