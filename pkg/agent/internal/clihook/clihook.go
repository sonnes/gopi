// Package clihook holds the hook wire that the agent CLIs share: the
// input of a hook event, its answer, and the text of the values of a
// gopi hook. A CLI takes only text from a hook, so a hook of a CLI
// agent gives text entries only.
package clihook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sync"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/tool"
)

// The hook events that Claude Code and Codex both send.
const (
	EventPreToolUse  = "PreToolUse"
	EventPostToolUse = "PostToolUse"
	EventStop        = "Stop"
)

// AskTimeout is the time limit, in seconds, that an agent gives the CLI
// for a PreToolUse hook: 24 hours. A BeforeTool hook can ask a person,
// and the hook waits for the answer. When a hook times out, each CLI
// runs the call. So the limit must be longer than a person takes to
// answer. A cancel of the run still ends the wait. The other hook
// events keep the default limit of the CLI.
const AskTimeout = 24 * 60 * 60

// Input is the input of a hook event.
type Input struct {
	HookEventName string          `json:"hook_event_name"`
	ToolName      string          `json:"tool_name"`
	ToolInput     json.RawMessage `json:"tool_input"`
	ToolResponse  json.RawMessage `json:"tool_response"`
	ToolUseID     string          `json:"tool_use_id"`
	// Error is the error of a failed call. Claude Code sends it at
	// PostToolUseFailure.
	Error string `json:"error"`
}

// Call returns the tool call of in, with the gopi name of the tool, as
// the log names it.
func (in Input) Call() ai.ToolCall {
	return ai.ToolCall{
		ID:    in.ToolUseID,
		Name:  tool.Canonical(in.ToolName),
		Input: in.ToolInput,
	}
}

// Output is the answer to a hook event. The zero value changes nothing.
type Output struct {
	// Continue false stops the CLI, with StopReason.
	Continue   *bool  `json:"continue,omitempty"`
	StopReason string `json:"stopReason,omitempty"`
	// Decision "block" with a Reason continues a stop, or replaces a
	// tool result. Each CLI says which.
	Decision           string    `json:"decision,omitempty"`
	Reason             string    `json:"reason,omitempty"`
	HookSpecificOutput *Specific `json:"hookSpecificOutput,omitempty"`
}

// Specific is the part of an [Output] for one hook event. Codex reads
// no UpdatedToolOutput and no AdditionalContext.
type Specific struct {
	HookEventName            string          `json:"hookEventName"`
	PermissionDecision       string          `json:"permissionDecision,omitempty"`
	PermissionDecisionReason string          `json:"permissionDecisionReason,omitempty"`
	UpdatedInput             json.RawMessage `json:"updatedInput,omitempty"`
	UpdatedToolOutput        json.RawMessage `json:"updatedToolOutput,omitempty"`
	AdditionalContext        string          `json:"additionalContext,omitempty"`
}

// Deny returns the answer at PreToolUse that blocks the call, with
// reason as the text that the model reads.
func Deny(reason string) Output {
	return Output{HookSpecificOutput: &Specific{
		HookEventName:            EventPreToolUse,
		PermissionDecision:       "deny",
		PermissionDecisionReason: reason,
	}}
}

// Block returns the answer with the decision "block" and reason.
func Block(reason string) Output {
	return Output{
		Decision: "block",
		Reason:   reason,
	}
}

// Stop returns the answer that stops the CLI, with reason.
func Stop(reason string) Output {
	stop := false
	return Output{
		Continue:   &stop,
		StopReason: reason,
	}
}

// SameInput reports whether a and b hold the same JSON value. A hook
// that encodes an input again can change the spaces or the order of
// the keys, and the input stays the same. If a or b is not valid JSON,
// SameInput compares the bytes.
func SameInput(a, b json.RawMessage) bool {
	if bytes.Equal(a, b) {
		return true
	}
	var va, vb any
	if json.Unmarshal(a, &va) != nil || json.Unmarshal(b, &vb) != nil {
		return false
	}
	return reflect.DeepEqual(va, vb)
}

// StopErr keeps the error of the first hook that stopped the CLI in a
// run, so that the run fails with it. It is safe for concurrent use.
type StopErr struct {
	// Agent names the agent at the start of the error, such as
	// "claude".
	Agent string

	mu  sync.Mutex
	err error
}

// Stop keeps err of a hook at event, unless s keeps an error already,
// and returns the answer that stops the CLI.
func (s *StopErr) Stop(event string, err error) Output {
	s.mu.Lock()
	if s.err == nil {
		s.err = fmt.Errorf("%s: the hook at %s: %w", s.Agent, event, err)
	}
	s.mu.Unlock()
	return Stop(err.Error())
}

// Take returns the kept error, or nil, and clears it.
func (s *StopErr) Take() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.err
	s.err = nil
	return err
}

// BeforeStop runs the BeforeStop hooks with [agent.RunBeforeStop], and
// returns the answer at Stop. The text of the entries of a hook blocks
// the stop, so the run goes on. An error of a hook, or an entry that is
// not text, stops the CLI, and stops keeps the error.
func BeforeStop(
	ctx context.Context,
	hooks []agent.Hook,
	state func() agent.State,
	stops *StopErr,
) Output {
	entries, err := agent.RunBeforeStop(ctx, hooks, state)
	if err == nil && len(entries) == 0 {
		return Output{}
	}
	var text string
	if err == nil {
		text, err = Entries(entries)
	}
	if err != nil {
		return stops.Stop(EventStop, err)
	}
	return Block(text)
}
