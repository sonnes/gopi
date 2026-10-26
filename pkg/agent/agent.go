package agent

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/session"
	"github.com/sonnes/gopi/pkg/stream"
	"github.com/sonnes/gopi/pkg/tool"
)

// Agent runs a model with tools until the work is done. The
// [session.Log] of the agent stores each message of a run before its
// message_end, so a crash loses at most the message in flight. Each run
// changes its [State] one event at a time.
type Agent struct {
	lm     ai.LanguageModel
	cfg    config
	log    *session.Log
	queues *queues

	mu    sync.Mutex
	state State
	// closed is true after the first [Agent.Close].
	closed bool
}

// config holds the options of an agent that do not change after [New]
// or [Durable].
type config struct {
	// store is the store of a [Durable] agent.
	store session.Storer
	// name is the Author of each entry that the agent writes.
	name string
	// tools keeps the order of Toolset, then the search tool. The base
	// of a new log declares the tools that are not deferred, in the
	// same order.
	tools []tool.Tool
	// callOpts are the options of every model call.
	callOpts ai.Options
	// system builds the sections of the system prompt before each
	// model call. Nil means that the agent leaves the sections of the
	// log as they are.
	system SystemFunc
	hooks  []Hook
	// closers run once, at the first [Agent.Close].
	closers []func() error
}

// Option configures an [Agent].
type Option func(*Agent)

// Store sets the store of a [Durable] agent. Without Store, Durable
// keeps the session in a [session.MemoryStore], and the log ends with
// the process. [New] ignores Store, because an unsaved agent has
// no session ID.
func Store(s session.Storer) Option {
	return func(a *Agent) { a.cfg.store = s }
}

// Name sets the Author of each entry that the agent writes: the
// assistant messages and the tool results. Without Name, the Author is
// empty. [ai.Message.Model] says which model ran. Name says which
// agent.
func Name(s string) Option {
	return func(a *Agent) { a.cfg.name = s }
}

// Toolset sets the tools of the agent. The base of a new log declares
// the tools that are not deferred, and the agent runs the ones that the
// model calls. A session that [Durable] resumes keeps the tools of its
// base, and the agent declares each tool that Toolset added or removed
// since as a change. Each tool must have a unique name, because the
// model calls a tool by its name.
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
// result with the sections of the log. If they differ, the agent adds
// a system message with the change, so the log records each version of
// the prompt, and the start of the request stays the same
// for the cache. The change goes before the new messages of the turn.
//
// The first change of a new log is its base: the first message of the
// log, with every section and every tool. Each model call sends the base
// as [ai.SystemPrompt] and [ai.Tools], so a provider puts it in the
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
// leaves the sections of the log as they are.
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

// OnClose adds fn to the functions that [Agent.Close] runs. A builder of
// the agent uses it to release what the tools of the agent hold, such
// as the MCP sessions of the harness.
func OnClose(fn func() error) Option {
	return func(a *Agent) { a.cfg.closers = append(a.cfg.closers, fn) }
}

// Close runs each function of [OnClose] once, in order, and returns
// their errors joined. A later Close runs nothing and returns nil.
// Close does not stop a run. A tool that needs what a function
// released fails after Close.
func (a *Agent) Close() error {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return nil
	}
	a.closed = true
	a.mu.Unlock()

	var errs []error
	for _, fn := range a.cfg.closers {
		errs = append(errs, fn())
	}
	return errors.Join(errs...)
}

// New returns an agent over an unsaved log. The log lives in process
// memory and ends with the process. New does no I/O, so it
// returns no error and takes no context. If two tools have the same
// name, New panics, because that is a bug in the caller. A deferred
// tool counts, because a change can add it to a later call.
func New(lm ai.LanguageModel, opts ...Option) *Agent {
	a, err := newAgent(lm, opts)
	if err != nil {
		panic(err)
	}
	a.setLog(session.New())
	return a
}

// Durable returns an agent over the session id. The id is required.
// The [Store] option sets where the session lives. If id is unknown,
// Durable creates it. If id exists, Durable resumes it after its last
// entry, with the tools that the model can call there. To load the
// tools that a search loaded, Durable runs each search of the log
// again. Durable is the one constructor that does store I/O. If two
// tools have the same name, Durable returns an error before it touches
// the store.
func Durable(
	ctx context.Context,
	lm ai.LanguageModel,
	id string,
	opts ...Option,
) (*Agent, error) {
	a, err := newAgent(lm, opts)
	if err != nil {
		return nil, err
	}
	store := a.cfg.store
	if store == nil {
		store = session.NewMemoryStore()
	}
	log, err := session.Open(ctx, store, id)
	if err != nil {
		return nil, err
	}
	a.setLog(log)
	if err := a.reload(ctx, a.state.messages); err != nil {
		return nil, err
	}
	return a, nil
}

// newAgent applies opts and returns an agent with no log. It fails if
// two tools have the same name.
func newAgent(lm ai.LanguageModel, opts []Option) (*Agent, error) {
	a := &Agent{
		lm:     lm,
		queues: &queues{},
	}
	a.state.queues = a.queues
	for _, opt := range opts {
		opt(a)
	}
	a.cfg.tools = slices.Clip(a.cfg.tools)

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
			return nil, fmt.Errorf("agent: two tools are named %q", name)
		}
		seen[name] = true
	}
	return a, nil
}

// setLog makes l the log of the agent, and its messages the messages of
// the state.
func (a *Agent) setLog(l *session.Log) {
	a.log = l
	a.state.log = l.Writer()
	a.state.messages = l.Messages()
}

// Log returns the log of the agent: its entries, and the values of the
// session. To add entries, pass them to [Agent.Run], [Agent.Steer], or
// [Agent.FollowUp]. The agent reads the log only in [Durable]. So an
// entry that a caller appends to the log directly reaches the model
// only when Durable resumes the session. The log of [New] has no
// session ID, so no agent resumes it.
func (a *Agent) Log() *session.Log { return a.log }

// State returns a copy of the state of the agent. It is safe during a
// run. The copy shares the arrays and the queues of the agent, and
// costs no allocation. A caller must not change a message of the copy.
func (a *Agent) State() State {
	a.mu.Lock()
	defer a.mu.Unlock()
	// The clip caps the slices at their length, so an append of the
	// agent never shows in the copy.
	return a.state.Clip()
}

// Run starts a run with entries as its input, and returns the stream
// of the run. The loop stores the input in the log after the first
// turn_start. With no entries, the loop continues from the last
// message. An ephemeral entry reaches the model in this run, and the
// log does not keep it.
//
// Runs on one Agent are sequential. If a run is active, the returned
// stream fails with no event. Every error reaches the stream, so Run
// never panics.
//
// If the caller breaks out of the events or calls Wait, the run
// continues to its end and the stream drops the later events. The log
// still stores every message. To stop a run, cancel ctx.
func (a *Agent) Run(ctx context.Context, entries ...session.Entry) *Stream {
	return stream.New(func(push func(Event)) ([]ai.Message, error) {
		if err := a.start(push); err != nil {
			return nil, err
		}
		em := &emitter{
			agent: a,
			push:  push,
		}
		return a.run(ctx, entries, em)
	})
}

// emitter sends the events of one run, and stores its entries. It is
// the only way that a run changes the state. Tool goroutines call it
// too, so it sends one event at a time, and the order of the log, of
// Apply, and of the stream is one order.
type emitter struct {
	agent *Agent
	push  func(Event)
	order sync.Mutex
}

// emit applies e to the state, then pushes it.
func (em *emitter) emit(e Event) {
	em.order.Lock()
	defer em.order.Unlock()
	em.apply(e)
}

// commit appends entries to the log, then applies events to the state
// and pushes them. If the append fails, the state and the stream do not
// change, and commit returns the error. The append ignores the
// cancellation of ctx, so a run that stops keeps what it finished.
func (em *emitter) commit(ctx context.Context, entries []session.Entry, events ...Event) error {
	em.order.Lock()
	defer em.order.Unlock()
	if len(entries) > 0 {
		_, err := em.agent.log.Append(context.WithoutCancel(ctx), entries...)
		if err != nil {
			return err
		}
	}
	for _, e := range events {
		em.apply(e)
	}
	return nil
}

func (em *emitter) apply(e Event) {
	em.agent.mu.Lock()
	em.agent.state.Apply(e)
	em.agent.mu.Unlock()
	em.push(e)
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
