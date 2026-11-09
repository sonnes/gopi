package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/session"
)

// Hook changes the loop at the points that it implements. Each point
// is its own interface: [BeforeCallHook], [AfterCallHook],
// [BeforeToolHook], [AfterToolHook], [AfterTurnHook], and
// [BeforeStopHook]. [BeforeTool] and the other constructors of one
// point return a hook from a function. A hook at more than one point
// embeds [BaseHook] and implements the points that it needs. Each point
// gets a copy of the [State] and returns the value that the loop uses.
//
// A point is an interface, so the code that runs a hook can see the
// points that the hook uses. A driver of an agent that cannot call a
// point rejects a hook that implements it. [New], [Durable], and each
// driver reject a hook that implements no point, because such a hook
// has a misspelled method.
//
// The loop calls each point of each hook, in the order of
// registration. A point gets the value that the hook before it
// returned.
//
// A hook reads the session from [State.Log], and sets values through
// it. A hook adds entries only through AfterTurn and BeforeStop, or
// queues them with [State.Steer] and [State.FollowUp], so the agent
// stores each entry and sends its events in one order.
type Hook interface {
	// nop makes every Hook embed BaseHook, so a value that is not a
	// hook cannot go in a list of hooks.
	nop()
}

// BeforeCallHook changes one model call. See [BeforeCall].
type BeforeCallHook interface {
	BeforeCall(ctx context.Context, s State, r Request) (Request, error)
}

// AfterCallHook changes the message of one model call. See [AfterCall].
type AfterCallHook interface {
	AfterCall(ctx context.Context, s State, r Request, m ai.Message) (ai.Message, error)
}

// BeforeToolHook changes or blocks one tool call. See [BeforeTool].
type BeforeToolHook interface {
	BeforeTool(ctx context.Context, s State, c ai.ToolCall) (ai.ToolCall, error)
}

// AfterToolHook changes the result of one tool call. See [AfterTool].
type AfterToolHook interface {
	AfterTool(ctx context.Context, s State, c ai.ToolCall, r ai.ToolResult) (ai.ToolResult, error)
}

// AfterTurnHook adds entries after a turn. See [AfterTurn].
type AfterTurnHook interface {
	AfterTurn(ctx context.Context, s State) ([]session.Entry, error)
}

// BeforeStopHook continues a run that would stop. See [BeforeStop].
type BeforeStopHook interface {
	BeforeStop(ctx context.Context, s State) ([]session.Entry, error)
}

// BaseHook is a [Hook] that implements no point. A hook embeds it.
type BaseHook struct{}

func (BaseHook) nop() {}

// Hooks adds hs to the hooks of the agent. Each hook takes its place
// in each point that it implements, in the order of registration.
func Hooks(hs ...Hook) Option {
	return func(a *Agent) { a.cfg.hooks = append(a.cfg.hooks, hs...) }
}

// Request is one model call, as a [BeforeCallFunc] sees it.
//
// Messages are the messages of the state after the base. The base is
// in Options, as [ai.SystemPrompt] and [ai.Tools]. An append to Messages does not change the state. A hook
// that changes a message must change a copy of it.
//
// A provider reads the sections and the tool changes from the system
// messages. If a hook removes a system message, the request does not
// have its sections and its tool changes.
type Request struct {
	Messages []ai.Message
	// Options are the options of the call, with the [ai.Tools]. A hook
	// adds an option with [ai.JoinOptions]. A later value for the same
	// option wins.
	Options ai.Options
}

// BeforeCallFunc changes one model call. See [BeforeCall].
type BeforeCallFunc func(ctx context.Context, s State, r Request) (Request, error)

type beforeCall struct {
	BaseHook
	fn BeforeCallFunc
}

func (h beforeCall) BeforeCall(ctx context.Context, s State, r Request) (Request, error) {
	return h.fn(ctx, s, r)
}

// BeforeCall returns a hook that runs before each model call. The state is
// the state before the call, and its turn is the turn of the call. The
// agent sends the request that the last hook returns. The state and
// the log do not change, and no event is sent. If a hook returns an
// error, the run fails before the model call.
func BeforeCall(fn BeforeCallFunc) Hook {
	return beforeCall{fn: fn}
}

// AfterCallFunc changes the message of one model call. See [AfterCall].
type AfterCallFunc func(
	ctx context.Context,
	s State,
	r Request,
	m ai.Message,
) (ai.Message, error)

type afterCall struct {
	BaseHook
	fn AfterCallFunc
}

func (h afterCall) AfterCall(
	ctx context.Context,
	s State,
	r Request,
	m ai.Message,
) (ai.Message, error) {
	return h.fn(ctx, s, r, m)
}

// AfterCall returns a hook that runs after each model call that ends
// without an error. The request is the request that the model read.
// The message is the final message of the model. The hook runs before
// message_end, so message_end and the log have the message that the
// last hook returns. The loop runs the tool calls of that message.
//
// The message_update events carry the message of the model before the
// hooks. If the stream fails, no hook runs, and the agent keeps the
// partial message.
//
// If a hook returns an error, the run fails, the hooks after it do not
// run, and the log does not keep the message.
func AfterCall(fn AfterCallFunc) Hook {
	return afterCall{fn: fn}
}

// BeforeToolFunc changes or blocks one tool call. See [BeforeTool].
type BeforeToolFunc func(ctx context.Context, s State, c ai.ToolCall) (ai.ToolCall, error)

type beforeTool struct {
	BaseHook
	fn BeforeToolFunc
}

func (h beforeTool) BeforeTool(ctx context.Context, s State, c ai.ToolCall) (ai.ToolCall, error) {
	return h.fn(ctx, s, c)
}

// BeforeTool returns a hook that runs before each tool call. The state
// holds the assistant message of the call, and the results of the calls
// before it if the calls run in order. The agent runs the call that the
// last hook returns. If a hook changes the name, the agent finds the
// tool again. The ID of the call stays, so the result answers the call
// of the model.
//
// The hooks run after the agent finds the tool. A call to an unknown
// tool gets its error result, and no hook runs.
//
// For the calls of one message, the hooks run one call at a time, in
// call order, after the tool_execution_start of the call. Then the
// calls run. The tool events and [State.Pending] keep the call of the
// model, because the start event comes before the hooks.
//
// If a hook returns an error, the call does not run, the hooks after
// it do not run, and the text of the error is the error result. [Deny]
// gives a reason to the block. [Ask] asks the function of [Approve]
// instead, as [RunBeforeTool] says. If ctx is done, the run fails, and
// no hook runs for the calls after it.
func BeforeTool(fn BeforeToolFunc) Hook {
	return beforeTool{fn: fn}
}

// AfterToolFunc changes the result of one tool call. See [AfterTool].
type AfterToolFunc func(
	ctx context.Context,
	s State,
	c ai.ToolCall,
	r ai.ToolResult,
) (ai.ToolResult, error)

type afterTool struct {
	BaseHook
	fn AfterToolFunc
}

func (h afterTool) AfterTool(
	ctx context.Context,
	s State,
	c ai.ToolCall,
	r ai.ToolResult,
) (ai.ToolResult, error) {
	return h.fn(ctx, s, c, r)
}

// AfterTool returns a hook that runs after each tool call, before
// tool_execution_end, so the event has the final result. The call is
// the call that ran, with the changes of the BeforeTool hooks.
// [State.Pending] still holds the call as the model sent it. The agent
// stores the result that the last hook returns as the call ends.
//
// The calls of one message run at the same time, and the hooks run in
// the goroutine of each call. So a hook must be safe for concurrent
// use.
//
// If a hook returns an error, the result is an error result with the
// text of the error, and the hooks after it do not run. If ctx is done,
// the run fails.
func AfterTool(fn AfterToolFunc) Hook {
	return afterTool{fn: fn}
}

// AfterTurnFunc reads a finished turn and returns entries to add.
// See [AfterTurn].
type AfterTurnFunc func(ctx context.Context, s State) ([]session.Entry, error)

type afterTurn struct {
	BaseHook
	fn AfterTurnFunc
}

func (h afterTurn) AfterTurn(ctx context.Context, s State) ([]session.Entry, error) {
	return h.fn(ctx, s)
}

// AfterTurn returns a hook that runs after each turn, after turn_end.
//
// After each hook, the agent stores the entries of the hook in the
// log. Each message entry gets message_start and message_end, and the
// next hook sees it in its state. A custom entry, from
// [session.Custom], gets no event, and the model does not read it. An
// ephemeral entry reaches the state and not the store. The entries do
// not start another turn. If the run continues, the next model call
// reads them. If not, the next run reads them, except an ephemeral
// entry. If a hook returns an
// error, the run fails.
func AfterTurn(fn AfterTurnFunc) Hook {
	return afterTurn{fn: fn}
}

// BeforeStopFunc continues a run that would stop. See [BeforeStop].
type BeforeStopFunc func(ctx context.Context, s State) ([]session.Entry, error)

type beforeStop struct {
	BaseHook
	fn BeforeStopFunc
}

func (h beforeStop) BeforeStop(ctx context.Context, s State) ([]session.Entry, error) {
	return h.fn(ctx, s)
}

// BeforeStop returns a hook that runs when the run would stop: the turn
// made no tool calls, and the queues are empty. The first hook that
// returns entries wins, and the hooks after it do not run.
//
// The agent starts the next turn, and stores the entries after its
// turn_start, as AfterTurn does. If no hook returns entries, the run
// stops. If a hook returns an error, the run fails.
func BeforeStop(fn BeforeStopFunc) Hook {
	return beforeStop{fn: fn}
}

// DenyError blocks a tool call. A [BeforeToolFunc] returns one through
// [Deny]. The model reads the reason as an error result.
type DenyError struct {
	// Reason is the text of the error result. If it is empty, the text
	// is "the tool call was blocked".
	Reason string
}

func (e *DenyError) Error() string {
	if e.Reason == "" {
		return "the tool call was blocked"
	}
	return e.Reason
}

// Deny returns an error that blocks a tool call:
//
//	return c, agent.Deny("write outside the workspace")
func Deny(reason string) error {
	return &DenyError{Reason: reason}
}

// AskError asks a person about a tool call before it runs. A
// [BeforeToolFunc] returns one through [Ask]. Each agent gives the
// call to its approve function before the call runs, as
// [RunBeforeTool] does.
type AskError struct {
	// Reason tells the person why the call asks. If it is empty, the
	// text is "the tool call needs a person".
	Reason string
}

func (e *AskError) Error() string {
	if e.Reason == "" {
		return "the tool call needs a person"
	}
	return e.Reason
}

// Ask returns an error that asks a person about a tool call:
//
//	return c, agent.Ask("the rule Bash(git push *) asks")
func Ask(reason string) error {
	return &AskError{Reason: reason}
}

// RunBeforeTool runs the BeforeTool point of each hook on c, in order.
// Each hook gets a new state from state, and the call that the hook
// before it returned. RunBeforeTool returns the call that runs. The
// loop and each agent CLI run the BeforeTool hooks with it.
//
// If a hook returns an [AskError], RunBeforeTool asks approve with the
// call and the reason of the error. If the person allows the call, the
// call takes the input of the decision, and the next hook runs. A
// denied call gives a [DenyError] with the message of the decision. A
// nil approve also denies the call.
//
// On an error, the hooks after it do not run. RunBeforeTool then
// returns the call that the failed hook got, and the error.
func RunBeforeTool(
	ctx context.Context,
	hooks []Hook,
	state func() State,
	approve ApproveFunc,
	c ai.ToolCall,
) (ai.ToolCall, error) {
	for _, h := range hooks {
		bt, ok := h.(BeforeToolHook)
		if !ok {
			continue
		}
		next, err := bt.BeforeTool(ctx, state(), c)
		if ask, ok := errors.AsType[*AskError](err); ok {
			next, err = answerAsk(ctx, approve, c, ask)
		}
		if err != nil {
			return c, err
		}
		c = next
	}
	return c, nil
}

// answerAsk asks approve about the call c, which ask asks about. It
// returns c with the input of the decision, or the error that blocks c.
func answerAsk(ctx context.Context, approve ApproveFunc, c ai.ToolCall, ask *AskError) (ai.ToolCall, error) {
	if approve == nil {
		reason := fmt.Sprintf("the tool call %q needs a person, and the agent has no approve function", c.Name)
		return c, Deny(reason)
	}
	req := ApprovalRequest{
		Call:   c,
		Reason: ask.Reason,
	}
	d, err := approve(ctx, req)
	if err != nil {
		return c, fmt.Errorf("agent: approve the tool call %q: %w", c.Name, err)
	}
	if !d.Allow {
		reason := d.Message
		if reason == "" {
			reason = fmt.Sprintf("the person denied the tool call %q", c.Name)
		}
		return c, Deny(reason)
	}
	if d.Input != nil {
		c.Input = d.Input
	}
	return c, nil
}

// RunAfterTool runs the AfterTool point of each hook on the result r of
// the call c, in order. Each hook gets a new state from state, and the
// result that the hook before it returned. RunAfterTool returns the last
// result. If a hook returns an error, the hooks after it do not run,
// and RunAfterTool returns the error.
func RunAfterTool(
	ctx context.Context,
	hooks []Hook,
	state func() State,
	c ai.ToolCall,
	r ai.ToolResult,
) (ai.ToolResult, error) {
	for _, h := range hooks {
		at, ok := h.(AfterToolHook)
		if !ok {
			continue
		}
		next, err := at.AfterTool(ctx, state(), c, r)
		if err != nil {
			return r, err
		}
		r = next
	}
	return r, nil
}

// RunBeforeStop runs the BeforeStop point of each hook, in order, until
// a hook returns entries. It returns the entries of that hook, or nil if
// the run stops. Each hook gets a new state from state. If a hook
// returns an error, the hooks after it do not run.
func RunBeforeStop(ctx context.Context, hooks []Hook, state func() State) ([]session.Entry, error) {
	for _, h := range hooks {
		bs, ok := h.(BeforeStopHook)
		if !ok {
			continue
		}
		entries, err := bs.BeforeStop(ctx, state())
		if err != nil {
			return nil, err
		}
		if len(entries) > 0 {
			return entries, nil
		}
	}
	return nil, nil
}
