package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/sonnes/gopi/pkg/ai"
)

// Tool is one function that a model can call.
//
// Run has two failure channels. An [ai.ToolResult] with IsError set is
// a failure that the model reads and can correct, for example a bad
// path. A Go error is a failure of the run function, for example a
// canceled context or a transport that is down. A retry middleware acts
// on the Go error and passes the error result through. The agent turns
// a Go error that is left into an error result, and stops the run only
// on a canceled context.
type Tool interface {
	Info() ai.ToolInfo
	Run(ctx context.Context, call ai.ToolCall) (ai.ToolResult, error)
}

// Func returns a [Tool] from a name, a run function, and options, as
// [New] does. It is the raw form: run reads the input of the call
// itself, and [InputSchema] sets the schema that the model reads.
func Func(
	name string,
	run func(context.Context, ai.ToolCall) (ai.ToolResult, error),
	opts ...Option,
) Tool {
	info := ai.ToolInfo{Name: name}
	for _, opt := range opts {
		opt(&info)
	}
	return funcTool{info: info, run: run}
}

// Wrap returns a [Tool] with the info of next and the run function run.
// The options edit the info. A [Middleware] builds on Wrap, so the
// wrapped tool keeps its description, its schema, and its deferred
// flag.
func Wrap(
	next Tool,
	run func(context.Context, ai.ToolCall) (ai.ToolResult, error),
	opts ...Option,
) Tool {
	info := next.Info()
	for _, opt := range opts {
		opt(&info)
	}
	return funcTool{info: info, run: run}
}

type funcTool struct {
	info ai.ToolInfo
	run  func(context.Context, ai.ToolCall) (ai.ToolResult, error)
}

func (t funcTool) Info() ai.ToolInfo { return t.info }

func (t funcTool) Run(ctx context.Context, call ai.ToolCall) (ai.ToolResult, error) {
	return t.run(ctx, call)
}

// Option sets a field of the info of a tool from [New], [Func], or
// [Wrap].
type Option func(*ai.ToolInfo)

// Description sets the description of a tool. The model reads it to
// decide when to call the tool.
func Description(s string) Option {
	return func(info *ai.ToolInfo) { info.Description = s }
}

// InputSchema sets the schema of the input of a tool. [New] derives the
// schema from its input type, so only [Func] needs it.
func InputSchema(schema *jsonschema.Schema) Option {
	return func(info *ai.ToolInfo) { info.InputSchema = schema }
}

// Deferred marks a tool that the model cannot call until the agent adds
// it. See [ai.ToolInfo]. To defer a tool that is already built, use
// [Defer].
func Deferred() Option {
	return func(info *ai.ToolInfo) { info.Deferred = true }
}

// Sequential marks a tool that must not run at the same time as the
// other calls of one message. If a message calls a sequential tool, the
// agent runs every call of the message one after another. See
// [ai.ToolInfo].
func Sequential() Option {
	return func(info *ai.ToolInfo) { info.Sequential = true }
}

// Defer returns t as a deferred tool. A tool from another package, such
// as a sandbox tool, takes no [Option], so Defer marks it.
func Defer(t Tool) Tool {
	return Wrap(t, t.Run, Deferred())
}

// New returns a [Tool] with a typed input and output. It derives the
// input schema from In and decodes each call into an In. If In has no
// schema, New panics, because that is a bug in the tool.
//
// A string Out becomes one [ai.Text] block. An [ai.ToolResult] Out
// passes through. Any other Out becomes JSON text in one [ai.Text]
// block.
//
// An error from fn becomes an error result, because the model can read
// the error and correct the call. A canceled or expired context is the
// exception. Run returns it as a Go error, so the agent stops the run.
//
// New returns the interface, not a struct, so a middleware can wrap the
// tool without losing a method.
func New[In, Out any](
	name string,
	fn func(context.Context, In) (Out, error),
	opts ...Option,
) Tool {
	schema, err := jsonschema.For[In](nil)
	if err != nil {
		panic(fmt.Sprintf("tool: input schema for tool %s: %v", name, err))
	}

	run := func(ctx context.Context, call ai.ToolCall) (ai.ToolResult, error) {
		var in In
		if err := json.Unmarshal(call.Input, &in); err != nil {
			return errorResult("invalid input: " + err.Error()), nil
		}

		out, err := fn(ctx, in)
		if err != nil {
			if ctx.Err() != nil {
				return ai.ToolResult{}, err
			}
			return errorResult(err.Error()), nil
		}

		return marshalOutput(out)
	}

	opts = append([]Option{InputSchema(schema)}, opts...)
	return Func(name, run, opts...)
}

// errorResult returns a result that reports text as an error to the
// model.
func errorResult(text string) ai.ToolResult {
	return ai.ToolResult{
		Content: []ai.Content{ai.Text{Text: text}},
		IsError: true,
	}
}

// marshalOutput turns a typed output into an [ai.ToolResult].
func marshalOutput[Out any](out Out) (ai.ToolResult, error) {
	switch v := any(out).(type) {
	case ai.ToolResult:
		return v, nil
	case string:
		return ai.ToolResult{Content: []ai.Content{ai.Text{Text: v}}}, nil
	default:
		data, err := json.Marshal(out)
		if err != nil {
			return ai.ToolResult{}, fmt.Errorf("tool: encode output: %w", err)
		}
		return ai.ToolResult{Content: []ai.Content{ai.Text{Text: string(data)}}}, nil
	}
}

// Middleware wraps a [Tool]. It builds the new tool with [Wrap]. It sees
// both Info and Run, so it can
// rename a tool, edit its description, deny a call, retry a run, or
// append to a result. Run receives ctx and the [ai.ToolCall] and
// nothing else. A layer with more state closes over the state or puts
// it in ctx.
type Middleware func(Tool) Tool

// Chain applies mws to t so that the first middleware is the
// outermost. Chain(t, a, b) runs a, then b, then t.
func Chain(t Tool, mws ...Middleware) Tool {
	for _, mw := range slices.Backward(mws) {
		t = mw(t)
	}
	return t
}
