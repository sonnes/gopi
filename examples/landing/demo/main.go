// Command demo is the program that the landing page replays. It runs a
// durable triage agent on a scripted model, so each run is the same and
// needs no key. web/scripts/record-demo.mjs runs it, stops it during a
// tool call, and runs it again with no task.
//
// Usage:
//
//	demo -dir <sessions dir> ["task"]
//
// The program prints one line for each tool call and the last answer.
// With -events, it prints each agent event as a JSON line to stderr.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/session"
	storefs "github.com/sonnes/gopi/pkg/store/fs"
	"github.com/sonnes/gopi/pkg/tool"
)

// readDelay is how long read_issue takes, so the recorder can stop the
// program while a call runs.
const readDelay = 2 * time.Second

func main() {
	dir := flag.String("dir", "sessions", "the folder of the sessions")
	events := flag.Bool("events", false, "print each agent event to stderr")
	flag.Parse()

	if err := run(context.Background(), *dir, *events, flag.Args()); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, dir string, events bool, args []string) error {
	store, err := storefs.Sessions(dir)
	if err != nil {
		return err
	}

	model := ai.LanguageModel{
		Model:    ai.Model{ID: "scripted"},
		Provider: scripted{},
	}
	a, err := agent.Durable(ctx, model, "triage",
		agent.Store(store),
		agent.Toolset(listIssues, readIssue),
	)
	if err != nil {
		return err
	}

	// With no input, the run continues from the last stored message.
	var input []session.Entry
	if len(args) > 0 {
		input = append(input, session.Text(args[0]))
	}

	stream := a.Run(ctx, input...)
	for event, err := range stream.Events() {
		if err != nil {
			return err
		}
		if events {
			data, err := json.Marshal(event)
			if err != nil {
				return err
			}
			fmt.Fprintln(os.Stderr, string(data))
		}
		if e, ok := event.(agent.EventToolExecutionStart); ok {
			fmt.Println("->", e.ToolName, string(e.Args))
		}
	}

	msgs, err := stream.Wait()
	if err != nil {
		return err
	}
	fmt.Println(msgs[len(msgs)-1].Text())
	return nil
}

// IssueIn is the input of read_issue.
type IssueIn struct {
	Number int `json:"number"`
}

// ListIn is the input of list_issues.
type ListIn struct {
	State string `json:"state"`
}

var listIssues = tool.New("list_issues",
	func(ctx context.Context, in ListIn) ([]int, error) {
		return []int{41, 42}, nil
	},
	tool.Description("List the issues in a state"),
)

var readIssue = tool.New("read_issue",
	func(ctx context.Context, in IssueIn) (string, error) {
		select {
		case <-time.After(readDelay):
		case <-ctx.Done():
			return "", ctx.Err()
		}
		return issues[in.Number], nil
	},
	tool.Description("Read one issue"),
)

var issues = map[int]string{
	41: "The login page returns 500. (bug)",
	42: "Add a dark theme. (feature)",
}

// scripted is a model that answers from the messages alone, so a run
// that resumes a session gets the next step. It counts the tool results
// that did not fail, and an interrupted call does not count.
type scripted struct{}

var _ ai.TextProvider = scripted{}

// steps are the answers of the model, one for each count of results.
var steps = []ai.Content{
	ai.ToolCall{ID: "call_1", Name: "list_issues", Input: json.RawMessage(`{"state":"open"}`)},
	ai.ToolCall{ID: "call_2", Name: "read_issue", Input: json.RawMessage(`{"number":41}`)},
	ai.ToolCall{ID: "call_3", Name: "read_issue", Input: json.RawMessage(`{"number":42}`)},
	ai.Text{Text: "Two issues are open: #41 is a bug, #42 asks for a feature."},
}

func (scripted) StreamText(
	ctx context.Context,
	model ai.Model,
	messages []ai.Message,
	opts ai.Options,
) *ai.EventStream {
	done := 0
	for _, m := range messages {
		if m.Role == ai.RoleToolResult && !m.IsError {
			done++
		}
	}
	if done >= len(steps) {
		return ai.ErrStream(errors.New("scripted: no step left"))
	}

	step := steps[done]
	if call, ok := step.(ai.ToolCall); ok {
		// A retried call gets a new ID, as a model gives it.
		call.ID = fmt.Sprintf("call_%d", len(messages))
		step = call
	}
	reply := ai.AssistantMessage(step)
	reply.Model = model.ID

	return ai.NewEventStream(func(push func(ai.Event)) (*ai.Message, error) {
		push(ai.Event{Type: ai.EventStart})
		switch b := step.(type) {
		case ai.Text:
			push(ai.Event{Type: ai.EventTextStart})
			push(ai.Event{Type: ai.EventTextDelta, Delta: b.Text})
			push(ai.Event{Type: ai.EventTextEnd, Content: b.Text})
			reply.StopReason = ai.StopReasonStop
		case ai.ToolCall:
			push(ai.Event{Type: ai.EventToolStart})
			push(ai.Event{Type: ai.EventToolEnd, ToolCall: &b})
			reply.StopReason = ai.StopReasonToolUse
		}
		return &reply, nil
	})
}
