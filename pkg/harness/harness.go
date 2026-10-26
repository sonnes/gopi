package harness

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/catalog"
	"github.com/sonnes/gopi/pkg/def"
	"github.com/sonnes/gopi/pkg/harness/permission"
	"github.com/sonnes/gopi/pkg/mcp"
	"github.com/sonnes/gopi/pkg/sandbox"
	"github.com/sonnes/gopi/pkg/session"
)

// SessionModel is the session value that keeps the model spec of a
// session from [Harness.Durable]. A caller reads it to show the model of
// a session. The harness writes it; a caller does not.
var SessionModel = session.Value[string]{
	Namespace: "gopi.harness",
	Key:       "model",
}

// Harness holds the builders of an agent: the catalog and the model
// spec, the readers of instructions, skills, subagents, and tools, the
// permission rules, the prompt builder, the seeders, and the agent
// options. It builds nothing until a call of [Harness.New] or
// [Harness.Durable]. One harness can serve many sessions, and a call can
// add its own options, such as the [Dir] of one repository.
type Harness struct {
	cat          *catalog.Catalog
	spec         string
	store        session.Storer
	dir          string
	instructions []def.InstructionsReader
	skills       []def.SkillsReader
	agents       []def.AgentsReader
	tools        []ToolsReader
	builtin      Builtins
	box          sandbox.Sandbox
	system       string
	preload      []string
	prompt       PromptBuilder
	sections     agent.SystemFunc
	seeds        []Seeder
	agentOpts    []agent.Option
	policy       permission.Policy
	mcp          []agent.MCPServer

	subagentEvents func(callID string, e agent.Event)

	// child is the subagent of a call that the task tool makes. It is
	// nil in the call of a caller.
	child *childCall
	// mem keeps the sessions of [Harness.Durable] when no [Store] is
	// set, so a second call with the same id resumes the session.
	mem session.Storer
}

// New returns a harness with opts as the baseline of every call.
func New(opts ...Option) *Harness {
	h := &Harness{mem: session.NewMemoryStore()}
	return h.with(opts)
}

// with returns a copy of h with opts on top. The lists of the copy have
// no spare capacity, so an option that adds to a list of the copy does
// not write to a list of h. Calls on one harness can then run at the
// same time.
func (h *Harness) with(opts []Option) *Harness {
	c := *h
	c.instructions = slices.Clip(c.instructions)
	c.skills = slices.Clip(c.skills)
	c.preload = slices.Clip(c.preload)
	c.agents = slices.Clip(c.agents)
	c.tools = slices.Clip(c.tools)
	c.seeds = slices.Clip(c.seeds)
	c.agentOpts = slices.Clip(c.agentOpts)
	c.mcp = slices.Clip(c.mcp)
	c.policy.Allow = slices.Clip(c.policy.Allow)
	c.policy.Ask = slices.Clip(c.policy.Ask)
	c.policy.Deny = slices.Clip(c.policy.Deny)
	for _, opt := range opts {
		opt(&c)
	}
	return &c
}

// New assembles an agent over an unsaved session, with opts on top of
// the baseline. It reads the readers, makes the agent,
// and seeds its log. The agent builds the prompt before each model call.
// A subagent of an unsaved agent is unsaved too, so the task tool cannot
// resume it.
func (h *Harness) New(ctx context.Context, opts ...Option) (*agent.Agent, error) {
	call, closeMCP, err := h.with(opts).connectMCP(ctx)
	if err != nil {
		return nil, err
	}
	a, err := call.newAgent(ctx, closeMCP)
	if err != nil {
		return nil, errors.Join(err, closeMCP())
	}
	return a, nil
}

// newAgent is [Harness.New] after the MCP servers connect. The agent
// runs closeMCP on [agent.Agent.Close].
func (h *Harness) newAgent(ctx context.Context, closeMCP func() error) (*agent.Agent, error) {
	asm, err := h.assemble(ctx, "")
	if err != nil {
		return nil, err
	}
	lm, agentOpts, err := asm.prepare()
	if err != nil {
		return nil, err
	}

	agentOpts = append(agentOpts, agent.OnClose(closeMCP))
	a := agent.New(lm, agentOpts...)
	if err := asm.seed(ctx, a.Log()); err != nil {
		return nil, err
	}
	return a, nil
}

// Durable assembles an agent over the session id, with opts on top of
// the baseline. If id is unknown, Durable creates the session. If id
// exists, Durable resumes it.
//
// The session keeps its model spec. The spec is the [Model] of the
// call, then the spec that the session keeps, then the Model of [New].
// Only a session with no entries gets the seed.
func (h *Harness) Durable(ctx context.Context, id string, opts ...Option) (*agent.Agent, error) {
	call, stored, err := h.resolve(ctx, id, opts)
	if err != nil {
		return nil, err
	}
	call, closeMCP, err := call.connectMCP(ctx)
	if err != nil {
		return nil, err
	}
	a, err := call.durable(ctx, id, stored, closeMCP)
	if err != nil {
		return nil, errors.Join(err, closeMCP())
	}
	return a, nil
}

// durable is [Harness.Durable] after the MCP servers connect. stored is
// the spec that the session keeps. The agent runs closeMCP on
// [agent.Agent.Close].
func (h *Harness) durable(ctx context.Context, id, stored string, closeMCP func() error) (*agent.Agent, error) {
	call := h
	asm, err := call.assemble(ctx, id)
	if err != nil {
		return nil, err
	}
	lm, agentOpts, err := asm.prepare()
	if err != nil {
		return nil, err
	}

	agentOpts = append(
		agentOpts,
		agent.Store(call.store),
		agent.OnClose(closeMCP),
	)
	a, err := agent.Durable(ctx, lm, id, agentOpts...)
	if err != nil {
		return nil, err
	}
	w := a.Log().Writer()
	if call.spec != stored {
		if err := SessionModel.Set(ctx, w, call.spec); err != nil {
			return nil, err
		}
	}
	if call.child != nil && len(a.Log().Entries()) == 0 {
		if err := Parent.Set(ctx, w, call.child.parent); err != nil {
			return nil, err
		}
	}
	if err := asm.seed(ctx, a.Log()); err != nil {
		return nil, err
	}
	return a, nil
}

// connectMCP connects to the servers of [MCP], and returns a copy of h
// with their tools and with no servers, and the function that closes
// the sessions. The task tool builds a subagent from the copy, so a
// subagent shares the sessions of its parent and connects to nothing.
// If a server does not connect, connectMCP closes the others and fails.
func (h *Harness) connectMCP(ctx context.Context) (*Harness, func() error, error) {
	noop := func() error { return nil }
	if len(h.mcp) == 0 {
		return h, noop, nil
	}
	tools, closeMCP, err := mcp.Connect(ctx, h.mcp, os.Environ())
	if err != nil {
		return nil, nil, errors.Join(fmt.Errorf("harness: %w", err), closeMCP())
	}
	call := h.with([]Option{Toolset(Tools(tools...))})
	call.mcp = nil
	return call, closeMCP, nil
}

// resolve returns the harness of one call of [Harness.Durable]: a
// copy of h with opts on top, and with the spec and the store of the
// session id. It also returns the spec that the session keeps, or ""
// if there is none.
//
// It reads the session once to learn its model spec. It reads the
// store directly, so that it does not create a session before the
// model is known. If the session exists, the store of the copy gives
// the snapshot that resolve read to the first load of the session.
func (h *Harness) resolve(ctx context.Context, id string, opts []Option) (*Harness, string, error) {
	if id == "" {
		return nil, "", errors.New("harness: the session id is required")
	}
	call := h.with(opts)
	if call.store == nil {
		call.store = h.mem
	}

	snap, err := call.store.Load(ctx, id)
	switch {
	case errors.Is(err, session.ErrNotFound):
		return call, "", nil
	case err != nil:
		return nil, "", err
	}

	stored, _, err := SessionModel.Read(snap)
	if err != nil {
		return nil, "", fmt.Errorf("harness: session %s: %w", id, err)
	}
	// A spec in opts changes the model of the session. The spec of the
	// baseline does not.
	var only Harness
	callSpec := only.with(opts).spec
	if callSpec == "" && stored != "" {
		call.spec = stored
	}
	call.store = session.Preloaded(call.store, id, snap)
	return call, stored, nil
}
