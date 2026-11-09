package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/agent/internal/clihook"
)

// The hook events of the CLI that Codex does not have. The agent
// registers each event as one callback with the name of the event.
const (
	eventPostToolUseFailure = "PostToolUseFailure"
	eventPostToolBatch      = "PostToolBatch"
)

// hookEvents maps each point of a gopi hook that the CLI calls to the
// hook events of the CLI at that point. The CLI fires PostToolUse after
// a call that succeeds, and PostToolUseFailure after a call that fails,
// so AfterTool has both. The CLI has no hook at a model call.
var hookEvents = map[agent.Point][]string{
	agent.PointBeforeTool: {clihook.EventPreToolUse},
	agent.PointAfterTool:  {clihook.EventPostToolUse, eventPostToolUseFailure},
	agent.PointAfterTurn:  {eventPostToolBatch},
	agent.PointBeforeStop: {clihook.EventStop},
}

// checkHooks returns an error for a hook at a point that the CLI does
// not call, or for a hook that implements no point.
func checkHooks(hooks []agent.Hook) error {
	points := slices.Collect(maps.Keys(hookEvents))
	if err := agent.CheckHooks(hooks, points...); err != nil {
		return fmt.Errorf("claude: %w", err)
	}
	return nil
}

// renameText is the reason of a deny when a hook renames a tool call.
const renameText = "claude: a hook cannot rename a tool call of Claude Code"

// hookMatcher registers one callback for a hook event.
type hookMatcher struct {
	HookCallbackIDs []string `json:"hookCallbackIds"`
	// Timeout is the time limit of the callback, in seconds. Zero keeps
	// the default limit of the CLI.
	Timeout int `json:"timeout,omitempty"`
}

// registration returns the hooks field of the initialize request: one
// callback for each event that a hook uses. PreToolUse gets
// [clihook.AskTimeout], because a BeforeTool hook can wait for a person.
func registration(hooks []agent.Hook) map[string][]hookMatcher {
	events := make(map[string][]hookMatcher)
	for _, h := range hooks {
		for _, p := range agent.Points(h) {
			for _, event := range hookEvents[p] {
				m := hookMatcher{HookCallbackIDs: []string{event}}
				if event == clihook.EventPreToolUse {
					m.Timeout = clihook.AskTimeout
				}
				events[event] = []hookMatcher{m}
			}
		}
	}
	if len(events) == 0 {
		return nil
	}
	return events
}

// callHook runs the hooks of one hook_callback request, in the order
// of registration, and returns the answer.
func (pr *process) callHook(ctx context.Context, req *controlRequest) (clihook.Output, error) {
	var in clihook.Input
	if err := json.Unmarshal(req.Input, &in); err != nil {
		return clihook.Output{}, fmt.Errorf("claude: decode the input of the hook %q: %w", req.CallbackID, err)
	}
	switch req.CallbackID {
	case clihook.EventPreToolUse:
		return pr.beforeTool(ctx, in), nil
	case clihook.EventPostToolUse:
		return pr.afterTool(ctx, in), nil
	case eventPostToolUseFailure:
		return pr.afterToolFailure(ctx, in), nil
	case eventPostToolBatch:
		return pr.afterTurn(ctx), nil
	case clihook.EventStop:
		return clihook.BeforeStop(ctx, pr.hooks, pr.state, pr.stops), nil
	}
	return clihook.Output{}, fmt.Errorf("claude: gopi has no hook %q", req.CallbackID)
}

func (pr *process) beforeTool(ctx context.Context, in clihook.Input) clihook.Output {
	orig := in.Call()
	call, err := agent.RunBeforeTool(
		ctx,
		pr.hooks,
		pr.state,
		pr.approve,
		orig,
	)
	if err != nil {
		return clihook.Deny(err.Error())
	}
	if call.Name != orig.Name {
		return clihook.Deny(renameText)
	}
	if clihook.SameInput(call.Input, orig.Input) {
		return clihook.Output{}
	}
	return clihook.Output{HookSpecificOutput: &clihook.Specific{
		HookEventName: clihook.EventPreToolUse,
		UpdatedInput:  call.Input,
	}}
}

func (pr *process) afterTool(ctx context.Context, in clihook.Input) clihook.Output {
	orig := clihook.Response(in.ToolResponse)
	result, err := agent.RunAfterTool(
		ctx,
		pr.hooks,
		pr.state,
		in.Call(),
		clihook.ToolResult(orig),
	)
	if err != nil {
		return clihook.Block(err.Error())
	}
	text := clihook.Result(result)
	if text == orig {
		return clihook.Output{}
	}
	return clihook.Output{HookSpecificOutput: &clihook.Specific{
		HookEventName:     clihook.EventPostToolUse,
		UpdatedToolOutput: toolOutput(in.ToolResponse, text),
	}}
}

// afterToolFailure runs the AfterTool hooks on a failed call. The
// result is an error with the error text of the CLI. The CLI takes only
// context at PostToolUseFailure, so a new text or the text of an error
// goes to the model next to the error.
func (pr *process) afterToolFailure(ctx context.Context, in clihook.Input) clihook.Output {
	failed := clihook.ToolResult(in.Error)
	failed.IsError = true
	result, err := agent.RunAfterTool(
		ctx,
		pr.hooks,
		pr.state,
		in.Call(),
		failed,
	)
	text := clihook.Result(result)
	if err != nil {
		text = err.Error()
	}
	if text == in.Error {
		return clihook.Output{}
	}
	return clihook.Output{HookSpecificOutput: &clihook.Specific{
		HookEventName:     eventPostToolUseFailure,
		AdditionalContext: text,
	}}
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

func (pr *process) afterTurn(ctx context.Context) clihook.Output {
	var texts []string
	for _, h := range pr.hooks {
		at, ok := h.(agent.AfterTurnHook)
		if !ok {
			continue
		}
		entries, err := at.AfterTurn(ctx, pr.state())
		if err != nil {
			return pr.stops.Stop(eventPostToolBatch, err)
		}
		text, err := clihook.Entries(entries)
		if err != nil {
			return pr.stops.Stop(eventPostToolBatch, err)
		}
		texts = append(texts, text)
	}
	extra := clihook.Join(texts)
	if extra == "" {
		return clihook.Output{}
	}
	return clihook.Output{HookSpecificOutput: &clihook.Specific{
		HookEventName:     eventPostToolBatch,
		AdditionalContext: extra,
	}}
}
