package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/agent/internal/hooktext"
	"github.com/sonnes/gopi/pkg/ai"
)

// The hook events of the CLI at the points of a gopi hook. The agent
// registers each event as one callback with the name of the event.
const (
	eventPreToolUse         = "PreToolUse"
	eventPostToolUse        = "PostToolUse"
	eventPostToolUseFailure = "PostToolUseFailure"
	eventPostToolBatch      = "PostToolBatch"
	eventStop               = "Stop"
)

// hookPoints are the points of a gopi hook that the CLI calls. The CLI
// has no hook at a model call, so BeforeCall and AfterCall are not in
// it.
var hookPoints = []agent.Point{
	agent.PointBeforeTool,
	agent.PointAfterTool,
	agent.PointAfterTurn,
	agent.PointBeforeStop,
}

// renameText is the reason of a deny when a hook renames a tool call.
const renameText = "claude: a hook cannot rename a tool call of Claude Code"

// Hooks adds hooks that the CLI calls at its own hook events, over the
// control protocol:
//
//   - BeforeTool at PreToolUse. An error denies the call with its text.
//     A new input replaces the input. The CLI cannot rename a call, so
//     a new name denies it.
//   - AfterTool at PostToolUse. The result has the text of the
//     tool_response. A built-in tool such as Bash gives a JSON object,
//     so its result is the JSON of that object. A new text replaces the
//     output that the model reads. If the tool_response is an object
//     and the new text is valid JSON, the CLI gets the JSON. Otherwise
//     the CLI gets a string. The CLI ignores an output whose shape is
//     not the shape of the tool_response. So a hook must keep the JSON
//     shape of a built-in tool. An error adds its text to the result.
//   - AfterTool also at PostToolUseFailure, after a call that fails.
//     The result is an error with the error text of the CLI. The CLI
//     cannot replace the output of a failed call. So the model reads
//     the error, and a new text or the text of an error comes after it
//     as context.
//   - AfterTurn at PostToolBatch, after a turn with tool calls only.
//     The text of the entries reaches the model before its next call.
//   - BeforeStop at Stop. The text of the entries continues the run.
//
// Each hook gets the state of [Agent.State], with the log of the agent.
// A hook gives only text entries. An error of AfterTurn or
// BeforeStop stops the CLI and fails the run. If a hook uses BeforeCall
// or AfterCall, the run fails with an error that wraps
// [errors.ErrUnsupported].
func Hooks(hs ...agent.Hook) Option {
	return func(c *config) { c.hooks = append(c.hooks, hs...) }
}

// hookMatcher registers one callback for a hook event.
type hookMatcher struct {
	HookCallbackIDs []string `json:"hookCallbackIds"`
}

// registration returns the hooks field of the initialize request: one
// callback for each event that a hook uses.
func registration(hooks []agent.Hook) map[string][]hookMatcher {
	events := make(map[string][]hookMatcher)
	for _, h := range hooks {
		for _, p := range agent.Points(h) {
			for _, event := range eventsOf(p) {
				events[event] = []hookMatcher{{HookCallbackIDs: []string{event}}}
			}
		}
	}
	if len(events) == 0 {
		return nil
	}
	return events
}

// eventsOf returns the hook events of the CLI at the point p. The CLI
// fires PostToolUse after a call that succeeds, and PostToolUseFailure
// after a call that fails, so AfterTool has both.
func eventsOf(p agent.Point) []string {
	switch p {
	case agent.PointBeforeTool:
		return []string{eventPreToolUse}
	case agent.PointAfterTool:
		return []string{eventPostToolUse, eventPostToolUseFailure}
	case agent.PointAfterTurn:
		return []string{eventPostToolBatch}
	case agent.PointBeforeStop:
		return []string{eventStop}
	}
	return nil
}

// hookInput is the input of a hook callback.
type hookInput struct {
	HookEventName string          `json:"hook_event_name"`
	ToolName      string          `json:"tool_name"`
	ToolInput     json.RawMessage `json:"tool_input"`
	ToolResponse  json.RawMessage `json:"tool_response"`
	ToolUseID     string          `json:"tool_use_id"`
	// Error is the error of a failed call, at PostToolUseFailure.
	Error string `json:"error"`
}

// hookOutput is the answer to a hook callback. The zero value changes
// nothing.
type hookOutput struct {
	// Continue false stops the CLI, with StopReason.
	Continue   *bool  `json:"continue,omitempty"`
	StopReason string `json:"stopReason,omitempty"`
	// Decision "block" with a Reason continues a stop, or adds the
	// reason to a tool result.
	Decision           string        `json:"decision,omitempty"`
	Reason             string        `json:"reason,omitempty"`
	HookSpecificOutput *hookSpecific `json:"hookSpecificOutput,omitempty"`
}

type hookSpecific struct {
	HookEventName            string          `json:"hookEventName"`
	PermissionDecision       string          `json:"permissionDecision,omitempty"`
	PermissionDecisionReason string          `json:"permissionDecisionReason,omitempty"`
	UpdatedInput             json.RawMessage `json:"updatedInput,omitempty"`
	UpdatedToolOutput        json.RawMessage `json:"updatedToolOutput,omitempty"`
	AdditionalContext        string          `json:"additionalContext,omitempty"`
}

// callHook runs the hooks of one hook_callback request, in the order
// of registration, and returns the answer.
func (pr *process) callHook(ctx context.Context, req *controlRequest) (hookOutput, error) {
	var in hookInput
	if err := json.Unmarshal(req.Input, &in); err != nil {
		return hookOutput{}, fmt.Errorf("claude: decode the input of the hook %q: %w", req.CallbackID, err)
	}
	switch req.CallbackID {
	case eventPreToolUse:
		return pr.beforeTool(ctx, in), nil
	case eventPostToolUse:
		return pr.afterTool(ctx, in), nil
	case eventPostToolUseFailure:
		return pr.afterToolFailure(ctx, in), nil
	case eventPostToolBatch:
		return pr.afterTurn(ctx), nil
	case eventStop:
		return pr.beforeStop(ctx), nil
	}
	return hookOutput{}, fmt.Errorf("claude: gopi has no hook %q", req.CallbackID)
}

func (pr *process) beforeTool(ctx context.Context, in hookInput) hookOutput {
	orig := ai.ToolCall{
		ID:    in.ToolUseID,
		Name:  in.ToolName,
		Input: in.ToolInput,
	}
	call := orig
	for _, h := range pr.hooks {
		bt, ok := h.(agent.BeforeToolHook)
		if !ok {
			continue
		}
		next, err := bt.BeforeTool(ctx, pr.state(), call)
		if err != nil {
			return denyTool(err.Error())
		}
		call = next
	}
	if call.Name != orig.Name {
		return denyTool(renameText)
	}
	if bytes.Equal(call.Input, orig.Input) {
		return hookOutput{}
	}
	return hookOutput{HookSpecificOutput: &hookSpecific{
		HookEventName: eventPreToolUse,
		UpdatedInput:  call.Input,
	}}
}

func denyTool(reason string) hookOutput {
	return hookOutput{HookSpecificOutput: &hookSpecific{
		HookEventName:            eventPreToolUse,
		PermissionDecision:       "deny",
		PermissionDecisionReason: reason,
	}}
}

func (pr *process) afterTool(ctx context.Context, in hookInput) hookOutput {
	orig := hooktext.Response(in.ToolResponse)
	result, err := pr.runAfterTool(ctx, in, hooktext.ToolResult(orig))
	if err != nil {
		return hookOutput{
			Decision: "block",
			Reason:   err.Error(),
		}
	}
	text := hooktext.Result(result)
	if text == orig {
		return hookOutput{}
	}
	return hookOutput{HookSpecificOutput: &hookSpecific{
		HookEventName:     eventPostToolUse,
		UpdatedToolOutput: toolOutput(in.ToolResponse, text),
	}}
}

// afterToolFailure runs the AfterTool hooks on a failed call. The
// result is an error with the error text of the CLI. The CLI takes only
// context at PostToolUseFailure, so a new text or the text of an error
// goes to the model next to the error.
func (pr *process) afterToolFailure(ctx context.Context, in hookInput) hookOutput {
	failed := hooktext.ToolResult(in.Error)
	failed.IsError = true
	result, err := pr.runAfterTool(ctx, in, failed)
	text := hooktext.Result(result)
	if err != nil {
		text = err.Error()
	}
	if text == in.Error {
		return hookOutput{}
	}
	return hookOutput{HookSpecificOutput: &hookSpecific{
		HookEventName:     eventPostToolUseFailure,
		AdditionalContext: text,
	}}
}

// runAfterTool runs the AfterTool hooks on the result of the call in
// in, in the order of registration. It stops at the first error.
func (pr *process) runAfterTool(
	ctx context.Context,
	in hookInput,
	result ai.ToolResult,
) (ai.ToolResult, error) {
	call := ai.ToolCall{
		ID:    in.ToolUseID,
		Name:  in.ToolName,
		Input: in.ToolInput,
	}
	for _, h := range pr.hooks {
		at, ok := h.(agent.AfterToolHook)
		if !ok {
			continue
		}
		next, err := at.AfterTool(ctx, pr.state(), call, result)
		if err != nil {
			return result, err
		}
		result = next
	}
	return result, nil
}

// toolOutput returns the updatedToolOutput for the new text of a
// result. The CLI ignores an output whose shape is not the shape of
// the tool_response. If the tool_response is not a JSON string and the
// text is valid JSON, the output is that JSON. Otherwise the output is
// the text as a JSON string.
func toolOutput(response json.RawMessage, text string) json.RawMessage {
	var s string
	isString := json.Unmarshal(response, &s) == nil
	if !isString && json.Valid([]byte(text)) {
		return json.RawMessage(text)
	}
	data, _ := json.Marshal(text)
	return data
}

func (pr *process) afterTurn(ctx context.Context) hookOutput {
	var texts []string
	for _, h := range pr.hooks {
		at, ok := h.(agent.AfterTurnHook)
		if !ok {
			continue
		}
		entries, err := at.AfterTurn(ctx, pr.state())
		if err != nil {
			return pr.stopFor(eventPostToolBatch, err)
		}
		text, err := hooktext.Entries(entries)
		if err != nil {
			return pr.stopFor(eventPostToolBatch, err)
		}
		texts = append(texts, text)
	}
	extra := hooktext.Join(texts)
	if extra == "" {
		return hookOutput{}
	}
	return hookOutput{HookSpecificOutput: &hookSpecific{
		HookEventName:     eventPostToolBatch,
		AdditionalContext: extra,
	}}
}

func (pr *process) beforeStop(ctx context.Context) hookOutput {
	for _, h := range pr.hooks {
		bs, ok := h.(agent.BeforeStopHook)
		if !ok {
			continue
		}
		entries, err := bs.BeforeStop(ctx, pr.state())
		if err != nil {
			return pr.stopFor(eventStop, err)
		}
		if len(entries) == 0 {
			continue
		}
		text, err := hooktext.Entries(entries)
		if err != nil {
			return pr.stopFor(eventStop, err)
		}
		return hookOutput{
			Decision: "block",
			Reason:   text,
		}
	}
	return hookOutput{}
}

// stopFor keeps the error of a hook at event, so that the run fails,
// and returns the answer that stops the CLI.
func (pr *process) stopFor(event string, err error) hookOutput {
	if pr.hookErr == nil {
		pr.hookErr = fmt.Errorf("claude: the hook at %s: %w", event, err)
	}
	stop := false
	return hookOutput{
		Continue:   &stop,
		StopReason: err.Error(),
	}
}

// takeHookErr returns the error of a hook in this run, and clears it.
func (pr *process) takeHookErr() error {
	err := pr.hookErr
	pr.hookErr = nil
	return err
}
