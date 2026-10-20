package harness

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync/atomic"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/catalog"
	"github.com/sonnes/gopi/pkg/harness/permission"
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
// spec, the sources and the readers of skills, subagents, and tools, the
// permission rules, the prompt builder, the seeders, and the agent
// options. It builds nothing until a call of [Harness.New] or
// [Harness.Durable]. One harness can serve many sessions, and a call can
// add its own options, such as the [Dir] of one repository.
type Harness struct {
	cat        *catalog.Catalog
	spec       string
	store      session.Storer
	dir        string
	home       string
	sources    []Source
	sourcesSet bool
	skills     []SkillsReader
	agents     []AgentsReader
	tools      []ToolsReader
	builtin    Builtins
	box        sandbox.Sandbox
	system     string
	preload    []string
	prompt     PromptBuilder
	seeds      []Seeder
	agentOpts  []agent.Option
	policy     permission.Policy

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
	c.sources = slices.Clip(c.sources)
	c.skills = slices.Clip(c.skills)
	c.preload = slices.Clip(c.preload)
	c.agents = slices.Clip(c.agents)
	c.tools = slices.Clip(c.tools)
	c.seeds = slices.Clip(c.seeds)
	c.agentOpts = slices.Clip(c.agentOpts)
	c.policy.Allow = slices.Clip(c.policy.Allow)
	c.policy.Ask = slices.Clip(c.policy.Ask)
	c.policy.Deny = slices.Clip(c.policy.Deny)
	for _, opt := range opts {
		opt(&c)
	}
	return &c
}

// New assembles an agent over an unsaved session, with opts on top of
// the baseline. It reads the sources and the readers, makes the agent,
// and seeds its log. The agent builds the prompt before each model call.
// A subagent of an unsaved agent is unsaved too, so the task tool cannot
// resume it.
func (h *Harness) New(ctx context.Context, opts ...Option) (*agent.Agent, error) {
	asm, err := h.with(opts).assemble(ctx, "")
	if err != nil {
		return nil, err
	}
	lm, agentOpts, err := asm.prepare()
	if err != nil {
		return nil, err
	}

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
	asm, err := call.assemble(ctx, id)
	if err != nil {
		return nil, err
	}
	lm, agentOpts, err := asm.prepare()
	if err != nil {
		return nil, err
	}

	agentOpts = append(agentOpts, agent.Store(call.store))
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
	call.store = &loaded{
		Storer: call.store,
		id:     id,
		snap:   snap,
	}
	return call, stored, nil
}

// loaded is a store that answers the first load of one session with a
// snapshot that the harness already read. The agent then does not read
// the session a second time. Every other call goes to the store.
type loaded struct {
	session.Storer
	id   string
	snap session.Snapshot
	used atomic.Bool
}

// Load returns the snapshot of the harness on the first load of the
// session, and loads from the store after that.
func (s *loaded) Load(ctx context.Context, id string) (session.Snapshot, error) {
	if id == s.id && s.used.CompareAndSwap(false, true) {
		return s.snap, nil
	}
	return s.Storer.Load(ctx, id)
}
