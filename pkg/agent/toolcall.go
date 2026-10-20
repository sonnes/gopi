package agent

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/session"
	"github.com/sonnes/gopi/pkg/tool"
)

// runsInOrder reports if the calls run one after another: with one
// call, or with a call of a sequential tool.
func (a *Agent) runsInOrder(declared []ai.ToolInfo, calls []ai.ToolCall) bool {
	if len(calls) < 2 {
		return true
	}
	return slices.ContainsFunc(calls, func(tc ai.ToolCall) bool {
		t := a.tool(declared, tc.Name)
		return t != nil && t.Info().Sequential
	})
}

// runTools runs one batch of calls at the same time. It emits every
// tool_execution_start in call order, and runs the BeforeTool hooks of
// each call after its start. Then it runs the calls. As each call
// finishes, it emits its tool_execution_end and stores its result, so
// a crash keeps each result that finished. The results are in the order
// in which the calls finish. If ctx is done, a call that did not finish
// adds no result. Turn 1 of the next run adds a [session.Interrupted]
// result for it. Calls in order run as batches of one call.
func (a *Agent) runTools(
	ctx context.Context,
	declared []ai.ToolInfo,
	calls []ai.ToolCall,
	em *emitter,
) ([]ai.Message, error) {
	prepared := make([]pending, len(calls))
	for i, tc := range calls {
		em.emit(startEvent(tc))
		prepared[i] = a.prepare(ctx, declared, tc)
	}

	var (
		mu  sync.Mutex
		out = make([]ai.Message, 0, len(calls))
	)
	errs := make([]error, len(calls))
	var wg sync.WaitGroup
	for i, tc := range calls {
		wg.Go(func() {
			result, err := a.execute(ctx, prepared[i])
			// The lock keeps the end and the result of one call together,
			// and out in the order of the log.
			mu.Lock()
			defer mu.Unlock()
			em.emit(endEvent(tc, result))
			if err != nil {
				errs[i] = err
				return
			}
			msg, err := a.addResult(ctx, em, tc, result)
			if err != nil {
				errs[i] = err
				return
			}
			out = append(out, msg)
		})
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return out, err
	}
	return out, errors.Join(errs...)
}

func startEvent(tc ai.ToolCall) Event {
	return EventToolExecutionStart{
		ToolCallID: tc.ID,
		ToolName:   tc.Name,
		Args:       tc.Input,
	}
}

func endEvent(tc ai.ToolCall, result ai.ToolResult) Event {
	return EventToolExecutionEnd{
		ToolCallID: tc.ID,
		ToolName:   tc.Name,
		Result:     result,
	}
}

// addResult stores the result of tc in the log as a tool result
// message, sends its events, and returns the message.
func (a *Agent) addResult(
	ctx context.Context,
	em *emitter,
	tc ai.ToolCall,
	result ai.ToolResult,
) (ai.Message, error) {
	msg := ai.ToolResultMessage(tc.ID, result.Content...)
	msg.IsError = result.IsError
	entry := session.NewEntry(msg, session.Author(a.cfg.name))
	_, err := a.add(ctx, em, entry)
	return msg, err
}

// pending is a tool call after its BeforeTool hooks. If tool is nil,
// the call does not run, and result and err are its outcome.
type pending struct {
	call   ai.ToolCall
	tool   tool.Tool
	result ai.ToolResult
	err    error
}

// prepare finds the tool of tc in declared and runs the BeforeTool
// hooks. An unknown tool is an error result, because the model can
// correct the name, and no hook runs. An error from a hook blocks the
// call, and its text is the error result. If ctx is done, err is set:
// the run then fails, and the result is not added. A done ctx before
// the hooks skips them, because the run fails anyway.
func (a *Agent) prepare(ctx context.Context, declared []ai.ToolInfo, tc ai.ToolCall) pending {
	if err := ctx.Err(); err != nil {
		return pending{
			call:   tc,
			result: errorResult(err.Error()),
			err:    err,
		}
	}
	t := a.tool(declared, tc.Name)
	if t == nil {
		return pending{call: tc, result: a.notFound(tc.Name)}
	}

	call := tc
	for _, h := range a.cfg.hooks {
		bt, ok := h.(BeforeToolHook)
		if !ok {
			continue
		}
		next, err := bt.BeforeTool(ctx, a.State(), call)
		if err != nil {
			result, err := failure(ctx, err)
			return pending{call: call, result: result, err: err}
		}
		call = next
	}

	if call.Name != tc.Name {
		t = a.tool(declared, call.Name)
		if t == nil {
			return pending{call: call, result: a.notFound(call.Name)}
		}
	}
	return pending{call: call, tool: t}
}

// execute runs the tool of p and the AfterTool hooks, and returns the
// result. A Go error from the tool or a hook is an error result, so
// every call has a result and the run goes on. It returns an error only
// when ctx is done: the run then fails, and the result is not added.
func (a *Agent) execute(ctx context.Context, p pending) (ai.ToolResult, error) {
	if p.tool == nil {
		return p.result, p.err
	}

	result, err := p.tool.Run(ctx, p.call)
	if err != nil {
		return failure(ctx, err)
	}
	for _, h := range a.cfg.hooks {
		at, ok := h.(AfterToolHook)
		if !ok {
			continue
		}
		result, err = at.AfterTool(ctx, a.State(), p.call, result)
		if err != nil {
			return failure(ctx, err)
		}
	}
	return result, nil
}

// failure returns the error result of err. If ctx is done, it returns
// the error of ctx too, so the run fails.
func failure(ctx context.Context, err error) (ai.ToolResult, error) {
	if ctx.Err() != nil {
		return errorResult(ctx.Err().Error()), ctx.Err()
	}
	return errorResult(err.Error()), nil
}

// notFound returns the result of a call to a tool that the model
// cannot call. A deferred tool that no search loaded names the search,
// so the model loads it and calls again.
func (a *Agent) notFound(name string) ai.ToolResult {
	if t := a.known(name); t != nil && t.Info().Deferred {
		return errorResult(fmt.Sprintf("tool %q is deferred. Load it with %s first.", name, searchToolName))
	}
	return errorResult(fmt.Sprintf("tool %q not found", name))
}

func textResult(s string) ai.ToolResult {
	return ai.ToolResult{Content: []ai.Content{ai.Text{Text: s}}}
}

func errorResult(s string) ai.ToolResult {
	return ai.ToolResult{
		Content: []ai.Content{ai.Text{Text: s}},
		IsError: true,
	}
}
