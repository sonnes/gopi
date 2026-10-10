package agent

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/stream"
	"github.com/sonnes/gopi/pkg/tool"
)

// Agent runs a model with tools until the work is done. It keeps its
// messages in memory, and each run changes its [State] one event at a
// time.
type Agent struct {
	lm  ai.LanguageModel
	cfg config

	mu    sync.Mutex
	state State
}

// config holds the options of an agent that do not change after
// [New]. The [History] option writes to the state instead, because the
// history grows with each run.
type config struct {
	// tools keeps the order of Toolset, then the search tool. The base
	// of a new history declares the tools that are not deferred, in the
	// same order.
	tools []tool.Tool
	// callOpts are the options of every model call.
	callOpts ai.Options
	// system builds the sections of the system prompt before each
	// model call. Nil means that the agent leaves the sections of the
	// history as they are.
	system SystemFunc
	hooks  []Hook
}

// Option configures an [Agent].
type Option func(*Agent)

// History sets the messages that the agent starts with, such as a
// conversation that the caller saved. If the first message is a system
// message, it is the base of the prompt. The messages of each run stay
// in the agent for the next run.
func History(msgs ...ai.Message) Option {
	return func(a *Agent) { a.state.messages = append(a.state.messages, msgs...) }
}

// Toolset sets the tools of the agent. The base of a new history
// declares the tools that are not deferred, and the agent runs the ones
// that the model calls. A history from [History] keeps the tools of its
// base, and the agent declares each tool that Toolset added or removed
// since as a change. Each tool must have a unique name, because the model
// calls a tool by its name.
//
// A deferred tool, from [tool.Deferred] or [tool.Defer], costs no
// tokens until the model loads it. If a tool is deferred, the agent
// adds a search tool, named "tool_search", after the tools. Its
// description names each deferred tool, and the search loads the tools
// that a query names, or the best matches of its keywords.
func Toolset(tools ...tool.Tool) Option {
	return func(a *Agent) { a.cfg.tools = append(a.cfg.tools, tools...) }
}

// SystemFunc returns the sections of the system prompt, in order. See
// [System].
type SystemFunc func(ctx context.Context) ([]ai.Section, error)

// System sets the function that builds the system prompt, as a list of
// sections. The agent calls fn before each model call and compares the
// result with the sections of the history. If they differ, the agent
// adds a system message with the change, so the history records each
// version of the prompt, and the start of the request stays the same
// for the cache. The change goes before the new messages of the turn.
//
// The first change of a new history is its base: the first message of
// the history, with every section and every tool. Each model call sends the
// base as [ai.SystemPrompt] and [ai.Tools], so a provider puts it in the
// system prompt and the tool list of its API. Each later change goes in
// its place in the messages.
//
// The change is as small as the order allows. A changed section is
// set again in its place, a missing section is removed, and a new
// section goes at the end. If that does not give the order of fn, the
// change removes every section and sets them all again.
//
// A prompt that never changes is one section:
//
//	agent.System(func(context.Context) ([]ai.Section, error) {
//		return []ai.Section{{Name: "base", Text: "Answer in one sentence."}}, nil
//	})
//
// If fn returns an error, the run fails. Without System, the agent
// leaves the sections of the history as they are.
func System(fn SystemFunc) Option {
	return func(a *Agent) { a.cfg.system = fn }
}

// MaxTokens caps the output tokens of every model call. See
// [ai.MaxTokens]. Without MaxTokens, the provider uses
// [ai.Model.MaxTokens], then its own default.
func MaxTokens(n int) Option {
	return func(a *Agent) { a.cfg.callOpts = ai.JoinOptions(a.cfg.callOpts, ai.MaxTokens(n)) }
}

// Thinking sets the thinking level of every model call. See
// [ai.Thinking]. Without Thinking, the provider uses the default of the
// model.
func Thinking(level ai.ThinkingLevel) Option {
	return func(a *Agent) { a.cfg.callOpts = ai.JoinOptions(a.cfg.callOpts, ai.Thinking(level)) }
}

// New returns an agent. New does no I/O, so it returns no error and
// takes no context. If two tools have the same name, New panics,
// because that is a bug in the caller. A deferred tool counts, because
// a change can add it to a later call.
func New(lm ai.LanguageModel, opts ...Option) *Agent {
	a := &Agent{lm: lm}
	for _, opt := range opts {
		opt(a)
	}
	a.cfg.tools = slices.Clip(a.cfg.tools)
	a.state.messages = slices.Clip(a.state.messages)

	var deferred []tool.Tool
	for _, t := range a.cfg.tools {
		if t.Info().Deferred {
			deferred = append(deferred, t)
		}
	}
	if len(deferred) > 0 {
		a.cfg.tools = append(a.cfg.tools, newSearchTool(a, deferred))
	}

	seen := make(map[string]bool, len(a.cfg.tools))
	for _, t := range a.cfg.tools {
		name := t.Info().Name
		if seen[name] {
			panic(fmt.Errorf("agent: two tools are named %q", name))
		}
		seen[name] = true
	}
	return a
}

// State returns a copy of the state of the agent. It is safe during a
// run. The copy shares the arrays of the agent and costs no
// allocation. A caller must not change a message of the copy.
func (a *Agent) State() State {
	a.mu.Lock()
	defer a.mu.Unlock()
	// The clip caps the messages at their length, so an append of the
	// agent never shows in the copy.
	s := a.state
	s.messages = slices.Clip(s.messages)
	return s
}

// Run adds msgs to the history and runs the loop. It returns the
// stream of the run. With no messages, the loop continues from the last
// message.
//
// Runs on one Agent are sequential. If a run is active, the returned
// stream fails with no event. Every error reaches the stream, so Run
// never panics.
//
// If the caller stops reading the stream, the run continues to its end
// and the stream drops its events. To stop a run, cancel ctx.
func (a *Agent) Run(ctx context.Context, msgs ...ai.Message) *Stream {
	return stream.New(func(push func(Event)) ([]ai.Message, error) {
		if err := a.start(push); err != nil {
			return nil, err
		}
		return a.run(ctx, msgs, a.emitter(push))
	})
}

// emitter returns the function that sends each event of a run. It
// applies the event to the state, then pushes it. It is the only way
// that a run changes the state. Tool goroutines call it
// too, so it sends one event at a time, and the order of the stream is
// the order of Apply.
func (a *Agent) emitter(push func(Event)) func(Event) {
	var order sync.Mutex
	return func(e Event) {
		order.Lock()
		defer order.Unlock()
		a.mu.Lock()
		a.state.Apply(e)
		a.mu.Unlock()
		push(e)
	}
}

// start sends agent_start, or fails if a run is active. The check and
// the change of the state are one step under the lock, so two runs
// never start together. The agent_end of the run ends it.
func (a *Agent) start(push func(Event)) error {
	start := EventAgentStart{}
	a.mu.Lock()
	if a.state.running {
		a.mu.Unlock()
		return errors.New("agent: a run is active")
	}
	a.state.Apply(start)
	a.mu.Unlock()
	push(start)
	return nil
}
